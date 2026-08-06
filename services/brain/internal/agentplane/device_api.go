// Device pairing + device-token management HTTP endpoints (Runtime v3 R6.1 / D5).
//
// 流程：
//   1. Mac daemon  POST /v1/agent/devices/pair/request  (UNAUTH) → {pairing_id, code, pairing_secret}
//   2. 用户(已登录) POST /v1/agent/devices/pair/approve  (AUTH)   {code} → 绑定 user
//   3. Mac daemon  POST /v1/agent/devices/pair/poll     (UNAUTH+secret) → 命中 approved 返 device_token
//   4. 管理：GET /v1/agent/devices (AUTH) 列表；DELETE /v1/agent/devices/{id} (AUTH) 吊销
//
// pair/request + pair/poll 是 UNAUTH（daemon 配对前还没 token）；靠短 TTL +
// pairing_secret + janitor GC 控面。approve/list/revoke 需用户鉴权。

package agentplane

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

// MountDeviceRoutes 挂设备配对 + 管理路由。pair/request + pair/poll 不过 requireAuth。
func (s *Server) MountDeviceRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST   /v1/agent/devices/pair/request", s.handlePairRequest)
	mux.HandleFunc("POST   /v1/agent/devices/pair/poll", s.handlePairPoll)
	mux.HandleFunc("POST   /v1/agent/devices/pair/approve", s.requireAuth(s.handlePairApprove))
	mux.HandleFunc("GET    /v1/agent/devices", s.requireAuth(s.handleListDevices))
	mux.HandleFunc("PATCH  /v1/agent/devices/{id}", s.requireAuth(s.handleUpdateDevicePolicy))
	mux.HandleFunc("DELETE /v1/agent/devices/{id}", s.requireAuth(s.handleRevokeDevice))
}

type pairRequestReq struct {
	MachineName string `json:"machine_name"`
	OsArch      string `json:"os_arch,omitempty"`
	WorkerKind  string `json:"worker_kind,omitempty"`
}

func (s *Server) handlePairRequest(w http.ResponseWriter, r *http.Request) {
	var req pairRequestReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	if strings.TrimSpace(req.MachineName) == "" {
		writeErr(w, http.StatusBadRequest, "bad_machine_name", "machine_name required")
		return
	}
	p, err := s.Store.CreatePairing(r.Context(), req.MachineName, req.OsArch, req.WorkerKind)
	if err != nil {
		s.serverErr(w, "create pairing", err)
		return
	}
	s.Logger.Info("agentplane: pairing requested",
		"pairing_id", p.PairingID, "machine", req.MachineName)
	writeJSON(w, http.StatusOK, map[string]any{
		"pairing_id":     p.PairingID.String(),
		"code":           p.Code,
		"pairing_secret": p.PairingSecret,
		"expires_at":     p.ExpiresAt.UTC().Format(time.RFC3339),
	})
}

type pairApproveReq struct {
	Code string `json:"code"`
}

func (s *Server) handlePairApprove(w http.ResponseWriter, r *http.Request) {
	uid := mustUserID(r)
	var req pairApproveReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	code := strings.TrimSpace(req.Code)
	if code == "" {
		writeErr(w, http.StatusBadRequest, "bad_code", "code required")
		return
	}
	machineName, err := s.Store.ApprovePairing(r.Context(), uid, code)
	if errors.Is(err, ErrNotFound) {
		// 错码 / 过期 / 已批准 —— 统一 404，不泄漏哪种（防探测）。
		writeErr(w, http.StatusNotFound, "invalid_code", "pairing code invalid or expired")
		return
	}
	if err != nil {
		s.serverErr(w, "approve pairing", err)
		return
	}
	s.Logger.Info("agentplane: pairing approved", "user_id", uid, "machine", machineName)
	writeJSON(w, http.StatusOK, map[string]any{"machine_name": machineName})
}

type pairPollReq struct {
	PairingID     string `json:"pairing_id"`
	PairingSecret string `json:"pairing_secret"`
}

func (s *Server) handlePairPoll(w http.ResponseWriter, r *http.Request) {
	var req pairPollReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	pid, err := uuid.Parse(req.PairingID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_pairing_id", "")
		return
	}
	token, dev, status, err := s.Store.PollPairing(r.Context(), pid, req.PairingSecret)
	switch {
	case errors.Is(err, ErrPairingSecret):
		writeErr(w, http.StatusForbidden, "bad_pairing_secret", "")
		return
	case errors.Is(err, ErrNotFound):
		writeErr(w, http.StatusNotFound, "pairing_not_found", "")
		return
	case err != nil:
		s.serverErr(w, "poll pairing", err)
		return
	}
	switch status {
	case "pending":
		writeJSON(w, http.StatusAccepted, map[string]any{"status": "pending"})
	case "approved":
		// 明文 token 只此一次返回。
		writeJSON(w, http.StatusOK, map[string]any{
			"status":       "approved",
			"device_token": token,
			"device_id":    dev.DeviceID.String(),
		})
	case "expired":
		writeErr(w, http.StatusGone, "pairing_expired", "")
	case "consumed":
		writeErr(w, http.StatusConflict, "pairing_consumed", "already paired")
	default:
		writeErr(w, http.StatusConflict, "pairing_"+status, "")
	}
}

func (s *Server) handleListDevices(w http.ResponseWriter, r *http.Request) {
	uid := mustUserID(r)
	devices, err := s.Store.ListDevices(r.Context(), uid)
	if err != nil {
		s.serverErr(w, "list devices", err)
		return
	}
	out := make([]map[string]any, 0, len(devices))
	for _, d := range devices {
		m := map[string]any{
			"device_id":   d.DeviceID.String(),
			"name":        d.Name,
			"prefix":      d.Prefix,
			"tool_policy": d.ToolPolicy,
			"online":      d.Online, // R6.4：最新 environment state=='online'
			"created_at":  d.CreatedAt.UTC().Format(time.RFC3339),
			"expires_at":  d.ExpiresAt.UTC().Format(time.RFC3339),
			"revoked":     d.RevokedAt != nil,
		}
		if d.LastSeenAt != nil {
			m["last_seen_at"] = d.LastSeenAt.UTC().Format(time.RFC3339)
		}
		if d.LastUsedAt != nil {
			m["last_used_at"] = d.LastUsedAt.UTC().Format(time.RFC3339)
		}
		if d.RevokedAt != nil {
			m["revoked_at"] = d.RevokedAt.UTC().Format(time.RFC3339)
		}
		out = append(out, m)
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": out})
}

type updateDevicePolicyReq struct {
	ToolPolicy string `json:"tool_policy"`
}

// handleUpdateDevicePolicy 改某设备的 tool_policy preset（R6.3 / D7）。属主校验
// 在 store 层。preset 非法 → 400。
func (s *Server) handleUpdateDevicePolicy(w http.ResponseWriter, r *http.Request) {
	uid := mustUserID(r)
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_id", "")
		return
	}
	var req updateDevicePolicyReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	if !ToolPolicyPresets[req.ToolPolicy] {
		writeErr(w, http.StatusBadRequest, "bad_tool_policy",
			"tool_policy must be one of: readonly, workspace-write, full")
		return
	}
	if err := s.Store.SetDeviceToolPolicy(r.Context(), uid, id, req.ToolPolicy); err != nil {
		s.handleStoreErr(w, err)
		return
	}
	s.Logger.Info("agentplane: device tool_policy updated",
		"user_id", uid, "device_id", id, "tool_policy", req.ToolPolicy)
	writeJSON(w, http.StatusOK, map[string]any{"device_id": id.String(), "tool_policy": req.ToolPolicy})
}

func (s *Server) handleRevokeDevice(w http.ResponseWriter, r *http.Request) {
	uid := mustUserID(r)
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_id", "")
		return
	}
	if err := s.Store.RevokeDevice(r.Context(), uid, id); err != nil {
		s.handleStoreErr(w, err)
		return
	}
	s.Logger.Info("agentplane: device revoked", "user_id", uid, "device_id", id)
	w.WriteHeader(http.StatusNoContent)
}

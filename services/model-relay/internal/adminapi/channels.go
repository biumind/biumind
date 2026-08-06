package adminapi

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/biumind/biumind/services/model-relay/internal/registry"
)

// GET /v1/admin/channels?model_id=...&credential_id=...&status=active
func (s *Server) handleListChannels(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := registry.ChannelFilter{
		Status: registry.EntityStatus(q.Get("status")),
	}
	if mid := q.Get("model_id"); mid != "" {
		id, err := uuid.Parse(mid)
		if err != nil {
			writeErrorField(w, http.StatusBadRequest, "invalid_uuid",
				"model_id must be a UUID", "model_id")
			return
		}
		f.ModelID = id
	}
	if cid := q.Get("credential_id"); cid != "" {
		id, err := uuid.Parse(cid)
		if err != nil {
			writeErrorField(w, http.StatusBadRequest, "invalid_uuid",
				"credential_id must be a UUID", "credential_id")
			return
		}
		f.CredentialID = id
	}
	items, err := s.Store.Channels.List(r.Context(), f)
	if err != nil {
		translateRegistryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": items, "total": len(items),
	})
}

func (s *Server) handleGetChannel(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	got, err := s.Store.Channels.Get(r.Context(), id)
	if err != nil {
		translateRegistryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, got)
}

type channelRequest struct {
	ModelID       string                `json:"model_id"`
	CredentialID  string                `json:"credential_id"`
	UpstreamModel string                `json:"upstream_model"`
	Priority      int                   `json:"priority"`
	Weight        int                   `json:"weight"`
	RPMLimit      int                   `json:"rpm_limit"`
	TPMLimit      int                   `json:"tpm_limit"`
	Status        registry.EntityStatus `json:"status"`
	Extra         map[string]any        `json:"extra"`
}

func (req channelRequest) toInput() (registry.ChannelInput, *ErrorBody) {
	mid, err := uuid.Parse(req.ModelID)
	if err != nil {
		return registry.ChannelInput{}, &ErrorBody{
			Code: "invalid_uuid", Message: "model_id required", Field: "model_id",
		}
	}
	cid, err := uuid.Parse(req.CredentialID)
	if err != nil {
		return registry.ChannelInput{}, &ErrorBody{
			Code: "invalid_uuid", Message: "credential_id required", Field: "credential_id",
		}
	}
	return registry.ChannelInput{
		ModelID: mid, CredentialID: cid,
		UpstreamModel: req.UpstreamModel,
		Priority:      req.Priority, Weight: req.Weight,
		RPMLimit: req.RPMLimit, TPMLimit: req.TPMLimit,
		Status: req.Status, Extra: req.Extra,
	}, nil
}

func (s *Server) handleCreateChannel(w http.ResponseWriter, r *http.Request) {
	var req channelRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	in, errBody := req.toInput()
	if errBody != nil {
		writeErrorField(w, http.StatusBadRequest, errBody.Code, errBody.Message, errBody.Field)
		return
	}
	got, err := s.Store.Channels.Insert(r.Context(), in)
	if err != nil {
		translateRegistryError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, got)
}

func (s *Server) handleUpdateChannel(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	var req channelRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	in, errBody := req.toInput()
	if errBody != nil {
		writeErrorField(w, http.StatusBadRequest, errBody.Code, errBody.Message, errBody.Field)
		return
	}
	got, err := s.Store.Channels.Update(r.Context(), id, in)
	if err != nil {
		translateRegistryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, got)
}

func (s *Server) handleDeleteChannel(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	if err := s.Store.Channels.Delete(r.Context(), id); err != nil {
		translateRegistryError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// POST /v1/admin/channels/{id}/test
func (s *Server) handleTestChannel(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDPath(w, r, "id")
	if !ok {
		return
	}
	res := s.Probe.RunChannel(r.Context(), id)

	// Stamp the test result on the channel row (same pattern as
	// credential test). On success, RecordSuccess clears failure_count
	// and recovers from auto_disabled. On failure, do NOT call
	// RecordFailure here — admin "测试" buttons are diagnostic, not
	// production traffic; they shouldn't push a channel toward
	// auto_disable on their own.
	if res.OK {
		_ = s.Store.Channels.RecordSuccess(r.Context(), id, res.LatencyMs)
	}
	writeJSON(w, http.StatusOK, res)
}

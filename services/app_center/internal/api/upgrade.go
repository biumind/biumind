// Upgrade flow HTTP handlers (M15).
//
//   GET    /v1/apps/installs/{id}/upgrade
//          → 200 { available, current_version, target_version,
//                  requires_approval, pinned, perms_diff: {...} }
//          → 404 not_found
//
//   POST   /v1/apps/installs/{id}/upgrade
//          body: { accepted_new_permissions: ["..."] }
//          → 200 Installation (updated)
//          → 409 already_latest
//          → 409 pinned_version
//          → 400 permissions_not_accepted
//
// The check + upgrade pair is intentionally split — the client
// renders the diff from the GET (no side-effect) and the user only
// hits POST after explicit consent.

package api

import (
	"encoding/json"
	"errors"
	"net/http"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/biumind/biumind/services/app_center/internal/installs"
)

func (s *Server) handleCheckUpgrade(w http.ResponseWriter, r *http.Request) {
	if s.Installer == nil {
		writeErr(w, http.StatusServiceUnavailable, "installs_disabled", "")
		return
	}
	id := r.PathValue("id")
	status, err := s.Installer.CheckUpgradable(r.Context(), id)
	if err != nil {
		mapInstallError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

type upgradeReq struct {
	AcceptedNewPermissions []string `json:"accepted_new_permissions"`
}

func (s *Server) handleUpgrade(w http.ResponseWriter, r *http.Request) {
	if s.Installer == nil {
		writeErr(w, http.StatusServiceUnavailable, "installs_disabled", "")
		return
	}
	claims, _ := bauth.ClaimsFrom(r.Context())
	if claims == nil {
		writeErr(w, http.StatusUnauthorized, "no_claims", "")
		return
	}
	var req upgradeReq
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
	}

	row, err := s.Installer.Upgrade(r.Context(), installs.UpgradeRequest{
		InstallID:              r.PathValue("id"),
		AcceptedNewPermissions: req.AcceptedNewPermissions,
		CallerUserID:           claims.UserID,
		CallerOrgID:            claims.OrgID,
		CallerRoles:            claims.Roles,
	})
	if err != nil {
		mapUpgradeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

// mapUpgradeError extends mapInstallError with the upgrade-specific
// sentinels. Caller decides whether to call this or the generic
// mapInstallError; we route by error type.
func mapUpgradeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, installs.ErrAlreadyLatest):
		writeErr(w, http.StatusConflict, "already_latest", err.Error())
	case errors.Is(err, installs.ErrPinnedVersion):
		writeErr(w, http.StatusConflict, "pinned_version", err.Error())
	case errors.Is(err, installs.ErrPermissionsNotAccepted):
		writeErr(w, http.StatusBadRequest, "permissions_not_accepted", err.Error())
	case errors.Is(err, installs.ErrApprovalRequired):
		writeErr(w, http.StatusBadRequest, "approval_required", err.Error())
	default:
		mapInstallError(w, err)
	}
}

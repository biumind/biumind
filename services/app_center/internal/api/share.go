// Public read-only shared-view renderer (M11.3).
//
//	GET /share/rss/{token}
//
// Auth model: NO JWT. The unguessable token IS the auth — same posture
// as the webhook receiver (the HMAC is its auth). The token resolves to
// an rss.shared_views row; missing / expired / revoked → 404. Otherwise
// we render a minimal read-only HTML page with a live slice of the view.

package api

import (
	"errors"
	"net/http"

	"github.com/biumind/biumind/services/app_center/internal/rss/share"
)

// MountShares attaches the public share route. Mounted on the same mux
// as /v1/apps/* but bypasses requireAuth. No-op without a DB pool.
func (s *Server) MountShares(mux *http.ServeMux) {
	if s.Pool == nil {
		return
	}
	mux.HandleFunc("GET /share/rss/{token}", s.handleShareRSS)
}

func (s *Server) handleShareRSS(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	if token == "" {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	htmlDoc, err := share.Render(r.Context(), s.Pool, token)
	if err != nil {
		if errors.Is(err, share.ErrNotShareable) {
			http.Error(w, "此分享不存在或已过期", http.StatusNotFound)
			return
		}
		if s.Logger != nil {
			s.Logger.ErrorContext(r.Context(), "share render failed",
				"token", token, "err", err.Error())
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write([]byte(htmlDoc))
}

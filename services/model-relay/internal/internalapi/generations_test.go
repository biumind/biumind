package internalapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/biumind/biumind/services/model-relay/internal/api"
)

// helper: 构造带 token 的 POST /v1/internal/generations 请求.
func genReq(t *testing.T, token string, body map[string]any) *http.Request {
	t.Helper()
	b, _ := json.Marshal(body)
	r := httptest.NewRequest(http.MethodPost, "/v1/internal/generations", bytes.NewReader(b))
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	return r
}

func TestGenerate_AuthRequired(t *testing.T) {
	s := &Server{Token: "secret", Images: &api.ImagesHandler{}, Videos: &api.VideosHandler{}}
	mux := http.NewServeMux()
	s.MountGenerations(mux)

	// 无 token → 401
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, genReq(t, "", map[string]any{"user_id": "u1", "type": "image"}))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("no token: got %d, want 401", w.Code)
	}

	// 错 token → 401
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, genReq(t, "wrong", map[string]any{"user_id": "u1", "type": "image"}))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("bad token: got %d, want 401", w.Code)
	}
}

func TestGenerate_MissingUserID(t *testing.T) {
	s := &Server{Token: "secret", Images: &api.ImagesHandler{}, Videos: &api.VideosHandler{}}
	mux := http.NewServeMux()
	s.MountGenerations(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, genReq(t, "secret", map[string]any{"type": "image", "model": "wanx"}))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("missing user_id: got %d, want 400", w.Code)
	}
}

func TestGenerate_HandlersNotWired(t *testing.T) {
	s := &Server{Token: "secret"} // Images/Videos nil
	mux := http.NewServeMux()
	s.MountGenerations(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, genReq(t, "secret", map[string]any{"user_id": "u1", "type": "image"}))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("nil handlers: got %d, want 503", w.Code)
	}
}

func TestGenerate_UnsupportedType(t *testing.T) {
	s := &Server{Token: "secret", Images: &api.ImagesHandler{}, Videos: &api.VideosHandler{}}
	mux := http.NewServeMux()
	s.MountGenerations(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, genReq(t, "secret", map[string]any{"user_id": "u1", "type": "digital_human"}))
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("unsupported type: got %d, want 501", w.Code)
	}
}

// 路由确实打到对应 handler:ImagesHandler/VideosHandler 在 ModeRouter 为 nil
// 时返 500 no_mode_router,以此证明请求被分发进去了(而非停在 internalapi)。
func TestGenerate_RoutesToHandler(t *testing.T) {
	s := &Server{Token: "secret", Images: &api.ImagesHandler{}, Videos: &api.VideosHandler{}}
	mux := http.NewServeMux()
	s.MountGenerations(mux)

	for _, typ := range []string{"image", "video"} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, genReq(t, "secret", map[string]any{
			"user_id": "11111111-1111-1111-1111-111111111111",
			"type":    typ, "model": "m", "prompt": "p",
		}))
		// ModeRouter nil → handler 写 500 no_mode_router. 关键是 *不是* 401/400/501,
		// 证明已分发到 image/video handler。
		if w.Code != http.StatusInternalServerError {
			t.Errorf("type=%s: got %d, want 500 (no_mode_router, 证明已分发)", typ, w.Code)
		}
	}
}

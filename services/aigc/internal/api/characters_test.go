package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

// ════════════════════════════════════════════════════════════
// /v1/characters CRUD
// ════════════════════════════════════════════════════════════

func TestCreateAndListCharacter(t *testing.T) {
	_, mux := newTestServer(t)

	uid := uuid.New()
	tok := issueToken(t, uid, "free", []string{"member"})

	body := []byte(`{"name":"主播 A","avatar_url":"cas:` +
		"abcdef0000000000000000000000000000000000000000000000000000000000" +
		`","voice_default":"BV001_streaming","is_public":false,"config":{"style":"fashion"}}`)
	req := httptest.NewRequest("POST", "/v1/characters", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("create status=%d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Character map[string]any `json:"character"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	id := resp.Character["id"].(string)
	if id == "" {
		t.Fatal("create returned empty id")
	}
	if resp.Character["is_system"].(bool) {
		t.Fatal("user-created character marked is_system")
	}

	// list (only own — no include_public)
	req = httptest.NewRequest("GET", "/v1/characters", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("list status=%d body=%s", w.Code, w.Body.String())
	}
	var listResp struct {
		Characters []map[string]any `json:"characters"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	found := false
	for _, c := range listResp.Characters {
		if c["id"].(string) == id {
			found = true
			if c["name"].(string) != "主播 A" {
				t.Errorf("name = %v", c["name"])
			}
		}
	}
	if !found {
		t.Errorf("created character not in list")
	}
}

func TestCreateCharacter_NameRequired(t *testing.T) {
	_, mux := newTestServer(t)
	tok := issueToken(t, uuid.New(), "free", []string{"member"})

	req := httptest.NewRequest("POST", "/v1/characters",
		bytes.NewReader([]byte(`{"name":""}`)))
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 400 {
		t.Errorf("status=%d, want 400", w.Code)
	}
}

func TestDeleteCharacter_Owner(t *testing.T) {
	_, mux := newTestServer(t)
	uid := uuid.New()
	tok := issueToken(t, uid, "free", []string{"member"})

	// 先 create
	req := httptest.NewRequest("POST", "/v1/characters",
		bytes.NewReader([]byte(`{"name":"to-delete"}`)))
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("create status=%d", w.Code)
	}
	var cresp struct {
		Character map[string]any `json:"character"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &cresp)
	id := cresp.Character["id"].(string)

	// delete
	req = httptest.NewRequest("DELETE", "/v1/characters/"+id, nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Errorf("delete status=%d body=%s", w.Code, w.Body.String())
	}

	// 再删一次 → 404
	req = httptest.NewRequest("DELETE", "/v1/characters/"+id, nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 404 {
		t.Errorf("second delete status=%d, want 404", w.Code)
	}
}

func TestDeleteCharacter_NotOwner_Returns404(t *testing.T) {
	_, mux := newTestServer(t)
	owner := uuid.New()
	other := uuid.New()
	tokOwner := issueToken(t, owner, "free", []string{"member"})
	tokOther := issueToken(t, other, "free", []string{"member"})

	// owner 创建
	req := httptest.NewRequest("POST", "/v1/characters",
		bytes.NewReader([]byte(`{"name":"private"}`)))
	req.Header.Set("Authorization", "Bearer "+tokOwner)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	var cresp struct {
		Character map[string]any `json:"character"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &cresp)
	id := cresp.Character["id"].(string)

	// other 删 → 404 (不暴露存在性)
	req = httptest.NewRequest("DELETE", "/v1/characters/"+id, nil)
	req.Header.Set("Authorization", "Bearer "+tokOther)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 404 {
		t.Errorf("status=%d, want 404", w.Code)
	}
}

func TestListCharacters_Unauthorized(t *testing.T) {
	_, mux := newTestServer(t)
	req := httptest.NewRequest("GET", "/v1/characters", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status=%d, want 401", w.Code)
	}
}

// ════════════════════════════════════════════════════════════
// /v1/voices
// ════════════════════════════════════════════════════════════

func TestListVoices_All(t *testing.T) {
	_, mux := newTestServer(t)
	tok := issueToken(t, uuid.New(), "free", []string{"member"})
	req := httptest.NewRequest("GET", "/v1/voices", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Voices []VoiceEntry `json:"voices"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Voices) == 0 {
		t.Errorf("expected voices, got 0")
	}
}

func TestListVoices_FilterByProvider(t *testing.T) {
	_, mux := newTestServer(t)
	tok := issueToken(t, uuid.New(), "free", []string{"member"})
	req := httptest.NewRequest("GET", "/v1/voices?provider=dashscope", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status=%d", w.Code)
	}
	var resp struct {
		Voices []VoiceEntry `json:"voices"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	for _, v := range resp.Voices {
		if v.Provider != "dashscope" {
			t.Errorf("got provider=%s, want dashscope only", v.Provider)
		}
	}
}

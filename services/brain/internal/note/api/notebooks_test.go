// HTTP handler tests for notebook hierarchy (parent_id) — create with
// parent, reparent (三态：不动/移动/升根), 4xx 错误映射。
// Skips when DATABASE_URL unset (same convention as api_test.go).

package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"github.com/google/uuid"
)

// doJSON — 带 body 的请求便捷封装；返回状态码与解析后的响应体。
func (h *apiHarness) doJSON(t *testing.T, method, path, token string, payload any) (int, map[string]any) {
	t.Helper()
	var body *bytes.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		body = bytes.NewReader(b)
	} else {
		body = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, h.server.URL+path, body)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("req: %v", err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func (h *apiHarness) createNotebook(t *testing.T, token string, payload map[string]any) map[string]any {
	t.Helper()
	status, body := h.doJSON(t, "POST", "/v1/notebooks", token, payload)
	if status != http.StatusOK {
		t.Fatalf("create notebook %v: expected 200 got %d (%v)", payload, status, body)
	}
	return body
}

func errCode(body map[string]any) string {
	if e, ok := body["error"].(map[string]any); ok {
		if c, ok := e["code"].(string); ok {
			return c
		}
	}
	return ""
}

func TestNotebookAPI_CreateWithParentAndListShape(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()
	uid := uuid.New()
	defer h.cleanupUser(t, uid)
	token := h.mintToken(uid)

	root := h.createNotebook(t, token, map[string]any{"name": "根目录"})
	if root["parent_id"] != nil {
		t.Fatalf("root parent_id should serialize as null, got %v", root["parent_id"])
	}
	child := h.createNotebook(t, token, map[string]any{"name": "子目录", "parent_id": root["id"]})
	if child["parent_id"] != root["id"] {
		t.Fatalf("child parent_id = %v, want %v", child["parent_id"], root["id"])
	}

	status, body := h.get(t, "/v1/notebooks", token)
	if status != http.StatusOK {
		t.Fatalf("list: expected 200 got %d (%v)", status, body)
	}
	nbs, ok := body["notebooks"].([]any)
	if !ok || len(nbs) != 2 {
		t.Fatalf("expected 2 notebooks, got %v", body)
	}
	byID := map[string]map[string]any{}
	for _, raw := range nbs {
		nb := raw.(map[string]any)
		if _, ok := nb["parent_id"]; !ok {
			t.Errorf("list entry missing parent_id key: %v", nb)
		}
		byID[nb["id"].(string)] = nb
	}
	if byID[root["id"].(string)]["parent_id"] != nil {
		t.Errorf("listed root parent_id should be null: %v", byID[root["id"].(string)])
	}
	if byID[child["id"].(string)]["parent_id"] != root["id"] {
		t.Errorf("listed child parent_id wrong: %v", byID[child["id"].(string)])
	}
}

func TestNotebookAPI_CreateParentErrors(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()
	uid := uuid.New()
	defer h.cleanupUser(t, uid)
	token := h.mintToken(uid)

	// 非法 uuid → 400 bad_parent_id。
	status, body := h.doJSON(t, "POST", "/v1/notebooks", token, map[string]any{
		"name": "X", "parent_id": "not-a-uuid",
	})
	if status != http.StatusBadRequest || errCode(body) != "bad_parent_id" {
		t.Fatalf("bad uuid: expected 400 bad_parent_id, got %d (%v)", status, body)
	}
	// 不存在的父本 → 400 bad_parent_id（store.ErrInvalidParent 映射）。
	status, body = h.doJSON(t, "POST", "/v1/notebooks", token, map[string]any{
		"name": "X", "parent_id": uuid.New().String(),
	})
	if status != http.StatusBadRequest || errCode(body) != "bad_parent_id" {
		t.Fatalf("missing parent: expected 400 bad_parent_id, got %d (%v)", status, body)
	}
	// 超深：建到第 5 层后再建 → 400 depth_limit。
	parent := h.createNotebook(t, token, map[string]any{"name": "L1"})
	for level := 2; level <= 5; level++ {
		parent = h.createNotebook(t, token, map[string]any{"name": "L" + strconv.Itoa(level), "parent_id": parent["id"]})
	}
	status, body = h.doJSON(t, "POST", "/v1/notebooks", token, map[string]any{
		"name": "L6", "parent_id": parent["id"],
	})
	if status != http.StatusBadRequest || errCode(body) != "depth_limit" {
		t.Fatalf("level 6: expected 400 depth_limit, got %d (%v)", status, body)
	}
	// 同父下同名（大小写不敏感）→ 409 name_conflict（store.ErrDuplicateName 映射）。
	h.createNotebook(t, token, map[string]any{"name": "Dup"})
	status, body = h.doJSON(t, "POST", "/v1/notebooks", token, map[string]any{"name": "dup"})
	if status != http.StatusConflict || errCode(body) != "name_conflict" {
		t.Fatalf("duplicate name: expected 409 name_conflict, got %d (%v)", status, body)
	}
}

func TestNotebookAPI_Reparent(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()
	uid := uuid.New()
	defer h.cleanupUser(t, uid)
	token := h.mintToken(uid)

	a := h.createNotebook(t, token, map[string]any{"name": "A"})
	b := h.createNotebook(t, token, map[string]any{"name": "B"})
	c := h.createNotebook(t, token, map[string]any{"name": "C", "parent_id": b["id"]})

	// 不动 parent 的更新（只改名）→ parent_id 保持。
	newName := "C2"
	status, body := h.doJSON(t, "PUT", "/v1/notebooks/"+c["id"].(string), token, map[string]any{"name": newName})
	if status != http.StatusOK || body["parent_id"] != b["id"] || body["name"] != newName {
		t.Fatalf("rename should keep parent: %d (%v)", status, body)
	}
	// 移到 A 下。
	status, body = h.doJSON(t, "PUT", "/v1/notebooks/"+c["id"].(string), token, map[string]any{"parent_id": a["id"]})
	if status != http.StatusOK || body["parent_id"] != a["id"] {
		t.Fatalf("reparent under A: %d (%v)", status, body)
	}
	// 升到根（空串）。
	status, body = h.doJSON(t, "PUT", "/v1/notebooks/"+c["id"].(string), token, map[string]any{"parent_id": ""})
	if status != http.StatusOK || body["parent_id"] != nil {
		t.Fatalf("move to root: %d (%v)", status, body)
	}
	// 放回 B 下，再测防环：B → C 下（C 是 B 的后代）→ 400 notebook_cycle。
	h.doJSON(t, "PUT", "/v1/notebooks/"+c["id"].(string), token, map[string]any{"parent_id": b["id"]})
	status, body = h.doJSON(t, "PUT", "/v1/notebooks/"+b["id"].(string), token, map[string]any{"parent_id": c["id"]})
	if status != http.StatusBadRequest || errCode(body) != "notebook_cycle" {
		t.Fatalf("cycle: expected 400 notebook_cycle, got %d (%v)", status, body)
	}
	// 非法 uuid → 400 bad_parent_id。
	status, body = h.doJSON(t, "PUT", "/v1/notebooks/"+c["id"].(string), token, map[string]any{"parent_id": "nope"})
	if status != http.StatusBadRequest || errCode(body) != "bad_parent_id" {
		t.Fatalf("bad uuid: expected 400 bad_parent_id, got %d (%v)", status, body)
	}
	// 不存在的笔记本 → 404 not_found（既有行为不变）。
	status, body = h.doJSON(t, "PUT", "/v1/notebooks/"+uuid.New().String(), token, map[string]any{"name": "x"})
	if status != http.StatusNotFound || errCode(body) != "not_found" {
		t.Fatalf("missing notebook: expected 404 not_found, got %d (%v)", status, body)
	}
}

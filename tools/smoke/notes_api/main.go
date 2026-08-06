// notes_api — brain 笔记域（N0）HTTP 冒烟：笔记本 CRUD → 建笔记（含
// 客户端 uuid 重复提交的幂等验证）→ If-Match 更新 → 旧 version 409 →
// 软删 → 回收站/还原 → changes 增量拉取（含删除 tombstone 事件）。
//
// Usage:
//
//	go run ./tools/smoke/notes_api -base http://localhost:7003 -token <JWT>
//
// base/token 也可走环境变量 BIUMIND_BASE_URL / BIUMIND_TOKEN。
// 需要一个有效 JWT（identity 登录拿的 access_token）；脚本会在目标
// 账号下创建真实数据（一个笔记本 + 一条笔记，最后笔记留在回收站外
// 的还原状态，自行清理无妨）。
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

var (
	passCount int
	failCount int
)

func ok(format string, a ...any)   { passCount++; fmt.Printf("  ✓ %s\n", fmt.Sprintf(format, a...)) }
func fail(format string, a ...any) { failCount++; fmt.Printf("  ✗ %s\n", fmt.Sprintf(format, a...)) }
func die(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "fatal: %s\n", fmt.Sprintf(format, a...))
	os.Exit(1)
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

type client struct {
	base  string
	token string
	hc    *http.Client
}

// do 发起请求并返回 (status, body)。body 尽量解析成 map，失败给 nil。
func (c *client) do(method, path string, headers map[string]string, payload any) (int, map[string]any) {
	var body io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			die("marshal payload: %v", err)
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.base+path, body)
	if err != nil {
		die("new request: %v", err)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		die("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		m = map[string]any{"_raw": string(raw)}
	}
	return resp.StatusCode, m
}

func str(m map[string]any, key string) string {
	if v, okv := m[key].(string); okv {
		return v
	}
	return ""
}

func main() {
	base := flag.String("base", envOr("BIUMIND_BASE_URL", "http://localhost:7003"), "brain base URL")
	token := flag.String("token", envOr("BIUMIND_TOKEN", ""), "JWT access token (required)")
	flag.Parse()
	if *token == "" {
		die("missing token: pass -token or BIUMIND_TOKEN")
	}
	c := &client{base: strings.TrimRight(*base, "/"), token: *token, hc: &http.Client{Timeout: 10 * time.Second}}
	fmt.Printf("BASE_URL = %s\n\n", c.base)

	// ─── 1. 健康检查 ───────────────────────────────────────
	fmt.Println("[1/8] healthz")
	if st, _ := c.do("GET", "/healthz", nil, nil); st == 200 {
		ok("GET /healthz = 200")
	} else {
		fail("GET /healthz = %d", st)
	}

	// ─── 2. 建笔记本 ───────────────────────────────────────
	fmt.Println("[2/8] create notebook")
	nbName := fmt.Sprintf("smoke-nb-%d", time.Now().Unix())
	st, nb := c.do("POST", "/v1/notebooks", nil, map[string]any{"name": nbName, "position": 1.0})
	nbID := str(nb, "id")
	if st == 200 && nbID != "" {
		ok("POST /v1/notebooks → id=%s", nbID)
	} else {
		die("create notebook failed: status=%d body=%v", st, nb)
	}

	// ─── 3. 建笔记（客户端 uuid）+ 幂等重放 ───────────────
	fmt.Println("[3/8] create note (client uuid) + idempotent replay")
	noteUUID := fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		time.Now().Unix(), 1, 2, 3, 4) // 固定格式的合法 uuid
	createBody := map[string]any{
		"id": noteUUID, "notebook_id": nbID,
		"title": "smoke note", "content_md": "# smoke\nhello 笔记",
		"position": 1.0,
	}
	st, note := c.do("POST", "/v1/notes", nil, createBody)
	if st == 200 && str(note, "id") == noteUUID && note["version"] == float64(1) {
		ok("POST /v1/notes → id=%s version=1", noteUUID)
	} else {
		die("create note failed: status=%d body=%v", st, note)
	}
	// 同 uuid 重放：不报错、返回同一条、version 不涨
	st, replay := c.do("POST", "/v1/notes", nil, createBody)
	if st == 200 && str(replay, "id") == noteUUID && replay["version"] == float64(1) {
		ok("同 uuid 重放幂等（version 仍为 1）")
	} else {
		fail("幂等重放异常: status=%d body=%v", st, replay)
	}

	// ─── 4. If-Match 更新成功 ─────────────────────────────
	fmt.Println("[4/8] update with correct If-Match")
	st, upd := c.do("PUT", "/v1/notes/"+noteUUID,
		map[string]string{"If-Match": "1"},
		map[string]any{"content_md": "# smoke\nhello 笔记 v2"})
	if st == 200 && upd["version"] == float64(2) {
		ok("PUT If-Match:1 → version=2")
	} else {
		fail("If-Match 更新失败: status=%d body=%v", st, upd)
	}

	// ─── 5. 旧 version 更新 → 409 + 服务端当前内容 ────────
	fmt.Println("[5/8] stale If-Match → 409")
	st, conflict := c.do("PUT", "/v1/notes/"+noteUUID,
		map[string]string{"If-Match": "1"},
		map[string]any{"content_md": "stale write"})
	cur, hasCur := conflict["current"].(map[string]any)
	if st == 409 && conflict["current_version"] == float64(2) && hasCur && str(cur, "content_md") == "# smoke\nhello 笔记 v2" {
		ok("旧 version → 409 current_version=2 且带服务端当前内容")
	} else {
		fail("409 语义不对: status=%d body=%v", st, conflict)
	}

	// ─── 6. 软删 → 回收站 → 还原 ──────────────────────────
	fmt.Println("[6/8] soft delete → trash → restore")
	st, _ = c.do("DELETE", "/v1/notes/"+noteUUID, nil, nil)
	if st == 200 {
		ok("DELETE /v1/notes/{id} = 200（软删）")
	} else {
		fail("软删失败: status=%d", st)
	}
	st, trash := c.do("GET", "/v1/notes/trash", nil, nil)
	found := false
	if items, okv := trash["notes"].([]any); okv {
		for _, it := range items {
			if m, okm := it.(map[string]any); okm && str(m, "id") == noteUUID {
				found = true
			}
		}
	}
	if st == 200 && found {
		ok("回收站列表含已删笔记")
	} else {
		fail("回收站未找到笔记: status=%d body=%v", st, trash)
	}
	st, restored := c.do("POST", "/v1/notes/"+noteUUID+"/restore", nil, nil)
	if st == 200 && str(restored, "id") == noteUUID && str(restored, "notebook_id") == nbID {
		ok("还原成功，回到原笔记本")
	} else {
		fail("还原失败: status=%d body=%v", st, restored)
	}

	// ─── 7. changes 增量拉取（含删除 tombstone）───────────
	fmt.Println("[7/8] changes since=0")
	st, changes := c.do("GET", "/v1/notes/changes?since=0", nil, nil)
	seenCreated, seenDeleted := false, false
	var lastID float64
	if items, okv := changes["events"].([]any); okv {
		for _, it := range items {
			m, okm := it.(map[string]any)
			if !okm {
				continue
			}
			if id, okid := m["id"].(float64); okid && id > lastID {
				lastID = id
			}
			pl, _ := m["payload"].(map[string]any)
			if str(pl, "note_id") != noteUUID {
				continue
			}
			switch m["event_type"] {
			case "note.created":
				seenCreated = true
			case "note.deleted":
				seenDeleted = true
			}
		}
	}
	latest, _ := changes["latest"].(float64)
	if st == 200 && seenCreated && seenDeleted && latest >= lastID && lastID > 0 {
		ok("changes 含 note.created + note.deleted（tombstone），latest=%v", latest)
	} else {
		fail("changes 校验失败: status=%d created=%v deleted=%v latest=%v", st, seenCreated, seenDeleted, latest)
	}

	// ─── 8. 清理：软删笔记本 + 笔记进回收站再 purge ───────
	fmt.Println("[8/8] cleanup")
	if st, _ := c.do("DELETE", "/v1/notes/"+noteUUID, nil, nil); st == 200 {
		if st2, _ := c.do("DELETE", "/v1/notes/"+noteUUID+"/purge", nil, nil); st2 == 200 {
			ok("笔记 purge 清理完成")
		} else {
			fail("purge 失败: status=%d", st2)
		}
	} else {
		fail("清理软删失败: status=%d", st)
	}
	if st, _ := c.do("DELETE", "/v1/notebooks/"+nbID, nil, nil); st == 200 {
		ok("笔记本软删清理完成")
	} else {
		fail("笔记本清理失败: status=%d", st)
	}

	fmt.Printf("\npass: %d    fail: %d\n", passCount, failCount)
	if failCount > 0 {
		os.Exit(1)
	}
	fmt.Println("✓ notes api smoke OK")
}

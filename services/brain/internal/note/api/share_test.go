// 笔记分享 HTTP 测试 —— 管理端行为 + 公开端校验链（§7.6 五步）。
// 真 Postgres（DATABASE_URL 未设跳过，同 api_test.go 约定）；
// 附件 302 happy path 额外需要 MinIO（连不上跳过该子测试，同 files 域惯例）。

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	filespkg "github.com/biumind/biumind/services/brain/internal/files"
	"github.com/biumind/biumind/services/brain/internal/note/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

const testShareSigningKey = "biumind-share-access-test-signing-key-32+"

type shareHarness struct {
	server *httptest.Server
	signer *bauth.Signer
	pool   *pgxpool.Pool
	st     *store.Store
}

func newShareHarness(t *testing.T) *shareHarness {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL unset — skipping integration test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	st := store.New(pool)
	srv := NewServer(st,
		bauth.NewVerifier(testJWTSecret, testJWTIssuer, testJWTAudience),
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv.ShareSigningKey = []byte(testShareSigningKey)
	mux := http.NewServeMux()
	srv.Mount(mux)
	srv.MountPublic(mux)
	h := &shareHarness{
		server: httptest.NewServer(mux),
		signer: bauth.NewSigner(testJWTSecret, testJWTIssuer, testJWTAudience, 5*time.Minute),
		pool:   pool,
		st:     st,
	}
	t.Cleanup(func() { h.server.Close(); pool.Close() })
	return h
}

func (h *shareHarness) mintUserToken(uid uuid.UUID) string {
	tok, err := h.signer.Sign(&bauth.Claims{UserID: uid.String()})
	if err != nil {
		panic(err)
	}
	return tok
}

func (h *shareHarness) cleanupUser(t *testing.T, uid uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	stmts := []struct {
		q    string
		args []any
	}{
		{`DELETE FROM brain.note_shares WHERE user_id = $1`, []any{uid}},
		{`DELETE FROM brain.note_attachments WHERE note_id IN (SELECT id FROM brain.note_notes WHERE user_id = $1)`, []any{uid}},
		{`DELETE FROM brain.note_notes WHERE user_id = $1`, []any{uid}},
		{`DELETE FROM brain.events WHERE scope = $1 AND actor_id = $2`, []any{store.ShareEventScope, uid.String()}},
		{`DELETE FROM brain.events WHERE scope = $1`, []any{"note:user:" + uid.String()}},
		{`DELETE FROM files.objects WHERE user_id = $1`, []any{uid}},
	}
	for _, s := range stmts {
		if _, err := h.pool.Exec(ctx, s.q, s.args...); err != nil {
			t.Fatalf("cleanup: %v\nquery: %s", err, s.q)
		}
	}
}

// do —— 发请求；token 非空带 Authorization: Bearer（管理端用户 JWT）。
func (h *shareHarness) do(t *testing.T, method, path, token string, body any) (int, map[string]any, string) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		bs, _ := json.Marshal(body)
		rdr = bytes.NewReader(bs)
	}
	req, _ := http.NewRequest(method, h.server.URL+path, rdr)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	bb, _ := io.ReadAll(resp.Body)
	out := map[string]any{}
	if len(bb) > 0 && bb[0] == '{' {
		_ = json.Unmarshal(bb, &out)
	}
	return resp.StatusCode, out, string(bb)
}

// shareErrCode —— 从嵌套错误体 {"error":{"code","message"}} 取 code。
func shareErrCode(body map[string]any) string {
	e, _ := body["error"].(map[string]any)
	c, _ := e["code"].(string)
	return c
}

// noRedirect —— files 代理断言 302 用，不跟随跳转。
var noRedirect = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
	return http.ErrUseLastResponse
}}

func (h *shareHarness) getPublic(t *testing.T, path string) (int, map[string]any, string) {
	t.Helper()
	return h.do(t, "GET", path, "", nil)
}

func (h *shareHarness) createNote(t *testing.T, uid uuid.UUID, title, content string) uuid.UUID {
	t.Helper()
	n, _, err := h.st.CreateNote(context.Background(), store.CreateNoteInput{
		UserID: uid, Title: title, ContentMD: content, ActorID: uid.String(),
	})
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	return n.ID
}

func (h *shareHarness) putShare(t *testing.T, userTok string, noteID uuid.UUID, body map[string]any) (int, map[string]any) {
	t.Helper()
	st, out, raw := h.do(t, "PUT", "/v1/notes/"+noteID.String()+"/share", userTok, body)
	if st != 200 {
		t.Fatalf("PUT share: %d %s", st, raw)
	}
	return st, out
}

// ─── 管理端 ─────────────────────────────────────────────

func TestShareManagementFlow(t *testing.T) {
	h := newShareHarness(t)
	uid := uuid.New()
	defer h.cleanupUser(t, uid)
	tok := h.mintUserToken(uid)
	noteID := h.createNote(t, uid, "管理流", "正文")

	// 无分享 → GET 404 {"error":{"code":"not_found"}}（与 note 域一致的嵌套错误体）
	st, body, raw := h.do(t, "GET", "/v1/notes/"+noteID.String()+"/share", tok, nil)
	if st != 404 || shareErrCode(body) != "not_found" {
		t.Fatalf("GET no share: %d %s", st, raw)
	}

	// PUT 创建（never）
	_, sh := h.putShare(t, tok, noteID, map[string]any{"expires_in": "never"})
	if sh["token"] == nil || sh["password_set"] != false ||
		sh["credential_version"] != float64(1) || sh["expires_at"] != nil ||
		sh["disabled_at"] != nil || sh["view_count"] != float64(0) {
		t.Fatalf("share object shape: %v", sh)
	}
	token := sh["token"].(string)
	if len(token) != 32 {
		t.Fatalf("token should be 32-char base64url, got %q", token)
	}

	// PUT 幂等：同 token，只改有效期
	_, sh2 := h.putShare(t, tok, noteID, map[string]any{"expires_in": "7d"})
	if sh2["token"] != token || sh2["expires_at"] == nil {
		t.Fatalf("idempotent PUT: %v", sh2)
	}

	// GET 返回同一对象
	st, sh3, _ := h.do(t, "GET", "/v1/notes/"+noteID.String()+"/share", tok, nil)
	if st != 200 || sh3["token"] != token {
		t.Fatalf("GET share: %d %v", st, sh3)
	}

	// 跨用户 → 404（不泄露存在性）
	st, _, _ = h.do(t, "GET", "/v1/notes/"+noteID.String()+"/share", h.mintUserToken(uuid.New()), nil)
	if st != 404 {
		t.Fatalf("cross-user GET should 404, got %d", st)
	}
	st, _, _ = h.do(t, "PUT", "/v1/notes/"+noteID.String()+"/share", h.mintUserToken(uuid.New()),
		map[string]any{"expires_in": "never"})
	if st != 404 {
		t.Fatalf("cross-user PUT should 404, got %d", st)
	}

	// 参数校验：bad_expires_in / bad_password / 未鉴权 401
	st, body, _ = h.do(t, "PUT", "/v1/notes/"+noteID.String()+"/share", tok, map[string]any{"expires_in": "3d"})
	if st != 400 || shareErrCode(body) != "bad_expires_in" {
		t.Fatalf("bad expires_in: %d %v", st, body)
	}
	st, body, _ = h.do(t, "PUT", "/v1/notes/"+noteID.String()+"/share", tok,
		map[string]any{"expires_in": "1d", "password": "123"})
	if st != 400 || shareErrCode(body) != "bad_password" {
		t.Fatalf("short password: %d %v", st, body)
	}
	st, _, _ = h.do(t, "PUT", "/v1/notes/"+noteID.String()+"/share", "", map[string]any{"expires_in": "1d"})
	if st != 401 {
		t.Fatalf("management endpoints must require auth, got %d", st)
	}

	// DELETE → 204；再 DELETE → 404；PUT 恢复原 token
	st, _, _ = h.do(t, "DELETE", "/v1/notes/"+noteID.String()+"/share", tok, nil)
	if st != 204 {
		t.Fatalf("DELETE share: %d", st)
	}
	st, _, _ = h.do(t, "DELETE", "/v1/notes/"+noteID.String()+"/share", tok, nil)
	if st != 404 {
		t.Fatalf("double DELETE should 404, got %d", st)
	}
	_, sh4 := h.putShare(t, tok, noteID, map[string]any{"expires_in": "never"})
	if sh4["token"] != token || sh4["disabled_at"] != nil {
		t.Fatalf("PUT on disabled should restore original token: %v", sh4)
	}

	// rotate → 新 token + credential_version+1
	st, sh5, raw := h.do(t, "POST", "/v1/notes/"+noteID.String()+"/share/rotate", tok, nil)
	if st != 200 {
		t.Fatalf("rotate: %d %s", st, raw)
	}
	if sh5["token"] == token || sh5["credential_version"] != float64(2) {
		t.Fatalf("rotated share: %v", sh5)
	}
}

func TestShareListEndpoint(t *testing.T) {
	h := newShareHarness(t)
	uid := uuid.New()
	defer h.cleanupUser(t, uid)
	tok := h.mintUserToken(uid)

	n1 := h.createNote(t, uid, "列表甲", "")
	n2 := h.createNote(t, uid, "列表乙", "")
	h.putShare(t, tok, n1, map[string]any{"expires_in": "30d"})
	h.putShare(t, tok, n2, map[string]any{"expires_in": "never"})
	if st, _, _ := h.do(t, "DELETE", "/v1/notes/"+n2.String()+"/share", tok, nil); st != 204 {
		t.Fatalf("disable second share: %d", st)
	}

	st, body, raw := h.do(t, "GET", "/v1/notes/shares", tok, nil)
	if st != 200 {
		t.Fatalf("list shares: %d %s", st, raw)
	}
	shares, _ := body["shares"].([]any)
	if len(shares) != 2 {
		t.Fatalf("expected 2 shares, got %v", body)
	}
	byNote := map[string]map[string]any{}
	for _, it := range shares {
		m := it.(map[string]any)
		byNote[m["note_id"].(string)] = m
	}
	if byNote[n1.String()]["note_title"] != "列表甲" || byNote[n1.String()]["status"] != "active" {
		t.Fatalf("first share row: %v", byNote[n1.String()])
	}
	if byNote[n2.String()]["note_title"] != "列表乙" || byNote[n2.String()]["status"] != "disabled" {
		t.Fatalf("second share row: %v", byNote[n2.String()])
	}
	// 契约：服务端不返回 url 字段
	if _, has := byNote[n1.String()]["url"]; has {
		t.Fatalf("share object must not carry url field: %v", byNote[n1.String()])
	}
}

// ─── 公开端校验链 ────────────────────────────────────────

func TestSharePublicPasswordFlow(t *testing.T) {
	h := newShareHarness(t)
	uid := uuid.New()
	defer h.cleanupUser(t, uid)
	tok := h.mintUserToken(uid)
	fileID := uuid.New()
	noteID := h.createNote(t, uid, "带密码", fmt.Sprintf("正文见 ![图](biu-file://%s)。", fileID))

	_, sh := h.putShare(t, tok, noteID, map[string]any{"expires_in": "never", "password": "1234"})
	token := sh["token"].(string)

	// ① 有密码未解锁 → 401 password_required
	st, body, _ := h.getPublic(t, "/v1/shares/"+token)
	if st != 401 || shareErrCode(body) != "password_required" {
		t.Fatalf("locked share: %d %v", st, body)
	}

	// unlock 密码错误 → 401 invalid_password（并写审计事件）
	st, body, _ = h.do(t, "POST", "/v1/shares/"+token+"/unlock", "", map[string]any{"password": "0000"})
	if st != 401 || shareErrCode(body) != "invalid_password" {
		t.Fatalf("wrong password: %d %v", st, body)
	}
	var audit int
	if err := h.pool.QueryRow(context.Background(), `
		SELECT COUNT(*) FROM brain.events
		WHERE scope = $1 AND event_type = $2
	`, store.ShareEventScope, store.ShareEventUnlockFailed).Scan(&audit); err != nil || audit == 0 {
		t.Fatalf("unlock_failed audit event missing: count=%d err=%v", audit, err)
	}

	// unlock 正确 → 200 access_token + expires_in=7200
	st, body, _ = h.do(t, "POST", "/v1/shares/"+token+"/unlock", "", map[string]any{"password": "1234"})
	if st != 200 || body["access_token"] == nil || body["expires_in"] != float64(7200) {
		t.Fatalf("unlock: %d %v", st, body)
	}
	access := body["access_token"].(string)

	// 双通道：Bearer
	st, body, raw := h.do(t, "GET", "/v1/shares/"+token, access, nil)
	if st != 200 {
		t.Fatalf("public GET with bearer: %d %s", st, raw)
	}
	// 双通道：?access_token=
	st, body, _ = h.getPublic(t, "/v1/shares/"+token+"?access_token="+access)
	if st != 200 {
		t.Fatalf("public GET with query token: %d %v", st, body)
	}
	// 内容形状 + biu-file:// 改写
	if body["title"] != "带密码" || body["password_required"] != false {
		t.Fatalf("public content shape: %v", body)
	}
	content := body["content_md"].(string)
	if !strings.Contains(content, "/v1/shares/"+token+"/files/"+fileID.String()) ||
		strings.Contains(content, "biu-file://"+fileID.String()) {
		t.Fatalf("content_md not rewritten: %s", content)
	}

	// 重设密码 → credential_version+1 → 旧访问 JWT 立即失效
	h.putShare(t, tok, noteID, map[string]any{"expires_in": "never", "password": "5678"})
	st, body, _ = h.do(t, "GET", "/v1/shares/"+token, access, nil)
	if st != 401 || shareErrCode(body) != "password_required" {
		t.Fatalf("stale access JWT must fail after password reset: %d %v", st, body)
	}
	st, body, _ = h.do(t, "POST", "/v1/shares/"+token+"/unlock", "", map[string]any{"password": "5678"})
	if st != 200 {
		t.Fatalf("unlock with new password: %d %v", st, body)
	}

	// 移除密码 → 无需 JWT 直接可读
	h.putShare(t, tok, noteID, map[string]any{"expires_in": "never", "password": ""})
	st, body, _ = h.getPublic(t, "/v1/shares/"+token)
	if st != 200 || body["title"] != "带密码" {
		t.Fatalf("password removed should open access: %d %v", st, body)
	}
}

func TestSharePublicStatusChain(t *testing.T) {
	h := newShareHarness(t)
	uid := uuid.New()
	defer h.cleanupUser(t, uid)
	tok := h.mintUserToken(uid)
	ctx := context.Background()

	// 过期：expires_at 直接改库（API 面只有 1d/7d/30d/never）
	n1 := h.createNote(t, uid, "会过期", "")
	_, sh1 := h.putShare(t, tok, n1, map[string]any{"expires_in": "1d"})
	tok1 := sh1["token"].(string)
	if _, err := h.pool.Exec(ctx, `
		UPDATE brain.note_shares SET expires_at = now() - interval '1 hour' WHERE token = $1
	`, tok1); err != nil {
		t.Fatalf("force expire: %v", err)
	}
	st, body, _ := h.getPublic(t, "/v1/shares/"+tok1)
	if st != 410 || shareErrCode(body) != "expired" {
		t.Fatalf("expired share: %d %v", st, body)
	}

	// 停用 → 404 not_found（不暴露「存在但停用」）
	n2 := h.createNote(t, uid, "被停用", "")
	_, sh2 := h.putShare(t, tok, n2, map[string]any{"expires_in": "never"})
	tok2 := sh2["token"].(string)
	st, body, _ = h.getPublic(t, "/v1/shares/"+tok2)
	if st != 200 {
		t.Fatalf("active share: %d %v", st, body)
	}
	if st, _, _ := h.do(t, "DELETE", "/v1/notes/"+n2.String()+"/share", tok, nil); st != 204 {
		t.Fatalf("disable: %d", st)
	}
	st, body, _ = h.getPublic(t, "/v1/shares/"+tok2)
	if st != 404 || shareErrCode(body) != "not_found" {
		t.Fatalf("disabled share: %d %v", st, body)
	}

	// 笔记进回收站 → 410 note_deleted
	n3 := h.createNote(t, uid, "进回收站", "")
	_, sh3 := h.putShare(t, tok, n3, map[string]any{"expires_in": "never"})
	tok3 := sh3["token"].(string)
	if st, _, _ := h.do(t, "DELETE", "/v1/notes/"+n3.String(), tok, nil); st != 200 {
		t.Fatalf("trash note: %d", st)
	}
	st, body, _ = h.getPublic(t, "/v1/shares/"+tok3)
	if st != 410 || shareErrCode(body) != "note_deleted" {
		t.Fatalf("trashed note share: %d %v", st, body)
	}

	// rotate → 旧 token 404，新 token 200
	n4 := h.createNote(t, uid, "重置链接", "")
	_, sh4 := h.putShare(t, tok, n4, map[string]any{"expires_in": "never"})
	tok4 := sh4["token"].(string)
	st, sh5, _ := h.do(t, "POST", "/v1/notes/"+n4.String()+"/share/rotate", tok, nil)
	if st != 200 {
		t.Fatalf("rotate: %d", st)
	}
	st, _, _ = h.getPublic(t, "/v1/shares/"+tok4)
	if st != 404 {
		t.Fatalf("old token after rotate: %d", st)
	}
	st, _, _ = h.getPublic(t, "/v1/shares/"+sh5["token"].(string))
	if st != 200 {
		t.Fatalf("new token after rotate: %d", st)
	}

	// 不存在的 token → 404
	st, body, _ = h.getPublic(t, "/v1/shares/no-such-token")
	if st != 404 || shareErrCode(body) != "not_found" {
		t.Fatalf("unknown token: %d %v", st, body)
	}
}

// TestSharePublicFileGuards —— files 路由校验链 ③（附件不归属 → 404）。
// 归属且对象 ready 时无 MinIO → 503 files_unavailable。
func TestSharePublicFileGuards(t *testing.T) {
	h := newShareHarness(t)
	uid := uuid.New()
	defer h.cleanupUser(t, uid)
	tok := h.mintUserToken(uid)
	ctx := context.Background()

	fileID := uuid.New()
	noteID := h.createNote(t, uid, "附件守卫", fmt.Sprintf("![x](biu-file://%s)", fileID))
	_, sh := h.putShare(t, tok, noteID, map[string]any{"expires_in": "never"})
	token := sh["token"].(string)

	// ③ 附件未挂在笔记上 → 404（防随机 ID 盗链）
	st, body, _ := h.getPublic(t, fmt.Sprintf("/v1/shares/%s/files/%s", token, uuid.New()))
	if st != 404 || shareErrCode(body) != "not_found" {
		t.Fatalf("stranger attachment: %d %v", st, body)
	}

	// 归属（直接建 ready 对象 + 关联行）但 blob 未配置 → 503 files_unavailable
	if _, err := h.pool.Exec(ctx, `
		INSERT INTO files.objects (id, user_id, sha256, size_bytes, bucket, object_key, status)
		VALUES ($1, $2, $3, 10, 'biumind-files-test', $4, 'ready')
	`, fileID, uid, strings.Repeat("c", 64), uid.String()+"/"+fileID.String()); err != nil {
		t.Fatalf("insert files.objects: %v", err)
	}
	if _, err := h.pool.Exec(ctx, `
		INSERT INTO brain.note_attachments (note_id, file_id, is_associated)
		VALUES ($1, $2, true) ON CONFLICT DO NOTHING
	`, noteID, fileID); err != nil {
		t.Fatalf("insert note_attachments: %v", err)
	}
	st, body, _ = h.getPublic(t, fmt.Sprintf("/v1/shares/%s/files/%s", token, fileID))
	if st != 503 || shareErrCode(body) != "files_unavailable" {
		t.Fatalf("no-blob files route: %d %v", st, body)
	}

	// 坏 file_id → 400
	st, _, _ = h.getPublic(t, fmt.Sprintf("/v1/shares/%s/files/not-a-uuid", token))
	if st != 400 {
		t.Fatalf("bad file id: %d", st)
	}
}

// TestSharePublicFileRedirect —— 校验链全过后 302 到 presign URL。
// 需要真 MinIO（同 files 域测试惯例：连不上跳过）。
func TestSharePublicFileRedirect(t *testing.T) {
	h := newShareHarness(t)
	uid := uuid.New()
	defer h.cleanupUser(t, uid)
	ctx := context.Background()

	mEndpoint := os.Getenv("MINIO_ENDPOINT")
	mAccess, mSecret := os.Getenv("MINIO_ACCESS_KEY"), os.Getenv("MINIO_SECRET_KEY")
	if mEndpoint == "" {
		mEndpoint, mAccess, mSecret = "localhost:9000", "biumind", "biumind_minio_dev"
	}
	blob, err := filespkg.NewBlob(ctx, filespkg.BlobConfig{
		Endpoint: mEndpoint, AccessKey: mAccess, SecretKey: mSecret,
		Bucket: "biumind-files-test", EnsureBucket: true,
	})
	if err != nil {
		t.Skipf("MinIO connect failed (skip): %v", err)
	}

	// 重新装一台带 blob 的服务
	srv := NewServer(h.st,
		bauth.NewVerifier(testJWTSecret, testJWTIssuer, testJWTAudience),
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv.ShareSigningKey = []byte(testShareSigningKey)
	srv.ShareBlob = blob
	mux := http.NewServeMux()
	srv.Mount(mux)
	srv.MountPublic(mux)
	server := httptest.NewServer(mux)
	defer server.Close()

	tok := h.mintUserToken(uid)
	fileID := uuid.New()
	payload := []byte("share-proxy-payload-" + uuid.New().String())
	objectKey := uid.String() + "/" + fileID.String()
	if err := blob.Put(ctx, objectKey, bytes.NewReader(payload), int64(len(payload)), "image/png"); err != nil {
		t.Fatalf("minio put: %v", err)
	}
	defer blob.Remove(ctx, objectKey) //nolint:errcheck
	if _, err := h.pool.Exec(ctx, `
		INSERT INTO files.objects (id, user_id, sha256, size_bytes, bucket, object_key, status)
		VALUES ($1, $2, $3, $4, 'biumind-files-test', $5, 'ready')
	`, fileID, uid, strings.Repeat("d", 64), len(payload), objectKey); err != nil {
		t.Fatalf("insert files.objects: %v", err)
	}
	noteID := h.createNote(t, uid, "附件 302", fmt.Sprintf("![x](biu-file://%s)", fileID))
	if _, err := h.pool.Exec(ctx, `
		INSERT INTO brain.note_attachments (note_id, file_id, is_associated)
		VALUES ($1, $2, true) ON CONFLICT DO NOTHING
	`, noteID, fileID); err != nil {
		t.Fatalf("insert note_attachments: %v", err)
	}

	req, _ := http.NewRequest("PUT", server.URL+"/v1/notes/"+noteID.String()+"/share",
		strings.NewReader(`{"expires_in":"never"}`))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("put share: %v", err)
	}
	var shBody map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&shBody)
	resp.Body.Close()
	token := shBody["token"].(string)

	// 302 → Location 指向 presign URL；响应头带 nosniff + CSP sandbox
	req, _ = http.NewRequest("GET",
		fmt.Sprintf("%s/v1/shares/%s/files/%s", server.URL, token, fileID), nil)
	resp, err = noRedirect.Do(req)
	if err != nil {
		t.Fatalf("files route: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 302 {
		bb, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 302, got %d %s", resp.StatusCode, bb)
	}
	loc := resp.Header.Get("Location")
	if loc == "" || !strings.Contains(loc, objectKey) {
		t.Fatalf("redirect target should be presigned object URL: %q", loc)
	}
	if resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("missing nosniff header: %v", resp.Header)
	}
	if resp.Header.Get("Content-Security-Policy") != "sandbox" {
		t.Fatalf("missing CSP sandbox header: %v", resp.Header)
	}

	// presign URL 真能下载（15min TTL 内任何人可拉，与 files 域语义一致）
	dl, err := http.Get(loc)
	if err != nil {
		t.Fatalf("GET presigned: %v", err)
	}
	defer dl.Body.Close()
	got, _ := io.ReadAll(dl.Body)
	if dl.StatusCode != 200 || !bytes.Equal(got, payload) {
		t.Fatalf("presigned download: %d", dl.StatusCode)
	}

	// view_count 在内容接口累计（走带 blob 的第二台服务）
	cresp, err := http.Get(server.URL + "/v1/shares/" + token)
	if err != nil {
		t.Fatalf("public content via blob server: %v", err)
	}
	cresp.Body.Close()
	if cresp.StatusCode != 200 {
		t.Fatalf("public content via blob server: %d", cresp.StatusCode)
	}
	var vc int
	if err := h.pool.QueryRow(ctx, `
		SELECT view_count FROM brain.note_shares WHERE token = $1
	`, token).Scan(&vc); err != nil || vc != 1 {
		t.Fatalf("view_count after one public GET: %d err=%v", vc, err)
	}
}

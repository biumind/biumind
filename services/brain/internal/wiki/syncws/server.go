// Package syncws —— wiki 项目实时事件 WebSocket。
//
// 路径：
//
//	GET /v1/wiki/projects/{pid}/sync?token=...&since=N
//
// Wire 协议（JSON over WS text frame）：
//
//	{type: "ready",   since: <event_id>}
//	{type: "catchup", events: [...]}                  // 一次性
//	{type: "live",    event:  {...}}                  // 后续实时推送
//	{type: "ping"}
//	{type: "error",   reason: "..."}
//
// 事件 schema（client activity reducer 期望的形态）：
//
//	{
//	  entity:    "ingest_task" | "research_task" | "page" | "block" | ...
//	  op:        "started" | "done" | "created" | "updated" | ...
//	  entity_id: "<uuid string>"
//	  event_id:  <brain.events.id 的 int>
//	  payload:   <brain.events.payload 原样>
//	}
//
// 投影规则：brain.events.event_type 形如 "ingest_task.started" /
// "page.created"，按 "." 拆 (entity, op)。entity_id 从 payload 里挑
// task_id / page_id / source_id / id 中第一个 string 字段（见 [pickEntityID]）。
//
// 当前实现：每个 WS 连接独立 polling brain.events（500ms 间隔），按
// scope = "wiki:project:<pid>" 过滤，自带 since 游标续推。无需 pg_notify，
// 单项目下连接数小（通常 1-2），polling 开销可忽略。
package syncws

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	wikistore "github.com/biumind/biumind/services/brain/internal/wiki/store"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

const (
	pingInterval  = 25 * time.Second
	pollInterval  = 500 * time.Millisecond
	writeWait     = 10 * time.Second
	pongWait      = 60 * time.Second
	catchupLimit  = 200 // 单次 catchup / poll 最多拉多少行
	scopePrefix   = "wiki:project:"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 4096,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

type Server struct {
	Wiki     *wikistore.Store
	Verifier *bauth.Verifier
	Logger   *slog.Logger
}

func NewServer(w *wikistore.Store, v *bauth.Verifier, l *slog.Logger) *Server {
	return &Server{Wiki: w, Verifier: v, Logger: l}
}

func (s *Server) Mount(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/wiki/projects/{pid}/sync", s.handleStream)
}

func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	pid, err := uuid.Parse(r.PathValue("pid"))
	if err != nil {
		http.Error(w, "bad project id", http.StatusBadRequest)
		return
	}

	token := r.URL.Query().Get("token")
	if token == "" {
		auth := r.Header.Get("Authorization")
		if len(auth) > 7 && auth[:7] == "Bearer " {
			token = auth[7:]
		}
	}
	if token == "" {
		http.Error(w, "missing token", http.StatusUnauthorized)
		return
	}
	claims, err := s.Verifier.Verify(token)
	if err != nil {
		http.Error(w, "invalid token", http.StatusUnauthorized)
		return
	}
	uid, err := uuid.Parse(claims.UserID)
	if err != nil {
		http.Error(w, "bad user id", http.StatusUnauthorized)
		return
	}

	proj, err := s.Wiki.GetProject(r.Context(), pid)
	if err != nil {
		http.Error(w, "project not found", http.StatusNotFound)
		return
	}
	if proj.OwnerID != uid {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	// since 参数：从查询串读，0 表示首次连接 / 不需要历史。
	since := int64(0)
	if v := r.URL.Query().Get("since"); v != "" {
		if n, perr := strconv.ParseInt(v, 10, 64); perr == nil && n > 0 {
			since = n
		}
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		s.Logger.Warn("wiki sync ws upgrade failed",
			"project_id", pid, "err", err)
		return
	}
	defer conn.Close()

	conn.SetReadDeadline(time.Now().Add(pongWait))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	scope := scopePrefix + pid.String()

	// ─── 1) Catchup：拉 since 之后所有 events 一次性发 ─────────────
	catchup, lastID, err := s.fetchSince(r.Context(), scope, since)
	if err != nil {
		_ = writeJSON(conn, map[string]any{
			"type":   "error",
			"reason": "catchup_failed: " + err.Error(),
		})
		return
	}
	if err := writeJSON(conn, map[string]any{
		"type":  "ready",
		"since": lastID,
	}); err != nil {
		return
	}
	if len(catchup) > 0 {
		if err := writeJSON(conn, map[string]any{
			"type":   "catchup",
			"events": catchup,
		}); err != nil {
			return
		}
	}

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// 读循环：丢掉所有 client→server 帧（当前协议是 server-only push）；
	// 仅维持 read deadline + 检测对端关闭。
	go func() {
		defer cancel()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	// ─── 2) Live polling 循环 + 心跳 ─────────────────────────────
	pollTimer := time.NewTicker(pollInterval)
	defer pollTimer.Stop()
	pingTimer := time.NewTicker(pingInterval)
	defer pingTimer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-pingTimer.C:
			conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := conn.WriteControl(
				websocket.PingMessage, nil,
				time.Now().Add(writeWait),
			); err != nil {
				return
			}
			_ = writeJSON(conn, map[string]any{"type": "ping"})
		case <-pollTimer.C:
			fresh, newLastID, err := s.fetchSince(ctx, scope, lastID)
			if err != nil {
				// 单次失败不致命；继续 poll，下次再试。
				s.Logger.Debug("wiki sync poll failed",
					"project_id", pid, "err", err)
				continue
			}
			if len(fresh) == 0 {
				continue
			}
			lastID = newLastID
			for _, ev := range fresh {
				if err := writeJSON(conn, map[string]any{
					"type":  "live",
					"event": ev,
				}); err != nil {
					return
				}
			}
		}
	}
}

// fetchSince 拉取 [since+1, ...] 的 events 并投影到 client schema。
// 返回投影后的 events + 最后一行 id（若空则保持 since 不变）。
func (s *Server) fetchSince(
	ctx context.Context, scope string, since int64,
) ([]map[string]any, int64, error) {
	rows, err := s.Wiki.EventsSince(ctx, scope, since, catchupLimit)
	if err != nil {
		return nil, since, err
	}
	out := make([]map[string]any, 0, len(rows))
	last := since
	for _, e := range rows {
		entity, op := splitEventType(e.EventType)
		out = append(out, map[string]any{
			"entity":     entity,
			"op":         op,
			"entity_id":  pickEntityID(e.Payload),
			"event_id":   e.ID,
			"payload":    e.Payload,
			"created_at": e.CreatedAt.UTC().Format(time.RFC3339),
		})
		if e.ID > last {
			last = e.ID
		}
	}
	return out, last, nil
}

// "ingest_task.started" → ("ingest_task", "started")
// "page.created"        → ("page", "created")
// "block.deleted.soft"  → ("block", "deleted.soft")  （多 dot 时 op 包含尾巴）
func splitEventType(t string) (entity, op string) {
	idx := strings.IndexByte(t, '.')
	if idx <= 0 {
		return t, ""
	}
	return t[:idx], t[idx+1:]
}

// payload 里挑一个 string 字段当 entity_id：依次试 task_id / id /
// page_id / source_id。worker 写 events 时按约定填一个即可。
func pickEntityID(p map[string]any) string {
	if p == nil {
		return ""
	}
	for _, k := range []string{"task_id", "id", "entity_id", "page_id", "source_id"} {
		if v, ok := p[k]; ok {
			if s, isStr := v.(string); isStr && s != "" {
				return s
			}
		}
	}
	return ""
}

func writeJSON(conn *websocket.Conn, v any) error {
	conn.SetWriteDeadline(time.Now().Add(writeWait))
	return conn.WriteJSON(v)
}

// wiki 项目内对话 HTTP API。conversations + messages 双层 CRUD。
//
// assistant 自动回复目前**不**在本服务里跑 —— 等 worker（独立部署、订阅
// brain.events.wiki_message.created）调 LLM 生成 + 写入 role='assistant'
// row + emit event。前端通过 syncws 实时收到 assistant 消息。
//
// 当前 user 发完 message 后立即返回，前端通过 watch + WS 等 assistant 行。
package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	wikistore "github.com/biumind/biumind/services/brain/internal/wiki/store"
	"github.com/biumind/biumind/services/brain/internal/wiki/wikicommon"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Server struct {
	Store    *Store
	Wiki     *wikistore.Store
	Pool     *pgxpool.Pool // 用来写 brain.events
	Verifier *bauth.Verifier
	Logger   *slog.Logger
}

func NewServer(s *Store, w *wikistore.Store, pool *pgxpool.Pool, v *bauth.Verifier, l *slog.Logger) *Server {
	return &Server{Store: s, Wiki: w, Pool: pool, Verifier: v, Logger: l}
}

func (s *Server) Mount(mux *http.ServeMux) {
	auth := func(h http.HandlerFunc) http.HandlerFunc {
		return wikicommon.RequireAuth(s.Verifier, h)
	}
	base := "/v1/wiki/projects/{pid}/conversations"
	mux.HandleFunc("GET "+base, auth(s.handleListConv))
	mux.HandleFunc("POST "+base, auth(s.handleCreateConv))
	mux.HandleFunc("PATCH "+base+"/{cid}", auth(s.handlePatchConv))
	mux.HandleFunc("DELETE "+base+"/{cid}", auth(s.handleDeleteConv))
	mux.HandleFunc("GET "+base+"/{cid}/messages", auth(s.handleListMsg))
	mux.HandleFunc("POST "+base+"/{cid}/messages", auth(s.handleCreateMsg))
	mux.HandleFunc("PATCH "+base+"/{cid}/messages/{mid}", auth(s.handlePatchMsg))
	mux.HandleFunc("DELETE "+base+"/{cid}/messages/{mid}", auth(s.handleDeleteMsg))
	mux.HandleFunc("POST "+base+"/{cid}/messages/{mid}/regenerate",
		auth(s.handleRegenerate))
}

// ─── Wire ───────────────────────────────────────────────────────

type convOut struct {
	ID           string `json:"id"`
	ProjectID    string `json:"project_id"`
	Title        string `json:"title"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
	MessageCount int    `json:"message_count,omitempty"`
}

type msgOut struct {
	ID             string         `json:"id"`
	ConversationID string         `json:"conversation_id"`
	Role           string         `json:"role"`
	Content        string         `json:"content"`
	Metadata       map[string]any `json:"metadata,omitempty"`
	CreatedAt      string         `json:"created_at"`
}

func conversationJSON(c *Conversation, msgCount int) convOut {
	return convOut{
		ID:           c.ID.String(),
		ProjectID:    c.ProjectID.String(),
		Title:        c.Title,
		CreatedAt:    c.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:    c.UpdatedAt.UTC().Format(time.RFC3339),
		MessageCount: msgCount,
	}
}

func messageJSON(m *Message) msgOut {
	return msgOut{
		ID:             m.ID.String(),
		ConversationID: m.ConversationID.String(),
		Role:           m.Role,
		Content:        m.Content,
		Metadata:       m.Metadata,
		CreatedAt:      m.CreatedAt.UTC().Format(time.RFC3339),
	}
}

// ─── Handlers ───────────────────────────────────────────────────

func (s *Server) handleListConv(w http.ResponseWriter, r *http.Request) {
	pid, err := uuid.Parse(r.PathValue("pid"))
	if err != nil {
		wikicommon.WriteErr(w, http.StatusBadRequest, "bad_project_id", "")
		return
	}
	if !s.requireOwner(w, r, pid) {
		return
	}
	uid := wikicommon.MustUserID(r)
	rows, err := s.Store.ListConversations(r.Context(), pid, uid, 100)
	if err != nil {
		wikicommon.WriteErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	out := make([]convOut, 0, len(rows))
	for _, c := range rows {
		// 简单计数：每个 conv 单独 count(*) 在小数据 OK；后续如有性能问题
		// 可以一次 GROUP BY 拉。
		var n int
		_ = s.Pool.QueryRow(r.Context(),
			`SELECT count(*) FROM brain.wiki_messages WHERE conversation_id=$1`,
			c.ID).Scan(&n)
		out = append(out, conversationJSON(c, n))
	}
	wikicommon.WriteJSON(w, http.StatusOK, map[string]any{"items": out})
}

type createConvReq struct {
	Title string `json:"title"`
}

func (s *Server) handleCreateConv(w http.ResponseWriter, r *http.Request) {
	pid, err := uuid.Parse(r.PathValue("pid"))
	if err != nil {
		wikicommon.WriteErr(w, http.StatusBadRequest, "bad_project_id", "")
		return
	}
	if !s.requireOwner(w, r, pid) {
		return
	}
	uid := wikicommon.MustUserID(r)
	var req createConvReq
	_ = json.NewDecoder(r.Body).Decode(&req)
	c, err := s.Store.CreateConversation(r.Context(), pid, uid, req.Title)
	if err != nil {
		wikicommon.WriteErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	s.emitEvent(r.Context(), pid, uid, "conversation.created", map[string]any{
		"id":              c.ID.String(),
		"conversation_id": c.ID.String(),
		"title":           c.Title,
	})
	wikicommon.WriteJSON(w, http.StatusCreated, conversationJSON(c, 0))
}

type patchConvReq struct {
	Title string `json:"title"`
}

func (s *Server) handlePatchConv(w http.ResponseWriter, r *http.Request) {
	c, ok := s.loadOwnedConv(w, r)
	if !ok {
		return
	}
	var req patchConvReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		wikicommon.WriteErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	updated, err := s.Store.PatchConversation(r.Context(), c.ID, req.Title)
	if err != nil {
		wikicommon.WriteErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	s.emitEvent(r.Context(), c.ProjectID, c.OwnerID, "conversation.updated", map[string]any{
		"id":              c.ID.String(),
		"conversation_id": c.ID.String(),
		"title":           updated.Title,
	})
	wikicommon.WriteJSON(w, http.StatusOK, conversationJSON(updated, 0))
}

func (s *Server) handleDeleteConv(w http.ResponseWriter, r *http.Request) {
	c, ok := s.loadOwnedConv(w, r)
	if !ok {
		return
	}
	if err := s.Store.SoftDeleteConversation(r.Context(), c.ID); err != nil {
		wikicommon.WriteErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	s.emitEvent(r.Context(), c.ProjectID, c.OwnerID, "conversation.deleted", map[string]any{
		"id":              c.ID.String(),
		"conversation_id": c.ID.String(),
	})
	wikicommon.WriteJSON(w, http.StatusOK, map[string]any{"deleted": c.ID.String()})
}

func (s *Server) handleListMsg(w http.ResponseWriter, r *http.Request) {
	c, ok := s.loadOwnedConv(w, r)
	if !ok {
		return
	}
	rows, err := s.Store.ListMessages(r.Context(), c.ID, 500)
	if err != nil {
		wikicommon.WriteErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	out := make([]msgOut, 0, len(rows))
	for _, m := range rows {
		out = append(out, messageJSON(m))
	}
	wikicommon.WriteJSON(w, http.StatusOK, map[string]any{"items": out})
}

type createMsgReq struct {
	Role     string         `json:"role"`
	Content  string         `json:"content"`
	Metadata map[string]any `json:"metadata"`
}

func (s *Server) handleCreateMsg(w http.ResponseWriter, r *http.Request) {
	c, ok := s.loadOwnedConv(w, r)
	if !ok {
		return
	}
	var req createMsgReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		wikicommon.WriteErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	role := req.Role
	if role == "" {
		role = "user"
	}
	if role != "user" && role != "assistant" && role != "system" {
		wikicommon.WriteErr(w, http.StatusBadRequest, "bad_role", role)
		return
	}
	m, err := s.Store.CreateMessage(r.Context(), c.ID, role, req.Content, req.Metadata)
	if err != nil {
		wikicommon.WriteErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	s.emitEvent(r.Context(), c.ProjectID, c.OwnerID,
		"conversation_message.created", map[string]any{
			"id":              m.ID.String(),
			"conversation_id": c.ID.String(),
			"role":            m.Role,
		})
	wikicommon.WriteJSON(w, http.StatusCreated, messageJSON(m))
}

func (s *Server) handlePatchMsg(w http.ResponseWriter, r *http.Request) {
	wikicommon.NotImplemented(w, "wiki.chat", "patch_message")
}

func (s *Server) handleDeleteMsg(w http.ResponseWriter, r *http.Request) {
	c, ok := s.loadOwnedConv(w, r)
	if !ok {
		return
	}
	mid, err := uuid.Parse(r.PathValue("mid"))
	if err != nil {
		wikicommon.WriteErr(w, http.StatusBadRequest, "bad_msg_id", "")
		return
	}
	m, err := s.Store.GetMessage(r.Context(), mid)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			wikicommon.WriteErr(w, http.StatusNotFound, "not_found", "message")
			return
		}
		wikicommon.WriteErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	if m.ConversationID != c.ID {
		wikicommon.WriteErr(w, http.StatusNotFound, "not_found", "message")
		return
	}
	if err := s.Store.DeleteMessage(r.Context(), mid); err != nil {
		wikicommon.WriteErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	s.emitEvent(r.Context(), c.ProjectID, c.OwnerID,
		"conversation_message.deleted", map[string]any{
			"id":              mid.String(),
			"conversation_id": c.ID.String(),
		})
	wikicommon.WriteJSON(w, http.StatusOK, map[string]any{"deleted": mid.String()})
}

func (s *Server) handleRegenerate(w http.ResponseWriter, r *http.Request) {
	// 标记一个 event，等 worker 接管。当前 noop 实现仅为前端按钮可点。
	c, ok := s.loadOwnedConv(w, r)
	if !ok {
		return
	}
	mid := r.PathValue("mid")
	s.emitEvent(r.Context(), c.ProjectID, c.OwnerID,
		"conversation_message.regenerate_requested", map[string]any{
			"id":              mid,
			"conversation_id": c.ID.String(),
		})
	wikicommon.WriteJSON(w, http.StatusAccepted, map[string]any{
		"queued":          true,
		"conversation_id": c.ID.String(),
		"message_id":      mid,
	})
}

// ─── Helpers ────────────────────────────────────────────────────

func (s *Server) requireOwner(w http.ResponseWriter, r *http.Request, pid uuid.UUID) bool {
	uid := wikicommon.MustUserID(r)
	proj, err := s.Wiki.GetProject(r.Context(), pid)
	if err != nil {
		wikicommon.WriteErr(w, http.StatusNotFound, "not_found", "project")
		return false
	}
	if proj.OwnerID != uid {
		wikicommon.WriteErr(w, http.StatusForbidden, "forbidden", "")
		return false
	}
	return true
}

func (s *Server) loadOwnedConv(w http.ResponseWriter, r *http.Request) (*Conversation, bool) {
	pid, err := uuid.Parse(r.PathValue("pid"))
	if err != nil {
		wikicommon.WriteErr(w, http.StatusBadRequest, "bad_project_id", "")
		return nil, false
	}
	cid, err := uuid.Parse(r.PathValue("cid"))
	if err != nil {
		wikicommon.WriteErr(w, http.StatusBadRequest, "bad_conv_id", "")
		return nil, false
	}
	c, err := s.Store.GetConversation(r.Context(), cid)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			wikicommon.WriteErr(w, http.StatusNotFound, "not_found", "conversation")
			return nil, false
		}
		wikicommon.WriteErr(w, http.StatusInternalServerError, "internal", err.Error())
		return nil, false
	}
	if c.ProjectID != pid {
		wikicommon.WriteErr(w, http.StatusNotFound, "not_found", "conversation")
		return nil, false
	}
	if !s.requireOwner(w, r, pid) {
		return nil, false
	}
	return c, true
}

// emitEvent 写 brain.events 让 syncws 推给前端。失败不阻断主流程，
// 仅日志告警 — events 是观测信道，丢一条不影响数据正确性。
func (s *Server) emitEvent(
	ctx context.Context, projectID, ownerID uuid.UUID,
	eventType string, payload map[string]any,
) {
	if s.Pool == nil {
		return
	}
	scope := fmt.Sprintf("wiki:project:%s", projectID)
	pl, _ := json.Marshal(payload)
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO brain.events
			(scope, actor_type, actor_id, event_type, payload)
		VALUES ($1, 'user', $2, $3, $4)
	`, scope, ownerID.String(), eventType, pl)
	if err != nil {
		s.Logger.Warn("wiki chat emit event failed",
			"event_type", eventType, "err", err)
	}
}

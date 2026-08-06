package api

// characters.go — 数字人角色 endpoints (P5-4).
//
//   GET    /v1/characters?include_public=1  列出 (自己 + (可选) 系统/公开)
//   POST   /v1/characters                    创建 (用户私有)
//   DELETE /v1/characters/{id}               删除 (仅 owner)
//
// 系统内置角色 (user_id IS NULL) 不能通过 POST/DELETE 操作; 那走 admin 路径.
// 调用方 (Flutter) 默认 include_public=1, 让用户菜单里立刻看到内置角色.

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/biumind/biumind/services/aigc/internal/store"
	"github.com/google/uuid"
)

// MountCharacters 挂数字人角色路由.
func (s *Server) MountCharacters(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/characters", s.requireAuth(s.handleListCharacters))
	mux.HandleFunc("POST /v1/characters", s.requireAuth(s.handleCreateCharacter))
	mux.HandleFunc("DELETE /v1/characters/{id}", s.requireAuth(s.handleDeleteCharacter))
}

// ─── GET /v1/characters ──────────────────────────────

func (s *Server) handleListCharacters(w http.ResponseWriter, r *http.Request) {
	uid, _, ok := requireUserID(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	includePublic := firstQ(q, "include_public") == "1" ||
		strings.EqualFold(firstQ(q, "include_public"), "true")
	limit, offset := paginationFromQuery(q)

	chars, err := s.Store.ListCharacters(r.Context(), store.ListCharactersArgs{
		UserID:        &uid,
		IncludePublic: includePublic,
		Limit:         limit,
		Offset:        offset,
	})
	if writeStoreErr(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"characters": projectCharacters(chars),
	})
}

// ─── POST /v1/characters ─────────────────────────────

type createCharacterReq struct {
	Name         string          `json:"name"`
	AvatarURL    string          `json:"avatar_url"`
	VoiceDefault string          `json:"voice_default"`
	Config       json.RawMessage `json:"config"`
	IsPublic     bool            `json:"is_public"`
}

func (s *Server) handleCreateCharacter(w http.ResponseWriter, r *http.Request) {
	uid, _, ok := requireUserID(w, r)
	if !ok {
		return
	}
	var req createCharacterReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeErr(w, http.StatusBadRequest, "name_required", "")
		return
	}
	if len(name) > 64 {
		writeErr(w, http.StatusBadRequest, "name_too_long", "max 64 chars")
		return
	}

	var configAny any
	if len(req.Config) > 0 {
		// 透传 raw json — store 会再 marshal 一次, 这里先 decode 校 JSON 合法性.
		if err := json.Unmarshal(req.Config, &configAny); err != nil {
			writeErr(w, http.StatusBadRequest, "bad_config", err.Error())
			return
		}
	}

	c, err := s.Store.CreateCharacter(r.Context(), store.CreateCharacterArgs{
		UserID:       &uid,
		Name:         name,
		AvatarURL:    req.AvatarURL,
		VoiceDefault: req.VoiceDefault,
		Config:       configAny,
		IsPublic:     req.IsPublic,
	})
	if writeStoreErr(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"character": projectCharacter(c),
	})
}

// ─── DELETE /v1/characters/{id} ──────────────────────

func (s *Server) handleDeleteCharacter(w http.ResponseWriter, r *http.Request) {
	uid, _, ok := requireUserID(w, r)
	if !ok {
		return
	}
	idStr := r.PathValue("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_id", err.Error())
		return
	}
	if err := s.Store.DeleteCharacter(r.Context(), uid, id); writeStoreErr(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

// ─── projection ──────────────────────────────────────

func projectCharacter(c *store.Character) map[string]any {
	if c == nil {
		return nil
	}
	owner := ""
	if c.UserID != nil {
		owner = c.UserID.String()
	}
	return map[string]any{
		"id":            c.ID.String(),
		"user_id":       owner, // 空字符串 = 系统内置
		"name":          c.Name,
		"avatar_url":    c.AvatarURL,
		"voice_default": c.VoiceDefault,
		"config":        rawJSONOrEmpty(c.Config),
		"is_public":     c.IsPublic,
		"created_at":    c.CreatedAt,
		"is_system":     c.UserID == nil,
	}
}

func projectCharacters(cs []*store.Character) []map[string]any {
	out := make([]map[string]any, 0, len(cs))
	for _, c := range cs {
		out = append(out, projectCharacter(c))
	}
	return out
}

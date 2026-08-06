// 公告 / 通知 inbox HTTP 层(PERI-4)。
//
//	公共(requireAuth):
//	  GET    /v1/announcements                列当前用户可见的已发布公告 + 未读数(带 is_read)
//	  POST   /v1/announcements/{id}/read       标记单条已读
//	  POST   /v1/announcements/read-all        全部标已读
//	后台(requireAdmin,claims.Roles 含 admin/superadmin):
//	  GET    /v1/admin/announcements           列全部(含草稿)
//	  POST   /v1/admin/announcements           新建/发布
//	  PUT    /v1/admin/announcements/{id}      编辑
//	  DELETE /v1/admin/announcements/{id}      删除
//
// 发布(published=true)时经 Realtime 推送让在线客户端即时刷新(见 s.AnnouncementNotifier,
// 由 main 注入;nil 时仅入库,客户端靠轮询兜底)。版本门槛 min/max_app_version 在此按
// 客户端 X-App-Version 头过滤。
package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/google/uuid"

	"github.com/biumind/biumind/services/identity/internal/store"
)

// adminRoles 是可管理公告的后台角色。
var announcementAdminRoles = []string{"admin", "superadmin"}

// AnnouncementNotifier 在公告发布时通知 Realtime 下发(main 注入;nil = 仅入库)。
type AnnouncementNotifier interface {
	NotifyAnnouncementPublished(id string)
}

// requireAdmin = requireAuth + claims.Roles 含管理角色。
func (s *Server) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		claims, _ := bauth.ClaimsFrom(r.Context())
		if !hasAnnouncementAdminRole(claims) {
			writeErr(w, http.StatusForbidden, "forbidden", "admin role required")
			return
		}
		next(w, r)
	})
}

func hasAnnouncementAdminRole(claims *bauth.Claims) bool {
	if claims == nil {
		return false
	}
	for _, want := range announcementAdminRoles {
		for _, got := range claims.Roles {
			if got == want {
				return true
			}
		}
	}
	return false
}

// ─── 序列化 ────────────────────────────────────────────────────────────────

type announcementOut struct {
	ID            string  `json:"id"`
	Level         string  `json:"level"`
	Title         string  `json:"title"`
	Body          string  `json:"body"`
	BodyZh        string  `json:"body_zh"`
	URL           string  `json:"url"`
	MinAppVersion string  `json:"min_app_version"`
	MaxAppVersion string  `json:"max_app_version"`
	Published     bool    `json:"published"`
	CreatedAt     string  `json:"created_at"`
	ExpiresAt     *string `json:"expires_at"`
	IsRead        bool    `json:"is_read"`
}

func toAnnouncementOut(a *store.Announcement, isRead bool) announcementOut {
	o := announcementOut{
		ID:            a.ID.String(),
		Level:         a.Level,
		Title:         a.Title,
		Body:          a.Body,
		BodyZh:        a.BodyZh,
		URL:           a.URL,
		MinAppVersion: a.MinAppVersion,
		MaxAppVersion: a.MaxAppVersion,
		Published:     a.Published,
		CreatedAt:     a.CreatedAt.UTC().Format(time.RFC3339),
		IsRead:        isRead,
	}
	if a.ExpiresAt != nil {
		s := a.ExpiresAt.UTC().Format(time.RFC3339)
		o.ExpiresAt = &s
	}
	return o
}

// ─── 公共端点 ──────────────────────────────────────────────────────────────

func (s *Server) handleListAnnouncements(w http.ResponseWriter, r *http.Request) {
	claims, _ := bauth.ClaimsFrom(r.Context())
	uid, err := uuid.Parse(claims.UserID)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "bad_subject", "")
		return
	}
	list, err := s.Store.ListActiveAnnouncementsForUser(r.Context(), uid)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}
	appVer := strings.TrimSpace(r.Header.Get("X-App-Version"))
	out := make([]announcementOut, 0, len(list))
	unread := 0
	for _, a := range list {
		if !appVersionInRange(appVer, a.MinAppVersion, a.MaxAppVersion) {
			continue
		}
		out = append(out, toAnnouncementOut(&a.Announcement, a.IsRead))
		if !a.IsRead {
			unread++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"announcements": out,
		"unread_count":  unread,
	})
}

func (s *Server) handleMarkAnnouncementRead(w http.ResponseWriter, r *http.Request) {
	claims, _ := bauth.ClaimsFrom(r.Context())
	uid, err := uuid.Parse(claims.UserID)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "bad_subject", "")
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_id", "")
		return
	}
	if err := s.Store.MarkAnnouncementRead(r.Context(), uid, id); err != nil {
		writeErr(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleMarkAllAnnouncementsRead(w http.ResponseWriter, r *http.Request) {
	claims, _ := bauth.ClaimsFrom(r.Context())
	uid, err := uuid.Parse(claims.UserID)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "bad_subject", "")
		return
	}
	if err := s.Store.MarkAllAnnouncementsRead(r.Context(), uid); err != nil {
		writeErr(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ─── 后台端点 ──────────────────────────────────────────────────────────────

func (s *Server) handleAdminListAnnouncements(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.ListAnnouncements(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}
	out := make([]announcementOut, 0, len(list))
	for _, a := range list {
		out = append(out, toAnnouncementOut(a, false))
	}
	writeJSON(w, http.StatusOK, map[string]any{"announcements": out})
}

type announcementReq struct {
	Level         string  `json:"level"`
	Title         string  `json:"title"`
	Body          string  `json:"body"`
	BodyZh        string  `json:"body_zh"`
	URL           string  `json:"url"`
	MinAppVersion string  `json:"min_app_version"`
	MaxAppVersion string  `json:"max_app_version"`
	Published     bool    `json:"published"`
	ExpiresAt     *string `json:"expires_at"`
}

func (in announcementReq) toInput() (store.CreateAnnouncementInput, error) {
	var exp *time.Time
	if in.ExpiresAt != nil && strings.TrimSpace(*in.ExpiresAt) != "" {
		t, err := time.Parse(time.RFC3339, *in.ExpiresAt)
		if err != nil {
			return store.CreateAnnouncementInput{}, err
		}
		exp = &t
	}
	return store.CreateAnnouncementInput{
		Level:         in.Level,
		Title:         strings.TrimSpace(in.Title),
		Body:          in.Body,
		BodyZh:        in.BodyZh,
		URL:           in.URL,
		MinAppVersion: in.MinAppVersion,
		MaxAppVersion: in.MaxAppVersion,
		Published:     in.Published,
		ExpiresAt:     exp,
	}, nil
}

func (s *Server) handleAdminCreateAnnouncement(w http.ResponseWriter, r *http.Request) {
	var req announcementReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	in, err := req.toInput()
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_expires_at", err.Error())
		return
	}
	if in.Title == "" {
		writeErr(w, http.StatusBadRequest, "title_required", "")
		return
	}
	a, err := s.Store.CreateAnnouncement(r.Context(), in)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}
	if a.Published && s.AnnouncementNotifier != nil {
		s.AnnouncementNotifier.NotifyAnnouncementPublished(a.ID.String())
	}
	writeJSON(w, http.StatusOK, toAnnouncementOut(a, false))
}

func (s *Server) handleAdminUpdateAnnouncement(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_id", "")
		return
	}
	var req announcementReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	in, err := req.toInput()
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_expires_at", err.Error())
		return
	}
	if in.Title == "" {
		writeErr(w, http.StatusBadRequest, "title_required", "")
		return
	}
	if err := s.Store.UpdateAnnouncement(r.Context(), id, in); err != nil {
		writeErr(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}
	if in.Published && s.AnnouncementNotifier != nil {
		s.AnnouncementNotifier.NotifyAnnouncementPublished(id.String())
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleAdminDeleteAnnouncement(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_id", "")
		return
	}
	if err := s.Store.DeleteAnnouncement(r.Context(), id); err != nil {
		writeErr(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// appVersionInRange 判定客户端版本是否落在 [min,max](空端不限)。版本号非法 / 客户端
// 未带版本 → 放行(不因版本元数据缺失而漏发公告)。
func appVersionInRange(appVer, min, max string) bool {
	if appVer == "" {
		return true
	}
	v, ok := parseSemver(appVer)
	if !ok {
		return true
	}
	if min != "" {
		if mv, ok := parseSemver(min); ok && semverCmp(v, mv) < 0 {
			return false
		}
	}
	if max != "" {
		if mv, ok := parseSemver(max); ok && semverCmp(v, mv) > 0 {
			return false
		}
	}
	return true
}

func parseSemver(s string) ([3]int, bool) {
	var v [3]int
	parts := strings.SplitN(strings.TrimSpace(s), ".", 3)
	if len(parts) == 0 {
		return v, false
	}
	for i := 0; i < len(parts) && i < 3; i++ {
		n := 0
		seen := false
		for _, c := range parts[i] {
			if c < '0' || c > '9' {
				break
			}
			n = n*10 + int(c-'0')
			seen = true
		}
		if !seen {
			return v, false
		}
		v[i] = n
	}
	return v, true
}

func semverCmp(a, b [3]int) int {
	for i := 0; i < 3; i++ {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

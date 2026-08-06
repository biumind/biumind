package api

// mp_subscribe.go — 小程序订阅消息授权登记 + 列表.
//
//	POST /v1/notify/mp-subscribe        (Bearer)
//	  { "platform": "wechat_mp", "openid": "...", "template_id": "tpl_xx", "times": 1 }
//	→ { "id": "..." }
//
//	GET /v1/notify/me/subscriptions     (Bearer)
//	→ { "subscriptions": [ { id, platform, template_id, times_remaining, granted_at } ] }
//
// 真正发送由 notify worker (v2.0 路线图) 消费 mp_subscriptions 行 + 调各
// 平台 API; 这里只负责"用户授权 → 落库". 调用流:
//   1. 客户端 wx.requestSubscribeMessage(...) 拿到 ['accept']
//   2. 客户端 POST /v1/notify/mp-subscribe 上报 template_id
//   3. 服务端业务事件触发时 (任务完成/Agent 跑完), worker pickForDispatch
//      → 调微信 subscribeMessage.send → consume

import (
	"encoding/json"
	"net/http"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/google/uuid"
)

type subscribeMPReq struct {
	Platform   string `json:"platform"`
	OpenID     string `json:"openid"`
	TemplateID string `json:"template_id"`
	Times      int    `json:"times"`
}

func (s *Server) handleSubscribeMP(w http.ResponseWriter, r *http.Request) {
	claims, ok := bauth.ClaimsFrom(r.Context())
	if !ok {
		writeErr(w, http.StatusUnauthorized, "no_claims", "")
		return
	}
	uid, err := uuid.Parse(claims.UserID)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "bad_subject", "")
		return
	}
	var req subscribeMPReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if req.Platform == "" || req.OpenID == "" || req.TemplateID == "" {
		writeErr(w, http.StatusBadRequest, "missing_fields",
			"platform / openid / template_id 必填")
		return
	}
	if req.Times <= 0 {
		req.Times = 1
	}
	row, err := s.Store.CreateMPSubscription(
		r.Context(), uid, req.Platform, req.OpenID, req.TemplateID, req.Times,
	)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":              row.ID.String(),
		"times_remaining": row.TimesRemaining,
	})
}

type subscriptionOut struct {
	ID             string `json:"id"`
	Platform       string `json:"platform"`
	TemplateID     string `json:"template_id"`
	TimesRemaining int    `json:"times_remaining"`
	GrantedAt      string `json:"granted_at"`
}

// handleListInbox — 通知收件箱占位 stub.
//
// 真实实现要等 services/notify/ 上线 (v2.0 路线图): worker 把推送出去
// 的订阅消息 / 站内信 / 系统通知聚合到 inbox 表, 这里 SELECT 返回.
//
// 当前先返空 list, 让客户端 UI 不 404. v2.0 后切到真实查询.
func (s *Server) handleListInbox(w http.ResponseWriter, r *http.Request) {
	if _, ok := bauth.ClaimsFrom(r.Context()); !ok {
		writeErr(w, http.StatusUnauthorized, "no_claims", "")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": []any{}})
}

func (s *Server) handleListSubscriptionsMP(w http.ResponseWriter, r *http.Request) {
	claims, ok := bauth.ClaimsFrom(r.Context())
	if !ok {
		writeErr(w, http.StatusUnauthorized, "no_claims", "")
		return
	}
	uid, err := uuid.Parse(claims.UserID)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "bad_subject", "")
		return
	}
	rows, err := s.Store.ListMPSubscriptionsByUser(r.Context(), uid)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	out := make([]subscriptionOut, 0, len(rows))
	for _, row := range rows {
		out = append(out, subscriptionOut{
			ID:             row.ID.String(),
			Platform:       row.Platform,
			TemplateID:     row.TemplateID,
			TimesRemaining: row.TimesRemaining,
			GrantedAt:      row.GrantedAt.UTC().Format("2006-01-02T15:04:05Z"),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"subscriptions": out})
}

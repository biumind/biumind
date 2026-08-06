// alert_webhook — 接收 Alertmanager 推送, 用 system_config 里的 SMTP 发邮件.
//
// Alertmanager webhook 格式 (v0.27):
//   POST /v1/internal/alerts/email
//   Content-Type: application/json
//   {
//     "version": "4",
//     "groupKey": "...",
//     "status": "firing" | "resolved",
//     "receiver": "biumind-email",
//     "groupLabels": {"alertname":"ServiceDown"},
//     "commonLabels": {"severity":"critical","service":"model-relay"},
//     "commonAnnotations": {"summary":"...","description":"..."},
//     "alerts": [{ "status":"firing","labels":{...}, "annotations":{...},
//                  "startsAt":"...","endsAt":"..." }]
//   }
//
// 鉴权: 仅 docker biu-net 内部访问 (site nginx 不暴露 /v1/internal/*),
// 不需要 JWT. 但仍校验 X-Biumind-Webhook 头作为简单 shared secret
// (从 system_config.alert.email.webhook_secret 读, 没配置时跳过).
//
// 邮件发送:
//   - 从 biumind_system.config 拿 alert.email value
//   - enabled=false → 200 noop, 写 audit
//   - SMTP 配置缺失 → 200 noop + warn log
//   - 发送失败 → 500, 写 audit error 字段

package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/biumind/biumind/services/identity/internal/mailer"
)

// AlertEmailConfig — system_config key='alert.email' 的结构.
type AlertEmailConfig struct {
	Enabled       bool     `json:"enabled"`
	SMTPHost      string   `json:"smtp_host"`
	SMTPPort      int      `json:"smtp_port"`
	SMTPUser      string   `json:"smtp_user"`
	SMTPPass      string   `json:"smtp_pass"`
	SMTPTLS       bool     `json:"smtp_tls"`
	From          string   `json:"from"`
	To            []string `json:"to"`
	WebhookSecret string   `json:"webhook_secret,omitempty"` // 可选 shared secret
}

// AlertmanagerPayload — 简化版只保留我们需要的字段.
type AlertmanagerPayload struct {
	Status            string            `json:"status"` // firing | resolved
	Receiver          string            `json:"receiver"`
	GroupLabels       map[string]string `json:"groupLabels"`
	CommonLabels      map[string]string `json:"commonLabels"`
	CommonAnnotations map[string]string `json:"commonAnnotations"`
	Alerts            []struct {
		Status      string            `json:"status"`
		Labels      map[string]string `json:"labels"`
		Annotations map[string]string `json:"annotations"`
		StartsAt    time.Time         `json:"startsAt"`
		EndsAt      time.Time         `json:"endsAt"`
	} `json:"alerts"`
}

func (s *Server) handleAlertWebhook(w http.ResponseWriter, r *http.Request) {
	if s.SystemConfig == nil {
		writeErr(w, http.StatusServiceUnavailable, "system_config_disabled", "")
		return
	}

	// 拿配置
	entry, err := s.SystemConfig.Get(r.Context(), "alert.email")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "config_load", err.Error())
		return
	}
	if entry == nil {
		writeErr(w, http.StatusServiceUnavailable, "no_config", "alert.email not configured")
		return
	}
	var cfg AlertEmailConfig
	if err := json.Unmarshal(entry.Value, &cfg); err != nil {
		writeErr(w, http.StatusInternalServerError, "config_invalid", err.Error())
		return
	}

	// shared secret (可选)
	if cfg.WebhookSecret != "" {
		got := r.Header.Get("X-Biumind-Webhook")
		if got != cfg.WebhookSecret {
			writeErr(w, http.StatusUnauthorized, "bad_webhook_secret", "")
			return
		}
	}

	// 解析 payload
	var p AlertmanagerPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_payload", err.Error())
		return
	}

	// audit 收到, 不管发送成功与否
	defer s.Audit.Append(AuditEvent{
		Action:     "alert.received",
		Resource:   "alert",
		Target:     p.GroupLabels["alertname"],
		TargetType: "alertname",
		Detail: fmt.Sprintf("status=%s severity=%s service=%s alerts=%d",
			p.Status, p.CommonLabels["severity"],
			p.CommonLabels["service"], len(p.Alerts)),
		Success: true,
	})

	if !cfg.Enabled {
		writeJSON(w, http.StatusOK, map[string]any{"sent": false, "reason": "email disabled"})
		return
	}
	if cfg.SMTPHost == "" || cfg.From == "" || len(cfg.To) == 0 {
		writeErr(w, http.StatusServiceUnavailable, "incomplete_smtp",
			"smtp_host/from/to required")
		return
	}

	// 构造 + 发件 — 走 mailer 共享 helper.
	sendErr := mailer.Send(mailer.SMTPConfig{
		Host: cfg.SMTPHost, Port: cfg.SMTPPort,
		User: cfg.SMTPUser, Pass: cfg.SMTPPass,
		TLS: cfg.SMTPTLS, From: cfg.From,
	}, cfg.To, buildSubject(&p), buildBody(&p))

	if sendErr != nil {
		s.Logger.Error("alert email send failed", "err", sendErr,
			"alertname", p.GroupLabels["alertname"])
		writeErr(w, http.StatusInternalServerError, "smtp_error", sendErr.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"sent":       true,
		"recipients": len(cfg.To),
	})
}

func buildSubject(p *AlertmanagerPayload) string {
	severity := p.CommonLabels["severity"]
	emoji := "ℹ️"
	if severity == "critical" {
		emoji = "🚨"
	} else if severity == "warning" {
		emoji = "⚠️"
	}
	status := strings.ToUpper(p.Status)
	if p.Status == "resolved" {
		emoji = "✅"
	}
	return fmt.Sprintf("=?UTF-8?B?%s?= [BiuMind %s] %s",
		base64Encode(emoji), status, p.GroupLabels["alertname"])
}

func buildBody(p *AlertmanagerPayload) string {
	var b strings.Builder
	severityColor := "#909399"
	switch p.CommonLabels["severity"] {
	case "critical":
		severityColor = "#f56c6c"
	case "warning":
		severityColor = "#e6a23c"
	case "info":
		severityColor = "#67c23a"
	}
	fmt.Fprintf(&b, `<div style="font-family:sans-serif">`)
	fmt.Fprintf(&b, `<h2 style="color:%s">%s: %s</h2>`,
		severityColor, p.Status, p.GroupLabels["alertname"])
	if svc, ok := p.CommonLabels["service"]; ok && svc != "" {
		fmt.Fprintf(&b, `<p><b>Service:</b> %s</p>`, svc)
	}
	if sev := p.CommonLabels["severity"]; sev != "" {
		fmt.Fprintf(&b, `<p><b>Severity:</b> <span style="color:%s">%s</span></p>`, severityColor, sev)
	}
	for _, a := range p.Alerts {
		b.WriteString("<hr>")
		if s, ok := a.Annotations["summary"]; ok {
			fmt.Fprintf(&b, `<p><b>%s</b></p>`, s)
		}
		if d, ok := a.Annotations["description"]; ok {
			fmt.Fprintf(&b, `<p>%s</p>`, d)
		}
		fmt.Fprintf(&b, `<p style="color:#666;font-size:12px">Started: %s`, a.StartsAt.Format(time.RFC3339))
		if !a.EndsAt.IsZero() && p.Status == "resolved" {
			fmt.Fprintf(&b, ` &nbsp; Resolved: %s`, a.EndsAt.Format(time.RFC3339))
		}
		fmt.Fprintf(&b, `</p>`)
		if len(a.Labels) > 0 {
			b.WriteString(`<pre style="background:#f5f5f5;padding:8px;border-radius:4px;font-size:12px">`)
			for k, v := range a.Labels {
				fmt.Fprintf(&b, "%s=%s\n", k, v)
			}
			b.WriteString(`</pre>`)
		}
	}
	b.WriteString(`</div>`)
	return b.String()
}

// base64 mini wrapper (subject 中文需要 RFC 2047 编码)
func base64Encode(s string) string {
	const tbl = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	b := []byte(s)
	var out strings.Builder
	for i := 0; i < len(b); i += 3 {
		n := len(b) - i
		var c [3]byte
		copy(c[:], b[i:])
		out.WriteByte(tbl[c[0]>>2])
		if n > 1 {
			out.WriteByte(tbl[((c[0]&0x3)<<4)|(c[1]>>4)])
			if n > 2 {
				out.WriteByte(tbl[((c[1]&0xf)<<2)|(c[2]>>6)])
				out.WriteByte(tbl[c[2]&0x3f])
			} else {
				out.WriteByte(tbl[(c[1]&0xf)<<2])
				out.WriteByte('=')
			}
		} else {
			out.WriteByte(tbl[(c[0]&0x3)<<4])
			out.WriteString("==")
		}
	}
	return out.String()
}

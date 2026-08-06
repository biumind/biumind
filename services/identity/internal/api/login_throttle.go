// LoginThrottle — 登录失败滑动窗口检测.
//
// 不是限流 (没拒绝请求, 仍由 nginx + RateLimiter 兜底), 而是审计层的
// 异常检测: 同 email 在 windowSec 秒内累计 >= threshold 次失败, 立即:
//   1. 写一条 auth.login.brute_force 审计 (Success=false, ErrorCode=brute_force_detected)
//   2. 触发 alertmanager 模式邮件 (复用 alert.email 配置), 主题
//      含 email + IP, 让管理员人工介入.
//
// 简单实现: 内存 map[email][]time.Time, 每次记录前 prune 过期项.
// 多实例部署时各自计数 — 阈值偏低也无大碍 (告警是早警钟而非精确风控).
// 重启清零是预期行为 (告警冷却).

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/biumind/biumind/services/identity/internal/admin"
	"github.com/biumind/biumind/services/identity/internal/mailer"
)

// LoginThrottle 配置 + 状态.
type LoginThrottle struct {
	Window        time.Duration // 滑动窗口长度 (默认 5 分钟)
	Threshold     int           // 触发阈值 (默认 5 次)
	AlertCooldown time.Duration // 触发后多长时间不再重复告警 (默认 15 分钟)

	mu        sync.Mutex
	failures  map[string][]time.Time // email → 失败时间戳列表
	lastAlert map[string]time.Time   // email → 上次告警时间, 实现冷却
}

// NewLoginThrottle 默认参数: 5min/5次, 告警冷却 15min.
func NewLoginThrottle() *LoginThrottle {
	return &LoginThrottle{
		Window:        5 * time.Minute,
		Threshold:     5,
		AlertCooldown: 15 * time.Minute,
		failures:      make(map[string][]time.Time),
		lastAlert:     make(map[string]time.Time),
	}
}

// recordFailure 记录一次失败. 触发阈值时同步写 anomaly audit.
// srv 用于反查 Audit + Logger; 不直接发邮件 (那是 alertmanager 的职责),
// 但写 audit + 给运维 dashboard 显眼标记.
func (l *LoginThrottle) recordFailure(email, ip, ua string, srv *Server) {
	if email == "" {
		return // 没 email 不计数 (避免 "" 桶溢出)
	}
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()

	// prune 过期项 + 追加当前
	cutoff := now.Add(-l.Window)
	hist := l.failures[email]
	pruned := hist[:0]
	for _, t := range hist {
		if t.After(cutoff) {
			pruned = append(pruned, t)
		}
	}
	pruned = append(pruned, now)
	l.failures[email] = pruned

	if len(pruned) < l.Threshold {
		return
	}

	// 命中阈值 — 看冷却
	if last, ok := l.lastAlert[email]; ok && now.Sub(last) < l.AlertCooldown {
		return
	}
	l.lastAlert[email] = now

	if srv == nil {
		return
	}
	srv.appendAudit(admin.AuditEvent{
		ActorEmail: email,
		ActorIP:    ip,
		ActorUA:    ua,
		Action:     "auth.login.brute_force",
		Resource:   "session",
		Success:    false,
		ErrorCode:  "brute_force_detected",
		ErrorMessage: fmt.Sprintf("%d failed login attempts in %s",
			len(pruned), l.Window),
		Detail: strings.Join([]string{
			fmt.Sprintf("attempts=%d", len(pruned)),
			fmt.Sprintf("window=%s", l.Window),
			fmt.Sprintf("ip=%s", ip),
		}, " "),
	})
	if srv.Logger != nil {
		srv.Logger.Warn("login brute-force detected",
			"email", email, "ip", ip, "attempts", len(pruned))
	}

	// 异步发告警邮件给 ops 收件人. 不卡 login 响应 (HTTP request context
	// 可能要 close), 用独立 ctx + goroutine. 失败只 log warn, audit 已写.
	go sendBruteForceAlert(srv, email, ip, ua, len(pruned), l.Window)
}

// sendBruteForceAlert 读 alert.email 配置, 给所有 ops 收件人发告警邮件.
// 复用告警邮件这条线 — 跟 alertmanager 推过来的服务降级邮件走同一收件箱,
// 运维一处看. 配置缺 / 关 / SMTP 错都 fail-open: log warn, 流程不阻塞.
func sendBruteForceAlert(srv *Server, email, ip, ua string, count int, window time.Duration) {
	if srv == nil || srv.SystemConfig == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	entry, err := srv.SystemConfig.Get(ctx, "alert.email")
	if err != nil || entry == nil {
		return
	}
	var cfg admin.AlertEmailConfig
	if err := json.Unmarshal(entry.Value, &cfg); err != nil {
		if srv.Logger != nil {
			srv.Logger.Warn("brute-force alert: parse alert.email failed", "err", err)
		}
		return
	}
	if !cfg.Enabled || cfg.SMTPHost == "" || cfg.From == "" || len(cfg.To) == 0 {
		return
	}

	subject := fmt.Sprintf("[BiuMind 安全告警] 暴力破解检测: %s", email)
	body := fmt.Sprintf(`<!doctype html><html lang="zh-CN"><body style="font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Helvetica,Arial,sans-serif;background:#f6f8fa;padding:24px">
<div style="max-width:520px;margin:0 auto;background:#fff;border-radius:8px;padding:32px;box-shadow:0 1px 3px rgba(0,0,0,0.06)">
<h2 style="margin:0 0 16px 0;color:#cf222e">🚨 暴力破解登录尝试</h2>
<p style="color:#57606a;line-height:1.6">检测到 BiuMind 账户在短时间内累计多次登录失败. 请确认是否为合法用户操作; 若不是请考虑临时禁用账户或封 IP.</p>
<table style="width:100%%;border-collapse:collapse;margin:16px 0">
  <tr><td style="padding:8px 0;color:#57606a;width:120px">账户邮箱</td><td style="font-family:'SF Mono',Menlo,Consolas,monospace">%s</td></tr>
  <tr><td style="padding:8px 0;color:#57606a">来源 IP</td><td style="font-family:'SF Mono',Menlo,Consolas,monospace">%s</td></tr>
  <tr><td style="padding:8px 0;color:#57606a">User-Agent</td><td style="font-family:'SF Mono',Menlo,Consolas,monospace;font-size:12px;word-break:break-all">%s</td></tr>
  <tr><td style="padding:8px 0;color:#57606a">失败次数</td><td><b>%d</b> 次 (窗口 %s)</td></tr>
  <tr><td style="padding:8px 0;color:#57606a">告警时间</td><td>%s</td></tr>
</table>
<hr style="margin:24px 0;border:none;border-top:1px solid #d0d7de">
<p style="color:#8c959f;font-size:12px;margin:0">由 identity LoginThrottle 触发. 详情查 audit (action=auth.login.brute_force).</p>
</div></body></html>`,
		htmlEscape(email), htmlEscape(ip), htmlEscape(ua), count, window, time.Now().Format(time.RFC3339))

	if err := mailer.Send(mailer.SMTPConfig{
		Host: cfg.SMTPHost, Port: cfg.SMTPPort,
		User: cfg.SMTPUser, Pass: cfg.SMTPPass,
		TLS: cfg.SMTPTLS, From: cfg.From,
	}, cfg.To, subject, body); err != nil {
		if srv.Logger != nil {
			srv.Logger.Warn("brute-force alert email send failed",
				"err", err, "email", email, "to_count", len(cfg.To))
		}
	} else if srv.Logger != nil {
		srv.Logger.Info("brute-force alert email sent",
			"email", email, "to_count", len(cfg.To))
	}
}

// htmlEscape — 邮件正文嵌的 email/IP/UA 来自请求, 防一手 XSS (邮件客户端
// 渲染 HTML, 内容里有 <script> / <img onerror=...> 会被执行). strings.Replacer
// 比 html/template 轻很多, 这里就 5 个字符够用.
func htmlEscape(s string) string {
	return strings.NewReplacer(
		"&", "&amp;", "<", "&lt;", ">", "&gt;",
		"\"", "&quot;", "'", "&#39;",
	).Replace(s)
}

// recordSuccess 清掉该 email 的失败记录 — 合法登录后阈值重置.
func (l *LoginThrottle) recordSuccess(email, ip string) {
	if email == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.failures, email)
	// lastAlert 不删 — 冷却期内连成 1 次也不再告警 (防抖)
	_ = ip
}

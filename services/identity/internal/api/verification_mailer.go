// verification_mailer — 注册邮箱验证邮件发送 + dev 兜底.
//
// 配置走 system_config key='auth.email' (跟 alert.email 同 schema, 不同 key).
// SMTP 字段缺失或 enabled=false → dev 兜底: code 写日志, 返回 nil. 让本地
// 测试 / 没配 SMTP 的环境注册流程不阻塞 — 操作员从 docker logs identity 拿 code.

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/biumind/biumind/services/identity/internal/admin"
	"github.com/biumind/biumind/services/identity/internal/mailer"
)

// authEmailConfig — 反序列化自 biumind_system.config (key='auth.email').
// 字段命名跟 admin.AlertEmailConfig 一致 (复用调用方对运维 UI 的肌肉记忆).
type authEmailConfig struct {
	Enabled        bool   `json:"enabled"`
	SMTPHost       string `json:"smtp_host"`
	SMTPPort       int    `json:"smtp_port"`
	SMTPUser       string `json:"smtp_user"`
	SMTPPass       string `json:"smtp_pass"`
	SMTPTLS        bool   `json:"smtp_tls"`
	From           string `json:"from"`
	CodeTTLSeconds int    `json:"code_ttl_seconds"` // ≤0 时调用方走 default
	Subject        string `json:"subject"`
}

// VerificationMailer 把"读 auth.email + 发 code 邮件"封一层. 注入到
// api.Server 的可选依赖. nil 时所有调用走 dev 兜底 (code 写日志).
type VerificationMailer struct {
	Cfg    *admin.SystemConfigStore
	Logger *slog.Logger
}

// CodePurpose 区分 code 的用途, 决定邮件主题 + 正文 + 日志标签.
type CodePurpose int

const (
	PurposeEmailVerification CodePurpose = iota
	PurposePasswordReset
)

// SendCode 向 toEmail 发送 code. 返回 (sent, err):
//   - sent=true, err=nil → SMTP 真发了
//   - sent=false, err=nil → dev 兜底 (config 缺失或 enabled=false), code 已写日志
//   - sent=false, err≠nil → 配置加载失败 / SMTP 投递失败, 调用方应返 5xx
//
// 调用方拿到 code 仍要写库 (不依赖发件成功); 即使发件失败用户也能用日志中
// 的 code 完成验证 (dev) 或 resend (prod).
func (m *VerificationMailer) SendCode(ctx context.Context, toEmail, code string, purpose CodePurpose) (bool, error) {
	if m == nil || m.Cfg == nil {
		m.devLog(toEmail, code, purpose, "no system config injected")
		return false, nil
	}
	entry, err := m.Cfg.Get(ctx, "auth.email")
	if err != nil {
		return false, fmt.Errorf("load auth.email config: %w", err)
	}
	if entry == nil {
		m.devLog(toEmail, code, purpose, "auth.email key missing")
		return false, nil
	}
	var cfg authEmailConfig
	if err := json.Unmarshal(entry.Value, &cfg); err != nil {
		return false, fmt.Errorf("parse auth.email: %w", err)
	}
	if !cfg.Enabled {
		m.devLog(toEmail, code, purpose, "auth.email disabled")
		return false, nil
	}
	if cfg.SMTPHost == "" || cfg.From == "" {
		m.devLog(toEmail, code, purpose, "auth.email incomplete (host/from)")
		return false, nil
	}

	subject, body := buildEmailContent(cfg, code, purpose)
	if err := mailer.Send(mailer.SMTPConfig{
		Host: cfg.SMTPHost, Port: cfg.SMTPPort,
		User: cfg.SMTPUser, Pass: cfg.SMTPPass,
		TLS: cfg.SMTPTLS, From: cfg.From,
	}, []string{toEmail}, subject, body); err != nil {
		return false, fmt.Errorf("smtp: %w", err)
	}
	if m.Logger != nil {
		m.Logger.Info("auth code email sent", "to", toEmail, "purpose", purposeName(purpose))
	}
	return true, nil
}

// buildEmailContent 选 subject + body 模板.
func buildEmailContent(cfg authEmailConfig, code string, purpose CodePurpose) (subject, body string) {
	switch purpose {
	case PurposePasswordReset:
		subject = "[BiuMind] 重置密码验证码"
		body = buildPasswordResetBody(code)
	default: // PurposeEmailVerification
		subject = cfg.Subject
		if subject == "" {
			subject = "[BiuMind] 邮箱验证码"
		}
		body = buildVerificationBody(code)
	}
	return
}

func purposeName(p CodePurpose) string {
	switch p {
	case PurposePasswordReset:
		return "password_reset"
	default:
		return "email_verification"
	}
}

// CodeTTL — 业务层先调拿 TTL, 再传给 store.CreateEmailVerification. nil
// receiver / 配置缺失 / 字段 ≤0 → 默认 10 分钟.
func (m *VerificationMailer) CodeTTLSeconds(ctx context.Context) int {
	const def = 600
	if m == nil || m.Cfg == nil {
		return def
	}
	entry, err := m.Cfg.Get(ctx, "auth.email")
	if err != nil || entry == nil {
		return def
	}
	var cfg authEmailConfig
	if err := json.Unmarshal(entry.Value, &cfg); err != nil {
		return def
	}
	if cfg.CodeTTLSeconds > 0 {
		return cfg.CodeTTLSeconds
	}
	return def
}

func (m *VerificationMailer) devLog(toEmail, code string, purpose CodePurpose, reason string) {
	// 用 WARN 让 dev 启动日志里 code 显眼; 提示运维 prod 部署需配 SMTP.
	if m == nil || m.Logger == nil {
		return
	}
	tag := "EMAIL VERIFICATION CODE"
	if purpose == PurposePasswordReset {
		tag = "PASSWORD RESET CODE"
	}
	m.Logger.Warn(tag+" (dev fallback — SMTP not sent)",
		"to", toEmail, "code", code, "reason", reason, "purpose", purposeName(purpose),
		"hint", "set system_config 'auth.email'.enabled=true with SMTP creds for production")
}

// buildVerificationBody — 简单中文 HTML 模板. 突出 code 让用户 copy.
// prod 需要更花的 (logo / footer) 时改这里, 不影响业务流程.
func buildVerificationBody(code string) string {
	return fmt.Sprintf(`<!doctype html>
<html lang="zh-CN"><body style="font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Helvetica,Arial,sans-serif;background:#f6f8fa;padding:24px">
  <div style="max-width:480px;margin:0 auto;background:#fff;border-radius:8px;padding:32px;box-shadow:0 1px 3px rgba(0,0,0,0.06)">
    <h2 style="margin:0 0 16px 0;color:#1f2328">验证您的 BiuMind 邮箱</h2>
    <p style="color:#57606a;line-height:1.6">您正在注册 BiuMind 账户. 请在 10 分钟内输入下方验证码完成注册:</p>
    <div style="margin:24px 0;padding:16px 20px;background:#f6f8fa;border-radius:6px;text-align:center">
      <span style="font-family:'SF Mono',Menlo,Consolas,monospace;font-size:32px;letter-spacing:8px;color:#0969da;font-weight:600">%s</span>
    </div>
    <p style="color:#57606a;font-size:13px;line-height:1.6">如果您没有发起此次注册, 请直接忽略本邮件 — 您的邮箱地址不会被使用.</p>
    <hr style="margin:24px 0;border:none;border-top:1px solid #d0d7de">
    <p style="color:#8c959f;font-size:12px;margin:0">此邮件由系统自动发送, 请勿回复.</p>
  </div>
</body></html>`, code)
}

// buildPasswordResetBody — 密码重置专用模板. 标橘色 + 警告"未发起请忽略",
// 防钓鱼意识 (重置邮件被滥用是钓鱼常见入口).
func buildPasswordResetBody(code string) string {
	return fmt.Sprintf(`<!doctype html>
<html lang="zh-CN"><body style="font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Helvetica,Arial,sans-serif;background:#f6f8fa;padding:24px">
  <div style="max-width:480px;margin:0 auto;background:#fff;border-radius:8px;padding:32px;box-shadow:0 1px 3px rgba(0,0,0,0.06)">
    <h2 style="margin:0 0 16px 0;color:#bc4c00">重置您的 BiuMind 密码</h2>
    <p style="color:#57606a;line-height:1.6">收到此邮件是因为有人 (希望是您本人) 请求重置 BiuMind 账户密码. 请在 10 分钟内输入下方验证码完成重置:</p>
    <div style="margin:24px 0;padding:16px 20px;background:#fff8e1;border-radius:6px;text-align:center;border:1px solid #fbca77">
      <span style="font-family:'SF Mono',Menlo,Consolas,monospace;font-size:32px;letter-spacing:8px;color:#bc4c00;font-weight:600">%s</span>
    </div>
    <p style="color:#cf222e;font-size:13px;line-height:1.6"><b>如果您没有请求重置密码, 请立即忽略本邮件并检查账户安全</b> — 仅有持验证码者可以更改密码.</p>
    <p style="color:#57606a;font-size:13px;line-height:1.6">重置成功后所有已登录设备将被强制下线, 您需要用新密码重新登录.</p>
    <hr style="margin:24px 0;border:none;border-top:1px solid #d0d7de">
    <p style="color:#8c959f;font-size:12px;margin:0">此邮件由系统自动发送, 请勿回复.</p>
  </div>
</body></html>`, code)
}

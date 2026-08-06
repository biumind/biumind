// Package mailer — 进程内 SMTP 发件 helper.
//
// 提取自原 admin/alert_webhook.go 的 sendMailTLS, 给 api (邮箱验证) +
// admin (Alertmanager 邮件) 复用. 不引外部依赖, 只用 net/smtp + crypto/tls.
//
// 设计:
//   - SMTPConfig 跟 system_config 里的 alert.email / auth.email 字段同型,
//     调用方各自反序列化后塞进来即可
//   - Send 同步阻塞; 调用方决定是否丢 goroutine (发件慢 + 失败需要 audit)
//   - TLS 模式: 隐式 TLS (465) 直接 tls.Dial; 否则走 STARTTLS (net/smtp 自动协商)
package mailer

import (
	"crypto/tls"
	"fmt"
	"net/smtp"
	"strings"
)

// SMTPConfig — 必填 Host + From + 至少一个 To. 其他字段允许空 (无认证 / 明文 / 25 端口).
type SMTPConfig struct {
	Host string
	Port int
	User string
	Pass string
	TLS  bool
	From string
}

// Send 发一封 HTML 邮件. subject 应预先做 RFC 2047 编码 (或调用方简单 ASCII).
// 实现按 cfg.TLS 选 SMTPS (465) 或 net/smtp 自动 STARTTLS.
func Send(cfg SMTPConfig, to []string, subject, htmlBody string) error {
	if cfg.Host == "" || cfg.From == "" || len(to) == 0 {
		return fmt.Errorf("mailer: incomplete config (host=%q from=%q to=%v)",
			cfg.Host, cfg.From, to)
	}
	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	var auth smtp.Auth
	if cfg.User != "" {
		auth = smtp.PlainAuth("", cfg.User, cfg.Pass, cfg.Host)
	}
	msg := fmt.Sprintf(
		"From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/html; charset=UTF-8\r\n\r\n%s",
		cfg.From, strings.Join(to, ","), subject, htmlBody,
	)
	if cfg.TLS {
		return sendMailTLS(addr, auth, cfg.From, to, []byte(msg), cfg.Host)
	}
	return smtp.SendMail(addr, auth, cfg.From, to, []byte(msg))
}

// sendMailTLS — 隐式 TLS (465). net/smtp 不直接支持 SMTPS, 自起 TLS conn.
func sendMailTLS(addr string, auth smtp.Auth, from string, to []string, msg []byte, hostname string) error {
	tlsConfig := &tls.Config{ServerName: hostname}
	conn, err := tls.Dial("tcp", addr, tlsConfig)
	if err != nil {
		return fmt.Errorf("tls dial: %w", err)
	}
	defer conn.Close()
	c, err := smtp.NewClient(conn, hostname)
	if err != nil {
		return fmt.Errorf("smtp client: %w", err)
	}
	defer c.Quit()
	if auth != nil {
		if err := c.Auth(auth); err != nil {
			return fmt.Errorf("auth: %w", err)
		}
	}
	if err := c.Mail(from); err != nil {
		return err
	}
	for _, t := range to {
		if err := c.Rcpt(t); err != nil {
			return err
		}
	}
	wc, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := wc.Write(msg); err != nil {
		return err
	}
	return wc.Close()
}

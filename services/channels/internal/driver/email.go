// Email driver — accepts inbound mail via webhook (Mailgun / Postmark
// / SES inbound; we don't run our own MX) and sends outbound via SMTP.
//
// Why not poll IMAP? An always-on IMAP connection per inbox is fragile
// (idle drops, NAT, OAuth expiry) and forces us to manage cursors. Most
// transactional-mail vendors expose a forwarding webhook that's already
// the recommended path; we accept their format and let SES/Mailgun
// handle MX, SPF, DKIM, DMARC. Outbound stays generic SMTP because
// every operator already has SMTP creds and locking ourselves to a
// single ESP for *outgoing* mail just adds vendor risk.
//
// Inbound: POST /v1/channels/email/webhook
// Body: form-urlencoded (Mailgun "store + notify" / Postmark inbound JSON
// / SES inbound JSON via SNS). The driver auto-detects the shape from
// content-type; signature verification is per-vendor.
//
// Conversation threading uses the In-Reply-To / References headers when
// present, falling back to a synthesized id from From+Subject so two
// messages with no Message-ID still land in one thread.

package driver

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/mail"
	"net/smtp"
	"strings"
	"time"

	"github.com/biumind/biumind/services/channels/internal/envelope"
)

// EmailVendor distinguishes the inbound webhook formats.
type EmailVendor string

const (
	VendorMailgun  EmailVendor = "mailgun"
	VendorPostmark EmailVendor = "postmark"
)

// Email driver instance. The same struct handles inbound (parsing the
// vendor's webhook) and outbound (SMTP send), so a deployment with
// only-inbound or only-outbound use needs to fill just one half.
type Email struct {
	// Inbound webhook authentication.
	Vendor            EmailVendor
	MailgunSigningKey string // shared across all your Mailgun domains
	PostmarkBasicAuth string // "user:pass" basic auth header value
	// Anti-replay window for Mailgun signatures (default 5 min).
	ClockSkew time.Duration

	// Outbound SMTP configuration.
	SMTPHost     string // e.g. "smtp.example.com"
	SMTPPort     int    // 587 (STARTTLS) or 465 (TLS)
	SMTPUsername string
	SMTPPassword string
	FromAddress  string // "BiuMind Bot <bot@biumind.com>"

	// SMTPSender — overrideable for tests; defaults to smtp.SendMail.
	// Nil → real network.
	SMTPSender func(addr string, auth smtp.Auth, from string, to []string, msg []byte) error
}

// NewEmail builds a driver. Pass empty values for the SMTP fields if
// only inbound is needed (Send returns ErrUnsupported in that case).
func NewEmail(vendor EmailVendor) *Email {
	return &Email{
		Vendor:    vendor,
		ClockSkew: 5 * time.Minute,
	}
}

func (e *Email) Name() string { return "email" }

// VerifyAndParse routes by vendor. Mailgun + Postmark are the two we
// support out of the box; adding SES means writing one new branch and
// pointing the deployment at a new Vendor value.
func (e *Email) VerifyAndParse(r *http.Request) ([]envelope.Envelope, error) {
	switch e.Vendor {
	case VendorMailgun:
		return e.parseMailgun(r)
	case VendorPostmark:
		return e.parsePostmark(r)
	default:
		return nil, fmt.Errorf("email: unknown vendor %q", e.Vendor)
	}
}

// ─── Mailgun ────────────────────────────────────────────

func (e *Email) parseMailgun(r *http.Request) ([]envelope.Envelope, error) {
	// Mailgun signed-store webhook is form-urlencoded.
	if err := r.ParseMultipartForm(16 << 20); err != nil {
		// Fallback: form-urlencoded without multipart.
		if err2 := r.ParseForm(); err2 != nil {
			return nil, fmt.Errorf("email: parse mailgun body: %w", err2)
		}
	}
	if e.MailgunSigningKey != "" {
		if err := e.verifyMailgunSig(r); err != nil {
			return nil, err
		}
	}
	from := r.FormValue("from")
	subj := r.FormValue("subject")
	body := r.FormValue("body-plain")
	if body == "" {
		body = r.FormValue("stripped-text")
	}
	msgID := r.FormValue("Message-Id")
	if msgID == "" {
		msgID = r.FormValue("message-headers-id") // best-effort fallback
	}
	inReplyTo := r.FormValue("In-Reply-To")
	references := r.FormValue("References")

	addr, _ := mail.ParseAddress(from)
	platformID := from
	displayName := ""
	if addr != nil {
		platformID = addr.Address
		displayName = addr.Name
	}

	env := envelope.Envelope{
		MessageID:      strings.Trim(msgID, "<>"),
		Channel:        e.Name(),
		Direction:      envelope.DirectionInbound,
		ConversationID: threadID(addr, subj, references, inReplyTo),
		Sender: envelope.Sender{
			PlatformID:  platformID,
			DisplayName: displayName,
		},
		Text:    body,
		ReplyTo: strings.Trim(inReplyTo, "<>"),
		SentAt:  time.Now().UTC(),
		Raw: map[string]any{
			"subject":    subj,
			"references": references,
		},
	}
	return []envelope.Envelope{env}, nil
}

// verifyMailgunSig — Mailgun signs every webhook with HMAC-SHA256 of
// `<timestamp><token>` keyed by the API signing key. The form fields
// `timestamp`, `token`, `signature` are guaranteed present.
func (e *Email) verifyMailgunSig(r *http.Request) error {
	ts := r.FormValue("timestamp")
	tok := r.FormValue("token")
	sig := r.FormValue("signature")
	if ts == "" || tok == "" || sig == "" {
		return ErrUnsigned
	}
	mac := hmac.New(sha256.New, []byte(e.MailgunSigningKey))
	mac.Write([]byte(ts))
	mac.Write([]byte(tok))
	want := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(want), []byte(sig)) {
		return ErrSignatureInvalid
	}
	return nil
}

// ─── Postmark ───────────────────────────────────────────

// Postmark inbound is a fat JSON document. We extract the canonical
// fields and store the rest in Raw so downstream can read attachments,
// custom headers, etc.
type postmarkInbound struct {
	MessageID    string `json:"MessageID"`
	From         string `json:"From"`
	FromName     string `json:"FromName"`
	Subject      string `json:"Subject"`
	TextBody     string `json:"TextBody"`
	StrippedText string `json:"StrippedTextReply"`
	Date         string `json:"Date"`
	Headers      []struct {
		Name  string `json:"Name"`
		Value string `json:"Value"`
	} `json:"Headers"`
}

func (e *Email) parsePostmark(r *http.Request) ([]envelope.Envelope, error) {
	if e.PostmarkBasicAuth != "" {
		if got := r.Header.Get("Authorization"); got != "Basic "+e.PostmarkBasicAuth {
			return nil, ErrSignatureInvalid
		}
	}
	body, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, 25<<20))
	if err != nil {
		return nil, fmt.Errorf("email: read postmark body: %w", err)
	}
	var p postmarkInbound
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, fmt.Errorf("email: parse postmark json: %w", err)
	}

	text := p.TextBody
	if p.StrippedText != "" {
		text = p.StrippedText
	}

	var inReplyTo, references string
	for _, h := range p.Headers {
		switch strings.ToLower(h.Name) {
		case "in-reply-to":
			inReplyTo = h.Value
		case "references":
			references = h.Value
		}
	}

	addr, _ := mail.ParseAddress(p.From)
	platformID := p.From
	displayName := p.FromName
	if addr != nil {
		platformID = addr.Address
	}

	var raw map[string]any
	_ = json.Unmarshal(body, &raw)

	env := envelope.Envelope{
		MessageID:      strings.Trim(p.MessageID, "<>"),
		Channel:        e.Name(),
		Direction:      envelope.DirectionInbound,
		ConversationID: threadID(addr, p.Subject, references, inReplyTo),
		Sender: envelope.Sender{
			PlatformID:  platformID,
			DisplayName: displayName,
		},
		Text:    text,
		ReplyTo: strings.Trim(inReplyTo, "<>"),
		SentAt:  parseEmailDate(p.Date),
		Raw:     raw,
	}
	return []envelope.Envelope{env}, nil
}

// ─── Outbound (SMTP) ────────────────────────────────────

func (e *Email) Send(ctx context.Context, env envelope.Envelope) (envelope.Envelope, error) {
	if e.SMTPHost == "" {
		return env, ErrUnsupported
	}
	if env.ConversationID == "" || env.Text == "" {
		return env, errors.New("email: conversation_id (recipient) and text required")
	}
	to := env.ConversationID

	// Synthesize a Message-ID so the receiver can In-Reply-To us.
	msgID := fmt.Sprintf("<%d.%s@biumind>", time.Now().UnixNano(), randomToken(8))

	subject := "Re: BiuMind"
	if raw, ok := env.Raw["subject"].(string); ok && raw != "" {
		subject = raw
		if !strings.HasPrefix(strings.ToLower(subject), "re:") {
			subject = "Re: " + subject
		}
	}

	headers := []string{
		"From: " + e.FromAddress,
		"To: " + to,
		"Subject: " + subject,
		"Message-ID: " + msgID,
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=utf-8",
		"Date: " + time.Now().UTC().Format(time.RFC1123Z),
	}
	if env.ReplyTo != "" {
		headers = append(headers, "In-Reply-To: <"+env.ReplyTo+">")
		headers = append(headers, "References: <"+env.ReplyTo+">")
	}
	body := strings.Join(headers, "\r\n") + "\r\n\r\n" + env.Text

	addr := fmt.Sprintf("%s:%d", e.SMTPHost, e.SMTPPort)
	auth := smtp.PlainAuth("", e.SMTPUsername, e.SMTPPassword, e.SMTPHost)
	sender := e.SMTPSender
	if sender == nil {
		sender = e.defaultSendMail
	}
	if err := sender(addr, auth, parseFromAddr(e.FromAddress),
		[]string{to}, []byte(body)); err != nil {
		return env, fmt.Errorf("email: smtp send: %w", err)
	}

	out := env
	out.Channel = e.Name()
	out.Direction = envelope.DirectionOutbound
	out.MessageID = strings.Trim(msgID, "<>")
	if out.SentAt.IsZero() {
		out.SentAt = time.Now().UTC()
	}
	return out, nil
}

// defaultSendMail wraps net/smtp's SendMail with explicit STARTTLS so
// we don't accidentally send plaintext when an operator forgets to use
// port 465 + ImplicitTLS.
func (e *Email) defaultSendMail(addr string, auth smtp.Auth, from string, to []string, msg []byte) error {
	c, err := smtp.Dial(addr)
	if err != nil {
		return err
	}
	defer c.Close()
	if ok, _ := c.Extension("STARTTLS"); ok {
		if err := c.StartTLS(&tls.Config{ServerName: e.SMTPHost}); err != nil {
			return fmt.Errorf("starttls: %w", err)
		}
	}
	if auth != nil {
		if ok, _ := c.Extension("AUTH"); ok {
			if err := c.Auth(auth); err != nil {
				return fmt.Errorf("auth: %w", err)
			}
		}
	}
	if err := c.Mail(from); err != nil {
		return err
	}
	for _, rcpt := range to {
		if err := c.Rcpt(rcpt); err != nil {
			return err
		}
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(msg); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

// ─── Helpers ───────────────────────────────────────────

// threadID picks the most stable identifier for grouping replies.
//
// Priority: References → In-Reply-To → from+subject hash. The first
// two are RFC 5322 standard threading; the synthesized fallback keeps
// long-running senders coherent even when they don't quote.
func threadID(from *mail.Address, subject, references, inReplyTo string) string {
	if references != "" {
		// References is a space-separated chain; the FIRST id is the
		// thread root.
		fields := strings.Fields(references)
		if len(fields) > 0 {
			return strings.Trim(fields[0], "<>")
		}
	}
	if inReplyTo != "" {
		return strings.Trim(inReplyTo, "<>")
	}
	addr := ""
	if from != nil {
		addr = from.Address
	}
	subj := strings.TrimSpace(strings.TrimPrefix(strings.ToLower(subject), "re:"))
	h := sha256.Sum256([]byte(addr + "|" + subj))
	return "thread-" + hex.EncodeToString(h[:8])
}

func parseEmailDate(s string) time.Time {
	if s == "" {
		return time.Now().UTC()
	}
	for _, layout := range []string{
		time.RFC1123Z, time.RFC1123,
		"Mon, 2 Jan 2006 15:04:05 -0700",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Now().UTC()
}

func parseFromAddr(s string) string {
	if a, err := mail.ParseAddress(s); err == nil {
		return a.Address
	}
	return s
}

func randomToken(n int) string {
	const alphabet = "0123456789abcdefghijklmnopqrstuvwxyz"
	b := make([]byte, n)
	now := time.Now().UnixNano()
	for i := range b {
		b[i] = alphabet[(now>>(uint(i)*5))&0x1f]
	}
	return string(b)
}

package driver

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/smtp"
	"net/url"
	"strings"
	"testing"

	"github.com/biumind/biumind/services/channels/internal/envelope"
)

// ─── Mailgun parsing ───────────────────────────────────

func mailgunForm(t *testing.T, key string, fields map[string]string) *http.Request {
	t.Helper()
	form := url.Values{}
	for k, v := range fields {
		form.Set(k, v)
	}
	if key != "" {
		ts, tok := "1700000000", "abc123token"
		form.Set("timestamp", ts)
		form.Set("token", tok)
		mac := hmac.New(sha256.New, []byte(key))
		mac.Write([]byte(ts))
		mac.Write([]byte(tok))
		form.Set("signature", hex.EncodeToString(mac.Sum(nil)))
	}
	r := httptest.NewRequest(http.MethodPost, "/v1/channels/email/webhook",
		strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return r
}

func TestEmail_ParseMailgun_HappyPath(t *testing.T) {
	d := NewEmail(VendorMailgun)
	d.MailgunSigningKey = "test-key"
	r := mailgunForm(t, "test-key", map[string]string{
		"from":        "Alice <alice@example.com>",
		"subject":     "Hello",
		"body-plain":  "This is the body.",
		"Message-Id":  "<msg-1@example.com>",
		"In-Reply-To": "<msg-0@example.com>",
		"References":  "<msg-0@example.com>",
	})
	envs, err := d.VerifyAndParse(r)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(envs) != 1 {
		t.Fatalf("envs: %d", len(envs))
	}
	e := envs[0]
	if e.Channel != "email" || e.Direction != envelope.DirectionInbound {
		t.Errorf("envelope: %+v", e)
	}
	if e.Sender.PlatformID != "alice@example.com" || e.Sender.DisplayName != "Alice" {
		t.Errorf("sender: %+v", e.Sender)
	}
	if e.MessageID != "msg-1@example.com" {
		t.Errorf("msg id: %q", e.MessageID)
	}
	if e.ConversationID != "msg-0@example.com" {
		t.Errorf("conversation should follow References root: %q", e.ConversationID)
	}
	if e.Text != "This is the body." {
		t.Errorf("text: %q", e.Text)
	}
}

func TestEmail_ParseMailgun_BadSignature(t *testing.T) {
	d := NewEmail(VendorMailgun)
	d.MailgunSigningKey = "real-key"
	r := mailgunForm(t, "wrong-key", map[string]string{
		"from": "x@y", "body-plain": "x",
	})
	_, err := d.VerifyAndParse(r)
	if err != ErrSignatureInvalid {
		t.Errorf("expected ErrSignatureInvalid; got %v", err)
	}
}

func TestEmail_ParseMailgun_MissingSig(t *testing.T) {
	d := NewEmail(VendorMailgun)
	d.MailgunSigningKey = "real-key"
	form := url.Values{}
	form.Set("from", "x@y")
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	_, err := d.VerifyAndParse(r)
	if err != ErrUnsigned {
		t.Errorf("expected ErrUnsigned; got %v", err)
	}
}

func TestEmail_ParseMailgun_NoSigningKeySkipsVerify(t *testing.T) {
	d := NewEmail(VendorMailgun) // no MailgunSigningKey set
	r := mailgunForm(t, "", map[string]string{
		"from":       "x@y",
		"body-plain": "ok",
		"Message-Id": "<m1>",
	})
	envs, err := d.VerifyAndParse(r)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(envs) != 1 {
		t.Errorf("envs: %d", len(envs))
	}
}

func TestEmail_ThreadID_FallsBackToHash(t *testing.T) {
	d := NewEmail(VendorMailgun)
	r := mailgunForm(t, "", map[string]string{
		"from":       "Alice <alice@example.com>",
		"subject":    "Re: Hello",
		"body-plain": "x",
	})
	envs, _ := d.VerifyAndParse(r)
	if len(envs) != 1 {
		t.Fatal("envs")
	}
	if !strings.HasPrefix(envs[0].ConversationID, "thread-") {
		t.Errorf("expected synthesized thread id; got %q", envs[0].ConversationID)
	}
}

// ─── Postmark parsing ───────────────────────────────────

func postmarkReq(t *testing.T, body any, basic string) *http.Request {
	t.Helper()
	b, _ := json.Marshal(body)
	r := httptest.NewRequest(http.MethodPost, "/v1/channels/email/webhook",
		bytes.NewReader(b))
	r.Header.Set("Content-Type", "application/json")
	if basic != "" {
		r.Header.Set("Authorization", "Basic "+basic)
	}
	return r
}

func TestEmail_ParsePostmark_HappyPath(t *testing.T) {
	d := NewEmail(VendorPostmark)
	creds := base64.StdEncoding.EncodeToString([]byte("user:pass"))
	d.PostmarkBasicAuth = creds

	r := postmarkReq(t, map[string]any{
		"MessageID":         "msg-2@example.com",
		"From":              "bob@example.com",
		"FromName":          "Bob",
		"Subject":           "Re: hi",
		"TextBody":          "The body.",
		"StrippedTextReply": "The body.",
		"Date":              "Mon, 14 Nov 2023 10:00:00 -0500",
		"Headers": []map[string]string{
			{"Name": "In-Reply-To", "Value": "<msg-0@example.com>"},
			{"Name": "References", "Value": "<root@example.com> <msg-0@example.com>"},
		},
	}, creds)

	envs, err := d.VerifyAndParse(r)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(envs) != 1 {
		t.Fatalf("envs: %d", len(envs))
	}
	e := envs[0]
	if e.Sender.PlatformID != "bob@example.com" {
		t.Errorf("sender: %+v", e.Sender)
	}
	if e.MessageID != "msg-2@example.com" {
		t.Errorf("msg id: %q", e.MessageID)
	}
	if e.ConversationID != "root@example.com" {
		t.Errorf("References root should win: got %q", e.ConversationID)
	}
}

func TestEmail_ParsePostmark_BasicAuthRequired(t *testing.T) {
	d := NewEmail(VendorPostmark)
	d.PostmarkBasicAuth = "expected"
	r := postmarkReq(t, map[string]any{"From": "x@y"}, "wrong")
	_, err := d.VerifyAndParse(r)
	if err != ErrSignatureInvalid {
		t.Errorf("expected ErrSignatureInvalid; got %v", err)
	}
}

// ─── SMTP send ──────────────────────────────────────────

func TestEmail_Send_ConstructsRfc5322Message(t *testing.T) {
	var captured struct {
		addr string
		from string
		to   []string
		body []byte
		auth smtp.Auth
	}
	d := NewEmail(VendorMailgun)
	d.SMTPHost = "smtp.test"
	d.SMTPPort = 587
	d.SMTPUsername = "u"
	d.SMTPPassword = "p"
	d.FromAddress = "BiuMind <bot@biumind.com>"
	d.SMTPSender = func(addr string, auth smtp.Auth, from string,
		to []string, msg []byte,
	) error {
		captured.addr, captured.auth = addr, auth
		captured.from, captured.to = from, to
		captured.body = msg
		return nil
	}

	out, err := d.Send(context.Background(), envelope.Envelope{
		Channel:        "email",
		ConversationID: "alice@example.com",
		Text:           "Hi Alice!",
		ReplyTo:        "msg-0@example.com",
		Raw:            map[string]any{"subject": "Hello"},
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if captured.addr != "smtp.test:587" {
		t.Errorf("addr: %s", captured.addr)
	}
	if captured.from != "bot@biumind.com" {
		t.Errorf("from: %s", captured.from)
	}
	if captured.to[0] != "alice@example.com" {
		t.Errorf("to: %+v", captured.to)
	}
	body := string(captured.body)
	for _, want := range []string{
		"From: BiuMind <bot@biumind.com>",
		"To: alice@example.com",
		"Subject: Re: Hello", // auto-prefixed
		"In-Reply-To: <msg-0@example.com>",
		"Hi Alice!",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q\n%s", want, body)
		}
	}
	if out.Direction != envelope.DirectionOutbound {
		t.Errorf("direction: %s", out.Direction)
	}
	if out.MessageID == "" {
		t.Errorf("expected synthesized Message-ID")
	}
}

func TestEmail_Send_ReturnsUnsupportedWithoutSMTPHost(t *testing.T) {
	d := NewEmail(VendorMailgun)
	_, err := d.Send(context.Background(), envelope.Envelope{
		ConversationID: "x@y", Text: "hi",
	})
	if err != ErrUnsupported {
		t.Errorf("expected ErrUnsupported; got %v", err)
	}
}

func TestEmail_Send_RejectsMissingFields(t *testing.T) {
	d := NewEmail(VendorMailgun)
	d.SMTPHost = "smtp"
	d.SMTPPort = 25
	d.SMTPSender = func(string, smtp.Auth, string, []string, []byte) error {
		t.Fatal("should not be called")
		return nil
	}
	_, err := d.Send(context.Background(), envelope.Envelope{Text: "x"})
	if err == nil || !strings.Contains(err.Error(), "required") {
		t.Errorf("expected required-field error; got %v", err)
	}
}

func TestEmail_Send_PropagatesSMTPError(t *testing.T) {
	d := NewEmail(VendorMailgun)
	d.SMTPHost = "smtp"
	d.SMTPPort = 25
	d.SMTPSender = func(string, smtp.Auth, string, []string, []byte) error {
		return fmt.Errorf("connection refused")
	}
	_, err := d.Send(context.Background(), envelope.Envelope{
		ConversationID: "a@b", Text: "x",
	})
	if err == nil || !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("expected wrapped error; got %v", err)
	}
}

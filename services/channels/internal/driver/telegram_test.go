package driver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/biumind/biumind/services/channels/internal/envelope"
)

func TestTelegramVerifyAndParse_HappyPath(t *testing.T) {
	body := []byte(`{
		"update_id": 1,
		"message": {
			"message_id": 42,
			"from": {"id": 555, "is_bot": false, "first_name": "Alice", "username": "alice"},
			"chat": {"id": -100, "type": "supergroup", "title": "Devs"},
			"date": 1700000000,
			"text": "hello bots"
		}
	}`)
	d := NewTelegram("tok", "")
	req := httptest.NewRequest("POST", "/v1/channels/telegram/webhook", bytes.NewReader(body))
	envs, err := d.VerifyAndParse(req)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(envs) != 1 {
		t.Fatalf("want 1 envelope, got %d", len(envs))
	}
	e := envs[0]
	if e.Channel != "telegram" || e.Direction != envelope.DirectionInbound {
		t.Errorf("bad routing: %+v", e)
	}
	if e.Text != "hello bots" || e.Sender.PlatformID != "555" || e.Sender.DisplayName != "alice" {
		t.Errorf("bad message body: %+v", e)
	}
	if e.ConversationID != "-100" {
		t.Errorf("conversation_id = %q, want -100", e.ConversationID)
	}
	if e.MessageID != "42" {
		t.Errorf("message_id = %q, want 42", e.MessageID)
	}
	if e.Raw == nil {
		t.Errorf("raw payload should be preserved")
	}
}

func TestTelegramVerifyAndParse_SecretMatching(t *testing.T) {
	d := NewTelegram("tok", "shh")
	body := []byte(`{"update_id":1}`)
	// missing header
	req := httptest.NewRequest("POST", "/", bytes.NewReader(body))
	if _, err := d.VerifyAndParse(req); !errors.Is(err, ErrUnsigned) {
		t.Errorf("want ErrUnsigned, got %v", err)
	}
	// wrong header
	req = httptest.NewRequest("POST", "/", bytes.NewReader(body))
	req.Header.Set("X-Telegram-Bot-Api-Secret-Token", "nope")
	if _, err := d.VerifyAndParse(req); !errors.Is(err, ErrSignatureInvalid) {
		t.Errorf("want ErrSignatureInvalid, got %v", err)
	}
	// correct header
	req = httptest.NewRequest("POST", "/", bytes.NewReader(body))
	req.Header.Set("X-Telegram-Bot-Api-Secret-Token", "shh")
	envs, err := d.VerifyAndParse(req)
	if err != nil {
		t.Errorf("want nil err, got %v", err)
	}
	if len(envs) != 0 {
		t.Errorf("update without message should produce 0 envelopes, got %d", len(envs))
	}
}

func TestTelegramVerifyAndParse_NonMessageUpdateIgnored(t *testing.T) {
	d := NewTelegram("tok", "")
	body := []byte(`{"update_id":2,"edited_message":{"text":"x"}}`)
	req := httptest.NewRequest("POST", "/", bytes.NewReader(body))
	envs, err := d.VerifyAndParse(req)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(envs) != 0 {
		t.Errorf("non-message update should produce 0 envelopes, got %d", len(envs))
	}
}

func TestTelegramSend_PostsToBotAPIAndCapturesMessageID(t *testing.T) {
	// Fake api.telegram.org
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/sendMessage") {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		var got map[string]any
		_ = json.Unmarshal(body, &got)
		if got["chat_id"] != float64(-100) || got["text"] != "hi" {
			t.Errorf("bad body: %+v", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":7}}`))
	}))
	defer srv.Close()

	d := NewTelegram("tok", "")
	d.APIBase = srv.URL
	out, err := d.Send(context.Background(), envelope.Envelope{
		Channel:        "telegram",
		ConversationID: "-100",
		Text:           "hi",
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if out.MessageID != "7" || out.Direction != envelope.DirectionOutbound {
		t.Errorf("bad outbound: %+v", out)
	}
}

func TestTelegramSend_RejectsEmptyText(t *testing.T) {
	d := NewTelegram("tok", "")
	d.APIBase = "http://nowhere.invalid"
	if _, err := d.Send(context.Background(), envelope.Envelope{
		Channel: "telegram", ConversationID: "1",
	}); err == nil {
		t.Errorf("empty text should be rejected")
	}
}

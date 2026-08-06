package driver

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/biumind/biumind/services/channels/internal/envelope"
)

func mintDiscordKey(t *testing.T) (string, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	return hex.EncodeToString(pub), priv
}

func discordSign(priv ed25519.PrivateKey, ts string, body []byte) string {
	msg := append([]byte(ts), body...)
	return hex.EncodeToString(ed25519.Sign(priv, msg))
}

func TestDiscordVerifyAndParse_PingType1(t *testing.T) {
	pubHex, priv := mintDiscordKey(t)
	d, err := NewDiscord("Bot.x", pubHex)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"type":1}`)
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	req := httptest.NewRequest("POST", "/", bytes.NewReader(body))
	req.Header.Set("X-Signature-Timestamp", ts)
	req.Header.Set("X-Signature-Ed25519", discordSign(priv, ts, body))
	envs, err := d.VerifyAndParse(req)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(envs) != 1 || envs[0].Raw["_discord_ping"] != true {
		t.Errorf("ping sentinel missing: %+v", envs)
	}
}

func TestDiscordVerifyAndParse_BadSignature(t *testing.T) {
	pubHex, _ := mintDiscordKey(t)
	d, _ := NewDiscord("Bot.x", pubHex)
	body := []byte(`{"type":2}`)
	req := httptest.NewRequest("POST", "/", bytes.NewReader(body))
	req.Header.Set("X-Signature-Timestamp", "1700000000")
	req.Header.Set("X-Signature-Ed25519", strings.Repeat("00", 64))
	if _, err := d.VerifyAndParse(req); !errors.Is(err, ErrSignatureInvalid) {
		t.Errorf("want ErrSignatureInvalid, got %v", err)
	}
}

func TestDiscordVerifyAndParse_SlashCommand(t *testing.T) {
	pubHex, priv := mintDiscordKey(t)
	d, _ := NewDiscord("Bot.x", pubHex)
	body := []byte(`{
		"type": 2, "id": "intr1", "channel_id": "ch1",
		"member": {"user": {"id": "u1", "username": "alice", "bot": false}},
		"data": {"name": "hello", "options": [{"name":"who","value":"world"}]}
	}`)
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	req := httptest.NewRequest("POST", "/", bytes.NewReader(body))
	req.Header.Set("X-Signature-Timestamp", ts)
	req.Header.Set("X-Signature-Ed25519", discordSign(priv, ts, body))
	envs, err := d.VerifyAndParse(req)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(envs) != 1 {
		t.Fatalf("want 1 envelope, got %d", len(envs))
	}
	e := envs[0]
	if e.MessageID != "intr1" || e.ConversationID != "ch1" {
		t.Errorf("ids: %+v", e)
	}
	if e.Sender.PlatformID != "u1" || e.Sender.DisplayName != "alice" {
		t.Errorf("sender: %+v", e.Sender)
	}
	if !strings.Contains(e.Text, "/hello") || !strings.Contains(e.Text, "world") {
		t.Errorf("text: %q", e.Text)
	}
}

func TestDiscordSend(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/v10/channels/") {
			t.Errorf("path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bot xyz" {
			t.Errorf("auth: %s", r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{"id":"msg1"}`))
	}))
	defer srv.Close()
	d, _ := NewDiscord("xyz", "")
	d.APIBase = srv.URL
	out, err := d.Send(context.Background(), envelope.Envelope{
		Channel: "discord", ConversationID: "ch1", Text: "hi",
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if out.MessageID != "msg1" {
		t.Errorf("bad outbound: %+v", out)
	}
}

func TestNewDiscord_BadKey(t *testing.T) {
	if _, err := NewDiscord("tok", "not-hex"); err == nil {
		t.Errorf("bad hex should fail")
	}
	if _, err := NewDiscord("tok", "00aa"); err == nil {
		t.Errorf("short key should fail")
	}
}

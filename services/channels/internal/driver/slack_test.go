package driver

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/biumind/biumind/services/channels/internal/envelope"
)

const slackSecret = "test-slack-signing"

func slackSign(t *testing.T, body []byte, ts string) string {
	t.Helper()
	mac := hmac.New(sha256.New, []byte(slackSecret))
	fmt.Fprintf(mac, "v0:%s:", ts)
	mac.Write(body)
	return "v0=" + hex.EncodeToString(mac.Sum(nil))
}

func TestSlackVerifyAndParse_Message(t *testing.T) {
	body := []byte(`{"type":"event_callback","team_id":"T1","event_id":"E1",
		"event":{"type":"message","user":"U1","text":"hi","channel":"C1","ts":"1700000000.000123"}}`)
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	d := NewSlack("xoxb-x", slackSecret)
	req := httptest.NewRequest("POST", "/", bytes.NewReader(body))
	req.Header.Set("X-Slack-Request-Timestamp", ts)
	req.Header.Set("X-Slack-Signature", slackSign(t, body, ts))

	envs, err := d.VerifyAndParse(req)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(envs) != 1 || envs[0].ConversationID != "C1" || envs[0].Sender.PlatformID != "U1" {
		t.Errorf("bad envelope: %+v", envs)
	}
	if envs[0].MessageID != "1700000000.000123" {
		t.Errorf("ts not preserved: %s", envs[0].MessageID)
	}
}

func TestSlackVerifyAndParse_BadSig(t *testing.T) {
	body := []byte(`{"type":"event_callback","event":{"type":"message"}}`)
	d := NewSlack("xoxb", slackSecret)
	req := httptest.NewRequest("POST", "/", bytes.NewReader(body))
	req.Header.Set("X-Slack-Request-Timestamp", strconv.FormatInt(time.Now().Unix(), 10))
	req.Header.Set("X-Slack-Signature", "v0=deadbeef")
	if _, err := d.VerifyAndParse(req); !errors.Is(err, ErrSignatureInvalid) {
		t.Errorf("want ErrSignatureInvalid, got %v", err)
	}
}

func TestSlackVerifyAndParse_StaleTimestamp(t *testing.T) {
	body := []byte(`{}`)
	stale := strconv.FormatInt(time.Now().Unix()-3600, 10)
	d := NewSlack("xoxb", slackSecret)
	req := httptest.NewRequest("POST", "/", bytes.NewReader(body))
	req.Header.Set("X-Slack-Request-Timestamp", stale)
	req.Header.Set("X-Slack-Signature", slackSign(t, body, stale))
	if _, err := d.VerifyAndParse(req); !errors.Is(err, ErrSignatureInvalid) {
		t.Errorf("stale ts should reject as ErrSignatureInvalid, got %v", err)
	}
}

func TestSlackVerifyAndParse_URLChallenge(t *testing.T) {
	body := []byte(`{"type":"url_verification","challenge":"zyx987","token":""}`)
	d := NewSlack("", "") // no signing secret → skip verification
	req := httptest.NewRequest("POST", "/", bytes.NewReader(body))
	envs, err := d.VerifyAndParse(req)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(envs) != 1 || envs[0].Raw["_slack_challenge"] != "zyx987" {
		t.Errorf("challenge sentinel missing: %+v", envs)
	}
}

func TestSlackSend(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/api/chat.postMessage") {
			t.Errorf("path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer xoxb-test" {
			t.Errorf("auth: %s", r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{"ok":true,"ts":"1700000001.000001"}`))
	}))
	defer srv.Close()

	d := NewSlack("xoxb-test", slackSecret)
	d.APIBase = srv.URL
	out, err := d.Send(context.Background(), envelope.Envelope{
		Channel: "slack", ConversationID: "C1", Text: "hello",
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if out.MessageID != "1700000001.000001" || out.Direction != envelope.DirectionOutbound {
		t.Errorf("bad outbound: %+v", out)
	}
}

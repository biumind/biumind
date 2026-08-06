// Slack driver — Events API webhook in / chat.postMessage out.
//
// Inbound:
//
//	POST /v1/channels/slack/webhook
//	Body: Slack Event payload (events_api wrapper)
//	Headers:
//	  X-Slack-Request-Timestamp: <unix>
//	  X-Slack-Signature: v0=<hmac-sha256(signing_secret, "v0:" + ts + ":" + body)>
//	Slack also sends url_verification challenge requests on first setup;
//	we handle those by responding with the challenge token directly.
//
// Outbound:
//
//	POST https://slack.com/api/chat.postMessage
//	Authorization: Bearer <bot_token>
//	{ channel: <conversation_id>, text, thread_ts?: <reply_to> }
package driver

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/biumind/biumind/services/channels/internal/envelope"
)

type Slack struct {
	BotToken      string
	SigningSecret string
	APIBase       string
	Client        *http.Client
}

func NewSlack(botToken, signingSecret string) *Slack {
	return &Slack{
		BotToken:      botToken,
		SigningSecret: signingSecret,
		APIBase:       "https://slack.com",
		Client:        &http.Client{Timeout: 15 * time.Second},
	}
}

func (s *Slack) Name() string { return "slack" }

func (s *Slack) VerifyAndParse(r *http.Request) ([]envelope.Envelope, error) {
	// Read body once; we need it for both signature verification and
	// JSON decoding.
	body, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("slack: read body: %w", err)
	}

	if s.SigningSecret != "" {
		if err := s.verifySig(r, body); err != nil {
			return nil, err
		}
	}

	// url_verification challenge — must respond with the token directly,
	// but we surface that via error so the API layer can reply with
	// 200 + challenge body. Convention: parse but emit 0 envelopes,
	// caller writes back challenge from raw payload.
	var probe struct {
		Type      string `json:"type"`
		Challenge string `json:"challenge"`
	}
	_ = json.Unmarshal(body, &probe)
	if probe.Type == "url_verification" {
		// Encode challenge into a synthetic envelope's Raw so the API
		// handler can detect + respond. We use a sentinel kind that
		// the router knows to drop.
		return []envelope.Envelope{{
			Channel:   s.Name(),
			Direction: envelope.DirectionInbound,
			Raw:       map[string]any{"_slack_challenge": probe.Challenge},
		}}, nil
	}

	var event struct {
		Type    string `json:"type"`
		TeamID  string `json:"team_id"`
		EventID string `json:"event_id"`
		Event   struct {
			Type     string `json:"type"`
			User     string `json:"user"`
			Text     string `json:"text"`
			Channel  string `json:"channel"`
			Ts       string `json:"ts"`
			ThreadTs string `json:"thread_ts"`
			BotID    string `json:"bot_id"`
		} `json:"event"`
	}
	if err := json.Unmarshal(body, &event); err != nil {
		return nil, fmt.Errorf("slack: bad json: %w", err)
	}
	// We only emit envelopes for `event_callback` + inner `message`.
	if event.Type != "event_callback" || event.Event.Type != "message" {
		return nil, nil
	}
	if event.Event.BotID != "" {
		// Skip our own bot's messages.
		return nil, nil
	}

	var raw map[string]any
	_ = json.Unmarshal(body, &raw)

	env := envelope.Envelope{
		MessageID:      event.Event.Ts,
		Channel:        s.Name(),
		Direction:      envelope.DirectionInbound,
		ConversationID: event.Event.Channel,
		Text:           event.Event.Text,
		Sender: envelope.Sender{
			PlatformID: event.Event.User,
		},
		ReplyTo: event.Event.ThreadTs,
		SentAt:  parseSlackTs(event.Event.Ts),
		Raw:     raw,
	}
	return []envelope.Envelope{env}, nil
}

func (s *Slack) verifySig(r *http.Request, body []byte) error {
	ts := r.Header.Get("X-Slack-Request-Timestamp")
	sig := r.Header.Get("X-Slack-Signature")
	if ts == "" || sig == "" {
		return ErrUnsigned
	}
	// Reject replays older than 5 minutes (Slack's recommendation).
	if t, err := strconv.ParseInt(ts, 10, 64); err == nil {
		if abs(time.Now().Unix()-t) > 5*60 {
			return ErrSignatureInvalid
		}
	}
	mac := hmac.New(sha256.New, []byte(s.SigningSecret))
	fmt.Fprintf(mac, "v0:%s:", ts)
	mac.Write(body)
	want := "v0=" + hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(want), []byte(sig)) {
		return ErrSignatureInvalid
	}
	return nil
}

func (s *Slack) Send(ctx context.Context, e envelope.Envelope) (envelope.Envelope, error) {
	if e.ConversationID == "" || e.Text == "" {
		return e, fmt.Errorf("slack: conversation_id and text required")
	}
	body := map[string]any{
		"channel": e.ConversationID,
		"text":    e.Text,
	}
	if e.ReplyTo != "" {
		body["thread_ts"] = e.ReplyTo
	}
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		s.APIBase+"/api/chat.postMessage", bytes.NewReader(b))
	if err != nil {
		return e, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.BotToken)
	resp, err := s.Client.Do(req)
	if err != nil {
		return e, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	var out struct {
		OK    bool   `json:"ok"`
		Ts    string `json:"ts"`
		Error string `json:"error"`
	}
	_ = json.Unmarshal(respBody, &out)
	if !out.OK {
		return e, fmt.Errorf("slack: %s", out.Error)
	}
	o := e
	o.Channel = s.Name()
	o.Direction = envelope.DirectionOutbound
	o.MessageID = out.Ts
	if o.SentAt.IsZero() {
		o.SentAt = time.Now().UTC()
	}
	return o, nil
}

// parseSlackTs — Slack timestamps are "1700000000.000123" floats.
func parseSlackTs(s string) time.Time {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return time.Time{}
	}
	return time.Unix(int64(f), int64((f-float64(int64(f)))*1e9)).UTC()
}

func abs(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}

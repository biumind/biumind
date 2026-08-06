// Telegram driver — Bot API webhook in / sendMessage out.
//
// Inbound:
//
//	POST /v1/channels/telegram/webhook
//	Body: Telegram `Update` JSON
//	(Optional) Header `X-Telegram-Bot-Api-Secret-Token` matching the
//	secret you registered with `setWebhook`. We require it when
//	TELEGRAM_WEBHOOK_SECRET is configured.
//
// Outbound:
//
//	POST https://api.telegram.org/bot<TOKEN>/sendMessage
//	{ chat_id: <conversation_id as int>, text, reply_to_message_id?: <…> }
//
// References:
//
//	https://core.telegram.org/bots/api#update
//	https://core.telegram.org/bots/api#sendmessage
package driver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/biumind/biumind/services/channels/internal/envelope"
)

type Telegram struct {
	Token   string // bot token
	Secret  string // optional shared secret for webhook verification
	APIBase string // override for testing; defaults to https://api.telegram.org
	Client  *http.Client
}

func NewTelegram(token, secret string) *Telegram {
	return &Telegram{
		Token:   token,
		Secret:  secret,
		APIBase: "https://api.telegram.org",
		Client:  &http.Client{Timeout: 15 * time.Second},
	}
}

func (t *Telegram) Name() string { return "telegram" }

// telegramUpdate is the subset of the Update object we care about.
type telegramUpdate struct {
	UpdateID int64 `json:"update_id"`
	Message  *struct {
		MessageID int64 `json:"message_id"`
		From      *struct {
			ID        int64  `json:"id"`
			IsBot     bool   `json:"is_bot"`
			FirstName string `json:"first_name"`
			Username  string `json:"username"`
		} `json:"from"`
		Chat *struct {
			ID    int64  `json:"id"`
			Type  string `json:"type"`
			Title string `json:"title"`
		} `json:"chat"`
		Date           int64  `json:"date"`
		Text           string `json:"text"`
		ReplyToMessage *struct {
			MessageID int64 `json:"message_id"`
		} `json:"reply_to_message"`
	} `json:"message"`
}

func (t *Telegram) VerifyAndParse(r *http.Request) ([]envelope.Envelope, error) {
	if t.Secret != "" {
		got := r.Header.Get("X-Telegram-Bot-Api-Secret-Token")
		if got == "" {
			return nil, ErrUnsigned
		}
		if got != t.Secret {
			return nil, ErrSignatureInvalid
		}
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	// Preserve raw payload so the router can include it in audit logs.
	var raw map[string]any
	_ = json.Unmarshal(body, &raw)

	var u telegramUpdate
	if err := json.Unmarshal(body, &u); err != nil {
		return nil, fmt.Errorf("telegram: bad json: %w", err)
	}
	if u.Message == nil {
		// Edited messages, callback queries, channel posts, etc. fall
		// through. Future drivers will branch on update kind.
		return nil, nil
	}
	m := u.Message
	env := envelope.Envelope{
		MessageID:      strconv.FormatInt(m.MessageID, 10),
		Channel:        t.Name(),
		Direction:      envelope.DirectionInbound,
		ConversationID: chatIDString(m.Chat),
		Text:           m.Text,
		SentAt:         time.Unix(m.Date, 0).UTC(),
		Raw:            raw,
	}
	if m.From != nil {
		env.Sender = envelope.Sender{
			PlatformID:  strconv.FormatInt(m.From.ID, 10),
			DisplayName: firstNonEmpty(m.From.Username, m.From.FirstName),
			Bot:         m.From.IsBot,
		}
	}
	if m.ReplyToMessage != nil {
		env.ReplyTo = strconv.FormatInt(m.ReplyToMessage.MessageID, 10)
	}
	return []envelope.Envelope{env}, nil
}

func (t *Telegram) Send(ctx context.Context, e envelope.Envelope) (envelope.Envelope, error) {
	if e.ConversationID == "" {
		return e, fmt.Errorf("telegram: missing conversation_id")
	}
	if e.Text == "" {
		return e, fmt.Errorf("telegram: empty text not allowed")
	}
	chatID, err := strconv.ParseInt(e.ConversationID, 10, 64)
	if err != nil {
		return e, fmt.Errorf("telegram: bad conversation_id %q: %w", e.ConversationID, err)
	}
	body := map[string]any{
		"chat_id": chatID,
		"text":    e.Text,
	}
	if e.ReplyTo != "" {
		if rid, err := strconv.ParseInt(e.ReplyTo, 10, 64); err == nil {
			body["reply_to_message_id"] = rid
		}
	}
	b, _ := json.Marshal(body)

	url := t.APIBase + "/bot" + t.Token + "/sendMessage"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return e, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := t.Client.Do(req)
	if err != nil {
		return e, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return e, fmt.Errorf("telegram: send %d: %s", resp.StatusCode, string(respBody))
	}
	var sendResp struct {
		OK     bool `json:"ok"`
		Result struct {
			MessageID int64 `json:"message_id"`
		} `json:"result"`
	}
	_ = json.Unmarshal(respBody, &sendResp)
	if !sendResp.OK {
		return e, fmt.Errorf("telegram: send not ok: %s", string(respBody))
	}
	out := e
	out.Channel = t.Name()
	out.Direction = envelope.DirectionOutbound
	out.MessageID = strconv.FormatInt(sendResp.Result.MessageID, 10)
	if out.SentAt.IsZero() {
		out.SentAt = time.Now().UTC()
	}
	return out, nil
}

// ─── helpers ──────────────────────────────────────────────

func chatIDString(c *struct {
	ID    int64  `json:"id"`
	Type  string `json:"type"`
	Title string `json:"title"`
}) string {
	if c == nil {
		return ""
	}
	return strconv.FormatInt(c.ID, 10)
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

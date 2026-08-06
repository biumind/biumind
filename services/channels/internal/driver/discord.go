// Discord driver — Interactions endpoint webhook in / channel.send out.
//
// Inbound (Interactions API or Webhook events):
//
//	POST /v1/channels/discord/webhook
//	Body: Interaction or Message Component payload
//	Headers:
//	  X-Signature-Ed25519: <hex sig>
//	  X-Signature-Timestamp: <unix>
//	Discord uses Ed25519 signatures over (timestamp + body) verified with
//	the application's PUBLIC_KEY (not a shared secret).
//
// Outbound (REST API for posting messages):
//
//	POST https://discord.com/api/v10/channels/{channel_id}/messages
//	Authorization: Bot <bot_token>
//	{ content, message_reference?: {message_id, channel_id} }
package driver

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/biumind/biumind/services/channels/internal/envelope"
)

type Discord struct {
	BotToken  string
	PublicKey ed25519.PublicKey // 32 bytes
	APIBase   string
	Client    *http.Client
}

// NewDiscord — publicKeyHex is the 64-char hex string from Discord's
// Developer Portal → Application → General → Public Key.
func NewDiscord(botToken, publicKeyHex string) (*Discord, error) {
	var pk ed25519.PublicKey
	if publicKeyHex != "" {
		raw, err := hex.DecodeString(publicKeyHex)
		if err != nil {
			return nil, fmt.Errorf("discord: bad public key hex: %w", err)
		}
		if len(raw) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("discord: public key size %d, want %d",
				len(raw), ed25519.PublicKeySize)
		}
		pk = ed25519.PublicKey(raw)
	}
	return &Discord{
		BotToken:  botToken,
		PublicKey: pk,
		APIBase:   "https://discord.com",
		Client:    &http.Client{Timeout: 15 * time.Second},
	}, nil
}

func (d *Discord) Name() string { return "discord" }

func (d *Discord) VerifyAndParse(r *http.Request) ([]envelope.Envelope, error) {
	body, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, 4<<20))
	if err != nil {
		return nil, err
	}

	if d.PublicKey != nil {
		sigHex := r.Header.Get("X-Signature-Ed25519")
		ts := r.Header.Get("X-Signature-Timestamp")
		if sigHex == "" || ts == "" {
			return nil, ErrUnsigned
		}
		sig, err := hex.DecodeString(sigHex)
		if err != nil || len(sig) != ed25519.SignatureSize {
			return nil, ErrSignatureInvalid
		}
		// Discord signs (timestamp || body).
		msg := append([]byte(ts), body...)
		if !ed25519.Verify(d.PublicKey, msg, sig) {
			return nil, ErrSignatureInvalid
		}
	}

	var inter struct {
		Type      int    `json:"type"`
		ID        string `json:"id"`
		ChannelID string `json:"channel_id"`
		GuildID   string `json:"guild_id"`
		Member    *struct {
			User struct {
				ID       string `json:"id"`
				Username string `json:"username"`
				Bot      bool   `json:"bot"`
			} `json:"user"`
		} `json:"member"`
		User *struct {
			ID       string `json:"id"`
			Username string `json:"username"`
			Bot      bool   `json:"bot"`
		} `json:"user"`
		Data struct {
			Name    string `json:"name"`    // for slash commands
			Content string `json:"content"` // for message components
			Options []struct {
				Name  string `json:"name"`
				Value any    `json:"value"`
			} `json:"options"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &inter); err != nil {
		return nil, fmt.Errorf("discord: bad json: %w", err)
	}

	// Type 1 = PING (Discord's reachability test). The API layer
	// handles by checking for empty envelopes + responding with
	// {"type": 1}. We surface as a sentinel envelope.
	if inter.Type == 1 {
		return []envelope.Envelope{{
			Channel: d.Name(), Direction: envelope.DirectionInbound,
			Raw: map[string]any{"_discord_ping": true},
		}}, nil
	}

	// We treat both ApplicationCommand (2) and MessageComponent (3) as
	// inbound user actions. Pull the user info from member or user.
	var user struct {
		ID       string
		Username string
		Bot      bool
	}
	if inter.Member != nil {
		user.ID = inter.Member.User.ID
		user.Username = inter.Member.User.Username
		user.Bot = inter.Member.User.Bot
	} else if inter.User != nil {
		user.ID = inter.User.ID
		user.Username = inter.User.Username
		user.Bot = inter.User.Bot
	}
	if user.Bot || user.ID == "" {
		return nil, nil
	}

	// Compose text from the slash-command options if available.
	text := inter.Data.Content
	if text == "" && inter.Data.Name != "" {
		text = "/" + inter.Data.Name
		for _, opt := range inter.Data.Options {
			text += " " + fmt.Sprintf("%v", opt.Value)
		}
	}

	var raw map[string]any
	_ = json.Unmarshal(body, &raw)

	env := envelope.Envelope{
		MessageID:      inter.ID,
		Channel:        d.Name(),
		Direction:      envelope.DirectionInbound,
		ConversationID: inter.ChannelID,
		Text:           text,
		Sender: envelope.Sender{
			PlatformID:  user.ID,
			DisplayName: user.Username,
			Bot:         user.Bot,
		},
		SentAt: time.Now().UTC(),
		Raw:    raw,
	}
	return []envelope.Envelope{env}, nil
}

func (d *Discord) Send(ctx context.Context, e envelope.Envelope) (envelope.Envelope, error) {
	if e.ConversationID == "" || e.Text == "" {
		return e, fmt.Errorf("discord: conversation_id and text required")
	}
	body := map[string]any{"content": e.Text}
	if e.ReplyTo != "" {
		body["message_reference"] = map[string]any{
			"message_id": e.ReplyTo,
			"channel_id": e.ConversationID,
		}
	}
	b, _ := json.Marshal(body)
	url := d.APIBase + "/api/v10/channels/" + e.ConversationID + "/messages"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return e, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bot "+d.BotToken)
	resp, err := d.Client.Do(req)
	if err != nil {
		return e, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return e, fmt.Errorf("discord: send %d: %s", resp.StatusCode, string(respBody))
	}
	var out struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(respBody, &out)
	o := e
	o.Channel = d.Name()
	o.Direction = envelope.DirectionOutbound
	o.MessageID = out.ID
	if o.SentAt.IsZero() {
		o.SentAt = time.Now().UTC()
	}
	return o, nil
}

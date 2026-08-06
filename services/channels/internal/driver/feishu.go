// Feishu (Lark) driver — Open Platform event subscription in / im.message.send out.
//
// Inbound:
//
//	POST /v1/channels/feishu/webhook
//	Body: encrypted or plain JSON depending on the app's setting.
//	Verification token in the body's `token` field; we compare against
//	FEISHU_VERIFICATION_TOKEN. Encrypted payloads use AES-256-CBC keyed
//	by FEISHU_ENCRYPT_KEY (we support BOTH plain + encrypted modes).
//
// Outbound:
//
//	POST https://open.feishu.cn/open-apis/im/v1/messages?receive_id_type=open_id
//	Authorization: Bearer <tenant_access_token>     -- caller must refresh
//	{ receive_id, msg_type:"text", content:"{\"text\":\"...\"}" }
//
// We DON'T handle tenant_access_token refresh here — that needs persistent
// state (token+expiry) which belongs upstream. Caller passes a fresh token
// via `BotToken`. Production wires a TokenRotator; MVP stays stateless.
package driver

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/biumind/biumind/services/channels/internal/envelope"
)

type Feishu struct {
	BotToken          string // tenant_access_token; caller refreshes
	VerificationToken string // app's verification token
	EncryptKey        string // optional, when message_encrypt is on
	APIBase           string
	Client            *http.Client
}

func NewFeishu(botToken, verificationToken, encryptKey string) *Feishu {
	return &Feishu{
		BotToken:          botToken,
		VerificationToken: verificationToken,
		EncryptKey:        encryptKey,
		APIBase:           "https://open.feishu.cn",
		Client:            &http.Client{Timeout: 15 * time.Second},
	}
}

func (f *Feishu) Name() string { return "feishu" }

func (f *Feishu) VerifyAndParse(r *http.Request) ([]envelope.Envelope, error) {
	body, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, 4<<20))
	if err != nil {
		return nil, err
	}

	// Probe for encrypted payload first.
	var probe map[string]any
	_ = json.Unmarshal(body, &probe)
	if enc, ok := probe["encrypt"].(string); ok && enc != "" {
		if f.EncryptKey == "" {
			return nil, fmt.Errorf("feishu: encrypted payload but no FEISHU_ENCRYPT_KEY configured")
		}
		decrypted, err := feishuDecrypt(f.EncryptKey, enc)
		if err != nil {
			return nil, fmt.Errorf("feishu: decrypt: %w", err)
		}
		body = decrypted
		_ = json.Unmarshal(body, &probe)
	}

	// Verification token check (Feishu's request body always carries it
	// at top-level OR under "header.token" depending on schema version).
	tok := stringFromMaps(probe, "token", "header.token")
	if f.VerificationToken != "" && tok != f.VerificationToken {
		return nil, ErrSignatureInvalid
	}

	// URL verification challenge — schema_v1 sends {type:url_verification,
	// challenge:…}. We surface it via a sentinel envelope so the API
	// layer can echo the challenge back.
	if probe["type"] == "url_verification" {
		ch, _ := probe["challenge"].(string)
		return []envelope.Envelope{{
			Channel: f.Name(), Direction: envelope.DirectionInbound,
			Raw: map[string]any{"_feishu_challenge": ch},
		}}, nil
	}

	// Schema_v2: events live under {schema:"2.0", header:{event_type, ...},
	// event:{message:{message_id,chat_id,content}, sender:{sender_id:{open_id,…},sender_type}}}.
	var v2 struct {
		Schema string `json:"schema"`
		Header struct {
			EventType string `json:"event_type"`
			EventID   string `json:"event_id"`
			AppID     string `json:"app_id"`
		} `json:"header"`
		Event struct {
			Sender struct {
				SenderID struct {
					OpenID string `json:"open_id"`
				} `json:"sender_id"`
				SenderType string `json:"sender_type"`
				IsBot      bool   `json:"is_bot"`
			} `json:"sender"`
			Message struct {
				MessageID   string `json:"message_id"`
				ChatID      string `json:"chat_id"`
				ChatType    string `json:"chat_type"`
				MessageType string `json:"message_type"`
				CreateTime  string `json:"create_time"`
				Content     string `json:"content"` // JSON-encoded
				ParentID    string `json:"parent_id"`
			} `json:"message"`
		} `json:"event"`
	}
	if err := json.Unmarshal(body, &v2); err != nil {
		return nil, fmt.Errorf("feishu: bad json: %w", err)
	}
	if v2.Header.EventType != "im.message.receive_v1" {
		return nil, nil
	}
	if v2.Event.Sender.IsBot {
		return nil, nil
	}

	// Extract the visible text from the JSON content envelope.
	var contentObj map[string]any
	_ = json.Unmarshal([]byte(v2.Event.Message.Content), &contentObj)
	text, _ := contentObj["text"].(string)

	env := envelope.Envelope{
		MessageID:      v2.Event.Message.MessageID,
		Channel:        f.Name(),
		Direction:      envelope.DirectionInbound,
		ConversationID: v2.Event.Message.ChatID,
		Text:           text,
		Sender: envelope.Sender{
			PlatformID: v2.Event.Sender.SenderID.OpenID,
			Bot:        v2.Event.Sender.IsBot,
		},
		ReplyTo: v2.Event.Message.ParentID,
		SentAt:  time.Now().UTC(),
		Raw:     probe,
	}
	return []envelope.Envelope{env}, nil
}

func (f *Feishu) Send(ctx context.Context, e envelope.Envelope) (envelope.Envelope, error) {
	if e.ConversationID == "" || e.Text == "" {
		return e, errors.New("feishu: conversation_id + text required")
	}
	contentJSON, _ := json.Marshal(map[string]any{"text": e.Text})
	body := map[string]any{
		"receive_id": e.ConversationID,
		"msg_type":   "text",
		"content":    string(contentJSON),
	}
	b, _ := json.Marshal(body)
	url := f.APIBase + "/open-apis/im/v1/messages?receive_id_type=chat_id"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return e, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+f.BotToken)
	resp, err := f.Client.Do(req)
	if err != nil {
		return e, err
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	var out struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			MessageID string `json:"message_id"`
		} `json:"data"`
	}
	_ = json.Unmarshal(rb, &out)
	if out.Code != 0 {
		return e, fmt.Errorf("feishu: %d %s", out.Code, out.Msg)
	}
	o := e
	o.Channel = f.Name()
	o.Direction = envelope.DirectionOutbound
	o.MessageID = out.Data.MessageID
	if o.SentAt.IsZero() {
		o.SentAt = time.Now().UTC()
	}
	return o, nil
}

// feishuDecrypt — AES-256-CBC with key = sha256(EncryptKey), IV = first
// 16 bytes of the base64-decoded ciphertext (Feishu's spec).
func feishuDecrypt(key, b64 string) ([]byte, error) {
	cipherText, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, err
	}
	if len(cipherText) < aes.BlockSize {
		return nil, errors.New("ciphertext too short")
	}
	hashed := sha256.Sum256([]byte(key))
	block, err := aes.NewCipher(hashed[:])
	if err != nil {
		return nil, err
	}
	iv := cipherText[:aes.BlockSize]
	ct := cipherText[aes.BlockSize:]
	if len(ct)%aes.BlockSize != 0 {
		return nil, errors.New("ciphertext is not a multiple of the block size")
	}
	pt := make([]byte, len(ct))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(pt, ct)
	// PKCS#7 unpad.
	if len(pt) == 0 {
		return nil, errors.New("empty plaintext")
	}
	pad := int(pt[len(pt)-1])
	if pad > aes.BlockSize || pad > len(pt) {
		return nil, errors.New("bad padding")
	}
	return pt[:len(pt)-pad], nil
}

// stringFromMaps walks dot-notation paths in a nested map and returns the
// first non-empty string. Used to lift `token` out of multiple schema
// versions without committing to one.
func stringFromMaps(m map[string]any, paths ...string) string {
	for _, p := range paths {
		cur := any(m)
		for _, seg := range splitDots(p) {
			mm, ok := cur.(map[string]any)
			if !ok {
				cur = nil
				break
			}
			cur = mm[seg]
		}
		if s, ok := cur.(string); ok && s != "" {
			return s
		}
	}
	return ""
}

func splitDots(s string) []string {
	out, buf := []string{}, ""
	for _, r := range s {
		if r == '.' {
			out = append(out, buf)
			buf = ""
		} else {
			buf += string(r)
		}
	}
	if buf != "" {
		out = append(out, buf)
	}
	return out
}

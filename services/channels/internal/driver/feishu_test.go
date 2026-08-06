package driver

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/biumind/biumind/services/channels/internal/envelope"
)

const feishuVerify = "TestVerifyTokenABC"

func TestFeishuVerifyAndParse_PlainV2(t *testing.T) {
	body := []byte(`{
		"schema": "2.0",
		"header": {"event_type":"im.message.receive_v1","event_id":"ev1","app_id":"a1","token":"` + feishuVerify + `"},
		"event": {
			"sender": {"sender_id":{"open_id":"ou_x"},"sender_type":"user","is_bot":false},
			"message": {"message_id":"om_1","chat_id":"oc_1","chat_type":"p2p","message_type":"text","content":"{\"text\":\"hi\"}"}
		}
	}`)
	d := NewFeishu("token", feishuVerify, "")
	req := httptest.NewRequest("POST", "/", bytes.NewReader(body))
	envs, err := d.VerifyAndParse(req)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(envs) != 1 {
		t.Fatalf("want 1 envelope, got %d", len(envs))
	}
	e := envs[0]
	if e.MessageID != "om_1" || e.ConversationID != "oc_1" || e.Text != "hi" ||
		e.Sender.PlatformID != "ou_x" {
		t.Errorf("envelope: %+v", e)
	}
}

func TestFeishuVerifyAndParse_BadVerifyToken(t *testing.T) {
	body := []byte(`{
		"schema": "2.0",
		"header": {"event_type":"im.message.receive_v1","token":"WRONG"},
		"event": {"sender":{"sender_id":{"open_id":"x"}},"message":{"message_id":"m","chat_id":"c","content":"{}"}}
	}`)
	d := NewFeishu("tok", feishuVerify, "")
	req := httptest.NewRequest("POST", "/", bytes.NewReader(body))
	if _, err := d.VerifyAndParse(req); !errors.Is(err, ErrSignatureInvalid) {
		t.Errorf("want ErrSignatureInvalid, got %v", err)
	}
}

func TestFeishuVerifyAndParse_URLChallenge(t *testing.T) {
	body := []byte(`{"type":"url_verification","challenge":"abc123","token":"` + feishuVerify + `"}`)
	d := NewFeishu("tok", feishuVerify, "")
	req := httptest.NewRequest("POST", "/", bytes.NewReader(body))
	envs, err := d.VerifyAndParse(req)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(envs) != 1 || envs[0].Raw["_feishu_challenge"] != "abc123" {
		t.Errorf("challenge sentinel: %+v", envs)
	}
}

func TestFeishuVerifyAndParse_EncryptedRoundtrip(t *testing.T) {
	encryptKey := "biumind-feishu-encrypt-key-32"
	// Build a plain payload, encrypt it the same way Feishu would, send it.
	plain := []byte(`{"type":"url_verification","challenge":"enc-ok","token":"` + feishuVerify + `"}`)
	hashed := sha256.Sum256([]byte(encryptKey))
	block, err := aes.NewCipher(hashed[:])
	if err != nil {
		t.Fatal(err)
	}
	pad := aes.BlockSize - (len(plain) % aes.BlockSize)
	for i := 0; i < pad; i++ {
		plain = append(plain, byte(pad))
	}
	iv := make([]byte, aes.BlockSize)
	if _, err := rand.Read(iv); err != nil {
		t.Fatal(err)
	}
	ct := make([]byte, len(plain))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ct, plain)
	encrypted := append(iv, ct...)
	body := []byte(`{"encrypt":"` + base64.StdEncoding.EncodeToString(encrypted) + `"}`)

	d := NewFeishu("tok", feishuVerify, encryptKey)
	req := httptest.NewRequest("POST", "/", bytes.NewReader(body))
	envs, err := d.VerifyAndParse(req)
	if err != nil {
		t.Fatalf("encrypted parse: %v", err)
	}
	if len(envs) != 1 || envs[0].Raw["_feishu_challenge"] != "enc-ok" {
		t.Errorf("encrypted challenge: %+v", envs)
	}
}

func TestFeishuSend_RequiresFields(t *testing.T) {
	d := NewFeishu("tok", "", "")
	if _, err := d.Send(nil, envelope.Envelope{Channel: "feishu"}); err == nil {
		t.Errorf("missing fields should error")
	}
}

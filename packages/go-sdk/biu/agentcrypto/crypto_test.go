package agentcrypto

import (
	"bytes"
	"errors"
	"testing"
)

func TestCrypto_GenerateKeypair(t *testing.T) {
	priv, pub, err := GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	if len(priv) != X25519KeySize {
		t.Errorf("priv size=%d want %d", len(priv), X25519KeySize)
	}
	if len(pub) != X25519KeySize {
		t.Errorf("pub size=%d want %d", len(pub), X25519KeySize)
	}
	// 两次生成应该不同（rand 随机性）
	priv2, pub2, _ := GenerateKeypair()
	if bytes.Equal(priv, priv2) {
		t.Error("two generations produced same priv")
	}
	if bytes.Equal(pub, pub2) {
		t.Error("two generations produced same pub")
	}
}

func TestCrypto_PublicFromPrivate(t *testing.T) {
	priv, pub, err := GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	got, err := PublicFromPrivate(priv)
	if err != nil {
		t.Fatalf("PublicFromPrivate: %v", err)
	}
	if !bytes.Equal(got, pub) {
		t.Errorf("derived pubkey != original\n got=%x\nwant=%x", got, pub)
	}
	if _, err := PublicFromPrivate(priv[:10]); err == nil {
		t.Error("expected error on short privkey")
	}
}

func TestCrypto_RoundTrip(t *testing.T) {
	priv, pub, err := GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	plaintexts := [][]byte{
		[]byte(""),   // 空 payload
		[]byte("hi"), // 小
		[]byte(`{"api_key":"sk-secret","model":"x"}`), // 真实 work payload 形态
		bytes.Repeat([]byte("A"), 64*1024),            // 大（64KB）
	}
	for _, pt := range plaintexts {
		t.Run("", func(t *testing.T) {
			ct, err := EncryptForWorker(pub, pt)
			if err != nil {
				t.Fatalf("encrypt: %v", err)
			}
			// envelope 总长度 = overhead + plaintext
			if len(ct) != envelopeOverhead+len(pt) {
				t.Errorf("ct len=%d want %d", len(ct), envelopeOverhead+len(pt))
			}
			pt2, err := DecryptWithPrivkey(priv, ct)
			if err != nil {
				t.Fatalf("decrypt: %v", err)
			}
			if !bytes.Equal(pt, pt2) {
				t.Errorf("plaintext mismatch (len=%d): got %x want %x",
					len(pt), pt2[:min(20, len(pt2))], pt[:min(20, len(pt))])
			}
		})
	}
}

// 同一 payload 加密两次应得不同密文（ephemeral key + nonce 都随机），
// 决定性密文是降级攻击信号。
func TestCrypto_NonDeterministic(t *testing.T) {
	_, pub, _ := GenerateKeypair()
	plaintext := []byte("same input")
	c1, _ := EncryptForWorker(pub, plaintext)
	c2, _ := EncryptForWorker(pub, plaintext)
	if bytes.Equal(c1, c2) {
		t.Error("encrypt twice produced identical ciphertext (no random ephemeral / nonce)")
	}
}

func TestCrypto_WrongKeyFails(t *testing.T) {
	_, pubA, _ := GenerateKeypair()
	privB, _, _ := GenerateKeypair() // 不同密钥对的私钥

	ct, _ := EncryptForWorker(pubA, []byte("secret"))
	_, err := DecryptWithPrivkey(privB, ct)
	if !errors.Is(err, ErrAuthFailed) {
		t.Errorf("wrong key should ErrAuthFailed, got %v", err)
	}
}

func TestCrypto_TamperedFails(t *testing.T) {
	priv, pub, _ := GenerateKeypair()
	ct, _ := EncryptForWorker(pub, []byte("payload-to-tamper"))

	// 翻一个 ciphertext 字节（避开 Epub 头 32 + nonce 12 = 前 44 字节，
	// 改后面的 ciphertext 部分让 tag 校验必失败）
	mid := len(ct) / 2
	ct[mid] ^= 0x01
	_, err := DecryptWithPrivkey(priv, ct)
	if !errors.Is(err, ErrAuthFailed) {
		t.Errorf("tampered ct should ErrAuthFailed, got %v", err)
	}
}

func TestCrypto_TooShortFails(t *testing.T) {
	priv, _, _ := GenerateKeypair()
	cases := [][]byte{
		nil,                              // empty
		make([]byte, 32),                 // just Epub
		make([]byte, 32+12),              // Epub + nonce, no ct/tag
		make([]byte, envelopeOverhead-1), // one byte short
	}
	for _, c := range cases {
		if _, err := DecryptWithPrivkey(priv, c); err == nil {
			t.Errorf("len=%d should fail", len(c))
		}
	}
}

func TestCrypto_BadKeySize(t *testing.T) {
	_, err := EncryptForWorker([]byte("not-32-bytes"), []byte("x"))
	if err == nil {
		t.Error("non-32 pubkey should fail")
	}
	_, err = DecryptWithPrivkey([]byte("not-32-bytes"), make([]byte, 100))
	if err == nil {
		t.Error("non-32 privkey should fail")
	}
}

// min 是 Go 1.21 内置；测试代码为方便老版本兜底。
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

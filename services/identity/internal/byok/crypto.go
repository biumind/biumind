// Package byok — BYOK (Bring Your Own Key) 加密 / 解密辅助.
//
// AES-256-GCM 对称加密. 主密钥 (32 字节, base64) 从 env BYOK_MASTER_KEY 注入,
// 不进 git. nonce (12 字节) 每次写新生成, 与密文一并存; GCM 自带认证保密文
// 不被篡改.
//
// v2 接 KMS 信封加密时, 主密钥扩展成 (key_id → key_bytes) map, 旋转用 KEK 表
// 让历史密文仍可解.
//
// 设计: docs/BiuMind-Billing-Redesign.md §5.4 安全 / 合规.
package byok

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
)

const (
	// KeySize — AES-256 主密钥长度.
	KeySize = 32
	// NonceSize — GCM 标准 12 字节.
	NonceSize = 12
)

var (
	ErrInvalidMasterKey = errors.New("byok: master key must be 32 bytes (256-bit)")
	ErrShortCiphertext  = errors.New("byok: ciphertext too short")
	ErrDecryptFailed    = errors.New("byok: decrypt failed (wrong key or tampered ciphertext)")
)

// Cipher 持有 AES-GCM AEAD 实例. 跨请求复用即可, 内部线程安全.
type Cipher struct {
	aead cipher.AEAD
}

// NewCipherFromBase64 用 base64 编码的主密钥构造. 期望解码后正好 32 字节.
//
// 生成密钥示例:
//
//	openssl rand -base64 32
//	→ "abc...xyz=" (44 字符 base64)
func NewCipherFromBase64(b64 string) (*Cipher, error) {
	key, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, fmt.Errorf("byok: master key not base64: %w", err)
	}
	return NewCipher(key)
}

func NewCipher(key []byte) (*Cipher, error) {
	if len(key) != KeySize {
		return nil, ErrInvalidMasterKey
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Cipher{aead: aead}, nil
}

// Encrypt 返 (ciphertext, nonce, error). plaintext 长度任意.
// nonce 由 crypto/rand 生成, 调用方应跟 ciphertext 一并存进 DB.
func (c *Cipher) Encrypt(plaintext []byte) ([]byte, []byte, error) {
	nonce := make([]byte, NonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, fmt.Errorf("byok: rand nonce: %w", err)
	}
	// AEAD.Seal 签名 plaintext + 附加在密文末尾 (GCM 16 字节 tag); 不传 AD.
	ct := c.aead.Seal(nil, nonce, plaintext, nil)
	return ct, nonce, nil
}

// Decrypt 反向; 密钥不对或 ciphertext 被改 → ErrDecryptFailed.
func (c *Cipher) Decrypt(ciphertext, nonce []byte) ([]byte, error) {
	if len(ciphertext) < c.aead.Overhead() {
		return nil, ErrShortCiphertext
	}
	if len(nonce) != NonceSize {
		return nil, fmt.Errorf("byok: nonce size %d (want %d)", len(nonce), NonceSize)
	}
	pt, err := c.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, ErrDecryptFailed
	}
	return pt, nil
}

// Last4 返 plaintext 末 4 字符 (展示用 "sk-...AbCd"). 不足 4 字符返原样.
func Last4(plaintext string) string {
	if len(plaintext) <= 4 {
		return plaintext
	}
	return plaintext[len(plaintext)-4:]
}

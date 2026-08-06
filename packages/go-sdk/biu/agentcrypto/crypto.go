// Package agentcrypto — envelope encryption for Agent Plane work payloads.
//
// 共享包：brain 端加密、daemon(biu CLI，独立 module)端解密都 import 它
// （原在 services/brain/internal/agentplane/crypto.go，daemon 引不到 internal，
// 故 Runtime v3 R6.2 挪到 go-sdk 共享）。
//
// 为什么需要：worker 通过 NATS JetStream 收 work；即使 NATS 走 mTLS，broker
// 服务器还能看到明文（运维 / 落盘 / 多租户漏洞）。敏感字段（用户 BYOK API
// key 等）必须先在 brain 端加密，broker 拿到的是密文，只有持对应私钥的 worker
// 能解。
//
// 算法：X25519(ECDH) + HKDF-SHA256 + ChaCha20-Poly1305(AEAD)。NaCl box / age
// 同款选择——性能高、密钥短(32B)便于走 DB BYTEA / NATS 头。
//
// Wire 格式：
//
//	┌────────┬───────┬──────────────┬─────┐
//	│ Epub   │ nonce │  ciphertext  │ tag │
//	│ 32 B   │ 12 B  │  N B         │ 16 B│
//	└────────┴───────┴──────────────┴─────┘
//
// Epub —— ephemeral X25519 公钥（每条消息新生成；Epriv 用完即丢，前向安全）。
// 总开销 N + 60 字节常量。
package agentcrypto

import (
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/hkdf"
)

const (
	// X25519KeySize —— 标准 32 字节。
	X25519KeySize = 32
	nonceSize     = chacha20poly1305.NonceSize
	tagSize       = chacha20poly1305.Overhead
	// envelopeOverhead —— 32 + 12 + 16；校验最小密文长度。
	envelopeOverhead = X25519KeySize + nonceSize + tagSize
	// hkdfInfo —— 绑定本协议，防 cross-protocol 攻击。**改它会破坏 wire 兼容**。
	hkdfInfo = "biumind-agentplane-work-v1"
)

// ErrAuthFailed —— AEAD 校验失败的统一错误（密文损坏 / 私钥错 / 篡改都映射到
// 此，不暴露内部原因）。
var ErrAuthFailed = errors.New("agentcrypto: envelope authentication failed")

// GenerateKeypair 生成 X25519 密钥对。worker 启动时调一次，public 上报 brain，
// private 落盘（~/.biu/agentplane/privkey 0600）。返回 raw 32 字节。
func GenerateKeypair() (privkey, pubkey []byte, err error) {
	curve := ecdh.X25519()
	priv, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("agentcrypto: generate X25519 key: %w", err)
	}
	return priv.Bytes(), priv.PublicKey().Bytes(), nil
}

// PublicFromPrivate 从 raw 32B 私钥推导对应 X25519 公钥（worker 启动时从落盘
// 私钥还原 pubkey 用，省得再存一份）。
func PublicFromPrivate(privkey []byte) ([]byte, error) {
	if len(privkey) != X25519KeySize {
		return nil, fmt.Errorf("agentcrypto: privkey size %d, want %d", len(privkey), X25519KeySize)
	}
	priv, err := ecdh.X25519().NewPrivateKey(privkey)
	if err != nil {
		return nil, fmt.Errorf("agentcrypto: parse privkey: %w", err)
	}
	return priv.PublicKey().Bytes(), nil
}

// EncryptForWorker 用 worker 公钥加密 payload（每次新 ephemeral keypair，前向安全）。
func EncryptForWorker(workerPubKey, payload []byte) ([]byte, error) {
	if len(workerPubKey) != X25519KeySize {
		return nil, fmt.Errorf("agentcrypto: pubkey size %d, want %d", len(workerPubKey), X25519KeySize)
	}
	curve := ecdh.X25519()
	wPub, err := curve.NewPublicKey(workerPubKey)
	if err != nil {
		return nil, fmt.Errorf("agentcrypto: parse worker pubkey: %w", err)
	}
	ePriv, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("agentcrypto: gen ephemeral: %w", err)
	}
	shared, err := ePriv.ECDH(wPub)
	if err != nil {
		return nil, fmt.Errorf("agentcrypto: ecdh: %w", err)
	}
	key := make([]byte, chacha20poly1305.KeySize)
	if _, err := io.ReadFull(hkdf.New(sha256.New, shared, nil, []byte(hkdfInfo)), key); err != nil {
		return nil, fmt.Errorf("agentcrypto: hkdf: %w", err)
	}
	aead, err := chacha20poly1305.New(key)
	if err != nil {
		return nil, fmt.Errorf("agentcrypto: chacha20poly1305: %w", err)
	}
	nonce := make([]byte, nonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("agentcrypto: nonce: %w", err)
	}
	out := make([]byte, 0, envelopeOverhead+len(payload))
	out = append(out, ePriv.PublicKey().Bytes()...)
	out = append(out, nonce...)
	out = aead.Seal(out, nonce, payload, nil)
	return out, nil
}

// DecryptWithPrivkey 用 worker 私钥解 envelope。损坏 / 私钥错 / 篡改 → ErrAuthFailed。
func DecryptWithPrivkey(privkey, ciphertext []byte) ([]byte, error) {
	if len(privkey) != X25519KeySize {
		return nil, fmt.Errorf("agentcrypto: privkey size %d, want %d", len(privkey), X25519KeySize)
	}
	if len(ciphertext) < envelopeOverhead {
		return nil, fmt.Errorf("agentcrypto: ciphertext too short (%d < %d)", len(ciphertext), envelopeOverhead)
	}
	curve := ecdh.X25519()
	priv, err := curve.NewPrivateKey(privkey)
	if err != nil {
		return nil, fmt.Errorf("agentcrypto: parse privkey: %w", err)
	}
	ePub, err := curve.NewPublicKey(ciphertext[:X25519KeySize])
	if err != nil {
		return nil, fmt.Errorf("agentcrypto: parse epub: %w", err)
	}
	nonce := ciphertext[X25519KeySize : X25519KeySize+nonceSize]
	sealed := ciphertext[X25519KeySize+nonceSize:]
	shared, err := priv.ECDH(ePub)
	if err != nil {
		return nil, fmt.Errorf("agentcrypto: ecdh: %w", err)
	}
	key := make([]byte, chacha20poly1305.KeySize)
	if _, err := io.ReadFull(hkdf.New(sha256.New, shared, nil, []byte(hkdfInfo)), key); err != nil {
		return nil, fmt.Errorf("agentcrypto: hkdf: %w", err)
	}
	aead, err := chacha20poly1305.New(key)
	if err != nil {
		return nil, fmt.Errorf("agentcrypto: chacha20poly1305: %w", err)
	}
	plaintext, err := aead.Open(nil, nonce, sealed, nil)
	if err != nil {
		return nil, ErrAuthFailed
	}
	return plaintext, nil
}

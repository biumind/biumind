// refresh token rotation grace window 的对称加密 (AES-256-GCM)。
//
// rotate 成功时 handleRefresh 把新 refresh_token 明文加密存进被 revoke 老行
// 的 rotated_token_enc; 宽限窗口内老 token 被重放时, handleGraceReplay 沿
// rotated_to 链找到 head 后解密出 head token 明文返回给客户端。
//
// 密钥 32B, 由 DeriveGraceKey 从配置原料 (IDENTITY_REFRESH_GRACE_KEY /
// JWT_SECRET / RSA 私钥 PEM) 派生 — 跟 token_hash 一样, 明文 refresh_token
// 不以可逆形式落库, 这里存的是密文, 拖库拿不到可用 token。
package api

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"io"
)

// graceNonceSize — GCM 标准 nonce 长度, 随机生成后前置到密文。
const graceNonceSize = 12

// graceKeyDomain — 派生域分隔, 防止 grace key 跟其他从 JWT_SECRET 派生的
// 子密钥 (现在或将来) 撞车。
const graceKeyDomain = "biumind/refresh-grace-key\x00"

// errGraceDecrypt — 解密失败 (错 key / 密文被篡改 / 截断)。不区分细节,
// caller 一律按 grace replay 未命中处理。
var errGraceDecrypt = errors.New("grace: decrypt failed")

// DeriveGraceKey 从任意密钥原料派生 32B AES-256 key。domain 前缀做域分隔。
func DeriveGraceKey(material string) []byte {
	h := sha256.Sum256([]byte(graceKeyDomain + material))
	return h[:]
}

// encryptGrace AES-256-GCM 加密, 输出 = nonce(12B) || ciphertext || tag.
func encryptGrace(key, plaintext []byte) ([]byte, error) {
	gcm, err := graceGCM(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, graceNonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// decryptGrace 解 encryptGrace 的输出。
func decryptGrace(key, ct []byte) ([]byte, error) {
	gcm, err := graceGCM(key)
	if err != nil {
		return nil, err
	}
	if len(ct) < graceNonceSize+gcm.Overhead() {
		return nil, errGraceDecrypt
	}
	pt, err := gcm.Open(nil, ct[:graceNonceSize], ct[graceNonceSize:], nil)
	if err != nil {
		return nil, errGraceDecrypt
	}
	return pt, nil
}

func graceGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

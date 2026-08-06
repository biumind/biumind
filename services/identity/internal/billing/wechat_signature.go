// wechat_signature.go — 微信支付 v3 签名 / 验签 / 回调解密.
//
// 微信支付 v3 接口规范:
//   - 请求签名: 商户私钥 (RSA SHA256) → Authorization 头
//   - 回调验签: 平台公钥 (RSA SHA256) 校验 Wechatpay-Signature 头
//   - 回调内容: AES-256-GCM 解密 (key=APIv3Key, 32 字节)
//
// 文档: https://pay.weixin.qq.com/wiki/doc/apiv3/wechatpay/wechatpay4_0.shtml

package billing

import (
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ─── Sentinel errors ───────────────────────────────────

var (
	ErrWechatBadPrivateKey   = errors.New("wechat: invalid PEM private key")
	ErrWechatBadPublicKey    = errors.New("wechat: invalid PEM public key")
	ErrWechatBadSignature    = errors.New("wechat: signature verification failed")
	ErrWechatBadAPIv3Key     = errors.New("wechat: APIv3Key must be 32 bytes")
	ErrWechatTimestampSkew   = errors.New("wechat: callback timestamp skew > 5min")
	ErrWechatBadCiphertext   = errors.New("wechat: ciphertext decode failed")
)

// ─── PEM helpers ───────────────────────────────────────

// LoadWechatPrivateKey — 从 PEM 字符串 (PKCS1 或 PKCS8) 解析 RSA 私钥.
// 商户上传的 apiclient_key.pem 通常是 PKCS8 格式.
func LoadWechatPrivateKey(pemData string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemData))
	if block == nil {
		return nil, ErrWechatBadPrivateKey
	}
	// PKCS8 (常见于 apiclient_key.pem)
	if k, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		if rsaKey, ok := k.(*rsa.PrivateKey); ok {
			return rsaKey, nil
		}
		return nil, ErrWechatBadPrivateKey
	}
	// PKCS1 fallback
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return k, nil
	}
	return nil, ErrWechatBadPrivateKey
}

// LoadWechatPublicKey — 从 PEM 字符串解析 RSA 公钥 (平台证书 / 公钥).
func LoadWechatPublicKey(pemData string) (*rsa.PublicKey, error) {
	block, _ := pem.Decode([]byte(pemData))
	if block == nil {
		return nil, ErrWechatBadPublicKey
	}
	// X.509 证书 (常见于 wechatpay_xxx.pem)
	if cert, err := x509.ParseCertificate(block.Bytes); err == nil {
		if pub, ok := cert.PublicKey.(*rsa.PublicKey); ok {
			return pub, nil
		}
	}
	// 裸公钥 PKIX
	if k, err := x509.ParsePKIXPublicKey(block.Bytes); err == nil {
		if pub, ok := k.(*rsa.PublicKey); ok {
			return pub, nil
		}
	}
	return nil, ErrWechatBadPublicKey
}

// ─── 请求签名 ──────────────────────────────────────────

// SignRequest — 给微信 v3 请求生成 Authorization 头.
//
// message 拼接规则 (注意每行末尾 \n, 包括最后一行):
//
//	method + \n + canonicalURL + \n + timestamp + \n + nonce + \n + body + \n
//
// canonicalURL 是请求路径 + 查询串 (含 ?, 不含 host).
func SignRequest(method, canonicalURL, body string, privateKey *rsa.PrivateKey, mchID, certSerialNo string, now time.Time) (string, error) {
	timestamp := strconv.FormatInt(now.Unix(), 10)
	nonce, err := randomHex(16)
	if err != nil {
		return "", err
	}
	message := method + "\n" + canonicalURL + "\n" + timestamp + "\n" + nonce + "\n" + body + "\n"

	hashed := sha256.Sum256([]byte(message))
	sigBytes, err := rsa.SignPKCS1v15(rand.Reader, privateKey, crypto.SHA256, hashed[:])
	if err != nil {
		return "", fmt.Errorf("wechat sign: %w", err)
	}
	signature := base64.StdEncoding.EncodeToString(sigBytes)

	auth := fmt.Sprintf(
		`WECHATPAY2-SHA256-RSA2048 mchid="%s",nonce_str="%s",timestamp="%s",serial_no="%s",signature="%s"`,
		mchID, nonce, timestamp, certSerialNo, signature,
	)
	return auth, nil
}

// ─── 回调验签 ──────────────────────────────────────────

// VerifyCallbackSignature — 验证微信回调签名.
//
// 头部:
//   - Wechatpay-Timestamp
//   - Wechatpay-Nonce
//   - Wechatpay-Signature (base64 RSA-SHA256)
//   - Wechatpay-Serial (平台证书序列号 — 用来匹配公钥)
//
// message = timestamp + \n + nonce + \n + body + \n
//
// publicKey 是平台公钥 (按 Serial 找到的那张). now 用来检查 5 分钟时间窗口.
func VerifyCallbackSignature(timestamp, nonce, body, signatureB64 string, publicKey *rsa.PublicKey, now time.Time) error {
	if signatureB64 == "" || timestamp == "" || nonce == "" {
		return ErrWechatBadSignature
	}
	// 5 分钟时间窗口防重放.
	ts, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return fmt.Errorf("%w: bad timestamp", ErrWechatBadSignature)
	}
	if abs(now.Unix()-ts) > 300 {
		return ErrWechatTimestampSkew
	}

	sig, err := base64.StdEncoding.DecodeString(signatureB64)
	if err != nil {
		return fmt.Errorf("%w: signature b64", ErrWechatBadSignature)
	}
	message := timestamp + "\n" + nonce + "\n" + body + "\n"
	hashed := sha256.Sum256([]byte(message))
	if err := rsa.VerifyPKCS1v15(publicKey, crypto.SHA256, hashed[:], sig); err != nil {
		return ErrWechatBadSignature
	}
	return nil
}

// ─── AES-GCM 解密回调内容 ──────────────────────────────

// DecryptCallbackResource — 解密 resource 字段.
//
// 微信回调 body.resource:
//
//	{
//	  "ciphertext": "<base64>",
//	  "associated_data": "transaction",
//	  "nonce": "随机串",
//	  "original_type": "transaction",
//	  "algorithm": "AEAD_AES_256_GCM"
//	}
//
// apiKeyV3 必须是 32 字节. 解密结果是 JSON, 由调用方反序列化为
// 业务对象 (e.g. WechatTransaction).
func DecryptCallbackResource(apiKeyV3, ciphertextB64, associatedData, nonce string) ([]byte, error) {
	if len(apiKeyV3) != 32 {
		return nil, ErrWechatBadAPIv3Key
	}
	ciphertext, err := base64.StdEncoding.DecodeString(ciphertextB64)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrWechatBadCiphertext, err)
	}
	block, err := aes.NewCipher([]byte(apiKeyV3))
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(nonce) != gcm.NonceSize() {
		return nil, fmt.Errorf("%w: nonce len = %d, want %d", ErrWechatBadCiphertext, len(nonce), gcm.NonceSize())
	}
	plaintext, err := gcm.Open(nil, []byte(nonce), ciphertext, []byte(associatedData))
	if err != nil {
		return nil, fmt.Errorf("%w: gcm open: %v", ErrWechatBadCiphertext, err)
	}
	return plaintext, nil
}

// ─── helpers ───────────────────────────────────────────

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return strings.ToUpper(fmt.Sprintf("%x", b)), nil
}


// apple_iap.go — W6-4 Apple Server Notifications V2 + JWS receipt.
//
// Apple IAP v2 协议:
//   - Server Notifications: Apple POST 一个 JSON {signedPayload: <JWS>} 到我方
//     callback. JWS 头部 x5c 是 Apple 签发的证书链 (root → intermediate → leaf),
//     payload 是 ResponseBodyV2. 二级嵌套: data.signedTransactionInfo /
//     signedRenewalInfo 又是 JWS, 解码后是 JWSTransactionDecodedPayload.
//   - 客户端调 verifyReceipt: 客户端用 StoreKit 2 拿 JWS 后传给我方, 校验同上.
//
// 文档: https://developer.apple.com/documentation/appstoreservernotifications
//
// 本文件提供:
//   - ParseSignedPayload — 不验链, 直接解 JWS payload (测试 / dev 用)
//   - VerifySignedPayload — 用 Apple Root CA 验链 (生产用)
//   - DecodeServerNotification — 反序列化 ResponseBodyV2 + 嵌套 transaction info
//
// 真集成 Apple Root CA bundle 由 main.go 注入 (从 system_config.payment.apple_iap
// 的 root_cas_pem 读). dev / 单测可注入测试自签 CA.

package billing

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"
)

var (
	ErrAppleBadJWS         = errors.New("apple: malformed JWS")
	ErrAppleBadSignature   = errors.New("apple: signature verify failed")
	ErrAppleNoCertChain    = errors.New("apple: x5c header missing")
	ErrAppleCertChainBad   = errors.New("apple: cert chain not trusted")
)

// ─── JWS header / payload ──────────────────────

type appleJWSHeader struct {
	Alg string   `json:"alg"`
	Kid string   `json:"kid"`
	X5C []string `json:"x5c"` // base64-encoded DER cert chain
	Typ string   `json:"typ"`
}

// ParseJWS — split + base64 解 header / payload, 不验签.
//
// 返 (header, payloadBytes, signingInputBytes, signatureBytes).
func ParseJWS(token string) (*appleJWSHeader, []byte, []byte, []byte, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, nil, nil, nil, ErrAppleBadJWS
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("%w: header b64", ErrAppleBadJWS)
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("%w: payload b64", ErrAppleBadJWS)
	}
	sigBytes, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("%w: sig b64", ErrAppleBadJWS)
	}
	var h appleJWSHeader
	if err := json.Unmarshal(headerBytes, &h); err != nil {
		return nil, nil, nil, nil, fmt.Errorf("%w: header json", ErrAppleBadJWS)
	}
	signingInput := []byte(parts[0] + "." + parts[1])
	return &h, payloadBytes, signingInput, sigBytes, nil
}

// ─── 验签 ─────────────────────────────────────

// VerifyJWS — 用 x5c 链验. rootCerts 是受信 Apple Root CA. now 用作链验时间戳.
//
// 简化: 校验
//   1. x5c 链非空
//   2. leaf cert 验签 signing_input
//   3. leaf 链可被 rootCerts 之一验证 (Verify method)
//
// 不做 OCSP / CRL revocation; Apple Root 永不过期 (2035), 短期内没必要.
func VerifyJWS(token string, rootCerts []*x509.Certificate, now time.Time) ([]byte, error) {
	h, payload, signingInput, sig, err := ParseJWS(token)
	if err != nil {
		return nil, err
	}
	if len(h.X5C) == 0 {
		return nil, ErrAppleNoCertChain
	}
	leafDER, err := base64.StdEncoding.DecodeString(h.X5C[0])
	if err != nil {
		return nil, fmt.Errorf("%w: x5c b64", ErrAppleBadJWS)
	}
	leaf, err := x509.ParseCertificate(leafDER)
	if err != nil {
		return nil, fmt.Errorf("%w: x5c parse", ErrAppleBadJWS)
	}
	// 1. leaf 验签
	hashed := sha256DigestSafe(signingInput)
	switch h.Alg {
	case "ES256":
		pub, ok := leaf.PublicKey.(*ecdsa.PublicKey)
		if !ok {
			return nil, fmt.Errorf("%w: not ecdsa", ErrAppleBadSignature)
		}
		// JWS ES256 sig 是 r||s 各 32 字节; 转 ASN.1 让 ecdsa.VerifyASN1 用.
		if len(sig) != 64 {
			return nil, fmt.Errorf("%w: ES256 sig len=%d", ErrAppleBadSignature, len(sig))
		}
		if !ecdsaVerifyRawSig(pub, hashed, sig) {
			return nil, ErrAppleBadSignature
		}
	case "RS256":
		pub, ok := leaf.PublicKey.(*rsa.PublicKey)
		if !ok {
			return nil, fmt.Errorf("%w: not rsa", ErrAppleBadSignature)
		}
		if err := rsaVerifyPKCS1v15(pub, hashed, sig); err != nil {
			return nil, ErrAppleBadSignature
		}
	default:
		return nil, fmt.Errorf("%w: alg %s unsupported", ErrAppleBadSignature, h.Alg)
	}
	// 2. 链验证
	if len(rootCerts) > 0 {
		intermediates := x509.NewCertPool()
		for i := 1; i < len(h.X5C); i++ {
			der, err := base64.StdEncoding.DecodeString(h.X5C[i])
			if err != nil {
				continue
			}
			c, err := x509.ParseCertificate(der)
			if err != nil {
				continue
			}
			intermediates.AddCert(c)
		}
		roots := x509.NewCertPool()
		for _, r := range rootCerts {
			roots.AddCert(r)
		}
		opts := x509.VerifyOptions{
			Roots:         roots,
			Intermediates: intermediates,
			CurrentTime:   now,
		}
		if _, err := leaf.Verify(opts); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrAppleCertChainBad, err)
		}
	}
	return payload, nil
}

// ─── ResponseBodyV2 / TransactionInfo ──────

// AppleNotificationV2 — Apple Server Notifications v2 ResponseBodyV2 (顶层).
type AppleNotificationV2 struct {
	NotificationType string                  `json:"notificationType"`
	Subtype          string                  `json:"subtype,omitempty"`
	NotificationUUID string                  `json:"notificationUUID"`
	Data             AppleNotificationData   `json:"data"`
	Version          string                  `json:"version"`
	SignedDate       int64                   `json:"signedDate"`
}

type AppleNotificationData struct {
	BundleID                   string `json:"bundleId"`
	Environment                string `json:"environment"` // Sandbox / Production
	SignedRenewalInfo          string `json:"signedRenewalInfo,omitempty"`
	SignedTransactionInfo      string `json:"signedTransactionInfo,omitempty"`
	AppAppleID                 int64  `json:"appAppleId,omitempty"`
}

// AppleTransaction — JWSTransactionDecodedPayload 关键字段.
type AppleTransaction struct {
	TransactionID         string `json:"transactionId"`
	OriginalTransactionID string `json:"originalTransactionId"`
	WebOrderLineItemID    string `json:"webOrderLineItemId,omitempty"`
	BundleID              string `json:"bundleId"`
	ProductID             string `json:"productId"`
	SubscriptionGroupID   string `json:"subscriptionGroupIdentifier,omitempty"`
	PurchaseDate          int64  `json:"purchaseDate"`
	OriginalPurchaseDate  int64  `json:"originalPurchaseDate"`
	ExpiresDate           int64  `json:"expiresDate,omitempty"`
	Type                  string `json:"type"` // Auto-Renewable Subscription / Non-Consumable / etc
	InAppOwnershipType    string `json:"inAppOwnershipType,omitempty"`
	Environment           string `json:"environment"`
}

// DecodeNotificationV2 — 接收 raw JWS, 不验签, 解出 notification 主体 + 嵌套
// transaction. 真生产路径调用方应先 VerifyJWS 确保来源正确.
func DecodeNotificationV2(signedPayload string) (*AppleNotificationV2, *AppleTransaction, error) {
	_, payload, _, _, err := ParseJWS(signedPayload)
	if err != nil {
		return nil, nil, err
	}
	var n AppleNotificationV2
	if err := json.Unmarshal(payload, &n); err != nil {
		return nil, nil, fmt.Errorf("apple notification decode: %w", err)
	}
	var tx *AppleTransaction
	if n.Data.SignedTransactionInfo != "" {
		_, txPayload, _, _, err := ParseJWS(n.Data.SignedTransactionInfo)
		if err == nil {
			var t AppleTransaction
			if err := json.Unmarshal(txPayload, &t); err == nil {
				tx = &t
			}
		}
	}
	return &n, tx, nil
}

// ─── crypto helpers (薄封装, 避免在 package 顶层重复 import) ──

func sha256DigestSafe(b []byte) []byte {
	h := sha256.Sum256(b)
	return h[:]
}

func rsaVerifyPKCS1v15(pub *rsa.PublicKey, hashed, sig []byte) error {
	return rsa.VerifyPKCS1v15(pub, crypto.SHA256, hashed, sig)
}

// ecdsaVerifyRawSig — JWS 用 r||s 形式 (各 32 字节), Apple ES256.
func ecdsaVerifyRawSig(pub *ecdsa.PublicKey, hashed, sig []byte) bool {
	if len(sig) != 64 {
		return false
	}
	r := new(big.Int).SetBytes(sig[:32])
	s := new(big.Int).SetBytes(sig[32:])
	return ecdsa.Verify(pub, hashed, r, s)
}

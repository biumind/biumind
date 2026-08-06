// alipay_signature.go — 支付宝 OpenAPI RSA2 (SHA256withRSA) 签名 / 验签.
//
// 协议:
//   - 公共参数 + biz_content 都参与签名
//   - 字段按 key 字典序排序后拼 "k1=v1&k2=v2..." 作为待签字符串
//   - signature = base64(rsa_sha256_sign(appPrivateKey, message))
//   - 回调用支付宝公钥 base64+SHA256withRSA verify
//
// 文档: https://opendocs.alipay.com/common/02kf5q

package billing

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
)

var (
	ErrAlipayBadPrivateKey = errors.New("alipay: invalid PEM private key")
	ErrAlipayBadPublicKey  = errors.New("alipay: invalid PEM public key")
	ErrAlipayBadSignature  = errors.New("alipay: signature verification failed")
)

// LoadAlipayPrivateKey — 从 PEM (PKCS1 / PKCS8) 解析 RSA 私钥.
// 支付宝开放平台下载的应用私钥通常是 PKCS8.
func LoadAlipayPrivateKey(pemData string) (*rsa.PrivateKey, error) {
	pemData = normalizeAlipayPEM(pemData, "PRIVATE KEY")
	block, _ := pem.Decode([]byte(pemData))
	if block == nil {
		return nil, ErrAlipayBadPrivateKey
	}
	if k, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		if rsaKey, ok := k.(*rsa.PrivateKey); ok {
			return rsaKey, nil
		}
	}
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return k, nil
	}
	return nil, ErrAlipayBadPrivateKey
}

// LoadAlipayPublicKey — 从 PEM 解析支付宝公钥.
func LoadAlipayPublicKey(pemData string) (*rsa.PublicKey, error) {
	pemData = normalizeAlipayPEM(pemData, "PUBLIC KEY")
	block, _ := pem.Decode([]byte(pemData))
	if block == nil {
		return nil, ErrAlipayBadPublicKey
	}
	if k, err := x509.ParsePKIXPublicKey(block.Bytes); err == nil {
		if pub, ok := k.(*rsa.PublicKey); ok {
			return pub, nil
		}
	}
	if cert, err := x509.ParseCertificate(block.Bytes); err == nil {
		if pub, ok := cert.PublicKey.(*rsa.PublicKey); ok {
			return pub, nil
		}
	}
	return nil, ErrAlipayBadPublicKey
}

// normalizeAlipayPEM — 用户从支付宝控制台粘贴的"裸 base64"补全 PEM 头尾.
// 已经带头尾的 PEM 不动.
func normalizeAlipayPEM(s, label string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "-----BEGIN") {
		return s
	}
	// 64 字符断行
	var lines []string
	for len(s) > 0 {
		n := 64
		if len(s) < n {
			n = len(s)
		}
		lines = append(lines, s[:n])
		s = s[n:]
	}
	return "-----BEGIN " + label + "-----\n" + strings.Join(lines, "\n") + "\n-----END " + label + "-----\n"
}

// SignAlipayParams — 生成参数签名.
//
// 排除字段: sign 自身, sign_type. 其他字段按 key 字典序拼 k=v&k=v 后签.
// 返回 base64 sign.
func SignAlipayParams(params map[string]string, privateKey *rsa.PrivateKey) (string, error) {
	msg := concatAlipayParams(params)
	hashed := sha256.Sum256([]byte(msg))
	sig, err := rsa.SignPKCS1v15(rand.Reader, privateKey, crypto.SHA256, hashed[:])
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(sig), nil
}

// VerifyAlipayCallback — 验证支付宝回调签名 (form 表单或 url.Values).
//
// 不需要任何字段除了 sign / sign_type / 其他全部按 key 字典序拼.
func VerifyAlipayCallback(params map[string]string, publicKey *rsa.PublicKey) error {
	signB64, ok := params["sign"]
	if !ok || signB64 == "" {
		return ErrAlipayBadSignature
	}
	msg := concatAlipayParams(params)
	hashed := sha256.Sum256([]byte(msg))
	sig, err := base64.StdEncoding.DecodeString(signB64)
	if err != nil {
		return ErrAlipayBadSignature
	}
	if err := rsa.VerifyPKCS1v15(publicKey, crypto.SHA256, hashed[:], sig); err != nil {
		return ErrAlipayBadSignature
	}
	return nil
}

// concatAlipayParams — 字段按 key 排序拼 "k=v&k=v...". 排除 sign / sign_type.
// value 不做 URL encode (这是支付宝 sign 规约).
func concatAlipayParams(params map[string]string) string {
	keys := make([]string, 0, len(params))
	for k, v := range params {
		if k == "sign" || k == "sign_type" || v == "" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+params[k])
	}
	return strings.Join(parts, "&")
}

// VerifyFormValues — 适配 url.Values 的便利函数.
func VerifyFormValues(form url.Values, publicKey *rsa.PublicKey) error {
	m := make(map[string]string, len(form))
	for k, v := range form {
		if len(v) > 0 {
			m[k] = v[0]
		}
	}
	return VerifyAlipayCallback(m, publicKey)
}

// FormatSignedQuery — 给客户端跳转用的 query 串 (带签名 + URL encode).
// 用在 page.pay / wap.pay 等需要前端跳转的场景.
func FormatSignedQuery(params map[string]string, privateKey *rsa.PrivateKey) (string, error) {
	sig, err := SignAlipayParams(params, privateKey)
	if err != nil {
		return "", err
	}
	params["sign"] = sig
	values := url.Values{}
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		values.Set(k, params[k])
	}
	return values.Encode(), nil
}

// hashHex — 测试帮手, 不是必需.
func hashHex(s string) string {
	h := sha256.Sum256([]byte(s))
	return fmt.Sprintf("%x", h)
}

var _ = hashHex // 防止 unused warning 当未来移走

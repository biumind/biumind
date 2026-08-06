// W6-4 Apple IAP — 10 测试.

package billing

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"testing"
	"time"
)

// ─── helpers ───────────────────────────────────

// signES256JWS — 拼一个 ES256 JWS, 模拟 Apple Server Notification.
func signES256JWS(t *testing.T, priv *ecdsa.PrivateKey, leafDER []byte, payload []byte) string {
	t.Helper()
	header := appleJWSHeader{
		Alg: "ES256", Typ: "JWT",
		X5C: []string{base64.StdEncoding.EncodeToString(leafDER)},
	}
	hdrBytes, _ := json.Marshal(header)
	hdrB64 := base64.RawURLEncoding.EncodeToString(hdrBytes)
	pldB64 := base64.RawURLEncoding.EncodeToString(payload)
	signingInput := hdrB64 + "." + pldB64
	hashed := sha256.Sum256([]byte(signingInput))
	r, s, err := ecdsa.Sign(rand.Reader, priv, hashed[:])
	if err != nil {
		t.Fatal(err)
	}
	// pad r/s to 32 bytes each
	rBytes := r.Bytes()
	sBytes := s.Bytes()
	sigRaw := make([]byte, 64)
	copy(sigRaw[32-len(rBytes):32], rBytes)
	copy(sigRaw[64-len(sBytes):64], sBytes)
	sigB64 := base64.RawURLEncoding.EncodeToString(sigRaw)
	return signingInput + "." + sigB64
}

func mkSelfSignedECDSA(t *testing.T) (*ecdsa.PrivateKey, []byte) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test"},
		NotBefore: time.Now().Add(-1 * time.Hour),
		NotAfter:  time.Now().Add(24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatal(err)
	}
	return priv, der
}

// ─── 1. ParseJWS happy ────────────────────────

func TestApple_ParseJWS_Happy(t *testing.T) {
	priv, leafDER := mkSelfSignedECDSA(t)
	jws := signES256JWS(t, priv, leafDER, []byte(`{"foo":"bar"}`))
	h, payload, _, _, err := ParseJWS(jws)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if h.Alg != "ES256" {
		t.Fatalf("alg = %s", h.Alg)
	}
	if string(payload) != `{"foo":"bar"}` {
		t.Fatalf("payload = %s", payload)
	}
}

// 2. ParseJWS 缺段 → ErrAppleBadJWS
func TestApple_ParseJWS_Malformed(t *testing.T) {
	_, _, _, _, err := ParseJWS("not.a.jws.too.many")
	if err != ErrAppleBadJWS {
		t.Fatalf("got %v want ErrAppleBadJWS", err)
	}
}

// 3. VerifyJWS happy (ES256, 自签 root)
func TestApple_VerifyJWS_ES256(t *testing.T) {
	priv, leafDER := mkSelfSignedECDSA(t)
	leaf, _ := x509.ParseCertificate(leafDER)
	jws := signES256JWS(t, priv, leafDER, []byte(`{"x":1}`))
	payload, err := VerifyJWS(jws, []*x509.Certificate{leaf}, time.Now())
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if string(payload) != `{"x":1}` {
		t.Fatalf("payload = %s", payload)
	}
}

// 4. VerifyJWS 没 x5c → ErrAppleNoCertChain.
func TestApple_VerifyJWS_NoX5C(t *testing.T) {
	header := appleJWSHeader{Alg: "ES256", X5C: nil}
	hdr, _ := json.Marshal(header)
	hb := base64.RawURLEncoding.EncodeToString(hdr)
	pb := base64.RawURLEncoding.EncodeToString([]byte("{}"))
	sig := base64.RawURLEncoding.EncodeToString([]byte("xxxxxxxxxx"))
	_, err := VerifyJWS(hb+"."+pb+"."+sig, nil, time.Now())
	if err != ErrAppleNoCertChain {
		t.Fatalf("got %v want ErrAppleNoCertChain", err)
	}
}

// 5. VerifyJWS 篡改 payload → 验签失败
func TestApple_VerifyJWS_Tampered(t *testing.T) {
	priv, leafDER := mkSelfSignedECDSA(t)
	leaf, _ := x509.ParseCertificate(leafDER)
	jws := signES256JWS(t, priv, leafDER, []byte(`{"a":1}`))
	// 篡改 payload 段 (中间) — 改 sig 末尾偶发因 base64 编码相同字节流而不实际改变.
	// 找 payload 段 (第 2 段) 改一个字符: a→b.
	tampered := []byte(jws)
	for i := 0; i < len(tampered); i++ {
		if tampered[i] == 'a' {
			tampered[i] = 'b'
			break
		}
	}
	_, err := VerifyJWS(string(tampered), []*x509.Certificate{leaf}, time.Now())
	if err == nil {
		t.Fatal("tampered should fail")
	}
}

// 6. ES256 + RSA fallback path: 用 RS256 走 RSA verify (额外覆盖).
func TestApple_VerifyJWS_RS256(t *testing.T) {
	rsaPriv, _ := rsa.GenerateKey(rand.Reader, 2048)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "rsa-test"},
		NotBefore: time.Now().Add(-time.Hour),
		NotAfter:  time.Now().Add(24 * time.Hour),
		KeyUsage:  x509.KeyUsageDigitalSignature,
	}
	leafDER, _ := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &rsaPriv.PublicKey, rsaPriv)
	leaf, _ := x509.ParseCertificate(leafDER)

	header := appleJWSHeader{
		Alg: "RS256", Typ: "JWT",
		X5C: []string{base64.StdEncoding.EncodeToString(leafDER)},
	}
	hb, _ := json.Marshal(header)
	hbB64 := base64.RawURLEncoding.EncodeToString(hb)
	pbB64 := base64.RawURLEncoding.EncodeToString([]byte(`{"k":"v"}`))
	signingInput := hbB64 + "." + pbB64
	hashed := sha256.Sum256([]byte(signingInput))
	sig, _ := rsa.SignPKCS1v15(rand.Reader, rsaPriv, 5 /*SHA256*/, hashed[:]) // 5 = crypto.SHA256
	sigB64 := base64.RawURLEncoding.EncodeToString(sig)
	jws := signingInput + "." + sigB64

	payload, err := VerifyJWS(jws, []*x509.Certificate{leaf}, time.Now())
	if err != nil {
		t.Fatalf("rs256 verify: %v", err)
	}
	if string(payload) != `{"k":"v"}` {
		t.Fatalf("payload = %s", payload)
	}
}

// 7. DecodeNotificationV2 — 解析 outer + 嵌套 transaction.
func TestApple_DecodeNotification(t *testing.T) {
	priv, leafDER := mkSelfSignedECDSA(t)

	// Inner transaction JWS
	tx := AppleTransaction{
		TransactionID: "TX1", BundleID: "com.biumind.app",
		ProductID: "pro_monthly", PurchaseDate: 1750000000000,
	}
	txBytes, _ := json.Marshal(tx)
	signedTx := signES256JWS(t, priv, leafDER, txBytes)

	// Outer notification
	noti := AppleNotificationV2{
		NotificationType: "SUBSCRIBED", NotificationUUID: "u1",
		Version: "2.0", SignedDate: 1750000000000,
		Data: AppleNotificationData{
			BundleID: "com.biumind.app", Environment: "Sandbox",
			SignedTransactionInfo: signedTx,
		},
	}
	notiBytes, _ := json.Marshal(noti)
	signedPayload := signES256JWS(t, priv, leafDER, notiBytes)

	got, gotTx, err := DecodeNotificationV2(signedPayload)
	if err != nil {
		t.Fatal(err)
	}
	if got.NotificationType != "SUBSCRIBED" {
		t.Fatalf("type = %s", got.NotificationType)
	}
	if gotTx == nil || gotTx.TransactionID != "TX1" {
		t.Fatalf("tx = %+v", gotTx)
	}
}

// 8. DecodeNotificationV2 — 没 signedTransactionInfo 返 nil tx.
func TestApple_DecodeNotification_NoTx(t *testing.T) {
	priv, leafDER := mkSelfSignedECDSA(t)
	noti := AppleNotificationV2{
		NotificationType: "TEST", NotificationUUID: "u2",
	}
	bytes, _ := json.Marshal(noti)
	signed := signES256JWS(t, priv, leafDER, bytes)
	got, gotTx, err := DecodeNotificationV2(signed)
	if err != nil {
		t.Fatal(err)
	}
	if gotTx != nil {
		t.Fatalf("tx should be nil: %+v", gotTx)
	}
	if got.NotificationType != "TEST" {
		t.Fatal("type lost")
	}
}

// 9. ecdsaVerifyRawSig — sig 长度错误返 false.
func TestApple_ECDSARawSig_BadLen(t *testing.T) {
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if ecdsaVerifyRawSig(&priv.PublicKey, []byte("hash"), []byte("short")) {
		t.Fatal("short sig should fail")
	}
}

// 10. VerifyJWS 不传 root certs 时跳过链验证 (但仍验签).
func TestApple_VerifyJWS_NoRoots(t *testing.T) {
	priv, leafDER := mkSelfSignedECDSA(t)
	jws := signES256JWS(t, priv, leafDER, []byte(`{"ok":1}`))
	// 不传 rootCerts → 仅 leaf 自验签, 不做链验.
	payload, err := VerifyJWS(jws, nil, time.Now())
	if err != nil {
		t.Fatalf("nil roots verify: %v", err)
	}
	if string(payload) != `{"ok":1}` {
		t.Fatalf("payload = %s", payload)
	}
}

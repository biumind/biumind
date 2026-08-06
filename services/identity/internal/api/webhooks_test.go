// W5-9 webhook 路由测试 — 8 cases.

package api

import (
	"bytes"
	"context"
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/biumind/biumind/services/identity/internal/billing"
)

// ─── 共用 helpers ───────────────────────────────────

func seedPaymentOrder(t *testing.T, s *Server, uid uuid.UUID, provider, outTradeNo string) {
	t.Helper()
	pool := s.Subscriptions.Pool()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO billing.payment_orders
		    (user_id, order_type, provider, amount, currency, status, provider_order_id)
		VALUES ($1, 'subscription', $2, 19.00, 'CNY', 'pending', $3)
	`, uid, provider, outTradeNo)
	if err != nil {
		t.Fatal(err)
	}
}

func cleanupOrdersByOTN(t *testing.T, s *Server, otn string) {
	t.Helper()
	_, _ = s.Subscriptions.Pool().Exec(context.Background(),
		`DELETE FROM billing.payment_orders WHERE provider_order_id=$1`, otn)
}

func orderStatus(t *testing.T, s *Server, outTradeNo string) string {
	t.Helper()
	var status string
	_ = s.Subscriptions.Pool().QueryRow(context.Background(),
		`SELECT status FROM billing.payment_orders WHERE provider_order_id=$1`,
		outTradeNo,
	).Scan(&status)
	return status
}

// ─── wechat 回调 ────────────────────────────────────

// 构造一个完整的 wechat callback 请求 (验签头 + AES-GCM 密文).
func buildWechatCallback(t *testing.T, priv *rsa.PrivateKey, apiKeyV3 string, tx billing.WechatTransaction, eventType string) (*http.Request, []byte) {
	t.Helper()
	plaintext, _ := json.Marshal(tx)
	nonce := "0123456789ab"

	block, _ := aes.NewCipher([]byte(apiKeyV3))
	gcm, _ := cipher.NewGCM(block)
	ct := gcm.Seal(nil, []byte(nonce), plaintext, []byte("transaction"))
	cipherB64 := base64.StdEncoding.EncodeToString(ct)

	cb := billing.WechatCallback{
		ID:           "EV_" + uuid.NewString()[:8],
		EventType:    eventType,
		ResourceType: "encrypt-resource",
		Resource: billing.WechatCallbackResource{
			Algorithm:      "AEAD_AES_256_GCM",
			Ciphertext:     cipherB64,
			AssociatedData: "transaction",
			Nonce:          nonce,
			OriginalType:   "transaction",
		},
	}
	body, _ := json.Marshal(cb)

	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	headerNonce := "h-" + uuid.NewString()[:8]
	msg := timestamp + "\n" + headerNonce + "\n" + string(body) + "\n"
	hashed := sha256.Sum256([]byte(msg))
	sig, _ := rsa.SignPKCS1v15(rand.Reader, priv, crypto.SHA256, hashed[:])
	sigB64 := base64.StdEncoding.EncodeToString(sig)

	req := httptest.NewRequest(http.MethodPost, "/v1/billing/wechat/callback", bytes.NewReader(body))
	req.Header.Set("Wechatpay-Timestamp", timestamp)
	req.Header.Set("Wechatpay-Nonce", headerNonce)
	req.Header.Set("Wechatpay-Signature", sigB64)
	req.Header.Set("Content-Type", "application/json")
	return req, body
}

func wireRealWechat(t *testing.T, s *Server) (*rsa.PrivateKey, string) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	privPEM, pubPEM := genRSAKey(t)
	_ = privPEM
	_ = pubPEM
	// 用同一对密钥签 + 验 (priv 既当商户私钥又当平台公钥的源).
	apiv3 := "0123456789abcdef0123456789abcdef"
	cfg := billing.WechatConfig{
		Enabled: true, AppID: "wxAPP", MchID: "190000",
		APIv3Key: apiv3, CertSerialNo: "SN1",
		APIClientKeyPEM:   privKeyPEM(priv),
		PlatformPublicKey: pubKeyPEM(&priv.PublicKey),
		NotifyURL:         "https://example.com/webhook/wechat",
	}
	c, err := billing.NewWechatClient(cfg, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	s.Wechat = c
	return priv, apiv3
}

// 1. wechat happy: TRANSACTION.SUCCESS → status=succeeded.
func TestWechatCallback_Happy(t *testing.T) {
	s, mux, _ := newPlansTestServer(t)
	priv, apiv3 := wireRealWechat(t, s)

	otn := "BIU_W5_" + uuid.NewString()[:8]
	uid := uuid.New()
	seedPaymentOrder(t, s, uid, "wechat_pay", otn)
	defer cleanupOrdersByOTN(t, s, otn)

	tx := billing.WechatTransaction{
		OutTradeNo:    otn,
		TransactionID: "TX_" + uuid.NewString()[:8],
		TradeState:    "SUCCESS",
	}
	req, _ := buildWechatCallback(t, priv, apiv3, tx, "TRANSACTION.SUCCESS")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	if got := orderStatus(t, s, otn); got != "succeeded" {
		t.Fatalf("order status = %s want succeeded", got)
	}
}

// 2. wechat bad sig: 401.
func TestWechatCallback_BadSig(t *testing.T) {
	s, mux, _ := newPlansTestServer(t)
	wireRealWechat(t, s)

	req := httptest.NewRequest(http.MethodPost, "/v1/billing/wechat/callback", strings.NewReader(`{"id":"x"}`))
	req.Header.Set("Wechatpay-Timestamp", strconv.FormatInt(time.Now().Unix(), 10))
	req.Header.Set("Wechatpay-Nonce", "n")
	req.Header.Set("Wechatpay-Signature", base64.StdEncoding.EncodeToString([]byte("bad")))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatalf("status = %d", w.Code)
	}
}

// 3. wechat 未知 out_trade_no: 仍 200 ack (避免微信 retry).
func TestWechatCallback_UnknownOTN(t *testing.T) {
	s, mux, _ := newPlansTestServer(t)
	priv, apiv3 := wireRealWechat(t, s)

	tx := billing.WechatTransaction{
		OutTradeNo:    "UNKNOWN_" + uuid.NewString()[:8],
		TransactionID: "TX",
		TradeState:    "SUCCESS",
	}
	req, _ := buildWechatCallback(t, priv, apiv3, tx, "TRANSACTION.SUCCESS")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200 ack, got %d", w.Code)
	}
}

// 4. wechat REFUND: status=refunded.
func TestWechatCallback_Refund(t *testing.T) {
	s, mux, _ := newPlansTestServer(t)
	priv, apiv3 := wireRealWechat(t, s)

	otn := "BIU_REF_" + uuid.NewString()[:8]
	uid := uuid.New()
	seedPaymentOrder(t, s, uid, "wechat_pay", otn)
	defer cleanupOrdersByOTN(t, s, otn)

	tx := billing.WechatTransaction{
		OutTradeNo: otn, TradeState: "REFUND",
	}
	req, _ := buildWechatCallback(t, priv, apiv3, tx, "REFUND.SUCCESS")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	if got := orderStatus(t, s, otn); got != "refunded" {
		t.Fatalf("status = %s want refunded", got)
	}
}

// ─── alipay 回调 ───────────────────────────────────

func wireRealAlipay(t *testing.T, s *Server) (*rsa.PrivateKey, *rsa.PublicKey) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	cfg := billing.AlipayConfig{
		Enabled:            true,
		AppID:              "2021_test",
		PrivateKeyPEM:      privKeyPEM(priv),
		AlipayPublicKeyPEM: pubKeyPEM(&priv.PublicKey),
		NotifyURL:          "https://example.com/webhook/alipay",
	}
	c, err := billing.NewAlipayClient(cfg, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	s.Alipay = c
	return priv, &priv.PublicKey
}

func signedAlipayForm(t *testing.T, priv *rsa.PrivateKey, params map[string]string) url.Values {
	t.Helper()
	sig, err := billing.SignAlipayParams(params, priv)
	if err != nil {
		t.Fatal(err)
	}
	form := url.Values{}
	for k, v := range params {
		form.Set(k, v)
	}
	form.Set("sign", sig)
	form.Set("sign_type", "RSA2")
	return form
}

// 5. alipay TRADE_SUCCESS → status=succeeded.
func TestAlipayCallback_Happy(t *testing.T) {
	s, mux, _ := newPlansTestServer(t)
	priv, _ := wireRealAlipay(t, s)

	otn := "BIU_AP_" + uuid.NewString()[:8]
	uid := uuid.New()
	seedPaymentOrder(t, s, uid, "alipay", otn)
	defer cleanupOrdersByOTN(t, s, otn)

	form := signedAlipayForm(t, priv, map[string]string{
		"app_id":       "2021_test",
		"out_trade_no": otn,
		"trade_no":     "AP_" + uuid.NewString()[:8],
		"trade_status": "TRADE_SUCCESS",
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/billing/alipay/callback", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	body, _ := io.ReadAll(w.Body)
	if string(body) != "success" {
		t.Fatalf("body = %s want 'success'", body)
	}
	if got := orderStatus(t, s, otn); got != "succeeded" {
		t.Fatalf("order status = %s", got)
	}
}

// 6. alipay bad sig: body=fail.
func TestAlipayCallback_BadSig(t *testing.T) {
	s, mux, _ := newPlansTestServer(t)
	wireRealAlipay(t, s)

	form := url.Values{}
	form.Set("app_id", "X")
	form.Set("out_trade_no", "O1")
	form.Set("trade_status", "TRADE_SUCCESS")
	form.Set("sign", base64.StdEncoding.EncodeToString([]byte("bad")))
	form.Set("sign_type", "RSA2")

	req := httptest.NewRequest(http.MethodPost, "/v1/billing/alipay/callback", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	body, _ := io.ReadAll(w.Body)
	if string(body) != "fail" {
		t.Fatalf("body = %s want 'fail'", body)
	}
}

// 7. alipay 未知 out_trade_no: body=success (ack ack).
func TestAlipayCallback_UnknownOTN(t *testing.T) {
	s, mux, _ := newPlansTestServer(t)
	priv, _ := wireRealAlipay(t, s)

	form := signedAlipayForm(t, priv, map[string]string{
		"app_id":       "2021_test",
		"out_trade_no": "UNKNOWN_" + uuid.NewString()[:8],
		"trade_status": "TRADE_SUCCESS",
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/billing/alipay/callback", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	body, _ := io.ReadAll(w.Body)
	// Verify-pass + lookup-miss 也 ack success (避免 alipay 重试)
	if string(body) != "success" {
		t.Fatalf("body = %s want 'success'", body)
	}
}

// 8. alipay TRADE_CLOSED → status=canceled.
func TestAlipayCallback_TradeClosed(t *testing.T) {
	s, mux, _ := newPlansTestServer(t)
	priv, _ := wireRealAlipay(t, s)

	otn := "BIU_CL_" + uuid.NewString()[:8]
	uid := uuid.New()
	seedPaymentOrder(t, s, uid, "alipay", otn)
	defer cleanupOrdersByOTN(t, s, otn)

	form := signedAlipayForm(t, priv, map[string]string{
		"app_id":       "2021_test",
		"out_trade_no": otn,
		"trade_status": "TRADE_CLOSED",
		"close_reason": "user_cancel",
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/billing/alipay/callback", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if got := orderStatus(t, s, otn); got != "canceled" {
		t.Fatalf("status = %s want canceled", got)
	}
}

// ─── PEM helpers (复用 wechat/alipay 测试模式) ──

func privKeyPEM(priv *rsa.PrivateKey) string {
	pkcs8, _ := x509.MarshalPKCS8PrivateKey(priv)
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8}))
}

func pubKeyPEM(pub *rsa.PublicKey) string {
	pkix, _ := x509.MarshalPKIXPublicKey(pub)
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pkix}))
}

// W5-2 微信支付 v3 — 12 单测.
//
// 测试覆盖:
//   1. 签名生成: 解析 + 校验签名
//   2. 验签 happy path
//   3. 验签时间窗口拒绝 (>5min)
//   4. 验签错误签名拒绝
//   5. AES-GCM 解密 happy
//   6. AES-GCM 解密 wrong key
//   7. 配置 Validate (合法 / 缺字段)
//   8. NewClient 解析 PEM 失败
//   9. CreateNativeOrder happy (mock server)
//  10. CreateJSAPIOrder happy
//  11. CreateH5Order happy
//  12. VerifyAndDecodeCallback happy

package billing

import (
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
	"strconv"
	"strings"
	"testing"
	"time"
)

// ─── helpers ───────────────────────────────────────────

// genTestRSAKey — 测试用 2048 位 RSA 密钥对; PKCS8 PEM 编码.
func genTestRSAKey(t *testing.T) (*rsa.PrivateKey, string, string) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("gen key: %v", err)
	}
	pkcs8, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatalf("marshal pkcs8: %v", err)
	}
	privPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8}))

	pubBytes, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		t.Fatalf("marshal pub: %v", err)
	}
	pubPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubBytes}))
	return priv, privPEM, pubPEM
}

func validWechatCfg(privPEM, pubPEM string) WechatConfig {
	return WechatConfig{
		Enabled:           true,
		AppID:             "wxAPPID",
		MchID:             "1900000000",
		APIv3Key:          "0123456789abcdef0123456789abcdef", // 32B
		CertSerialNo:      "ABCDEF123456",
		APIClientKeyPEM:   privPEM,
		PlatformPublicKey: pubPEM,
		NotifyURL:         "https://example.com/webhook/wechat",
	}
}

// ─── 1. SignRequest 解析 + 校验 ─────────────────────

func TestWechat_SignRequest(t *testing.T) {
	priv, _, _ := genTestRSAKey(t)
	now := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)

	auth, err := SignRequest("POST", "/v3/pay/transactions/native", `{"foo":"bar"}`, priv, "1900000000", "SERIAL", now)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if !strings.HasPrefix(auth, "WECHATPAY2-SHA256-RSA2048 ") {
		t.Fatalf("auth prefix wrong: %s", auth)
	}
	for _, kv := range []string{`mchid="1900000000"`, `serial_no="SERIAL"`, `signature="`, `timestamp="`, `nonce_str="`} {
		if !strings.Contains(auth, kv) {
			t.Fatalf("auth missing %s: %s", kv, auth)
		}
	}
}

// ─── 2. VerifyCallbackSignature happy ─────────────────

func TestWechat_VerifyCallback_Happy(t *testing.T) {
	priv, _, pubPEM := genTestRSAKey(t)
	pub, _ := LoadWechatPublicKey(pubPEM)

	body := `{"id":"x"}`
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	nonce := "abcdef0123456789"
	msg := timestamp + "\n" + nonce + "\n" + body + "\n"
	hashed := sha256.Sum256([]byte(msg))
	sig, err := rsa.SignPKCS1v15(rand.Reader, priv, crypto.SHA256, hashed[:])
	if err != nil {
		t.Fatal(err)
	}
	sigB64 := base64.StdEncoding.EncodeToString(sig)

	if err := VerifyCallbackSignature(timestamp, nonce, body, sigB64, pub, time.Now()); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

// ─── 3. 时间窗口 ──────────────────────────────────

func TestWechat_VerifyCallback_TimestampSkew(t *testing.T) {
	_, _, pubPEM := genTestRSAKey(t)
	pub, _ := LoadWechatPublicKey(pubPEM)
	old := strconv.FormatInt(time.Now().Add(-10*time.Minute).Unix(), 10)
	err := VerifyCallbackSignature(old, "n", "{}", "AA==", pub, time.Now())
	if err != ErrWechatTimestampSkew {
		t.Fatalf("got %v want ErrWechatTimestampSkew", err)
	}
}

// ─── 4. 错误签名 ──────────────────────────────────

func TestWechat_VerifyCallback_BadSignature(t *testing.T) {
	_, _, pubPEM := genTestRSAKey(t)
	pub, _ := LoadWechatPublicKey(pubPEM)
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	// 用错的 sig
	err := VerifyCallbackSignature(ts, "nonce", `{"foo":"bar"}`, base64.StdEncoding.EncodeToString([]byte("bad")), pub, time.Now())
	if err != ErrWechatBadSignature {
		t.Fatalf("got %v want ErrWechatBadSignature", err)
	}
}

// ─── 5. AES-GCM 解密 happy ────────────────────────

func TestWechat_DecryptCallback_Happy(t *testing.T) {
	apiKey := "0123456789abcdef0123456789abcdef" // 32B
	plaintext := `{"out_trade_no":"O1","trade_state":"SUCCESS"}`
	nonce := "0123456789ab" // 12B
	associated := "transaction"

	// encrypt
	cipherB64 := encryptAESGCM(t, []byte(apiKey), []byte(nonce), associated, plaintext)

	got, err := DecryptCallbackResource(apiKey, cipherB64, associated, nonce)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if string(got) != plaintext {
		t.Fatalf("got %q want %q", got, plaintext)
	}
}

// ─── 6. 错误密钥 ──────────────────────────────────

func TestWechat_DecryptCallback_WrongKey(t *testing.T) {
	plain := "hello"
	cipher := encryptAESGCM(t, []byte("0123456789abcdef0123456789abcdef"), []byte("0123456789ab"), "ad", plain)
	_, err := DecryptCallbackResource("ffffffffffffffffffffffffffffffff", cipher, "ad", "0123456789ab")
	if err == nil {
		t.Fatalf("should fail with wrong key")
	}
}

// ─── 7. Config Validate ──────────────────────────

func TestWechatConfig_Validate(t *testing.T) {
	_, privPEM, pubPEM := genTestRSAKey(t)
	good := validWechatCfg(privPEM, pubPEM)
	if err := good.Validate(); err != nil {
		t.Fatalf("good: %v", err)
	}
	disabled := WechatConfig{Enabled: false}
	if err := disabled.Validate(); err != nil {
		t.Fatalf("disabled should pass: %v", err)
	}
	bad := good
	bad.APIv3Key = "tooshort"
	if err := bad.Validate(); err == nil {
		t.Fatalf("short apiv3 key should fail")
	}
	bad2 := good
	bad2.NotifyURL = "http://insecure"
	if err := bad2.Validate(); err == nil {
		t.Fatalf("non-https notify should fail")
	}
}

// ─── 8. NewClient PEM 解析失败 ────────────────────

func TestNewWechatClient_BadPEM(t *testing.T) {
	cfg := WechatConfig{
		Enabled: true, AppID: "x", MchID: "y",
		APIv3Key:        "0123456789abcdef0123456789abcdef",
		APIClientKeyPEM: "not a pem",
		NotifyURL:       "https://x/cb",
	}
	if _, err := NewWechatClient(cfg, slog.Default()); err == nil {
		t.Fatalf("should fail on bad PEM")
	}
}

// ─── 9. CreateNativeOrder happy ───────────────────

func TestWechat_CreateNativeOrder(t *testing.T) {
	_, privPEM, pubPEM := genTestRSAKey(t)
	srv := mockWechatGateway(t, "/v3/pay/transactions/native", `{"code_url":"weixin://wxpay/bizpayurl?pr=abc"}`)
	defer srv.Close()

	c := newClientWithMock(t, validWechatCfg(privPEM, pubPEM), srv)
	codeURL, err := c.CreateNativeOrder(context.Background(), WechatOrderRequest{
		Description: "Pro 月会员", OutTradeNo: "O1", TotalCents: 1900,
	})
	if err != nil {
		t.Fatalf("native: %v", err)
	}
	if !strings.HasPrefix(codeURL, "weixin://") {
		t.Fatalf("code_url = %s", codeURL)
	}
}

// ─── 10. CreateJSAPIOrder happy ───────────────────

func TestWechat_CreateJSAPIOrder(t *testing.T) {
	_, privPEM, pubPEM := genTestRSAKey(t)
	srv := mockWechatGateway(t, "/v3/pay/transactions/jsapi", `{"prepay_id":"wx20260701..."}`)
	defer srv.Close()

	c := newClientWithMock(t, validWechatCfg(privPEM, pubPEM), srv)
	id, err := c.CreateJSAPIOrder(context.Background(), WechatOrderRequest{
		Description: "Pro", OutTradeNo: "O2", TotalCents: 1900, OpenID: "oABC",
	})
	if err != nil {
		t.Fatalf("jsapi: %v", err)
	}
	if !strings.HasPrefix(id, "wx") {
		t.Fatalf("prepay = %s", id)
	}

	// 缺 openid 应失败
	_, err = c.CreateJSAPIOrder(context.Background(), WechatOrderRequest{
		Description: "Pro", OutTradeNo: "O3", TotalCents: 1900,
	})
	if err == nil {
		t.Fatalf("missing openid should fail")
	}
}

// ─── 11. CreateH5Order happy ──────────────────────

func TestWechat_CreateH5Order(t *testing.T) {
	_, privPEM, pubPEM := genTestRSAKey(t)
	srv := mockWechatGateway(t, "/v3/pay/transactions/h5", `{"h5_url":"https://wx.tenpay.com/cgi-bin/mmpayweb-bin/checkmweb?prepay_id=..."}`)
	defer srv.Close()

	c := newClientWithMock(t, validWechatCfg(privPEM, pubPEM), srv)
	url, err := c.CreateH5Order(context.Background(), WechatOrderRequest{
		Description: "Pro", OutTradeNo: "O4", TotalCents: 1900, ClientIP: "1.2.3.4",
	})
	if err != nil {
		t.Fatalf("h5: %v", err)
	}
	if !strings.HasPrefix(url, "https://wx.tenpay.com") {
		t.Fatalf("h5 url = %s", url)
	}
}

// ─── 12. VerifyAndDecodeCallback happy ──────────

func TestWechat_VerifyAndDecodeCallback(t *testing.T) {
	priv, privPEM, pubPEM := genTestRSAKey(t)
	cfg := validWechatCfg(privPEM, pubPEM)
	c, err := NewWechatClient(cfg, slog.Default())
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	// 1. 构造加密的 transaction 内容.
	tx := WechatTransaction{
		OutTradeNo: "O42", TransactionID: "TX42", TradeState: "SUCCESS",
	}
	plaintext, _ := json.Marshal(tx)
	nonce := "0123456789ab"
	cipherB64 := encryptAESGCM(t, []byte(cfg.APIv3Key), []byte(nonce), "transaction", string(plaintext))

	cb := WechatCallback{
		ID:           "EV1",
		EventType:    "TRANSACTION.SUCCESS",
		ResourceType: "encrypt-resource",
		Resource: WechatCallbackResource{
			Algorithm:      "AEAD_AES_256_GCM",
			Ciphertext:     cipherB64,
			AssociatedData: "transaction",
			Nonce:          nonce,
			OriginalType:   "transaction",
		},
	}
	body, _ := json.Marshal(cb)

	// 2. 用商户私钥反签 (假装是平台公钥 — 测试里 priv/pub 是同一对).
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	headerNonce := "header-nonce"
	msg := timestamp + "\n" + headerNonce + "\n" + string(body) + "\n"
	hashed := sha256.Sum256([]byte(msg))
	sig, _ := rsa.SignPKCS1v15(rand.Reader, priv, crypto.SHA256, hashed[:])
	sigB64 := base64.StdEncoding.EncodeToString(sig)

	headers := http.Header{}
	headers.Set("Wechatpay-Timestamp", timestamp)
	headers.Set("Wechatpay-Nonce", headerNonce)
	headers.Set("Wechatpay-Signature", sigB64)

	got, err := c.VerifyAndDecodeCallback(headers, body)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if got.OutTradeNo != "O42" || got.TradeState != "SUCCESS" {
		t.Fatalf("got %+v", got)
	}
}

// ─── helpers ───────────────────────────────────────────

// encryptAESGCM — 给测试 fixture 准备密文.
func encryptAESGCM(t *testing.T, key, nonce []byte, ad, plaintext string) string {
	t.Helper()
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	ct := gcm.Seal(nil, nonce, []byte(plaintext), []byte(ad))
	return base64.StdEncoding.EncodeToString(ct)
}

// mockWechatGateway — 起一个 httptest.Server 拦截特定路径返指定 JSON.
func mockWechatGateway(t *testing.T, expectPath, respJSON string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != expectPath {
			http.Error(w, "wrong path", 404)
			return
		}
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "WECHATPAY2-SHA256-RSA2048 ") {
			http.Error(w, "missing auth", 401)
			return
		}
		// Require Content-Type and non-empty body
		bodyBytes, _ := io.ReadAll(r.Body)
		if len(bodyBytes) == 0 || r.Header.Get("Content-Type") != "application/json" {
			http.Error(w, "bad body", 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, respJSON)
	}))
}

// newClientWithMock — 把 client 的 gateway 重定向到 mock server.
func newClientWithMock(t *testing.T, cfg WechatConfig, srv *httptest.Server) *WechatClient {
	t.Helper()
	c, err := NewWechatClient(cfg, slog.Default())
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	c.SetHTTPClient(srv.Client())
	// Override gateway via a custom transport that rewrites host.
	orig := srv.Client().Transport
	if orig == nil {
		orig = http.DefaultTransport
	}
	c.http.Transport = &gatewayRedirect{base: srv.URL, inner: orig}
	return c
}

// gatewayRedirect — 把指向 api.mch.weixin.qq.com 的请求改路到 mock URL.
type gatewayRedirect struct {
	base  string
	inner http.RoundTripper
}

func (g *gatewayRedirect) RoundTrip(req *http.Request) (*http.Response, error) {
	if strings.HasPrefix(req.URL.String(), WechatGateway) {
		newURL := strings.Replace(req.URL.String(), WechatGateway, g.base, 1)
		req.URL, _ = req.URL.Parse(newURL)
		req.Host = ""
	}
	return g.inner.RoundTrip(req)
}

// W5-3 支付宝 — 10 单测.

package billing

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// ─── helpers ──────────────────────────────────────────

func genAlipayKey(t *testing.T) (*rsa.PrivateKey, string, string) {
	priv, privPEM, pubPEM := genTestRSAKey(t)
	return priv, privPEM, pubPEM
}

func validAlipayCfg(privPEM, pubPEM string) AlipayConfig {
	return AlipayConfig{
		Enabled:            true,
		AppID:              "2021000000",
		PrivateKeyPEM:      privPEM,
		AlipayPublicKeyPEM: pubPEM,
		NotifyURL:          "https://example.com/webhook/alipay",
		ReturnURL:          "https://example.com/return",
	}
}

// ─── 1. SignAlipayParams 排序确定性 ───────────────

func TestAlipay_SignParams_Deterministic(t *testing.T) {
	priv, _, _ := genAlipayKey(t)
	p1 := map[string]string{"app_id": "X", "method": "M", "biz_content": "{}"}
	s1, err := SignAlipayParams(p1, priv)
	if err != nil {
		t.Fatal(err)
	}
	// 不同插入顺序应得到相同签名
	p2 := map[string]string{"biz_content": "{}", "method": "M", "app_id": "X"}
	s2, err := SignAlipayParams(p2, priv)
	if err != nil {
		t.Fatal(err)
	}
	if s1 != s2 {
		t.Fatalf("signature should be deterministic regardless of map order")
	}
}

// ─── 2. concatAlipayParams 排除 sign / sign_type / 空 ──

func TestAlipay_ConcatExcludesSignAndEmpty(t *testing.T) {
	got := concatAlipayParams(map[string]string{
		"app_id":    "X",
		"method":    "M",
		"sign":      "should-skip",
		"sign_type": "should-skip-too",
		"empty":     "",
	})
	want := "app_id=X&method=M"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

// ─── 3. VerifyAlipayCallback happy ───────────────

func TestAlipay_VerifyCallback_Happy(t *testing.T) {
	priv, _, pubPEM := genAlipayKey(t)
	pub, _ := LoadAlipayPublicKey(pubPEM)
	params := map[string]string{
		"app_id":       "X",
		"out_trade_no": "O1",
		"trade_status": "TRADE_SUCCESS",
	}
	sig, err := SignAlipayParams(params, priv)
	if err != nil {
		t.Fatal(err)
	}
	params["sign"] = sig
	params["sign_type"] = "RSA2"
	if err := VerifyAlipayCallback(params, pub); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

// ─── 4. VerifyAlipayCallback bad sig ─────────────

func TestAlipay_VerifyCallback_BadSig(t *testing.T) {
	_, _, pubPEM := genAlipayKey(t)
	pub, _ := LoadAlipayPublicKey(pubPEM)
	params := map[string]string{"app_id": "X", "sign": "AA=="}
	if err := VerifyAlipayCallback(params, pub); err != ErrAlipayBadSignature {
		t.Fatalf("got %v want ErrAlipayBadSignature", err)
	}
}

// ─── 5. CreatePagePay 跳转 URL 含 sign + biz_content ──

func TestAlipay_CreatePagePay(t *testing.T) {
	_, privPEM, pubPEM := genAlipayKey(t)
	c, err := NewAlipayClient(validAlipayCfg(privPEM, pubPEM), slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.CreatePagePay(AlipayTradeArgs{
		OutTradeNo: "O1", TotalAmount: 19.00, Subject: "Pro 月会员",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "method=alipay.trade.page.pay") {
		t.Fatalf("missing method: %s", got)
	}
	if !strings.Contains(got, "sign=") || !strings.Contains(got, "biz_content=") {
		t.Fatalf("missing sign or biz_content: %s", got)
	}
	if !strings.Contains(got, "FAST_INSTANT_TRADE_PAY") {
		t.Fatalf("missing product_code: %s", got)
	}
}

// ─── 6. CreateWapPay ─────────────────────────────

func TestAlipay_CreateWapPay(t *testing.T) {
	_, privPEM, pubPEM := genAlipayKey(t)
	c, _ := NewAlipayClient(validAlipayCfg(privPEM, pubPEM), slog.Default())
	got, err := c.CreateWapPay(AlipayTradeArgs{
		OutTradeNo: "O2", TotalAmount: 9.90, Subject: "充值 100 积分",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "alipay.trade.wap.pay") {
		t.Fatalf("missing wap method: %s", got)
	}
	if !strings.Contains(got, "QUICK_WAP_WAY") {
		t.Fatalf("missing wap product_code")
	}
}

// ─── 7. CreateAgreementSign 周期扣款 ─────────────

func TestAlipay_CreateAgreementSign(t *testing.T) {
	_, privPEM, pubPEM := genAlipayKey(t)
	c, _ := NewAlipayClient(validAlipayCfg(privPEM, pubPEM), slog.Default())
	got, err := c.CreateAgreementSign(AlipayAgreementArgs{
		ExternalAgreementNo: "AG_2026_07",
		PeriodType:          "MONTH",
		Period:              1,
		ExecuteTime:         "2026-08-01",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "alipay.user.agreement.page.sign") {
		t.Fatalf("missing agreement method: %s", got)
	}
	if !strings.Contains(got, "AG_2026_07") {
		t.Fatalf("missing external_agreement_no")
	}
}

// ─── 8. Config Validate ─────────────────────────

func TestAlipayConfig_Validate(t *testing.T) {
	_, privPEM, pubPEM := genAlipayKey(t)
	good := validAlipayCfg(privPEM, pubPEM)
	if err := good.Validate(); err != nil {
		t.Fatalf("good: %v", err)
	}
	disabled := AlipayConfig{Enabled: false}
	if err := disabled.Validate(); err != nil {
		t.Fatalf("disabled should pass: %v", err)
	}
	bad := good
	bad.NotifyURL = "http://insecure"
	if err := bad.Validate(); err == nil {
		t.Fatalf("non-https notify should fail")
	}
}

// ─── 9. NewClient PEM 失败 ──────────────────────

func TestNewAlipayClient_BadPEM(t *testing.T) {
	cfg := AlipayConfig{
		Enabled: true, AppID: "X",
		PrivateKeyPEM: "garbage",
		AlipayPublicKeyPEM: "garbage",
		NotifyURL: "https://x/cb",
	}
	if _, err := NewAlipayClient(cfg, slog.Default()); err == nil {
		t.Fatalf("should fail")
	}
}

// ─── 10. QueryTradeStatus mock server ──────────

func TestAlipay_QueryTradeStatus(t *testing.T) {
	_, privPEM, pubPEM := genAlipayKey(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("method") != "alipay.trade.query" {
			http.Error(w, "wrong method", 400)
			return
		}
		if r.Form.Get("sign") == "" {
			http.Error(w, "missing sign", 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"alipay_trade_query_response":{"code":"10000","msg":"Success","trade_status":"TRADE_SUCCESS","out_trade_no":"O42"}}`)
	}))
	defer srv.Close()

	cfg := validAlipayCfg(privPEM, pubPEM)
	cfg.Gateway = srv.URL
	c, _ := NewAlipayClient(cfg, slog.Default())
	c.SetHTTPClient(srv.Client())

	status, err := c.QueryTradeStatus(context.Background(), "O42")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if status != "TRADE_SUCCESS" {
		t.Fatalf("got %q", status)
	}
}

// ─── 额外: normalizeAlipayPEM 兼容裸 base64 ──────

func TestAlipay_NormalizePEM(t *testing.T) {
	// 用真私钥的 base64 部分 (去除头尾) 来确认 normalize 后能 parse.
	priv, privPEM, _ := genAlipayKey(t)
	_ = priv

	block, _ := pem.Decode([]byte(privPEM))
	if block == nil {
		t.Fatalf("base PEM decode failed")
	}
	bare := strings.TrimSuffix(strings.ReplaceAll(privPEM, "-----BEGIN PRIVATE KEY-----\n", ""), "\n")
	bare = strings.ReplaceAll(bare, "-----END PRIVATE KEY-----", "")
	bare = strings.ReplaceAll(bare, "\n", "")

	pk, err := LoadAlipayPrivateKey(bare)
	if err != nil {
		t.Fatalf("load bare: %v", err)
	}
	if pk == nil {
		t.Fatalf("nil pk")
	}
	// sanity: re-marshal matches.
	if _, err := x509.MarshalPKCS8PrivateKey(pk); err != nil {
		t.Fatalf("re-marshal: %v", err)
	}
	_ = url.Values{} // avoid unused import
}

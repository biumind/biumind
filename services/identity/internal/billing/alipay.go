// alipay.go — W5-3 支付宝 OpenAPI 客户端.
//
// 三个核心接口:
//   - alipay.trade.page.pay   — PC 网站支付,    返跳转 URL
//   - alipay.trade.wap.pay    — 手机网站支付,   返跳转 URL
//   - alipay.user.agreement.page.sign — 周期扣款协议签订, 返跳转 URL
//
// + alipay.trade.query / refund / close (server-to-server, 后续扩).
//
// 文档: https://opendocs.alipay.com/open/repo-0033wb

package billing

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const AlipayGateway = "https://openapi.alipay.com/gateway.do"

var (
	ErrAlipayNotConfigured = errors.New("alipay: client not configured")
	ErrAlipayAPIError      = errors.New("alipay: api returned error")
)

// ─── Config ────────────────────────────────────────────

type AlipayConfig struct {
	Enabled            bool   `json:"enabled"`
	AppID              string `json:"app_id"`
	PrivateKeyPEM      string `json:"private_key_pem"`        // 应用私钥 (PKCS8 / PKCS1)
	AlipayPublicKeyPEM string `json:"alipay_public_key_pem"`  // 支付宝公钥 (验签回调用)
	NotifyURL          string `json:"notify_url"`             // 异步回调
	ReturnURL          string `json:"return_url"`             // 同步跳转 (可选)
	Gateway            string `json:"gateway,omitempty"`      // 留空用 AlipayGateway
	SignType           string `json:"sign_type,omitempty"`    // 留空用 RSA2
}

func (c AlipayConfig) Validate() error {
	if !c.Enabled {
		return nil
	}
	if c.AppID == "" || c.PrivateKeyPEM == "" {
		return errors.New("alipay: app_id / private_key_pem required")
	}
	if c.AlipayPublicKeyPEM == "" {
		return errors.New("alipay: alipay_public_key_pem required for callback verify")
	}
	if c.NotifyURL == "" || !strings.HasPrefix(c.NotifyURL, "https://") {
		return errors.New("alipay: notify_url must be https")
	}
	return nil
}

// ─── Client ────────────────────────────────────────────

type AlipayClient struct {
	cfg        AlipayConfig
	privateKey *rsa.PrivateKey
	publicKey  *rsa.PublicKey
	http       *http.Client
	now        func() time.Time
	logger     *slog.Logger
}

func NewAlipayClient(cfg AlipayConfig, logger *slog.Logger) (*AlipayClient, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.Default()
	}
	c := &AlipayClient{
		cfg:    cfg,
		http:   &http.Client{Timeout: 15 * time.Second},
		now:    time.Now,
		logger: logger,
	}
	if !cfg.Enabled {
		return c, nil
	}
	pk, err := LoadAlipayPrivateKey(cfg.PrivateKeyPEM)
	if err != nil {
		return nil, err
	}
	c.privateKey = pk
	pub, err := LoadAlipayPublicKey(cfg.AlipayPublicKeyPEM)
	if err != nil {
		return nil, err
	}
	c.publicKey = pub
	return c, nil
}

func (c *AlipayClient) SetClock(now func() time.Time)      { c.now = now }
func (c *AlipayClient) SetHTTPClient(h *http.Client)       { c.http = h }
func (c *AlipayClient) Enabled() bool                      { return c != nil && c.cfg.Enabled }

func (c *AlipayClient) gateway() string {
	if c.cfg.Gateway != "" {
		return c.cfg.Gateway
	}
	return AlipayGateway
}

func (c *AlipayClient) signType() string {
	if c.cfg.SignType != "" {
		return c.cfg.SignType
	}
	return "RSA2"
}

// ─── 公共参数 ───────────────────────────────────────

// commonParams — 所有接口都需要的公共字段.
func (c *AlipayClient) commonParams(method string) map[string]string {
	return map[string]string{
		"app_id":    c.cfg.AppID,
		"method":    method,
		"format":    "JSON",
		"charset":   "utf-8",
		"sign_type": c.signType(),
		"timestamp": c.now().Format("2006-01-02 15:04:05"),
		"version":   "1.0",
		"notify_url": c.cfg.NotifyURL,
	}
}

// ─── 业务请求 ───────────────────────────────────────

// AlipayTradeArgs — page.pay / wap.pay 共用的 biz_content 字段集.
type AlipayTradeArgs struct {
	OutTradeNo  string  `json:"out_trade_no"`            // 商户订单号
	TotalAmount float64 `json:"total_amount"`            // 元 (.2f)
	Subject     string  `json:"subject"`                 // 商品标题
	Body        string  `json:"body,omitempty"`          // 详情
	TimeoutExpress string `json:"timeout_express,omitempty"` // e.g. "30m"
}

// CreatePagePay — alipay.trade.page.pay (PC 网站支付).
// 返跳转 URL (浏览器 GET / form submit 跳过去).
func (c *AlipayClient) CreatePagePay(args AlipayTradeArgs) (string, error) {
	if !c.Enabled() {
		return "", ErrAlipayNotConfigured
	}
	bizContent, err := buildBizContent(args, "FAST_INSTANT_TRADE_PAY")
	if err != nil {
		return "", err
	}
	return c.buildRedirectURL("alipay.trade.page.pay", bizContent)
}

// CreateWapPay — alipay.trade.wap.pay (手机网站支付).
func (c *AlipayClient) CreateWapPay(args AlipayTradeArgs) (string, error) {
	if !c.Enabled() {
		return "", ErrAlipayNotConfigured
	}
	bizContent, err := buildBizContent(args, "QUICK_WAP_WAY")
	if err != nil {
		return "", err
	}
	return c.buildRedirectURL("alipay.trade.wap.pay", bizContent)
}

// AlipayAgreementArgs — 周期扣款协议参数.
type AlipayAgreementArgs struct {
	ExternalAgreementNo string `json:"external_agreement_no"`           // 商户协议号
	PersonalProductCode string `json:"personal_product_code,omitempty"` // 默认 CYCLE_PAY_AUTH_P
	SignScene           string `json:"sign_scene,omitempty"`            // 默认 INDUSTRY|DIGITAL_MEDIA
	PeriodType          string `json:"period_type,omitempty"`           // DAY / MONTH
	Period              int    `json:"period,omitempty"`                // 周期个数
	ExecuteTime         string `json:"execute_time,omitempty"`          // YYYY-MM-DD
	SingleAmount        string `json:"single_amount,omitempty"`         // 单笔金额上限
	TotalAmount         string `json:"total_amount,omitempty"`          // 总额度上限
}

// CreateAgreementSign — 周期扣款协议签订, 返跳转 URL.
func (c *AlipayClient) CreateAgreementSign(args AlipayAgreementArgs) (string, error) {
	if !c.Enabled() {
		return "", ErrAlipayNotConfigured
	}
	if args.ExternalAgreementNo == "" {
		return "", errors.New("alipay agreement: external_agreement_no required")
	}
	body := struct {
		PersonalProductCode string `json:"personal_product_code"`
		SignScene           string `json:"sign_scene"`
		ExternalAgreementNo string `json:"external_agreement_no"`
		PeriodRuleParams    struct {
			PeriodType   string `json:"period_type,omitempty"`
			Period       int    `json:"period,omitempty"`
			ExecuteTime  string `json:"execute_time,omitempty"`
			SingleAmount string `json:"single_amount,omitempty"`
			TotalAmount  string `json:"total_amount,omitempty"`
		} `json:"period_rule_params"`
	}{
		PersonalProductCode: orDefault(args.PersonalProductCode, "CYCLE_PAY_AUTH_P"),
		SignScene:           orDefault(args.SignScene, "INDUSTRY|DIGITAL_MEDIA"),
		ExternalAgreementNo: args.ExternalAgreementNo,
	}
	body.PeriodRuleParams.PeriodType = args.PeriodType
	body.PeriodRuleParams.Period = args.Period
	body.PeriodRuleParams.ExecuteTime = args.ExecuteTime
	body.PeriodRuleParams.SingleAmount = args.SingleAmount
	body.PeriodRuleParams.TotalAmount = args.TotalAmount

	bizBytes, _ := json.Marshal(body)
	return c.buildRedirectURL("alipay.user.agreement.page.sign", string(bizBytes))
}

// ─── server-to-server: trade.query ────────────────────

// QueryTradeStatus — 查询订单状态 (server-to-server).
func (c *AlipayClient) QueryTradeStatus(ctx context.Context, outTradeNo string) (status string, err error) {
	if !c.Enabled() {
		return "", ErrAlipayNotConfigured
	}
	biz, _ := json.Marshal(map[string]string{"out_trade_no": outTradeNo})
	params := c.commonParams("alipay.trade.query")
	params["biz_content"] = string(biz)

	sig, err := SignAlipayParams(params, c.privateKey)
	if err != nil {
		return "", err
	}
	params["sign"] = sig

	form := url.Values{}
	for k, v := range params {
		form.Set(k, v)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.gateway(), strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded;charset=utf-8")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("%w: status=%d body=%s", ErrAlipayAPIError, resp.StatusCode, body)
	}
	var wrap struct {
		Resp struct {
			Code        string `json:"code"`
			Msg         string `json:"msg"`
			TradeStatus string `json:"trade_status"`
			OutTradeNo  string `json:"out_trade_no"`
		} `json:"alipay_trade_query_response"`
	}
	if err := json.Unmarshal(body, &wrap); err != nil {
		return "", fmt.Errorf("alipay decode: %w", err)
	}
	if wrap.Resp.Code != "10000" {
		return "", fmt.Errorf("%w: code=%s msg=%s", ErrAlipayAPIError, wrap.Resp.Code, wrap.Resp.Msg)
	}
	return wrap.Resp.TradeStatus, nil
}

// ─── 回调验签 ──────────────────────────────────────

// VerifyCallback — 异步通知验签 (POST form). 解析 + 验签.
//
// 调用方应:
//  1. http.Request.ParseForm()
//  2. 把 form 传进来
//  3. 拒绝 ErrAlipayBadSignature; 否则按 form["trade_status"] 处理业务
func (c *AlipayClient) VerifyCallback(form url.Values) error {
	if !c.Enabled() {
		return ErrAlipayNotConfigured
	}
	if c.publicKey == nil {
		return errors.New("alipay: alipay public key not configured")
	}
	return VerifyFormValues(form, c.publicKey)
}

// ─── helpers ─────────────────────────────────────────

func (c *AlipayClient) buildRedirectURL(method, bizContent string) (string, error) {
	params := c.commonParams(method)
	if c.cfg.ReturnURL != "" {
		params["return_url"] = c.cfg.ReturnURL
	}
	params["biz_content"] = bizContent

	query, err := FormatSignedQuery(params, c.privateKey)
	if err != nil {
		return "", err
	}
	return c.gateway() + "?" + query, nil
}

func buildBizContent(args AlipayTradeArgs, productCode string) (string, error) {
	if args.OutTradeNo == "" || args.Subject == "" || args.TotalAmount <= 0 {
		return "", errors.New("alipay trade: out_trade_no / subject / total_amount required")
	}
	body := struct {
		OutTradeNo     string `json:"out_trade_no"`
		TotalAmount    string `json:"total_amount"`
		Subject        string `json:"subject"`
		Body           string `json:"body,omitempty"`
		ProductCode    string `json:"product_code"`
		TimeoutExpress string `json:"timeout_express,omitempty"`
	}{
		OutTradeNo:     args.OutTradeNo,
		TotalAmount:    fmt.Sprintf("%.2f", args.TotalAmount),
		Subject:        args.Subject,
		Body:           args.Body,
		ProductCode:    productCode,
		TimeoutExpress: args.TimeoutExpress,
	}
	b, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// wechat.go — W5-2 微信支付 v3 客户端.
//
// 三种下单模式:
//   - Native (扫码) — PC / 公众号外, 返 code_url 给前端生成二维码
//   - JSAPI         — 公众号内 / 小程序, 返 prepay_id (前端再调 wx.requestPayment)
//   - H5            — 移动浏览器, 返 h5_url 跳转
//
// + 退款 (refund) + 关单 (close) 留给 W5-9.
//
// 配置由 system_config.payment.wechat 注入 (见 admin/SystemConfigView.vue),
// 启动时读 + ResolveConfig 在 webhook 处理时按需 reload (无需重启).
//
// 文档: https://pay.weixin.qq.com/wiki/doc/apiv3/

package billing

import (
	"bytes"
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

// WechatGateway — v3 接口域名. 国际版 / 中国版默认相同.
const WechatGateway = "https://api.mch.weixin.qq.com"

// ─── Sentinel ──────────────────────────────────────────

var (
	ErrWechatNotConfigured = errors.New("wechat: client not configured")
	ErrWechatAPIError      = errors.New("wechat: api returned error")
)

// ─── Config ────────────────────────────────────────────

// WechatConfig — 由 system_config.payment.wechat 反序列化.
//
// AppID 与 MchID 是商户号配的两个不同身份: AppID 是对接的小程序 / 公众号 /
// App 的 ID, MchID 是商户号. 同一商户可以服务多个 AppID.
type WechatConfig struct {
	Enabled            bool   `json:"enabled"`
	AppID              string `json:"app_id"`
	MchID              string `json:"mch_id"`
	APIv3Key           string `json:"apiv3_key"`            // 32 字节 (AES-GCM key, 商户后台手动设)
	CertSerialNo       string `json:"cert_serial_no"`       // 商户证书序列号
	APIClientKeyPEM    string `json:"apiclient_key_pem"`    // 商户私钥 PEM (apiclient_key.pem 内容)
	PlatformPublicKey  string `json:"platform_public_key"`  // 平台公钥 PEM (从 /v3/certificates 拉到的解密后内容)
	NotifyURL          string `json:"notify_url"`           // https 回调 URL
}

func (c WechatConfig) Validate() error {
	if !c.Enabled {
		return nil
	}
	if c.AppID == "" || c.MchID == "" {
		return errors.New("wechat: app_id / mch_id required")
	}
	if len(c.APIv3Key) != 32 {
		return ErrWechatBadAPIv3Key
	}
	if c.APIClientKeyPEM == "" {
		return errors.New("wechat: apiclient_key_pem required")
	}
	if c.NotifyURL == "" || !strings.HasPrefix(c.NotifyURL, "https://") {
		return errors.New("wechat: notify_url must be https")
	}
	return nil
}

// ─── Client ────────────────────────────────────────────

type WechatClient struct {
	cfg        WechatConfig
	privateKey *rsa.PrivateKey
	publicKey  *rsa.PublicKey // 可选, 仅回调验签用
	http       *http.Client
	now        func() time.Time
	logger     *slog.Logger
}

// NewWechatClient — 创建 client. 配置无效 / 解析失败时返错; Enabled=false 不报错但下单/验签都拒绝.
func NewWechatClient(cfg WechatConfig, logger *slog.Logger) (*WechatClient, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.Default()
	}
	c := &WechatClient{
		cfg: cfg,
		http: &http.Client{Timeout: 15 * time.Second},
		now: time.Now,
		logger: logger,
	}
	if !cfg.Enabled {
		return c, nil
	}
	pk, err := LoadWechatPrivateKey(cfg.APIClientKeyPEM)
	if err != nil {
		return nil, err
	}
	c.privateKey = pk
	if cfg.PlatformPublicKey != "" {
		pub, err := LoadWechatPublicKey(cfg.PlatformPublicKey)
		if err != nil {
			return nil, err
		}
		c.publicKey = pub
	}
	return c, nil
}

// SetClock — 测试注入.
func (c *WechatClient) SetClock(now func() time.Time) { c.now = now }

// SetHTTPClient — 测试注入 mock server.
func (c *WechatClient) SetHTTPClient(h *http.Client) { c.http = h }

// Enabled — 配置是否启用.
func (c *WechatClient) Enabled() bool { return c != nil && c.cfg.Enabled }

// ─── 下单请求结构 ──────────────────────────────────────

// WechatOrderRequest — 三种下单共用核心字段.
type WechatOrderRequest struct {
	Description string `json:"description"`            // 商品描述 ≤ 127 字
	OutTradeNo  string `json:"out_trade_no"`           // 商户订单号 ≤ 32 字, 唯一
	TotalCents  int64  `json:"total_cents"`            // 金额 (分)
	Currency    string `json:"currency,omitempty"`     // 默认 CNY
	UserID      string `json:"user_id,omitempty"`      // 内部 user uuid, 写入 attach
	Subject     string `json:"subject,omitempty"`      // 业务标题
	// JSAPI 必填:
	OpenID string `json:"openid,omitempty"`
	// H5 必填:
	ClientIP string `json:"client_ip,omitempty"`
}

func (r WechatOrderRequest) validate(forJSAPI, forH5 bool) error {
	if r.Description == "" || r.OutTradeNo == "" || r.TotalCents <= 0 {
		return errors.New("wechat order: description / out_trade_no / total_cents required")
	}
	if forJSAPI && r.OpenID == "" {
		return errors.New("wechat JSAPI: openid required")
	}
	if forH5 && r.ClientIP == "" {
		return errors.New("wechat H5: client_ip required")
	}
	return nil
}

// ─── Native (扫码) ──────────────────────────────────

type wechatNativeBody struct {
	AppID       string                 `json:"appid"`
	MchID       string                 `json:"mchid"`
	Description string                 `json:"description"`
	OutTradeNo  string                 `json:"out_trade_no"`
	NotifyURL   string                 `json:"notify_url"`
	Attach      string                 `json:"attach,omitempty"`
	Amount      wechatAmount           `json:"amount"`
	SceneInfo   *wechatNativeSceneInfo `json:"scene_info,omitempty"`
}

type wechatAmount struct {
	Total    int64  `json:"total"`
	Currency string `json:"currency"`
}

type wechatNativeSceneInfo struct {
	PayerClientIP string `json:"payer_client_ip,omitempty"`
}

// CreateNativeOrder — 创建扫码支付订单, 返 code_url (前端转 QR).
func (c *WechatClient) CreateNativeOrder(ctx context.Context, req WechatOrderRequest) (codeURL string, err error) {
	if !c.Enabled() {
		return "", ErrWechatNotConfigured
	}
	if err := req.validate(false, false); err != nil {
		return "", err
	}
	body := wechatNativeBody{
		AppID:       c.cfg.AppID,
		MchID:       c.cfg.MchID,
		Description: req.Description,
		OutTradeNo:  req.OutTradeNo,
		NotifyURL:   c.cfg.NotifyURL,
		Attach:      req.UserID,
		Amount:      wechatAmount{Total: req.TotalCents, Currency: defaultCurrency(req.Currency)},
	}
	var resp struct {
		CodeURL string `json:"code_url"`
	}
	if err := c.doPost(ctx, "/v3/pay/transactions/native", body, &resp); err != nil {
		return "", err
	}
	return resp.CodeURL, nil
}

// ─── JSAPI (公众号 / 小程序) ─────────────────────

type wechatJSAPIBody struct {
	wechatNativeBody
	Payer wechatPayer `json:"payer"`
}

type wechatPayer struct {
	OpenID string `json:"openid"`
}

// CreateJSAPIOrder — 创建 JSAPI 订单, 返 prepay_id (前端再二次签名调起).
func (c *WechatClient) CreateJSAPIOrder(ctx context.Context, req WechatOrderRequest) (prepayID string, err error) {
	if !c.Enabled() {
		return "", ErrWechatNotConfigured
	}
	if err := req.validate(true, false); err != nil {
		return "", err
	}
	body := wechatJSAPIBody{
		wechatNativeBody: wechatNativeBody{
			AppID:       c.cfg.AppID,
			MchID:       c.cfg.MchID,
			Description: req.Description,
			OutTradeNo:  req.OutTradeNo,
			NotifyURL:   c.cfg.NotifyURL,
			Attach:      req.UserID,
			Amount:      wechatAmount{Total: req.TotalCents, Currency: defaultCurrency(req.Currency)},
		},
		Payer: wechatPayer{OpenID: req.OpenID},
	}
	var resp struct {
		PrepayID string `json:"prepay_id"`
	}
	if err := c.doPost(ctx, "/v3/pay/transactions/jsapi", body, &resp); err != nil {
		return "", err
	}
	return resp.PrepayID, nil
}

// ─── H5 (移动浏览器) ──────────────────────────────

type wechatH5Body struct {
	wechatNativeBody
	SceneInfo wechatH5SceneInfo `json:"scene_info"`
}

type wechatH5SceneInfo struct {
	PayerClientIP string         `json:"payer_client_ip"`
	H5Info        wechatH5Info   `json:"h5_info"`
}

type wechatH5Info struct {
	Type string `json:"type"` // 一般填 "Wap"
}

// CreateH5Order — 创建 H5 订单, 返 h5_url (跳转地址, 5 分钟内有效).
func (c *WechatClient) CreateH5Order(ctx context.Context, req WechatOrderRequest) (h5URL string, err error) {
	if !c.Enabled() {
		return "", ErrWechatNotConfigured
	}
	if err := req.validate(false, true); err != nil {
		return "", err
	}
	body := wechatH5Body{
		wechatNativeBody: wechatNativeBody{
			AppID:       c.cfg.AppID,
			MchID:       c.cfg.MchID,
			Description: req.Description,
			OutTradeNo:  req.OutTradeNo,
			NotifyURL:   c.cfg.NotifyURL,
			Attach:      req.UserID,
			Amount:      wechatAmount{Total: req.TotalCents, Currency: defaultCurrency(req.Currency)},
		},
		SceneInfo: wechatH5SceneInfo{
			PayerClientIP: req.ClientIP,
			H5Info:        wechatH5Info{Type: "Wap"},
		},
	}
	var resp struct {
		H5URL string `json:"h5_url"`
	}
	if err := c.doPost(ctx, "/v3/pay/transactions/h5", body, &resp); err != nil {
		return "", err
	}
	return resp.H5URL, nil
}

// ─── 回调处理 ──────────────────────────────────────────

// WechatCallback — 微信支付通知 body shape.
type WechatCallback struct {
	ID           string                 `json:"id"`
	CreateTime   string                 `json:"create_time"`
	ResourceType string                 `json:"resource_type"`
	EventType    string                 `json:"event_type"` // TRANSACTION.SUCCESS / REFUND.SUCCESS / 等
	Summary      string                 `json:"summary"`
	Resource     WechatCallbackResource `json:"resource"`
}

type WechatCallbackResource struct {
	Algorithm      string `json:"algorithm"`
	Ciphertext     string `json:"ciphertext"`
	AssociatedData string `json:"associated_data"`
	OriginalType   string `json:"original_type"`
	Nonce          string `json:"nonce"`
}

// WechatTransaction — TRANSACTION.SUCCESS 解密后内容 (核心字段).
type WechatTransaction struct {
	OutTradeNo     string `json:"out_trade_no"`
	TransactionID  string `json:"transaction_id"`
	TradeState     string `json:"trade_state"` // SUCCESS / REFUND / NOTPAY 等
	TradeStateDesc string `json:"trade_state_desc"`
	SuccessTime    string `json:"success_time"`
	Attach         string `json:"attach,omitempty"`
	Amount         struct {
		Total       int64  `json:"total"`
		PayerTotal  int64  `json:"payer_total"`
		Currency    string `json:"currency"`
	} `json:"amount"`
	Payer struct {
		OpenID string `json:"openid"`
	} `json:"payer"`
}

// VerifyAndDecodeCallback — 验签 + 解密. 返业务 transaction.
//
// 调用方负责从 HTTP request 取 headers + body 传入. 返 *WechatTransaction
// 反序列化失败时返原始 plaintext 让上层兜底.
func (c *WechatClient) VerifyAndDecodeCallback(headers http.Header, body []byte) (*WechatTransaction, error) {
	if !c.Enabled() {
		return nil, ErrWechatNotConfigured
	}
	if c.publicKey == nil {
		return nil, errors.New("wechat: platform public key not configured (unable to verify callback)")
	}
	timestamp := headers.Get("Wechatpay-Timestamp")
	nonce := headers.Get("Wechatpay-Nonce")
	signature := headers.Get("Wechatpay-Signature")

	if err := VerifyCallbackSignature(timestamp, nonce, string(body), signature, c.publicKey, c.now()); err != nil {
		return nil, err
	}
	var cb WechatCallback
	if err := json.Unmarshal(body, &cb); err != nil {
		return nil, fmt.Errorf("wechat callback decode: %w", err)
	}
	plaintext, err := DecryptCallbackResource(c.cfg.APIv3Key, cb.Resource.Ciphertext, cb.Resource.AssociatedData, cb.Resource.Nonce)
	if err != nil {
		return nil, err
	}
	var tx WechatTransaction
	if err := json.Unmarshal(plaintext, &tx); err != nil {
		return nil, fmt.Errorf("wechat transaction decode: %w", err)
	}
	return &tx, nil
}

// ─── HTTP plumbing ─────────────────────────────────────

// doPost — POST + 签名 + JSON encode/decode.
func (c *WechatClient) doPost(ctx context.Context, path string, body any, out any) error {
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, WechatGateway+path, bytes.NewReader(bodyBytes))
	if err != nil {
		return err
	}
	canonicalURL := canonicalPath(path)
	auth, err := SignRequest(http.MethodPost, canonicalURL, string(bodyBytes), c.privateKey, c.cfg.MchID, c.cfg.CertSerialNo, c.now())
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", auth)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "biumind-identity/1.0")

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%w: status=%d body=%s", ErrWechatAPIError, resp.StatusCode, respBody)
	}
	if out != nil {
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("wechat decode: %w body=%s", err, respBody)
		}
	}
	return nil
}

// canonicalPath — POST 路径含 query 时, 签名要包含 ? 之后部分.
func canonicalPath(path string) string {
	u, err := url.Parse(path)
	if err != nil {
		return path
	}
	if u.RawQuery == "" {
		return u.Path
	}
	return u.Path + "?" + u.RawQuery
}

func defaultCurrency(c string) string {
	if c == "" {
		return "CNY"
	}
	return c
}

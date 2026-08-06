// google_play.go — W6-5 Google Play Real-time Developer Notifications + 订阅状态.
//
// RTDN 协议:
//   Google Cloud Pub/Sub push subscription → 我方 callback. body 是 RTDN
//   message envelope: {message: {data: <base64 JSON>, messageId, publishTime}}.
//   data 解码后是 DeveloperNotification: {version, packageName, eventTimeMillis,
//   subscriptionNotification | oneTimeProductNotification | testNotification}.
//
// purchaseToken 真校验需要调 androidpublisher.purchases.subscriptions.get
// (Service Account JWT bearer). 真集成在 main.go wire ServiceAccountJSON 后开启;
// 本文件只提供 RTDN payload 解析 + 一个 client interface 让上层注入真实
// API caller (生产) 或 mock (单测).
//
// 文档: https://developer.android.com/google/play/billing/rtdn-reference

package billing

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
)

var (
	ErrGooglePlayBadEnvelope    = errors.New("google_play: malformed pub/sub envelope")
	ErrGooglePlayUnknownVersion = errors.New("google_play: unsupported notification version")
)

// ─── pub/sub envelope ─────────────────────────

type pubsubEnvelope struct {
	Message      pubsubMessage `json:"message"`
	Subscription string        `json:"subscription"`
}

type pubsubMessage struct {
	Data        string            `json:"data"` // base64 JSON
	MessageID   string            `json:"messageId"`
	PublishTime string            `json:"publishTime"`
	Attributes  map[string]string `json:"attributes,omitempty"`
}

// ─── DeveloperNotification ────────────────────

// GooglePlayNotification — RTDN 解码后内容.
type GooglePlayNotification struct {
	Version                  string                         `json:"version"`
	PackageName              string                         `json:"packageName"`
	EventTimeMillis          string                         `json:"eventTimeMillis"`
	SubscriptionNotification *GooglePlaySubscriptionNotif   `json:"subscriptionNotification,omitempty"`
	OneTimeProductNotif      *GooglePlayOneTimeNotif        `json:"oneTimeProductNotification,omitempty"`
	VoidedPurchaseNotif      *GooglePlayVoidedPurchaseNotif `json:"voidedPurchaseNotification,omitempty"`
	TestNotification         *GooglePlayTestNotif           `json:"testNotification,omitempty"`
}

type GooglePlaySubscriptionNotif struct {
	Version          string `json:"version"`
	NotificationType int    `json:"notificationType"` // 1-13 见 RTDN 文档
	PurchaseToken    string `json:"purchaseToken"`
	SubscriptionID   string `json:"subscriptionId"`
}

type GooglePlayOneTimeNotif struct {
	Version          string `json:"version"`
	NotificationType int    `json:"notificationType"`
	PurchaseToken    string `json:"purchaseToken"`
	SKU              string `json:"sku"`
}

type GooglePlayVoidedPurchaseNotif struct {
	PurchaseToken string `json:"purchaseToken"`
	OrderID       string `json:"orderId"`
	ProductType   int    `json:"productType"`
	RefundType    int    `json:"refundType"`
}

type GooglePlayTestNotif struct {
	Version string `json:"version"`
}

// SubscriptionNotificationType — 13 个值 (1-based) 的语义 helpers.
const (
	GPSubRecovered            = 1
	GPSubRenewed              = 2
	GPSubCanceled             = 3
	GPSubPurchased            = 4
	GPSubOnHold               = 5
	GPSubInGracePeriod        = 6
	GPSubRestarted            = 7
	GPSubPriceChangeConfirmed = 8
	GPSubDeferred             = 9
	GPSubPaused               = 10
	GPSubPauseScheduleChanged = 11
	GPSubRevoked              = 12
	GPSubExpired              = 13
)

// ─── 解析 ──────────────────────────────────

// DecodeRTDN — 接收 Pub/Sub push body, 返 DeveloperNotification.
//
// 不验签 — 真生产路径建议 GCE / Cloud Run + Pub/Sub 的内置认证 (OIDC token).
func DecodeRTDN(body []byte) (*GooglePlayNotification, error) {
	var env pubsubEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("%w: outer json", ErrGooglePlayBadEnvelope)
	}
	if env.Message.Data == "" {
		return nil, fmt.Errorf("%w: empty data", ErrGooglePlayBadEnvelope)
	}
	rawData, err := base64.StdEncoding.DecodeString(env.Message.Data)
	if err != nil {
		// some Cloud Run integrations send URL-safe base64
		rawData, err = base64.URLEncoding.DecodeString(env.Message.Data)
		if err != nil {
			return nil, fmt.Errorf("%w: data b64 %v", ErrGooglePlayBadEnvelope, err)
		}
	}
	var n GooglePlayNotification
	if err := json.Unmarshal(rawData, &n); err != nil {
		return nil, fmt.Errorf("%w: notification json %v", ErrGooglePlayBadEnvelope, err)
	}
	if n.Version != "1.0" {
		return nil, fmt.Errorf("%w: %s", ErrGooglePlayUnknownVersion, n.Version)
	}
	return &n, nil
}

// ─── purchaseToken 校验 (待真集成) ──────

// SubscriptionPurchase — androidpublisher.purchases.subscriptions.get
// 返回的核心字段子集.
type SubscriptionPurchase struct {
	OrderID              string `json:"orderId"`
	PurchaseTimeMillis   int64  `json:"purchaseTimeMillis,omitempty"`
	ExpiryTimeMillis     int64  `json:"expiryTimeMillis,omitempty"`
	AutoRenewing         bool   `json:"autoRenewing"`
	PaymentState         int    `json:"paymentState"` // 0=Pending, 1=Received, 2=Free trial, 3=Pending deferred upgrade
	CountryCode          string `json:"countryCode"`
	AcknowledgementState int    `json:"acknowledgementState"` // 0=Yet to be ACKed, 1=Acknowledged
	Kind                 string `json:"kind"`
}

// GooglePlayClient — purchases.subscriptions.get / acknowledge 接口.
//
// 生产实现走真 Service Account JWT. 单测走 mock.
type GooglePlayClient interface {
	GetSubscription(ctx context.Context, packageName, subscriptionID, purchaseToken string) (*SubscriptionPurchase, error)
	AcknowledgeSubscription(ctx context.Context, packageName, subscriptionID, purchaseToken string) error
}

// NoopGooglePlayClient — 默认占位, 所有方法返 ErrGooglePlayClientDisabled.
// 生产 main.go 注入真实实现.
type NoopGooglePlayClient struct{}

var ErrGooglePlayClientDisabled = errors.New("google_play: API client not configured")

func (NoopGooglePlayClient) GetSubscription(_ context.Context, _, _, _ string) (*SubscriptionPurchase, error) {
	return nil, ErrGooglePlayClientDisabled
}
func (NoopGooglePlayClient) AcknowledgeSubscription(_ context.Context, _, _, _ string) error {
	return ErrGooglePlayClientDisabled
}

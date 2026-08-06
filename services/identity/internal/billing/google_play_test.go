// W6-5 Google Play RTDN — 8 测试.

package billing

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"
)

// ─── helpers ───────────────────────────────

func encRTDN(t *testing.T, n GooglePlayNotification) []byte {
	t.Helper()
	dataJSON, err := json.Marshal(n)
	if err != nil {
		t.Fatal(err)
	}
	dataB64 := base64.StdEncoding.EncodeToString(dataJSON)
	env := map[string]any{
		"message": map[string]any{
			"data":        dataB64,
			"messageId":   "msg-1",
			"publishTime": "2026-07-01T00:00:00Z",
		},
		"subscription": "projects/x/subscriptions/y",
	}
	b, _ := json.Marshal(env)
	return b
}

// 1. happy: subscription notification renewed.
func TestGP_DecodeRTDN_SubscriptionRenewed(t *testing.T) {
	body := encRTDN(t, GooglePlayNotification{
		Version: "1.0", PackageName: "com.biumind.app",
		EventTimeMillis: "1750000000000",
		SubscriptionNotification: &GooglePlaySubscriptionNotif{
			Version: "1.0", NotificationType: GPSubRenewed,
			PurchaseToken: "tok-renew", SubscriptionID: "pro_monthly",
		},
	})
	n, err := DecodeRTDN(body)
	if err != nil {
		t.Fatal(err)
	}
	if n.SubscriptionNotification == nil ||
		n.SubscriptionNotification.NotificationType != GPSubRenewed {
		t.Fatalf("got %+v", n.SubscriptionNotification)
	}
	if n.PackageName != "com.biumind.app" {
		t.Fatalf("pkg = %s", n.PackageName)
	}
}

// 2. one-time product
func TestGP_DecodeRTDN_OneTime(t *testing.T) {
	body := encRTDN(t, GooglePlayNotification{
		Version: "1.0", PackageName: "com.biumind.app",
		EventTimeMillis: "1750000000000",
		OneTimeProductNotif: &GooglePlayOneTimeNotif{
			Version: "1.0", NotificationType: 1, // ONE_TIME_PRODUCT_PURCHASED
			PurchaseToken: "tok-1t", SKU: "credits_1500",
		},
	})
	n, err := DecodeRTDN(body)
	if err != nil {
		t.Fatal(err)
	}
	if n.OneTimeProductNotif == nil || n.OneTimeProductNotif.SKU != "credits_1500" {
		t.Fatalf("got %+v", n.OneTimeProductNotif)
	}
}

// 3. voided
func TestGP_DecodeRTDN_Voided(t *testing.T) {
	body := encRTDN(t, GooglePlayNotification{
		Version: "1.0", PackageName: "com.biumind.app",
		EventTimeMillis: "1",
		VoidedPurchaseNotif: &GooglePlayVoidedPurchaseNotif{
			PurchaseToken: "tok-voided", OrderID: "GPA.000",
			ProductType: 1, RefundType: 1,
		},
	})
	n, err := DecodeRTDN(body)
	if err != nil {
		t.Fatal(err)
	}
	if n.VoidedPurchaseNotif == nil || n.VoidedPurchaseNotif.OrderID != "GPA.000" {
		t.Fatalf("got %+v", n.VoidedPurchaseNotif)
	}
}

// 4. test notification
func TestGP_DecodeRTDN_Test(t *testing.T) {
	body := encRTDN(t, GooglePlayNotification{
		Version: "1.0", PackageName: "com.biumind.app",
		EventTimeMillis: "1",
		TestNotification: &GooglePlayTestNotif{Version: "1.0"},
	})
	n, err := DecodeRTDN(body)
	if err != nil {
		t.Fatal(err)
	}
	if n.TestNotification == nil {
		t.Fatal("test notif missing")
	}
}

// 5. malformed envelope
func TestGP_DecodeRTDN_Malformed(t *testing.T) {
	if _, err := DecodeRTDN([]byte("not json")); err == nil {
		t.Fatal("should fail")
	}
}

// 6. missing data
func TestGP_DecodeRTDN_NoData(t *testing.T) {
	body, _ := json.Marshal(map[string]any{
		"message": map[string]any{"messageId": "x"},
	})
	if _, err := DecodeRTDN(body); err == nil {
		t.Fatal("empty data should fail")
	}
}

// 7. unsupported version
func TestGP_DecodeRTDN_BadVersion(t *testing.T) {
	body := encRTDN(t, GooglePlayNotification{
		Version: "0.5", PackageName: "p",
	})
	_, err := DecodeRTDN(body)
	if err == nil {
		t.Fatal("0.5 version should fail")
	}
}

// 8. NoopGooglePlayClient 返 ErrGooglePlayClientDisabled.
func TestGP_NoopClient(t *testing.T) {
	c := NoopGooglePlayClient{}
	if _, err := c.GetSubscription(context.Background(), "pkg", "sub", "tok"); err != ErrGooglePlayClientDisabled {
		t.Fatalf("got %v want disabled", err)
	}
	if err := c.AcknowledgeSubscription(context.Background(), "pkg", "sub", "tok"); err != ErrGooglePlayClientDisabled {
		t.Fatalf("got %v want disabled", err)
	}
}

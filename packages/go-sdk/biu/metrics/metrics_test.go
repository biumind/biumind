package metrics

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSetService_StampsLabel(t *testing.T) {
	SetService("model-relay")
	RecordQuota("model-relay.rpm", true, 99)

	body := scrape(t)
	// Counter line should include the service label.
	if !strings.Contains(body, `biumind_quota_check_total{bucket="model-relay.rpm",decision="allow",service="model-relay"} 1`) {
		t.Errorf("missing labelled counter line in:\n%s", excerpt(body, "biumind_quota"))
	}
}

func TestRecordQuota_AllowAndDenyAreCountedSeparately(t *testing.T) {
	SetService("model-relay")
	for i := 0; i < 3; i++ {
		RecordQuota("model-relay.tpm", true, 50)
	}
	for i := 0; i < 2; i++ {
		RecordQuota("model-relay.tpm", false, 0)
	}
	body := scrape(t)
	if !contains(body, `biumind_quota_check_total{bucket="model-relay.tpm",decision="allow",service="model-relay"} 3`) {
		t.Errorf("allow count wrong:\n%s", excerpt(body, "model-relay.tpm"))
	}
	if !contains(body, `biumind_quota_check_total{bucket="model-relay.tpm",decision="deny",service="model-relay"} 2`) {
		t.Errorf("deny count wrong:\n%s", excerpt(body, "model-relay.tpm"))
	}
}

func TestRecordEmbedBatch_OkAndError(t *testing.T) {
	SetService("brain")
	RecordEmbedBatch(5, 2)
	RecordEmbedBatch(3, 0)
	body := scrape(t)
	if !contains(body, `biumind_memory_embed_processed_total{outcome="ok",service="brain"} 8`) {
		t.Errorf("ok count wrong:\n%s", excerpt(body, "memory_embed"))
	}
	if !contains(body, `biumind_memory_embed_processed_total{outcome="error",service="brain"} 2`) {
		t.Errorf("err count wrong:\n%s", excerpt(body, "memory_embed"))
	}
}

func TestSetEmbedPending_OverwritesGauge(t *testing.T) {
	SetService("brain")
	SetEmbedPending(100)
	SetEmbedPending(42)
	body := scrape(t)
	if !contains(body, `biumind_memory_embed_pending{service="brain"} 42`) {
		t.Errorf("pending gauge wrong:\n%s", excerpt(body, "memory_embed_pending"))
	}
}

func TestRecordRelayTokens_SplitsKinds(t *testing.T) {
	SetService("model-relay")
	RecordRelayTokens(100, 50, 20, 10)
	RecordRelayTokens(0, 0, 5, 0) // zero-value kinds must be skipped
	body := scrape(t)
	for _, line := range []string{
		`biumind_hub_tokens_charged_total{kind="prompt",service="model-relay"} 100`,
		`biumind_hub_tokens_charged_total{kind="completion",service="model-relay"} 50`,
		`biumind_hub_tokens_charged_total{kind="cache_read",service="model-relay"} 25`,
		`biumind_hub_tokens_charged_total{kind="cache_write",service="model-relay"} 10`,
	} {
		if !contains(body, line) {
			t.Errorf("missing line %q in:\n%s", line, excerpt(body, "tokens"))
		}
	}
}

func TestRecordHubRequest_StampsStatus(t *testing.T) {
	SetService("model-relay")
	RecordHubRequest("/v1/messages", 200)
	RecordHubRequest("/v1/messages", 200)
	RecordHubRequest("/v1/messages", 429)
	body := scrape(t)
	if !contains(body, `biumind_hub_request_total{path="/v1/messages",service="model-relay",status="200"} 2`) {
		t.Errorf("200 count wrong:\n%s", excerpt(body, "hub_request"))
	}
	if !contains(body, `biumind_hub_request_total{path="/v1/messages",service="model-relay",status="429"} 1`) {
		t.Errorf("429 count wrong:\n%s", excerpt(body, "hub_request"))
	}
}

func TestHandler_ServesPrometheusFormat(t *testing.T) {
	h := Handler()
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/metrics", nil)
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Errorf("status: %d", w.Code)
	}
	body := w.Body.String()
	if !strings.HasPrefix(body, "# HELP") {
		t.Errorf("missing prometheus banner; got: %q", excerpt(body, "HELP"))
	}
}

func TestRecordCancelRequest_LabelsAreStable(t *testing.T) {
	SetService("brain")
	// 触发每条已知 outcome,断言 metrics 体里都能看到。这把
	// outcome 枚举锁死在 metrics.go 注释 + ingress.maybeRouteCancel
	// 的实际取值之间不漂移。
	RecordCancelRequest("chat", "chat_inprocess")
	RecordCancelRequest("agent", "control_queue")
	RecordCancelRequest("agent", "no_route_no_env")
	RecordCancelRequest("agent", "queue_unavailable")
	RecordCancelRequest("unknown", "parse_error")

	body := scrape(t)
	for _, frag := range []string{
		`agent_cancel_requests_total{mode="chat",outcome="chat_inprocess",service="brain"} 1`,
		`agent_cancel_requests_total{mode="agent",outcome="control_queue",service="brain"} 1`,
		`agent_cancel_requests_total{mode="agent",outcome="no_route_no_env",service="brain"} 1`,
		`agent_cancel_requests_total{mode="agent",outcome="queue_unavailable",service="brain"} 1`,
		`agent_cancel_requests_total{mode="unknown",outcome="parse_error",service="brain"} 1`,
	} {
		if !contains(body, frag) {
			t.Errorf("missing line %q in:\n%s", frag,
				excerpt(body, "agent_cancel_requests_total"))
		}
	}
}

func TestRecordCancelLatency_BucketsObservation(t *testing.T) {
	SetService("brain")
	// 喂 3 个不同延迟,看直方图的 _count 跟 bucket 都对。
	RecordCancelLatency("chat", 80*time.Millisecond)
	RecordCancelLatency("chat", 300*time.Millisecond)
	RecordCancelLatency("chat", 1500*time.Millisecond)

	body := scrape(t)
	if !contains(body, `agent_cancel_latency_seconds_count{mode="chat",service="brain"}`) {
		t.Errorf("histogram count line missing:\n%s",
			excerpt(body, "agent_cancel_latency_seconds"))
	}
	// le=0.1 桶应该含 80ms 那次;le=2 应该含全部 3 次。
	if !contains(body, `agent_cancel_latency_seconds_bucket{mode="chat",service="brain",le="0.1"} 1`) {
		t.Errorf("0.1s bucket should count 80ms observation:\n%s",
			excerpt(body, "agent_cancel_latency_seconds_bucket"))
	}
	if !contains(body, `agent_cancel_latency_seconds_bucket{mode="chat",service="brain",le="2"} 3`) {
		t.Errorf("2s bucket should count all 3 observations:\n%s",
			excerpt(body, "agent_cancel_latency_seconds_bucket"))
	}
}

// ─── helpers ────────────────────────────────────────────

func scrape(t *testing.T) string {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/metrics", nil)
	Handler().ServeHTTP(w, r)
	return w.Body.String()
}

func contains(haystack, needle string) bool {
	return strings.Contains(haystack, needle)
}

func excerpt(haystack, marker string) string {
	lines := strings.Split(haystack, "\n")
	out := make([]string, 0, 16)
	for _, l := range lines {
		if strings.Contains(l, marker) {
			out = append(out, l)
			if len(out) >= 8 {
				break
			}
		}
	}
	return strings.Join(out, "\n")
}

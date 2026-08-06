// Package metrics — Prometheus collectors shared across BiuMind services.
//
// Every service that wants to be scraped does two things at boot:
//
//  1. mux.Handle("/metrics", metrics.Handler())
//  2. metrics.SetService("model-relay")  // (or "brain", "sandbox", …)
//
// Collectors are package-scoped singletons registered against the
// default Prometheus registry, so multiple services in one process
// (e.g. a smoke test) share the same series. Labels carry the service
// name so cross-service dashboards can filter without having to scrape
// distinct ports.
//
// Collectors land here when more than one service emits them. Service-
// local metrics (model-relay.relay request counts, brain.events arrival rate)
// stay in the service package — keep this file the cross-cutting
// vocabulary, not a kitchen sink.
package metrics

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// service is the label value attached to every collector below. Set
// once at boot via SetService; default "unknown" so unconfigured
// processes don't break scrapes.
var serviceName atomic.Value // string

func init() {
	serviceName.Store("unknown")
}

// SetService stamps the service label on every emitted metric. Call
// once at boot before any Record* invocation.
func SetService(name string) {
	if name != "" {
		serviceName.Store(name)
	}
}

func svc() string { return serviceName.Load().(string) }

// Handler returns the standard Prometheus /metrics HTTP handler.
// Wrap it with auth at the gateway when exposing publicly.
func Handler() http.Handler { return promhttp.Handler() }

// ─── Quota collectors ───────────────────────────────────

var (
	quotaCheckTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "biumind_quota_check_total",
			Help: "Quota CheckAndReserve calls grouped by bucket and decision.",
		},
		[]string{"service", "bucket", "decision"},
	)
	quotaRemaining = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "biumind_quota_remaining",
			Help: "Last-observed remaining quota for (service, bucket, key_hash).",
		},
		// key_hash so we don't blow out cardinality on user-id labels.
		[]string{"service", "bucket"},
	)
)

// RecordQuota emits one observation per CheckAndReserve. `allowed`
// drives the `decision` label ("allow" / "deny"); `remaining` updates
// the per-bucket gauge so dashboards can graph headroom.
func RecordQuota(bucket string, allowed bool, remaining int64) {
	dec := "allow"
	if !allowed {
		dec = "deny"
	}
	quotaCheckTotal.WithLabelValues(svc(), bucket, dec).Inc()
	if remaining >= 0 {
		quotaRemaining.WithLabelValues(svc(), bucket).Set(float64(remaining))
	}
}

// ─── Memory embed worker collectors ─────────────────────

var (
	memoryEmbedProcessedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "biumind_memory_embed_processed_total",
			Help: "Memories embedded by the backfill worker, by outcome.",
		},
		[]string{"service", "outcome"}, // outcome: "ok" / "error"
	)
	memoryEmbedPending = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "biumind_memory_embed_pending",
			Help: "Memories currently lacking an embedding (last observation).",
		},
		[]string{"service"},
	)
	memoryEmbedBatchSize = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "biumind_memory_embed_batch_size",
			Help:    "Rows committed per embed worker tick.",
			Buckets: prometheus.LinearBuckets(0, 8, 12), // 0..96 in steps of 8
		},
	)
)

// RecordEmbedBatch updates worker-tick metrics. ok is the count of
// successful embeddings committed in this tick; failed is how many
// rows were claimed but not embedded (provider error, dim mismatch,
// etc).
func RecordEmbedBatch(ok, failed int) {
	if ok > 0 {
		memoryEmbedProcessedTotal.WithLabelValues(svc(), "ok").Add(float64(ok))
		memoryEmbedBatchSize.Observe(float64(ok))
	}
	if failed > 0 {
		memoryEmbedProcessedTotal.WithLabelValues(svc(), "error").Add(float64(failed))
	}
}

// SetEmbedPending writes the current backlog gauge. Workers call this
// after each tick (or on a slower poll if the table is huge).
func SetEmbedPending(n int64) {
	memoryEmbedPending.WithLabelValues(svc()).Set(float64(n))
}

// ─── Memory consolidator collectors ─────────────────────

var memoryConsolidationTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "biumind_memory_consolidation_total",
		Help: "Memory consolidation outcomes per pass.",
	},
	[]string{"service", "outcome"}, // outcome: "merged" / "decayed"
)

// RecordConsolidation tallies one consolidator pass.
func RecordConsolidation(merged, decayed int) {
	if merged > 0 {
		memoryConsolidationTotal.WithLabelValues(svc(), "merged").
			Add(float64(merged))
	}
	if decayed > 0 {
		memoryConsolidationTotal.WithLabelValues(svc(), "decayed").
			Add(float64(decayed))
	}
}

// ─── model-relay relay collectors ───────────────────────────────

var (
	hubRequestTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "biumind_hub_request_total",
			Help: "model-relay relay requests by path and HTTP status.",
		},
		[]string{"service", "path", "status"},
	)
	relayTokensChargedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "biumind_hub_tokens_charged_total",
			Help: "Tokens charged to model-relay.tpm bucket by category.",
		},
		[]string{"service", "kind"}, // prompt / completion / cache_read / cache_write
	)
)

// RecordHubRequest increments the path/status counter for a single
// relay call. Path is canonicalised by the caller (e.g. "/v1/messages"
// without query strings) to keep cardinality bounded.
func RecordHubRequest(path string, status int) {
	hubRequestTotal.WithLabelValues(svc(), path, strconv.Itoa(status)).Inc()
}

// RecordRelayTokens splits the 4 Anthropic-style token kinds into
// separate counters so spend can be modelled (cache_read is ~10× cheaper
// than prompt; dashboards need the breakdown).
func RecordRelayTokens(prompt, completion, cacheRead, cacheWrite int64) {
	rec := func(kind string, n int64) {
		if n > 0 {
			relayTokensChargedTotal.WithLabelValues(svc(), kind).Add(float64(n))
		}
	}
	rec("prompt", prompt)
	rec("completion", completion)
	rec("cache_read", cacheRead)
	rec("cache_write", cacheWrite)
}

// ─── model-relay LLM (model + provider + plan 维度) ─────────
//
// 上面 relayTokensChargedTotal 是按 kind 切的. 业务仪表板还要看:
//   - 哪个模型用最多 / 哪家 provider 流量大
//   - 不同 plan (free/pro/team) 的消耗对比
//   - 每次调用的 latency (LLM 长尾延迟比 HTTP latency 重要)
//   - 钱花在哪 (按 model × provider 聚合美分)
//
// label cardinality 控制:
//   model:   ~20 种 (主流 LLM, 见 model-relay/internal/pricing)
//   provider: 2-5 (anthropic/openai/google/...)
//   plan:    3 (free/pro/team)
//   总组合 ~300, 可控.
//
// 不加 user_id label — 用户万级会让 series 爆炸. per-user 统计走 SQL.

var (
	hubLLMTokensTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "biumind_hub_llm_tokens_total",
			Help: "LLM token consumption by model/provider/plan/kind.",
		},
		[]string{"service", "model", "provider", "plan", "kind"},
		// kind: prompt | completion | cache_read | cache_write
	)
	hubLLMCostMillicentsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "biumind_hub_llm_cost_millicents_total",
			Help: "LLM cost accumulated in millicents (千分之一美分). 美元成本 = total / 100_000.",
		},
		[]string{"service", "model", "provider", "plan"},
	)
	hubLLMRequestTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "biumind_hub_llm_requests_total",
			Help: "LLM request count by model/provider/plan/status.",
		},
		[]string{"service", "model", "provider", "plan", "status"},
		// status: success | error
	)
	hubLLMRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "biumind_hub_llm_request_duration_seconds",
			Help:    "LLM request latency by model/provider.",
			Buckets: []float64{0.5, 1, 2, 5, 10, 30, 60, 120, 300, 600},
		},
		[]string{"service", "model", "provider"},
	)
)

// RecordHubLLMRequest 一次完整 LLM 调用 (流式或同步皆调). plan 取
// claims.Roles 不到, 调用方拿 plan='free' 兜底.
//
// success=false 时 token / cost 仍记录 (可能拿到部分 usage), status='error'.
func RecordHubLLMRequest(
	model, provider, plan string,
	prompt, completion, cacheRead, cacheWrite int64,
	costMillicents int64,
	durationSeconds float64,
	success bool,
) {
	if model == "" {
		model = "unknown"
	}
	if provider == "" {
		provider = "unknown"
	}
	if plan == "" {
		plan = "free"
	}
	rec := func(kind string, n int64) {
		if n > 0 {
			hubLLMTokensTotal.WithLabelValues(svc(), model, provider, plan, kind).Add(float64(n))
		}
	}
	rec("prompt", prompt)
	rec("completion", completion)
	rec("cache_read", cacheRead)
	rec("cache_write", cacheWrite)

	if costMillicents > 0 {
		hubLLMCostMillicentsTotal.
			WithLabelValues(svc(), model, provider, plan).
			Add(float64(costMillicents))
	}

	status := "success"
	if !success {
		status = "error"
	}
	hubLLMRequestTotal.WithLabelValues(svc(), model, provider, plan, status).Inc()

	if durationSeconds > 0 {
		hubLLMRequestDuration.
			WithLabelValues(svc(), model, provider).
			Observe(durationSeconds)
	}
}

// ─── Generic HTTP middleware ───────────────────────────
//
// Every Go service wraps its mux with HTTPMiddleware to auto-record
// request count / duration / inflight. 不带 route label 避免 cardinality
// 爆炸 (动态 path id 会让 series 无限增长). dashboard 按 service +
// method + status_class 切片即可定位.

var (
	httpRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "biumind_http_requests_total",
			Help: "Total HTTP requests served.",
		},
		[]string{"service", "method", "status_class"}, // status_class: 2xx/3xx/4xx/5xx
	)
	httpRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name: "biumind_http_request_duration_seconds",
			Help: "HTTP request latency.",
			// 5ms / 10ms / 25ms / 50ms / 100ms / 250ms / 500ms / 1s / 2.5s / 5s / 10s / 30s
			Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30},
		},
		[]string{"service", "method"},
	)
	httpInflight = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "biumind_http_inflight",
			Help: "Currently in-flight HTTP requests.",
		},
		[]string{"service"},
	)

	// ServiceInfo — 常驻 gauge=1, label 携带版本元数据. 部署变更可以
	// 通过这个 metric diff 检测到 (deploy_to vs running version).
	serviceInfo = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "biumind_service_info",
			Help: "Service metadata (always 1).",
		},
		[]string{"service", "version"},
	)
)

// SetServiceInfo 启动时调一次, version 写到 label, gauge 设 1. 重启
// (新版部署) 会让 series 出现新 (version) 标签, 旧的过期前还能看见.
func SetServiceInfo(version string) {
	serviceInfo.WithLabelValues(svc(), version).Set(1)
}

// HTTPMiddleware 自动记录 HTTP 指标. 用法:
//
//	mux := http.NewServeMux()
//	mux.HandleFunc(...)
//	wrapped := metrics.HTTPMiddleware(mux)
//	srv.Handler = wrapped
func HTTPMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// /metrics 自身不记录, 避免无限循环 + 噪声
		if r.URL.Path == "/metrics" {
			next.ServeHTTP(w, r)
			return
		}
		s := svc()
		httpInflight.WithLabelValues(s).Inc()
		defer httpInflight.WithLabelValues(s).Dec()

		rw := &statusRecorder{ResponseWriter: w, status: 200}
		start := time.Now()
		next.ServeHTTP(rw, r)
		dur := time.Since(start).Seconds()

		method := r.Method
		httpRequestDuration.WithLabelValues(s, method).Observe(dur)
		httpRequestsTotal.WithLabelValues(s, method, statusClass(rw.status)).Inc()
	})
}

// statusRecorder — 包 http.ResponseWriter 拿 status code.
type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (r *statusRecorder) WriteHeader(code int) {
	if !r.wroteHeader {
		r.status = code
		r.wroteHeader = true
	}
	r.ResponseWriter.WriteHeader(code)
}

// Hijack/Flush 等接口透传 — SSE 流式 / WebSocket 升级需要.
func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Hijack 透传给底层 ResponseWriter —— WebSocket upgrade 必经此路。
// 没这个 gorilla.Upgrader.Upgrade 会因为 wrapper 不实现 http.Hijacker
// 直接返 500，client 看到 "was not upgraded to websocket"。
func (r *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("metrics: underlying ResponseWriter is not http.Hijacker")
	}
	// 标记已写 header（hijack 后 server 不再走 WriteHeader 路径）。
	r.wroteHeader = true
	return h.Hijack()
}

func statusClass(s int) string {
	switch {
	case s >= 500:
		return "5xx"
	case s >= 400:
		return "4xx"
	case s >= 300:
		return "3xx"
	case s >= 200:
		return "2xx"
	default:
		return "1xx"
	}
}

// ─── model-relay admin / routing collectors (M5.6) ──────────────────
//
// 上面 hubLLM* 是 / v1/messages 端到端业务侧切片。下面这组是
// model-relay 自己的内部健康/性能可观察性，主要看:
//   - resolver 命中 channel 多快 (LLM 长尾不算在内, 只是 DB+cache+
//     decrypt 的开销; 应该 < 50ms p99)
//   - sync-upstream 每天点几下、多少次命中 ETag 短路、多少次失败
//   - channel 的健康分布 (active / disabled / auto_disabled 各多少)
//     方便告警: 当 auto_disabled 比例 > N% 时说明上游普遍翻车

var (
	modelRelayResolveDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name: "biumind_model_relay_resolve_duration_seconds",
			Help: "ModelResolver.Resolve duration: cache lookup + plan/group filter + strategy pick + envelope decrypt.",
			// resolver 路径基本全在 RAM, 长尾是 envelope.Decrypt (~100μs)
			// + DB miss 重新 load cache (~5ms)。10ms 以上就是异常。
			Buckets: []float64{0.0005, 0.001, 0.002, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0},
		},
		[]string{"service", "result"}, // result: ok | not_found | hidden | no_channel | exhausted | cred_unavailable
	)

	modelRelaySyncTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "biumind_model_relay_sync_total",
			Help: "POST /v1/admin/models/sync-upstream invocations by outcome.",
		},
		[]string{"service", "result"}, // result: ok | not_modified | upstream_error | parse_error
	)

	modelRelayChannelHealth = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "biumind_model_relay_channel_health",
			Help: "Number of channels in each status — periodic SELECT count(*) GROUP BY status.",
		},
		[]string{"service", "status"}, // status: active | disabled | auto_disabled
	)

	modelRelayFxSyncTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "biumind_model_relay_fx_sync_total",
			Help: "Daily fx-rate cron sync invocations by outcome.",
		},
		[]string{"service", "result"}, // ok / network_error / upstream_error / parse_error / db_error / internal_error
	)

	modelRelayChannelFallbackTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "biumind_model_relay_channel_fallback_total",
			Help: "Channel fallback events: a Strategy pick was rejected and the resolver fell through to the next priority. Useful for spotting overloaded keys before they cause user-visible errors.",
		},
		[]string{"service", "reason"}, // reason: rpm_exhausted / tpm_exhausted (future: cooldown / circuit_break)
	)
)

// RecordModelRelayResolve observes a Resolver.Resolve call. result
// matches the Resolver's sentinel error categories for easy alerting:
// long latency is one signal; high not_found / no_channel ratio is
// another (admin forgot to enable a model).
func RecordModelRelayResolve(result string, d time.Duration) {
	modelRelayResolveDuration.WithLabelValues(svc(), result).Observe(d.Seconds())
}

// RecordModelRelaySync increments the sync counter. result enum lets
// dashboards split successful runs / cache hits / upstream failures.
func RecordModelRelaySync(result string) {
	modelRelaySyncTotal.WithLabelValues(svc(), result).Inc()
}

// SetModelRelayChannelHealth replaces the gauge for a status bucket.
// Caller is the periodic poller in main.go (every 30s SELECT count(*)
// GROUP BY status); resetting all 3 buckets in one tick keeps the
// gauge accurate as channels move between states.
func SetModelRelayChannelHealth(status string, count int) {
	modelRelayChannelHealth.WithLabelValues(svc(), status).Set(float64(count))
}

// RecordFxSync increments the fx-sync counter. Stable result enum lets
// alert rules fire on prolonged network_error / upstream_error.
func RecordFxSync(result string) {
	modelRelayFxSyncTotal.WithLabelValues(svc(), result).Inc()
}

// RecordChannelFallback fires on every ChannelQuota.AcquireRPM rejection
// that triggers a Strategy retry. Counter, not Histogram — a single
// request that hits 3 channels until success is 2 fallbacks.
func RecordChannelFallback(reason string) {
	modelRelayChannelFallbackTotal.WithLabelValues(svc(), reason).Inc()
}

// ─── Cancel plane ──────────────────────────────────────
//
// Cancel routing(client 的 SDKControlCancelRequest → brain ingress →
// chat-mode 进程内 / control queue 反向投到 daemon)对用户体验是关键
// 路径,需要观测:
//
//   - 触发频率(用户多频繁 spam stop?)
//   - 路由命中率(chat vs daemon vs 没找到 session,只 brain 端打)
//   - 服务端延迟(按 stop 到真正 idle 的时间 — 应 <1s)
//
// brain 端 + daemon 端共用同一个 metric 名,靠 `service` label 区分。
// 这样仪表盘上写一个 query 同时拉两边,Grafana 面板按 service 切片
// 渲染就能并排比 P95。
//
//   service="brain" + mode="chat"  →  ChatRunner 进程内打断的延迟
//   service="biu_daemon"           →  Worker.handleControl → Done 帧 publish 的延迟
//   service="brain"                →  路由计数器(daemon 不会调,因为路由发生在 brain)

var (
	agentCancelRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "agent_cancel_requests_total",
			Help: "Cancel routing decisions. " +
				"outcome: chat_inprocess / control_queue / no_route_no_env / " +
				"parse_error / queue_unavailable.",
		},
		[]string{"service", "mode", "outcome"},
	)

	// 端到端延迟:服务端从「检测到 cancel」→「最终 Done{interrupted} 帧
	// 发出」的时间。包括 biumindkit clean-stop 路径(合成 tool_result +
	// emit Done) + 写 NATS / DB。一般应 <1s;>5s 多半是慢 io 工具 /
	// 网络断。
	//
	//   brain  side: ChatRunner.InterruptSession → runSessionImpl 退出
	//   daemon side: Worker.InterruptSession → Done{interrupted} publishFrame
	agentCancelLatencySeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name: "agent_cancel_latency_seconds",
			Help: "Server-side cancel latency: from cancel detected to " +
				"final Done{interrupted} frame emitted.",
			Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10},
		},
		[]string{"service", "mode"},
	)
)

// RecordCancelRequest 记一次 cancel 路由决策。outcome 用稳定枚举,
// 报警规则可以按 outcome="parse_error" 触发(协议层 bug)。
//
// mode: chat / agent / task / unknown。unknown 表示路由时还没拿到
// session 或不适用(如 parse error)。
func RecordCancelRequest(mode, outcome string) {
	agentCancelRequestsTotal.WithLabelValues(svc(), mode, outcome).Inc()
}

// RecordCancelLatency 记一次服务端 cancel 端到端延迟。brain + daemon
// 都调这个,service label(由 SetService 配置)区分两端。
func RecordCancelLatency(mode string, d time.Duration) {
	agentCancelLatencySeconds.WithLabelValues(svc(), mode).Observe(d.Seconds())
}

// ─── Registration ──────────────────────────────────────

func init() {
	prometheus.MustRegister(
		quotaCheckTotal,
		quotaRemaining,
		memoryEmbedProcessedTotal,
		memoryEmbedPending,
		memoryEmbedBatchSize,
		memoryConsolidationTotal,
		hubRequestTotal,
		relayTokensChargedTotal,
		hubLLMTokensTotal,
		hubLLMCostMillicentsTotal,
		hubLLMRequestTotal,
		hubLLMRequestDuration,
		modelRelayResolveDuration,
		modelRelaySyncTotal,
		modelRelayChannelHealth,
		modelRelayFxSyncTotal,
		modelRelayChannelFallbackTotal,
		agentCancelRequestsTotal,
		agentCancelLatencySeconds,
		httpRequestsTotal,
		httpRequestDuration,
		httpInflight,
		serviceInfo,
	)
}

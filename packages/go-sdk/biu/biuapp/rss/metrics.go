// RSS Prometheus collectors. Registered to the default Prom registry at
// init time so any service that imports this package and exposes
// /metrics will surface RSS metrics automatically.
//
// Cardinality discipline:
//   - 不带 user_id / feed_id / rule_id label —— 这些可达万级，会让 series 爆掉
//   - outcome / kind 之类的小枚举可以放心打
//   - 跨用户聚合走 Prometheus rate / sum，单用户钻取走 Postgres
//
// 关键路径打点的设计目标：M8 真 AI 上线后，能用这些指标证明
//   - 命中率（rss_radar_hits_total / rss_entries_inserted_total）
//   - AI 健康（rss_digest_calls_total{outcome=error} 是否飙升）
//   - 用户活跃（rss_active_users{window} gauge，由 main.go 周期 SQL 写入）
//   - 投递质量（rss_feed_refresh_duration_seconds p95）

package rss

import (
	"github.com/prometheus/client_golang/prometheus"
)

var (
	feedRefreshTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "biumind_rss_feed_refresh_total",
			Help: "Feed refresh attempts grouped by outcome.",
		},
		// outcome: ok | not_modified | error
		[]string{"outcome"},
	)

	feedRefreshDuration = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "biumind_rss_feed_refresh_duration_seconds",
			Help:    "Single-feed refresh latency (fetch + parse + insert).",
			Buckets: []float64{0.1, 0.25, 0.5, 1, 2, 5, 10, 30, 60},
		},
	)

	entriesInsertedTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "biumind_rss_entries_inserted_total",
			Help: "New entries inserted across all feeds. Pair with refresh_total to get insert ratio.",
		},
	)

	digestCallsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "biumind_rss_digest_calls_total",
			Help: "AI digest worker LLM calls grouped by outcome.",
		},
		// outcome: ok | error | empty (LLM 返空 content — C1 黄旗)
		[]string{"outcome"},
	)

	digestDuration = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "biumind_rss_digest_duration_seconds",
			Help:    "AI digest LLM call latency.",
			Buckets: []float64{0.5, 1, 2, 5, 10, 20, 30, 60, 120},
		},
	)

	radarHitsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "biumind_rss_radar_hits_total",
			Help: "Radar rule matches by match mode.",
		},
		// mode: keyword | semantic_token | semantic_cosine (M8 后)
		[]string{"mode"},
	)

	wikiSinksTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "biumind_rss_wiki_sinks_total",
			Help: "Entries sunk to Wiki, by outcome.",
		},
		[]string{"outcome"}, // ok | error
	)

	marksTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "biumind_rss_marks_total",
			Help: "User mark events on entries (engagement signal).",
		},
		[]string{"kind"}, // star | wiki | pin | shared
	)

	actionsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "biumind_rss_actions_total",
			Help: "RSS app action invocations by name and outcome. Cardinality bounded by ~30 known actions.",
		},
		[]string{"action", "outcome"}, // outcome: ok | error
	)

	activeUsers = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "biumind_rss_active_users",
			Help: "Distinct users with at least 1 enabled feed (window in days). Written by app_center main poller.",
		},
		[]string{"window"}, // 1d | 7d | 30d
	)

	feedsTotal = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "biumind_rss_feeds_total",
			Help: "Total enabled feeds across all users (last poll).",
		},
	)

	briefingTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "biumind_rss_briefing_total",
			Help: "AI 简报合成请求, 按 outcome 分.",
		},
		[]string{"outcome"}, // ok | cached | error
	)

	briefingDuration = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "biumind_rss_briefing_duration_seconds",
			Help:    "TTS 合成端到端延迟 (cached 不计).",
			Buckets: []float64{0.5, 1, 2, 5, 10, 20, 30, 60, 120},
		},
	)
)

func init() {
	prometheus.MustRegister(
		feedRefreshTotal,
		feedRefreshDuration,
		entriesInsertedTotal,
		digestCallsTotal,
		digestDuration,
		radarHitsTotal,
		wikiSinksTotal,
		marksTotal,
		actionsTotal,
		activeUsers,
		feedsTotal,
		briefingTotal,
		briefingDuration,
	)
}

// Public recorders — keep call sites trivial. Outcome / kind values
// must come from the small enums above to avoid cardinality blowups.

func RecordFeedRefresh(outcome string, durationSeconds float64, newEntries int) {
	feedRefreshTotal.WithLabelValues(outcome).Inc()
	if durationSeconds > 0 {
		feedRefreshDuration.Observe(durationSeconds)
	}
	if newEntries > 0 {
		entriesInsertedTotal.Add(float64(newEntries))
	}
}

func RecordDigestCall(outcome string, durationSeconds float64) {
	digestCallsTotal.WithLabelValues(outcome).Inc()
	if durationSeconds > 0 {
		digestDuration.Observe(durationSeconds)
	}
}

func RecordRadarHit(mode string) {
	radarHitsTotal.WithLabelValues(mode).Inc()
}

func RecordWikiSink(outcome string) {
	wikiSinksTotal.WithLabelValues(outcome).Inc()
}

func RecordMark(kind string) {
	marksTotal.WithLabelValues(kind).Inc()
}

func RecordAction(action, outcome string) {
	actionsTotal.WithLabelValues(action, outcome).Inc()
}

func SetActiveUsers(window string, n int64) {
	activeUsers.WithLabelValues(window).Set(float64(n))
}

func SetFeedsTotal(n int64) {
	feedsTotal.Set(float64(n))
}

// RecordBriefing — outcome ∈ ok | cached | error.
// durationSeconds 仅 outcome=ok 时有意义 (cached 走 0).
func RecordBriefing(outcome string, durationSeconds float64) {
	briefingTotal.WithLabelValues(outcome).Inc()
	if outcome == "ok" && durationSeconds > 0 {
		briefingDuration.Observe(durationSeconds)
	}
}

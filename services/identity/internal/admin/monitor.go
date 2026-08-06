// monitor — 服务健康聚合.
//
// 设计:
//   - 启动一个 goroutine 每 10s 并发调各服务 /healthz, 结果存内存
//   - HTTP handler 直接返 cache (不让前端等探测)
//   - 容器详情走 cadvisor (Prometheus metrics 派生), 不再挂 docker.sock
//
// 不做的事:
//   - 不持久化历史 (Prometheus 已经有时序了, 不重复)
//   - 不做告警 (alertmanager 单独配)
//   - 不暴露 /metrics (那是各服务自己的事, M-2 实现)

package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
)

// ServiceProbe — 各服务的健康状态.
type ServiceProbe struct {
	Name        string     `json:"name"`
	URL         string     `json:"url"`
	Status      string     `json:"status"` // healthy | degraded | unhealthy | unknown
	Version     string     `json:"version,omitempty"`
	HTTPStatus  int        `json:"http_status,omitempty"`
	LatencyMS   int        `json:"latency_ms,omitempty"`
	LastCheckAt time.Time  `json:"last_check_at"`
	Error       string     `json:"error,omitempty"`
	SubProbes   []SubProbe `json:"probes,omitempty"`
}

// SubProbe — 服务自报的子探针 (e.g. postgres / nats).
type SubProbe struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

// healthzReply — 各服务 /healthz 标准格式 (跟 packages/go-sdk/biu/healthz 对齐).
type healthzReply struct {
	Service string                  `json:"service"`
	Version string                  `json:"version"`
	Status  string                  `json:"status"`
	Probes  map[string]healthzProbe `json:"probes,omitempty"`
}

type healthzProbe struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// Monitor 健康探测器. 启动后后台轮询, handler 读 cache.
type Monitor struct {
	targets       []serviceTarget
	interval      time.Duration
	timeout       time.Duration
	prometheusURL string // 比如 http://prometheus:9090, 空则代理禁用
	logger        *slog.Logger

	mu     sync.RWMutex
	probes map[string]*ServiceProbe // 按 name 索引
}

type serviceTarget struct {
	Name string
	URL  string // e.g. http://model-relay:7001
}

// NewMonitor 默认 6 个 Go 服务 + identity 自身. URL 用 docker biu-net hostname.
//
// prometheusURL 传 "" 不启用 Prom 代理; 传 "http://prometheus:9090" 启用.
func NewMonitor(prometheusURL string, logger *slog.Logger) *Monitor {
	if logger == nil {
		logger = slog.Default()
	}
	m := &Monitor{
		targets: []serviceTarget{
			{Name: "identity", URL: "http://identity:7004"},
			{Name: "model-relay", URL: "http://model-relay:7001"},
			{Name: "runtime", URL: "http://runtime:7002"},
			{Name: "brain", URL: "http://brain:7003"},
			{Name: "realtime", URL: "http://realtime:7008"},
			{Name: "authz", URL: "http://authz:7009"},
		},
		interval: 10 * time.Second,
		timeout:  3 * time.Second,
		logger:   logger,
		probes:   map[string]*ServiceProbe{},
	}
	m.prometheusURL = prometheusURL
	// 初始化 probes 为 unknown 状态, 让 first response 不返空
	now := time.Now().UTC()
	for _, t := range m.targets {
		m.probes[t.Name] = &ServiceProbe{
			Name: t.Name, URL: t.URL,
			Status: "unknown", LastCheckAt: now,
		}
	}
	return m
}

// Start 后台 goroutine 周期探测. ctx Done 时退出.
func (m *Monitor) Start(ctx context.Context) {
	go func() {
		// 启动后立即探一次, 之后定时
		m.tick(ctx)
		ticker := time.NewTicker(m.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.tick(ctx)
			}
		}
	}()
}

// tick 一轮并发探测.
func (m *Monitor) tick(ctx context.Context) {
	var wg sync.WaitGroup
	for _, t := range m.targets {
		wg.Add(1)
		go func(t serviceTarget) {
			defer wg.Done()
			m.probe(ctx, t)
		}(t)
	}
	wg.Wait()
}

// probe 单服务探测 — GET /healthz, 解析 body 拿 version + sub probes.
func (m *Monitor) probe(parent context.Context, t serviceTarget) {
	ctx, cancel := context.WithTimeout(parent, m.timeout)
	defer cancel()

	now := time.Now().UTC()
	probe := &ServiceProbe{
		Name: t.Name, URL: t.URL, LastCheckAt: now,
	}

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, t.URL+"/healthz", nil)
	start := time.Now()
	resp, err := http.DefaultClient.Do(req)
	probe.LatencyMS = int(time.Since(start).Milliseconds())

	if err != nil {
		probe.Status = "unhealthy"
		probe.Error = err.Error()
		m.set(t.Name, probe)
		return
	}
	defer resp.Body.Close()
	probe.HTTPStatus = resp.StatusCode

	var body healthzReply
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		// 200 但 body 解析失败 — 当 healthy 但版本未知
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			probe.Status = "healthy"
		} else {
			probe.Status = "unhealthy"
			probe.Error = fmt.Sprintf("non-2xx %d", resp.StatusCode)
		}
		m.set(t.Name, probe)
		return
	}

	probe.Version = body.Version
	if resp.StatusCode != http.StatusOK {
		probe.Status = "unhealthy"
		probe.Error = fmt.Sprintf("http %d", resp.StatusCode)
	} else if body.Status == "ok" {
		probe.Status = "healthy"
	} else {
		probe.Status = "degraded"
	}
	for name, p := range body.Probes {
		probe.SubProbes = append(probe.SubProbes, SubProbe{Name: name, Status: p.Status})
	}
	m.set(t.Name, probe)
}

func (m *Monitor) set(name string, p *ServiceProbe) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.probes[name] = p
}

// Snapshot 返回当前所有 probes 的副本, 按 name 字典序.
func (m *Monitor) Snapshot() []ServiceProbe {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]ServiceProbe, 0, len(m.probes))
	for _, p := range m.probes {
		out = append(out, *p)
	}
	// 简单排序
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1].Name > out[j].Name; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

// ─── HTTP handlers ───────────────────────────────────────

// requireMonitorRead — admin/superadmin/ops/viewer 都能看监控.
func (s *Server) requireMonitorRead(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			writeErr(w, http.StatusUnauthorized, "missing_bearer", "")
			return
		}
		claims, err := s.Verifier.Verify(strings.TrimPrefix(auth, "Bearer "))
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "invalid_token", err.Error())
			return
		}
		if !hasAnyRole(claims, "admin", "superadmin", "ops", "viewer") {
			writeErr(w, http.StatusForbidden, "forbidden", "monitor:read required")
			return
		}
		ctx := bauth.WithClaims(r.Context(), claims)
		next(w, r.WithContext(ctx))
	}
}

func (s *Server) handleMonitorServices(w http.ResponseWriter, _ *http.Request) {
	if s.Monitor == nil {
		writeJSON(w, http.StatusOK, map[string]any{"services": []any{}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"services": s.Monitor.Snapshot(),
	})
}

// handleMonitorQuery 代理 Prometheus query / query_range API. 让前端
// 不暴露 prom 在公网, 也避免 CORS. 仅支持白名单 endpoint.
//
//	GET /v1/admin/monitor/query?type=instant&query=up
//	GET /v1/admin/monitor/query?type=range&query=...&start=...&end=...&step=15s
func (s *Server) handleMonitorQuery(w http.ResponseWriter, r *http.Request) {
	if s.Monitor == nil || s.Monitor.prometheusURL == "" {
		writeErr(w, http.StatusServiceUnavailable, "prometheus_disabled",
			"observability stack not running")
		return
	}
	q := r.URL.Query().Get("query")
	if q == "" {
		writeErr(w, http.StatusBadRequest, "missing_query", "")
		return
	}
	queryType := r.URL.Query().Get("type")
	var path string
	switch queryType {
	case "range":
		path = "/api/v1/query_range"
	case "instant", "":
		path = "/api/v1/query"
	default:
		writeErr(w, http.StatusBadRequest, "invalid_type", "type must be instant or range")
		return
	}
	upstreamURL := s.Monitor.prometheusURL + path + "?" + r.URL.RawQuery

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, upstreamURL, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "prometheus_error", err.Error())
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
	w.WriteHeader(resp.StatusCode)
	// 直接 stream upstream body
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			_, _ = w.Write(buf[:n])
		}
		if err != nil {
			break
		}
	}
}

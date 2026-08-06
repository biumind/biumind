// Package usage 读取外部编码 agent(Claude Code / Codex)的订阅用量快照。
//
// 关键事实(与 BiuMind-Code-Design.md §M5 + CLAUDE.md I6 的关系):
//
//   - Claude 用量读的是 **用户个人 Claude 订阅额度**(5h/7d 滚动窗),数据源是
//     Claude Code 自己写进 keychain 的 OAuth token + Anthropic 的 OAuth usage
//     端点。这 **不是推理调用**,且 model-relay 对用户自己 `claude` CLI 的订阅
//     配额一无所知 —— 故这是拿到该数据的唯一可行路径,不绕 model-relay 也不违反
//     I6(I6 约束的是 LLM 推理 SDK,不含账户计量端点)。
//   - Codex 用量走 `codex app-server` 的 JSON-RPC(account/read + rateLimits)。
//
// 按需计算、无缓存。每次 Snapshot 各带超时,
// Claude 命中 429 后进入 5 分钟冷却。Claude 仅 macOS keychain + ~/.claude 文件兜底;
// Codex 依赖本机装了 codex 二进制(未装 → unavailable,与 #21 同一阻塞)。
package usage

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	claudeUsageURL     = "https://api.anthropic.com/api/oauth/usage"
	claudeBetaHeader   = "oauth-2025-04-20"
	claudeKeychainName = "Claude Code-credentials"

	claudeTimeout      = 12 * time.Second
	codexTimeout       = 10 * time.Second
	claude429Backoff   = 5 * time.Minute
	defaultClaudeAgent = "claude-code/1.0.0" // User-Agent 兜底(版本探测失败时)
)

// Window 是一个用量窗口的归一化视图(0–100 整数百分比 + 可选重置时刻 Unix 秒)。
type Window struct {
	UsedPercent      int    `json:"usedPercent"`
	RemainingPercent int    `json:"remainingPercent"`
	ResetAt          *int64 `json:"resetAt"`
}

// ClaudeData 是 Claude 订阅的两档窗口。
type ClaudeData struct {
	FiveHour *Window `json:"fiveHour"`
	SevenDay *Window `json:"sevenDay"`
}

// CodexData 是 Codex 账户信息 + 两档限流窗口。
type CodexData struct {
	Email     *string `json:"email"`
	PlanType  *string `json:"planType"`
	Primary   *Window `json:"primary"`
	Secondary *Window `json:"secondary"`
}

// Source 包一个数据源的可用/不可用态。序列化为 {status:"available",data:...} 或
// {status:"unavailable",reason:"..."}。
type Source struct {
	ok     bool
	data   any
	reason string
}

func available(data any) Source { return Source{ok: true, data: data} }
func unavailable(reason string) Source {
	return Source{reason: reason}
}

// MarshalJSON 让 Source 落到稳定 JSON 形状,供 Dart 端直接映射。
func (s Source) MarshalJSON() ([]byte, error) {
	if s.ok {
		return json.Marshal(struct {
			Status string `json:"status"`
			Data   any    `json:"data"`
		}{Status: "available", Data: s.data})
	}
	return json.Marshal(struct {
		Status string `json:"status"`
		Reason string `json:"reason"`
	}{Status: "unavailable", Reason: s.reason})
}

// Snapshot 是一次用量读取的完整结果。
type Snapshot struct {
	Claude    Source `json:"claude"`
	Codex     Source `json:"codex"`
	FetchedAt int64  `json:"fetchedAt"`
}

// nowFn 抽出 time.Now 便于测试覆盖(429 冷却判定)。
var nowFn = time.Now

var (
	claude429Mu    sync.Mutex
	claude429Until time.Time // 零值 = 无冷却
)

// httpClient 复用连接;超时由 per-request context 控制。
var httpClient = &http.Client{}

// 测试桩缝:默认指向真实端点 / 真实取 token,单测可替换以避开网络与 keychain。
var (
	claudeUsageURLVar = claudeUsageURL
	claudeTokenFn     = claudeAccessToken
)

// Read 并发拉取 Claude + Codex 用量,合并为一个快照。任一源失败不影响另一源 ——
// 失败侧落 unavailable(reason)。
func Read(ctx context.Context) Snapshot {
	var (
		wg          sync.WaitGroup
		claudeSrc   Source
		codexSrc    Source
		claudeReady bool
	)
	_ = claudeReady

	wg.Add(2)
	go func() {
		defer wg.Done()
		cctx, cancel := context.WithTimeout(ctx, claudeTimeout)
		defer cancel()
		claudeSrc = readClaude(cctx)
	}()
	go func() {
		defer wg.Done()
		cctx, cancel := context.WithTimeout(ctx, codexTimeout)
		defer cancel()
		codexSrc = readCodex(cctx)
	}()
	wg.Wait()

	return Snapshot{
		Claude:    claudeSrc,
		Codex:     codexSrc,
		FetchedAt: nowFn().Unix(),
	}
}

// ─── Claude ──────────────────────────────────────────────────────────────

func readClaude(ctx context.Context) Source {
	// 429 冷却:上次限流后 5 分钟内直接跳过。
	claude429Mu.Lock()
	if !claude429Until.IsZero() {
		if remaining := claude429Until.Sub(nowFn()); remaining > 0 {
			claude429Mu.Unlock()
			return unavailable(fmt.Sprintf("Claude 用量被限流;%d 秒后重试。", int(remaining.Seconds())))
		}
	}
	claude429Mu.Unlock()

	token, err := claudeTokenFn(ctx)
	if err != nil {
		return unavailable(err.Error())
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, claudeUsageURLVar, nil)
	if err != nil {
		return unavailable("构造 Claude 用量请求失败:" + err.Error())
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("anthropic-beta", claudeBetaHeader)
	req.Header.Set("User-Agent", claudeUserAgent())
	req.Header.Set("Accept", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return unavailable("Claude 用量请求失败:" + err.Error())
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusTooManyRequests {
			claude429Mu.Lock()
			claude429Until = nowFn().Add(claude429Backoff)
			claude429Mu.Unlock()
			return unavailable("Claude 用量被限流(429);5 分钟后重试。")
		}
		return unavailable(fmt.Sprintf("Claude 用量 HTTP %d", resp.StatusCode))
	}

	var payload map[string]json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return unavailable("Claude 用量响应不是合法 JSON:" + err.Error())
	}

	data := ClaudeData{
		FiveHour: parseClaudeWindow(payload["five_hour"]),
		SevenDay: parseClaudeWindow(payload["seven_day"]),
	}
	if data.FiveHour == nil && data.SevenDay == nil {
		return unavailable("Claude 用量响应未包含可识别的窗口。")
	}
	return available(data)
}

// claudeAccessToken 取 Claude Code 写入的 OAuth accessToken:
//   - macOS:keychain 项 "Claude Code-credentials"(security find-generic-password,
//     按 service 匹配、不带 account —— Claude Code 不固定 account)
//   - 兜底(任意 OS):~/.claude/.credentials.json 文件(Linux 默认存储)
//
// 额外提供文件兜底(不止 macOS keychain):纯文件读、可测、零风险,顺带覆盖 Linux。
func claudeAccessToken(ctx context.Context) (string, error) {
	if runtime.GOOS == "darwin" {
		if tok, err := claudeKeychainToken(ctx); err == nil && tok != "" {
			return tok, nil
		}
	}
	if tok, err := claudeFileToken(); err == nil && tok != "" {
		return tok, nil
	}
	if runtime.GOOS == "windows" {
		return "", fmt.Errorf("Claude 用量当前依赖 macOS keychain 或 ~/.claude 凭据文件,本平台不可用。")
	}
	return "", fmt.Errorf("未找到 Claude 凭据(keychain 与 ~/.claude/.credentials.json 均不可用)。")
}

func claudeKeychainToken(ctx context.Context) (string, error) {
	cmd := exec.CommandContext(ctx, "security", "find-generic-password",
		"-s", claudeKeychainName, "-w")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("读取 Claude keychain 凭据失败:%w", err)
	}
	return extractClaudeToken(out)
}

func claudeFileToken() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	raw, err := os.ReadFile(filepath.Join(home, ".claude", ".credentials.json"))
	if err != nil {
		return "", err
	}
	return extractClaudeToken(raw)
}

// extractClaudeToken 从 {"claudeAiOauth":{"accessToken":"..."}} 取 token。
func extractClaudeToken(raw []byte) (string, error) {
	var parsed struct {
		ClaudeAiOauth struct {
			AccessToken string `json:"accessToken"`
		} `json:"claudeAiOauth"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(raw))), &parsed); err != nil {
		return "", fmt.Errorf("Claude 凭据 JSON 非法:%w", err)
	}
	tok := strings.TrimSpace(parsed.ClaudeAiOauth.AccessToken)
	if tok == "" {
		return "", fmt.Errorf("Claude 凭据里缺 accessToken。")
	}
	return tok, nil
}

// claudeUserAgent 形如 "claude-code/<version>";版本探测失败用兜底。
func claudeUserAgent() string {
	path, err := exec.LookPath("claude")
	if err != nil {
		return defaultClaudeAgent
	}
	out, err := exec.Command(path, "--version").Output()
	if err != nil {
		return defaultClaudeAgent
	}
	// `claude --version` 形如 "2.1.90 (Claude Code)" → 取前导版本号。
	fields := strings.Fields(strings.TrimSpace(string(out)))
	if len(fields) == 0 || fields[0] == "" {
		return defaultClaudeAgent
	}
	return "claude-code/" + fields[0]
}

// ─── 解析 helpers ────────────────────────────────────────────────────────

func parseClaudeWindow(raw json.RawMessage) *Window {
	if len(raw) == 0 {
		return nil
	}
	var node map[string]json.RawMessage
	if err := json.Unmarshal(raw, &node); err != nil {
		return nil
	}
	used, ok := parsePercentValue(node["utilization"])
	if !ok {
		return nil
	}
	return &Window{
		UsedPercent:      used,
		RemainingPercent: clampPercent(100 - used),
		ResetAt:          parseResetValue(node["resets_at"]),
	}
}

func parseCodexWindow(raw json.RawMessage) *Window {
	if len(raw) == 0 {
		return nil
	}
	var node map[string]json.RawMessage
	if err := json.Unmarshal(raw, &node); err != nil {
		return nil
	}
	// Codex 的 usedPercent 已是 0–100 整数,不是 0.0–1.0 小数。
	f, ok := parseFloatValue(node["usedPercent"])
	if !ok {
		return nil
	}
	used := clampPercent(int(roundHalfUp(clampFloat(f, 0, 100))))
	return &Window{
		UsedPercent:      used,
		RemainingPercent: clampPercent(100 - used),
		ResetAt:          parseResetValue(node["resetsAt"]),
	}
}

// parsePercentValue 归一化:≤1 视作 0.0–1.0 小数 ×100,否则视作已是百分比。
func parsePercentValue(raw json.RawMessage) (int, bool) {
	f, ok := parseFloatValue(raw)
	if !ok {
		return 0, false
	}
	if f <= 1.0 {
		f *= 100.0
	}
	return clampPercent(int(roundHalfUp(clampFloat(f, 0, 100)))), true
}

// parseFloatValue 接受 JSON number 或可解析为 float 的 string。
func parseFloatValue(raw json.RawMessage) (float64, bool) {
	if isAbsent(raw) {
		return 0, false
	}
	var num float64
	if err := json.Unmarshal(raw, &num); err == nil {
		return num, true
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if f, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err == nil {
			return f, true
		}
	}
	return 0, false
}

// parseResetValue 接受 Unix 秒(number / 数字串)或 RFC3339 时间串。
func parseResetValue(raw json.RawMessage) *int64 {
	if isAbsent(raw) {
		return nil
	}
	var num int64
	if err := json.Unmarshal(raw, &num); err == nil {
		return &num
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		s = strings.TrimSpace(s)
		if ts, err := strconv.ParseInt(s, 10, 64); err == nil {
			return &ts
		}
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			ts := t.Unix()
			return &ts
		}
	}
	return nil
}

// isAbsent 把缺字段与 JSON null 一视同仁(都当没有)。
func isAbsent(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return true
	}
	return string(bytesTrimSpace(raw)) == "null"
}

func bytesTrimSpace(b []byte) []byte {
	return []byte(strings.TrimSpace(string(b)))
}

func clampFloat(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func clampPercent(v int) int {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

// roundHalfUp 四舍五入(与 Rust f64::round 一致)。
func roundHalfUp(v float64) float64 {
	if v < 0 {
		return -roundHalfUp(-v)
	}
	return float64(int64(v + 0.5))
}

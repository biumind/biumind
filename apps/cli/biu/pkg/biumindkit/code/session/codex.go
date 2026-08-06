// codex.go 是编码模块对 Codex CLI 的会话发现 + JSONL 解析。
//
// 与 Claude 的差异(看代码看不出、易踩雷):
//   - Codex 不支持 --session-id(agent.SupportsSessionID 恒 false),故无法预算路径,
//     只能用「落盘发现」:任务启动前快照已有 rollout-*.jsonl,启动后轮询出现的新文件
//     即本任务会话(用 mtime-newest-new-file 这个简单、对单活动任务足够鲁棒的判定)。
//   - Codex 的 token_count 给的是**累计**用量(total_token_usage),不像 Claude 是每条
//     assistant 的单条 usage —— 故这里直接透传累计值(CostUpdate replace 语义),
//     不做 WatchClaude 那样的跨行累加。
//   - 工具确认判定:Codex 的 on-request 审批反映在 rollout 里(function_call 需确认 →
//     pending,function_call_output → 解除;request_user_input / 助手提问 / 用户中止 →
//     awaiting)。等待态上升沿 emit 一条 permission_ask 事件,客户端据此把任务标
//     inputRequired(进"需要注意"分组)。回到 running 的可靠切换留待 PERI-1 hook —
//     纯轮询反推无法可靠判定"用户已回复"。
package session

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// CodexSessionRoots 返回 Codex rollout JSONL 可能落盘的根目录:
// 项目内 <cwd>/.codex/sessions 优先,再加 ~/.codex/sessions。
func CodexSessionRoots(cwd string) []string {
	roots := []string{filepath.Join(cwd, ".codex", "sessions")}
	if home, err := os.UserHomeDir(); err == nil {
		hr := filepath.Join(home, ".codex", "sessions")
		if hr != roots[0] {
			roots = append(roots, hr)
		}
	}
	return roots
}

// collectRollouts 递归收集 roots 下的 rollout-*.jsonl(Codex 会话文件命名约定)。
// 目录不存在/不可读 → 跳过该子树,不中断(任务首跑时 .codex/sessions 可能还没建)。
func collectRollouts(roots []string) []string {
	var out []string
	for _, root := range roots {
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				return nil
			}
			name := d.Name()
			if strings.HasPrefix(name, "rollout-") && strings.HasSuffix(name, ".jsonl") {
				out = append(out, p)
			}
			return nil
		})
	}
	return out
}

// discoverCodexRollout 发现「本次任务新建」的 rollout 文件:startSet 是启动前的快照,
// 出现不在快照里的新文件即认定为本任务会话(取 mtime 最新者,容并发轻微竞态)。
// ctx 取消时做最后一次扫描兜底(进程在会话落盘后秒退也能抓到),仍无则返回 ""。
func discoverCodexRollout(ctx context.Context, roots []string, startSet map[string]bool) string {
	scan := func() string {
		var newest string
		var newestMod time.Time
		for _, p := range collectRollouts(roots) {
			if startSet[p] {
				continue
			}
			info, err := os.Stat(p)
			if err != nil {
				continue
			}
			if newest == "" || info.ModTime().After(newestMod) {
				newest, newestMod = p, info.ModTime()
			}
		}
		return newest
	}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		if p := scan(); p != "" {
			return p
		}
		select {
		case <-ctx.Done():
			return scan() // 兜底:秒退任务的会话可能刚落盘
		case <-ticker.C:
		}
	}
}

// WatchCodex 发现并 tail 本任务的 Codex rollout JSONL,解析成结构化事件 emit,直到 ctx
// 取消。ctx 取消时收尾 drain 一次(捕获进程退出前写入但还没轮询到的行)。
func WatchCodex(ctx context.Context, cwd string, emit EmitFn) {
	roots := CodexSessionRoots(cwd)
	startSet := map[string]bool{}
	for _, p := range collectRollouts(roots) {
		startSet[p] = true
	}
	sessionFile := discoverCodexRollout(ctx, roots, startSet)
	if sessionFile == "" {
		return // 任务在会话落盘前就退出,无可 tail
	}
	WatchCodexFile(ctx, sessionFile, cwd, emit)
}

// WatchCodexFile tail 一个已知路径的 Codex rollout JSONL(hook 驱动时由 SessionStart
// 的 transcript_path 直接给出,跳过落盘发现),解析成结构化事件 emit。cwd 供 apply_patch
// 越界确认判定。ctx 取消时收尾 drain 一次。
func WatchCodexFile(ctx context.Context, sessionFile, cwd string, emit EmitFn) {
	tr := &tailReader{path: sessionFile}
	conf := newCodexConfirm(cwd)
	drain := func() {
		lines, err := tr.read()
		if err != nil || len(lines) == 0 {
			return
		}
		var events []map[string]any
		for _, ln := range lines {
			events = append(events, parseCodexLine(ln, conf)...)
		}
		if len(events) > 0 {
			emit(events)
		}
	}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			drain()
			return
		case <-ticker.C:
			drain()
		}
	}
}

// ─── JSONL 解析 ───────────────────────────────────────────────────────────────

// codexConfirm 跨行累积工具确认状态(pending/awaiting/waiting 的边沿判定)。
type codexConfirm struct {
	cwd      string
	pending  map[string]bool // 待用户确认的 call_id
	awaiting bool            // 等用户回复(request_user_input / 助手提问 / 中止)
	waiting  bool            // 上次计算的等待态(用于算上升沿)
}

func newCodexConfirm(cwd string) *codexConfirm {
	return &codexConfirm{cwd: cwd, pending: map[string]bool{}}
}

// syncWaiting 重算等待态;false→true 上升沿时追加一条 permission_ask(置 inputRequired)。
func (c *codexConfirm) syncWaiting(ts, toolID, name string, args any, out *[]map[string]any) {
	next := c.awaiting || len(c.pending) > 0
	if next && !c.waiting {
		*out = append(*out, evPermissionAsk(ts, toolID, name, args))
	}
	c.waiting = next
}

// parseCodexLine 解析一行 Codex rollout JSONL,返回与 Dart AgentEvent.fromJson 同形的
// 事件 map(可能多个),并更新 conf 的确认状态。无关行返回 nil。
func parseCodexLine(raw []byte, conf *codexConfirm) []map[string]any {
	var head struct {
		Type      string          `json:"type"`
		Timestamp json.RawMessage `json:"timestamp"`
		Payload   json.RawMessage `json:"payload"`
	}
	if json.Unmarshal(raw, &head) != nil {
		return nil
	}
	ts := coerceTS(head.Timestamp)
	switch head.Type {
	case "event_msg":
		return parseCodexEventMsg(head.Payload, ts, conf)
	case "response_item":
		return parseCodexResponseItem(head.Payload, ts, conf)
	default:
		return nil
	}
}

func parseCodexEventMsg(payload json.RawMessage, ts string, conf *codexConfirm) []map[string]any {
	var p struct {
		Type string          `json:"type"`
		Info json.RawMessage `json:"info"`
	}
	if json.Unmarshal(payload, &p) != nil {
		return nil
	}
	var out []map[string]any
	switch p.Type {
	case "user_message":
		// 用户文本不进结构化流(与 Claude 侧一致:终端已即时显示);仅复位等待态。
		conf.awaiting = false
		conf.syncWaiting(ts, "", "", nil, &out)
	case "token_count":
		if ev := codexCost(p.Info, ts); ev != nil {
			out = append(out, ev)
		}
	}
	return out
}

func parseCodexResponseItem(payload json.RawMessage, ts string, conf *codexConfirm) []map[string]any {
	var p struct {
		Type      string            `json:"type"`
		Role      string            `json:"role"`
		Name      string            `json:"name"`
		CallID    string            `json:"call_id"`
		Status    string            `json:"status"`
		Arguments string            `json:"arguments"`
		Output    string            `json:"output"`
		Phase     string            `json:"phase"`
		Content   []json.RawMessage `json:"content"`
	}
	if json.Unmarshal(payload, &p) != nil {
		return nil
	}
	var out []map[string]any
	switch p.Type {
	case "message":
		switch p.Role {
		case "assistant":
			for _, b := range p.Content {
				var blk struct {
					Type string `json:"type"`
					Text string `json:"text"`
				}
				if json.Unmarshal(b, &blk) != nil {
					continue
				}
				if (blk.Type == "output_text" || blk.Type == "text") && strings.TrimSpace(blk.Text) != "" {
					out = append(out, evText(ts, blk.Text))
				}
			}
			if assistantAsksUser(p.Phase, p.Content) {
				conf.awaiting = true
			}
		case "user":
			conf.awaiting = false
		}
		conf.syncWaiting(ts, "", "", nil, &out)
	case "function_call":
		argsRaw := orDefault(p.Arguments, "{}")
		out = append(out, evToolStart(ts, p.CallID, p.Name, json.RawMessage(argsRaw)))
		if p.Name == "request_user_input" {
			conf.awaiting = true
		} else if toolCallRequiresConfirmation(p.Name, p.Arguments, conf.cwd) {
			if p.CallID != "" {
				conf.pending[p.CallID] = true
			} else {
				conf.awaiting = true
			}
		}
		var args any
		_ = json.Unmarshal([]byte(argsRaw), &args)
		conf.syncWaiting(ts, p.CallID, p.Name, args, &out)
	case "function_call_output":
		if p.CallID != "" {
			delete(conf.pending, p.CallID)
		}
		if strings.HasPrefix(p.Output, "aborted by user after") {
			conf.awaiting = true
		}
		out = append(out, evToolResult(ts, p.CallID, p.Output, false))
		conf.syncWaiting(ts, "", "", nil, &out)
	case "custom_tool_call":
		if p.Name != "" {
			out = append(out, evToolStart(ts, p.CallID, p.Name, json.RawMessage(orDefault(p.Arguments, "{}"))))
		}
		if (p.Status == "completed" || p.Status == "failed") && p.CallID != "" {
			delete(conf.pending, p.CallID)
		}
		conf.syncWaiting(ts, "", "", nil, &out)
	}
	return out
}

// codexCost 从 token_count 的 info.total_token_usage 提累计 token。Codex 给的是累计值,
// 直接透传(CostUpdate replace 语义)。input/output 都缺则退回 total_tokens 记为 output。
func codexCost(info json.RawMessage, ts string) map[string]any {
	if len(info) == 0 {
		return nil
	}
	var i struct {
		Total struct {
			Input  int `json:"input_tokens"`
			Output int `json:"output_tokens"`
			Total  int `json:"total_tokens"`
		} `json:"total_token_usage"`
		Last struct {
			Total int `json:"total_tokens"`
		} `json:"last_token_usage"`
		ContextWindow int `json:"model_context_window"`
	}
	if json.Unmarshal(info, &i) != nil {
		return nil
	}
	in, out := i.Total.Input, i.Total.Output
	if in == 0 && out == 0 {
		if i.Total.Total == 0 {
			return nil
		}
		out = i.Total.Total
	}
	// context_tokens = 最近一轮喂给模型的上下文(last_token_usage),配合
	// model_context_window 让端侧算上下文利用率。
	return evCost(ts, costParts{
		in:        in,
		out:       out,
		ctxTokens: i.Last.Total,
		ctxWindow: i.ContextWindow,
	})
}

// assistantAsksUser 判定助手最终消息是否在向用户提问:
// phase ∈ final/final_answer 且文本以 ? / ? 结尾。
func assistantAsksUser(phase string, content []json.RawMessage) bool {
	if phase != "final" && phase != "final_answer" {
		return false
	}
	var sb strings.Builder
	for _, b := range content {
		var blk struct {
			Text string `json:"text"`
		}
		if json.Unmarshal(b, &blk) == nil {
			sb.WriteString(blk.Text)
		}
	}
	t := strings.TrimSpace(sb.String())
	return strings.HasSuffix(t, "?") || strings.HasSuffix(t, "？")
}

// ─── 工具确认判定 ──────────────

// toolCallRequiresConfirmation 判定某 Codex 工具调用是否需要用户确认。
// 仅 exec_command(非只读 shell)与 apply_patch(改项目外文件)需确认,其余放行。
func toolCallRequiresConfirmation(name, arguments, cwd string) bool {
	switch name {
	case "exec_command":
		return execCommandRequiresConfirmation(arguments)
	case "apply_patch":
		return applyPatchRequiresConfirmation(arguments, cwd)
	default:
		return false
	}
}

func execCommandRequiresConfirmation(arguments string) bool {
	var args struct {
		SandboxPermissions string `json:"sandbox_permissions"`
		Cmd                string `json:"cmd"`
	}
	if json.Unmarshal([]byte(arguments), &args) != nil {
		return false
	}
	if args.SandboxPermissions == "require_escalated" {
		return true
	}
	if args.Cmd == "" {
		return false
	}
	return !looksLikeReadOnlyCommand(args.Cmd)
}

func looksLikeReadOnlyCommand(cmd string) bool {
	trimmed := strings.TrimSpace(cmd)
	if trimmed == "" || containsShellRedirection(trimmed) {
		return false
	}
	for _, seg := range strings.FieldsFunc(trimmed, func(r rune) bool {
		return r == ';' || r == '|' || r == '&' || r == '\n'
	}) {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		if !isReadOnlySegment(seg) {
			return false
		}
	}
	return true
}

func containsShellRedirection(cmd string) bool {
	return strings.Contains(cmd, " >") ||
		strings.Contains(cmd, ">>") ||
		strings.Contains(cmd, "<<") ||
		strings.Contains(cmd, " 2>") ||
		strings.HasPrefix(cmd, ">") ||
		strings.Contains(cmd, "| tee")
}

// readOnlyCommands 是单 token 即可判定只读的命令。
var readOnlyCommands = map[string]bool{
	"pwd": true, "ls": true, "rg": true, "grep": true, "cat": true, "head": true,
	"tail": true, "wc": true, "stat": true, "which": true, "type": true, "uname": true,
	"date": true, "ps": true, "env": true, "printenv": true, "echo": true, "printf": true,
	"Get-Location": true, "Get-ChildItem": true, "Get-Content": true, "Select-String": true,
	"Get-Process": true, "Get-Date": true, "Get-Command": true, "Test-Path": true,
	"Resolve-Path": true, "Where-Object": true, "Measure-Object": true, "Sort-Object": true,
	"Select-Object": true,
}

var gitReadOnlySub = map[string]bool{
	"status": true, "diff": true, "show": true, "log": true, "branch": true,
	"rev-parse": true, "remote": true,
}

func isReadOnlySegment(segment string) bool {
	tokens := strings.Fields(segment)
	if len(tokens) == 0 {
		return true
	}
	command := tokens[0]
	if readOnlyCommands[command] {
		return true
	}
	switch command {
	case "sed":
		hasN, hasI := false, false
		for _, t := range tokens {
			if t == "-n" {
				hasN = true
			}
			if strings.HasPrefix(t, "-i") {
				hasI = true
			}
		}
		return hasN && !hasI
	case "find":
		for _, t := range tokens {
			if t == "-delete" || t == "-exec" || t == "-ok" {
				return false
			}
		}
		return true
	case "git", "git.exe":
		if len(tokens) < 2 {
			return false
		}
		return gitReadOnlySub[tokens[1]]
	default:
		return false
	}
}

func applyPatchRequiresConfirmation(arguments, cwd string) bool {
	for _, line := range strings.Split(arguments, "\n") {
		if p := extractPatchPath(line); p != "" {
			if patchTargetRequiresConfirmation(p, cwd) {
				return true
			}
		}
	}
	return false
}

func extractPatchPath(line string) string {
	for _, prefix := range []string{"*** Add File: ", "*** Update File: ", "*** Delete File: ", "*** Move to: "} {
		if rest, ok := strings.CutPrefix(line, prefix); ok {
			return strings.TrimSpace(rest)
		}
	}
	return ""
}

// patchTargetRequiresConfirmation:apply_patch 目标是绝对路径、且既不在项目内也不在临时目录
// → 改项目外文件,需确认(相对路径默认在项目内,放行)。
func patchTargetRequiresConfirmation(path, cwd string) bool {
	if !filepath.IsAbs(path) {
		return false
	}
	target := filepath.Clean(path)
	if within(target, cwd) || within(target, os.TempDir()) {
		return false
	}
	return true
}

func within(target, base string) bool {
	if base == "" {
		return false
	}
	base = filepath.Clean(base)
	return target == base || strings.HasPrefix(target, base+string(filepath.Separator))
}

// ─── 小工具 ─────────────────────────────────────────────────────────────────

// coerceTS 把 timestamp 字段(可能是字符串,也可能缺失/为数字)取成字符串;非字符串 → ""。
func coerceTS(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return ""
}

func orDefault(s, d string) string {
	if strings.TrimSpace(s) == "" {
		return d
	}
	return s
}

// evPermissionAsk 构造 permission_ask 事件(键名对齐 Dart PermissionAsk.fromJson)。
func evPermissionAsk(ts, id, name string, args any) map[string]any {
	if args == nil {
		args = map[string]any{}
	}
	return map[string]any{"type": "permission_ask", "ts": ts, "tool_id": id, "name": name, "args": args}
}

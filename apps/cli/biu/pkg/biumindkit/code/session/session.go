// Package session 是编码模块的会话发现 + JSONL 解析。外部 agent(Claude/Codex)把每一步思考/工具调用/结果
// 写进自己的 JSONL 会话文件;本包 tail 这个文件、解析成与 Dart AgentEvent 同形的
// 事件,让桌面在终端(原始字节)之外再有一份结构化视图(双视图)。
//
// 定位:Claude≥2.1.87 用 --session-id 预定会话(agent 包),JSONL 路径确定为
//
//	~/.claude/projects/<encoded-cwd>/<sessionID>.jsonl
//
// encoded-cwd = cwd 里每个非 [A-Za-z0-9-] 字符替换成 '-'。
//
// watcher 用纯轮询(~150ms),无 fsnotify 依赖:stat 文件大小,增长则从 offset 读
// 新字节、按行解析。150ms 对结构化视图足够实时
// (终端已即时显示原始输出,这层滞后无感)。
package session

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// pollInterval 是 watcher 轮询会话文件的节奏。
const pollInterval = 150 * time.Millisecond

// EmitFn 收到一批解析好的事件(每个是与 Dart AgentEvent.fromJson 同形的 JSON map)。
type EmitFn func(events []map[string]any)

// EncodeProjectDir 把 cwd 编码成 Claude 的 projects 子目录名:非 [A-Za-z0-9-] → '-'。
func EncodeProjectDir(cwd string) string {
	var b strings.Builder
	b.Grow(len(cwd))
	for _, c := range cwd {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-':
			b.WriteRune(c)
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}

// ClaudeSessionFile 返回 cwd + sessionID 对应的 Claude 会话 JSONL 绝对路径。
//
// 先解析符号链接再编码:claude 用 process.cwd()(= getcwd(),返回 realpath)推算
// projects 子目录名,故 daemon 必须对 cwd 做同样的 realpath 解析,否则符号链接路径
// (如 macOS /tmp→/private/tmp、或软链的项目目录)会算出错误目录、watcher 盯空文件。
// EvalSymlinks 失败(路径不存在等)→ 退回原 cwd,不阻断。
func ClaudeSessionFile(cwd, sessionID string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if resolved, rerr := filepath.EvalSymlinks(cwd); rerr == nil {
		cwd = resolved
	}
	return filepath.Join(home, ".claude", "projects", EncodeProjectDir(cwd), sessionID+".jsonl"), nil
}

// WatchClaude tail 一个 Claude 会话文件,解析新行并 emit,直到 ctx 取消。文件可能在
// 启动后才落盘,故持续轮询直到出现;ctx 取消时再做一次收尾读,避免漏掉最后几行。
func WatchClaude(ctx context.Context, sessionFile string, emit EmitFn) {
	tr := &tailReader{path: sessionFile}
	// 累计 token:parseClaudeLine 给的是每条 assistant 消息的 usage(单条),这里跨行
	// 累加成"累计总量"再 emit —— 客户端 CostUpdate 是 replace 语义(显示最后一帧),
	// 故必须发累计值,详情头 TOKENS 才是任务总量而非最后一轮(CORE-6)。
	// 只累加 input+output(不含 cache_read),避免被每轮上下文缓存重复读撑爆。
	var cumIn, cumOut int
	drain := func() {
		lines, err := tr.read()
		if err != nil || len(lines) == 0 {
			return
		}
		var events []map[string]any
		for _, ln := range lines {
			for _, ev := range parseClaudeLine(ln) {
				if ev["type"] == "cost_update" {
					if v, ok := ev["input_tokens"].(int); ok {
						cumIn += v
					}
					if v, ok := ev["output_tokens"].(int); ok {
						cumOut += v
					}
					ev["input_tokens"] = cumIn
					ev["output_tokens"] = cumOut
				}
				events = append(events, ev)
			}
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
			drain() // 收尾:flush 进程退出前写入但还没轮询到的行
			return
		case <-ticker.C:
			drain()
		}
	}
}

// hook_watch.go 监听 hook 脚本写入的 events.jsonl,把生命周期事件翻译成任务状态信号。
// biumind 是 per-task:runTask 为每个 hook 可用的任务起一个本 watcher,经其 emit 回调
// 把 agent_status / 内容事件按 task_id 推回该 WS 连接。
//
// 事件 → 状态映射:
//   - SessionStart                       → 回调 onSessionStart(精确 session_id+transcript_path),
//     由调用方据此启动内容 watcher(取代 Claude 路径猜测 /
//     Codex 落盘发现的竞态)
//   - Notification(Claude)/PermissionRequest(Codex)/Stop → input_required
//     · Notification/PermissionRequest:工具审批/网络升级请求
//     · Stop:交互式 REPL 一轮结束、进程不退出、停在等用户 —— 这是纯 JSONL 轮询测不到的信号
//   - UserPromptSubmit / PostToolUse      → running(复位 input_required;工具审批通过靠 PostToolUse)
//   - SubagentStop                        → 忽略(主 agent 仍在跑,终态交给 PTY exit monitor)
//
// 监听用 fsnotify 盯 events 目录 + 1s 兜底轮询(漏事件也最坏 1s 内重扫);ctx 取消时收尾 drain。
package session

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
)

// hookFallbackInterval 是 fsnotify 之外的兜底轮询间隔。
const hookFallbackInterval = time.Second

// HookStatusFn 收到一个任务状态字符串(running | input_required)。
type HookStatusFn func(status string)

// HookSessionFn 收到 SessionStart 解析出的精确 session_id + transcript_path。
type HookSessionFn func(sessionID, transcriptPath string)

// hookEvent 是 biu-hook.mjs 写入 events.jsonl 的一行(字段已由脚本归一化)。
type hookEvent struct {
	TaskID         string `json:"task_id"`
	Agent          string `json:"agent"`
	Event          string `json:"event"`
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
}

// WatchHookEvents tail eventsDir/events.jsonl,按事件回调 onStatus / onSessionStart,直到
// ctx 取消。onSessionStart 只在首个带 session_id 的 SessionStart 触发一次。onStatus 做去重
// (PostToolUse 高频,只在状态变化时回调)。
func WatchHookEvents(ctx context.Context, eventsDir string, onStatus HookStatusFn, onSessionStart HookSessionFn) {
	if err := os.MkdirAll(eventsDir, 0o755); err != nil {
		return
	}
	tr := &tailReader{path: filepath.Join(eventsDir, "events.jsonl")}
	lastStatus := ""
	sessionStarted := false

	dispatch := func(ev hookEvent) {
		if ev.TaskID == "" {
			return
		}
		switch ev.Event {
		case "SessionStart":
			if !sessionStarted && ev.SessionID != "" {
				sessionStarted = true
				if onSessionStart != nil {
					onSessionStart(ev.SessionID, ev.TranscriptPath)
				}
			}
		case "Notification", "PermissionRequest", "Stop":
			if lastStatus != "input_required" {
				lastStatus = "input_required"
				if onStatus != nil {
					onStatus("input_required")
				}
			}
		case "UserPromptSubmit", "PostToolUse":
			if lastStatus != "running" {
				lastStatus = "running"
				if onStatus != nil {
					onStatus("running")
				}
			}
		}
	}
	drain := func() {
		lines, err := tr.read()
		if err != nil {
			return
		}
		for _, ln := range lines {
			var ev hookEvent
			if json.Unmarshal(ln, &ev) == nil {
				dispatch(ev)
			}
		}
	}

	// fsnotify 盯目录(events.jsonl 可能尚未创建,盯父目录捕获 create+write);失败回退纯轮询。
	var w *fsnotify.Watcher
	if nw, werr := fsnotify.NewWatcher(); werr == nil {
		if aerr := nw.Add(eventsDir); aerr == nil {
			w = nw
			defer w.Close()
		} else {
			_ = nw.Close()
		}
	}

	ticker := time.NewTicker(hookFallbackInterval)
	defer ticker.Stop()
	for {
		drain()
		if w != nil {
			select {
			case <-ctx.Done():
				drain()
				return
			case <-w.Events:
				// 合并同批写入产生的多个事件,下一轮 drain 统一读
				for drained := false; !drained; {
					select {
					case <-w.Events:
					default:
						drained = true
					}
				}
			case <-w.Errors:
			case <-ticker.C:
			}
		} else {
			select {
			case <-ctx.Done():
				drain()
				return
			case <-ticker.C:
			}
		}
	}
}

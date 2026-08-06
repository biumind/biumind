package sdkproto

import (
	"encoding/json"
	"fmt"
)

// 编码模块（BiuMind Code）专属 WS 帧。
//
// 设计背景：编码客户端通常采用双机制 —— Git/FS 走请求/响应 RPC、agent PTY
// 输出走专用字节流通道。这里把两者统一到一条 /v1/code/ws：
//
//   - code_request / code_response：通用 RPC 信封（method 分发 git.status / fs.read /
//     pty.open / pty.kill / ...）。
//   - code_pty_chunk / code_pty_input / code_pty_resize / code_pty_exit：PTY 字节流
//     与控制（chunk 下行 / input+resize 上行 / exit 通知）。
//
// 与 chat 平面（user/assistant/result/...）和 control 平面（control_request/...）
// 并列为第三组 wire 帧 —— UnmarshalFrame 按 type 前缀 "code_" 分发到 UnmarshalCodeFrame。
//
// PTY 字节用 []byte 承载（encoding/json 自动 base64），**无损传输**，不做 UTF-8
// 边界处理 —— 若改用 String 承载则强制合法 UTF-8，需 leftover 暂存半个多字节
// 序列；这里用 []byte 是有意简化，UTF-8 重组留给消费端（M2 的 xterm.dart）。
const (
	TypeCodeRequest      = "code_request"
	TypeCodeResponse     = "code_response"
	TypeCodePtyChunk     = "code_pty_chunk"
	TypeCodePtyInput     = "code_pty_input"
	TypeCodePtyResize    = "code_pty_resize"
	TypeCodePtyExit      = "code_pty_exit"
	TypeCodeSessionEvent = "code_session_event"
)

// CodeFrame 是编码模块 7 个帧的标记接口。嵌入 Frame —— 它们也是合法 WS 帧。
type CodeFrame interface {
	Frame
	isCodeFrame()
	CodeFrameType() string
}

// CodeRequest 是客户端 → 服务端的通用 RPC 信封。Method 决定动作
// （"git.status" / "fs.read" / "fs.list" / "pty.open" / "pty.kill" / ...），
// Params 是该 method 的入参（具体结构由 biumindkit/code 各 handler 定义）。
// RequestID 由客户端生成，服务端在对应 CodeResponse 原样回填。
type CodeRequest struct {
	Type      string          `json:"type"` // "code_request"
	RequestID string          `json:"request_id"`
	Method    string          `json:"method"`
	Params    json.RawMessage `json:"params,omitempty"`
}

func (*CodeRequest) isFrame()              {}
func (*CodeRequest) isCodeFrame()          {}
func (*CodeRequest) CodeFrameType() string { return TypeCodeRequest }

// CodeResponse 是服务端 → 客户端对某个 CodeRequest 的应答。OK=false 时 Error
// 填人类可读错误文本；OK=true 时 Result 填 method 专属返回结构（已 JSON 序列化）。
type CodeResponse struct {
	Type      string          `json:"type"` // "code_response"
	RequestID string          `json:"request_id"`
	OK        bool            `json:"ok"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     string          `json:"error,omitempty"`
}

func (*CodeResponse) isFrame()              {}
func (*CodeResponse) isCodeFrame()          {}
func (*CodeResponse) CodeFrameType() string { return TypeCodeResponse }

// CodePtyChunk 是服务端 → 客户端的 PTY 原始输出字节。Data 经 encoding/json
// 自动 base64 编码。PtyID 用于客户端把多个 PTY 的输出 demux 到各自终端。
type CodePtyChunk struct {
	Type  string `json:"type"` // "code_pty_chunk"
	PtyID string `json:"pty_id"`
	Data  []byte `json:"data"`
}

func (*CodePtyChunk) isFrame()              {}
func (*CodePtyChunk) isCodeFrame()          {}
func (*CodePtyChunk) CodeFrameType() string { return TypeCodePtyChunk }

// CodePtyInput 是客户端 → 服务端的 PTY 输入字节（键盘/粘贴）。Data 经 base64 承载。
type CodePtyInput struct {
	Type  string `json:"type"` // "code_pty_input"
	PtyID string `json:"pty_id"`
	Data  []byte `json:"data"`
}

func (*CodePtyInput) isFrame()              {}
func (*CodePtyInput) isCodeFrame()          {}
func (*CodePtyInput) CodeFrameType() string { return TypeCodePtyInput }

// CodePtyResize 是客户端 → 服务端的终端尺寸变更。服务端会把 Cols/Rows 钳到
// [2,10000]（防 FitAddon 在隐藏容器算出 cols=2 通过 SIGWINCH 打散全屏 TUI）。
type CodePtyResize struct {
	Type  string `json:"type"` // "code_pty_resize"
	PtyID string `json:"pty_id"`
	Cols  uint16 `json:"cols"`
	Rows  uint16 `json:"rows"`
}

func (*CodePtyResize) isFrame()              {}
func (*CodePtyResize) isCodeFrame()          {}
func (*CodePtyResize) CodeFrameType() string { return TypeCodePtyResize }

// CodePtyExit 是服务端 → 客户端的进程退出通知。ExitCode 为 0 表示正常退出；
// Err 非空表示 spawn/wait 自身出错（非进程退出码）。
type CodePtyExit struct {
	Type     string `json:"type"` // "code_pty_exit"
	PtyID    string `json:"pty_id"`
	ExitCode int    `json:"exit_code"`
	Err      string `json:"error,omitempty"`
}

func (*CodePtyExit) isFrame()              {}
func (*CodePtyExit) isCodeFrame()          {}
func (*CodePtyExit) CodeFrameType() string { return TypeCodePtyExit }

// CodeSessionEvent 是服务端 → 客户端的「结构化会话事件」(M3)。daemon 的会话
// watcher tail 外部 agent(Claude/Codex)写的 JSONL,解析成与 Dart AgentEvent 同形的
// 事件经此推回,按 TaskID demux 到对应任务的结构化视图(与 code_pty_chunk 的原始
// 终端流并存,形成「结构化会话 + 原始终端」双视图)。
// Event 是单条 AgentEvent JSON(type=text_delta/tool_use_start/tool_use_result/cost_update/...)。
type CodeSessionEvent struct {
	Type   string          `json:"type"` // "code_session_event"
	TaskID string          `json:"task_id"`
	Event  json.RawMessage `json:"event"`
}

func (*CodeSessionEvent) isFrame()              {}
func (*CodeSessionEvent) isCodeFrame()          {}
func (*CodeSessionEvent) CodeFrameType() string { return TypeCodeSessionEvent }

// UnmarshalCodeFrame peek type 字段并 dispatch 到具体 CodeFrame。
// 调用方（service.go 的 UnmarshalFrame）已确认 head.Type 属于 code 类。
func UnmarshalCodeFrame(data []byte) (CodeFrame, error) {
	var head struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return nil, fmt.Errorf("code: peek type: %w", err)
	}
	var v CodeFrame
	switch head.Type {
	case TypeCodeRequest:
		v = &CodeRequest{}
	case TypeCodeResponse:
		v = &CodeResponse{}
	case TypeCodePtyChunk:
		v = &CodePtyChunk{}
	case TypeCodePtyInput:
		v = &CodePtyInput{}
	case TypeCodePtyResize:
		v = &CodePtyResize{}
	case TypeCodePtyExit:
		v = &CodePtyExit{}
	case TypeCodeSessionEvent:
		v = &CodeSessionEvent{}
	default:
		return nil, fmt.Errorf("code: unknown type %q", head.Type)
	}
	if err := json.Unmarshal(data, v); err != nil {
		return nil, fmt.Errorf("code: unmarshal %s: %w", head.Type, err)
	}
	return v, nil
}

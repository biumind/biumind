package sdkproto

import (
	"encoding/json"
	"fmt"
)

// StdinMessage / StdoutMessage 是 WebSocket frame 的根类型。
//
// 同一个 type 字段被三类共享：
//   - 数据平面：user / assistant / system / result / tool_progress / ... → SDKMessage
//   - 控制平面：control_request / control_response / control_cancel_request → SDKControlRequest etc
//   - 生命周期：keep_alive / update_environment_variables / biumind.* → Lifecycle
//
// dispatchByType 先看 type 字段属于哪一类，再走该类的具体 dispatcher。
//
// StdinMessage 跟 StdoutMessage 用同一个 dispatcher（实现差异在协议层 —— 比如 deny 一些方向不允许出现的 type）。

// Frame 是 WS wire 帧的标记接口。所有合法 wire 类型实现 isFrame()：
//   - 28 SDKMessage variant（SDKMessage 接口已嵌入 Frame）
//   - 6 Lifecycle 帧（Lifecycle 接口已嵌入 Frame）
//   - SDKControlRequest / SDKControlResponse / SDKControlCancelRequest（直接实现）
//
// 编译期约束：FrameEnvelope.Inner / UnmarshalFrame 返回值都强制 Frame 类型 ——
// 任何不属于以上 ~37 个类型的对象都过不了编译。
//
// isFrame() unexported 故意限制只能本包定义新 Frame 类型 —— 协议是封闭集合，
// 外部包不应该新增 wire 类型（要加得改 schema + sdkproto 包）。
type Frame interface {
	isFrame()
}

// FrameEnvelope 是 Frame 的容器，便于嵌入到上层 struct（比如 NATS message wrapper）。
// 提供自定义 UnmarshalJSON —— peek type 后 dispatch 到具体 Frame 类型。
type FrameEnvelope struct {
	Inner Frame
}

// MarshalJSON: 直接把 Inner marshal 成 JSON。
func (f FrameEnvelope) MarshalJSON() ([]byte, error) {
	if f.Inner == nil {
		return nil, fmt.Errorf("frame: nil inner")
	}
	return json.Marshal(f.Inner)
}

// UnmarshalJSON: peek type 字段，dispatch 到具体 Frame 类型。
func (f *FrameEnvelope) UnmarshalJSON(data []byte) error {
	v, err := UnmarshalFrame(data)
	if err != nil {
		return err
	}
	f.Inner = v
	return nil
}

// UnmarshalFrame 是 WS 帧通用解析入口 —— 三类（数据/控制/生命周期）按 type 字段分发。
// 返回 Frame 接口而不是 any —— 调用方拿到的对象一定是合法 wire 类型。
func UnmarshalFrame(data []byte) (Frame, error) {
	var head struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return nil, fmt.Errorf("frame: peek type: %w", err)
	}
	switch head.Type {
	// 控制平面（3 种）
	case TypeControlRequest:
		var v SDKControlRequest
		if err := json.Unmarshal(data, &v); err != nil {
			return nil, err
		}
		return &v, nil
	case TypeControlResponse:
		var v SDKControlResponse
		if err := json.Unmarshal(data, &v); err != nil {
			return nil, err
		}
		return &v, nil
	case TypeControlCancelRequest:
		var v SDKControlCancelRequest
		if err := json.Unmarshal(data, &v); err != nil {
			return nil, err
		}
		return &v, nil

	// 生命周期（8 种 BiuMind 自有）
	case TypeKeepAlive,
		TypeUpdateEnvironmentVariables,
		TypeSessionDesynced,
		TypeSessionPaused,
		TypeSessionResumed,
		TypeSessionPrimaryPromoted,
		TypeBiumindCompactStarted,
		TypeBiumindCompactFinished:
		return UnmarshalLifecycle(data)

	// 编码模块（6 种 code 帧，见 code.go）
	case TypeCodeRequest,
		TypeCodeResponse,
		TypeCodePtyChunk,
		TypeCodePtyInput,
		TypeCodePtyResize,
		TypeCodePtyExit:
		return UnmarshalCodeFrame(data)

	default:
		// 数据平面：user / assistant / stream_event / result / system / auth_status /
		// rate_limit_event / prompt_suggestion / tool_progress / tool_use_summary /
		// streamlined_text / streamlined_tool_use_summary
		return UnmarshalSDKMessage(data)
	}
}

// IsStdinMessage 检查 Frame 是否合法的 StdinMessage（客户端 → 服务端方向）。
// 协议规则：StdinMessage 不应该出现 ControlResponse / 多数 lifecycle（UpdateEnvironmentVariables 例外）。
func IsStdinMessage(frame Frame) bool {
	switch v := frame.(type) {
	case *SDKControlResponse:
		return false
	case Lifecycle:
		return v.LifecycleType() == TypeUpdateEnvironmentVariables
	default:
		return true
	}
}

// IsStdoutMessage 检查 Frame 是否合法的 StdoutMessage（服务端 → 客户端方向）。
// 协议规则：StdoutMessage 不应出现 ControlCancelRequest（cancel 是 client 发起）+
// UpdateEnvironmentVariables（仅 client → server）。
func IsStdoutMessage(frame Frame) bool {
	if _, ok := frame.(*SDKControlCancelRequest); ok {
		return false
	}
	if lc, ok := frame.(Lifecycle); ok {
		return lc.LifecycleType() != TypeUpdateEnvironmentVariables
	}
	return true
}

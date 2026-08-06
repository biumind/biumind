package sdkproto

import (
	"encoding/json"
	"fmt"
)

// BiuMind lifecycle 帧 type 字面量。
const (
	TypeKeepAlive                  = "keep_alive"
	TypeUpdateEnvironmentVariables = "update_environment_variables"
	TypeSessionDesynced            = "biumind.session_desynced"
	TypeSessionPaused              = "biumind.session_paused"
	TypeSessionResumed             = "biumind.session_resumed"
	TypeSessionPrimaryPromoted     = "biumind.session_primary_promoted"
	TypeBiumindCompactStarted      = "biumind.compact_started"
	TypeBiumindCompactFinished     = "biumind.compact_finished"
)

// Lifecycle 是 6 个 BiuMind 自有帧的标记接口。嵌入 Frame —— lifecycle 也是合法 WS 帧。
type Lifecycle interface {
	Frame
	isLifecycle()
	LifecycleType() string
}

type KeepAlive struct {
	Type string `json:"type"` // "keep_alive"
	TS   int64  `json:"ts,omitempty"`
}

func (*KeepAlive) isLifecycle()          {}
func (*KeepAlive) isFrame()              {}
func (*KeepAlive) LifecycleType() string { return TypeKeepAlive }

type UpdateEnvironmentVariables struct {
	Type      string            `json:"type"` // "update_environment_variables"
	Variables map[string]string `json:"variables"`
}

func (*UpdateEnvironmentVariables) isLifecycle()          {}
func (*UpdateEnvironmentVariables) isFrame()              {}
func (*UpdateEnvironmentVariables) LifecycleType() string { return TypeUpdateEnvironmentVariables }

type SessionDesynced struct {
	Type           string `json:"type"` // "biumind.session_desynced"
	SessionID      string `json:"session_id"`
	FinalResultURL string `json:"final_result_url,omitempty"`
	SinceSeq       *int64 `json:"since_seq,omitempty"`
}

func (*SessionDesynced) isLifecycle()          {}
func (*SessionDesynced) isFrame()              {}
func (*SessionDesynced) LifecycleType() string { return TypeSessionDesynced }

type SessionPaused struct {
	Type      string `json:"type"` // "biumind.session_paused"
	SessionID string `json:"session_id"`
	Reason    string `json:"reason,omitempty"`
}

func (*SessionPaused) isLifecycle()          {}
func (*SessionPaused) isFrame()              {}
func (*SessionPaused) LifecycleType() string { return TypeSessionPaused }

type SessionResumed struct {
	Type      string `json:"type"` // "biumind.session_resumed"
	SessionID string `json:"session_id"`
	SinceSeq  *int64 `json:"since_seq,omitempty"`
}

func (*SessionResumed) isLifecycle()          {}
func (*SessionResumed) isFrame()              {}
func (*SessionResumed) LifecycleType() string { return TypeSessionResumed }

type SessionPrimaryPromoted struct {
	Type           string `json:"type"` // "biumind.session_primary_promoted"
	SessionID      string `json:"session_id"`
	PrimaryReplica string `json:"primary_replica"`
}

func (*SessionPrimaryPromoted) isLifecycle()          {}
func (*SessionPrimaryPromoted) isFrame()              {}
func (*SessionPrimaryPromoted) LifecycleType() string { return TypeSessionPrimaryPromoted }

// BiumindCompactStarted 是 biumindkit 拆分的 compact 开始事件。上游无对应概念。
type BiumindCompactStarted struct {
	Type         string `json:"type"` // "biumind.compact_started"
	SessionID    string `json:"session_id"`
	Reason       string `json:"reason"`
	TokensBefore int    `json:"tokens_before"`
}

func (*BiumindCompactStarted) isLifecycle()          {}
func (*BiumindCompactStarted) isFrame()              {}
func (*BiumindCompactStarted) LifecycleType() string { return TypeBiumindCompactStarted }

// BiumindCompactFinished 是 biumindkit compact 完成事件，含 token 变化。
type BiumindCompactFinished struct {
	Type         string `json:"type"` // "biumind.compact_finished"
	SessionID    string `json:"session_id"`
	TokensBefore int    `json:"tokens_before"`
	TokensAfter  int    `json:"tokens_after"`
	TokensSaved  int    `json:"tokens_saved"`
}

func (*BiumindCompactFinished) isLifecycle()          {}
func (*BiumindCompactFinished) isFrame()              {}
func (*BiumindCompactFinished) LifecycleType() string { return TypeBiumindCompactFinished }

// UnmarshalLifecycle peek type 字段并 dispatch。
// 注意：调用方需先确认 head.Type 属于 lifecycle 类（service.go 的 dispatcher 负责区分）。
func UnmarshalLifecycle(data []byte) (Lifecycle, error) {
	var head struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return nil, fmt.Errorf("lifecycle: peek type: %w", err)
	}
	var v Lifecycle
	switch head.Type {
	case TypeKeepAlive:
		v = &KeepAlive{}
	case TypeUpdateEnvironmentVariables:
		v = &UpdateEnvironmentVariables{}
	case TypeSessionDesynced:
		v = &SessionDesynced{}
	case TypeSessionPaused:
		v = &SessionPaused{}
	case TypeSessionResumed:
		v = &SessionResumed{}
	case TypeSessionPrimaryPromoted:
		v = &SessionPrimaryPromoted{}
	case TypeBiumindCompactStarted:
		v = &BiumindCompactStarted{}
	case TypeBiumindCompactFinished:
		v = &BiumindCompactFinished{}
	default:
		return nil, fmt.Errorf("lifecycle: unknown type %q", head.Type)
	}
	if err := json.Unmarshal(data, v); err != nil {
		return nil, fmt.Errorf("lifecycle: unmarshal %s: %w", head.Type, err)
	}
	return v, nil
}

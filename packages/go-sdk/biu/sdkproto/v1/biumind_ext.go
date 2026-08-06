package sdkproto

import "encoding/json"

// Mode 字面量 —— 跟 schema biumind_ext.json 的 Mode enum 对齐。
const (
	ModeChat  = "chat"
	ModeAgent = "agent"
	ModeTask  = "task"
)

// Worker 类型字面量。
const (
	WorkerKindBiuDaemon = "biu_daemon"
	WorkerKindBiuCLI    = "biu_cli"
	WorkerKindRuntime   = "runtime"
)

// Environment state 字面量。
const (
	EnvStateOnline   = "online"
	EnvStateOffline  = "offline"
	EnvStateDraining = "draining"
)

// CreateSessionReq 是 POST /v1/code/sessions 的请求体。
type CreateSessionReq struct {
	Mode                 string   `json:"mode"`
	EnvironmentID        string   `json:"environment_id,omitempty"`
	ThreadID             string   `json:"thread_id,omitempty"`
	Model                string   `json:"model,omitempty"`
	SystemPrompt         string   `json:"system_prompt,omitempty"`
	BiumindAttachmentIDs []string `json:"biumind_attachment_ids,omitempty"`
}

// Session 是 POST /v1/code/sessions 的响应体。
type Session struct {
	SessionID           string `json:"session_id"`
	SessionToken        string `json:"session_token"`
	Mode                string `json:"mode"`
	ServerSeqStart      int64  `json:"server_seq_start"`
	JetstreamSubjectIn  string `json:"jetstream_subject_in,omitempty"`
	JetstreamSubjectOut string `json:"jetstream_subject_out,omitempty"`
	EnvironmentID       string `json:"environment_id,omitempty"`
	ThreadID            string `json:"thread_id,omitempty"`
}

// EnvironmentInfo 来自 GET /v1/agent/environments。
type EnvironmentInfo struct {
	EnvironmentID string          `json:"environment_id"`
	UserID        string          `json:"user_id,omitempty"`
	WorkerKind    string          `json:"worker_kind"`
	MachineName   string          `json:"machine_name"`
	OsArch        string          `json:"os_arch,omitempty"`
	GitInfo       json.RawMessage `json:"git_info,omitempty"`
	Capabilities  []string        `json:"capabilities,omitempty"`
	State         string          `json:"state"`
	LastSeenAt    int64           `json:"last_seen_at,omitempty"`
}

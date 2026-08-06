package sdkproto

import (
	"encoding/json"
	"fmt"
)

const (
	McpTypeStdio       = "stdio"
	McpTypeSSE         = "sse"
	McpTypeHTTP        = "http"
	McpTypeSDK         = "sdk"
	McpTypeClaudeProxy = "claudeai-proxy"

	McpStatusConnected = "connected"
	McpStatusFailed    = "failed"
	McpStatusNeedsAuth = "needs-auth"
	McpStatusPending   = "pending"
	McpStatusDisabled  = "disabled"
)

// McpServerConfig 是 5 个 server 类型的标记接口。
type McpServerConfig interface {
	isMcpServerConfig()
	McpServerType() string
}

type McpStdioServerConfig struct {
	Type    string            `json:"type"` // "stdio"
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

func (*McpStdioServerConfig) isMcpServerConfig()    {}
func (*McpStdioServerConfig) McpServerType() string { return McpTypeStdio }

type McpSSEServerConfig struct {
	Type    string            `json:"type"` // "sse"
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
}

func (*McpSSEServerConfig) isMcpServerConfig()    {}
func (*McpSSEServerConfig) McpServerType() string { return McpTypeSSE }

type McpHttpServerConfig struct {
	Type    string            `json:"type"` // "http"
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
}

func (*McpHttpServerConfig) isMcpServerConfig()    {}
func (*McpHttpServerConfig) McpServerType() string { return McpTypeHTTP }

type McpSdkServerConfig struct {
	Type     string          `json:"type"` // "sdk"
	Name     string          `json:"name"`
	Instance json.RawMessage `json:"instance,omitempty"`
}

func (*McpSdkServerConfig) isMcpServerConfig()    {}
func (*McpSdkServerConfig) McpServerType() string { return McpTypeSDK }

type McpClaudeAIProxyServerConfig struct {
	Type    string            `json:"type"` // "claudeai-proxy"
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
}

func (*McpClaudeAIProxyServerConfig) isMcpServerConfig()    {}
func (*McpClaudeAIProxyServerConfig) McpServerType() string { return McpTypeClaudeProxy }

// UnmarshalMcpServerConfig dispatch 5 个变体。stdio 没有显式 type 字段时也按 stdio 处理（协议默认行为）。
func UnmarshalMcpServerConfig(data []byte) (McpServerConfig, error) {
	var head struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return nil, fmt.Errorf("mcp_server: peek type: %w", err)
	}
	var v McpServerConfig
	switch head.Type {
	case "", McpTypeStdio:
		v = &McpStdioServerConfig{}
	case McpTypeSSE:
		v = &McpSSEServerConfig{}
	case McpTypeHTTP:
		v = &McpHttpServerConfig{}
	case McpTypeSDK:
		v = &McpSdkServerConfig{}
	case McpTypeClaudeProxy:
		v = &McpClaudeAIProxyServerConfig{}
	default:
		return nil, fmt.Errorf("mcp_server: unknown type %q", head.Type)
	}
	if err := json.Unmarshal(data, v); err != nil {
		return nil, fmt.Errorf("mcp_server: unmarshal %s: %w", head.Type, err)
	}
	return v, nil
}

type McpServerInfo struct {
	Name    string `json:"name,omitempty"`
	Version string `json:"version,omitempty"`
}

type McpServerStatus struct {
	Name       string         `json:"name"`
	Status     string         `json:"status"` // connected | failed | needs-auth | pending | disabled
	ServerInfo *McpServerInfo `json:"serverInfo,omitempty"`
	Error      string         `json:"error,omitempty"`
}

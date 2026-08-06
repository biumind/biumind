package sdkproto

// SlashCommand / AgentInfo / AgentDefinition / ModelInfo / AccountInfo。

type SlashCommand struct {
	Name         string   `json:"name"`
	Description  string   `json:"description,omitempty"`
	ArgumentHint string   `json:"argumentHint,omitempty"`
	Aliases      []string `json:"aliases,omitempty"`
}

type AgentInfo struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Model       string   `json:"model,omitempty"`
	Tools       []string `json:"tools,omitempty"`
}

type AgentDefinition struct {
	Description string   `json:"description"`
	Prompt      string   `json:"prompt"`
	Model       string   `json:"model,omitempty"`
	Tools       []string `json:"tools,omitempty"`
	McpServers  []string `json:"mcp_servers,omitempty"`
	WhenToUse   string   `json:"whenToUse,omitempty"`
}

type ModelInfo struct {
	ID              string `json:"id"`
	DisplayName     string `json:"displayName"`
	ContextWindow   int    `json:"contextWindow,omitempty"`
	MaxOutputTokens int    `json:"maxOutputTokens,omitempty"`
	Default         *bool  `json:"default,omitempty"`
}

type AccountInfo struct {
	Email        string `json:"email,omitempty"`
	Organization string `json:"organization,omitempty"`
}

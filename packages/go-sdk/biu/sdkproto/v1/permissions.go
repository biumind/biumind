package sdkproto

import (
	"encoding/json"
	"fmt"
)

// Permission enum 字面量。
const (
	PermissionAllow = "allow"
	PermissionDeny  = "deny"
	PermissionAsk   = "ask"

	PermissionModeDefault           = "default"
	PermissionModeAcceptEdits       = "acceptEdits"
	PermissionModeBypassPermissions = "bypassPermissions"
	PermissionModePlan              = "plan"
	PermissionModeDontAsk           = "dontAsk"

	PermissionDestUserSettings    = "userSettings"
	PermissionDestProjectSettings = "projectSettings"
	PermissionDestLocalSettings   = "localSettings"
	PermissionDestSession         = "session"
	PermissionDestCliArg          = "cliArg"

	PermissionUpdateAddRules          = "addRules"
	PermissionUpdateReplaceRules      = "replaceRules"
	PermissionUpdateRemoveRules       = "removeRules"
	PermissionUpdateSetMode           = "setMode"
	PermissionUpdateAddDirectories    = "addDirectories"
	PermissionUpdateRemoveDirectories = "removeDirectories"
)

type PermissionRuleValue struct {
	ToolName    string `json:"toolName"`
	RuleContent string `json:"ruleContent,omitempty"`
}

// PermissionUpdate 是 6 个变体的标记接口。type 字段做 discriminator。
type PermissionUpdate interface {
	isPermissionUpdate()
	PermissionUpdateType() string
}

type AddRules struct {
	Type        string                `json:"type"` // "addRules"
	Rules       []PermissionRuleValue `json:"rules"`
	Behavior    string                `json:"behavior"`
	Destination string                `json:"destination"`
}

func (*AddRules) isPermissionUpdate()          {}
func (*AddRules) PermissionUpdateType() string { return PermissionUpdateAddRules }

type ReplaceRules struct {
	Type        string                `json:"type"` // "replaceRules"
	Rules       []PermissionRuleValue `json:"rules"`
	Behavior    string                `json:"behavior"`
	Destination string                `json:"destination"`
}

func (*ReplaceRules) isPermissionUpdate()          {}
func (*ReplaceRules) PermissionUpdateType() string { return PermissionUpdateReplaceRules }

type RemoveRules struct {
	Type        string                `json:"type"` // "removeRules"
	Rules       []PermissionRuleValue `json:"rules"`
	Behavior    string                `json:"behavior"`
	Destination string                `json:"destination"`
}

func (*RemoveRules) isPermissionUpdate()          {}
func (*RemoveRules) PermissionUpdateType() string { return PermissionUpdateRemoveRules }

type SetModeUpdate struct {
	Type        string `json:"type"` // "setMode"
	Mode        string `json:"mode"`
	Destination string `json:"destination"`
}

func (*SetModeUpdate) isPermissionUpdate()          {}
func (*SetModeUpdate) PermissionUpdateType() string { return PermissionUpdateSetMode }

type AddDirectories struct {
	Type        string   `json:"type"` // "addDirectories"
	Directories []string `json:"directories"`
	Destination string   `json:"destination"`
}

func (*AddDirectories) isPermissionUpdate()          {}
func (*AddDirectories) PermissionUpdateType() string { return PermissionUpdateAddDirectories }

type RemoveDirectories struct {
	Type        string   `json:"type"` // "removeDirectories"
	Directories []string `json:"directories"`
	Destination string   `json:"destination"`
}

func (*RemoveDirectories) isPermissionUpdate()          {}
func (*RemoveDirectories) PermissionUpdateType() string { return PermissionUpdateRemoveDirectories }

// UnmarshalPermissionUpdate dispatch 6 个变体。
func UnmarshalPermissionUpdate(data []byte) (PermissionUpdate, error) {
	var head struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return nil, fmt.Errorf("permission_update: peek type: %w", err)
	}
	var v PermissionUpdate
	switch head.Type {
	case PermissionUpdateAddRules:
		v = &AddRules{}
	case PermissionUpdateReplaceRules:
		v = &ReplaceRules{}
	case PermissionUpdateRemoveRules:
		v = &RemoveRules{}
	case PermissionUpdateSetMode:
		v = &SetModeUpdate{}
	case PermissionUpdateAddDirectories:
		v = &AddDirectories{}
	case PermissionUpdateRemoveDirectories:
		v = &RemoveDirectories{}
	default:
		return nil, fmt.Errorf("permission_update: unknown type %q", head.Type)
	}
	if err := json.Unmarshal(data, v); err != nil {
		return nil, fmt.Errorf("permission_update: unmarshal %s: %w", head.Type, err)
	}
	return v, nil
}

// PermissionResult 是 can_use_tool 的响应：allow 或 deny。
// behavior 区分；allow 带 updatedInput / updatedPermissions，deny 带 message / interrupt。
// 用单 struct 表示两态，简化 marshal。
type PermissionResult struct {
	Behavior           string             `json:"behavior"`
	UpdatedInput       json.RawMessage    `json:"updatedInput,omitempty"`
	UpdatedPermissions []PermissionUpdate `json:"updatedPermissions,omitempty"`
	Message            string             `json:"message,omitempty"`
	Interrupt          *bool              `json:"interrupt,omitempty"`
}

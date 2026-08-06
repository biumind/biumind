package sdkproto

import "encoding/json"

const (
	SettingSourceUser    = "userSettings"
	SettingSourceProject = "projectSettings"
	SettingSourceLocal   = "localSettings"
	SettingSourceManaged = "managedSettings"
	SettingSourcePolicy  = "policySettings"
	SettingSourceFlag    = "flag"
)

// GetSettingsResponse 三段：effective（合并后）/ sources（每层 raw）/ applied（每个 key 来自哪层）。
// 各层值结构差异大，用 RawMessage 保留原状。
type GetSettingsResponse struct {
	Effective json.RawMessage            `json:"effective"`
	Sources   map[string]json.RawMessage `json:"sources"`
	Applied   map[string]string          `json:"applied"`
}

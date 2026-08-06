package chat

import "encoding/json"

// jsonUnmarshalLooseImpl —— 用 encoding/json 反序列化，错也吞了。给
// agent_v2 的 hub tool input 翻译用：input 偶尔是空字符串 / 部分 JSON，
// 不想为这种 best-effort 路径在调用方手抖一行 err 处理。
func jsonUnmarshalLooseImpl(raw []byte, dst *map[string]any) error {
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, dst)
}

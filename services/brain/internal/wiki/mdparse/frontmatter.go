// frontmatter.go —— markdown 文档头的 YAML frontmatter 剥离。
//
// 背景（2026-09-04 页面串味事故）：wiki-llm 工人产出的页面是 Obsidian
// 风格单文档（frontmatter 写在 .md 开头），但 biumind 的存储模型是
// 结构化的 —— frontmatter 是 brain.pages 的独立 jsonb 列，body_md 只存
// 正文。goldmark（CommonMark）没有 frontmatter 概念：开头的 `---` 被当
// 成主题分隔线丢弃，YAML 各行 + 结尾 `---` 恰好构成 setext 二级标题
// 语法，于是整段 YAML 被投成 heading level=2 的正文块糊在页面顶部。
//
// 这里在写库边界统一「撕标签」：SplitFrontmatter 把严格形态的
// frontmatter 拆成 (结构化 map, 纯正文)。只支持严格锚定形态
// （^---\n…\n---\n），因为 wiki-llm 工人写前已 sanitize 成标准形态，
// 历史脏数据经核实也是标准形态；畸形形态由工人侧 sanitize 负责，
// 这里不做猜测性恢复（保守原则：宁可不动正文，不可误拆内容）。
package mdparse

import (
	"regexp"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// 严格锚定：开头围栏行 + YAML 负载 + 结尾围栏行，两围栏各自独占一行。
// 容忍 CRLF。非开头位置的 `---`（如正文里的分隔线）不匹配。
var frontmatterStrictRE = regexp.MustCompile(
	`(?s)^(?:\x{FEFF})?---[ \t]*\r?\n(.*?)\r?\n---[ \t]*(?:\r?\n|$)`)

// SplitFrontmatter 把文档开头的 YAML frontmatter 拆成结构化 map 与纯正文。
//
// 返回值约定：
//   - 命中且 YAML 解析为非空 map：返回 (规范化 map, 剥离后的正文)
//   - 其余情况（无 frontmatter / 围栏不成对 / YAML 不是 map / 空 map）：
//     返回 (nil, 原文) —— 调用方按"无 frontmatter"处理，正文一字节不动
//
// map 值规范化为 string 或 []string，与 brain.pages.frontmatter jsonb 的
// UI 契约一致（FrontmatterPanel 只渲染扁平 string / string list）；标量
// 转字符串，列表逐项转字符串。
func SplitFrontmatter(content string) (map[string]any, string) {
	m := frontmatterStrictRE.FindStringSubmatch(content)
	if m == nil {
		return nil, content
	}
	var parsed map[string]any
	if err := yaml.Unmarshal([]byte(m[1]), &parsed); err != nil || len(parsed) == 0 {
		return nil, content
	}
	fm := make(map[string]any, len(parsed))
	for k, v := range parsed {
		fm[k] = normalizeFrontmatterValue(v)
	}
	return fm, content[len(m[0]):]
}

// normalizeFrontmatterValue 拍平 YAML 值到 string / []string。
// 嵌套 map / 嵌套列表没有 UI 投影，序列化成 YAML 文本保留可见性（与
// wiki-llm 工人侧 _normalize 的 JSON 兜底同思路）。
func normalizeFrontmatterValue(v any) any {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			out = append(out, stringifyFrontmatterScalar(item))
		}
		return out
	default:
		return stringifyFrontmatterScalar(v)
	}
}

func stringifyFrontmatterScalar(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case time.Time:
		// YAML 会把 2026-07-31 解成 time.Time；与工人侧 _normalize 一致
		// 只保留日期部分（JS Date.toISOString().slice(0,10) 行为）。
		return t.Format("2006-01-02")
	default:
		// bool / int / float / 嵌套结构：yaml 序列化回文本。
		b, err := yaml.Marshal(v)
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(b))
	}
}

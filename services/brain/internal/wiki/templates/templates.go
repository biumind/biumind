// Package templates holds the built-in wiki project templates.
//
// A Template is pure data: an id, display metadata, and a set of seed
// pages (schema + purpose + index + log + overview) that get written into
// a freshly-created project
// so the user starts with structure instead of a blank slate. The seed
// markdown is embedded (go:embed) and parsed once at package init via
// mdparse into structured blocks — the same parser the ingest path uses —
// so seeded pages land as real heading/text/code blocks and flow through
// the normal chunker + embed pipeline with headingPath context.
//
// Design (see docs/BiuMind-Wiki-Gap-Analysis-DevPlan.md §S2 ②):
//   - 5 ids mirror reference/llm_wiki (research/reading/personal/business/
//     general). `general` and any unknown id seed nothing — Lookup returns
//     nil, and the project is created empty (back-compat with old clients).
//   - 每套模板 seed 5 个起手页（对齐 llm_wiki 的 5 个起手文件）：
//     schema（页面规范，模板专属）+ purpose（项目目标，模板专属）+
//     index（页面索引）/ log（变更日志）/ overview（项目概览，三者为
//     全模板共享的骨架）。overview 同时被 ingest-context 端点读出，
//     喂给 wiki-llm 两阶段 ingest 作项目上下文。
//   - schema/purpose/index/log/overview land as first-class, visible,
//     editable pages (frontmatter type 同名), not hidden system pages.
//   - 只影响新建项目：存量项目不 backfill（不知道哪些起手页已被用户
//     改过 / 删过，回填会覆盖用户意图；需要的人手动建页即可）。
//   - biumind has no filesystem directories (pages form a parent_id tree +
//     frontmatter.type), so the llm_wiki "Directory" column is reframed as
//     a frontmatter.type tag + [[backlink]] guidance — no empty container
//     pages are created.
//
// Content is authored in-house (CN-first), structure inspired by llm_wiki's
// templates.ts; no code is forked.
package templates

import (
	"embed"
	"fmt"

	"github.com/biumind/biumind/services/brain/internal/wiki/mdparse"
)

//go:embed *.md
var fs embed.FS

// SeedBlock is one structured block to seed into a new page, shaped to drop
// straight into a brain.blocks INSERT (type + content jsonb).
type SeedBlock struct {
	Type    string
	Content map[string]any
}

// SeedPage is one page to seed into a new project.
type SeedPage struct {
	Title       string
	Frontmatter map[string]any
	BodyMd      string // §⑤ 原始 markdown 正文（blocks 为其 mdparse 投影）
	Blocks      []SeedBlock
}

// Template is one built-in project template.
type Template struct {
	ID          string
	Name        string
	Description string
	SeedPages   []SeedPage
}

// list is built once at package init. `general` is intentionally absent —
// it seeds nothing, so Lookup("general") returns nil like unknown ids.
var list = []Template{
	must("research", "研究", "深度研究：假设追踪 + 方法论 + 综述 + 知识图谱",
		"research_schema.md", "research_purpose.md"),
	must("reading", "阅读", "读书笔记：人物 / 主题 / 情节线 / 章节记录",
		"reading_schema.md", "reading_purpose.md"),
	must("personal", "个人成长", "目标 / 习惯 / 反思 / 日记，链接成自我知识网",
		"personal_schema.md", "personal_purpose.md"),
	must("business", "商务团队", "团队知识库：会议纪要 / 决策记录 / 项目 / 干系人",
		"business_schema.md", "business_purpose.md"),
}

// Lookup returns the template for id, or nil for "" / "general" / unknown.
// Callers treat nil as "seed nothing" — the project is still created empty.
func Lookup(id string) *Template {
	for i := range list {
		if list[i].ID == id {
			return &list[i]
		}
	}
	return nil
}

// All returns every seed-bearing template (excludes general). Used by tests
// and a future admin/catalog endpoint.
func All() []Template { return list }

// must builds a Template whose seed pages are parsed from the embedded
// schema/purpose markdown (per-template) plus the shared index/log/
// overview skeletons. Panics on missing embed (compile-time guarantee
// means a missing file is a developer error, not a runtime one).
func must(id, name, desc, schemaFile, purposeFile string) Template {
	return Template{
		ID:          id,
		Name:        name,
		Description: desc,
		SeedPages: []SeedPage{
			seedPage("页面规范", "schema", schemaFile),
			seedPage("项目目标", "purpose", purposeFile),
			seedPage("页面索引", "index", "index.md"),
			seedPage("变更日志", "log", "log.md"),
			seedPage("项目概览", "overview", "overview.md"),
		},
	}
}

// seedPage parses one embedded markdown file into structured blocks.
func seedPage(title, typ, file string) SeedPage {
	md, err := fs.ReadFile(file)
	if err != nil {
		panic(fmt.Sprintf("templates: embed read %s: %v", file, err))
	}
	parsed := mdparse.ParseBlocks(string(md))
	blocks := make([]SeedBlock, 0, len(parsed))
	for _, b := range parsed {
		blocks = append(blocks, SeedBlock{Type: b.Type, Content: b.Content})
	}
	return SeedPage{
		Title:       title,
		Frontmatter: map[string]any{"type": typ},
		BodyMd:      string(md),
		Blocks:      blocks,
	}
}

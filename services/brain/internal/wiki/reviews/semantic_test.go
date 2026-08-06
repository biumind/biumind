package reviews

import (
	"testing"

	"github.com/google/uuid"
)

func TestParseLintBlocks(t *testing.T) {
	raw := `Some preamble the model was told not to emit.

---LINT: contradiction | warning | 性能指标冲突---
A 页说延迟 10ms，B 页说 100ms，相互矛盾。
PAGES: A页, B页
---END LINT---
---LINT: missing-page | info | Transformer---
多处引用 Transformer 但无专页。
PAGES: attention原理
---END LINT---
`
	got := parseLintBlocks(raw)
	if len(got) != 2 {
		t.Fatalf("want 2 findings, got %d: %+v", len(got), got)
	}
	if got[0].Category != "contradiction" || got[0].Severity != "warning" {
		t.Errorf("finding0 category/severity = %q/%q, want contradiction/warning", got[0].Category, got[0].Severity)
	}
	if got[0].Title != "性能指标冲突" {
		t.Errorf("finding0 title = %q", got[0].Title)
	}
	if len(got[0].Pages) != 2 || got[0].Pages[0] != "A页" || got[0].Pages[1] != "B页" {
		t.Errorf("finding0 pages = %+v", got[0].Pages)
	}
	if got[1].Category != "missing-page" || got[1].Title != "Transformer" {
		t.Errorf("finding1 = %+v", got[1])
	}
}

func TestParseLintBlocks_Garbage(t *testing.T) {
	cases := []string{
		"",
		"no blocks here at all",
		"---LINT: foo | bar---\nmissing END marker",
		"random prose without the delimiter",
	}
	for _, c := range cases {
		if got := parseLintBlocks(c); got != nil {
			t.Errorf("parse(%q) = %+v, want nil", c, got)
		}
	}
}

func TestNormCategory(t *testing.T) {
	cases := map[string]string{
		"contradiction":  "contradiction",
		"CONTRADICTION":  "contradiction",
		" missing_page ": "missing-page",
		"Missing-Page":   "missing-page",
		"stale":          "stale",
		"suggestion":     "suggestion",
		"unknown":        "unknown", // 非标准分类原样保留（rule_id 用，徽章留空）
	}
	for in, want := range cases {
		if got := normCategory(in); got != want {
			t.Errorf("normCategory(%q) = %q, want %q", in, got, want)
		}
	}
}

// postFilter 的核心契约：missing-page 标题若已对应现有页 → drop（LLM 不可靠
// 交叉引用文件列表，参考 llm_wiki lint.ts:72-79）。其它分类一律保留。
func TestPostFilter_MissingPageDrop(t *testing.T) {
	r := &SemanticRunner{}
	pageA := uuid.New()
	titleMap := map[string]uuid.UUID{
		"transformer": pageA, // "Transformer" 已建页 → missing-page 应被 drop
	}
	findings := []SemanticFinding{
		{Category: "missing-page", Title: "Transformer", Detail: "d1"},
		{Category: "missing-page", Title: "全新概念", Detail: "d2"},   // 无现页 → 保留
		{Category: "contradiction", Title: "冲突", Detail: "d3"},    // 非缺页 → 保留
		{Category: "suggestion", Title: "建议补图", Detail: "d4"},     // 保留
	}
	got := r.postFilter(findings, titleMap)
	if len(got) != 3 {
		t.Fatalf("want 3 findings after filter, got %d: %+v", len(got), got)
	}
	for _, f := range got {
		if f.Category == "missing-page" && f.Title == "Transformer" {
			t.Errorf("Transformer missing-page should have been dropped")
		}
	}
}

func TestSemanticDedupeKey(t *testing.T) {
	pid := uuid.New()
	mainID := uuid.New()
	cat := "contradiction"
	discrim := "title|detail"

	// 有 mainID → semantic:<page>:<cat>:<hash>
	k1 := semanticDedupeKey(pid, mainID, cat, discrim)
	wantPrefix := "semantic:" + mainID.String() + ":contradiction:"
	if len(k1) <= len(wantPrefix) || k1[:len(wantPrefix)] != wantPrefix {
		t.Errorf("key with mainID = %q, want prefix %q", k1, wantPrefix)
	}
	// 幂等：同输入同 key
	if k2 := semanticDedupeKey(pid, mainID, cat, discrim); k2 != k1 {
		t.Errorf("non-idempotent: %q != %q", k1, k2)
	}
	// 无 mainID → semantic:project:<pid>:<cat>:<hash>
	kp := semanticDedupeKey(pid, uuid.Nil, cat, discrim)
	wantProj := "semantic:project:" + pid.String() + ":contradiction:"
	if len(kp) <= len(wantProj) || kp[:len(wantProj)] != wantProj {
		t.Errorf("project key = %q, want prefix %q", kp, wantProj)
	}
	// 不同 discrim → 不同 hash
	if semanticDedupeKey(pid, mainID, cat, "other") == k1 {
		t.Errorf("different discrim produced same key (hash collision expected to differ)")
	}
}

func TestResolvePageIDs(t *testing.T) {
	a := uuid.New()
	b := uuid.New()
	titleMap := map[string]uuid.UUID{
		"page a": a,
		"page b": b,
	}
	// 命中 + 未命中 + 重复 去重
	got := resolvePageIDs([]string{"Page A", "Page A", "未知页", "Page B"}, titleMap)
	if len(got) != 2 || got[0] != a || got[1] != b {
		t.Errorf("resolvePageIDs = %+v, want [%s %s]", got, a, b)
	}
}

func TestNoopSemanticCaller(t *testing.T) {
	got, err := NoopSemanticCaller{}.Analyze(t.Context(), uuid.New(), nil)
	if err != nil || got != nil {
		t.Errorf("noop = %+v / %v, want nil/nil", got, err)
	}
}

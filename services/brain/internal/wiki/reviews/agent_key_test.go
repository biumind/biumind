package reviews

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestAgentKey_DeterministicRegardlessOfPageOrder(t *testing.T) {
	pid := uuid.New()
	a, b := uuid.New(), uuid.New()
	k1 := AgentKey(pid, KindContradiction, []uuid.UUID{a, b}, "X 与 Y 矛盾")
	k2 := AgentKey(pid, KindContradiction, []uuid.UUID{b, a}, "X 与 Y 矛盾")
	if k1 != k2 {
		t.Errorf("key must be page-order independent: %s vs %s", k1, k2)
	}
	if !strings.HasPrefix(k1, "agent:"+pid.String()+":"+KindContradiction+":") {
		t.Errorf("unexpected key shape: %s", k1)
	}
}

func TestAgentKey_TitleNormalised(t *testing.T) {
	pid := uuid.New()
	k1 := AgentKey(pid, KindSuggestion, nil, "  补全  Frontmatter ")
	k2 := AgentKey(pid, KindSuggestion, nil, "补全 frontmatter")
	if k1 != k2 {
		t.Errorf("case/whitespace-only title differences must hash the same: %s vs %s", k1, k2)
	}
}

func TestAgentKey_Discriminates(t *testing.T) {
	pid := uuid.New()
	page := uuid.New()
	base := AgentKey(pid, KindContradiction, []uuid.UUID{page}, "t")
	cases := map[string]string{
		"other project": AgentKey(uuid.New(), KindContradiction, []uuid.UUID{page}, "t"),
		"other kind":    AgentKey(pid, KindSuggestion, []uuid.UUID{page}, "t"),
		"other page":    AgentKey(pid, KindContradiction, []uuid.UUID{uuid.New()}, "t"),
		"no pages":      AgentKey(pid, KindContradiction, nil, "t"),
		"other title":   AgentKey(pid, KindContradiction, []uuid.UUID{page}, "t2"),
	}
	for name, k := range cases {
		if k == base {
			t.Errorf("%s must change the key", name)
		}
	}
}

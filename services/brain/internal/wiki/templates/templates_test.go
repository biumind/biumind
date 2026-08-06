package templates

import "testing"

func TestLookup_KnownTemplates(t *testing.T) {
	for _, id := range []string{"research", "reading", "personal", "business"} {
		tpl := Lookup(id)
		if tpl == nil {
			t.Errorf("Lookup(%q) = nil, want template", id)
			continue
		}
		if len(tpl.SeedPages) != 2 {
			t.Errorf("template %q: want 2 seed pages, got %d", id, len(tpl.SeedPages))
		}
		for _, sp := range tpl.SeedPages {
			if len(sp.Blocks) == 0 {
				t.Errorf("template %q seed page %q parsed zero blocks (mdparse regression?)",
					id, sp.Title)
			}
			if sp.Frontmatter["type"] != "schema" && sp.Frontmatter["type"] != "purpose" {
				t.Errorf("template %q seed page %q frontmatter.type = %v",
					id, sp.Title, sp.Frontmatter["type"])
			}
		}
	}
}

func TestLookup_NoSeedIds(t *testing.T) {
	// general / empty / unknown all seed nothing → nil.
	for _, id := range []string{"general", "", "nonexistent"} {
		if tpl := Lookup(id); tpl != nil {
			t.Errorf("Lookup(%q) = %v, want nil (no seed)", id, tpl)
		}
	}
}

func TestAll_HasFourSeedTemplates(t *testing.T) {
	// general is intentionally absent from All (it seeds nothing).
	if len(All()) != 4 {
		t.Errorf("All() returned %d templates, want 4", len(All()))
	}
}

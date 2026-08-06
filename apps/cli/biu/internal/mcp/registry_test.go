package mcp

import "testing"

func TestQualifyName(t *testing.T) {
	got := QualifyName("github", "create_pr")
	if got != "mcp__github__create_pr" {
		t.Errorf("got %q", got)
	}
}

func TestSplitName(t *testing.T) {
	cases := []struct {
		in     string
		server string
		tool   string
		ok     bool
	}{
		{"mcp__github__create_pr", "github", "create_pr", true},
		{"mcp__fs__read_file", "fs", "read_file", true},
		{"mcp__weird__a__b__c", "weird", "a__b__c", true}, // tool name may contain double underscore
		{"read", "", "", false},
		{"mcp__bad", "", "", false},
		{"mcp__", "", "", false},
		{"", "", "", false},
	}
	for _, c := range cases {
		s, tn, ok := SplitName(c.in)
		if ok != c.ok || s != c.server || tn != c.tool {
			t.Errorf("SplitName(%q) = (%q,%q,%v); want (%q,%q,%v)",
				c.in, s, tn, ok, c.server, c.tool, c.ok)
		}
	}
}

func TestValidServerName(t *testing.T) {
	good := []string{"github", "fs-1", "my_server", "abc123"}
	bad := []string{"", "Github", "with space", "weird/slash", "中文"}
	for _, n := range good {
		if !validServerName(n) {
			t.Errorf("expected %q valid", n)
		}
	}
	for _, n := range bad {
		if validServerName(n) {
			t.Errorf("expected %q invalid", n)
		}
	}
}

func TestFlattenSchemaNil(t *testing.T) {
	s := FlattenSchema(nil)
	if s["type"] != "object" {
		t.Errorf("nil schema should yield object")
	}
	if _, ok := s["properties"]; !ok {
		t.Errorf("missing properties")
	}
}

func TestFlattenTextResult(t *testing.T) {
	r := &CallToolResult{Content: []ContentBlock{
		{Type: "text", Text: "hello"},
		{Type: "text", Text: "world"},
		{Type: "image", MimeType: "image/png"},
	}}
	got := r.FlattenText()
	want := "hello\nworld\n[image: image/png]"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

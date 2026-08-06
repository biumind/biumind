package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/biumind/biumind/services/runtime/internal/authz"
	skillsreg "github.com/biumind/biumind/services/runtime/internal/skills"
	"github.com/google/uuid"
)

// Per-tool unit tests — pure-function checks that don't require a
// live Postgres. The DB-backed integration tests for the underlying
// skillsreg.Registry calls live alongside skills/registry_test.go;
// here we cover the parameter-validation + soft-error paths the
// agent loop relies on.

func TestRegisterSkillTools_NilRegistryIsNoOp(t *testing.T) {
	r := NewRegistry()
	RegisterSkillTools(r, SkillToolDeps{}) // no Registry
	if got := len(r.tools); got != 0 {
		t.Errorf("nil deps.Registry should register no tools; got %d", got)
	}
}

func TestRegisterSkillTools_RegistersEightTools(t *testing.T) {
	r := NewRegistry()
	RegisterSkillTools(r, SkillToolDeps{
		Registry: &skillsreg.Registry{}, // present but unused by this assertion
		OrgID:    uuid.New(),
		AgentID:  uuid.New(),
	})
	want := []string{
		"skill.list", "skill.activate", "skill.read_reference",
		"skill.exec_script", "skill.export_file", "skill.propose",
		"skill.recall_memory", "skill.read_wiki",
	}
	for _, name := range want {
		if _, ok := r.tools[name]; !ok {
			t.Errorf("missing tool %q", name)
		}
	}
}

func TestSkillRecallMemory_RequiresMemoryAndProject(t *testing.T) {
	// Without Memory client wired the tool surfaces a friendly error.
	tool := skillRecallMemoryTool(SkillToolDeps{
		Registry: &skillsreg.Registry{}, OrgID: uuid.New(),
	})
	_, err := tool.Invoke(context.Background(), map[string]any{
		"identifier": "x", "query": "anything",
	})
	if err == nil || !strings.Contains(err.Error(), "memory client not configured") {
		t.Errorf("want memory-not-configured error, got %v", err)
	}
}

func TestSkillReadWiki_RequiresWikiClient(t *testing.T) {
	tool := skillReadWikiTool(SkillToolDeps{
		Registry: &skillsreg.Registry{}, OrgID: uuid.New(),
	})
	_, err := tool.Invoke(context.Background(), map[string]any{
		"identifier": "x", "query": "anything",
	})
	if err == nil || !strings.Contains(err.Error(), "wiki client not configured") {
		t.Errorf("want wiki-not-configured error, got %v", err)
	}
}

func TestSkillReadWiki_RejectsMissingArgs(t *testing.T) {
	tool := skillReadWikiTool(SkillToolDeps{
		Registry: &skillsreg.Registry{}, OrgID: uuid.New(),
	})
	_, err := tool.Invoke(context.Background(), map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "identifier required") {
		t.Errorf("want identifier-required error, got %v", err)
	}
}

func TestSkillExecScript_RejectsMissingArgs(t *testing.T) {
	tool := skillExecScriptTool(SkillToolDeps{
		Registry: &skillsreg.Registry{}, OrgID: uuid.New(), AgentID: uuid.New(),
	})
	_, err := tool.Invoke(context.Background(), map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "required") {
		t.Errorf("expected required-args error, got %v", err)
	}
	_, err = tool.Invoke(context.Background(), map[string]any{
		"identifier": "x", "command": "",
	})
	if err == nil {
		t.Error("expected error for empty command")
	}
}

func TestSkillExecScript_SoftErrorWhenSandboxNil(t *testing.T) {
	// PS3.6 wires the real sandbox; until then nil Sandbox returns a
	// friendly message rather than a nil-deref crash. The model can
	// fall back to the regular bash tool.
	tool := skillExecScriptTool(SkillToolDeps{
		Registry: &skillsreg.Registry{}, OrgID: uuid.New(), AgentID: uuid.New(),
	})
	_, err := tool.Invoke(context.Background(), map[string]any{
		"identifier": "code-review", "command": "ls",
	})
	if err == nil || !strings.Contains(err.Error(), "sandbox not configured") {
		t.Errorf("want sandbox-soft-error, got %v", err)
	}
}

func TestSkillExportFile_SoftErrorWhenFilesNil(t *testing.T) {
	tool := skillExportFileTool(SkillToolDeps{
		Registry: &skillsreg.Registry{}, OrgID: uuid.New(), AgentID: uuid.New(),
	})
	_, err := tool.Invoke(context.Background(), map[string]any{
		"sandbox_path": "/work/out.csv", "filename": "out.csv",
	})
	if err == nil || !strings.Contains(err.Error(), "files service not configured") {
		t.Errorf("want files-soft-error, got %v", err)
	}
}

// stubFiles records UploadFromSandbox calls so Cedar-gated tests can
// assert the upload was (or wasn't) reached.
type stubFiles struct {
	called int
}

func (f *stubFiles) UploadFromSandbox(_ context.Context, _ uuid.UUID, _, _ string) (string, string, error) {
	f.called++
	return "file_x", "https://files/x", nil
}

func TestSkillExportFile_AuthzAllowReachesUpload(t *testing.T) {
	rec := &recordingDecider{want: authz.Allow}
	files := &stubFiles{}
	tool := skillExportFileTool(SkillToolDeps{
		Registry: &skillsreg.Registry{},
		Authz:    rec,
		Files:    files,
		OrgID:    uuid.New(), OwnerID: uuid.New(), AgentID: uuid.New(),
	})
	out, err := tool.Invoke(context.Background(), map[string]any{
		"sandbox_path": "/work/out.csv", "filename": "out.csv",
	})
	if err != nil {
		t.Fatalf("Allow path should succeed; err=%v", err)
	}
	if files.called != 1 {
		t.Errorf("Files.UploadFromSandbox not called: %d", files.called)
	}
	if !strings.Contains(out, "file_x") {
		t.Errorf("output should carry file_id: %s", out)
	}
	if rec.lastReq.Action != "skill:export_file" {
		t.Errorf("action = %q", rec.lastReq.Action)
	}
}

func TestSkillExportFile_AuthzDenyBlocksUpload(t *testing.T) {
	rec := &recordingDecider{want: authz.Deny}
	files := &stubFiles{}
	tool := skillExportFileTool(SkillToolDeps{
		Registry: &skillsreg.Registry{},
		Authz:    rec,
		Files:    files,
		OrgID:    uuid.New(), OwnerID: uuid.New(), AgentID: uuid.New(),
	})
	_, err := tool.Invoke(context.Background(), map[string]any{
		"sandbox_path": "/work/out.csv", "filename": "out.csv",
	})
	if err == nil || !strings.Contains(err.Error(), "authz denied") {
		t.Errorf("Deny should block upload with authz error; got %v", err)
	}
	if files.called != 0 {
		t.Errorf("Files must NOT be called when authz denies; got %d", files.called)
	}
}

func TestSkillReadReference_RejectsPathTraversal(t *testing.T) {
	tool := skillReadReferenceTool(SkillToolDeps{
		Registry: &skillsreg.Registry{}, OrgID: uuid.New(), AgentID: uuid.New(),
	})
	_, err := tool.Invoke(context.Background(), map[string]any{
		"identifier": "x", "path": "../../etc/passwd",
	})
	if err == nil || !strings.Contains(err.Error(), "traversal") {
		t.Errorf("want traversal block, got %v", err)
	}
}

func TestSkillReadReference_RejectsEmptyArgs(t *testing.T) {
	tool := skillReadReferenceTool(SkillToolDeps{
		Registry: &skillsreg.Registry{}, OrgID: uuid.New(), AgentID: uuid.New(),
	})
	_, err := tool.Invoke(context.Background(), map[string]any{})
	if err == nil {
		t.Error("expected error for empty args")
	}
}

func TestSkillActivate_RejectsEmptyIdentifier(t *testing.T) {
	tool := skillActivateTool(SkillToolDeps{
		Registry: &skillsreg.Registry{}, OrgID: uuid.New(), AgentID: uuid.New(),
	})
	_, err := tool.Invoke(context.Background(), map[string]any{"identifier": "  "})
	if err == nil {
		t.Error("whitespace-only identifier should error")
	}
}

func TestSkillPropose_RejectsIncompleteDraft(t *testing.T) {
	tool := skillProposeTool(SkillToolDeps{
		Registry: &skillsreg.Registry{}, OrgID: uuid.New(), OwnerID: uuid.New(), AgentID: uuid.New(),
	})
	cases := []map[string]any{
		{"name": "X", "description": "Y", "body": "Z"},                     // missing identifier
		{"identifier": "x", "description": "Y", "body": "Z"},               // missing name
		{"identifier": "x", "name": "X", "body": "Z"},                      // missing description
		{"identifier": "x", "name": "X", "description": "Y"},               // missing body
		{"identifier": "x", "name": "X", "description": "Y", "body": "  "}, // whitespace body
	}
	for i, c := range cases {
		t.Run(strSubcaseName(i, c), func(t *testing.T) {
			_, err := tool.Invoke(context.Background(), c)
			if err == nil {
				t.Errorf("should reject incomplete draft: %+v", c)
			}
		})
	}
}

func strSubcaseName(i int, c map[string]any) string {
	for _, k := range []string{"identifier", "name", "description", "body"} {
		if _, ok := c[k]; !ok {
			return "missing-" + k
		}
	}
	return "case-" + string(rune('a'+i))
}

func TestStrSlice_ParsesBothShapes(t *testing.T) {
	if got := strSlice([]string{"a", "b"}); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("[]string roundtrip: %v", got)
	}
	if got := strSlice([]any{"x", "y", 42, ""}); len(got) != 2 || got[0] != "x" || got[1] != "y" {
		t.Errorf("[]any filter: %v", got)
	}
	if got := strSlice(nil); got != nil {
		t.Errorf("nil → nil; got %v", got)
	}
}

func TestPermissionsAllow(t *testing.T) {
	if !permissionsAllow([]string{"sandbox.exec"}, "sandbox.exec") {
		t.Error("exact match should pass")
	}
	if permissionsAllow([]string{}, "sandbox.exec") {
		t.Error("empty perms should deny")
	}
	if permissionsAllow([]string{"network.fetch"}, "sandbox.exec") {
		t.Error("non-matching perms should deny")
	}
}

// recordingDecider captures the last Check request so tests can
// verify the marshalled principal + resource shape, while letting
// each test pick its own allow/deny verdict via the Want field.
type recordingDecider struct {
	want      authz.Decision
	wantErr   error
	lastReq   authz.Request
	callCount int
}

func (r *recordingDecider) Check(_ context.Context, req authz.Request) (*authz.Result, error) {
	r.callCount++
	r.lastReq = req
	if r.wantErr != nil {
		return nil, r.wantErr
	}
	return &authz.Result{Decision: r.want, Reason: "test stub"}, nil
}

func TestSkillToolDeps_DeciderFallbackToAlwaysAllow(t *testing.T) {
	// Nil Authz field → decider() returns AlwaysAllow so dev / CLI
	// runs (no Authz service) keep working. Production flips this
	// guarantee by wiring NewHTTP explicitly.
	d := SkillToolDeps{}
	res, err := d.decider().Check(context.Background(), authz.Request{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Decision != authz.Allow {
		t.Errorf("nil Authz should fall back to AlwaysAllow; got %s", res.Decision)
	}
}

func TestSkillExecScript_AuthzAllow(t *testing.T) {
	rec := &recordingDecider{want: authz.Allow}
	tool := skillExecScriptTool(SkillToolDeps{
		Registry: &skillsreg.Registry{},
		Authz:    rec,
		OrgID:    uuid.New(), AgentID: uuid.New(), OwnerID: uuid.New(),
	})
	// We can't drive the tool's full path without a registry hit,
	// but the decider() shortcut is exercised independently. Just
	// verify the tool exists with the new field plumbed through.
	if tool == nil {
		t.Fatal("nil tool")
	}
}

func TestSkillToolDeps_AuthzAllowMarshalsResource(t *testing.T) {
	rec := &recordingDecider{want: authz.Allow}
	d := SkillToolDeps{
		Registry: &skillsreg.Registry{},
		Authz:    rec,
		OrgID:    uuid.MustParse("00000000-0000-0000-0000-000000000001"),
		OwnerID:  uuid.MustParse("00000000-0000-0000-0000-000000000002"),
	}
	owner := uuid.MustParse("00000000-0000-0000-0000-000000000003")
	skill := &skillsreg.Skill{
		ID: "skill_x", OrgID: d.OrgID, OwnerID: &owner,
		Status:      skillsreg.StatusActive,
		Source:      skillsreg.SourceUser,
		Permissions: []string{"sandbox.exec", "network.fetch"},
	}
	ok, _, err := d.authzAllow(context.Background(), "skill:exec_script", skill)
	if err != nil || !ok {
		t.Fatalf("expected allow; got ok=%v err=%v", ok, err)
	}
	if rec.lastReq.Action != "skill:exec_script" {
		t.Errorf("action = %q", rec.lastReq.Action)
	}
	if rec.lastReq.Principal.Type != "User" {
		t.Errorf("principal type = %q", rec.lastReq.Principal.Type)
	}
	attrs := rec.lastReq.Resource.Attributes
	if attrs["status"] != "active" {
		t.Errorf("status attr = %v", attrs["status"])
	}
	if attrs["owner_id"] != owner.String() {
		t.Errorf("owner_id attr = %v", attrs["owner_id"])
	}
	perms, _ := attrs["permissions"].([]any)
	if len(perms) != 2 || perms[0] != "sandbox.exec" {
		t.Errorf("permissions attr = %v", perms)
	}
}

func TestSkillToolDeps_AuthzDenyReturnsReason(t *testing.T) {
	rec := &recordingDecider{want: authz.Deny}
	d := SkillToolDeps{
		Registry: &skillsreg.Registry{}, Authz: rec,
		OrgID: uuid.New(), OwnerID: uuid.New(),
	}
	skill := &skillsreg.Skill{
		ID: "x", OrgID: d.OrgID, Status: skillsreg.StatusActive,
		Source: skillsreg.SourceUser,
	}
	ok, reason, err := d.authzAllow(context.Background(), "skill:exec_script", skill)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("Deny should return false")
	}
	if !strings.Contains(reason, "test stub") {
		t.Errorf("reason should propagate from decider: %q", reason)
	}
}

func TestSkillToolDeps_AuthzErrorFailsClosed(t *testing.T) {
	// An Authz outage must not be treated as Allow. The tool layer
	// surfaces the error to the caller; the agent loop maps it to
	// a tool_result error so the model doesn't proceed.
	rec := &recordingDecider{wantErr: errStubFailure}
	d := SkillToolDeps{
		Registry: &skillsreg.Registry{}, Authz: rec,
		OrgID: uuid.New(), OwnerID: uuid.New(),
	}
	skill := &skillsreg.Skill{ID: "x", OrgID: d.OrgID, Status: skillsreg.StatusActive}
	ok, _, err := d.authzAllow(context.Background(), "skill:exec_script", skill)
	if err == nil {
		t.Fatal("expected error when authz check fails")
	}
	if ok {
		t.Error("error path must NOT return ok=true")
	}
}

var errStubFailure = stubErr("stub authz failure")

type stubErr string

func (e stubErr) Error() string { return string(e) }

func TestSkillListTool_Schema(t *testing.T) {
	// Pin the parameters schema so the LLM-facing surface doesn't
	// silently change. skill.list takes no required args; missing
	// the empty object would confuse some tool-use clients.
	tool := skillListTool(SkillToolDeps{Registry: &skillsreg.Registry{}})
	if tool.Parameters == nil {
		t.Fatal("nil parameters")
	}
	if tool.Parameters["type"] != "object" {
		t.Errorf("type should be object; got %v", tool.Parameters["type"])
	}
	if tool.IsReadOnly != true || tool.Risk != RiskLow {
		t.Errorf("skill.list must be read-only + low-risk")
	}
}

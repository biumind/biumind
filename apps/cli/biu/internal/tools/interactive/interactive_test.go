package interactive

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
	"github.com/biumind/biumind/apps/cli/biu/internal/permissions"
)

func TestEnterAndExitPlanMode(t *testing.T) {
	ctx := permissions.NewContext()
	enter := EnterPlanModeTool{Perms: ctx}
	exit := ExitPlanModeTool{Perms: ctx}

	if _, err := enter.Call(context.Background(), nil, nil); err != nil {
		t.Fatal(err)
	}
	if ctx.Mode() != permissions.ModePlan {
		t.Errorf("EnterPlanMode didn't switch mode")
	}

	out, _ := exit.Call(context.Background(), map[string]any{
		"plan": "1. survey\n2. propose",
	}, nil)
	if out.IsError {
		t.Fatalf("ExitPlanMode failed: %+v", out)
	}
	if ctx.Mode() != permissions.ModeDefault {
		t.Errorf("ExitPlanMode didn't revert mode")
	}
	if !strings.Contains(flatten(out), "propose") {
		t.Errorf("plan body missing")
	}
}

func TestExitPlanModeRequiresPlan(t *testing.T) {
	ctx := permissions.NewContext()
	ctx.EnterPlanMode() // satisfy the "must be in plan mode" guard
	out, _ := ExitPlanModeTool{Perms: ctx}.Call(context.Background(), map[string]any{
		"plan": "  ",
	}, nil)
	if !out.IsError {
		t.Errorf("blank plan must soft-error")
	}
}

func TestExitPlanModeRejectsOutsidePlan(t *testing.T) {
	ctx := permissions.NewContext()
	// Never entered plan mode — ExitPlanMode must refuse so we don't
	// silently switch the session into the prePlanMode fallback.
	out, _ := ExitPlanModeTool{Perms: ctx}.Call(context.Background(), map[string]any{
		"plan": "1. step",
	}, nil)
	if !out.IsError {
		t.Errorf("ExitPlanMode outside plan must soft-error")
	}
	if !strings.Contains(flatten(out), "not currently in plan mode") {
		t.Errorf("error must say why; got: %s", flatten(out))
	}
}

func TestExitPlanModeRestoresPrePlanMode(t *testing.T) {
	ctx := permissions.NewContext()
	ctx.SetMode(permissions.ModeAcceptEdits)
	if _, err := (EnterPlanModeTool{Perms: ctx}).Call(context.Background(), nil, nil); err != nil {
		t.Fatal(err)
	}
	if ctx.Mode() != permissions.ModePlan {
		t.Fatalf("expected plan; got %v", ctx.Mode())
	}

	out, _ := ExitPlanModeTool{Perms: ctx}.Call(context.Background(), map[string]any{
		"plan": "1. step",
	}, nil)
	if out.IsError {
		t.Fatalf("ExitPlanMode failed: %+v", out)
	}
	if ctx.Mode() != permissions.ModeAcceptEdits {
		t.Errorf("expected restore to acceptEdits; got %v", ctx.Mode())
	}
	if !strings.Contains(flatten(out), "acceptEdits") {
		t.Errorf("result should mention restored mode; got: %s", flatten(out))
	}
}

func TestExitPlanModeAllowedPromptsBecomeGrants(t *testing.T) {
	ctx := permissions.NewContext()
	ctx.EnterPlanMode()

	out, _ := ExitPlanModeTool{Perms: ctx}.Call(context.Background(), map[string]any{
		"plan": "do stuff",
		"allowedPrompts": []any{
			map[string]any{"tool": "Bash", "prompt": "go test ./..."},
			map[string]any{"tool": "Bash", "prompt": "go build"},
			// Malformed entries get dropped silently.
			map[string]any{"tool": "Bash"},
			"not-an-object",
		},
	}, nil)
	if out.IsError {
		t.Fatalf("ExitPlanMode failed: %+v", out)
	}
	want := permissions.SessionGrantKey("Bash", map[string]any{"command": "go test ./..."})
	if !ctx.HasGrant(want) {
		t.Errorf("session grant for `go test ./...` missing")
	}
	if !ctx.HasGrant(permissions.SessionGrantKey("Bash", map[string]any{"command": "go build"})) {
		t.Errorf("session grant for `go build` missing")
	}
	if !strings.Contains(flatten(out), "Pre-approved") {
		t.Errorf("result should advertise pre-approvals; got: %s", flatten(out))
	}
}

// Semantic prompts (e.g. "run tests") feed the classifier so the
// runner auto-allows commands that satisfy the description even
// when the literal SessionGrant key doesn't collide.
func TestExitPlanModeStagesSemanticAllowedPrompts(t *testing.T) {
	ctx := permissions.NewContext()
	ctx.EnterPlanMode()

	out, _ := ExitPlanModeTool{Perms: ctx}.Call(context.Background(), map[string]any{
		"plan": "run the test suite",
		"allowedPrompts": []any{
			map[string]any{"tool": "Bash", "prompt": "run tests"},
		},
	}, nil)
	if out.IsError {
		t.Fatalf("ExitPlanMode failed: %+v", out)
	}
	got := ctx.AllowedPrompts()
	if len(got) != 1 || got[0].Tool != "Bash" || got[0].Prompt != "run tests" {
		t.Errorf("AllowedPrompts not staged: %+v", got)
	}
	// The exact-grant cache wouldn't help here — the prompt is a
	// semantic description, not a literal command.
	literalKey := permissions.SessionGrantKey("Bash",
		map[string]any{"command": "go test ./..."})
	if ctx.HasGrant(literalKey) {
		t.Errorf("literal grant should not exist for semantic prompt")
	}
	// Decide should auto-allow the concrete command via the
	// classifier path now that the prompt is staged.
	d, r := permissions.Decide(ctx, permissions.Request{
		Tool: "Bash",
		Args: map[string]any{"command": "go test ./..."},
	})
	if d != permissions.DecideAllow || r.Kind != "allowedPrompt" {
		t.Errorf("decide should allow via allowedPrompt; got %v reason=%+v", d, r)
	}
}

func TestExitPlanModeWritesPlanFile(t *testing.T) {
	ctx := permissions.NewContext()
	ctx.EnterPlanMode()
	dir := t.TempDir()
	tool := ExitPlanModeTool{
		Perms:     ctx,
		PlanStore: NewDiskPlanStore(dir),
		SessionID: "20260301-120000-test",
	}
	out, _ := tool.Call(context.Background(), map[string]any{
		"plan": "## Steps\n1. read\n2. propose",
	}, nil)
	if out.IsError {
		t.Fatalf("ExitPlanMode failed: %+v", out)
	}
	planPath := filepath.Join(dir, "20260301-120000-test.md")
	if _, err := os.Stat(planPath); err != nil {
		t.Fatalf("plan file should exist at %s: %v", planPath, err)
	}
	body, _ := os.ReadFile(planPath)
	if !strings.Contains(string(body), "1. read") {
		t.Errorf("plan body missing in file: %q", body)
	}
	if !strings.Contains(flatten(out), planPath) {
		t.Errorf("tool result should mention path; got: %s", flatten(out))
	}
}

func TestAskUserQuestionWires(t *testing.T) {
	called := 0
	env := &engine.ToolEnv{
		AskUser: func(_ context.Context, q engine.UserQuestion) (engine.UserAnswer, error) {
			called++
			if q.Question == "" || len(q.Options) != 2 {
				t.Errorf("question mis-shaped: %+v", q)
			}
			return engine.UserAnswer{Selected: []int{1}}, nil
		},
	}
	out, _ := AskUserQuestionTool{}.Call(context.Background(), map[string]any{
		"question": "left or right?",
		"options": []any{
			map[string]any{"label": "left", "description": "go left"},
			map[string]any{"label": "right", "description": "go right"},
		},
	}, env)
	if called != 1 {
		t.Fatalf("AskUser wasn't called")
	}
	if !strings.Contains(flatten(out), "right") {
		t.Errorf("answer missing: %s", flatten(out))
	}
}

func TestAskUserQuestionNoUI(t *testing.T) {
	out, _ := AskUserQuestionTool{}.Call(context.Background(), map[string]any{
		"question": "pick", "options": []any{
			map[string]any{"label": "a"}, map[string]any{"label": "b"},
		},
	}, &engine.ToolEnv{})
	if !out.IsError {
		t.Errorf("missing UI must soft-error")
	}
}

func TestCronStoreCRUD(t *testing.T) {
	fired := make(chan CronJob, 1)
	store := NewCronStore(func(j CronJob) { fired <- j })
	defer store.Close()

	createOut, _ := CronCreateTool{Store: store}.Call(context.Background(), map[string]any{
		"cron":   "*/5 * * * *",
		"prompt": "check",
	}, nil)
	if createOut.IsError || !strings.Contains(flatten(createOut), "Cron #") {
		t.Fatalf("create failed: %+v", createOut)
	}

	listOut, _ := CronListTool{Store: store}.Call(context.Background(), nil, nil)
	if !strings.Contains(flatten(listOut), "check") {
		t.Errorf("list missing job: %s", flatten(listOut))
	}
	id := store.List()[0].ID
	delOut, _ := CronDeleteTool{Store: store}.Call(context.Background(), map[string]any{
		"id": id,
	}, nil)
	if delOut.IsError {
		t.Errorf("delete failed: %+v", delOut)
	}
	if len(store.List()) != 0 {
		t.Errorf("delete didn't remove job")
	}
}

func TestDurableCronSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tasks.json")

	// Round 1: create + persist a durable job.
	s1, err := NewDurableCronStore(nil, path)
	if err != nil {
		t.Fatal(err)
	}
	id := s1.Add(&CronJob{
		Cron: "*/5 * * * *", Prompt: "ping",
		Recurring: true, IntervalMin: 5,
		NextFire:  time.Now().Add(time.Hour),
		Durable:   true,
	})
	// In-memory: 1 job.
	if len(s1.List()) != 1 {
		t.Errorf("expected 1 job in memory")
	}
	s1.Close()

	// Round 2: fresh store at the same path should pick it up.
	s2, err := NewDurableCronStore(nil, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	got := s2.List()
	if len(got) != 1 {
		t.Fatalf("expected 1 persisted job, got %d", len(got))
	}
	if got[0].ID != id || got[0].Prompt != "ping" || !got[0].Durable {
		t.Errorf("persisted job mismatch: %+v", got[0])
	}
}

func TestNonDurableJobsNotPersisted(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tasks.json")

	s1, _ := NewDurableCronStore(nil, path)
	s1.Add(&CronJob{
		Cron: "*/5 * * * *", Prompt: "ephemeral",
		Recurring: true, IntervalMin: 5,
		NextFire:  time.Now().Add(time.Hour),
		Durable:   false,
	})
	s1.Close()

	s2, _ := NewDurableCronStore(nil, path)
	defer s2.Close()
	if got := s2.List(); len(got) != 0 {
		t.Errorf("non-durable jobs should not survive: %+v", got)
	}
}

func TestDeletePersists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tasks.json")

	s1, _ := NewDurableCronStore(nil, path)
	id := s1.Add(&CronJob{
		Prompt: "x", Durable: true, IntervalMin: 5, Recurring: true,
		NextFire: time.Now().Add(time.Hour),
	})
	s1.Delete(id)
	s1.Close()

	s2, _ := NewDurableCronStore(nil, path)
	defer s2.Close()
	if got := s2.List(); len(got) != 0 {
		t.Errorf("delete should persist: %+v", got)
	}
}

func TestParseInterval(t *testing.T) {
	cases := map[string]int{
		"*/5 * * * *":  5,
		"*/15 * * * *": 15,
		"30 9 * * *":   60, // specific minute → hourly bucket
		"garbage":      60,
	}
	for in, want := range cases {
		if got := parseInterval(in); got != want {
			t.Errorf("parseInterval(%q)=%d, want %d", in, got, want)
		}
	}
}

type stubNotifier struct {
	got []string
	err error
}

func (s *stubNotifier) Notify(_ context.Context, m string) error {
	s.got = append(s.got, m)
	return s.err
}

func TestPushNotificationWiresBackend(t *testing.T) {
	n := &stubNotifier{}
	env := &engine.ToolEnv{}
	out, _ := PushNotificationTool{Notifier: n}.Call(context.Background(), map[string]any{
		"message": "build done",
	}, env)
	if out.IsError {
		t.Fatalf("notify failed: %+v", out)
	}
	if len(n.got) != 1 || n.got[0] != "build done" {
		t.Errorf("backend not called: %+v", n.got)
	}
}

func TestPushNotificationBackendErrorIsSoft(t *testing.T) {
	n := &stubNotifier{err: errors.New("display unavailable")}
	out, _ := PushNotificationTool{Notifier: n}.Call(context.Background(), map[string]any{
		"message": "x",
	}, &engine.ToolEnv{})
	if !out.IsError {
		t.Errorf("backend error must soft-error")
	}
}

type stubSkill struct{ name string }

func (s stubSkill) Name() string                                  { return s.name }
func (s stubSkill) Run(_ context.Context, args string) (string, error) {
	return "skill[" + s.name + "]: " + args, nil
}

type stubSkills struct{ s map[string]Skill }

func (r stubSkills) Lookup(name string) (Skill, bool) {
	s, ok := r.s[name]
	return s, ok
}

func TestSkillToolHappyPath(t *testing.T) {
	reg := stubSkills{s: map[string]Skill{"hello": stubSkill{name: "hello"}}}
	out, _ := SkillTool{Registry: reg}.Call(context.Background(), map[string]any{
		"skill": "hello", "args": "world",
	}, nil)
	if out.IsError || !strings.Contains(flatten(out), "world") {
		t.Errorf("skill output wrong: %+v", out)
	}
}

func TestSkillToolUnknown(t *testing.T) {
	reg := stubSkills{s: map[string]Skill{}}
	out, _ := SkillTool{Registry: reg}.Call(context.Background(), map[string]any{
		"skill": "missing",
	}, nil)
	if !out.IsError {
		t.Errorf("unknown skill must soft-error")
	}
}

func flatten(p *engine.ToolResultPayload) string {
	out := ""
	for _, b := range p.Content {
		out += b.Text
	}
	return out
}

// ─── AskUserQuestion (batch + multiSelect + Other) ─────────

// stubAsker implements env.AskUser with a scripted answer per call.
// `responses` is consumed in order; passing fewer entries than questions
// returns Cancelled for the rest so callers can verify cancellation
// short-circuits the batch.
type stubAsker struct {
	calls     int
	questions []engine.UserQuestion
	responses []engine.UserAnswer
}

func (s *stubAsker) Ask(_ context.Context, q engine.UserQuestion) (engine.UserAnswer, error) {
	s.questions = append(s.questions, q)
	if s.calls < len(s.responses) {
		ans := s.responses[s.calls]
		s.calls++
		return ans, nil
	}
	s.calls++
	return engine.UserAnswer{Cancelled: true}, nil
}

func newAskEnv(s *stubAsker) *engine.ToolEnv {
	return &engine.ToolEnv{AskUser: s.Ask}
}

func TestAskUserQuestionBatchAllQuestionsAreAsked(t *testing.T) {
	asker := &stubAsker{
		responses: []engine.UserAnswer{
			{Selected: []int{0}},
			{Selected: []int{1}},
		},
	}
	env := newAskEnv(asker)
	out, _ := AskUserQuestionTool{}.Call(context.Background(), map[string]any{
		"questions": []any{
			map[string]any{
				"question": "Which DB?",
				"header":   "DB",
				"options": []any{
					map[string]any{"label": "Postgres", "description": "rdbms"},
					map[string]any{"label": "SQLite", "description": "embedded"},
				},
			},
			map[string]any{
				"question": "Which ORM?",
				"header":   "ORM",
				"options": []any{
					map[string]any{"label": "GORM", "description": "magic"},
					map[string]any{"label": "sqlx", "description": "thin"},
				},
			},
		},
	}, env)
	if out.IsError {
		t.Fatalf("tool failed: %s", flatten(out))
	}
	if len(asker.questions) != 2 {
		t.Errorf("expected 2 questions asked, got %d", len(asker.questions))
	}
	body := flatten(out)
	if !strings.Contains(body, `"Postgres"`) || !strings.Contains(body, `"sqlx"`) {
		t.Errorf("result missing answers: %s", body)
	}
}

func TestAskUserQuestionMultiSelectFormatsAllPicks(t *testing.T) {
	asker := &stubAsker{
		responses: []engine.UserAnswer{{Selected: []int{0, 2}}},
	}
	out, _ := AskUserQuestionTool{}.Call(context.Background(), map[string]any{
		"questions": []any{
			map[string]any{
				"question":    "Pick features",
				"header":      "feat",
				"multiSelect": true,
				"options": []any{
					map[string]any{"label": "auth"},
					map[string]any{"label": "billing"},
					map[string]any{"label": "audit"},
				},
			},
		},
	}, newAskEnv(asker))
	if out.IsError {
		t.Fatalf("tool failed: %s", flatten(out))
	}
	body := flatten(out)
	if !strings.Contains(body, `"auth", "audit"`) {
		t.Errorf("multi-select formatting wrong: %s", body)
	}
}

func TestAskUserQuestionOtherFreeText(t *testing.T) {
	asker := &stubAsker{
		responses: []engine.UserAnswer{{Notes: "use Snowflake"}},
	}
	out, _ := AskUserQuestionTool{}.Call(context.Background(), map[string]any{
		"questions": []any{
			map[string]any{
				"question": "Which warehouse?",
				"header":   "WH",
				"options": []any{
					map[string]any{"label": "Postgres"},
					map[string]any{"label": "BigQuery"},
				},
			},
		},
	}, newAskEnv(asker))
	if out.IsError {
		t.Fatalf("tool failed: %s", flatten(out))
	}
	body := flatten(out)
	if !strings.Contains(body, "free text") || !strings.Contains(body, "Snowflake") {
		t.Errorf("Other path lost free-text answer: %s", body)
	}
}

func TestAskUserQuestionPreviewPropagatesToResult(t *testing.T) {
	asker := &stubAsker{
		responses: []engine.UserAnswer{{Selected: []int{1}}},
	}
	out, _ := AskUserQuestionTool{}.Call(context.Background(), map[string]any{
		"questions": []any{
			map[string]any{
				"question": "Which layout?",
				"header":   "Layout",
				"options": []any{
					map[string]any{"label": "Compact", "description": "tight",
						"preview": "[A][B][C]"},
					map[string]any{"label": "Spaced", "description": "loose",
						"preview": "[ A ]\n[ B ]\n[ C ]"},
				},
			},
		},
	}, newAskEnv(asker))
	if out.IsError {
		t.Fatalf("tool failed: %s", flatten(out))
	}
	body := flatten(out)
	if !strings.Contains(body, "[ A ]") || !strings.Contains(body, "preview of selected option") {
		t.Errorf("preview not surfaced in result: %s", body)
	}
}

func TestAskUserQuestionRejectsDuplicateQuestions(t *testing.T) {
	// Stub answers Q1 happily; the loop should hit the duplicate
	// guard for Q2 before ever calling AskUser the second time.
	asker := &stubAsker{responses: []engine.UserAnswer{{Selected: []int{0}}}}
	out, _ := AskUserQuestionTool{}.Call(context.Background(), map[string]any{
		"questions": []any{
			map[string]any{
				"question": "Same?", "header": "x",
				"options": []any{
					map[string]any{"label": "a"},
					map[string]any{"label": "b"},
				},
			},
			map[string]any{
				"question": "Same?", "header": "x",
				"options": []any{
					map[string]any{"label": "c"},
					map[string]any{"label": "d"},
				},
			},
		},
	}, newAskEnv(asker))
	if !out.IsError {
		t.Errorf("duplicate question text should soft-error")
	}
	if !strings.Contains(flatten(out), "duplicate") {
		t.Errorf("error must say why; got: %s", flatten(out))
	}
	if asker.calls != 1 {
		t.Errorf("Q2 should not have been asked — duplicate caught before AskUser; calls=%d", asker.calls)
	}
}

func TestAskUserQuestionLegacySingleQuestionShape(t *testing.T) {
	// Models trained on biu's pre-batch schema still send a single
	// `question` + `options` at the top level. Tool must accept both.
	asker := &stubAsker{responses: []engine.UserAnswer{{Selected: []int{0}}}}
	out, _ := AskUserQuestionTool{}.Call(context.Background(), map[string]any{
		"question": "Which DB?",
		"header":   "DB",
		"options": []any{
			map[string]any{"label": "Postgres"},
			map[string]any{"label": "SQLite"},
		},
	}, newAskEnv(asker))
	if out.IsError {
		t.Fatalf("legacy shape should work: %s", flatten(out))
	}
	if len(asker.questions) != 1 || asker.questions[0].Question != "Which DB?" {
		t.Errorf("legacy shape lost in translation: %+v", asker.questions)
	}
}

func TestAskUserQuestionCancelStopsBatch(t *testing.T) {
	// User cancels on Q2 → tool short-circuits, doesn't ask Q3.
	asker := &stubAsker{responses: []engine.UserAnswer{
		{Selected: []int{0}},
		{Cancelled: true},
	}}
	out, _ := AskUserQuestionTool{}.Call(context.Background(), map[string]any{
		"questions": []any{
			map[string]any{"question": "Q1", "header": "x",
				"options": []any{map[string]any{"label": "a"}, map[string]any{"label": "b"}}},
			map[string]any{"question": "Q2", "header": "y",
				"options": []any{map[string]any{"label": "c"}, map[string]any{"label": "d"}}},
			map[string]any{"question": "Q3", "header": "z",
				"options": []any{map[string]any{"label": "e"}, map[string]any{"label": "f"}}},
		},
	}, newAskEnv(asker))
	if !out.IsError {
		t.Errorf("cancellation should soft-error")
	}
	if asker.calls != 2 {
		t.Errorf("Q3 should not have been asked after cancel; calls=%d", asker.calls)
	}
}

func TestExitPlanModeSetsCompactAttachment(t *testing.T) {
	ctx := permissions.NewContext()
	ctx.EnterPlanMode()
	out, _ := ExitPlanModeTool{Perms: ctx}.Call(context.Background(), map[string]any{
		"plan": "## Steps\n1. read\n2. propose",
	}, nil)
	if out.IsError {
		t.Fatalf("ExitPlanMode failed: %+v", out)
	}
	got := ctx.PlanAttachment()
	if got == "" {
		t.Errorf("plan attachment should be set after ExitPlanMode")
	}
	if !strings.Contains(got, "1. read") {
		t.Errorf("attachment should carry plan body; got %q", got)
	}
}

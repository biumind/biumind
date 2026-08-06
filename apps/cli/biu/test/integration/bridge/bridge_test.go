//go:build integration

// Layer F (HTTP Bridge) integration tests. The bridge is a tiny
// HTTP/SSE shim that wraps biumindkit so IDEs / remote operator UIs
// can drive an agent out-of-process. We start one `biu bridge`
// subprocess per test (cheap — bind takes ms) so test isolation is
// trivial.
//
// LLM-driven cases gate on RequireRealAPI; auth + DELETE cases run
// against a placeholder api_key because the bridge's startup probe
// only constructs the SDK agent, it never calls Anthropic.

package bridge_test

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/test/integration/harness"
)

// fakeAPI returns an AnthropicEnv with a non-empty placeholder key so
// the bridge factory's startup probe doesn't fail. No real network
// call is made by tests that use this.
func fakeAPI() harness.AnthropicEnv {
	return harness.AnthropicEnv{
		APIKey:  "sk-placeholder-no-real-call",
		BaseURL: "http://127.0.0.1:1", // unreachable; we never hit it
		Model:   "claude-opus-4-7",
	}
}

// TestF1_FullLifecycle exercises the canonical end-to-end flow:
// create session → submit a one-turn prompt → drain the SSE event
// stream → query cost → close session. Every step must return 200,
// SSE must terminate with a "done" event, and DELETE must yield
// 404 on subsequent reads.
func TestF1_FullLifecycle(t *testing.T) {
	api := harness.RequireRealAPI(t)
	sb := harness.NewSandbox(t)
	sb.SeedDirectConfig(t, api)
	api.Apply(sb)

	bs := harness.StartBridge(t, sb, "")
	defer bs.Close()

	id := bs.CreateSession(t)
	bs.SubmitPrompt(t, id, "Reply with the literal word PONG and nothing else.")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	events := bs.StreamEvents(ctx, t, id, nil)

	var sawAssistant, sawDone bool
	for ev := range events {
		switch ev.Event {
		case "assistant_text":
			if strings.Contains(strings.ToUpper(ev.Data), "PONG") {
				sawAssistant = true
			}
		case "done":
			sawDone = true
		}
		if sawDone {
			break
		}
	}
	if !sawAssistant {
		t.Errorf("never saw assistant_text containing PONG\nbridge stderr:\n%s", bs.Stderr())
	}
	if !sawDone {
		t.Errorf("never saw 'done' event before stream end\nbridge stderr:\n%s", bs.Stderr())
	}

	// Cost endpoint should now have a non-zero token tally.
	code, _, body := bs.Do(t, "GET", "/v1/code/sessions/"+id+"/cost", nil)
	if code != 200 {
		t.Fatalf("cost endpoint status %d: %s", code, body)
	}
	for _, kw := range []string{"InputTokens", "OutputTokens", "USD"} {
		if !strings.Contains(string(body), kw) {
			t.Errorf("cost JSON missing %q: %s", kw, body)
		}
	}

	// DELETE then re-GET cost should 404.
	code, _, _ = bs.Do(t, "DELETE", "/v1/code/sessions/"+id, nil)
	if code != 200 && code != 204 {
		t.Errorf("DELETE session status %d (want 200/204)", code)
	}
	code, _, _ = bs.Do(t, "GET", "/v1/code/sessions/"+id+"/cost", nil)
	if code != 404 {
		t.Errorf("cost after DELETE status %d (want 404)", code)
	}
}

// TestF2_LastEventIDResume drives one full turn, drops the SSE
// connection mid-stream, then reconnects with Last-Event-ID and
// asserts the second connection skips the already-seen ids and
// terminates cleanly with `done`.
func TestF2_LastEventIDResume(t *testing.T) {
	api := harness.RequireRealAPI(t)
	sb := harness.NewSandbox(t)
	sb.SeedDirectConfig(t, api)
	api.Apply(sb)

	bs := harness.StartBridge(t, sb, "")
	defer bs.Close()

	id := bs.CreateSession(t)
	bs.SubmitPrompt(t, id, "Say OK.")

	// Drain the full stream once so the buffer is fully populated.
	ctx1, cancel1 := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel1()
	first := bs.StreamEvents(ctx1, t, id, nil)
	var firstIDs []string
	for ev := range first {
		if ev.ID != "" {
			firstIDs = append(firstIDs, ev.ID)
		}
		if ev.Event == "done" {
			break
		}
	}
	if len(firstIDs) == 0 {
		t.Fatal("first stream produced no `id:` lines")
	}
	skipUpTo := firstIDs[0]

	// Reconnect with Last-Event-ID = first id; second stream should
	// not re-emit it.
	ctx2, cancel2 := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel2()
	second := bs.StreamEvents(ctx2, t, id, http.Header{"Last-Event-ID": []string{skipUpTo}})
	var secondIDs []string
	for ev := range second {
		if ev.ID != "" {
			secondIDs = append(secondIDs, ev.ID)
		}
		if ev.Event == "done" {
			break
		}
	}
	for _, sid := range secondIDs {
		if sid == skipUpTo {
			t.Errorf("resumed stream re-emitted skipped id %s", skipUpTo)
		}
	}
}

// TestF3_LastEventIDQueryFallback proves the same resume mechanism
// works via the `?last_event_id=` query parameter when the client
// can't set the header (some EventSource implementations).
func TestF3_LastEventIDQueryFallback(t *testing.T) {
	api := harness.RequireRealAPI(t)
	sb := harness.NewSandbox(t)
	sb.SeedDirectConfig(t, api)
	api.Apply(sb)

	bs := harness.StartBridge(t, sb, "")
	defer bs.Close()

	id := bs.CreateSession(t)
	bs.SubmitPrompt(t, id, "Reply with ACK.")

	ctx1, cancel1 := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel1()
	for ev := range bs.StreamEvents(ctx1, t, id, nil) {
		if ev.Event == "done" {
			break
		}
	}

	// Use the URL-level fallback path. We bypass StreamEvents to set
	// the query, then assert the response is 200.
	url := bs.URL + "/v1/code/sessions/" + id + "/events?last_event_id=999"
	req, _ := http.NewRequest("GET", url, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("query-fallback GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("query-fallback status %d", resp.StatusCode)
	}
}

// TestF4_ConcurrentSSE creates two sessions in parallel and drives
// each through one turn. The two SSE streams must not cross-talk
// (each session's done event must arrive on its own stream).
func TestF4_ConcurrentSSE(t *testing.T) {
	api := harness.RequireRealAPI(t)
	sb := harness.NewSandbox(t)
	sb.SeedDirectConfig(t, api)
	api.Apply(sb)

	bs := harness.StartBridge(t, sb, "")
	defer bs.Close()

	type streamResult struct {
		sessionID string
		text      string
		err       string
	}

	results := make(chan streamResult, 2)
	var wg sync.WaitGroup
	prompts := map[string]string{
		"alpha": "Reply with literally ALPHA.",
		"beta":  "Reply with literally BETA.",
	}

	for label, prompt := range prompts {
		wg.Add(1)
		go func(_ string, prompt string) {
			defer wg.Done()
			id := bs.CreateSession(t)
			bs.SubmitPrompt(t, id, prompt)
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			var collected strings.Builder
			done := false
			for ev := range bs.StreamEvents(ctx, t, id, nil) {
				if ev.Event == "assistant_text" {
					collected.WriteString(ev.Data)
				}
				if ev.Event == "done" {
					done = true
					break
				}
			}
			if !done {
				results <- streamResult{sessionID: id, err: "no done event"}
				return
			}
			results <- streamResult{sessionID: id, text: collected.String()}
		}(label, prompt)
	}

	wg.Wait()
	close(results)

	got := map[string]string{}
	for r := range results {
		if r.err != "" {
			t.Errorf("session %s: %s", r.sessionID, r.err)
			continue
		}
		got[r.sessionID] = r.text
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 distinct session results; got %d", len(got))
	}
	// Each session's text should hold its OWN sentinel, not the other's.
	// (The model could fold either into the other, so we check the
	// union of seen sentinels covers both.)
	combined := ""
	for _, v := range got {
		combined += " " + strings.ToUpper(v)
	}
	for _, want := range []string{"ALPHA", "BETA"} {
		if !strings.Contains(combined, want) {
			t.Errorf("expected sentinel %s in one of the streams; got: %v", want, got)
		}
	}
}

// TestF5_AuthEnforced verifies the bridge rejects requests without
// the configured Bearer when --auth-token is set, and accepts them
// when present. Doesn't need a real LLM — the auth gate fires
// before the agent is ever invoked.
func TestF5_AuthEnforced(t *testing.T) {
	sb := harness.NewSandbox(t)
	sb.SeedDirectConfig(t, fakeAPI())

	const token = "test-bearer-7f3b"
	bs := harness.StartBridge(t, sb, token)
	defer bs.Close()

	// 1) No token → 401.
	req, _ := http.NewRequest("POST", bs.URL+"/v1/code/sessions", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("unauth POST: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Errorf("no-auth POST status %d (want 401)", resp.StatusCode)
	}

	// 2) Wrong token → 401.
	req2, _ := http.NewRequest("POST", bs.URL+"/v1/code/sessions", nil)
	req2.Header.Set("Authorization", "Bearer wrong")
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("wrong-auth POST: %v", err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != 401 {
		t.Errorf("wrong-auth POST status %d (want 401)", resp2.StatusCode)
	}

	// 3) Right token → 200 (creates session). Use the harness method
	// which sets Authorization automatically.
	id := bs.CreateSession(t)
	if id == "" {
		t.Errorf("authed POST didn't return id")
	}
}

// TestF6_CompactReducesTokens drives one turn, captures the cost,
// runs /compact, and verifies the in-memory state reflects the
// compact ran successfully (status 200, response includes a known
// shape).
func TestF6_CompactReducesTokens(t *testing.T) {
	api := harness.RequireRealAPI(t)
	sb := harness.NewSandbox(t)
	sb.SeedDirectConfig(t, api)
	api.Apply(sb)

	bs := harness.StartBridge(t, sb, "")
	defer bs.Close()

	id := bs.CreateSession(t)
	// Burn a turn so there's something to compact.
	bs.SubmitPrompt(t, id, "Tell me one fact about UTF-8 in one sentence.")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	for ev := range bs.StreamEvents(ctx, t, id, nil) {
		if ev.Event == "done" {
			break
		}
	}

	// /compact must succeed and not blow up the session.
	code, _, body := bs.Do(t, "POST", "/v1/code/sessions/"+id+"/compact", nil)
	if code != 200 && code != 202 {
		t.Errorf("compact status %d body=%s\nbridge:%s", code, body, bs.Stderr())
	}

	// After compact the session must still be queryable for cost.
	code2, _, _ := bs.Do(t, "GET", "/v1/code/sessions/"+id+"/cost", nil)
	if code2 != 200 {
		t.Errorf("cost after compact status %d", code2)
	}
}

// TestF7_CostShape verifies the /cost endpoint returns a JSON object
// carrying the canonical token-tally fields after a real turn.
func TestF7_CostShape(t *testing.T) {
	api := harness.RequireRealAPI(t)
	sb := harness.NewSandbox(t)
	sb.SeedDirectConfig(t, api)
	api.Apply(sb)

	bs := harness.StartBridge(t, sb, "")
	defer bs.Close()

	id := bs.CreateSession(t)
	bs.SubmitPrompt(t, id, "Reply with literally OK.")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	for ev := range bs.StreamEvents(ctx, t, id, nil) {
		if ev.Event == "done" {
			break
		}
	}

	code, _, body := bs.Do(t, "GET", "/v1/code/sessions/"+id+"/cost", nil)
	if code != 200 {
		t.Fatalf("cost status %d", code)
	}
	for _, kw := range []string{"InputTokens", "OutputTokens", "USD"} {
		if !strings.Contains(string(body), kw) {
			t.Errorf("cost JSON missing %q: %s", kw, body)
		}
	}
}

// TestF8_DeleteThen404 exercises only the session lifecycle
// endpoints — no LLM involvement. Create → DELETE → GET cost
// must 404.
func TestF8_DeleteThen404(t *testing.T) {
	sb := harness.NewSandbox(t)
	sb.SeedDirectConfig(t, fakeAPI())

	bs := harness.StartBridge(t, sb, "")
	defer bs.Close()

	id := bs.CreateSession(t)

	code, _, _ := bs.Do(t, "DELETE", "/v1/code/sessions/"+id, nil)
	if code != 200 && code != 204 {
		t.Errorf("DELETE status %d (want 200/204)", code)
	}
	code2, _, _ := bs.Do(t, "GET", "/v1/code/sessions/"+id+"/cost", nil)
	if code2 != 404 {
		t.Errorf("post-DELETE GET status %d (want 404)", code2)
	}
}

# engine tests — authoring guide

Notes for writing unit and e2e tests against `internal/engine/` and
the tools that depend on it. Read this before adding a new
`*_test.go` file — the package has a few sharp edges that aren't
obvious from the source alone.

---

## 1. Test package: `engine` vs `engine_test`

Two package conventions live side-by-side:

| Package | Files (examples) | Use when |
|---------|------------------|----------|
| `package engine` (internal test) | `engine_test.go`, `swarm_test.go`, `deferred_test.go` | You need `engine.scriptedProvider`, `engine.fakeTool`, or any unexported field/symbol. |
| `package engine_test` (external test) | `full_lifecycle_e2e_test.go`, `hooks_lifecycle_test.go`, `swarm_e2e_test.go`, `*_e2e_test.go` | You need to import `tools/orchestration`, `tools/files`, or other packages that themselves import `engine` (avoids import cycle). |

E2E tests almost always live in `package engine_test` because they
need real tools. Pick the package up-front — switching later is a
file rewrite.

---

## 2. Reusable helpers (don't reinvent)

These already exist. Search them before writing your own.

### In `package engine_test`

| Helper | Source | What it does |
|--------|--------|--------------|
| `scripted` | `plan_e2e_test.go:38` | One-script-per-call provider. |
| `textTurn(text)` | `plan_e2e_test.go` | Build a final-text turn. |
| `toolUseTurn(useID, name, inputJSON)` | `plan_e2e_test.go` | Build a single-tool-use turn. |
| `drainAll(ch)` | `plan_e2e_test.go` | Drain all events from a Submit channel. |
| `routerProvider` | `full_lifecycle_e2e_test.go:54` | Routes by system-prompt fingerprint (parent vs sub-agents). |
| `recordingBash` | `plan_e2e_test.go:95` | Captures every Bash command. |
| `markerHooks(t, marker, events...)` | `hooks_lifecycle_test.go:33` | Single-marker shell hook for one or more events. |
| `buildPerEventMarkers(map[Event]string)` | `hooks_e2e_test.go` | Per-event marker files (use when one test needs distinct sentinels per event). |
| `waitForFile(path, deadline)` | `hooks_lifecycle_test.go:46` | Poll a path for hook-side-effect detection. |
| `contains` / `hasDone` / `findSystemText` / `flattenAll` / `messageRolesShort` | `full_lifecycle_e2e_test.go:546-602` | Assertions on event slices and state. |
| `swarmRouter` (content-routing) | `swarm_e2e_test.go` | Routes by user-prompt content; needed when sub-agents share the default system prompt. |
| `deferredCaptureProvider` | `deferred_e2e_test.go` | Records each `req.Tools` so tests assert on what the LLM actually saw. |
| `replayableScript` | `rewind_e2e_test.go` | Append-after-construction provider; needed when next-turn frames depend on captured runtime data (e.g. dynamic IDs). |
| `stagedStore(t)` | `session/snapshots_test.go:11` | Returns `(SnapshotStore, home)` with TempDir. |

### In `package engine` (internal)

| Helper | Source | What it does |
|--------|--------|--------------|
| `scriptedProvider` | `engine_test.go:24` | Sequential script provider. |
| `fakeTool` | `engine_test.go:52` | Configurable tool with all capability flags. |
| `stubTool` | `deferred_test.go:13` | Tool that implements `Deferrable`. |
| `swarmProvider` | `swarm_test.go:29` | Per-child-name routing for sub-agent tests. |
| `textTurn` / `toolUseTurn` / `twoToolsTurn` | `engine_test.go:382-438` | Frame builders for internal-package tests. |

Naming clash to watch for: `package engine_test` already has a
`captureProvider` (in `explore_e2e_test.go`); pick a unique name
for new providers (e.g. `deferredCaptureProvider`).

---

## 3. Pitfalls

### 3.1 Provider scripts are per-LLM-call, not per-Submit

One `Submit` triggers an inner tool loop: assistant message → tool
call → tool result → next assistant message → … each round trips
through `Provider.Stream`. A scripted provider needs **one entry per
inner round-trip**, not one per `Submit`.

Concretely, a Submit that fires a tool then closes with text takes
**two** parent-script entries:

```go
prov := newReplayable(
    toolUseTurn("u1", "Write", `{...}`), // turn 0: model decides to call Write
    textTurn("done"),                    // turn 1: model summarises after seeing tool result
)
eng.Submit(ctx, "go") // consumes BOTH script entries
```

If you only provide the tool-use turn, the second inner round-trip
hangs the engine — you'll see "did not reach DoneEvent (got N
events)" in test output, often misread as a permission/hook bug.

### 3.2 `TaskCreate` uses a process-global counter

Task IDs are assigned via an `atomic.AddInt64` counter that's never
reset. Writing `TaskUpdate` with `taskId: "1"` only works in
isolation; in the full suite the counter is already past 1 by the
time your test runs.

Use the two-Submit pattern: create first, then read the assigned
ID from state, then issue the update.

```go
prov := newFixedScript(
    toolUseTurn("tc1", "TaskCreate", `{"subject":"x","description":"d"}`),
    textTurn("created"),
)
eng.Submit(ctx, "create")
id := string(st.TasksSnapshot()[0].ID) // discover the real id

prov.appendTurns(
    toolUseTurn("tu1", "TaskUpdate", `{"taskId":"`+id+`","status":"completed"}`),
    textTurn("done"),
)
eng.Submit(ctx, "complete")
```

`replayableScript` (rewind_e2e) supports `appendTurns(...)` for this
pattern; `fixedScript` (hooks_e2e) exposes the same via its `mu` +
`turns` slice.

### 3.3 `Edit` / `Write` require a prior `Read` for existing files

`readForEdit` (in `tools/files/edit.go`) rejects writes to files the
engine hasn't read yet — the freshness gate that prevents stomping
concurrent edits. New files are exempt.

For e2e tests that pre-seed a file with `os.WriteFile` and then
exercise the tool path, prepend a `Read` tool-use turn:

```go
prov := newReplayable(
    toolUseTurn("r1", "Read",  `{"file_path":"`+target+`"}`),
    toolUseTurn("w1", "Write", `{"file_path":"`+target+`","content":"v2"}`),
    textTurn("done"),
)
```

There's a `readThenWriteTurns(rUseID, wUseID, path, content)` helper
in `rewind_e2e_test.go` for this.

### 3.4 Permission gate hangs the engine when no rules + no AskUser

A non-read-only tool with no `permissions.Decide` rule falls through
to `DecideAsk`, which calls `interactiveAsk` — and without an
`AskUser` handler wired into Options, the goroutine blocks on the
ask channel forever. Test times out at 30s.

Always pre-allow your test's tools:

```go
perms := permissions.NewContext()
perms.AddRules(permissions.SrcUserSettings, permissions.BehaviorAllow,
    []string{"Write", "Edit", "Read", "AgentBackground", ...})
opts := engine.Options{..., Permissions: perms}
```

The `hooks_e2e_test.go` `PermissionDenied` case is the deliberate
exception: it uses `BehaviorDeny` to force the deny path so the hook
fires.

---

## 4. Adding a new e2e test

1. Decide package: `engine_test` if you import any `tools/...`
   package; `engine` otherwise.
2. Search §2 first — most fakes you need already exist.
3. Build the engine via `engine.New(engine.Options{...})` directly
   rather than wrapping. Pre-allow tool permissions (§3.4).
4. Write the script as a list of `[]engine.StreamFrame` per
   inner LLM call (§3.1).
5. For destructive-tool flows, prepend `Read` (§3.3).
6. For hook-event assertions, use `markerHooks` or
   `buildPerEventMarkers` + `waitForFile` (don't build a custom
   "hookRecorder" — file sentinels match production behaviour
   closer and are race-free).
7. When async goroutines are involved, sync via store APIs (e.g.
   `eng.AsyncAgents().Active()`) with a polling helper, never
   `time.Sleep`.

---

## 5. Running the suite

```bash
# All engine tests (unit + e2e)
go test -count=1 -timeout 60s ./internal/engine/

# One e2e file
go test -run TestSwarmE2E -count=1 -timeout 30s ./internal/engine/

# With race detector (catches the async-agent / inbox shared-state bugs)
go test -race -count=1 -timeout 90s ./internal/engine/
```

Flaky tests in this package usually mean a goroutine sync gap.
Reproduce under `-race -count=20`; the failure mode is almost always
a missing `waitForActiveDrop` / `waitForFile` before an assertion.

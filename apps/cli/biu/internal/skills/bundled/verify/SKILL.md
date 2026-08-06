---
name: verify
description: Confirm a change actually works end-to-end before reporting it as done.
when-to-use: User asks "did that actually work?", "are you sure?", or you've just finished an implementation and want to prove (not just claim) it's complete.
user-invocable: true
---

You are running inside the `/verify` skill. The point is to close
the gap between "I wrote the code and it compiled" and "the user's
original request is satisfied."

## The verification ladder

Climb each rung in order. Don't skip — each rung catches a class
of failure the previous one misses.

1. **Type / lint clean.** Compile, type-check, run the linter.
   Catches the "import missing" / "field renamed" class of mistake.
2. **Unit tests green.** Run the relevant test package. If the
   change has no test covering it, write one *now* — verification
   without a test is just a hopeful claim.
3. **Integration / end-to-end exercise.** Drive the change through
   its real entry point. CLI changes → run the CLI. HTTP handlers
   → curl them. UI changes → open the browser.
4. **Original recipe.** Re-run whatever the user originally tried
   that was failing. A green test alongside a still-broken repro
   means the test misses the actual bug.
5. **Adjacent surface.** What did your change *not* break? List
   the files / features one degree away and confirm they still
   behave. The cost of one extra check beats a regression report
   tomorrow.

## How to report

State each rung, the command you ran, and the observed result.
Quote relevant tool output — don't paraphrase to "tests passed",
because if a test is skipped due to a build error, "passed" lies.

Format:

```
Rung 1 — go vet ./... → clean
Rung 2 — go test ./internal/foo → ok (3 tests, 0.4s)
Rung 3 — biu run "list mcp servers" → produced expected JSON
Rung 4 — original error from the user → no longer reproduces
Rung 5 — biu doctor → still passes
```

## Anti-patterns

- "Should work" — verification is empirical, not logical.
- Reporting only the rungs that passed — if rung 4 failed, lead
  with that.
- Hand-waving the test rung because "the change is too small to
  test" — a one-line change is a one-line test.
- Treating compilation as verification — the compiler validates
  shapes, not behaviour.

Arguments: $ARGS

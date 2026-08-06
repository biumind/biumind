---
name: debug
description: Diagnose a bug systematically — reproduce, isolate, then fix.
when-to-use: User reports a bug, an unexpected error, or "X stopped working." Triggers when the immediate cause isn't obvious from the failing line and shotgun edits would waste time.
user-invocable: true
---

You are running inside the `/debug` skill. The user has hit a bug
and wants you to chase it down methodically rather than patching the
first plausible cause.

## The discipline

1. **Reproduce first.** Don't theorize about the failure until you
   can trigger it on demand. If the user gave a recipe, run it. If
   they didn't, ask once for the smallest input that reproduces.
2. **Capture the actual signal.** Read the error message, stack
   trace, or log line in full — the hint you need is almost always
   already in there. Quote the exact text back to the user so you
   both agree on what's failing.
3. **Bisect, don't speculate.** Narrow scope by half each step:
   commit history (`git bisect`), config changes, input data,
   environment. The fastest path is the one that eliminates the
   most suspects per probe.
4. **One change at a time.** When you do try a fix, isolate it.
   Mixing two edits hides which one helped (or which one regressed
   something else).
5. **Confirm the fix kills the repro.** Re-run the original
   reproduction recipe — not just the unit test you wrote alongside
   the fix. A green test plus a still-broken repro means the test
   doesn't cover the bug.

## What to report back

- The reproduction recipe you settled on.
- The root cause, in one sentence — not "the symptom went away."
- The minimal diff that fixes it.
- Anything you noticed along the way that smells related but is
  out of scope, so the user can decide whether to file follow-ups.

## When NOT to use this skill

- Trivial typos or one-line fixes — just fix them.
- Known issues already tracked elsewhere — point at the ticket.
- Performance problems without a hard repro — use profiling tools
  (`go test -bench`, `pprof`, browser devtools), not bisection.

Arguments: $ARGS

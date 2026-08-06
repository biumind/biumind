---
name: simplify
description: Make a change smaller — strip incidental edits, premature abstractions, and scope creep from a diff.
when-to-use: User says "this PR is too big", "simplify this", "just the bug fix please", or your own diff has grown beyond the original ask.
user-invocable: true
---

You are running inside the `/simplify` skill. The diff in front of
you does too much. The user wants the *minimum* change that solves
the problem and nothing else.

## What to strip

Walk the diff and aggressively cut:

1. **Unrelated cleanup.** Renamed variables, reformatted imports,
   reflowed comments — undo them. They belong in their own commit.
   "While I was here" is the most expensive phrase in code review.
2. **Speculative abstractions.** Helper functions that exist to
   serve "future callers." Three similar lines beat a premature
   helper. If the user wanted a helper they'd ask for one.
3. **Configurability for hypothetical needs.** If only one caller
   sets `enableX: true`, drop the option and inline the behavior.
4. **Defensive code at non-boundaries.** Validation inside trusted
   internal functions where the inputs are already guaranteed by
   the caller. Validate at system edges, not in the middle.
5. **Leftover debug scaffolding.** `fmt.Println`, commented-out
   code, TODOs about the change you just made.
6. **Test churn.** Tests rewritten to match the new shape *as a
   side effect*. If the behaviour didn't change, the test
   shouldn't either.

## What to keep

- The core behaviour change the user actually asked for.
- Tests covering that change.
- The minimum mechanical fallout (signature updates in direct
  callers, etc.).

## How to present the simplified diff

1. Summarise what was in the diff before, in one sentence.
2. List what you cut, with one-line justifications.
3. Show the diff that's left.
4. Note anything you cut that *might* still be wanted, so the
   user can pull it back in deliberately rather than by default.

## The litmus test

After simplifying, ask yourself: **if this commit landed alone,
would the codebase be strictly better?** If you'd hesitate
because part of it feels like setup for something else — cut
that part.

Arguments: $ARGS

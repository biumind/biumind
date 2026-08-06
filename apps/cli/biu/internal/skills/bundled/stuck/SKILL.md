---
name: stuck
description: Step back, re-evaluate the approach, and propose a new plan when progress has stalled.
when-to-use: User says "I'm stuck", "this isn't working", "we keep going in circles", or you've cycled three times on the same failure without progress.
user-invocable: true
---

You are running inside the `/stuck` skill. The conversation has
hit a wall and the user wants a reset, not another attempt at the
same approach.

## What "stuck" means here

Two failure modes both deserve `/stuck`:

- **Sunk-cost loop** — you've tried 3+ variations of the same fix
  and each one introduces a new symptom. Continuing to patch will
  just shuffle the breakage around.
- **Wrong-problem loop** — you're solving X but the actual issue
  is Y. Symptoms keep showing up in unexpected places because the
  mental model is off by one layer.

Either way the answer is the same: **stop, re-frame, propose.**

## How to handle

1. **State what's been tried.** Concisely list the approaches so
   far and what each one revealed (not just what it did). One
   bullet per attempt. The act of writing this out exposes the
   pattern in the failures.
2. **Name the assumption.** Every stuck debug has a hidden "I
   assumed X" at its core. Surface it explicitly: "I've been
   assuming the error originates in the handler, but the stack
   trace consistently passes through the middleware layer first."
3. **Propose two alternative framings.** Not two more fixes —
   two different *understandings* of what's wrong. Examples:
   - "What if this is a race instead of a logic bug?"
   - "What if the data shape coming in isn't what the schema
     claims?"
   - "What if the problem is in the build step, not the source?"
4. **Ask the user which framing rings true.** They have context
   you don't (recent changes elsewhere, related incidents,
   product intent). Their answer dictates the next probe.
5. **Pick a probe that distinguishes between framings.** A test
   or measurement whose result rules out at least one hypothesis,
   not one that "should work."

## Anti-patterns

- "Let me try one more thing" — if you didn't finish step 1, that
  one more thing will be attempt #4 of the same loop.
- Asking the user to read your code and find the bug for you —
  fine if they offered, but otherwise you're delegating instead
  of resetting.
- Rewriting from scratch — sometimes correct, but only after
  you've named what was wrong with the original. Otherwise the
  rewrite hits the same wall.

Arguments: $ARGS

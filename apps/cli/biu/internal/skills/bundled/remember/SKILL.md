---
name: remember
description: Save a fact, preference, or piece of context to durable memory so future sessions have it.
when-to-use: User says "remember this", "save that", "next time", or shares a non-obvious preference / project fact you'd otherwise have to re-learn.
user-invocable: true
---

You are running inside the `/remember` skill. The user is asking
you to commit something to persistent memory across sessions —
not just acknowledge it in this conversation.

## What's worth saving

Memory is small and durable; treat it like a Post-it on the
monitor, not a notebook. Save things that:

- Will still be true next month.
- Aren't already obvious from the code or git history.
- Change how you'd approach future work for this user.

Good candidates:

- **User profile** — role, primary stack, level of expertise in
  the domain you're discussing.
- **Strong preferences** — "always run gofmt before commit",
  "prefer table-driven tests", "I hate emoji in commit messages."
- **Project facts** — non-obvious why-decisions ("we don't use
  WebSockets because of corp proxy", "auth lives in service X
  because legal mandated it").
- **External pointers** — where bugs are tracked, which dashboard
  is canonical, which Slack channel owns what.

## What's NOT worth saving

- Anything derivable from `git log`, `README`, or `CLAUDE.md`.
- The current state of in-progress work (use tasks/plan instead).
- Step-by-step recipes for one-off fixes — the fix itself is
  in the commit.
- Sensitive tokens / credentials / personal data.

## How to save

1. Decide which **type** the memory is: user, feedback,
   project, or reference. (See the auto-memory system prompt
   for definitions.)
2. Write a one-line description that names what's specific
   about it — future-you will scan descriptions to decide
   relevance.
3. Include the **why** alongside the rule. Knowing the
   motivation lets you handle edge cases later instead of
   blindly applying the rule.
4. Update an existing memory file rather than creating a
   duplicate. Check first.
5. Add a one-line pointer to `MEMORY.md` so the index can
   surface the entry. Don't paste full content into the index.

## How to confirm

After saving, briefly tell the user *what* you saved and *where*
(file name). One sentence. They might want to correct or refine
it before it solidifies.

Arguments: $ARGS

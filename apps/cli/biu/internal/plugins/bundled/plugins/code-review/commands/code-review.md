---
description: Review code changes — defaults to the current branch's diff vs main, or pass a PR number / sha range
---
Review the code changes specified in $ARGUMENTS (or, when empty, the current branch's diff against the main branch).

## How to scope the review

If $ARGUMENTS is empty:
- Use Bash to find the main branch (`git rev-parse --abbrev-ref origin/HEAD` falls back to `main` then `master`).
- Use `git diff <main>...HEAD --stat` to list changed files; `git diff <main>...HEAD` for the full diff.

If $ARGUMENTS looks like a PR number (`#123` or `123`):
- Use `gh pr view <n> --json title,body,files,additions,deletions` for the metadata.
- Use `gh pr diff <n>` for the diff.

If $ARGUMENTS looks like a sha range (`<sha>..<sha>`) or a single sha:
- Use `git diff <range>` or `git show <sha>`.

If neither matches, treat the whole argument as a free-text scope description and ask one clarifying question before proceeding.

## What to look for

You are reviewing for **defects**, not style. Flag only issues where you can articulate a concrete failure mode or a violation of the codebase's stated conventions. In priority order:

1. **Will-it-fail bugs** — null deref, off-by-one, race, resource leak, wrong API contract, broken error path. Cite the line and explain the failure scenario.
2. **Hidden state changes** — silent swallowed errors, mutation of caller-owned data, side effects in functions named like queries.
3. **CLAUDE.md / BIUMIND.md violations** — find every CLAUDE.md or BIUMIND.md file that covers a changed path (the changed file's directory and every parent up to the repo root). Quote the rule that was violated. Do NOT flag rules that don't scope to this file.
4. **Public API regressions** — exported function signature changes, struct field removals, behavior changes to documented contracts.
5. **Security** — user input flowing to shell / SQL / HTTP without sanitization; secrets logged; auth bypassed.

Do NOT flag:
- Style preferences (naming, formatting, comment count) unless CLAUDE.md explicitly requires them.
- Things a linter / formatter / type-checker catches.
- "This could be more general" / "this could be DRY-er" / "consider extracting…" — refactor opinions are not defect reports.
- Things that depend on inputs you can't see in the diff (potential nil only at the call site that's outside the diff).
- Pre-existing issues outside the diff scope.

When uncertain, do NOT flag. False positives erode trust faster than missed bugs.

## Method

1. **Read the diff first**, no exploration. Form a hypothesis about what the change is trying to do, based on diff text + commit messages alone.
2. **Confirm the hypothesis** by reading the changed files in full (Read), not just the hunks. Diff context is usually too thin to judge correctness.
3. **Read CLAUDE.md / BIUMIND.md** at every directory level that contains a changed file. These are the rules you cite; not your own preferences.
4. **Spawn parallel sub-agents** via the Agent tool when the diff spans clearly separable concerns (frontend vs backend vs migration). Single small diffs don't need parallelism.
5. **For each candidate issue**: state the file:line, the concrete failure scenario, and (if a fix is small + unambiguous) a suggested patch. Otherwise just describe the fix shape.

## Output format

End with a verdict block:

```
VERDICT: APPROVE | REQUEST CHANGES | NEEDS DISCUSSION
HIGH-CONFIDENCE ISSUES: <count>
LOWER-CONFIDENCE FLAGS: <count>
```

`APPROVE` is allowed and welcomed when the diff is clean — over-flagging trivial things is failure mode #1 of automated review.

If $ARGUMENTS contained `--comment` and there's a PR number, use `gh pr review <n>` to post inline comments for the high-confidence issues. Skip the lower-confidence flags from the comment thread (they go in your local output only). Posting noisy inline comments is worse than posting none.

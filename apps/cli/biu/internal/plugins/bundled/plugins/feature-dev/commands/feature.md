---
description: End-to-end feature workflow — discover, plan, implement, verify
---
Implement the feature described in $ARGUMENTS, broken into four stages so each step is reviewable.

## Stage 1 — Discover (read-only)

Goal: understand what already exists. Do not write code yet.

- Search for existing implementations of similar functionality (Grep / Glob).
- Read the modules that will be touched (Read).
- Identify integration points: where does this feature plug in? Is there a registry / dispatcher / config layer to extend?
- Surface any constraints from CLAUDE.md / BIUMIND.md in the directories you'll be editing.

End this stage with a one-paragraph summary of what you found and a list of files you expect to modify.

## Stage 2 — Plan

Use the **Plan agent** (via the Agent tool with `subagent_type: Plan`) to design the implementation. Pass the discovery summary + the feature description as the agent's task.

When the Plan agent returns, review the plan critically:
- Does it actually solve the user's request, or did it drift?
- Does it touch only the necessary files? (More files = more review surface = more risk.)
- Does it include test changes alongside the implementation, or is testing an afterthought?

If the plan is solid, surface it via ExitPlanMode and wait for the user's approval before implementing. If you have material doubts, say so and propose a different approach BEFORE asking for approval.

## Stage 3 — Implement

After the user approves:

- Make the changes described in the plan, **and only those changes**. No surrounding cleanup, no opportunistic refactors, no extra abstractions.
- Add or update tests in the same commit as the implementation. Tests should fail without the change and pass with it.
- Keep diffs minimal — three similar lines beats a premature abstraction. The user can refactor later if they want.
- If you discover the plan was wrong mid-implementation, stop and surface it. Don't silently rewrite the plan in code.

## Stage 4 — Verify

- Run the project's test command (look for `Makefile`, `Taskfile.yml`, `package.json` `scripts`, or a CLAUDE.md `test:` line).
- Run the type-checker / linter if the project has one.
- For UI changes: actually exercise the feature in a running dev server. Type-checks alone don't validate UX.
- Spawn the **Verify agent** (Agent tool with `subagent_type: Verify`) on the implementation. Pass the plan and the diff. Its job is to find what your read-only review missed by actually running things.
- If the Verify agent reports `VERDICT: FAIL` or `PARTIAL`, address the findings before reporting completion.

## Output

End with a brief status:
- What changed (files + test count)
- What was verified (commands run, sub-agents consulted)
- What's left for the user (if any) — e.g. "smoke-test in production environment", "update docs site"

Do **not** commit unless the user explicitly asked for that. Use `/commit` (commit-helper plugin) as the natural next step.

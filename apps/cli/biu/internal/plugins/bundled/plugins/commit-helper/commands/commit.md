---
description: Inspect staged + unstaged diffs, draft a conventional commit message, then commit (no push)
---
Build a conventional-commits message that accurately describes the changes ready to commit, then create the commit. Do **not** push.

## Steps (run in order)

1. **Survey state**, in parallel:
   - `git status --porcelain` (untracked + modified)
   - `git diff --staged` (what will commit)
   - `git diff` (what won't commit yet)
   - `git log --oneline -5` (existing commit-message style in this repo)

2. **Decide what to stage.** If `git diff --staged` is empty:
   - Look at `git diff` and propose a logical commit boundary (one feature, one fix, one refactor — not a "wip" dump).
   - Stage only the files that match. Avoid `git add -A` / `git add .` — they pull in `.env`, `node_modules/` rebuilds, lockfile churn the user didn't intend.
   - If files clearly belong to multiple commits, stage just the first batch and note in your final response that more commits are needed.

3. **Refuse to commit secrets.** Before composing the message, scan staged diffs for: `*.env`, `*credentials*.json`, anything matching `(api[_-]?key|secret|password|token)\s*[:=]\s*['"]([A-Za-z0-9+/=]{16,})['"]`. If matched, abort and surface what was found — do not commit.

4. **Compose the message.** Conventional Commits 1.0.0:
   ```
   <type>(<scope>): <subject ≤ 72 chars, imperative mood>

   <optional body — wrap at 72; explain WHY, not WHAT>

   <optional footer — BREAKING CHANGE: …, Refs: #123>
   ```

   Choose `<type>` from the actual change shape:
   - `feat` — new behaviour visible to a caller / user
   - `fix` — bug fix
   - `refactor` — internal restructure with no behaviour change
   - `perf` — performance improvement
   - `docs` — documentation only
   - `test` — test code only
   - `chore` — build / tooling / dependency bumps
   - `ci` — CI config / pipeline only

   `<scope>` follows the recent commits' convention. Look at `git log --oneline -20` and copy the scope shape (e.g. `biu/plugins`, `runtime/agent`, `client/skills`).

   The subject should be **what changed** in active voice. Body explains **why** when the why isn't obvious.

5. **Commit** via `git commit -m`. Use a HEREDOC if the message has multiple lines:
   ```
   git commit -m "$(cat <<'EOF'
   feat(scope): subject
   
   body line 1
   body line 2
   EOF
   )"
   ```

6. **Verify** with `git status` after the commit completes.

## Hard rules

- Never `git push` from this command. Pushing is a separate decision. Tell the user `biu push` (or `git push`) is the next step if they want it.
- Never `git commit --amend` unless the user typed "amend". Amending in autopilot is a footgun.
- Never use `--no-verify` to bypass hooks. If a pre-commit hook fails, fix the underlying issue and re-run, or report what failed.
- Don't co-author yourself unless the user has stated they want a co-author trailer in this repo.

## Output

Print the commit hash + first line of the message after success. If you staged anything that wasn't already staged, list the files. If you noticed staged changes that should be a separate commit, mention it as a follow-up suggestion.

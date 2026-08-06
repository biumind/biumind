# hookify

Extracts user-stated **"don't do X"** rules from `.local.md` files in the current project (and `~/.biumind/.local.md` for user-global rules), then surfaces them at two checkpoints:

- **UserPromptSubmit** — appends matched rules to the prompt as `additionalContext`, so the model sees them at the start of every turn.
- **PreToolUse** — when a tool call's input contains a phrase the rule warned against, blocks the call with an explanation.

## Rule format

A `.local.md` file under your project root or home directory:

```markdown
# project rules

- don't run `bun install` — use pnpm
- never push to main directly
- avoid `git rebase -i` in shared branches
- DO NOT modify generated files under `proto/gen/`
```

Lines starting with `- ` (or `* `) and containing one of `don't`, `do not`, `never`, `avoid` are extracted as rules. The rule body (text after the trigger word) becomes the matcher.

## Why Go-native

biu implements the rule matching as Go internal handlers via the `type: "internal"` hook mechanism — no Python dependency, no subprocess fork per tool call, microsecond dispatch instead of milliseconds.

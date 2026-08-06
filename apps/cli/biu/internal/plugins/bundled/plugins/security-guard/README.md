# security-guard

PreToolUse guard that blocks two specific failure modes:

1. **Edits to credential stores** — `Edit` / `Write` targeting `~/.ssh`, `~/.aws`, `~/.gnupg`, or any `.env*` file. These are almost always wrong calls — the model was probably grepping for config and meant Read instead.

2. **Secrets being written into source files** — `Edit` / `Write` whose new content contains a high-entropy string after `api_key=` / `password=` / `token=` / `secret=`. Catches accidental hardcodes that would commit a live key.

The block is informative, not silent — the model receives the rule it tripped and can re-plan (move the secret to env, ask the user, abort).

## What this is NOT

- Not a sandbox. The biu sandbox layer (settings.json `sandbox` block) is the real defense; this plugin layers on top to catch *intent*-level mistakes the sandbox can't reason about.
- Not a secret scanner for the whole repo. Only inspects in-flight tool calls.
- Not a substitute for the destructive-command warnings biu's BashTool already ships (P20.32/P20.34) — those cover `git push --force`, `rm -rf /`, etc. security-guard covers credential leakage specifically.

## Tuning

The path block-list and the secret regex are baked into the handler. To extend, fork the plugin (`biu plugin install <forked>`) — installation order means a forked plugin overrides the bundled one of the same name only if you also `biu plugin disable security-guard` on the bundled version.

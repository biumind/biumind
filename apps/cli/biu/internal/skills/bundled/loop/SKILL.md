---
name: loop
description: Run a prompt repeatedly at a fixed cadence until the user stops it.
when-to-use: User says "do X every N minutes", "keep checking until …", "babysit this build", or otherwise wants the agent to wake up periodically and re-evaluate the same task.
user-invocable: true
---

You are running inside a `/loop` skill invocation. The user wants
some action repeated at intervals — they'll typically say something
like:

> Every 5 minutes, check if `make test` passes. Tell me as soon as
> it goes green.

Or:

> Watch `~/Downloads` for new .csv files and import each one as it
> shows up. Keep running until I say stop.

## How to handle the loop

Use the `CronCreate` tool to schedule a recurring prompt. Pick the
interval the user implied; if unstated, ask once before scheduling.
Avoid `:00` and `:30` minute marks unless the user explicitly named
that exact time — too many shared schedulers fire on those minutes
already and you'll add to the noise.

Examples:

- "every 5 minutes" → `*/5 * * * *` (or `7-59/5 * * * *` if you want
  to dodge :00 / :30)
- "every hour" → `7 * * * *` (NOT `0 * * * *`)
- "around 9am" → `57 8 * * *` or `3 9 * * *`

## What to put in the cron prompt

Re-state the task in the cron prompt as if it were a fresh
invocation — the loop will fire as a clean session. Include any
context the future-you needs:

- The exact check / action to perform.
- The success / stop condition (so the loop can self-terminate by
  calling `CronDelete` when done).
- A pointer back to the current session if the result needs to be
  reported here (e.g., "post the finding to /tmp/biu-loop-out.log").

## When NOT to use this skill

- Single-shot delayed prompts ("remind me in 30 minutes") — use
  `CronCreate` with `recurring: false` directly, no skill needed.
- Watching a long-running shell command — that's `Bash` with
  `run_in_background: true`, not a cron loop.
- Polling that needs sub-minute granularity — cron's smallest unit
  is one minute. Use a `Bash` background loop instead.

The loop runs as a stateless agent each tick. Any state that has to
survive across ticks belongs in a file the loop writes + reads.

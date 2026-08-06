# ralph-loop

A self-driving loop pattern: the Stop hook re-submits the goal prompt unless a sentinel file (`ralph.done`) exists in the project root, letting the agent grind on a long-running task without per-iteration user nudging.

## Usage

1. Create `ralph.goal` in your project root with the prompt you want re-fired:

   ```
   Continue the migration. Pick the next file under src/legacy/ that
   hasn't been touched, port it, run the tests for that module, then
   stop. When src/legacy/ is empty, write 'done' to ralph.done.
   ```

2. Start a session and ask the agent to begin: `read ralph.goal and start`.

3. Each time the agent finishes a turn, this Stop hook resubmits the contents of `ralph.goal` as the next user message.

4. The loop terminates when the agent (or you) creates a file named `ralph.done` in the cwd. The hook checks for it before re-submitting and short-circuits.

## Why use this

- **Long migrations** where each step is bounded but the total list isn't.
- **Repetitive batch jobs** that need an LLM judgement per item but a deterministic stopping condition.
- **Self-healing tasks** that need "try → stop → check → retry" cycles.

## Why **not** to use this

- Anything that requires user choice mid-loop. The hook fires automatically; no chance to intervene.
- Tasks where each iteration is expensive and bugs are silent. ralph-loop will burn through tokens without you noticing.
- Tasks with no terminating condition. The sentinel file is the kill switch. If you can't articulate when "done" looks like, don't loop.

## Stopping the loop manually

Three options:
- `touch ralph.done` from another terminal
- `Ctrl-C` in the biu REPL (single press exits the loop, double-press exits biu)
- `biu plugin disable ralph-loop` — persists across sessions, restart biu to apply

## Why Go-native

biu implements the loop logic as a Go internal handler — no shell script, no mutation of user settings beyond installing the plugin.

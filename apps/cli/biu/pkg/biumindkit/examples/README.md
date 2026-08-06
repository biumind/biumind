# biumindkit examples

Runnable Go programs that exercise the public SDK surface. Each
directory is a standalone `main` package — clone the repo, set
`ANTHROPIC_API_KEY`, and run.

| Directory | Pattern shown |
|-----------|---------------|
| [`headless/`](headless/) | One-shot prompt → final assistant text. CI-friendly. |
| [`streaming/`](streaming/) | Drain the event channel, print every event type. Foundation for IDE plugins / progress UIs. |
| [`customtool/`](customtool/) | Register a custom `Tool` via `NewTool` / `ToolDef`. |
| [`policy/`](policy/) | Custom permission policy that audits every decision. |

Run from the repo root:

```sh
ANTHROPIC_API_KEY=sk-ant-… go run ./pkg/biumindkit/examples/streaming \
    "what does main.go do?"
```

Each program reads:

- `ANTHROPIC_API_KEY` — required
- `ANTHROPIC_MODEL` — optional, defaults to `claude-sonnet-4-6`

For inline godoc-rendered snippets (the kind that show up on
[pkg.go.dev](https://pkg.go.dev/github.com/biumind/biumind/apps/cli/biu/pkg/biumindkit)
under "Examples"), see `pkg/biumindkit/example_test.go`.

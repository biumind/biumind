---
name: with-paths
description: Auto-attach demo — body folds into the system prompt when the cwd touches a Go file.
when-to-use: Editing or reviewing Go services or CLI tools.
paths: ["**/*.go", "services/**", "apps/cli/**"]
---

# Go-style review checklist

When reviewing or modifying Go in this project, confirm before completing:

1. Errors wrapped with `%w` so callers can `errors.Is` upstream.
2. Tests pass `go test ./... -race`.
3. New exported identifiers carry godoc comments.
4. No `panic` outside `main` / fatal init paths.

Argument forwarded by the slash invocation: `$ARGS`.

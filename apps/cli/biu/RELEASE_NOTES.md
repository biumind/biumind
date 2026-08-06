# biu CLI Release Notes

Embedded into the binary at build time (`/release-notes` slash).
Keep in sync with `internal/repl/release_notes_embed.md`:

```sh
cp apps/cli/biu/RELEASE_NOTES.md apps/cli/biu/internal/repl/release_notes_embed.md
```

## v0.1.0 — 2026-08-05

Initial public release.

- Agent REPL loop: streaming turns, permission modes, slash commands.
- `biu bridge`: local HTTP + SSE bridge for IDE integration
  (see `extensions/vscode`).
- Shared kernel with the BiuMind server (`biumindkit`).
- Multi-platform binaries: darwin/linux × amd64/arm64 via GoReleaser.

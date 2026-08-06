# BiuMind VS Code extension

Drive the [biu CLI](https://github.com/biumind/biumind) agent loop
from VS Code. The extension auto-spawns `biu bridge` on a random
local port, generates a per-session auth token, and renders the
streaming response in a webview.

## Status

Proof-of-concept. Internal install only — not published to the
marketplace yet.

## Install (dev)

```sh
cd extensions/vscode
npm install
npm run compile
# Open in VS Code: F5 launches an Extension Development Host with
# the extension loaded.
```

Or build a `.vsix` and install it manually:

```sh
npx @vscode/vsce package --no-yarn -o biu.vsix
code --install-extension biu.vsix
```

## Setup

1. Make sure `biu` is on your `PATH` (`biu doctor` should be all green).
2. Reload VS Code. The extension auto-starts the bridge on activation.
3. Run command palette → **BiuMind: Send Prompt…** to start chatting.
4. Or select code → right-click → **BiuMind: Send Selection to Agent**.

## Settings

| Key | Default | Effect |
|-----|---------|--------|
| `biu.binaryPath` | `biu` | Path to the binary (override if not on PATH). |
| `biu.bridgePort` | `0` | TCP port; `0` picks free. |
| `biu.autoStart` | `true` | Spawn `biu bridge` on activation. |
| `biu.permissionMode` | `default` | Initial mode (`default`/`acceptEdits`/`plan`/`bypassPermissions`). |

## Commands

| Command | What it does |
|---------|--------------|
| `BiuMind: New Session` | Open the chat panel with a fresh session. |
| `BiuMind: Send Selection to Agent` | Send the current editor selection. |
| `BiuMind: Send Prompt…` | Quick-pick prompt input. |
| `BiuMind: Show Bridge Status` | Print bridge endpoint + session id + cost. |
| `BiuMind: Cancel Current Turn` | Drop the in-flight turn. |

## Architecture

```
extensions/vscode/src/
  extension.ts      activation + command wiring
  bridgeProcess.ts  spawns + supervises `biu bridge`
  bridgeClient.ts   typed POST + SSE consumer
  chatPanel.ts      webview rendering
  statusBar.ts      mode + cost
```

The bridge protocol is documented in
[`docs/biu/usage.md`](../../docs/biu/usage.md#3-http-bridge-ide-integration)
and implemented in
[`apps/cli/biu/internal/bridge/server.go`](../../apps/cli/biu/internal/bridge/server.go).
SSE supports `Last-Event-ID` resume out of the box; the client tracks
the cursor per session for future use.

## Roadmap

- [ ] Inline diff preview when the agent edits a file
- [ ] Permission prompt UX (mode bar opens a quick-pick on ask)
- [ ] Multi-session tabs in one panel
- [ ] Marketplace publish

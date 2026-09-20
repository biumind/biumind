# Download & Install

One BiuMind account works across every platform. Install channels and caveats per platform are listed below — you can also get everything from the official [download page](https://biumind.ai/download), which auto-detects your system and marks the recommended download.

> [!NOTE]
> Links on the download page are generated dynamically from the server-side release manifest (`releases.json`). When a platform has no release artifact in the current round, its button is automatically labeled "coming soon" and grayed out — each section below notes the current release status.

## Desktop

### macOS

- A `.dmg` installer is provided for **Apple Silicon (arm64)**, with a filename like `biumind-{version}-macos-arm64.dmg`.
- There is no separate installer for Intel Macs yet — the download page shows that button as "coming soon"; use the [Web version](https://biumind.ai/app) for now.
- System requirements: macOS 12.0 (Monterey) or later.

To install, open the `.dmg` and drag `biumind.app` into the Applications folder.

> [!WARNING]
> Whether the installer is code-signed and notarized depends on whether a signing certificate was configured at release time. If the download page shows instructions for an unsigned package, on first open **right-click the app → "Open"** (don't double-click), or click "Open Anyway" under System Settings → Privacy & Security.

The desktop app bundles the `biu` CLI as a local daemon that starts out of the box; if biu isn't installed separately on your machine, the client downloads and installs it automatically as needed.

### Windows / Linux

Windows (`.msix`) and Linux (`.deb` / AppImage) installers have not been released yet — the download page shows those buttons as "coming soon". Until then, use the [Web version](https://biumind.ai/app) in your browser; accounts and data are exactly the same.

## Mobile

### Android

Install directly from the **APK** (the "Download Android APK" button on the download page, or grab `biumind-{version}-android.apk` from GitHub Releases). System requirements: Android 10.0 (API 29) or later.

During installation, the system will ask you to allow "installing apps from unknown sources" — follow the prompt to enable it.

### iOS

Not released yet — the download page shows "coming soon". Use the Web version for now.

## Web

Don't want to install anything? Open [biumind.ai/app](https://biumind.ai/app) in a browser and start using it — accounts and data are fully shared with the desktop and CLI.

## The biu command line

`biu` is BiuMind's terminal AI coding agent — a single static binary with no runtime dependencies. Pick one of three ways to install:

**Homebrew (recommended, macOS / Linux):**

```bash
brew install biumind/tap/biu
```

**Download a pre-built binary from GitHub Releases** (covering darwin / linux × amd64 / arm64 — four platforms):

```bash
tar -xzf biu_*_$(uname -s)_$(uname -m).tar.gz
install -m 0755 biu /usr/local/bin/biu
```

**Build from source** (Go 1.25+):

```bash
go install github.com/biumind/biumind/apps/cli/biu/cmd/biu@latest
```

Verify the installation:

```bash
biu version --short
```

On first use, run `biu init` to complete the setup (cloud accounts use browser-based authorization login; credentials are stored in the system keychain), then type `biu` to enter the REPL. For the full walkthrough, see the [CLI getting started guide](../cli/getting-started.md).

## Browser extension (BiuMind Clipper)

A Chrome / Edge / Brave extension (Manifest V3) that saves the current webpage or selected text into your BiuMind knowledge base with one click.

The extension is not yet published to the app stores, so it needs to be sideloaded in developer mode:

1. Clone the repository (`git clone https://github.com/biumind/biumind.git`) — the extension source lives in `apps/webclip/`.
2. Open your browser's extensions page (enter `chrome://extensions/` in Chrome / Edge) and enable **Developer mode**.
3. Click **Load unpacked** and select the `apps/webclip/` directory.
4. In the extension's **Options** page, configure your BiuMind server address and JWT token (copy the token from the client's settings page), then click "Test connection" to confirm it works.

Once installed: click the extension icon to open the popup, pick a target project, and save; or select text on a page, right-click, and choose "Save selection to BiuMind". The default shortcut is `Ctrl+Shift+S` (`Cmd+Shift+S` on macOS), changeable in your browser's extension shortcut settings. Clipped content lands in the sources list of the corresponding Wiki project, where you can trigger AI ingestion to turn it into Wiki pages.

## Next steps

- [Quickstart](quickstart.md) — register a cloud account or try a self-hosted trial
- [What is BiuMind](index.md) — the product and the six modules at a glance

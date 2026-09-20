# What is BiuMind

BiuMind is an all-in-one AI work platform. Work that used to be scattered across multiple tools — writing documents, running agents, coding, taking notes — comes together in a single workspace with **shared data**: documents, web clips, and anything worth keeping from your conversations all land in the same knowledge base, where AI connects them into a knowledge graph that every module can reuse.

There are two ways to run it:

- **Cloud (sign up and go)**: register an account at [biumind.ai](https://biumind.ai) and start using it — good for individuals and small teams.
- **Self-hosting**: bring up the full service stack with one command, with all data staying on your own servers — good for enterprises and data-sensitive scenarios.

The client experience is identical in both modes. See the [Quickstart](quickstart.md) for details.

## The six modules

### Knowledge Hub

Where you write documents and manage knowledge (backed by the `brain` service; the client maps to `features/wiki`, `features/notes`, `features/memory`, `features/search`):

- **Wiki documents**: write in a block-based editor, with page version history, revision diffs, and merging.
- **Knowledge graph**: see how pages relate to each other in a graph view, with condition-based filtering.
- **AI memory**: AI remembers what you've said and your preferences, and automatically brings that context into chat, coding, Channels, and other scenarios.
- **Global search**: semantic search across all your documents — one sentence brings back what you wrote before.
- **External content**: the browser clipper extension saves any webpage into a Wiki source with one click; clipped or imported content can then be organized into multiple Wiki pages through an AI ingestion pipeline.

### Coding Workbench

Dispatch AI engineers from a graphical interface (the client maps to `features/code`; backed by the `runtime` service, which shares the same agent kernel as the `biu` CLI):

- Run multiple AI tasks **in parallel**, each editing code in its own workspace.
- Built-in Git panel (branches / commits / history), terminal, and file tree.
- The AI **asks for your confirmation before editing files or running commands**, with controllable permission policies and optional Hooks for auditing.
- Extend agent capabilities with Skills.

### Creation (AIGC)

Turn an idea into a finished piece in one sentence (the client maps to `features/creation`; backed by the `aigc` service plus async workers):

- Text-to-image and text-to-video.
- Viral content teardown that analyzes why a piece of content took off.
- Persona management, a gallery of generation tasks, and a single place where your finished work accumulates.

### Cloud Workspace

Switch devices or entry points without interrupting your work:

- Sessions, tasks, and memory live on the server (`runtime` + `realtime` services); sign in with the same account from desktop, mobile, or the command line and pick up where you left off.
- Bring your own model key (BYOK): your API keys are stored encrypted by the platform, all model calls are routed through the model gateway (`model-relay`), and billing stays consistent.

### Channels

Bring AI into the communication tools you already use (backed by the `channels` service):

- Drivers for Feishu, Telegram, Slack, Discord, and email are implemented (enabled per deployment environment).
- Incoming messages get automatic replies, with your knowledge base and AI memory automatically recalled as answer context so answers stay on point.

### App Center

Professional AI assistants out of the box (the client maps to `features/apps`; backed by the `app_center` service):

- Built-in reference apps: **RSS** (subscribe to feeds; AI generates summaries / daily digests / weekly digests / transcripts), **Translate**, and **Tasks**.
- Apps can be installed and upgraded — or build your own BiuApp and publish it to the App Center.

## Available on every device

One account, multiple entry points you can switch between at any time. The CLI and the graphical clients share the same kernel, and sessions are interchangeable:

| Entry point | Notes |
|------|------|
| Desktop (the primary work client) | macOS (Apple Silicon) installer is released; Windows / Linux installers are in the works — use the Web version in the meantime |
| Mobile | Android is available as a direct APK download; iOS is coming soon |
| Web | Open in a browser and go, nothing to install |
| Browser extension | Chrome / Edge / Brave clipper extension — save whatever you're looking at into your knowledge base with one click |
| Command line | `biu` CLI: chat, run agents, and work with your knowledge base from the terminal |
| Mini programs | uni-app mini programs (WeChat / Alipay / Douyin / Baidu / QQ / Kuaishou / JD / Feishu / H5), under construction |

For install instructions on each platform, see [Download & Install](download.md).

## Next steps

- [Quickstart](quickstart.md) — sign up on the cloud, or try self-hosting with one command
- [Download & Install](download.md) — install channels and caveats for every platform
- [Getting started with the CLI (biu)](../cli/getting-started.md) — the terminal workflow
- [Self-hosting guide](../self-hosting/index.md) — deploy BiuMind on your own servers

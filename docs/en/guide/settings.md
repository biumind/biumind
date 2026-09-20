# Account and Settings User Guide

Settings is BiuMind's hub for accounts and preferences: sign in to your account, plug in your own model API keys, issue long-lived tokens for third-party tools, manage signed-in devices, tweak appearance and per-module defaults, view usage stats, and handle membership subscriptions and version updates — all from one place.

## Getting to know the Settings page

Click the user area at the bottom of the left sidebar to open **Settings**. Desktop is a two-pane layout — navigation on the left, content on the right; mobile becomes two stacked pages: the category list first, then full-screen detail once you tap an entry, with the back button returning to the list.

The left navigation is organized by group:

| Group | Entries |
|---|---|
| General | Data statistics, Appearance, Search, My shares, Document processing |
| Agent | Credentials, Model services, Logged-in devices, API Tokens, Activity, Coding Workbench (desktop), Chat platform |
| Subscription & billing | Membership Center, Order history, Redemption codes, Referral rewards |
| Devices | My devices |
| System | About |
| Account | Sign out |

A few entries (shortcuts, network proxy, data storage, etc.) are marked "soon" — placeholders not yet open, and not clickable for now.

> [!NOTE]
> The **Coding Workbench** entry appears on desktop only. **Logged-in devices** manages **login sessions**, while **My devices** manages **paired biu CLI machines** — different purposes, see [Multi-device sign-in and device management](#multi-device-sign-in-and-device-management).

## Sign-in and account

### First sign-in (Credentials)

On first use, set up your server address and account under **Agent → Credentials**:

1. **Server address**: your BiuMind server address (the production site or your self-hosted site; `http://localhost:8088` for a local dev environment).
2. **Email** and **Password**: your account credentials.
3. Click **Sign in**.

After a successful sign-in, session history syncs automatically across your devices; the bottom of the page shows when your access token expires (tokens renew automatically — nothing to manage).

### Registering and email verification

On the Credentials page click **Register**, submit an email and password, and the system emails you a 6-digit verification code (if the site has no mail service configured, ask your administrator to get the code from the server logs). Then enter the code on the sign-in page to finish verification — unverified accounts cannot sign in.

### Signing out

Two entries: Settings → **Account → Sign out**, or the user area at the bottom of the sidebar. After confirming, local caches are cleared (knowledge base, creation, and subscription caches); coding projects are kept. You land back on the sign-in page.

> [!WARNING]
> Signing out only affects the current device; other devices stay signed in. To take a device offline immediately, use the revoke function in [Logged-in devices](#logged-in-devices-login-sessions).

## Model services: bring your own API key (BYOK)

**Agent → Model services** lets you plug in API keys from your own model providers (Bring Your Own Key). Once configured, calls to those models go through your key and skip platform billing.

The page shows provider cards on the left and, on the right (wide screens), the configuration detail of the selected provider; on narrow screens, tapping a card opens its config page. Credentials come in two flavors, told apart by the badge on the card:

| Badge | Route | Best for |
|---|---|---|
| Cloud | Forwarded through the BiuMind cloud to the provider | Works across devices — any client signed into the same account can use it |
| Desktop | Dials the upstream directly from your desktop client | Upstreams the server can't reach, like a corporate intranet proxy or a gateway you run locally |

### Supported providers

Anthropic Claude, OpenAI GPT, DeepSeek, ByteDance Doubao, Alibaba DashScope, Volcengine Ark (Doubao vision), Google Gemini, Azure OpenAI, Kimi (Moonshot), Alibaba Qwen, Baichuan — plus **Custom** (any OpenAI-compatible / Anthropic / Google protocol upstream, such as a self-hosted gateway).

Each card shows its credential state:

- Not configured (cloud providers show a "Not configured — add a key to skip platform billing" hint);
- Configured and verified: a green check at the end of the card, with the subtitle showing the key's last four characters and the upstream address;
- Configured but failed verification: a red exclamation mark.

### Configuring a cloud credential

1. Click the provider's card on the left.
2. Enter the **API Key** (that's the only field for standard providers).
3. (Optional) Add a **note** — handy for telling multiple keys apart.
4. Click **Save**. When editing an existing credential, leaving the key field blank keeps the current key unchanged.

### Configuring a custom provider

After selecting **Custom**, fill in these in addition to the API Key:

- **Base URL**: the upstream address, e.g. `https://new-api.example.com`;
- **Protocol**: OpenAI-compatible (new-api / vLLM / DeepSeek-style gateways), Anthropic, or Google;
- **Models covered**: declare which models this key covers, comma-separated. Prefix wildcards are supported — `glm-*` (every model starting with glm) or `glm-4.5,glm-4` (exact list).

> [!WARNING]
> Model matching supports only "the full wildcard `*`" or "prefix wildcards (like `glm-*`)" — not wildcards in the middle. `glm-*-mini` is treated as a literal model name and never matches anything.

When a call comes in, the system matches the model name against these declarations; a hit routes to your key instead of platform billing.

### Configuring a local direct credential

1. Click **Add local direct credential** at the bottom of the list.
2. **Base URL** is required — e.g. `https://intranet-proxy` or a provider's official address.
3. Pick the protocol, fill in the models covered (same rules as above, required), and enter the API Key.
4. After saving, the card carries the "Desktop" badge, and chat traffic for those models is routed directly from your desktop client to that upstream.

> [!NOTE]
> Local direct credentials currently support the **Custom** provider only. If you see a legacy direct credential for a standard provider (created by an older version) flagged "legacy dead credential" in red, delete it and recreate it as Custom.

### Testing the connection

The **Test** button in the bottom-left corner of the config page runs a connectivity check:

- Cloud credentials: tested by the server on your behalf, returning `valid` / `invalid` / `network` and the like;
- Local direct credentials: a real request goes out from this machine; on success you'll see "Connected (latency ms) · model name". If the models you entered use a wildcard (like `glm-*`), it first pulls the upstream's `/models` list, resolves the first concrete matching model, and tests with that.

### Deleting a credential

The **Revoke** (cloud) or **Delete** (local direct) button in the bottom-right corner of the config page takes effect immediately after confirmation — irreversible; you'd have to re-enter the key if you need it again.

## API Tokens (long-lived tokens for third-party tools)

**Agent → API Tokens** issues long-lived Bearer tokens (PATs) for situations where a login session doesn't fit — MCP servers, CI scripts, automation programs. For the full picture see [API and third-party integrations](../developers/api.md).

### Creating a token

1. Click **Create** in the top-right corner.
2. Fill in a **name** (like `claude-desktop` / `ci-deploy`).
3. Check the **permissions**: `read` (read the knowledge base / search / lists), `write` (create / edit / merge pages).
4. Drag the slider to pick the **validity**: 7 to 365 days, 365 by default.
5. Click **Create**; a dialog shows the full token in the clear, in the form `bm_<8-char prefix>_<JWT>`. Click **Copy** to save it, then **I've saved it** to close.

> [!WARNING]
> The secret is shown exactly once, at creation; the server stores no plaintext, and it cannot be viewed again after the dialog closes. Copy it to a safe place immediately.

To use it, put the whole `bm_...` string into the request header as-is:

```text
Authorization: Bearer bm_xxxxxxxx_eyJhbGciOi...
```

### Revoking a token

Each token card in the list shows its name, masked prefix, permissions, last used, and expiry. Click **Revoke** and confirm.

> [!WARNING]
> The revoke confirmation notes: the current deployment does not enforce revocation, and issued tokens may remain valid until they expire. If you suspect a leak, reissue with a shorter validity and replace every consumer.

## Multi-device sign-in and device management

One account can be signed in on desktop, mobile, and web at the same time, with data syncing automatically. Each client registers a session under the account on sign-in.

### Logged-in devices (login sessions)

**Agent → Logged-in devices** lists all your login sessions:

- Each card shows the device name (like `macOS · MacBook-Pro`, `Web on macOS`), a device-type icon, last active time and IP, and the authorization validity and expiry;
- The device you're using carries a purple "Current device" tag and sorts first;
- Click **Revoke** to take that device offline immediately — it must sign in again; revoking the current device is the same as signing out on this machine;
- When other devices exist, a **Sign out everywhere else** button appears at the bottom of the page: everything except the current device gets logged out.

### My devices (biu CLI pairing)

**Devices → My devices** manages remote machines paired via `biu pair` (say, the Mac at home running the biu CLI):

1. On the target machine, run:

```bash
biu pair
```

2. The machine displays an 8-digit pairing code; back in the client, click **Approve new device** in the bottom-right corner and enter the code to confirm.
3. Once approved, the device gets its device token automatically and appears in the list with its online state (online / offline / last active).

Each device can have its own **tool permissions**: read-only, read-write within a restricted directory, or full access; and you can **revoke** any device at any time — its token stops working immediately and it must be paired again.

### Command-line (CLI) sign-in

The biu CLI signs in to the same account via the OAuth flow with `biu auth login` (a `--manual` mode is available); see [Getting started with the biu CLI](../cli/getting-started.md).

> [!TIP]
> **Logged-in devices** manages the sign-in state of each client app / browser; **My devices** manages the remote execution machines paired through `biu pair`. Revoking the former means typing your password again; revoking the latter means pairing again.

## Appearance

**General → Appearance** has four sections, all applied instantly — no restart needed:

| Setting | Content |
|---|---|
| Interface language | Follow system / 中文 / English |
| Color theme | 18 theme palettes (recommended ones first), with two-tone dots previewing the primary and accent colors — click to switch |
| Font size | Small / Medium / Large, affecting font size, spacing, and list density, with a live preview card below |
| Mode | Follow system / Light / Dark |

## Chat platform

**Agent → Chat platform** configures the out-of-the-box behavior of new chats (for the concepts, see the [Chat user guide](chat.md)):

| Setting | Content |
|---|---|
| Default chat mode | Which of Chat / Agent / Task a new chat starts in (Agent out of the box) |
| Default model (chat mode) | The model new sessions use by default; picking "BiuMind default (unspecified)" follows the platform configuration |
| Text-to-speech | The voice source for read-aloud: the default is "device-local speech (offline, free)"; pick a cloud voice model and you can further set a voice ID — read-aloud then uses high-quality cloud synthesis, falling back to the local voice on failure |
| Auto-title from the first prompt | When on, new sessions are named automatically from the first message; when off, the sidebar keeps the "New chat" placeholder until you rename it |

## Coding Workbench

**Agent → Coding Workbench** (desktop only) configures the executable paths and default working directory of your AI coding agents (for the concepts, see the [Coding Workbench user guide](code.md)):

- The **Task isolation (worktree)** toggle: on, every task runs in its own git worktree + branch (`biu/<agent>-<id>`), so parallel tasks never interfere; off, all tasks share the working directory and may overwrite each other's files. Switching saves immediately.
- **Working dir**: the directory tasks run in — where agents read and write files.
- The three executable paths, **biu / Claude / Codex**: leave empty to resolve via PATH; use an absolute path for unusual install locations. Each row has a **Test** button that runs `<path> --version` and shows the version or an error.
- **Auto-detect paths & versions**: the desktop daemon scans PATH and common install directories (nvm / brew / `.local/bin`, etc.), auto-fills **blank** fields, and summarizes each agent's version. The button is disabled while the daemon isn't connected (per-field Test still works).
- The bottom of the page shows the **effective PATH** (parsed from your login shell) — handy for debugging "it works in a terminal but the app can't find it".
- **Restore defaults** empties every path field; **Save** writes the configuration.

## Document processing

**General → Document processing** decides where documents imported into the knowledge base (PDF / DOCX / XLSX / PPTX / EPUB / HTML / MD / TXT) get parsed:

| Option | Behavior |
|---|---|
| Automatic (default) | Small files parsed locally (free; desktop ≤50MB / mobile ≤10MB), large files automatically in the cloud |
| Prefer local | Parse locally whenever possible (free); only very large files (>200MB / mobile >80MB) go to the cloud |
| Prefer cloud | Everything is uploaded and parsed in the cloud, billed in credits per page |

Local parsing is free; cloud parsing costs credits per page; scanned-document OCR and audio/video transcription are cloud-only. Platforms without local parsing (Windows / Linux) disable "Prefer local" and note that the cloud is always used.

Below that is the **Wiki generation model**: the model used to auto-generate Wiki pages from uploaded documents. The preference is stored on your account and synced across devices; the default is "follow the platform default", and a model you pick yourself is billed against your usage (your configured API key is used first).

## Search

**General → Search** currently has a single toggle: **Include notes in unified search**. On, the global search page's results include matching notes (title + summary); off, only knowledge base content is searched. It doesn't affect note search inside the Notes module.

## My shares

**General → My shares** centrally manages every note-share link you've issued (sharing itself is initiated from the note editor's toolbar or the list's context menu). Each row shows the note title, a status badge (Active / Stopped / Expired / Limit reached), validity, view count (like `3 / 10 views`), and whether a password is set.

Available actions:

| Action | Effect |
|---|---|
| Copy link | Active shares only; copies the current share address |
| Stop sharing | The link stops working immediately; it can be resumed later (same address) |
| Resume sharing | A stopped share comes back at its original link |
| Reset link | Issues a new token; the old link dies immediately, and every link already sent out goes dead |

## Data statistics

**General → Data statistics** is account-level, cross-device usage and activity stats, split into **Activity overview** and **Usage**.

**Activity overview**:

- Four metric cards: topics, messages, cumulative tokens, and models used — each with a month-over-month figure (percent change versus last month);
- An activity heatmap for the past year (contribution-grid style), with "active N days" and "N-day streak" in the top-right corner;
- Two rankings: model usage (messages per model) and topic volume (the sessions with the most messages).

**Usage** (viewed by month, flip forward and back):

- Three cards: today's spend (credits), this month's spend (credits + call count), and number of active models;
- A daily bar chart, switchable between **credits** and **tokens**;
- A spend-by-model ranking;
- A call detail table: each call shows model, input / output tokens, TPS, spend (credits), and time — paginated.

## AI memory

<!-- TODO: unverified: the Memory page (list / recall, filter by type, manually add and delete memories) and the Settings "Memory" entry are currently offline (the backend knowledge-memory module is not fully implemented); fill this section in once restored. -->

## Membership and credits

### Membership Center

Three ways in: the credit badge at the bottom of the sidebar, Settings → **Subscription & billing → Membership Center**, or the user avatar menu. The page, top to bottom:

- **Current plan card**: plan name, subscription status, and period end date; credit balance (split into permanent and time-limited credits); progress bars for the used portion of your monthly Chat quota and monthly AIGC quota;
- **Monthly / yearly toggle** (yearly carries a discount badge);
- **Choose a plan**: each plan card shows the price (yearly prorated to a monthly figure) and the benefits list (monthly credits, Hub RPM / TPM, daily sandbox tasks, Brain projects); your current plan is marked "Current", and the others show "Upgrade" / "Downgrade" by tier;
- **One-time credit packs**: credits bought once (not affecting your subscription or monthly credits); time-limited packs carry a validity badge (like `30d`); click to enter the payment page.

### Upgrading, downgrading, and canceling

- **Upgrade**: picking a higher plan opens a confirmation showing the prorated difference in detail (credit for the remainder of the old plan, the top-up for the new plan, what you pay now); confirm to proceed to payment;
- **Downgrade**: takes effect at the end of the current period after confirmation;
- **Cancel subscription**: the "Cancel subscription" on the current-plan card offers two options — "Stop at period end" (service continues to the end of the period, no refund) or "Stop immediately + prorated refund";
- A canceled subscription can be reinstated with **Resume subscription**.

### Payment

On the payment page, pick a payment method, then click **Pay now**:

| Method | Notes |
|---|---|
| WeChat Pay (QR) | Shows the QR code content and order number; pay by scanning with WeChat |
| WeChat Pay (H5) | Jumps to WeChat's H5 payment |
| Alipay (web / mobile) | Opens the browser and jumps to Alipay to pay |
| International credit card (Stripe) | The overseas card channel |

After paying you return to the client; credits usually arrive once the payment callback is confirmed — go back to the Membership Center and refresh to check.

### Order history

**Subscription & billing → Order history** lists all orders: amount, payment channel (WeChat Pay / Alipay / Stripe / Apple IAP / Google Play), order type, creation time, and the channel's order number (copyable), with status badges: paid (green) / pending (blue) / refunded (orange) / failed and canceled (red).

### Redemption codes

**Subscription & billing → Redemption codes**: enter a code and click **Redeem**. Four kinds of codes are supported: price-off / discount / credit packs / trial extensions. Common errors are shown with their reasons (invalid code, expired, disabled, already used, not applicable to your plan, currency mismatch, usage limit reached). After a successful redemption, subscription and order data refresh automatically.

### Referral rewards

**Subscription & billing → Referral rewards** shows your personal referral code and invite link (one-click copy or the system share sheet), plus referral stats (total invites / rewarded / pending / revoked).

Reward rules:

- When an invitee completes their first valid subscription, both sides get 500 credits;
- Invites over a threshold from the same IP / device fingerprint within 24 hours count as fraud — no reward;
- If the invitee gets a refund or is flagged, rewards already issued are clawed back.

### Plan comparison table

The client ships a plan comparison table comparing tiers across monthly fee, yearly fee, monthly credits, Hub RPM / TPM, daily sandbox tasks, and Brain projects, with a plan picker at the bottom of each column.<!-- TODO: unverified: the comparison page (`/membership/compare`) has a registered route but no in-app entry was found; the plan cards in the membership center serve as the main comparison view. -->

## Version updates

- **Update banner**: when a new version is detected, a banner appears at the top of the app with the version number and a release-notes summary; **Go to download** jumps to the official download page. Closing the banner silences it for this session only — it reappears after a restart.
- **Manual check**: the **System → About** page checks for updates automatically (repeat visits within 60 seconds don't re-request), showing one of three states: "New version found", "Up to date", or "Check failed"; **Check again** forces a re-query, and when a new version is found the banner carries a "Go to download" button.
- **Dev builds**: a toggle on the **About** page (labeled "Unsigned · may be unstable · back up your data first"). On, updates come from the nightly build channel instead; a new build triggers "New dev build #N found", with download links matched to your platform automatically (Apple Silicon / Windows / Linux AppImage / Android APK).

The **About** page also shows the current version and build number (version + build timestamp).

## Activity

**Agent → Activity** is your account's event stream: recent operations in reverse-chronological order (PAT created / revoked, with page edits, skill installs, and more to come). Scrolling to the bottom loads earlier records automatically; the top-right corner has a manual refresh.

## Related docs

- [Chat user guide](chat.md) — chat and agent modes
- [Knowledge Hub guide](knowledge.md) — document import and the Wiki
- [Coding Workbench user guide](code.md) — the Coding Workbench
- [API and third-party integrations](../developers/api.md) — PATs and the REST / MCP interfaces
- [Getting started with the biu CLI](../cli/getting-started.md) — command-line sign-in and pairing

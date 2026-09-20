# App Center Guide

The App Center is BiuMind's app directory: browse, install, upgrade, and manage apps here; pin your favorites to the sidebar; or turn any website or open-source project into a member of your workspace.

The entry is **App Center** in the desktop client's left sidebar. You need to sign in and configure server credentials in **Settings** first — otherwise the App Center only shows a placeholder and doesn't load the directory.

## Browsing and finding apps

Open the App Center and you get a grid of app cards (more per row the wider the window; two columns on mobile). Each card shows the app's icon, name, and description; installed apps carry an "Installed" badge.

At the top of the page there are two ways to filter:

- **Search box**: fuzzy-matches name, identifier, or description, filtering as you type.
- **Category tabs**: `All` / `Installed (n)` / `Productivity` / `Content` / `Data` / `Communication` / `Developer` / `Tools`.

Click any card to open that app's detail page. **Right-clicking** a card (long-press on touch) opens a context menu:

| Menu item | What it does |
|---|---|
| Pin to sidebar / Unpin | Pin an installed app into the left sidebar (grayed out when not installed, prompting you to install first) |
| Customize sidebar… | Opens the [sidebar customization](#customizing-the-sidebar) page |

There are also four buttons on the right of the title bar: **Add WebView app** (+ link icon), **Install GitHub app** (download icon, desktop only), **Refresh**, and **Manage** (opens the app management page).

> [!TIP]
> If you're running your own app locally with `biu app run --dev` from the CLI, a "In development" section appears at the top of the directory; its cards carry a `DEV` badge, clearly separated from formally installed apps. This section is invisible in normal use.

## Viewing an app's details

The detail page header shows the app's large icon, name, author, and version, with the action buttons on the right. Below that, in order:

- **Full description**
- **Permissions**: every permission the app declares, listed as tags; high-privilege ones — external network access, sandboxed execution, reading credentials — stand out with a prominent warning style
- **Views** (if any): the interfaces the app ships
- **Triggers** (if any): automatic triggers like cron schedules / webhooks
- **Included skills** (if any): Skills installed alongside the app

The action buttons change with install state:

- **Not installed**: a primary **Install** button
- **Installed**: **Open** (enter the app's interface), an enable/disable toggle, and **Uninstall**

Purely background apps (with no declared views) show no **Open** button — their capabilities surface as skills in chat; after installing, just stay on the detail page and read the description.

## Installing apps and permission consent

Clicking **Install** opens the permission consent dialog:

1. Every permission the app declares is listed, each with an explanation (e.g. "Access the specified external network. Limited to the domains listed in the manifest" or "Read the contents of your Wiki")
2. All permissions are checked by default; **you can uncheck any of them** — the app simply won't have the unchecked permissions at runtime after installation
3. High-risk permissions carry a warning icon; confirm each one before installing
4. Click **Install** to finish, or **Cancel** to back out

A success banner pops after installation. If the app has an interface, you're **taken straight into it** — no hunting for the entry after install; the banner also carries an **Add to sidebar** button (prompted at most once per app per 7 days) to pin it to the sidebar.

> [!NOTE]
> Uninstalling a WebView app also clears that site's login cookies and other data stored in the client; reinstalling the same site means a fresh login.

## Managing and upgrading installed apps

Click **Manage** in the App Center's title bar to open the **App Management** page:

- **Available upgrades section**: apps with new versions are gathered here, each row with an **Upgrade** button and the `vcurrent → vtarget` version numbers
- **Installed list**: each row shows the app's name, version, and scope, followed by **Open**, the enable/disable toggle, and **Uninstall**; click a row to open its detail page

**The upgrade flow**: clicking **Upgrade** opens a permission-diff dialog with three sections (only sections with content are shown):

| Section | Meaning | What you do |
|---|---|---|
| New permissions (red) | Permissions the new version adds | **Check and confirm each one** — the **Upgrade** button unlocks only after all are checked |
| No longer requested (gray) | Permissions the new version drops | Informational only |
| Already granted | Unchanged permissions | Collapsed by default; expand to view |

Click **Not now** to stay on the current version. If the upgrade adds no new permissions, the dialog simply offers a one-click upgrade.

> [!WARNING]
> Apps force-installed by your organization's administrators can't be disabled or uninstalled from the management page (the buttons are grayed out) — contact your administrator.

## Adding WebView apps

Want your favorite sites inside BiuMind? Click the **Add WebView app** button in the title bar and fill in two or three fields:

1. **Name**: what shows in the App Center and the sidebar (e.g. "Kimi")
2. **URL**: http(s) only, and it must be a full domain or localhost
3. **Icon**: no need — once you enter the URL the client fetches the site's favicon automatically and previews it; not happy? Change the URL to trigger a re-fetch

After creation you jump straight to the app's detail page; from there it opens, pins, and behaves like any other app.

> [!NOTE]
> All WebView apps share one login state: sign in to a site inside BiuMind, and every WebView app sees the same cookies, isolated from your system browser. For multiple accounts, use the system browser.

The WebView app interface has a small browser toolbar (back / forward / refresh, etc.). Navigating to **a different site** prompts for confirmation first, as anti-phishing protection; only http(s) addresses are allowed. Windows / Linux and web clients have no embedded WebView engine and show a fallback interface with "Open externally" and a copy-link option.

## Installing apps from GitHub repositories

(macOS / Linux clients only; requires the biu CLI on your machine)

The App Center can turn an open-source project on GitHub into a **locally running web app** with one click: the client clones the repository, installs dependencies, and starts the service on your machine, then wires its web interface into the workspace.

How it works:

1. Click the **Install GitHub app** button in the title bar, paste the repository URL (e.g. `https://github.com/owner/repo`), and click **Analyze repository**
2. When analysis finishes, a confirmation page shows:
   - **Analysis summary**: project name, version, stars, license, description, plus any warnings from the analysis
   - **What will run locally**: tech stack, dependency-install commands, start command, port, health check, and required runtimes (and whether missing ones get installed automatically) — everything in plain sight before anything executes
   - **Configuration**: a form of the environment variables the project needs; secret fields are marked "🔒 Stored locally only, never uploaded"
   - **Version source**: track the **latest release** or the **default branch**
3. Click **Install**. On first open the client clones the repository and installs dependencies (this can take a few minutes, with live progress logs), and when it's done:
   - **macOS**: the app opens in its own native window; closing the window doesn't stop the background service, and the next open reconnects instantly
   - **Linux**: runs full-screen inside the app; a failed start can be retried in one click

Unsupported repository types are rejected during analysis with the reason. **Upgrades** go through the app management page: for GitHub apps the upgrade button runs "confirm → server rebuilds → local update (auto-rollback to the old version on failure)" — no permission-diff dialog.

> [!WARNING]
> GitHub apps execute code from the repository on your machine. Only install repositories you trust, and read the command list under **What will run locally** carefully before confirming.

## What app interfaces look like

After installing, click **Open** — the app's interface is presented in one of three ways:

- **Declared views**: interfaces described by the app manifest and rendered natively by the client. Common shapes include lists, list-detail, forms, grids, dashboards, and conversational views; toolbar buttons and refresh on the page are all defined by the app itself.
- **Dynamic UI**: the app sends down a UI description tree at runtime (nested combinations of text, cards, buttons, inputs, charts, and other components), which the client renders from a whitelist. The upside: apps can change their interface freely, while the client enforces hard limits on component count and nesting depth — pathologically huge UI trees are refused.
- **WebView panel**: the whole interface is just an embedded web page (previous section).

## Customizing the sidebar

The sidebar decides which entries stay within reach. There are three ways into the **Customize sidebar** page:

- The tune icon button at the bottom of the sidebar
- An app card's context menu → **Customize sidebar…**
- Just **drag an App Center card onto the sidebar** (drop to pin)

The page has three sections:

| Section | What you can do |
|---|---|
| System (show / hide toggles) | System entries like Chat, Search, Wiki, Notes, Skills, App Center, and Code: drag to reorder, check to show or hide (system entries can only be hidden, never deleted) |
| Pinned apps | Apps pinned into the sidebar: drag to reorder, click × to remove; an uninstalled app shows as an invalid item — removing it is recommended |
| Pinnable apps | Installed but unpinned apps: click **Pin** to add them to the sidebar |

Click **Save** in the top-right corner when done; **Restore defaults** reverts everything. Saves sync to the cloud, so **the sidebar stays the same on every device**:

- If another device just changed the sidebar, your editing page reloads with a notice: "The sidebar was changed on another device — the latest version has been reloaded"
- Offline edits are staged locally (a yellow banner appears at the top) and sync automatically on reconnect

The sidebar itself also supports three modes: collapsed, icons only, and icons with labels.

## Built-in apps

The current version of the App Center actually ships three built-in apps:

### RSS

A one-stop information hub. After installing, open it from the detail page — it has six tabs:

- **Today**: AI-curated daily headlines — a hero card + a grid of top stories + a "missed" list + a trend strip; **Regenerate** in the top-right corner, plus the ability to **play the day's briefing as synthesized speech** (with play / pause / progress)
- **Inbox**: a classic three-pane reader (feed list | article list | reader) that folds automatically on narrow screens. Click **Subscribe** to add a source, with three kinds supported:
  - RSS / website: paste the address and the server auto-discovers the feed
  - WeChat official accounts: enter the account name (requires configuring a relay address in RSS settings first)
  - X users: enter an @handle (requires configuring a Nitter instance address in settings first)
  **Refresh all subscriptions** in the Inbox's top-right corner pulls manually (shortcut `R`); auto-refresh from every 15 minutes to every 3 hours can be configured in settings
- **Radar**: a rule engine for monitoring. The left column lists rules (toggleable), the right shows hits (filterable by all / unread). Click **New rule** to build one with the visual editor: keywords (any match / all match / exclude), source scope, severity, cooldown — or describe it in **natural language and click "AI parse"** to generate the rule automatically. Every rule can have an **action recipe**: on a hit, send a notification, save to the Wiki, create a task, or invoke a skill
- **Leaderboards**: a wall of trending-topic cards from various platforms (trends, hot lists, communities); click an item to open the original
- **Explore**: subscribe to curated **topic bundles** in one click, or migrate subscriptions wholesale from an **OPML file** exported by Feedly / Inoreader and the like
- **Starred**: everything you've marked, in four groups — favorites / saved to Wiki / pinned / shared

Two high-frequency features live in the top-right corner:

- **AI Co-Pilot** (shortcut `⌘J` / `Ctrl+J`): a right-hand drawer for asking questions about the current view (Today / Inbox / Radar); answers carry citation markers that jump to the original article
- **Share this view**: turns today's digest / subscriptions / radar hits / favorites into a **read-only share link** — copy it to someone without BiuMind and they can still view it

The **⋯** menu holds **Settings**: theme (the reader goes true-black automatically in dark mode), default refresh frequency, an AI summary toggle (generates AI summaries for new articles — consumes credits), WeChat / Nitter relay configuration, OPML export, and full data export (subscriptions / articles / favorites / rules / settings zipped up).

> [!TIP]
> Team-plan users can switch the data scope between "Mine / Team" below the app bar: switched to Team, feed subscriptions, radar rules, and hits are shared across the team.

### Translate

A small utility that calls platform models to translate text into a target language; the source language can be set to "auto-detect".

### Tasks

A personal task list: create tasks, filter by completion status / tag, mark done, delete.

## Known limits

- The server currently registers exactly three apps — **RSS**, **Translate**, and **Tasks**; the other cards in the directory depend on which apps your connected server has deployed
- GitHub repository apps are macOS / Linux clients only for now; Windows and mobile show a platform-not-supported notice
- To build your own app for the App Center, see the developer documentation, the [BiuApp development guide](../developers/biuapp.md)

# Coding Workbench User Guide

The Coding Workbench is the **Code** page of the BiuMind desktop client: hand a code repository to an AI engineer — you write the task description, review approvals, and watch progress, while Git, the terminal, and file browsing all live in the same window. You can run several tasks in parallel and accept them one by one, like a project manager.

> [!NOTE]
> The Coding Workbench is desktop-only (macOS / Windows / Linux clients); there is no such module on the web.

## Interface overview

With a project open, the workbench has five areas:

```text
┌──┬────────────┬──────────────────────────┬──────────┬──┐
│Pr│ Branch bar │   Main area              │ Right    │Ic│
│oj│ Task list  │   - Task detail + replay │ panel   │on│
│ec│ (searchable│   - or file viewer tab   │ (Files/  │st│
│ts│            │   ─────────────────────  │ Git/… )  │rp│
│  │            │   (bottom: terminal panel │          │  │
│  │            │    toggleable)            │          │  │
└──┴────────────┴──────────────────────────┴──────────┴──┘
      Bottom status bar: current task's agent / model / permissions + usage
```

- **Project bar** (leftmost, 48px): project avatars stacked vertically; click to switch projects, long-press and drag to reorder; the **+** at the bottom adds a new project.
- **Task list**: all tasks of the current project. At the top are the current branch bar and the **New Task** button; below them a search box, task counts, and **Clear all**; the bottom shows the number of running tasks. Tasks that need your attention (awaiting approval, connection lost, interrupted) are pinned to the top under a "Needs attention" group.
- **Main area**: with no task selected it shows the new-task page; with a task selected it shows the task detail header + session replay; opening a file from the file tree switches to a file-viewer tab (click a task on the left to switch back to the conversation).
- **Right panel** (300px, collapsible): one of six panels — Files / Git / History / Skills / Status signals / Project config.
- **Right icon strip** (48px): Files, Git, History, Terminal, and Search on top; Skills, Project config, and Status signals (hooks) below. Clicking the icon of an expanded panel collapses it; **Terminal** toggles the bottom terminal panel; **Search** opens the file-search overlay.
- **Bottom status bar**: shows the current task's agent, model, and permission level; the usage entry is on the right.

> [!TIP]
> The divider between the task list and the main area can be dragged to resize.

## Adding and switching projects

1. Click the **+** at the bottom of the left project bar, or the **Add project** button on the welcome page you get on first launch.
2. Pick a local repository directory in the system directory picker and click **Add**.
3. The project switches to it automatically; tap its avatar any time to switch back.

On first launch (no projects yet), the top of the page has two tabs: **Projects | Timeline**. The Projects tab is the welcome page (with recently opened projects); the Timeline tab is the cross-project task timeline (see [Parallel tasks](#running-multiple-tasks-in-parallel)). Once a project is selected the timeline disappears, and tasks live in the left-hand list.

## Having AI fix a bug

### 1. Dispatch the task

Click **New Task** at the top of the task list and the main area shows the task description page ("What do you want to build today?"):

- **Description**: a multi-line input for the task description. Typing `@` opens a file picker — search by file name, select, and the file path is inserted into the description as `@path`, letting the AI locate the relevant files directly.
- **Attachments**: the **+** on the left of the toolbar adds attachments — pick an image, paste one from the clipboard (an error screenshot, say), or paste text. Images are stored under the project's `.biu/attachments/`; text is appended to the end of the prompt.
- **Agent**: choose the execution engine — `biu` (BiuMind's own CLI), `Claude Code`, or `Codex`.
- **Permissions**: three levels — `Ask Permission` (asks before every sensitive action), `Auto Edit` (edits files automatically), `Full Access` (fully automatic).
- **Run environment** (below the input): `Follow settings` (per your global settings), `Local` (runs directly in the project directory), `New worktree` (creates a dedicated git worktree for this task, with a choice of base branch: current HEAD or any local branch).

Submit with `⌘/Ctrl + Enter` (the button shows the hint too). You're switched to the new task automatically.

> [!TIP]
> Submitting with an empty description starts an interactive terminal session — like opening an AI terminal in the project directory, chatting and working at the same time.

### 2. Track progress, handle approvals

After submitting, the main area shows the task detail header and the conversation:

- **biu tasks**: shown as a structured message stream — the AI's text output renders as Markdown, and every tool call appears as a card (reading files, diff previews of file edits, Bash commands and output, etc.); the card currently executing has a spinner.
- **Claude Code / Codex tasks**: run in a real terminal; the main area shows the live terminal. When the task ends, if structured events exist, it automatically switches to a Markdown-style replay.
- **Approvals**: at the Ask permission level, sensitive operations requested by the AI insert an orange approval card (`Allow <tool>?` plus arguments). You can **Allow** (for this session), **Allow once**, or **Deny**. Tasks waiting for approval go into the pinned "Needs attention" group.
- **Detail header**: the task title (renameable, AI-namable, exportable as Markdown), agent / model / permissions, working directory, plus metrics like duration, TOKENS, context usage, cost, and lines added / removed; on the right, per status: **Stop**, **Mark complete**, **Resume** (continue from where it was interrupted), **Merge to main** (merge the branch back into its base once a worktree task finishes), and so on.

Task states at a glance:

| State | Meaning |
|---|---|
| Queued | Created, waiting to start |
| Running | Executing |
| Needs confirmation | Waiting for you to approve an action |
| Completed / Failed | Ended normally / ended with an error |
| Interrupted | The process has stopped; you can **Resume** |
| Terminal connection lost | The process is still running in the background — only the UI lost the connection; **Reconnect** reattaches |

> [!TIP]
> Both "Interrupted" and "Terminal connection lost" show a banner at the top of the replay: the former's **Resume** button continues from the last session (disabled when there is no session to resume), the latter's **Reconnect** reattaches to the still-running terminal (without restarting the session). Both can be wrapped up with **Mark complete**.

### 3. Accept the result

When a task finishes: the task row shows `+N −N` lines added / removed relative to the base branch; for worktree tasks, click **Merge to main** in the detail header to merge the branch back (on conflict it fails and keeps the worktree — resolve it by hand in the Git panel). You can also review diffs and commit from the Git panel.

## Running multiple tasks in parallel

Parallelism needs no special switch — every task is independent:

1. After dispatching the first task, click **New Task** again for a second, a third… Each task has its own run environment and state.
2. Click different tasks in the task list to switch between them in the main area; tasks waiting on you gather in the "Needs attention" group.
3. The bottom of the left column shows `N running` live.

The key to parallelism is **isolation**. Settings → Coding Workbench has a **Task isolation (worktree)** toggle:

- **On** (recommended): every task automatically gets its own git worktree and branch (branch names like `biu/<agent>-<id>`). Multiple tasks editing files at once never interfere, and each merges back when done.
- **Off**: all tasks share the project directory, and parallel tasks may overwrite each other's files.

When creating a task you can also pick **Local** or **New worktree** in **Run environment** for that one task, overriding the global default.

Other actions on the task list:

- **Search**: the search box at the top filters by title or task description.
- **Star**: hover a task row and click the star icon; starred tasks sort first.
- **Context menu** (right-click, or long-press): rename, AI name (auto-names the task from its description), copy prompt, star / unstar, delete (deleting cleans up the worktree too — irreversible).
- **Clear all**: deletes every task of the current project at once (worktrees included), with a confirmation.

The little icons on task rows mean: a branch-shaped icon = the task has a worktree branch (hover shows the branch name); a device icon = the task came from another device of yours; different shapes distinguish the three agents — biu / Claude Code / Codex.

### Cross-project timeline

With no project open, switch to the **Timeline** tab at the top: tasks are grouped by "Today / Yesterday / Earlier", and within each group by project. Clicking any task switches to its project and opens it.

## Browsing and viewing files

### File tree

Click **Files** on the right icon strip to open the files panel:

- The directory tree is **lazily loaded**; click a folder to expand / collapse, with a small spinner while loading.
- Three small buttons in the header: new file, new folder, refresh (all act on the project root).
- **Right-click** (or long-press) a directory for **New file / New folder**; right-click any node to **Delete** (permanent, no trash, with a confirmation).
- Click a file → it opens as a tab in the main area.

### File viewer

File tabs in the main area:

- Text files get syntax highlighting, read-only; image files preview directly. Very large files are truncated with a notice.
- Multiple files open side by side as tabs; tabs are closable; switching tabs doesn't lose content (kept alive in the background).
- Click a task on the left to switch back to the conversation any time.

### File search

Click **Search** on the right icon strip to open the search overlay: fuzzy-search **git-tracked files** by name, `↑` `↓` to navigate, Enter or click to open in the main area. The search is debounced at 300ms, capped at 50 results.

## Git operations

All Git features act on the **currently open project**; the source of truth is your local git repository.

### Branch switching

The **branch bar** at the top of the task list shows the current branch; click to expand the menu:

- **Switch branch**: lists local branches (the current one has a ✓); click to check it out.
- **New branch…**: type a branch name (like `feature/x`) — it's created and checked out.
- **Delete branch…**: a submenu to pick a non-current branch, then delete after confirmation; branches with unmerged commits prompt whether to force-delete.

### Source control (Git panel)

Click **Git** on the right icon strip. The panel is split into a left column (380px) and a diff area, with a **Changes / History** view toggle at the top.

**Changes view**:

- The header shows the current branch, ahead / behind counts versus the remote (`↑N` `↓N`), and three buttons: **Pull**, **Push**, **Refresh**.
- The change list has two groups: **Staged changes** (count + **Unstage all**; individual rows can be unstaged) and **Changes** (count + **Stage all** and **Discard all**; individual rows can be staged or discarded). A letter before each file name marks its state: `M` modified, `A` added, `U` untracked, `D` deleted, `R` renamed — each with its own color.
- Discard actions (single file or all) come with a confirmation dialog — unstaged changes and untracked files are deleted, irreversibly.
- The commit box at the bottom: write the commit message (with a nudge toward Conventional Commits); click **AI generate** to generate a message from the current changes automatically (works with any changes present — no need to stage first); click **Commit N files** to commit what's staged.
- Click any file and the right side shows its unified diff, colored per line: additions green, deletions red, position info purple; monospace, horizontally scrollable.

**History view**: lists the commit history; click a commit and the right side shows its metadata (message, short hash, author, date, lines added / removed, file count) and the full diff.

### Commit history (History panel)

**History** on the right icon strip is a separate, lightweight history panel: the most recent 100 commits (message, short hash, author, date); click one to view its unified diff, with a back arrow at the top-left to return to the list. Compared to the History view inside the Git panel it's narrower — good for quick browsing.

## Terminal

Click **Terminal** on the right icon strip to expand a terminal panel at the bottom of the main area (closable):

- An interactive shell (zsh / bash) opened in the **current project directory** — unrelated to tasks, usable any time.
- Multiple terminals per project: switch with the **Shell 1 / Shell 2 …** sub-tabs at the top; `+` to open, `×` to close; shells moved to the background keep their output.
- Entering the terminal panel opens the first shell automatically if there is none.

Task session terminals: Claude Code / Codex tasks run inside real PTY terminals, so while they run the main area is their live terminal; reopening a finished task replays the persisted terminal log first, then attaches to live output. When the process ends, the terminal's last line shows the exit code.

> [!NOTE]
> Terminal colors follow the app's light / dark theme. If a terminal recording predates the persistence feature, or a task produced no output, you'll see "This task has no terminal recording to replay".

## Usage

The usage entry on the right of the bottom status bar (hover the icon for a "Usage" tooltip) opens an overlay:

- **Claude Code**: remaining percentage and reset time for both the 5-hour window and the 7-day window of your subscription.
- **Codex**: remaining percentage and reset time for the primary and secondary quotas, with plan type and account.

Remaining percentages are colored: above 70% green, 20%–70% orange, below 20% red. The refresh button inside the overlay re-fetches the snapshot.

> [!TIP]
> Usage data comes from each agent's subscription status, read by the local daemon; the entry is grayed out when the daemon isn't ready. With many parallel tasks, glance at the remaining quota first so you don't hit a limit mid-run.

## Skills (project-level)

Click **Skills** on the right icon strip to open the project-level skills panel:

- It lists every skill in `~/.biumind/skills` — the same store as the [Skills management](skills.md) page, so anything you add there shows up here.
- Each skill card has `claude` and `codex` buttons; clicking installs / uninstalls that agent's skill for the current project: installing links the skill into the project's `.claude/skills` or `.codex/skills` directory — so not only can workbench tasks use it, running Claude Code / Codex directly in a terminal discovers and reuses the same skills.

## Status signals (hooks)

Click **Status signals (hooks)** on the right icon strip:

- **Node.js detection**: hook scripts need Node.js; the panel shows whether it was found and its path.
- **Agent readiness**: lists whether Claude / Codex and friends meet the minimum usable version, with reasons when they don't.
- **Install / Reinstall** and **Uninstall** buttons: install or remove the hooks in one click.

What the hooks do: they let Claude Code / Codex report lifecycle events while running (waiting for approval, turn finished, etc.), which drives the reliable "Needs attention" grouping in the task list instead of inferring state by polling logs. Recommended when you start using the Coding Workbench.

## Project config

Click **Project config** on the right icon strip to edit the current project's `.biu/config.toml` (the panel subtitle: defaults for new tasks and a prompt prefix for every task):

- **Default agent**: `biu` / `claude` / `codex` — pre-filled on the new-task page.
- **Default permission level**: `ask` / `auto_edit` / `full_access` — likewise the default for new tasks.
- **Prompt prefix**: text automatically prepended to the description of **every task** in this project — for example, "Follow the code style in STYLE.md". Left empty, nothing is added.

Click **Save** to write the config file.

## Desktop and the local daemon

Every local operation of the Coding Workbench — Git, file reads and writes, terminals, skill installs, usage reads — goes through a local daemon: after sign-in the desktop client automatically starts `biu serve` in the background (listening only on the loopback address), and the UI talks to your repositories through it. Normally you never need to think about it; if panels say "Local daemon not ready — the desktop client starts biu serve automatically after sign-in", check two things:

1. Are you signed in?
2. Is the `biu` CLI installed? (See the install section of the [CLI quickstart](../cli/getting-started.md).)

> [!TIP]
> If the desktop client finds no `biu` on your machine, it can download and install it automatically. You can also go to Settings → Coding Workbench and click **Auto-detect paths & versions** to have the daemon scan PATH and common install directories (Homebrew, global npm, `~/.local/bin`, etc.) for the `biu` / `claude` / `codex` executables, with a Test button per path to verify versions.

## Settings: binaries and working directory

The Settings → Coding Workbench page configures the execution environment:

- **Task isolation (worktree)**: see [Parallel tasks](#running-multiple-tasks-in-parallel).
- **Working dir**: the directory tasks run in by default.
- **biu / Claude / Codex paths**: paths to the three agent executables; leave empty to resolve via PATH; each field has a Test button that runs `--version` to verify.
- **Effective PATH**: the PATH parsed from your login shell (desktop apps launched from the Dock often carry an incomplete PATH — this shows the search path actually used for spawned child processes).

## Going deeper

- Installing, signing in to, and everyday use of the biu CLI: [CLI quickstart](../cli/getting-started.md), [CLI usage guide](../cli/usage.md)
- Full semantics of permission modes, rules, and hooks: [Permissions & Hooks](../cli/permissions.md)
- Complete subcommand reference: [Command reference](../cli/commands.md)
- Browsing, installing, and approving cloud skills: [Skills user guide](skills.md)

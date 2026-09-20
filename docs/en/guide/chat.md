# Chat User Guide

Chat is BiuMind's AI chat workspace: sessions on the left, the conversation on the right. Switch models at any time, attach images, invoke skills, and keep what matters by favoriting messages, saving them to the Wiki, or exporting a backup. In agent mode the assistant can also call tools and ask you questions — you decide which actions get through.

## Getting to know the interface

On desktop it's a two-pane layout — session list + chat area. On narrow screens (mobile) it becomes two stacked pages: the list first, then full-screen chat once you open a session, with a back button in the top-left corner to return.

A row of icon buttons sits at the top of the session list, left to right:

| Button | What it does |
|---|---|
| Command palette | Open the global command palette (Cmd/Ctrl+K) |
| Favorited messages | Browse all ⭐ favorited messages across sessions |
| Search all chats | Search history messages across sessions (Cmd/Ctrl+Shift+F) |
| Import chat JSON | Restore sessions from a backup file |
| Bulk manage | Enter multi-select mode for sessions (bulk delete) |
| New chat | Create a new empty session |

Below that is a **Filter chats…** input that filters the current list by title as you type (unarchived sessions only).

When no session is selected, the right side shows the welcome page: a time-of-day greeting, usage stats, six starter prompt cards, your installed skills, recently used models, and recent sessions. Mobile has no welcome page — when the session list is empty it shows a greeting and four starter cards instead.

## Starting a conversation

### Creating a session

Any of these three works:

- Click the **+** button at the top of the list (or press Cmd/Ctrl+N) to create an empty session with your default preferences;
- Click any **starter card** on the welcome page — a session is created automatically and the prompt is dropped into the input box;
- Press Cmd/Ctrl+K to open the command palette and choose **New chat**.

A new session uses the default model from your preferences; if none is set, or that model has been retired, the platform default model is used.

### Sending and stopping

- **Enter** sends, **Shift+Enter** inserts a newline; on mobile keyboards Enter is a newline — send with the round button in the bottom-right corner of the input box.
- While a response is being generated, the bottom-right corner of the input box turns into a **stop button**; a stop button also appears in the title bar at the top so you can interrupt while scrolling back through history. After stopping, the message is tagged "Stopped" at the end.
- During generation, "Generating" appears next to the title along with the live rate (e.g. `· 25 t/s`).

### Input history and drafts

- When the input box is empty, press **↑** to flip through messages you've sent in this session (↓ to go back); selecting one refills the box.
- Every session's input **draft is saved automatically** (written to disk about half a second after you stop typing), so the text is still there when you switch away and back; a draft clears itself once it's sent.
- To see every unsent draft across sessions: command palette (Cmd/Ctrl+K) → **View drafts (works in progress)**. Each row shows the session title, a draft excerpt, and its length; click to jump back to that session, or use the button on the right to discard a single draft.

## Choosing a model

### Switching models per session

Models are a **per-session** setting: every session can use its own model without affecting the others.

Click the **model chip** at the bottom of the input box (it shows the current model name) to open the model picker:

- The search box at the top filters by name / identifier;
- Models are grouped by provider; each row shows the model name, a price tag (the actual billed price), and the context window size (e.g. 200K); the platform default model carries a "Default" badge;
- The gear at the right of each group header: platform-pool models jump to the Membership Center, while your-own-key (BYOK) models jump to the API Keys page in Settings to manage keys.

Only models that are actually usable are listed — either from the platform pool, or models for which you've configured a valid key of your own. If the list is empty, you'll be prompted to go set up a key.

Press Cmd/Ctrl+Shift+M inside a session to bring up the model picker directly.

> [!TIP]
> On the welcome page, click a chip in the **recently used models** row to set it as your **global default model** — every new session will start with it.

### Cost estimates and credits

- Once your input exceeds 20 characters, a **cost estimate chip** appears next to the send button: platform-billed models show "≈ N–M credits", while your-own-key calls show a green "0 credits · BYOK". The estimate refreshes about 0.6 seconds after you stop typing, and hides silently on failure so it never interrupts your typing.
- Next to the chip is a small counter: "N chars · ~M tokens" (the token count is a local estimate, for reference only).
- If your **credits run out**, an "Insufficient balance" dialog appears showing the credits needed for this request and your current balance, with two options: **Top up now** (jumps to the Membership Center) or **Add a BYOK key** (jumps to Settings; once your own upstream key is configured, the platform stops charging you).
- When a model is unavailable (disabled / nonexistent / no channel), a red error banner appears at the top of the session with a one-click **Re-pick model** fix; if your plan tier isn't high enough, an **Upgrade membership** entry appears instead.

### Context window

A thin progress bar sits at the top of the session (below the search bar) showing the **cumulative context consumed** in this session: bar plus "used / total · percent". It turns orange above 60% and red above 85%. Hover for exact numbers.

> [!TIP]
> When the context is nearly full, the model starts to "forget" the earliest content. Start a new session at that point — or save important conclusions to the Wiki / favorites first, then continue.

## Advanced input

### Slash commands and skills

Typing `/` at the start of the input box opens the command menu, which has two sections — built-in commands and your installed skills:

| Command | What it does |
|---|---|
| `/new` | New chat |
| `/clear` | Clear the input box |
| `/note` | Save the latest completed reply as a note |
| `/help` | Show the list of available commands |

Selecting a **skill** from the menu inserts `/<skill-id> ` — then just type your question. ↑↓ to navigate, Enter to trigger, Esc to clear; the menu steps aside automatically once `/` is followed by a space or arguments. The Cmd/Ctrl+/ shortcut opens the menu directly when the input box is empty.

### Referencing skills with @

Besides `/`, you can reference a skill in a message with `@skill-id`. Clicking a chip in the skills row on the welcome page fills `@<id> ` into the input box automatically — you just write your actual request after it.

> [!NOTE]
> The input box itself has no `@` autocomplete popup; `@` references are made through the skill chips on the welcome page or by typing the identifier manually.

### Web search

The **globe icon** in the bottom-left corner of the input box is a one-shot **web search** toggle: when on, the icon lights up, and sending automatically prepends a "prefer web search" instruction to your message; the toggle turns itself off after that send. It can't be toggled while generating.

### Chat / Agent mode

The **mode chip** at the bottom of the input box switches between two modes:

| Mode | Description |
|---|---|
| Chat | Plain model Q&A, no tool calls |
| Agent | Calls tools through the local daemon (reading and writing files, running commands, etc.) |

If no online daemon is detected when switching to **Agent**, you'll see "No daemon online" and the switch is refused. After a successful switch, two more chips appear below the input box:

- **Working directory**: click to pick a local directory as the assistant's working scope (the chosen directory is added to trusted scope automatically); once set it shows the directory name, and **long-pressing the chip clears it**.
- **Tool call approval**: three levels — **Auto-approve** (all tool calls pass through), **Whitelist**, and **Manual approval** (ask every time).

### System prompts and templates

Open **⋮ menu → Chat settings** in the top-right corner of a session to edit that session's system prompt (attached as a system message to every request) and review the mode, model, and created / updated times.

System prompts you use often can be saved as **templates**:

- Command palette → **Manage system prompt templates**: create / edit / delete templates (name + full text).
- Command palette → **Apply system prompt template** (with a session selected first): apply a template to the current session in one click.
- The chat settings panel also has a **Choose from template** button that fills the editor directly.

## Sending image attachments

Attachments currently support **images only** (png / jpg / jpeg / webp / gif / heic), up to 10MB each. Images are automatically compressed to a model-friendly size before sending (oversized or wrong-format files get a notice).

Ways to add them:

- Click the **+** button in the bottom-left corner of the input box and pick an image file;
- On desktop, **drag** an image into the input box, or **Cmd/Ctrl+V** to paste one from the clipboard;
- On mobile, tapping + gives three options: take a photo / choose from library / pick a file.

Attachments line up as thumbnail chips above the input box, showing file name and size; click the ✕ in the top-right corner of a chip to remove it. They are sent with your next message.

> [!WARNING]
> If the current model doesn't support image input, the + button is grayed out with a hint to switch to a vision-capable model.

## Managing your sessions

### Per-session actions

Hover over a session and a **⋯** button appears on the right; you can also **right-click** (long-press on mobile) a session for the same menu:

| Menu item | Shortcut | What it does |
|---|---|---|
| Pin / Unpin | Cmd/Ctrl+P | Pinned sessions stay in a "Pinned" group at the top of the list |
| Rename | F2 | Change the session title |
| Archive | Cmd/Ctrl+E | Removes it from the list into archive management |
| Export JSON | Cmd/Ctrl+Shift+E | Export the session as a JSON backup file |
| Delete | Cmd/Ctrl+Backspace | Delete after a confirmation (irreversible) |

On mobile you can also **swipe left** on a session to archive it quickly.

### Archive management

At the bottom of the list is an "Archived N" entry (hidden when the count is 0). In the archive page each session can be **unarchived** back into the list, or **deleted forever** (with confirmation, irreversible).

### Favorites / drafts / cross-session search

- **Favorited messages**: click ⭐ under an assistant message to favorite it. The star button at the top of the list opens the favorites view, showing all favorites across sessions; click any one to jump back to the original session at that message.
- **Cross-session search**: type keywords into the search box (instant, debounced); results are grouped by session, showing role, a highlighted matching snippet, and time; click to jump back to the session and locate it.
- **In-session search**: press Cmd/Ctrl+F inside a session; a search bar appears at the top — Enter for the next hit, Shift+Enter for the previous, Esc to close, with "current / total" shown on the right.

### Export and import

- **Export one session**: session menu → **Export JSON**.
- **Export everything**: command palette → **Export all chats**, which generates a complete JSON backup including archived sessions.
- **Import**: the upload icon at the top of the list; pick a JSON file. Import automatically recognizes both single-session backups and full backups.

### Command palette

Cmd/Ctrl+K opens it. It gathers every entry point above (new, search, favorites, drafts, archive, bulk manage, export all, templates, settings, shortcut panel) and also fuzzy-matches to **switch to any recent session**.

## Working with messages

### Assistant message actions

Every completed assistant message has a row of action icons underneath (condensed on mobile to the frequent ones, with the rest in a ⋮ menu):

| Action | Description |
|---|---|
| 😊 Emoji reaction | Opens a grid of common emojis; "More" opens the full picker; chosen emojis show as chips — tap again to remove |
| ⭐ Favorite | Favorite / unfavorite the message (it goes to your favorites) |
| Copy | Copy the full text |
| Copy as markdown quote | Copies as a `>` quote block (over-long text is truncated to the first 6 lines) |
| Regenerate | Regenerate the answer for the same prompt |
| Reply with quote | Turns the message into a quote block injected into the input box (desktop only) |
| Translate | Opens in Google Translate (desktop only) |
| Read aloud / Stop reading | System text-to-speech, language detected automatically |
| Save as Wiki page | See below |
| Share as image | See below |
| Delete | Delete the message after a confirmation (irreversible) |

At the end of each message there's also a small token stat (e.g. `↑1.2k ↓356`), and hovering over a message floats a mini toolbar with the most common actions in the top-right corner. Long-pressing a message avatar reveals advanced actions: view raw structure / copy message ID / toggle favorite / delete, and more.

### User message actions

Hover over a message you sent to: copy, **edit** (an inline editor — saving rewrites that message), regenerate (re-sends with this message as the prompt, truncating everything after it), or delete.

### Multi-select messages

Session ⋮ menu (top-right) → **Select messages** (or via the command palette): checkboxes appear before messages and an action bar floats at the bottom — **Copy** (merged as markdown), **Translate** (opens in Google Translate, truncated to 4500 characters when over-long), **Export MD** (generates a Markdown file with model and message count), **Delete**, select all, and cancel.

### Navigating long answers

- Long answers with 3 or more headings get a collapsible **outline** at the top ("Outline · N items") that expands into a hierarchical list.
- When a session exceeds 6 messages, a **navigation strip** appears on the right: one segment per message (user gray, assistant purple, failed red); click to jump straight to it.
- Under every completed assistant message are three **follow-up suggestion** chips (elaborate / give an example / make it more concise); clicking one fills the corresponding follow-up into the input box.

### Saving to the Wiki and Notes

- **Save as Wiki page**: noise like chain-of-thought is stripped automatically and the first line becomes the default title; in the dialog you can change the title and pick the target project. After saving, the banner lets you open the new page in one click.
- **Save as note**: type `/note` to save the latest completed reply as a note; the banner jumps to the notes list.

### Share as image

**Share as image** renders a single message as a branded card (with the BiuMind logo, model name, and time). You can **save a PNG** locally; on desktop you can also **copy the image** to the clipboard and paste it anywhere.

> [!NOTE]
> The share card strips markdown down to plain text — code blocks and tables appear as plain text in the image.

## Working with the agent: approvals and forms

### Tool approval cards

In agent mode (with approval mode not set to "Auto-approve"), whenever the assistant wants to make a tool call, an approval card floats above the input box: it shows the tool name, the reason for the call, and a preview of the arguments (collapsed past 4 lines; click to expand). Three choices:

| Button | Effect |
|---|---|
| Allow | Let this call through |
| Always allow | Let it through and raise this session's approval level to "Auto-approve" — no more prompts |
| Deny | Skip this call; the assistant will try another route |

### Question form cards

When the assistant needs a decision from you, a **form card** floats up: a question and optional choice chips (single-select / multi-select / free text input). You can **Submit** your answer, **Skip** (letting the assistant choose a default), or **Cancel**. Once answered, the card becomes read-only and records your choice; unanswered cards that time out are marked too.

## Keyboard shortcuts

Press **Shift+?** (while the input box is not focused) to open the shortcut panel at any time:

| Shortcut | Action |
|---|---|
| Enter | Send |
| Shift+Enter | Newline |
| ↑ / ↓ | Browse input history |
| `/` | Slash command menu |
| Cmd/Ctrl+K | Command palette |
| Cmd/Ctrl+N | New chat |
| Cmd/Ctrl+P | Pin / unpin the current session |
| Cmd/Ctrl+Shift+F | Search all chats |
| Cmd/Ctrl+F | Search in the current chat |
| Cmd/Ctrl+Shift+M | Switch model |
| F2 | Rename the current session |
| Cmd/Ctrl+E | Archive the current session |
| Cmd/Ctrl+Shift+E | Export the current session as JSON |
| Cmd/Ctrl+Backspace | Delete the current session |
| Shift+? | Shortcut panel |

> [!NOTE]
> On macOS the modifier is ⌘; on Windows / Linux it's Ctrl. Mobile doesn't show shortcut hints. For installing and managing skills, see the [Skills user guide](skills.md).

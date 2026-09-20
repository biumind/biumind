# Knowledge Hub Guide

This guide covers the day-to-day use of BiuMind's Knowledge Hub — the **Wiki** and **Notes** modules. It picks up where [Getting Started](../getting-started/index.md) leaves off; for chat, App Center, and the other modules, see the other guides in this section.

The Knowledge Hub has two layers:

- **Wiki**: a structured knowledge base organized into projects. Feed in PDFs, Word documents, web pages, Markdown files, and other material, and the system automatically parses them into interlinked Wiki pages — with a knowledge graph, search, AI chat, deep research, and AI-powered maintenance on top.
- **Notes**: lightweight personal notes organized with notebooks, tags, and to-dos, with sharing and a trash folder. When a note is ready for something more permanent, you can move it into the Wiki as a full page with one click.

The two share data: web clips and chat content can flow into the Wiki, notes can be promoted into Wiki pages, and everything is findable through global search.

## Wiki

### Create a project

1. Click **Wiki** in the left sidebar of the desktop client to open the project list.
2. If you don't have any projects yet, you'll see a welcome page with five starter template cards. Later on, you can use the **New Workspace** button in the top-right corner, or the **New Project** card at the end of the grid.
3. Pick a template (or just start from a blank project):

| Template | Best for |
|---|---|
| Research | Deep research: hypothesis tracking + methodology + literature review + knowledge graph |
| Reading | Reading notes: characters / themes / plot lines / chapter notes |
| Personal growth | Goals / habits / reflections / journal, linked into a personal knowledge web |
| Business team | Team knowledge base: meeting notes / decisions / projects / stakeholders |
| General | Start from scratch and organize freely |

4. In the **New Project** dialog, enter a project name and click **Create**. If you picked a template (other than **General**), the system generates a set of structured starter pages for the project.

Inside a project, the left side holds the feature bar with these entries:

| Entry | What it does |
|---|---|
| Pages | Browse, read, and edit all Wiki pages in this project (⌘P jumps to a page by name) |
| Sources | Upload / manage PDFs, Markdown files, URLs, and other sources |
| Search | Three-way search within the project (keyword / semantic / graph) |
| Graph | Visual network of page relationships |
| Chat | AI chat grounded in this project |
| Research | Deep Research tasks |
| Review | AI review queue (duplicates, contradictions, stale content, and other issues) |
| Mirror | Export the whole project as a markdown bundle |

At the bottom of the feature bar you'll find **Workspaces** (back to the project list), **LLM Settings**, **Feedback**, and **Global Settings**. The status bar at the bottom of the window shows the project name, connection state, and the **Activity** entry (which opens the background-task panel), plus a hint for the **⌘K command palette**.

> [!TIP]
> Inside the Wiki, press **⌘K** (Ctrl+K on Windows) to open the command palette and jump to any of the entries above from the keyboard. Press **⌘P** for "jump to page by name" — type a few characters and land on any page. On mobile, these entries live in the list button at the top and the ⋮ menu in the top-right corner of a page.

### Bring material into a project (Sources)

**Upload files or web pages**

1. Open the **Sources** page on the left side of the project.
2. Click the **Upload** button in the top-right corner to open the **Import Data Sources** dialog. There are three ways:
   - **Single file**: pick one PDF / DOCX / XLSX / PPTX / EPUB / Markdown / HTML / TXT document;
   - **Multiple files**: select several files at once and add them all to the background queue;
   - **URL**: paste a `https://…` web address (HTML / PDF / Markdown) and the system fetches and stores it.
3. Selected items are listed at the bottom of the dialog. Click **Add to Queue** and you're returned immediately — parsing happens in the background. Track progress in the **Activity** panel of the bottom status bar (you can cancel and retry failed items).

When parsing finishes, the material is broken down by an LLM into multiple Wiki pages that automatically appear in the **Pages** list.

> [!NOTE]
> Where documents are parsed is configured in **Settings → General → Document Processing**: Automatic (the default — small files parsed locally for free, large files in the cloud), Prefer cloud (consumes credits per page), and so on. Platforms without local parsing always use the cloud.

**Manage sources**

Each row in the **Sources** list shows the file name, a status badge (Queued / Parsing / Ready / Failed), size, and path, with two buttons on the right:

- ⚡ **Parse**: manually trigger a parsing job (this jumps to a progress page that shows statuses — Queued / Parsing / Partially done / Done / Failed — and an event log in real time);
- 🗑 **Delete**: remove the source (page references are not cleaned up automatically; duplicates go to the review queue for handling).

> [!WARNING]
> There is currently no "new blank page" button — pages in a project come from source parsing, AI research, and chat archives. The empty-state text on the page list mentions "new page", but that entry is not implemented yet.

### Reading and editing pages

**Page list**: the **Pages** pane groups pages by their **type** (the `type` field in the frontmatter). Group headers expand / collapse, and the filter box above filters by title or path as you type. Click any page to open it in the reading view on the right.

**Read / Edit toggle**: to the right of the page title is a Read / Edit segmented switch. Pages with no content open in edit mode by default. The editor is a what-you-see-is-what-you-get Markdown editor with autosave — no manual save needed. The body supports tables, task lists, math, and code blocks (mermaid blocks render as diagrams).

**Wikilinks**: while editing, typing `[[` brings up autocomplete over page names in this project; pick one to insert a `[[page name]]` link. While reading, click a wikilink to jump straight to the target page.

**Page context** (between the title and the body):

- **Metadata bar**: chips for type, tags, description, origin (e.g. web-clip / deep-research), and related sources; click the pencil icon to open an edit form for the title, description, type, creation date, origin, tags, and custom key-value pairs;
- **Related pages**: chips for the pages most related to the current one (hover to see why — direct wikilink, shared neighbors, type affinity); click to jump;
- **Backlinks**: which pages reference the current page, as cards with a context snippet; click to jump.

**Outline**: pages with two or more headings get an automatic outline panel on the right; in edit mode, click an outline entry to scroll to it.

**Title-bar toolbar** (folded into the ⋮ menu on mobile):

| Button | What it does |
|---|---|
| Search in pages | Search across all page content in the project; hits show title + highlighted snippet, Enter to jump |
| History | A timeline of "who did what, when" for this page |
| Version history | List of content snapshots, see below |
| Maintain (agent) | Have AI tidy up this project, see "Let AI maintain your knowledge base" |

**Version history**: the left side of the dialog lists versions, the right previews that version's title and body. Two actions:

- **Restore this version**: the current page is overwritten with that version (the server automatically snapshots the pre-overwrite state as a restore point, so nothing is lost);
- **Save as copy**: store that version as a new page.

**Selection AI**: in edit mode, select a passage of text and a floating toolbar appears under the selection with two modes:

- **Rewrite**: type an instruction (e.g. make it more concise / translate to English / add an example) and the AI shows a side-by-side preview with word-level diffs highlighted; you can **Regenerate** or **Accept** to write it back into the body;
- **Ask**: ask a question about the selection (e.g. how does this relate to concept X?) and the AI answers with citations.

### Knowledge graph

Open **Graph** on the left to see the project's relationship graph: every node is a page, and edges between nodes represent relationships (wikilinks and semantic similarity), laid out automatically with a force-directed layout. Zoom and pan with mouse / gestures; click a node to jump to the page.

The graph is generated and maintained automatically — no manual wiring. The top toolbar, left to right:

| Control | What it does |
|---|---|
| Search box | Search and highlight nodes by title |
| Filter | Panel: hide structural pages (navigation pages like index / overview / log), hide isolated pages, show by page type, filter by connection-count range |
| Color toggle | Switch node coloring between **cluster** and **page type** modes |
| Palette | Change the graph color theme |
| Insights | Open the **Graph Insights** panel, see below |
| Refresh | Re-fetch graph data |
| Rebuild relations | Trigger a server-side recomputation of page relations (including semantic similarity) |

> [!TIP]
> The **Graph Insights** panel surfaces two kinds of findings: **unexpected connections** (strong links across clusters / types — possibly connections you hadn't noticed) and **knowledge gaps** (isolated pages, sparse clusters, bridge nodes missing a link). For each one, click the ⌖ icon to locate and highlight it on the canvas, or dismiss it individually. Knowledge-gap cards also have a **Research** button to launch a Deep Research into that gap with one click.

### Search

**Project search** (**Search** on the left): type a keyword and the search runs automatically after a 300ms debounce. Results merge three channels, each labeled with its source — **Keyword** (BM25 text match), **Semantic** (vector similarity), and **Graph** (discovered by expanding through related pages). Every hit shows a score and a snippet; click to jump to the page.

**Global search** (**Search** in the main sidebar): unified search across modules — the input placeholder reads "Search BiuMind knowledge base and the web…". Results are grouped by section:

- **Notes**: matching note entries;
- **Pages**: Wiki page hits, with BM25 / VEC / GRAPH / WEB source badges (web search results are fused in);
- **Images**: a grid of matching images; open one and you can **jump to the source page**.

### Chat within a project

Two ways in: the **Chat** entry on the left, or simply don't select any page in **Pages** — the panel on the right then defaults to this project's chat. The chat answers based on this project's knowledge.

When something worth keeping comes up, open the action menu on an assistant message and choose **Save as Wiki page**: fill in the page title, pick the target project, and the message content becomes a page in that project.

### AI research (Deep Research)

1. Open **Research** on the left and click **New Research** in the top-right corner.
2. Fill in the **Topic** (required, e.g. attention mechanisms in transformers) and **Auxiliary queries** (optional, one per line, used to steer the search direction).
3. Click **Start Research**. The task card shows the status flow: Queued → Searching → Synthesizing → Writing pages → Done (or Failed).
4. When it finishes, click **Open generated pages**: the research report — synthesized by AI from multiple sources — lands as a Wiki page with `[[wikilink]]` cross-references in the body. Its origin metadata is marked deep-research, and it joins the graph and search like any other page.

> [!TIP]
> There are two more "one-click research" entries: items in the review queue have a **Research** button that investigates directly (when the research completes, the review item is automatically marked resolved), and knowledge-gap cards in Graph Insights can launch research right away.

### Let AI maintain your knowledge base

**Automatic maintenance (agent)**

1. Open any page and click **Maintain (agent)** in the title-bar toolbar.
2. In the **Maintain (agent)** dialog, write the **maintenance instructions** (e.g. tidy up this project's knowledge, fill in missing pages, merge obvious duplicates), then choose an **intensity** (Quick 4 rounds / Standard 8 rounds / Deep 12 rounds) and a **model**.
3. Click **Start Maintenance**. The dialog switches to a run view: the top shows the **tool calls** step list (which pages the agent read and what it did, in real time), and the bottom streams the agent's running commentary. You can **Stop** at any time.
4. When the run finishes you get a summary and a **changes panel**: every modification the agent made (created / edited / merged pages) is listed one by one; open any of them to see a **diff**, or **Undo** them individually.
5. Click **Run history** in the bottom-left corner to review past maintenance runs — those changes can be undone too.

> [!NOTE]
> Every write the maintenance agent makes is automatically snapshotted into the page's version history, and any change can be rolled back from **Version history** — don't worry about the AI breaking content.

**Review queue**

Open **Review** on the left (titled "Review Queue"). This is where background workers surface things worth tidying, filterable by type tag: dedup (duplicate pages), lint (rule issues), sweep (stale / orphaned pages), and more. The **Scan** menu in the top-right corner triggers scans manually:

- **Structural scan**: rule checks like empty titles / orphan pages / duplicate titles, completes instantly;
- **Semantic scan**: an LLM flags semantic issues like contradictions and stale content, processed in the background.

Each issue card shows a type badge, a description, the pages involved (click to deep-link), and a set of actions:

| Action | Meaning |
|---|---|
| Research | Launch a Deep Research into this issue; the item is automatically marked resolved when it completes |
| Merge… | (duplicates) choose which side to keep as the "main page"; the other side's content is merged in and then soft-deleted |
| Create query page | (contradictions) distill the contradiction into a new page to be verified |
| Resolved | I fixed this manually |
| Ignore | Not an issue — don't show it again |

### Export a project

The **Mirror** page on the left exports the entire project as a zip (an Obsidian-style markdown bundle that fully preserves bodies and wikilinks): click **Export as zip** in the top-right corner; when it finishes, a dialog shows how many pages were exported and offers to copy the path. The exported files open in any markdown tool.

## Notes

### Layout and organization

Click **Notes** in the main sidebar. Desktop is three columns: **Notebooks** on the left, the note list in the middle, and the editor on the right; mobile switches between a list page and a full-screen editor.

- **New note**: the **+** button in the header of the middle column; creating one while inside a notebook or the to-do view files it into that notebook / creates it as a to-do directly.
- **New notebook**: the new-folder button in the left column's header. Notebooks support nested folders: right-click (long-press on mobile) a notebook for its menu — New subfolder / Move to… / Promote to root / Delete notebook (with a confirmation first; its notes go to the trash, sub-notebooks move up one level, and the notebook itself cannot be recovered).
- **Smart views**: All notes / Unfiled / To-dos / Shared (with an active-share-count badge).
- **Tags**: the tags area at the bottom of the left column; click + to create a tag, then filter notes by tag.
- **Search**: the **Search notes** box at the top of the middle column does full-text search, with results showing snippets with highlighted hits.

### Writing notes

- **Title + body**: the body is a what-you-see-is-what-you-get Markdown editor (same engine as the Wiki); title and content autosave — the status strip in the bottom-right corner shows "Saving… / Saved", and failed saves keep a persistent red banner. Writing works offline too; changes sync automatically once you're back online.
- **Attachments**: **Insert image** in the title row (on mobile, camera / photo library, plus paste and drag-and-drop) and **Insert attachment** (any file up to 10MB each); after upload they are embedded as links in the body.
- **To-dos**: click **Convert to to-do** to turn a note into a to-do item; a completion checkbox bar appears at the top of the editor, and items can also be checked off directly from the list.
- **Tags**: the tag row at the bottom of the editor — click **+ Tag** to multi-select or create tags.
- **Multi-device sync and conflicts**: if you edit the same note on two devices at once, a merge dialog appears on conflict where you pick which content to keep section by section (or **use all local / all server**), or **save as a copy** to keep both.

> [!TIP]
> On mobile, the ⋮ menu at the top of the editor gathers every action: insert image / insert attachment / convert to to-do / edit tags / version history / share / move to Wiki / move to trash.

### Version history

Click **Version history** in the ⋯ menu of the editor's title row: a version list on the left, a preview of that version's title and body on the right. You can **Restore this version** (current content is backed up first) or **Save as copy**.

### Sharing a note

1. Click the **Share** button in the editor's title row (or right-click / long-press the note in the list → Share) to open the **Share Note** panel.
2. For a first share, click **Create share link** to get a public link of the form `https://your-server/s/n/xxxx` — anyone with the link can view the note **read-only** without signing in; when the note is updated, readers just refresh to see the latest content.
3. Once created, manage it in the same panel:

| Setting | Options |
|---|---|
| Access password | Toggle + 4–8 character password; when on, the link must be unlocked first |
| Validity | 1 day / 7 days / 30 days / forever |
| View limit | Unlimited / 100 / 500 / 1000 / custom count |
| Reset link | The old link stops working immediately and a new address is issued (useful if the link leaks) |
| Stop sharing / Resume sharing | Take the link offline any time; it can be restored later at the same address |

The panel also shows the creation time and cumulative view count, supports one-click link copy (with a password set, you can copy combined "link + password" text) and the system share sheet. The **Shared** view in the left column lists all notes currently being shared; click **Manage all shares →** at the bottom of the panel to open the **My Shares** management page in Settings.

### Trash and moving to the Wiki

- **Trash**: enter via the trash icon in the middle column's header. Deleted notes are listed newest-discarded first and can be **Restored** or **Deleted forever** (with confirmation — permanent deletion cannot be undone).
- **Move to Wiki**: once a note has matured, click **Move to Wiki** in the ⋯ menu, pick the target project, and the note's content is archived as a Wiki page in that project (the original note shows a "Moved to Wiki — this note is archived" banner and is removed from the list). From then on it participates in the graph, search, and AI maintenance.

## Web Clipper (browser extension)

BiuMind ships a browser clipper extension (Chrome / Edge / Brave) that saves the web page you're looking at into your knowledge base with one click. Install it from the [official download page](https://biumind.ai/download), then fill in your server address and login token on the extension's options page (both can be copied from the client's settings page).

How to use it:

- Click the extension icon in the toolbar to open the popup: choose the **destination** (**Save to Wiki** — pick a project; or **Save to Notes**), confirm the title (grabbed automatically from the page) and the preview; tick **Save only the selected text** to store just the highlighted passage; tick **Auto-convert to a wiki page after saving (CoT ingest)** and the clipped content goes through the parsing pipeline to generate Wiki pages automatically;
- Select text on a page, right-click → **Save selection to BiuMind**;
- Default shortcut `Ctrl+Shift+S` (`Cmd+Shift+S` on macOS).

Pages clipped into the Wiki appear in that project's **Sources** list with origin metadata marked web-clip, and participate in parsing, the graph, and search like any other material.

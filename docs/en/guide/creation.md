# Creation User Guide

Creation is BiuMind's multimodal generation module: generate an image or a video from a single prompt, break a hit short video down into script, hooks, and shot list, then turn any of it into your own material with one click. Everything you generate lands in **My Works**, can be made public, and public works enter the **Discovery Square** where everyone can browse them and **Remake** them.

The entry is **Creation** in the client's left sidebar. Generation requires you to be signed in and the server to have creation services configured; when signed out, a banner shows at the top of the module — click **Sign in** to jump to Settings.

## Page structure

The Creation module has four sub-pages (a left navigation bar on desktop; horizontally scrolling tabs on narrow windows and mobile):

| Page | What it's for |
|---|---|
| Inspiration | The default home page: the generation panel + trending public works |
| Studio | The creation desk: focused on generating, with **Recent creations** underneath |
| Works | My Works: a waterfall of all your generation tasks, with bulk management |
| Square | Discovery Square: a waterfall of public works from all users |

Two status strips sit at the top of the module (hidden when everything is fine):

- **Connection status strip**: sign-in, service configuration, network, and realtime channel status (see [Connection status and common errors](#connection-status-and-common-errors)).
- **Generation progress strip**: while tasks are generating, shows "Generating · NN%" with a linear progress bar; the ✕ on the right cancels the latest task; brief notices appear when tasks complete, fail, or credits are refunded.

## Generating an image or video

The generation panel appears on both the **Inspiration** and **Studio** pages with the same structure: type switcher, model picker, prompt input, parameters, reference material, toggles, and the submit button.

### 1. Pick a generation type

The top of the panel has four segments: **Video**, **Image**, **Digital human**, and **Hit analysis**. Digital human carries a "coming soon" dot — selecting it only shows a preview card and can't be submitted (see [Digital humans (coming soon)](#digital-humans-coming-soon)). Switching types keeps your prompt draft but requires re-picking a model.

### 2. Pick a model

The **Select model** chip lists the models available for the current type; each option shows the model name and unit price (like "N credits"). Which models are available depends on the server-side model catalog and varies by deployment (see [About available models](#about-available-models)); if no model is configured for a type, you'll see "No models available".

After selecting a model, parameters like aspect ratio, resolution, and duration fill in with that model's defaults automatically.

> [!TIP]
> Models support different capabilities: some take reference images, some support first / last frame for video, some offer duration choices. The parameter and upload rows below appear or hide automatically based on the selected model.

### 3. Write the prompt

The prompt input is multi-line (3–6 lines, adaptive), up to 2000 characters. The submit button is disabled when the prompt is empty.

### 4. Set parameters

Parameters show as chip rows; click a chip to open its options (a popover just beneath on wide screens, a bottom sheet on narrow ones):

| Parameter | Description |
|---|---|
| Aspect ratio | Frame ratio (e.g. 16:9, 1:1); options depend on the model |
| Resolution | e.g. 720P; options depend on the model |
| Duration | Video models only, in the steps the model offers (e.g. 5 / 10 / 15 seconds) |
| Count | Fixed at 1 / 2 / 4 images; multi-output works show a +N badge on the card |

### 5. First frame / last frame / reference images

If the selected model declares support, matching entries appear below the parameter row:

- **First frame** / **Last frame**: a single image controlling the opening / closing frames of a video.
- **Reference images**: multiple images (limit set by the model, 5 by default) for style reference.

> [!NOTE]
> In the current version you paste image addresses (`cas:<sha>` or `https://…` links); the dialog also notes "file upload arrives in the next version". Local file picking and upload aren't available yet.

### 6. AI enhancement and public works

Two toggles sit above the submit row:

- **AI enhancement**: when on, the request carries a prompt-enhancement flag and the server optimizes the prompt.
- **Public work**: when on, the work is marked public on submission and appears in the Discovery Square. Off by default; you can also flip it any time later in **My Works** (see [Public vs. private](#public-vs-private)).

### 7. Submitting and billing

The left of the submit button shows the selected model's price live ("N credits / item"). Click **Generate**:

1. A **Pending** placeholder card appears immediately in **My Works** and in Recent creations under **Studio**.
2. Submission charges immediately on success; the credit balance in the sidebar refreshes at once, the prompt clears, and the parameters stay — handy for batch creating.
3. A failed submission (insufficient credits, say) pops a banner with the specific reason; the placeholder is removed and nothing is charged.

### 8. Tracking progress and canceling

Generation is an async job with progress pushed live:

- Cards show status text (Pending → Queued → Generating) and a progress bar with percentage.
- With several tasks in parallel, the module's top strip shows "N generating… (latest NN%)".
- Queued / generating cards have an ✕ button in the bottom-right corner to cancel that task; the ✕ on the top progress strip cancels the latest one.
- On completion, a "Generation complete" notice pops at the top; failures pop the reason; tasks blocked by moderation show "Content moderation failed".
- Tasks keep running even if you leave the Creation module for another one; the task list persists locally, so recent works are still there after restarting the client.

> [!TIP]
> Closing the client doesn't stop the server from finishing your tasks — reopen, go to **My Works**, and the results are waiting.

## Hit analysis

Hit analysis automatically breaks a hit short video down into reusable creative material.

### Submitting a link

Switch to the **Hit analysis** type and the input becomes a link field: paste a Bilibili / Douyin share link, or a direct public video URL (mp4 / m3u8). No prompt needed — the link alone is enough; the submit button reads **Start analysis**. The UI notes that Xiaohongshu analysis is coming soon.

### Reading the breakdown

When analysis finishes, open the task in **My Works** (or view it in the bottom detail sheet). The result has five parts:

| Part | Content |
|---|---|
| Script | The full voice-over script extracted from the video, one-click copy |
| Hooks | A list of attention-grabbing hooks from the opening |
| Shots (N) | Shot by shot: number, visual description, and a prompt ready for generation |
| Tags | Hashtags (starting with #) |
| Full transcript | The video's speech transcript; expandable and copyable |

While analysis is still running you'll see "Analyzing video…"; failures show the specific reason.

### Making a remake

Every shot has a **Remake** button on its right that fires off an image generation with that shot's prompt (automatically using the first available image model in the list); the shot section's title row also has **Remake all**, launching one generation per shot. Remakes record their lineage back to the original analysis task, so the material's origin stays traceable. If no image model is available at all, the shot prompt is instead pre-filled into the Studio, and you generate after picking a model.

## My Works

**My Works** shows all your generation tasks (images, videos, hit analyses) as a waterfall, newest first. Desktop shows 2–6 per row, adapting to the window width.

### Card states

| State | Card |
|---|---|
| Pending / Queued / Generating | Gradient placeholder + status text + progress bar; cancelable |
| Completed | The finished image (videos show a cover and play icon); hovering floats a prompt excerpt with **Remake** and **Delete** buttons; multi-output shows a +N badge |
| Failed | Error explanation + **Regenerate** and **Delete** buttons; shows "N credits refunded" if refunded |
| Moderation failed | Shield icon + explanation |
| Canceled | Explanation text + **Delete** |

### Viewing details

Click a card to open the bottom detail sheet: large preview (with a thumbnail row underneath for multi-output), the full prompt, negative prompt (if any), model, type, parameters, credits actually spent, and credits refunded. Hit analysis tasks show their breakdown right in the sheet.

### Remake and regenerate

- **Remake**: refills the work's type, model, prompt, and parameters into the Studio as-is — submit directly, or tweak first.
- **Regenerate** (failed tasks only): refills parameters into the Studio the same way; adjust and submit again.

### Public vs. private

Works are private by default. Two ways to switch:

- The lock / globe button in the top-right corner of the detail sheet toggles between public and private.
- Bulk mode flips multiple works at once (below).

Public works appear in the Discovery Square, where other users can see the prompt and parameters and launch a remake.

### Bulk management

Click **Select** in the top toolbar (or **long-press** a card) to enter multi-select mode: a check circle appears in the top-left corner of each card, and the toolbar becomes "N selected" + **Select all**, **Make public**, **Make private**, **Delete**, **Exit**. Select works and click the action to apply it in bulk.

### Deleting

There are three delete entries: the delete button on a card's hover overlay, the delete button in the detail sheet, and the delete button in bulk mode. Deleting removes the work from the list immediately, and the Square stops showing it in sync.

> [!WARNING]
> Deleting cannot be undone. Multi-output works are deleted as a whole group.

## Discovery Square

The Discovery Square gathers public works from all users:

- **Type filter**: All / Image / Video / Digital human.
- **Keyword search**: type a keyword and press Enter to filter by prompt and other fields.
- Click any card to open the detail sheet, where you can view the full prompt and parameters and launch a **Remake** — other people's works only offer Remake, not public / delete buttons.

When the Square is empty, it says "No public works yet" with a **Go create** button that jumps to the Studio.

## Creative inspiration

**Inspiration** is the Creation module's default home page: a big "Create" title and subtitle at the top, the full generation panel in the middle, and 12 public works in the **Trending works** row below; **More** leads to the Discovery Square. It's a good place to browse for ideas — see something you like and remake it on the spot.

## Digital humans (coming soon)

Digital human generation (upload a look, pick a voice, and generate a talking-head video in one click) isn't open yet: selecting that type in the client only shows a "Digital human synthesis · coming soon" preview card, and the server rejects the type outright — nothing can be submitted today.

> [!NOTE]
> The code already contains a digital-human character picker (built-in characters + custom characters + voice selection, and a new-character form), but since the generation pipeline is closed, no entry point in the current build can open it. This section will be completed once the feature ships.

## Credits and spending

### Where to see your balance

When signed in, a credit badge sits at the bottom of the client's main sidebar: a lightning icon + your current credit count (when the sidebar is collapsed it shows only the abbreviated number, like 1.2k). Hover to see the balance and this month's chat quota progress; click to enter the **Membership Center**, which is also where you top up.

### How generation is billed

- Before submitting, the left of the submit button shows the selected model's unit price, "N credits / item".
- The charge happens on successful submission (not when generation finishes), and the balance refreshes immediately.
- Each work's actual spend is recorded in the **Spend** field of its detail sheet.

### Refunds on failure

When a task fails or is canceled, the credits already charged are refunded automatically: the card shows "N credits refunded", a refund notice pops at the top of the module, and the balance refreshes in sync. The **Refunds** field in the detail sheet shows the cumulative amount refunded.

### Topping up

If credits are insufficient, submission fails outright with "Insufficient credits — please top up first". Click the credit badge in the sidebar to enter the Membership Center and browse top-up packages.

## Connection status and common errors

The module's status strips only appear when something's off:

| Message | Meaning | What to do |
|---|---|---|
| AIGC service address not configured — creation unavailable | Signed in, but the server has no creation service configured | Click **Go to settings** to configure it |
| Sign in to create / view works | Not signed in | Click **Sign in** |
| Realtime channel disconnected, falling back to 30s polling — progress may lag slightly | The realtime push dropped and polling every 30 seconds took over | Nothing to do; it switches back automatically on recovery |
| Network error — the works list may be stale | Network unavailable | Check your connection; it syncs automatically once back |

Common error notices: insufficient credits (top up first), the selected model doesn't match the type (re-pick — appears after switching types without re-picking a model), this model has been retired, content triggered moderation (revise the prompt), too many requests (try again later), and so on. Every error shows as a plain-language banner — no technical details ever leak through.

## About available models

The model list in the creation panel comes from the server-side catalog, provided separately for the **Image / Video / Hit analysis** types, each model with a display name and a credit price. Which models are available is configured by the administrator of your environment in the admin console, so the cloud SaaS and different self-hosted instances may offer different lineups; out of the box, only **Hit analysis** is enabled by default. If a type has no models, that environment hasn't connected the corresponding upstream service yet — contact your administrator. Digital human generation is not open yet.

# BiuMind Clipper

Chrome / Edge / Brave extension (Manifest V3) that saves the current page
or a text selection into BiuMind as a wiki source — one click in the
popup, or right-click → "保存选中文字到 BiuMind".

The clip POSTs to `POST /v1/wiki/projects/{pid}/sources/clip` on your
configured BiuMind brain server. From there the page lives in
`brain.sources` and the wiki ingest pipeline (P1) can promote it to
multi-page wiki content via the `/v1/wiki/projects/{pid}/ingest`
endpoint.

## Files

```
manifest.json     MV3 manifest (popup + background SW + options)
popup.html/.js    Popup UI: project picker + preview + clip
options.html/.js  Settings UI: server URL + JWT token, with test-connection
background.js    Service worker: right-click "save selection" handler
extract.js        Tab-side page extraction (Readability + Turndown)
icon{16,48,128}.png  Placeholder action / notification icons
vendor/          Upstream libs (Readability, Turndown) — see vendor/LICENSES.md
```

## Develop / sideload

1. Run `chrome://extensions/`, toggle **Developer mode**.
2. Click **Load unpacked** → pick `apps/webclip/`.
3. The clipper's icon appears in the toolbar; click it to open the
   popup. Open **Options** to configure server URL and JWT token.
4. The default keyboard shortcut is `Ctrl+Shift+S`
   (`Cmd+Shift+S` on macOS) — change at `chrome://extensions/shortcuts`.

## Configuration

Two values in `chrome://extensions/?id=…/options.html`:

| Field | Example | Notes |
| --- | --- | --- |
| **Server URL** | `https://api.biumind.com` | Brain base URL — no trailing `/v1/...`. |
| **JWT Token**  | `eyJhbGc…` | Copy from the Flutter client's settings page. |

The "test connection" button calls `GET /v1/wiki/projects` and reports
the project count. Save credentials are kept only in
`chrome.storage.local` — never synced or transmitted off-device.

## Endpoints used

```
GET  /v1/wiki/projects                      list projects (project picker)
POST /v1/wiki/projects/{pid}/sources/clip   save the clipped page
POST /v1/notes                              save the clipped page as a note
```

The popup's 「保存目标」switch picks between the two POST targets
(remembered in `chrome.storage.local` as `last_target`). Note clips
send `{title, content_md, source_url, author?}` — `author` only when
Readability produced a byline; older brain builds ignore the extra
`source_url`/`author` keys. The right-click menu flow always goes to
the wiki project.

The clip payload schema matches `wiki.api.clipReq` on brain:

```json
{
  "url":        "https://example.com/article",
  "title":      "Article title",
  "content_md": "# Article …",
  "tags":       [],
  "metadata":   { "via": "webclip", "extractor": "readability" }
}
```

## Auto-ingest

Tick **「保存后自动转成 wiki 页（CoT ingest）」** in the popup (or set it
as default in **Options**) and the extension chains a second POST after
each successful clip:

```http
POST /v1/wiki/projects/{pid}/ingest
{ "source_id": "<from clip response>", "raw_text": "<markdown>", "title": "..." }
```

Brain creates a row in `brain.ingest_tasks` and publishes
`brain.wiki.ingest.requested` to NATS; `workers/wiki-llm` picks it up,
runs the two-stage CoT prompt against your model-relay-configured model, and
streams `brain.wiki.ingest.update` events back so each emitted FILE
block lands as a wiki page in real time.

Both surfaces (popup button + right-click menu) share the same
`auto_ingest` flag in `chrome.storage.local`. Duplicate sources (same
URL + content sha256) skip the auto-ingest step on purpose — the
existing source already has wiki pages from its first ingest, and
re-running would just create duplicates.

## Vendor / license

The extension code is Apache-2.0 (matching the rest of biumind). Vendor
libraries inside `vendor/` are upstream open-source — see
`vendor/LICENSES.md`. Re-implemented from the knowcode reference
extension's UX rather than imported, so no GPL inheritance.

# Vendored Miniflux v2 reader packages

Curated vendor of the parsing layer of [Miniflux 2](https://github.com/miniflux/v2)
(Apache-2.0). Used by `biu/biuapp/rss` for RSS / Atom / RDF / JSON Feed
parsing, sanitizing, readability extraction, URL normalization and content
filtering.

## Upstream

- Repository: <https://github.com/miniflux/v2>
- Commit: `87d78916009b64e5d4b6d41c7dfa7766d70dacf6`
- Vendored at: 2026-06-07

## Scope (what was vendored)

Parsing chain — driven by [`parser`](./parser):

- `parser/` — entry point, format auto-detection
- `atom/` — Atom 0.3 + 1.0
- `rss/` — RSS 0.9 / 2.0
- `rdf/` — RSS 1.0 (RDF)
- `json/` — JSON Feed 1.x
- `encoding/` — charset sniffing
- `xml/` — CDATA-safe XML reader
- `date/` — tolerant date parsing

Namespace extensions (referenced by atom/rss):

- `dublincore/`, `googleplay/`, `itunes/`, `media/`

Post-processing:

- `sanitizer/` — HTML XSS sanitizer
- `rewrite/` — content rewrite rules
- `filter/` — block/keep filters
- `urlcleaner/` — URL normalization
- `readability/` + `readingtime/` — readability + reading-time estimation

Supporting packages (transitive deps from `internal/`):

- `model/` — only the struct types `Feed`, `Entry`, `Entries`, `Enclosure`,
  `EnclosureList`, `Category`, `Icon`, `FeedIcon`. Methods that touched the
  database, the User type, or `config.Opts` deeply have been removed.
- `urllib/` — pure stdlib URL helpers, vendored as-is.
- `crypto/` — small crypto helpers used for `crypto.Hash` and SHA-256.
- `config/` — **SHIM**. The real config bag is huge; we expose a stub `Opts`
  whose methods (`InvidiousInstance`, `YouTubeEmbedDomain`,
  `YouTubeEmbedUrlOverride`, plus `NewConfigOptions`/`NewConfigParser`/
  `ParseEnvironmentVariables` for the test files) all return zero values.

## Excluded (and why)

| Upstream package | Why we did not vendor |
|---|---|
| `handler/`, `processor/`, `subscription/`, `fetcher/`, `icon/`, `opml/` | HTTP / orchestration layer — BiuMind ships its own |
| `internal/storage/` | DB-backed; BiuMind uses its own `rss.feeds` / `rss.entries` schema |
| `internal/proxyrotator/`, `internal/locale/`, `internal/integration/`, `internal/metric/` | not needed by the parsing chain |
| `internal/mediaproxy/` | media proxy is a runtime feature; we strip the methods that needed it |
| `internal/config/` (real) | too coupled to env / CLI; replaced with the shim above |

## Sync policy

Re-syncing means:

1. Pull upstream commit, diff `internal/reader/{<vendored subdirs>}` against
   our copy.
2. Replay every entry in `VENDOR_NOTES.md` (deleted methods, `t.Skip`
   markers, the `new(value)` → pre-1.26 fix, etc.).
3. Bump the commit hash in this file.

Do **not** introduce new dependencies on `internal/storage`, `internal/locale`,
or `internal/integration`. If a new upstream change pulls those in, prefer
commenting out the branch and recording it in `VENDOR_NOTES.md`.

## License

Apache-2.0. See [`LICENSE`](./LICENSE), verbatim from upstream Miniflux v2.

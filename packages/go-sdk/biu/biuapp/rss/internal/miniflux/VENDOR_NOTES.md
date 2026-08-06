# Vendor surgical changes

Every spot below is a deliberate deviation from upstream Miniflux 2
(`87d78916009b64e5d4b6d41c7dfa7766d70dacf6`). When re-syncing, replay each one.

## Deleted files (under `model/`)

The following files were dropped wholesale because they pull in `internal/config`,
`internal/locale`, `internal/storage`, `internal/integration`, `bcrypt` (auth) or
otherwise have nothing to do with feed parsing:

- `model/api_key.go`
- `model/categories_sort_options.go`
- `model/integration.go`
- `model/job.go`
- `model/subscription.go`
- `model/theme.go`
- `model/user.go`
- `model/web_session.go`
- `model/web_session_test.go`
- `model/webauthn.go`
- `model/home_page.go`
- `model/feed_test.go` (only tested the deleted `ScheduleNextCheck`)
- `model/enclosure_test.go` (only tested the deleted `ProxifyEnclosureURL`)

## In-file edits

### `model/feed.go`
- Removed `import "miniflux.app/v2/internal/config"`.
- Removed `func (f *Feed) ScheduleNextCheck(...)` (used `config.Opts.SchedulerRoundRobinMinInterval()` etc. — BiuMind has its own scheduler).

### `model/entry.go`
- Removed `func (e *Entry) ShouldMarkAsReadOnView(user *User)` (depended on the deleted `model.User` type).

### `model/enclosure.go`
- Removed `import "miniflux.app/v2/internal/mediaproxy"`.
- Removed `func (e *Enclosure) ProxifyEnclosureURL(...)`.
- Removed `func (el EnclosureList) ProxifyEnclosureURL(...)`.

### `model/model.go`
- Rewrote `func SetOptionalField[T any](value T) *T` body from the Go 1.26
  `return new(value)` form to a 1.25-compatible `v := value; return &v`. The
  module is on `go 1.25.7`.

### `config/config.go` (SHIM, new file — not from upstream)
- Replaces upstream `internal/config`. Exports `Opts`, `Options`,
  `NewConfigOptions`, `NewConfigParser`, `(*Parser).ParseEnvironmentVariables`.
- All accessor methods (`InvidiousInstance`, `YouTubeEmbedDomain`,
  `YouTubeEmbedUrlOverride`) return `""` so the vendored sanitizer/rewrite
  code falls back to "no rewriting".

## Skipped tests

Upstream tests below set `INVIDIOUS_INSTANCE` / `YOUTUBE_EMBED_URL_OVERRIDE` env
vars and assume `config.NewConfigParser().ParseEnvironmentVariables()` honours
them. Our config shim ignores the environment, so those expectations no longer
hold. Each test was prefixed with a `t.Skip` (single-line marker
"VENDOR-NOTE: requires real config.Opts (YouTube/Invidious env vars)"):

- `sanitizer/sanitizer_test.go::TestInvidiousIFrame` (line 425)
- `sanitizer/sanitizer_test.go::TestCustomYoutubeEmbedURL` (line 468)
- `sanitizer/sanitizer_test.go::TestIFrameWithReferrerPolicy` (line 498)
- `sanitizer/sanitizer_test.go::TestReplaceYoutubeURL` (line 759)
- `sanitizer/sanitizer_test.go::TestReplaceSecureYoutubeURL` (line 777)
- `sanitizer/sanitizer_test.go::TestReplaceSecureYoutubeURLWithParameters` (line 795)
- `sanitizer/sanitizer_test.go::TestReplaceProtocolRelativeYoutubeURL` (line 831)
- `sanitizer/sanitizer_test.go::TestReplaceYoutubeURLWithCustomURL` (line 849)
- `rewrite/content_rewrite_test.go::TestRewriteYoutubeVideoLink` (line 69)
- `rewrite/content_rewrite_test.go::TestRewriteYoutubeShortLink` (line 89)
- `rewrite/content_rewrite_test.go::TestRewriteYoutubeLinkAndCustomEmbedURL` (line 129)
- `rewrite/content_rewrite_test.go::TestRewriteYoutubeVideoLinkUsingInvidious` (line 157)
- `rewrite/content_rewrite_test.go::TestRewriteYoutubeShortLinkUsingInvidious` (line 177)
- `rewrite/content_rewrite_test.go::TestAddYoutubeVideoFromId` (line 197)
- `rewrite/content_rewrite_test.go::TestAddYoutubeVideoFromIdWithCustomEmbedURL` (line 236)

(Line numbers refer to the original upstream file — search for the function
name when re-syncing, since added skip lines shift them.)

## Import path rewrites

Every `miniflux.app/v2/internal/{model,urllib,crypto,config,reader/...}`
import was rewritten to
`github.com/biumind/biumind/packages/go-sdk/biu/biuapp/rss/internal/miniflux/...`
via sed. The two leftover upstream imports — `internal/mediaproxy` and
`internal/timezone` — were eliminated by the model-file edits above.

## External dependencies added to the parent module

The vendored code pulls these in via the parent `packages/go-sdk/biu`
`go.mod`. None were transitive before:

- `github.com/PuerkitoBio/goquery` (readability)
- `github.com/andybalholm/cascadia` (transitive of goquery)

`golang.org/x/net`, `golang.org/x/text`, and `golang.org/x/crypto` were
already in the module graph as indirect dependencies and remain so.

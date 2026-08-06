// /break-cache slash — defeat prompt caching for the next request.
//
// Anthropic's prompt cache hashes the prefix of system + tools +
// messages. When you're debugging a flaky reply you sometimes want
// to be sure the model regenerated from scratch rather than
// returning a cached answer. /break-cache injects a one-shot nonce
// into the system prompt for the next turn — the hash differs, so
// the cache misses, the response is guaranteed fresh.
//
// State semantics:
//
//   - The flag is one-shot. After the next ChatStream call it's
//     cleared automatically (see model.applyCacheBreaker).
//   - /break-cache toggles when called twice without an intervening
//     send: a second invocation says "actually, never mind".
//   - It does not affect any subsequent request, so it's safe to
//     leave armed across slash commands.

package repl

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

func (m model) handleBreakCache(parts []string) (model, string) {
	if m.cacheBreaker != "" {
		m.cacheBreaker = ""
		return m, "/break-cache: cleared (next request will use the cache normally)"
	}
	m.cacheBreaker = newCacheNonce()
	return m, "/break-cache: armed — next request will skip the prompt cache (nonce: " +
		m.cacheBreaker[:8] + "…)"
}

// applyCacheBreaker returns the system prompt with the nonce baked
// in if the breaker is armed. The breaker should be cleared by the
// caller after the request lands.
//
// We append rather than prepend: the cache hash includes the system
// prefix, so any change in the body invalidates the prefix-cached
// portion. Trailing comment is the cheapest way and stays out of
// the model's "real" view because the comment marker is universally
// ignored.
func applyCacheBreaker(system, nonce string) string {
	if nonce == "" {
		return system
	}
	suffix := fmt.Sprintf("\n\n<!-- cache-break: %s -->", nonce)
	return system + suffix
}

func newCacheNonce() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

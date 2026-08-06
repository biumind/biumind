// Chat-mode tool whitelist (Q1, Runtime v3 §4).
//
// Chat is a lightweight Q&A surface. It is advertised exactly the
// read-only knowledge/utility tools below and nothing else — even if more
// cloud-runtime tools get registered later for agent/task mode. The gate
// is **default-deny**: a tool not in the allowlist is never advertised to
// a chat-mode LLM, regardless of its Runtime mask.
//
// Mirrors lobehub's `chatModeAllowedToolIds` + `enableChecker(default
// false)`: the backend is authoritative; the client only displays.
//
// Enforcement lives at tool-set construction in BOTH chat kernels:
//   - RunV2  (BYOK WS path)        → Registry.AvailableForBiumindkit(allow)
//   - Run    (shared-pool SSE path) → tools.FilterChatAllowed(Available, allow)
// The allowlist is carried on chat.AgentLoop.ChatToolAllowlist; a nil
// allowlist means "no chat restriction" (kernel-mechanics tests and any
// non-chat caller), so production must set it explicitly.

package tools

// DefaultChatToolAllowlist is the canonical chat-mode whitelist. Names
// align with registration in cmd/brain/main.go (TimeNow / WebSearch /
// WikiSearch / MemoryRecall). Keep in sync when adding chat-safe tools.
var DefaultChatToolAllowlist = map[string]struct{}{
	"websearch":     {},
	"wiki_search":   {},
	"memory_recall": {},
	"time_now":      {},
}

// WikiAgentToolAllowlist (S3 P0-1) is the wiki autonomous-maintenance
// agent loop's专用 whitelist. It widens DefaultChatToolAllowlist with
// the wiki write tools (create/update/merge_page) so the agent loop can
// "read sources → mutate pages → maintain backlinks". Plain chat is
// unaffected — it still uses DefaultChatToolAllowlist (read-only,
// default-deny). The wiki agent loop entrypoint (POST /v1/wiki/.../agent/run)
// sets AgentLoop.ChatToolAllowlist = WikiAgentToolAllowlist explicitly.
//
// Write-tool safety does NOT rest on this whitelist (it's just an
// LLM-advertised surface gate): the store layer enforces create-only /
// version乐观锁 + page_revisions rollback (S2 ④).
var WikiAgentToolAllowlist = map[string]struct{}{
	"websearch":        {},
	"wiki_search":      {},
	"memory_recall":    {},
	"time_now":         {},
	"wiki_create_page": {},
	"wiki_update_page": {},
	"wiki_merge_pages": {},
}

// chatAllows reports whether name passes the allowlist. A nil allowlist
// means "no chat restriction" — every tool passes (used by mechanics
// tests and non-chat callers).
func chatAllows(allow map[string]struct{}, name string) bool {
	if allow == nil {
		return true
	}
	_, ok := allow[name]
	return ok
}

// FilterChatAllowed returns the subset of ds permitted under allow,
// preserving order. A nil allow returns ds unchanged. Used by the legacy
// AgentLoop.Run path, which advertises tools.Descriptor lists directly.
func FilterChatAllowed(ds []Descriptor, allow map[string]struct{}) []Descriptor {
	if allow == nil {
		return ds
	}
	out := make([]Descriptor, 0, len(ds))
	for _, d := range ds {
		if _, ok := allow[d.Name]; ok {
			out = append(out, d)
		}
	}
	return out
}

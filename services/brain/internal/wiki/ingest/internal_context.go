// Ingest-context internal endpoint consumed by workers/wiki-llm's
// two-stage pipeline (P2 #17). Before the stage-1 analysis LLM call,
// the worker pulls the project's purpose page, schema page, and a
// lightweight page index so the analysis (and the stage-2 FILE-block
// generation that consumes it) can:
//
//   - align new pages with the project's declared purpose / page
//     conventions (templates seed these as type:purpose / type:schema
//     pages — see internal/wiki/templates);
//   - [[wikilink]] to existing pages by exact title instead of
//     inventing near-duplicate pages.
//
// Auth + owner pairing follow the same contract as handleGetSource:
// X-Biumind-Internal-Token shared secret, and owner_id must match the
// project's owner or the endpoint answers 404 (no existence leak).
//
// Truncation policy (worker prompt budget): purpose / schema bodies are
// cut at 4000 runes each (rune-safe for CJK), the page index at 200
// entries; pages_total always reports the untruncated count so the
// worker can tell the model "… and N more".
package ingest

import (
	"errors"
	"net/http"

	wikistore "github.com/biumind/biumind/services/brain/internal/wiki/store"
	"github.com/google/uuid"
)

const (
	// ingestContextBodyRunes caps each of purpose / schema body_md.
	// 4000 runes ≈ 1-2K tokens — enough for the template conventions,
	// small enough to leave prompt budget for the source itself.
	ingestContextBodyRunes = 4000
	// ingestContextPageLimit caps the page index. Beyond a few hundred
	// titles the marginal linking value drops off and the list starts
	// crowding the source out of the context window.
	ingestContextPageLimit = 200
)

// handleIngestContext serves
// GET /v1/internal/wiki/projects/{pid}/ingest-context?owner_id=<uuid>.
func (s *InternalServer) handleIngestContext(w http.ResponseWriter, r *http.Request) {
	pid, err := uuid.Parse(r.PathValue("pid"))
	if err != nil {
		writeInternalErr(w, http.StatusBadRequest, "bad_id", err.Error())
		return
	}
	owner, err := uuid.Parse(r.URL.Query().Get("owner_id"))
	if err != nil {
		writeInternalErr(w, http.StatusBadRequest, "bad_owner_id", "owner_id required")
		return
	}
	if s.Wiki == nil {
		writeInternalErr(w, http.StatusServiceUnavailable, "wiki_store_not_configured", "")
		return
	}
	proj, err := s.Wiki.GetProject(r.Context(), pid)
	if err != nil {
		if errors.Is(err, wikistore.ErrNotFound) {
			writeInternalErr(w, http.StatusNotFound, "not_found", "")
			return
		}
		writeInternalErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	// Owner 配对（同 handleGetSource）：不匹配 → 404 不泄存在。
	if proj.OwnerID != owner {
		writeInternalErr(w, http.StatusNotFound, "not_found", "")
		return
	}

	purpose, purposeTrunc := s.typedPageBody(r, pid, "purpose")
	schema, schemaTrunc := s.typedPageBody(r, pid, "schema")

	entries, total, err := s.Wiki.ListPageIndex(r.Context(), pid, ingestContextPageLimit)
	if err != nil {
		writeInternalErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	pages := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		pages = append(pages, map[string]any{"title": e.Title, "type": e.Type})
	}
	writeInternalJSON(w, http.StatusOK, map[string]any{
		"project_id":        pid.String(),
		"purpose":           purpose,
		"purpose_truncated": purposeTrunc,
		"schema":            schema,
		"schema_truncated":  schemaTrunc,
		"pages":             pages,
		"pages_total":       total,
	})
}

// typedPageBody loads the project's oldest page of the given frontmatter
// type and returns its body_md truncated to ingestContextBodyRunes. A
// project without such a page (blank template) yields ("", false) —
// not an error, the worker simply omits that section from the prompt.
func (s *InternalServer) typedPageBody(r *http.Request, pid uuid.UUID, typ string) (string, bool) {
	page, err := s.Wiki.GetPageByType(r.Context(), pid, typ)
	if err != nil {
		if !errors.Is(err, wikistore.ErrNotFound) {
			s.Logger.Warn("ingest-context: typed page lookup failed",
				"project", pid, "type", typ, "err", err)
		}
		return "", false
	}
	return truncateRunes(page.BodyMd, ingestContextBodyRunes)
}

// truncateRunes cuts s at n runes (never mid-codepoint — purpose/schema
// pages are CJK-heavy). Reports whether truncation happened.
func truncateRunes(s string, n int) (string, bool) {
	runes := []rune(s)
	if len(runes) <= n {
		return s, false
	}
	return string(runes[:n]), true
}

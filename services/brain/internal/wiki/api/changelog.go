// Page changelog endpoint — chronological event timeline for one page.
//
//	GET /v1/wiki/projects/{pid}/pages/{id}/changelog
//
// Returns up to 200 events from brain.events scoped to this page,
// newest first. Event payloads are passed through; the UI decides
// what to render per event_type:
//
//	page.created   {page_id, title}
//	page.updated   {page_id, title?}
//	page.deleted   {page_id}
//	block.created  {page_id, block_id, type, content}
//	block.updated  {page_id, block_id, type, content}
//	block.deleted  {page_id, block_id, type}
//	page.merged_*  (deferred — merge events don't currently carry page_id)
//
// "Diff per change" is intentionally NOT computed here — the events
// already contain the new content, and the UI shows that as the change.
// True structural diff (between consecutive block.updated events) is
// a v2 feature.

package api

import (
	"net/http"
	"time"

	"github.com/google/uuid"
)

type changelogEntry struct {
	ID        int64          `json:"id"`
	Type      string         `json:"type"`
	ActorType string         `json:"actor_type"`
	ActorID   string         `json:"actor_id"`
	Payload   map[string]any `json:"payload,omitempty"`
	CreatedAt string         `json:"created_at"`
}

func (s *Server) handleListChangelog(w http.ResponseWriter, r *http.Request) {
	pid, err := uuid.Parse(r.PathValue("pid"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_project_id", "")
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_id", "")
		return
	}
	if !s.ownsProject(w, r, pid) {
		return
	}
	// Verify the page belongs to this project (don't leak event history
	// across projects even if the caller can guess uuids).
	page, err := s.Store.GetPage(r.Context(), id)
	if err != nil || page.ProjectID != pid {
		writeErr(w, http.StatusNotFound, "not_found", "")
		return
	}
	events, err := s.Store.ListPageEvents(r.Context(), pid, id, 200)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	out := make([]changelogEntry, 0, len(events))
	for _, e := range events {
		out = append(out, changelogEntry{
			ID:        e.ID,
			Type:      e.EventType,
			ActorType: e.ActorType,
			ActorID:   e.ActorID,
			Payload:   e.Payload,
			CreatedAt: e.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": out})
}

package api

// selection_edit.go — S3 P1-6 inline selection edit/ask HTTP entry.
//
// POST /v1/wiki/projects/{pid}/pages/{id}/selection-edit
//
// The user selects a span in the Milkdown editor (ProseMirror) and gives an
// instruction. The host (Flutter) calls this endpoint with the selected text +
// surrounding context. We forward to model-relay via enrich.RelayLLMCaller
// (I6: business service never imports an LLM SDK directly). The host then
// writes the replacement back via replaceSelection (PM tr.replaceWith), which
// fires the normal autosave path — no DB write here.
//
// Phase A ships `mode=edit` only. `mode=ask` (KB top5 + [1][2] citations)
// lands in phase B.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/biumind/biumind/services/brain/internal/search/bm25"
)

func (s *Server) handleSelectionEdit(w http.ResponseWriter, r *http.Request) {
	pid, err := uuid.Parse(r.PathValue("pid"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_project_id", "")
		return
	}
	pageID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_page_id", "")
		return
	}
	uid := mustUserID(r)
	proj, err := s.Store.GetProject(r.Context(), pid)
	if err != nil || proj.OwnerID != uid {
		writeErr(w, http.StatusNotFound, "not_found", "project")
		return
	}
	if pg, err := s.Store.GetPage(r.Context(), pageID); err != nil || pg.ProjectID != pid {
		writeErr(w, http.StatusNotFound, "not_found", "page")
		return
	}
	if s.Selection == nil {
		writeErr(w, http.StatusServiceUnavailable, "selection_edit_disabled",
			"model-relay not configured (MODEL_RELAY_URL unset)")
		return
	}
	var req selectionEditReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if strings.TrimSpace(req.Selection) == "" {
		writeErr(w, http.StatusBadRequest, "empty_selection",
			"selection text is required")
		return
	}
	if strings.TrimSpace(req.Instruction) == "" {
		writeErr(w, http.StatusBadRequest, "empty_instruction",
			"instruction is required")
		return
	}

	switch req.Mode {
	case "edit":
		user := buildSelectionEditUser(req)
		out, err := s.Selection.Chat(r.Context(), uid, selectionEditSystemPrompt, user)
		if err != nil {
			writeErr(w, http.StatusBadGateway, "llm_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"replacement": normalizeReplacement(out),
		})
	case "ask":
		// Phase B: BM25 top5 same-project KB + [N] citations.
		if s.BM25 == nil {
			writeErr(w, http.StatusServiceUnavailable, "search_disabled",
				"BM25 searcher not configured")
			return
		}
		hits, err := s.BM25.Search(r.Context(), req.Instruction, bm25.SearchOptions{
			ProjectID:     &pid,
			OwnerID:       uid,
			IncludeBlocks: false,
			Limit:         5,
		})
		if err != nil {
			writeErr(w, http.StatusBadGateway, "search_failed", err.Error())
			return
		}
		var kb strings.Builder
		citations := make([]map[string]any, 0, len(hits))
		for i, h := range hits {
			n := i + 1
			citations = append(citations, map[string]any{
				"n":       n,
				"page_id": h.PageID,
				"title":   h.Title,
			})
			fmt.Fprintf(&kb, "[%d] %s\n", n, h.Title)
		}
		user := buildSelectionAskUser(req, kb.String())
		out, err := s.Selection.Chat(r.Context(), uid, selectionAskSystemPrompt, user)
		if err != nil {
			writeErr(w, http.StatusBadGateway, "llm_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"answer":    out,
			"citations": citations,
		})
	default:
		writeErr(w, http.StatusBadRequest, "bad_mode",
			"mode must be 'ask' or 'edit'")
	}
}

type selectionEditReq struct {
	// Mode: "ask" (Q&A + citations, phase B) | "edit" (rewrite selection, phase A).
	Mode string `json:"mode"`
	// Selection is the exact selected span (ProseMirror text between from..to).
	Selection string `json:"selection"`
	// Before / After are up to ~1200 chars of surrounding body context, so the
	// model can keep tone/voice/wikilinks consistent without rewriting the page.
	Before string `json:"before"`
	After  string `json:"after"`
	// Instruction is the user's directive ("make it concise", "translate to EN", ...).
	Instruction string `json:"instruction"`
}

const selectionEditSystemPrompt = `You are the BiuMind wiki inline editor. The user selected a span of text in a wiki page and gave an instruction. Rewrite ONLY the selected span so it satisfies the instruction and fits the surrounding context.

Rules:
- Output ONLY the replacement text. No explanation, no preamble, no markdown code fence.
- Keep the same language and tone as the surrounding text unless told otherwise.
- Preserve inline markdown that was inside the selection ([[wikilinks]], **bold**, ` + "`code`" + `) unless the instruction explicitly changes them.
- Do not add headings or block markup the user didn't ask for.
- If the instruction is impossible or unclear, output the original selection unchanged.`

func buildSelectionEditUser(req selectionEditReq) string {
	return "Instruction:\n" + strings.TrimSpace(req.Instruction) +
		"\n\n--- text BEFORE selection (context only, do not repeat) ---\n" + req.Before +
		"\n\n--- SELECTED SPAN (rewrite this) ---\n" + req.Selection +
		"\n\n--- text AFTER selection (context only, do not repeat) ---\n" + req.After
}

const selectionAskSystemPrompt = `You are the BiuMind wiki inline assistant. The user selected a span of text in a wiki page and asked a question about it. Answer using the knowledge-base entries below; cite with [N] markers matching their [N] prefixes.

Rules:
- Answer in the user's language, concisely.
- Cite KB entries inline as [1], [2]... where they support a claim.
- If the KB doesn't cover it, say so plainly — do not fabricate citations.
- The selected text + surrounding context is what the user is focused on; relate your answer to it.`

func buildSelectionAskUser(req selectionEditReq, kb string) string {
	var b strings.Builder
	b.WriteString("Question:\n")
	b.WriteString(strings.TrimSpace(req.Instruction))
	if kb != "" {
		b.WriteString("\n\n--- Knowledge base (cite as [N]) ---\n")
		b.WriteString(kb)
	}
	b.WriteString("\n\n--- selected text (the span the user is asking about) ---\n")
	b.WriteString(req.Selection)
	if req.Before != "" || req.After != "" {
		b.WriteString("\n\n--- surrounding context (do not repeat) ---\nbefore:\n")
		b.WriteString(req.Before)
		b.WriteString("\nafter:\n")
		b.WriteString(req.After)
	}
	return b.String()
}

// normalizeReplacement 剥 LLM 常裹的外层 markdown fence + 首尾空白。模型常
// 无视 "no fence" 要求，这里兜底——host 写回时不希望多一层 ``` 包裹。
func normalizeReplacement(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	// 形如 ```lang\n...\n``` 或 ```\n...\n```
	firstNL := strings.IndexByte(s, '\n')
	if firstNL < 0 {
		return s
	}
	rest := strings.TrimSpace(s[firstNL+1:])
	if strings.HasSuffix(rest, "```") {
		return strings.TrimSpace(strings.TrimSuffix(rest, "```"))
	}
	return rest
}

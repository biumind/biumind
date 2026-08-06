// Package activity —— wiki 项目活动聚合。
//
// GET /v1/wiki/projects/{pid}/activity
//
// 实时来源：brain.ingest_tasks + brain.research_tasks 两张已有的 task 表。
// 后续 lint/dedup/sweep 加入后扩 UNION ALL 即可。客户端 reducer
// （activity_state.dart）期望的 schema：
//
//	{
//	  id: <task_id>,
//	  raw_kind: 'ingest_task' | 'research_task' | ...
//	  status: 'running' | 'done' | 'failed' | 'cancelled' | 'unknown',
//	  label: 'Ingest <filename>' | 'Research: <topic>' | ...
//	  started_at: <iso>,
//	  last_updated_at: <iso>,
//	  summary: { ... 自由 dict，UI 卡片自取 ... }
//	  cancelable: bool
//	}
//
// status 映射规则（per-source）：
//
//	ingest_tasks.status
//	  pending / running / partial → running
//	  done                        → done
//	  failed                      → failed
//	  cancelled                   → cancelled
//
//	research_tasks.status（queued/running/done/failed）
//	  queued / running            → running
//	  done                        → done
//	  failed                      → failed
package activity

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	wikistore "github.com/biumind/biumind/services/brain/internal/wiki/store"
	"github.com/biumind/biumind/services/brain/internal/wiki/wikicommon"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Server struct {
	Pool     *pgxpool.Pool
	Wiki     *wikistore.Store
	Verifier *bauth.Verifier
	Logger   *slog.Logger
}

func NewServer(p *pgxpool.Pool, w *wikistore.Store, v *bauth.Verifier, l *slog.Logger) *Server {
	return &Server{Pool: p, Wiki: w, Verifier: v, Logger: l}
}

func (s *Server) Mount(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/wiki/projects/{pid}/activity",
		wikicommon.RequireAuth(s.Verifier, s.handleList))
}

type item struct {
	ID            string         `json:"id"`
	RawKind       string         `json:"raw_kind"`
	Status        string         `json:"status"`
	Label         string         `json:"label"`
	Phase         string         `json:"phase,omitempty"`
	Cancelable    bool           `json:"cancelable"`
	StartedAt     string         `json:"started_at"`
	LastUpdatedAt string         `json:"last_updated_at"`
	Summary       map[string]any `json:"summary"`
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	pid, err := uuid.Parse(r.PathValue("pid"))
	if err != nil {
		wikicommon.WriteErr(w, http.StatusBadRequest, "bad_project_id", "")
		return
	}
	uid := wikicommon.MustUserID(r)
	proj, err := s.Wiki.GetProject(r.Context(), pid)
	if err != nil || proj.OwnerID != uid {
		wikicommon.WriteErr(w, http.StatusNotFound, "not_found", "project")
		return
	}

	items := make([]item, 0, 50)

	if rows, err := s.fetchIngest(r.Context(), pid); err == nil {
		items = append(items, rows...)
	} else {
		s.Logger.Warn("activity ingest fetch failed", "err", err)
	}
	if rows, err := s.fetchResearch(r.Context(), pid); err == nil {
		items = append(items, rows...)
	} else {
		s.Logger.Warn("activity research fetch failed", "err", err)
	}

	// 简单全局排序：last_updated_at desc
	for i := 0; i < len(items)-1; i++ {
		for j := i + 1; j < len(items); j++ {
			if items[j].LastUpdatedAt > items[i].LastUpdatedAt {
				items[i], items[j] = items[j], items[i]
			}
		}
	}

	wikicommon.WriteJSON(w, http.StatusOK, map[string]any{
		"items":       items,
		"next_cursor": nil,
	})
}

func (s *Server) fetchIngest(ctx context.Context, pid uuid.UUID) ([]item, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id, status, title, progress, result_pages,
		       cancel_requested_at, started_at, finished_at, created_at, updated_at,
		       error
		FROM brain.ingest_tasks
		WHERE project_id = $1
		ORDER BY created_at DESC
		LIMIT 100
	`, pid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]item, 0, 50)
	for rows.Next() {
		var (
			id                   uuid.UUID
			status, title        string
			progressJSON         []byte
			resultPages          []uuid.UUID
			cancelReq, started   *time.Time
			finished             *time.Time
			createdAt, updatedAt time.Time
			errMsg               string
		)
		if err := rows.Scan(&id, &status, &title, &progressJSON, &resultPages,
			&cancelReq, &started, &finished, &createdAt, &updatedAt, &errMsg); err != nil {
			return nil, err
		}
		var prog map[string]any
		if len(progressJSON) > 0 {
			_ = json.Unmarshal(progressJSON, &prog)
		}
		summary := map[string]any{
			"task_id":         id.String(),
			"pages_completed": len(resultPages),
		}
		if title != "" {
			summary["source_filename"] = title
		}
		if errMsg != "" {
			summary["error"] = errMsg
		}
		// Merge progress payload（worker 写的 phase/percent/eta 等）
		for k, v := range prog {
			summary[k] = v
		}
		phase := ""
		if p, ok := prog["phase"].(string); ok {
			phase = p
		}
		st := mapIngestStatus(status, cancelReq != nil)
		startTime := createdAt
		if started != nil {
			startTime = *started
		}
		out = append(out, item{
			ID:            id.String(),
			RawKind:       "ingest_task",
			Status:        st,
			Label:         labelForIngest(title),
			Phase:         phase,
			Cancelable:    st == "running",
			StartedAt:     startTime.UTC().Format(time.RFC3339),
			LastUpdatedAt: updatedAt.UTC().Format(time.RFC3339),
			Summary:       summary,
		})
	}
	return out, rows.Err()
}

func (s *Server) fetchResearch(ctx context.Context, pid uuid.UUID) ([]item, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id, status, topic, queries, page_id, error_message,
		       created_at, updated_at
		FROM brain.research_tasks
		WHERE project_id = $1
		ORDER BY created_at DESC
		LIMIT 100
	`, pid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]item, 0, 50)
	for rows.Next() {
		var (
			id                   uuid.UUID
			status, topic        string
			queries              []string
			pageID               *uuid.UUID
			errMsg               *string
			createdAt, updatedAt time.Time
		)
		if err := rows.Scan(&id, &status, &topic, &queries, &pageID,
			&errMsg, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		summary := map[string]any{
			"task_id":       id.String(),
			"topic":         topic,
			"queries_count": len(queries),
		}
		if pageID != nil {
			summary["page_id"] = pageID.String()
		}
		if errMsg != nil && *errMsg != "" {
			summary["error"] = *errMsg
		}
		st := mapResearchStatus(status)
		out = append(out, item{
			ID:            id.String(),
			RawKind:       "research_task",
			Status:        st,
			Label:         labelForResearch(topic),
			StartedAt:     createdAt.UTC().Format(time.RFC3339),
			LastUpdatedAt: updatedAt.UTC().Format(time.RFC3339),
			Summary:       summary,
		})
	}
	return out, rows.Err()
}

func mapIngestStatus(s string, cancelRequested bool) string {
	if cancelRequested && (s == "pending" || s == "running" || s == "partial") {
		// brain ingest 接到 cancel 请求但 worker 还没消费完时，UI 提前
		// 显示 cancelled 让用户知道 cancel 在进行。worker 真正终态后会
		// 写回 status='cancelled' 覆盖。
		return "cancelled"
	}
	switch s {
	case "pending", "running", "partial":
		return "running"
	case "done":
		return "done"
	case "failed":
		return "failed"
	case "cancelled":
		return "cancelled"
	default:
		return "unknown"
	}
}

func mapResearchStatus(s string) string {
	switch s {
	case "queued", "running":
		return "running"
	case "done":
		return "done"
	case "failed":
		return "failed"
	default:
		return "unknown"
	}
}

func labelForIngest(filename string) string {
	if filename == "" {
		return "Ingest"
	}
	return "Ingest " + filename
}

func labelForResearch(topic string) string {
	if topic == "" {
		return "Research"
	}
	if len(topic) > 60 {
		topic = topic[:60] + "…"
	}
	return "Research: " + topic
}

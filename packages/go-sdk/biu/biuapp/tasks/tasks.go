// Package tasks — minimal task / calendar app exposed as a BiuApp.
//
// In-memory store keyed by user id. The MVP intentionally avoids a SQL
// dependency so the app can run anywhere; durable persistence lands when
// we wire it through Brain.Wiki (P6.2.5).
//
// Actions:
//
//	create   in: {title, due?, tags[]?}                      out: Task
//	list     in: {owner?, done?, tag?}                        out: {tasks[]}
//	complete in: {id}                                         out: Task
//	delete   in: {id}                                         out: {ok}
//	get      in: {id}                                         out: Task
package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/biumind/biumind/packages/go-sdk/biu/biuapp"
	"github.com/google/uuid"
)

const Name = "tasks"

type App struct {
	mu    sync.Mutex
	store map[string]*Task // id → task

	// path — when non-empty, every mutation is persisted to disk via
	// atomic write-temp + rename. Empty path = in-memory only.
	path string
}

func New() *App { return &App{store: map[string]*Task{}} }

// NewWithFile builds the app with file-backed persistence at `path`.
// The file is loaded on construction; missing file is fine (treated as
// empty). Subsequent mutations write atomically.
func NewWithFile(path string) (*App, error) {
	a := &App{store: map[string]*Task{}, path: path}
	if err := a.load(); err != nil {
		return nil, err
	}
	return a, nil
}

func (a *App) Manifest() biuapp.Manifest {
	return biuapp.Manifest{
		Name:        Name,
		Version:     "0.2.0",
		Description: "Personal task list — in-memory MVP",
		Author:      "BiuMind",
		Permissions: []string{}, // local only — no network, no LLM
		Actions: []biuapp.ActionSpec{
			{
				Name:        "create",
				Description: "Create a task",
				Risk:        biuapp.RiskLow,
				InputSchema: map[string]any{
					"type":     "object",
					"required": []string{"title"},
					"properties": map[string]any{
						"title": map[string]any{"type": "string", "title": "标题"},
						"due":   map[string]any{"type": "string", "format": "date-time", "title": "截止时间（可选）"},
					},
				},
			},
			{
				Name:        "list",
				Description: "List tasks (filterable by done/tag)",
				Risk:        biuapp.RiskLow,
				InputSchema: map[string]any{"type": "object"},
			},
			{
				Name:        "complete",
				Description: "Mark a task done",
				Risk:        biuapp.RiskLow,
				InputSchema: map[string]any{
					"type":     "object",
					"required": []string{"id"},
					"properties": map[string]any{"id": map[string]any{"type": "string"}},
				},
			},
			{
				Name:        "delete",
				Description: "Delete a task",
				Risk:        biuapp.RiskLow,
				InputSchema: map[string]any{
					"type":     "object",
					"required": []string{"id"},
					"properties": map[string]any{"id": map[string]any{"type": "string"}},
				},
			},
			{
				Name:        "get",
				Description: "Get one task by id",
				Risk:        biuapp.RiskLow,
				InputSchema: map[string]any{
					"type":     "object",
					"required": []string{"id"},
					"properties": map[string]any{"id": map[string]any{"type": "string"}},
				},
			},
			{
				Name:        "pending_count",
				Description: "Count of incomplete tasks (sidebar badge).",
				Risk:        biuapp.RiskLow,
				InputSchema: map[string]any{"type": "object"},
			},
		},
		ManifestExt: biuapp.ManifestExt{
			Identifier: Name,
			Title:      "任务",
			Category:   "productivity",
			Kind:       "hybrid",
			Views: []biuapp.ViewSpec{
				{
					ID:     "home",
					Route:  "/apps/tasks",
					Title:  "任务",
					Layout: biuapp.LayoutListDetail,
					DataSource: &biuapp.ViewDataSource{Action: "list"},
					ItemTemplate: &biuapp.ViewItemTemplate{
						Kind:     "card",
						Title:    "${item.title}",
						Subtitle: "${item.due | date(yyyy-MM-dd) | default(无截止)}",
						Actions: []biuapp.ViewActionRef{
							{
								Label:  "完成",
								Icon:   "open",
								Action: "complete",
								Input:  map[string]any{"id": "${item.id}"},
								OnSuccess: &biuapp.ViewActionEffect{
									Toast:   "已完成",
									Refresh: true,
								},
							},
							{
								Label:   "删除",
								Icon:    "trash",
								Action:  "delete",
								Input:   map[string]any{"id": "${item.id}"},
								Confirm: "确认删除 ${item.title}？",
								OnSuccess: &biuapp.ViewActionEffect{
									Toast:   "已删除",
									Refresh: true,
								},
							},
						},
					},
					Toolbar: []biuapp.ViewActionRef{
						{Label: "新建任务", Icon: "add", Route: "/apps/tasks/add"},
					},
				},
				{
					ID:        "add",
					Route:     "/apps/tasks/add",
					Title:     "新建任务",
					Layout:    biuapp.LayoutForm,
					SchemaRef: "actions.create.input_schema",
					Submit: &biuapp.FormSubmit{
						Action: "create",
						OnSuccess: &biuapp.ViewActionEffect{
							Toast:    "已创建",
							Navigate: "/apps/tasks",
						},
					},
				},
			},
			Sidebar: &biuapp.SidebarHints{
				PreferredPosition: "middle",
				BadgeAction:       "pending_count",
				BadgeRefreshSec:   60,
			},
		},
	}
}

func (a *App) Init(ctx context.Context, deps biuapp.Deps) error { return nil }

type Task struct {
	ID        string    `json:"id"`
	OwnerID   string    `json:"owner_id,omitempty"`
	Title     string    `json:"title"`
	Due       time.Time `json:"due,omitempty"`
	Tags      []string  `json:"tags,omitempty"`
	Done      bool      `json:"done"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (a *App) Invoke(ctx context.Context, action string, raw json.RawMessage) (any, error) {
	switch action {
	case "create":
		return a.create(raw)
	case "list":
		return a.list(raw)
	case "complete":
		return a.complete(raw)
	case "delete":
		return a.delete(raw)
	case "get":
		return a.get(raw)
	case "pending_count":
		return a.pendingCount(), nil
	default:
		return nil, fmt.Errorf("tasks: unknown action %q", action)
	}
}

// pendingCount: 未完成任务总数, 给 sidebar badge (设计 §10A.9)。
// severity 决策: 0=隐藏 (BadgeData.visible 自动处理); 1-9 info;
// ≥10 warn (说明积压多了)。今日逾期单独标 error 留 v2.0 (需要 due
// 字段过滤当前时间)。
func (a *App) pendingCount() map[string]any {
	a.mu.Lock()
	count := 0
	overdue := 0
	now := time.Now().UTC()
	for _, t := range a.store {
		if t.Done {
			continue
		}
		count++
		if !t.Due.IsZero() && t.Due.Before(now) {
			overdue++
		}
	}
	a.mu.Unlock()
	severity := "info"
	if overdue > 0 {
		severity = "error"
	} else if count >= 10 {
		severity = "warn"
	}
	return map[string]any{
		"count":    count,
		"severity": severity,
	}
}

// ─── create ───────────────────────────────────────────────

type createIn struct {
	OwnerID string    `json:"owner_id"`
	Title   string    `json:"title"`
	Due     time.Time `json:"due"`
	Tags    []string  `json:"tags"`
}

func (a *App) create(raw json.RawMessage) (*Task, error) {
	var in createIn
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, fmt.Errorf("tasks: bad input: %w", err)
	}
	if strings.TrimSpace(in.Title) == "" {
		return nil, errors.New("tasks: missing title")
	}
	now := time.Now().UTC()
	t := &Task{
		ID:        "tsk-" + uuid.NewString(),
		OwnerID:   in.OwnerID,
		Title:     in.Title,
		Due:       in.Due.UTC(),
		Tags:      in.Tags,
		CreatedAt: now,
		UpdatedAt: now,
	}
	a.mu.Lock()
	a.store[t.ID] = t
	a.mu.Unlock()
	if err := a.persist(); err != nil {
		return t, err
	}
	return t, nil
}

// ─── list ─────────────────────────────────────────────────

type listIn struct {
	OwnerID string `json:"owner_id"`
	Done    *bool  `json:"done"`
	Tag     string `json:"tag"`
}

// listOut wires to the App Center view layer via `items` (the JSON
// key AppViewHost expects for list / list_detail layouts). The Go
// field name keeps the original `Tasks` for source-level callers
// (existing tests + agents that consume the result programmatically).
type listOut struct {
	Tasks []Task `json:"items"`
}

func (a *App) list(raw json.RawMessage) (*listOut, error) {
	var in listIn
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &in)
	}
	a.mu.Lock()
	all := make([]Task, 0, len(a.store))
	for _, t := range a.store {
		if in.OwnerID != "" && t.OwnerID != in.OwnerID {
			continue
		}
		if in.Done != nil && t.Done != *in.Done {
			continue
		}
		if in.Tag != "" && !contains(t.Tags, in.Tag) {
			continue
		}
		all = append(all, *t)
	}
	a.mu.Unlock()
	sort.Slice(all, func(i, j int) bool {
		// done last; otherwise earliest-due first; createdAt as tiebreak.
		if all[i].Done != all[j].Done {
			return !all[i].Done
		}
		if !all[i].Due.Equal(all[j].Due) {
			return all[i].Due.Before(all[j].Due)
		}
		return all[i].CreatedAt.Before(all[j].CreatedAt)
	})
	return &listOut{Tasks: all}, nil
}

// ─── complete / delete / get ─────────────────────────────

type idIn struct {
	ID string `json:"id"`
}

func (a *App) complete(raw json.RawMessage) (*Task, error) {
	var in idIn
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	a.mu.Lock()
	t, ok := a.store[in.ID]
	if !ok {
		a.mu.Unlock()
		return nil, fmt.Errorf("tasks: %s not found", in.ID)
	}
	t.Done = true
	t.UpdatedAt = time.Now().UTC()
	out := *t
	a.mu.Unlock()
	if err := a.persist(); err != nil {
		return &out, err
	}
	return &out, nil
}

func (a *App) delete(raw json.RawMessage) (any, error) {
	var in idIn
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	a.mu.Lock()
	if _, ok := a.store[in.ID]; !ok {
		a.mu.Unlock()
		return nil, fmt.Errorf("tasks: %s not found", in.ID)
	}
	delete(a.store, in.ID)
	a.mu.Unlock()
	if err := a.persist(); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

func (a *App) get(raw json.RawMessage) (*Task, error) {
	var in idIn
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	t, ok := a.store[in.ID]
	if !ok {
		return nil, fmt.Errorf("tasks: %s not found", in.ID)
	}
	out := *t
	return &out, nil
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

// ─── persistence ──────────────────────────────────────────

// load reads `a.path` into `a.store`. Missing file → empty store, no
// error (lets first-run users start clean). Bad JSON → error so the
// app refuses to start rather than silently dropping data.
func (a *App) load() error {
	if a.path == "" {
		return nil
	}
	data, err := os.ReadFile(a.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("tasks: load: %w", err)
	}
	var snap []Task
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("tasks: parse: %w", err)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for i := range snap {
		t := snap[i]
		a.store[t.ID] = &t
	}
	return nil
}

// persist atomically writes the current store to `a.path` (no-op when
// empty). Strategy: write to <path>.tmp + rename — `rename` is atomic
// on POSIX so a crash mid-write never leaves a half-written file.
func (a *App) persist() error {
	if a.path == "" {
		return nil
	}
	a.mu.Lock()
	snap := make([]Task, 0, len(a.store))
	for _, t := range a.store {
		snap = append(snap, *t)
	}
	a.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(a.path), 0o755); err != nil {
		return fmt.Errorf("tasks: mkdir: %w", err)
	}
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return err
	}
	tmp := a.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("tasks: write tmp: %w", err)
	}
	if err := os.Rename(tmp, a.path); err != nil {
		return fmt.Errorf("tasks: rename: %w", err)
	}
	return nil
}

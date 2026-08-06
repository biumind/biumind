// Package skills is the data access layer for the runtime Skills
// subsystem. Three concerns split across three files:
//
//	registry.go — CRUD against runtime.skills + runtime.agent_skills
//	loader.go   — per-agent classification (pinned / available / auto)
//	inject.go   — system-prompt + selected-context block assembly
//
// The package is consumed by:
//
//   - services/runtime/internal/api/skills_server.go (PS2.2): the
//     Connect-Go SkillsService handler that translates proto requests
//     into Registry calls.
//   - services/runtime/internal/agent/skill_tools.go (PS2.4): the six
//     builtin tools (skill.list / activate / read_reference /
//     exec_script / export_file / propose) all bottom out here.
//
// We deliberately keep this layer transport-agnostic: no proto types,
// no http.Request, no logging side-effects (callers thread a slog
// handle into ctx if they want it). That makes registry_test.go a
// straight pgx-against-test-DB exercise without a server in the loop.
//
// I4 — every mutation emits a brain.events row in the SAME tx as the
// underlying skill change. emitEvent() is the only way state leaves
// this package, callers cannot bypass it. See Skills-Design §13.
package skills

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

var (
	ErrNotFound      = errors.New("skill not found")
	ErrNameTaken     = errors.New("skill identifier already exists in this org")
	ErrInvalidStatus = errors.New("invalid status transition")
	// ErrBundledImmutable — bundled skills are platform-shipped via the
	// skills-stdlib loader; deleting a row would be a no-op (LoadBundled
	// reinstates it on the next runtime boot) but worse, breaks every
	// org currently relying on it. Reject hard at the API layer.
	ErrBundledImmutable = errors.New("bundled skills cannot be deleted")
)

// Source matches packages/proto/biumind/runtime/v1/skills.proto Source
// enum, but kept as plain strings here so the store layer doesn't
// import the generated proto package (avoids a proto → store cycle
// when tools land later).
type Source string

const (
	SourceBundled     Source = "bundled"
	SourceOrg         Source = "org"
	SourceUser        Source = "user"
	SourceMarketplace Source = "marketplace"
	SourceImported    Source = "imported"
)

func ValidSource(s Source) bool {
	switch s {
	case SourceBundled, SourceOrg, SourceUser, SourceMarketplace, SourceImported:
		return true
	}
	return false
}

// Status mirrors the proto enum.
type Status string

const (
	StatusActive    Status = "active"
	StatusDisabled  Status = "disabled"
	StatusStaged    Status = "staged"
	StatusStagedOrg Status = "staged_org"
	StatusSuspended Status = "suspended"
)

func ValidStatus(s Status) bool {
	switch s {
	case StatusActive, StatusDisabled, StatusStaged, StatusStagedOrg, StatusSuspended:
		return true
	}
	return false
}

// ResourceMeta — one bundled resource entry. Mirrors proto
// SkillResourceMeta. Either inline (Inline non-empty) or CAS
// (Sha256 + SizeBytes pointing at files.objects).
type ResourceMeta struct {
	Sha256    string `json:"sha256,omitempty"`
	SizeBytes int64  `json:"size_bytes,omitempty"`
	MimeType  string `json:"mime_type,omitempty"`
	Inline    string `json:"inline,omitempty"`
}

// Manifest mirrors proto SkillManifest — frontmatter-derived metadata
// stored as JSONB. Extra captures fields the platform doesn't model
// yet so a third-party manifest can pass through unchanged.
type Manifest struct {
	Version    string         `json:"version,omitempty"`
	Author     ManifestAuthor `json:"author,omitempty"`
	License    string         `json:"license,omitempty"`
	Repository string         `json:"repository,omitempty"`
	SourceURL  string         `json:"source_url,omitempty"`
	// Icon is a short visual hint shown in lists / cards. Two shapes
	// the client knows how to render:
	//   - Single emoji ("🛠", "🧠"): rendered as text inside an
	//     auto-coloured avatar tile.
	//   - https URL: rendered via Image.network with mem cache.
	// Anything else falls back to identifier-first-letter avatar.
	Icon  string            `json:"icon,omitempty"`
	Extra map[string]string `json:"extra,omitempty"`
}

type ManifestAuthor struct {
	Name string `json:"name,omitempty"`
	URL  string `json:"url,omitempty"`
}

// Skill — one row of runtime.skills, hydrated for application use.
// Field order matches the SQL column order in 00002_skills.sql so
// rows.Scan can reuse the same positional layout.
type Skill struct {
	ID            string
	OrgID         uuid.UUID
	OwnerID       *uuid.UUID // nil = org-shared (bundled / org)
	Identifier    string
	Name          string
	Description   string
	Source        Source
	Manifest      Manifest
	Content       string
	ContentHash   string
	Resources     map[string]ResourceMeta
	ZipFileSha256 string
	Paths         []string
	Permissions   []string
	Status        Status
	// UpdateOfID — staged skills that propose a replacement carry the
	// predecessor's id here. Empty for fresh proposals / non-staged
	// rows. Approver UI renders a diff against the predecessor when set.
	UpdateOfID string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// AgentSkill — one row of runtime.agent_skills.
type AgentSkill struct {
	AgentID   uuid.UUID
	SkillID   string
	IsEnabled bool
	Pinned    bool
	AddedAt   time.Time
}

// ActivationTrigger labels how a skill came to be loaded for a run.
// Constrained at the DB level (see migrations/00002_skills.sql) so
// adding a new value here without updating the CHECK constraint will
// surface as a write failure in tests.
type ActivationTrigger string

const (
	TriggerExplicit   ActivationTrigger = "explicit"    // user/UI flagged it
	TriggerAutoAttach ActivationTrigger = "auto_attach" // paths: matched cwd
	TriggerToolCall   ActivationTrigger = "tool_call"   // skill.activate
	TriggerPinned     ActivationTrigger = "pinned"      // agent_skills.pinned
)

func (t ActivationTrigger) Valid() bool {
	switch t {
	case TriggerExplicit, TriggerAutoAttach, TriggerToolCall, TriggerPinned:
		return true
	}
	return false
}

// Activation — one row of runtime.skill_activations. Append-only.
type Activation struct {
	ID         uuid.UUID
	SessionID  uuid.UUID
	SkillID    string
	Trigger    ActivationTrigger
	TraceID    string
	TokensIn   int
	TokensOut  int
	OccurredAt time.Time
}

// Registry is the package's only public type. Wrap a *pgxpool.Pool
// from the runtime daemon's main.go.
type Registry struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Registry { return &Registry{pool: pool} }

// ─── CRUD ──────────────────────────────────────────────────

// CreateInput packs all the column-level fields a fresh skill row
// needs. ID is assigned by the caller (typically `skill_<ulid>`); the
// package doesn't pick its own so tests stay deterministic.
type CreateInput struct {
	ID          string
	OrgID       uuid.UUID
	OwnerID     *uuid.UUID
	Identifier  string
	Name        string
	Description string
	Source      Source
	Manifest    Manifest
	Content     string
	Resources   map[string]ResourceMeta
	Paths       []string
	Permissions []string
	Status      Status // empty defaults to StatusActive
	// UpdateOfID — set when this is a v2-of an existing skill (propose
	// flow). Persists the predecessor pointer so the approver UI can
	// later render a diff. Optional.
	UpdateOfID string
}

// Create writes a new skill row + a corresponding brain.events row in
// the same tx. Returns ErrNameTaken on UNIQUE (org_id, identifier)
// collision so the caller can surface a friendly message.
func (r *Registry) Create(ctx context.Context, in CreateInput) (*Skill, error) {
	if err := validateCreate(in); err != nil {
		return nil, err
	}
	if in.Status == "" {
		in.Status = StatusActive
	}
	manifestBytes, _ := json.Marshal(in.Manifest)
	resourcesBytes, _ := json.Marshal(coalesceResources(in.Resources))
	contentHash := sha256Hex(in.Content)

	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var updateOfArg any
	if in.UpdateOfID != "" {
		updateOfArg = in.UpdateOfID
	}
	row := tx.QueryRow(ctx, `
		INSERT INTO runtime.skills (
			id, org_id, owner_id, identifier, name, description,
			source, manifest, content, content_hash, resources,
			paths, permissions, status, update_of_id
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
		RETURNING created_at, updated_at
	`, in.ID, in.OrgID, in.OwnerID, in.Identifier, in.Name, in.Description,
		string(in.Source), manifestBytes, in.Content, contentHash, resourcesBytes,
		stringSlice(in.Paths), stringSlice(in.Permissions), string(in.Status), updateOfArg)

	var createdAt, updatedAt time.Time
	if err := row.Scan(&createdAt, &updatedAt); err != nil {
		if isUniqueViolation(err) {
			return nil, ErrNameTaken
		}
		return nil, fmt.Errorf("insert skill: %w", err)
	}

	if err := emitEvent(ctx, tx, in.OrgID, in.ID, "skill.created", map[string]any{
		"skill_id":   in.ID,
		"identifier": in.Identifier,
		"source":     string(in.Source),
		"status":     string(in.Status),
	}); err != nil {
		return nil, fmt.Errorf("emit event: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	out := skillFromInput(in, contentHash, createdAt, updatedAt)
	return &out, nil
}

func validateCreate(in CreateInput) error {
	if in.ID == "" {
		return errors.New("id required")
	}
	// Bundled skills use BundledOrgID = uuid.Nil as a deliberate sentinel
	// (see builtin.go), letting LoadForAgent UNION-merge them into every
	// org's view without an extra "is_bundled" column. Only require a
	// real org for org-owned / user / marketplace / imported sources.
	if in.OrgID == uuid.Nil && in.Source != SourceBundled {
		return errors.New("org_id required")
	}
	if strings.TrimSpace(in.Identifier) == "" {
		return errors.New("identifier required")
	}
	if strings.TrimSpace(in.Name) == "" {
		return errors.New("name required")
	}
	if !ValidSource(in.Source) {
		return fmt.Errorf("invalid source %q", in.Source)
	}
	if in.Status != "" && !ValidStatus(in.Status) {
		return fmt.Errorf("invalid status %q", in.Status)
	}
	return nil
}

// Get loads one skill by ID. ErrNotFound when missing.
func (r *Registry) Get(ctx context.Context, id string) (*Skill, error) {
	row := r.pool.QueryRow(ctx, selectSkillSQL+` WHERE id = $1`, id)
	return scanSkill(row)
}

// GetByIdentifier loads a skill by its (org, identifier) tuple — the
// natural key user code reaches for ("the code-review skill in my
// org") rather than the surrogate id. ErrNotFound when missing.
func (r *Registry) GetByIdentifier(ctx context.Context, orgID uuid.UUID, identifier string) (*Skill, error) {
	row := r.pool.QueryRow(ctx,
		selectSkillSQL+` WHERE org_id = $1 AND identifier = $2`,
		orgID, identifier)
	return scanSkill(row)
}

// ListInput — filter shape for List. Empty fields = "no filter".
type ListInput struct {
	OrgID   uuid.UUID
	Source  Source
	Status  Status
	OwnerID *uuid.UUID // when non-nil, restrict to this owner OR org-shared (NULL owner)
	Limit   int        // ≤ 0 → 100; capped at 500
}

func (r *Registry) List(ctx context.Context, in ListInput) ([]*Skill, error) {
	if in.OrgID == uuid.Nil {
		return nil, errors.New("org_id required")
	}
	limit := in.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	args := []any{in.OrgID, limit}
	// Bundled skills (org_id = uuid.Nil = BundledOrgID) are visible to
	// every org by design — UNION them into the caller's view so the
	// "技能管理" page shows the platform-shipped skills alongside the
	// org's own. Without this the page would be empty for any org that
	// hasn't installed anything yet, even though 8 bundled rows exist.
	q := selectSkillSQL + ` WHERE (org_id = $1 OR org_id = '` + uuid.Nil.String() + `')`
	if in.Source != "" {
		args = append(args, string(in.Source))
		q += fmt.Sprintf(` AND source = $%d`, len(args))
	}
	if in.Status != "" {
		args = append(args, string(in.Status))
		q += fmt.Sprintf(` AND status = $%d`, len(args))
	}
	if in.OwnerID != nil {
		args = append(args, *in.OwnerID)
		q += fmt.Sprintf(` AND (owner_id IS NULL OR owner_id = $%d)`, len(args))
	}
	q += ` ORDER BY updated_at DESC LIMIT $2`

	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Skill
	for rows.Next() {
		s, err := scanSkill(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// UpdateInput — sparse update via paired set_<field> booleans
// matching the proto (so transport layer can copy 1:1). Fields where
// Set* is false are left untouched server-side.
type UpdateInput struct {
	ID               string
	Name             string
	SetName          bool
	Description      string
	SetDescription   bool
	Content          string
	SetContent       bool
	Manifest         Manifest
	SetManifest      bool
	Paths            []string
	SetPaths         bool
	Permissions      []string
	SetPermissions   bool
	Resources        map[string]ResourceMeta
	SetResources     bool
	ZipFileSha256    string
	SetZipFileSha256 bool
}

// Update applies a sparse field set. Returns the post-update row.
func (r *Registry) Update(ctx context.Context, in UpdateInput) (*Skill, error) {
	if in.ID == "" {
		return nil, errors.New("id required")
	}

	// Load first so we can include the existing identifier in the
	// event payload (and so empty-update is a no-op fast path
	// reporting the live row).
	cur, err := r.Get(ctx, in.ID)
	if err != nil {
		return nil, err
	}

	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	sets := []string{}
	args := []any{in.ID}
	if in.SetName {
		args = append(args, in.Name)
		sets = append(sets, fmt.Sprintf("name = $%d", len(args)))
	}
	if in.SetDescription {
		args = append(args, in.Description)
		sets = append(sets, fmt.Sprintf("description = $%d", len(args)))
	}
	if in.SetContent {
		args = append(args, in.Content)
		sets = append(sets, fmt.Sprintf("content = $%d", len(args)))
		args = append(args, sha256Hex(in.Content))
		sets = append(sets, fmt.Sprintf("content_hash = $%d", len(args)))
	}
	if in.SetManifest {
		mb, _ := json.Marshal(in.Manifest)
		args = append(args, mb)
		sets = append(sets, fmt.Sprintf("manifest = $%d", len(args)))
	}
	if in.SetPaths {
		args = append(args, stringSlice(in.Paths))
		sets = append(sets, fmt.Sprintf("paths = $%d", len(args)))
	}
	if in.SetPermissions {
		args = append(args, stringSlice(in.Permissions))
		sets = append(sets, fmt.Sprintf("permissions = $%d", len(args)))
	}
	if in.SetResources {
		rb, _ := json.Marshal(coalesceResources(in.Resources))
		args = append(args, rb)
		sets = append(sets, fmt.Sprintf("resources = $%d", len(args)))
	}
	if in.SetZipFileSha256 {
		var v any = in.ZipFileSha256
		if in.ZipFileSha256 == "" {
			v = nil
		}
		args = append(args, v)
		sets = append(sets, fmt.Sprintf("zip_file_sha256 = $%d", len(args)))
	}
	if len(sets) == 0 {
		// No-op update; still emit an event so audit reflects the
		// "touched" intent — callers can dedupe by comparing payload
		// fields if they care.
		if err := emitEvent(ctx, tx, cur.OrgID, in.ID, "skill.touched", nil); err != nil {
			return nil, err
		}
		_ = tx.Commit(ctx)
		return cur, nil
	}
	sets = append(sets, "updated_at = now()")

	q := fmt.Sprintf(`UPDATE runtime.skills SET %s WHERE id = $1`,
		strings.Join(sets, ", "))
	if _, err := tx.Exec(ctx, q, args...); err != nil {
		return nil, fmt.Errorf("update skill: %w", err)
	}

	if err := emitEvent(ctx, tx, cur.OrgID, in.ID, "skill.updated", map[string]any{
		"fields": setFieldNames(in),
	}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return r.Get(ctx, in.ID)
}

func setFieldNames(u UpdateInput) []string {
	out := []string{}
	if u.SetName {
		out = append(out, "name")
	}
	if u.SetDescription {
		out = append(out, "description")
	}
	if u.SetContent {
		out = append(out, "content")
	}
	if u.SetManifest {
		out = append(out, "manifest")
	}
	if u.SetPaths {
		out = append(out, "paths")
	}
	if u.SetPermissions {
		out = append(out, "permissions")
	}
	if u.SetResources {
		out = append(out, "resources")
	}
	if u.SetZipFileSha256 {
		out = append(out, "zip_file_sha256")
	}
	return out
}

// Delete removes a skill. ON DELETE CASCADE on agent_skills handles
// the join table; activations rows persist for audit (no FK there).
// Returns ErrNotFound if the row didn't exist, or ErrBundledImmutable
// when the row is platform-shipped (source=bundled).
func (r *Registry) Delete(ctx context.Context, id string) error {
	cur, err := r.Get(ctx, id)
	if err != nil {
		return err
	}
	if cur.Source == SourceBundled {
		return ErrBundledImmutable
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx, `DELETE FROM runtime.skills WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if err := emitEvent(ctx, tx, cur.OrgID, id, "skill.deleted", map[string]any{
		"identifier": cur.Identifier,
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// SetStatus drives the propose/approve/reject/share-org state
// machine. Validates transition lightly — the proto layer handles
// the full graph; here we just ensure the new value parses. Returns
// the post-update row.
func (r *Registry) SetStatus(ctx context.Context, id string, next Status, reason string) (*Skill, error) {
	if !ValidStatus(next) {
		return nil, fmt.Errorf("%w: %q", ErrInvalidStatus, next)
	}
	cur, err := r.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx,
		`UPDATE runtime.skills SET status = $1, updated_at = now() WHERE id = $2`,
		string(next), id); err != nil {
		return nil, fmt.Errorf("update status: %w", err)
	}
	payload := map[string]any{
		"from":   string(cur.Status),
		"to":     string(next),
		"reason": reason,
	}
	if err := emitEvent(ctx, tx, cur.OrgID, id, "skill.status_changed", payload); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return r.Get(ctx, id)
}

// ─── AgentSkill (per-agent enablement) ──────────────────────

// Toggle upserts an agent_skills row. Returns the post-write state.
// Setting both is_enabled=false and pinned=false is supported but
// rare; typically callers either disable (and leave the row for
// "user explicitly opted out" audit) or delete entirely (Detach).
func (r *Registry) Toggle(ctx context.Context, agentID uuid.UUID, skillID string, enabled, pinned bool) (*AgentSkill, error) {
	cur, err := r.Get(ctx, skillID)
	if err != nil {
		return nil, err
	}

	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	row := tx.QueryRow(ctx, `
		INSERT INTO runtime.agent_skills (agent_id, skill_id, is_enabled, pinned)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (agent_id, skill_id) DO UPDATE
		   SET is_enabled = EXCLUDED.is_enabled,
		       pinned     = EXCLUDED.pinned
		RETURNING agent_id, skill_id, is_enabled, pinned, added_at
	`, agentID, skillID, enabled, pinned)

	var as AgentSkill
	if err := row.Scan(&as.AgentID, &as.SkillID, &as.IsEnabled, &as.Pinned, &as.AddedAt); err != nil {
		return nil, fmt.Errorf("upsert agent_skills: %w", err)
	}
	if err := emitEvent(ctx, tx, cur.OrgID, skillID, "skill.toggled", map[string]any{
		"agent_id":   agentID.String(),
		"is_enabled": enabled,
		"pinned":     pinned,
	}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &as, nil
}

// Detach removes the agent_skills row entirely. Use this for "I no
// longer want this skill associated with this agent at all" rather
// than "I want it disabled but remembered". No-op when the row
// doesn't exist (returns nil).
func (r *Registry) Detach(ctx context.Context, agentID uuid.UUID, skillID string) error {
	_, err := r.pool.Exec(ctx,
		`DELETE FROM runtime.agent_skills WHERE agent_id = $1 AND skill_id = $2`,
		agentID, skillID)
	return err
}

// ─── Activation audit ──────────────────────────────────────

// LogActivation appends one row to runtime.skill_activations. Every
// path that surfaces a skill body to the model — pinned/auto_attach
// injection in the system prompt, the skill.activate tool call,
// explicit UI selection — should write through here. The table is
// append-only by design (no FK on session_id / skill_id) so deletes
// upstream don't cascade away the audit trail.
//
// Errors are returned but caller-discardable: a failed activation
// log must not block the underlying activation. Callers typically
// log + swallow the error so a transient pg outage doesn't taint
// the model's reply.
func (r *Registry) LogActivation(ctx context.Context, in Activation) (*Activation, error) {
	if !in.Trigger.Valid() {
		return nil, fmt.Errorf("%w: trigger %q", ErrInvalidStatus, in.Trigger)
	}
	if in.SkillID == "" {
		return nil, fmt.Errorf("LogActivation: skill_id required")
	}
	row := r.pool.QueryRow(ctx, `
		INSERT INTO runtime.skill_activations
		    (session_id, skill_id, trigger, trace_id, tokens_in, tokens_out)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, session_id, skill_id, trigger, trace_id,
		          tokens_in, tokens_out, occurred_at
	`, in.SessionID, in.SkillID, string(in.Trigger), in.TraceID,
		in.TokensIn, in.TokensOut)
	var (
		out        Activation
		triggerStr string
	)
	if err := row.Scan(
		&out.ID, &out.SessionID, &out.SkillID, &triggerStr,
		&out.TraceID, &out.TokensIn, &out.TokensOut, &out.OccurredAt,
	); err != nil {
		return nil, fmt.Errorf("insert skill_activations: %w", err)
	}
	out.Trigger = ActivationTrigger(triggerStr)
	return &out, nil
}

// ListActivationsBySkill returns the most recent activations for one
// skill, newest first. Powers the "调用 N 次 / 最后调用 X 时间前"
// panel on the SkillDetail drawer. The skill_act_skill_recent_idx
// covers (skill_id, occurred_at DESC) so this is a single index seek.
func (r *Registry) ListActivationsBySkill(ctx context.Context, skillID string, limit int) ([]*Activation, error) {
	if limit <= 0 || limit > 1000 {
		limit = 50
	}
	rows, err := r.pool.Query(ctx, `
		SELECT id, session_id, skill_id, trigger, trace_id,
		       tokens_in, tokens_out, occurred_at
		  FROM runtime.skill_activations
		 WHERE skill_id = $1
		 ORDER BY occurred_at DESC
		 LIMIT $2
	`, skillID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Activation
	for rows.Next() {
		var a Activation
		var trig string
		if err := rows.Scan(&a.ID, &a.SessionID, &a.SkillID, &trig,
			&a.TraceID, &a.TokensIn, &a.TokensOut, &a.OccurredAt); err != nil {
			return nil, err
		}
		a.Trigger = ActivationTrigger(trig)
		out = append(out, &a)
	}
	return out, rows.Err()
}

// SkillActivationStats — small summary projected for the detail
// drawer. count = total rows; lastAt = most recent occurred_at (zero
// when no activations).
type SkillActivationStats struct {
	Count  int64
	LastAt time.Time
}

// ActivationStats returns count + last-occurrence for one skill in a
// single round-trip (cheaper than ListActivationsBySkill when the UI
// only needs the headline numbers).
func (r *Registry) ActivationStats(ctx context.Context, skillID string) (*SkillActivationStats, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT count(*), coalesce(max(occurred_at), 'epoch'::timestamptz)
		  FROM runtime.skill_activations
		 WHERE skill_id = $1
	`, skillID)
	var s SkillActivationStats
	if err := row.Scan(&s.Count, &s.LastAt); err != nil {
		return nil, err
	}
	return &s, nil
}

// ListActivationsBySession returns the activation ledger for one
// run, oldest first. Used by the UI's "what skills did this run
// touch?" panel and by billing replay.
func (r *Registry) ListActivationsBySession(ctx context.Context, sessionID uuid.UUID, limit int) ([]*Activation, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := r.pool.Query(ctx, `
		SELECT id, session_id, skill_id, trigger, trace_id,
		       tokens_in, tokens_out, occurred_at
		  FROM runtime.skill_activations
		 WHERE session_id = $1
		 ORDER BY occurred_at ASC
		 LIMIT $2
	`, sessionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Activation
	for rows.Next() {
		var a Activation
		var trig string
		if err := rows.Scan(&a.ID, &a.SessionID, &a.SkillID, &trig,
			&a.TraceID, &a.TokensIn, &a.TokensOut, &a.OccurredAt); err != nil {
			return nil, err
		}
		a.Trigger = ActivationTrigger(trig)
		out = append(out, &a)
	}
	return out, rows.Err()
}

// ─── helpers ───────────────────────────────────────────────

// selectSkillSQL is the canonical SELECT projection. Kept in one
// place so column ordering matches scanSkill exactly.
const selectSkillSQL = `
SELECT id, org_id, owner_id, identifier, name, description, source,
       manifest, content, content_hash, resources, zip_file_sha256,
       paths, permissions, status, update_of_id, created_at, updated_at
  FROM runtime.skills`

// scanSkill rehydrates one row. Accepts both pgx.Rows and pgx.Row
// via the rowScanner interface so List + Get share parsing.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanSkill(r rowScanner) (*Skill, error) {
	var (
		s              Skill
		ownerID        pgxNullableUUID
		manifestBytes  []byte
		resourcesBytes []byte
		zipHash        pgxNullableString
		updateOf       pgxNullableString
		sourceStr      string
		statusStr      string
		paths          []string
		perms          []string
	)
	err := r.Scan(
		&s.ID, &s.OrgID, &ownerID, &s.Identifier, &s.Name, &s.Description,
		&sourceStr, &manifestBytes, &s.Content, &s.ContentHash, &resourcesBytes,
		&zipHash, &paths, &perms, &statusStr, &updateOf, &s.CreatedAt, &s.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	s.Source = Source(sourceStr)
	s.Status = Status(statusStr)
	if ownerID.Valid {
		v := ownerID.UUID
		s.OwnerID = &v
	}
	if zipHash.Valid {
		s.ZipFileSha256 = zipHash.String
	}
	if updateOf.Valid {
		s.UpdateOfID = updateOf.String
	}
	if len(manifestBytes) > 0 {
		_ = json.Unmarshal(manifestBytes, &s.Manifest)
	}
	if len(resourcesBytes) > 0 {
		_ = json.Unmarshal(resourcesBytes, &s.Resources)
	}
	s.Paths = paths
	s.Permissions = perms
	return &s, nil
}

// emitEvent writes the I4-required brain.events row in the SAME tx
// as the underlying skill mutation. scope is "runtime:skill:<id>"
// so consumers can subscribe per-skill (UI activity feeds) or by
// org (admin dashboards) via prefix match.
func emitEvent(ctx context.Context, tx pgx.Tx, orgID uuid.UUID, skillID, eventType string, payload map[string]any) error {
	if payload == nil {
		payload = map[string]any{}
	}
	payload["org_id"] = orgID.String()
	pl, _ := json.Marshal(payload)
	scope := fmt.Sprintf("runtime:skill:%s", skillID)
	_, err := tx.Exec(ctx, `
		INSERT INTO brain.events (scope, actor_type, actor_id, event_type, payload)
		VALUES ($1, $2, $3, $4, $5)
	`, scope, "system", "runtime", eventType, pl)
	return err
}

// coalesceResources guarantees an empty map round-trips as `{}` in
// JSONB (rather than `null`) so the column's NOT NULL DEFAULT
// matches what we write.
func coalesceResources(in map[string]ResourceMeta) map[string]ResourceMeta {
	if in == nil {
		return map[string]ResourceMeta{}
	}
	return in
}

// stringSlice returns nil when the input has no entries so the
// text[] column accepts SQL NULL rather than `{}` — matches the
// SELECT path which returns nil for absent values.
func stringSlice(in []string) any {
	if len(in) == 0 {
		return nil
	}
	return in
}

func skillFromInput(in CreateInput, contentHash string, createdAt, updatedAt time.Time) Skill {
	return Skill{
		ID: in.ID, OrgID: in.OrgID, OwnerID: in.OwnerID,
		Identifier: in.Identifier, Name: in.Name, Description: in.Description,
		Source: in.Source, Manifest: in.Manifest,
		Content: in.Content, ContentHash: contentHash,
		Resources:   coalesceResources(in.Resources),
		Paths:       in.Paths,
		Permissions: in.Permissions,
		Status:      in.Status,
		UpdateOfID:  in.UpdateOfID,
		CreatedAt:   createdAt, UpdatedAt: updatedAt,
	}
}

// ─── pgx nullable helpers ──────────────────────────────────
//
// pgx exposes pgtype.UUID / pgtype.Text but those drag the whole
// pgtype package into our public API surface. The two we need —
// nullable UUID and nullable string — are simple enough to inline.

type pgxNullableUUID struct {
	UUID  uuid.UUID
	Valid bool
}

func (n *pgxNullableUUID) Scan(src any) error {
	if src == nil {
		n.Valid = false
		return nil
	}
	switch v := src.(type) {
	case [16]byte:
		n.UUID = v
		n.Valid = true
	case string:
		u, err := uuid.Parse(v)
		if err != nil {
			return err
		}
		n.UUID, n.Valid = u, true
	case []byte:
		if len(v) == 16 {
			copy(n.UUID[:], v)
			n.Valid = true
			return nil
		}
		u, err := uuid.Parse(string(v))
		if err != nil {
			return err
		}
		n.UUID, n.Valid = u, true
	default:
		return fmt.Errorf("unsupported scan src %T for nullable uuid", src)
	}
	return nil
}

type pgxNullableString struct {
	String string
	Valid  bool
}

func (n *pgxNullableString) Scan(src any) error {
	if src == nil {
		n.Valid = false
		return nil
	}
	switch v := src.(type) {
	case string:
		n.String, n.Valid = v, true
	case []byte:
		n.String, n.Valid = string(v), true
	default:
		return fmt.Errorf("unsupported scan src %T for nullable string", src)
	}
	return nil
}

func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	var pgErr interface{ SQLState() string }
	if errors.As(err, &pgErr) {
		return pgErr.SQLState() == "23505"
	}
	return false
}

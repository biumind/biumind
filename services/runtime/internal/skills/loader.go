package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/google/uuid"
)

// LoadedSkills is the per-session classification the engine consumes
// at the start of each turn. Three tiers per Skills-Design §8.0:
//
//	Pinned     — agent_skills.pinned=true. Body folded into system
//	             prompt directly so the model never spends a turn on
//	             skill.activate. Cap is governed by prompt budget,
//	             not by this loader.
//	AutoAttach — frontmatter `paths:` matches cwd or recently-touched.
//	             Same injection mechanic as Pinned (body in system
//	             prompt) but the trigger is the project tree, not an
//	             explicit toggle.
//	Available  — agent_skills.is_enabled=true (and not in the two
//	             classes above). Only (name, description) goes into
//	             the system prompt's available_skills block; the LLM
//	             must call skill.activate to load the body.
//
// Org-shared skills (source ∈ {bundled, org}) are visible to every
// agent in the org by default but are surfaced as Available only —
// they don't pin or auto-attach unless the user explicitly toggled
// them on. This matches Skills-Design's five-layer overlay rule
// where org sits at priority 5 (lowest).
type LoadedSkills struct {
	Pinned     []*Skill
	AutoAttach []*Skill
	Available  []*Skill
}

// LoadForAgentInput packs the per-call context the loader needs to
// classify skills. cwd + RecentlyTouched feed AutoAttach matching.
// Empty cwd skips path-based attachment entirely.
type LoadForAgentInput struct {
	OrgID            uuid.UUID
	AgentID          uuid.UUID
	Cwd              string
	RecentlyTouched  []string
	IncludeOrgShared bool // when true, org/bundled skills surface as Available even without an agent_skills row
}

// LoadForAgent builds the three-tier slice the engine drops into
// the next turn's system prompt. Returns ErrNotFound for the agent
// is treated as "no enabled skills" — empty result, nil error.
func (r *Registry) LoadForAgent(ctx context.Context, in LoadForAgentInput) (*LoadedSkills, error) {
	if in.OrgID == uuid.Nil {
		return nil, fmt.Errorf("org_id required")
	}

	// Two queries: agent's explicit enablement set, and the org
	// skills visible to everyone. Merging in Go avoids a UNION ALL
	// that gets harder to reason about as filters grow.

	// 1. Agent-explicit (with pinned flag).
	agentRows, err := r.pool.Query(ctx, `
		SELECT s.id, s.org_id, s.owner_id, s.identifier, s.name, s.description, s.source,
		       s.manifest, s.content, s.content_hash, s.resources, s.zip_file_sha256,
		       s.paths, s.permissions, s.status, s.created_at, s.updated_at,
		       a.pinned
		  FROM runtime.agent_skills a
		  JOIN runtime.skills s ON s.id = a.skill_id
		 WHERE a.agent_id = $1
		   AND a.is_enabled = true
		   AND s.status = 'active'
		   AND s.org_id = $2`,
		in.AgentID, in.OrgID)
	if err != nil {
		return nil, err
	}
	defer agentRows.Close()

	out := &LoadedSkills{}
	enabledIDs := make(map[string]struct{}) // dedupe vs the org-shared pass

	for agentRows.Next() {
		s, pinned, err := scanSkillWithPinned(agentRows)
		if err != nil {
			return nil, err
		}
		enabledIDs[s.ID] = struct{}{}
		switch {
		case pinned:
			out.Pinned = append(out.Pinned, s)
		case len(s.Paths) > 0 && pathMatches(s.Paths, in.Cwd, in.RecentlyTouched):
			out.AutoAttach = append(out.AutoAttach, s)
		default:
			out.Available = append(out.Available, s)
		}
	}
	if err := agentRows.Err(); err != nil {
		return nil, err
	}

	// 2. Org-shared visibility — bundled + org, not already covered
	// by the agent's explicit set. These never pin/auto-attach (the
	// user hasn't opted in yet); they only surface as Available so
	// the LLM can discover them via skill.list / skill.activate.
	if in.IncludeOrgShared {
		orgRows, err := r.pool.Query(ctx, selectSkillSQL+`
			 WHERE org_id = $1
			   AND status = 'active'
			   AND source IN ('bundled', 'org')
			   AND owner_id IS NULL`,
			in.OrgID)
		if err != nil {
			return nil, err
		}
		defer orgRows.Close()
		for orgRows.Next() {
			s, err := scanSkill(orgRows)
			if err != nil {
				return nil, err
			}
			if _, dup := enabledIDs[s.ID]; dup {
				continue
			}
			out.Available = append(out.Available, s)
		}
		if err := orgRows.Err(); err != nil {
			return nil, err
		}
	}

	return out, nil
}

// scanSkillWithPinned mirrors scanSkill but tacks on the pinned bool
// from the agent_skills join. Kept inline rather than reusing
// scanSkill so the column index doesn't drift if either side
// changes.
func scanSkillWithPinned(r rowScanner) (*Skill, bool, error) {
	var (
		s              Skill
		ownerID        pgxNullableUUID
		manifestBytes  []byte
		resourcesBytes []byte
		zipHash        pgxNullableString
		sourceStr      string
		statusStr      string
		paths          []string
		perms          []string
		pinned         bool
	)
	err := r.Scan(
		&s.ID, &s.OrgID, &ownerID, &s.Identifier, &s.Name, &s.Description,
		&sourceStr, &manifestBytes, &s.Content, &s.ContentHash, &resourcesBytes,
		&zipHash, &paths, &perms, &statusStr, &s.CreatedAt, &s.UpdatedAt,
		&pinned,
	)
	if err != nil {
		return nil, false, err
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
	if len(manifestBytes) > 0 {
		_ = json.Unmarshal(manifestBytes, &s.Manifest)
	}
	if len(resourcesBytes) > 0 {
		_ = json.Unmarshal(resourcesBytes, &s.Resources)
	}
	s.Paths = paths
	s.Permissions = perms
	return &s, pinned, nil
}

// pathMatches mirrors apps/cli/biu/internal/skills.matchesAny so the
// CLI loader and this server-side loader agree on a single rule:
//
//   - literal substring (no glob chars) → contained-in match
//   - `**`-bearing pattern → translated to a regex (* → [^/]*; ** → .*)
//   - other glob → filepath.Match against full path + basename
//
// We don't reuse the CLI helper directly because importing
// apps/cli/biu/internal/* from a server is a cyclic dependency
// trap once /healthz lands; the rule is small enough to inline.
func pathMatches(patterns []string, cwd string, recentlyTouched []string) bool {
	candidates := append([]string{cwd}, recentlyTouched...)
	for _, pat := range patterns {
		for _, p := range candidates {
			if matchOnePattern(pat, p) {
				return true
			}
		}
	}
	return false
}

func matchOnePattern(pat, target string) bool {
	if pat == "" || target == "" {
		return false
	}
	if !strings.ContainsAny(pat, "*?[") {
		return strings.Contains(target, pat)
	}
	if strings.Contains(pat, "**") {
		regexPat := regexp.QuoteMeta(pat)
		regexPat = strings.ReplaceAll(regexPat, `\*\*`, `.*`)
		regexPat = strings.ReplaceAll(regexPat, `\*`, `[^/]*`)
		regexPat = strings.ReplaceAll(regexPat, `\?`, `.`)
		re, err := regexp.Compile("(?:^|/)" + regexPat + "(?:$|/)")
		if err != nil {
			return false
		}
		return re.MatchString(target)
	}
	if ok, _ := filepath.Match(pat, target); ok {
		return true
	}
	if ok, _ := filepath.Match(pat, filepath.Base(target)); ok {
		return true
	}
	return false
}

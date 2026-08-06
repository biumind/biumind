// Package engine wraps cedar-go for policy evaluation.
//
// We isolate cedar-go behind this package so the upstream API can change
// (or be swapped to OPA) without touching API handlers.
package engine

import (
	"errors"
	"fmt"
	"sync"

	cedar "github.com/cedar-policy/cedar-go"
	cedartypes "github.com/cedar-policy/cedar-go/types"
)

// Decision is the public API; mirrors AuthzService Decision proto.
type Decision int

const (
	DecisionUnspecified Decision = iota
	DecisionAllow
	DecisionDeny
)

func (d Decision) String() string {
	switch d {
	case DecisionAllow:
		return "ALLOW"
	case DecisionDeny:
		return "DENY"
	}
	return "UNSPECIFIED"
}

// Input is what callers pass to Check.
type Input struct {
	Principal Entity
	Action    string // e.g. "wiki:Page::read"
	Resource  Entity
	Context   map[string]any // extra environment vars referenced from policies
}

// Entity describes a Cedar entity (principal or resource).
type Entity struct {
	Type       string         // e.g. "User" / "Page"
	ID         string         // e.g. "u-123"
	Attributes map[string]any // exposed in policies via principal.foo / resource.foo
	Parents    []EntityRef    // for hierarchy: User in Group::"team-x"
}

type EntityRef struct {
	Type string
	ID   string
}

// Result of a single decision.
type Result struct {
	Decision        Decision
	Reason          string   // human-readable summary
	MatchedPolicies []string // policy ids that fired
	Errors          []string
}

// Engine is the goroutine-safe Cedar evaluator.
type Engine struct {
	mu sync.RWMutex
	ps *cedar.PolicySet
}

// New returns an empty Engine. Call LoadPolicies() before evaluating.
func New() *Engine {
	return &Engine{ps: cedar.NewPolicySet()}
}

// LoadPolicies replaces the current PolicySet from raw cedar source bytes.
// Multiple files concatenated → one big text passed in.
func (e *Engine) LoadPolicies(raw []byte) error {
	ps, err := cedar.NewPolicySetFromBytes("biumind-policies", raw)
	if err != nil {
		return fmt.Errorf("authz: parse policies: %w", err)
	}
	e.mu.Lock()
	e.ps = ps
	e.mu.Unlock()
	return nil
}

// PolicyCount returns the number of compiled policies.
func (e *Engine) PolicyCount() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.ps == nil {
		return 0
	}
	return len(e.ps.Map())
}

// Check evaluates a single decision request.
func (e *Engine) Check(in Input) (*Result, error) {
	e.mu.RLock()
	ps := e.ps
	e.mu.RUnlock()
	if ps == nil {
		return nil, errors.New("authz: no policies loaded")
	}

	principalUID, err := uidFromEntity(in.Principal)
	if err != nil {
		return nil, fmt.Errorf("principal: %w", err)
	}
	resourceUID, err := uidFromEntity(in.Resource)
	if err != nil {
		return nil, fmt.Errorf("resource: %w", err)
	}
	actionUID, err := uidFromAction(in.Action)
	if err != nil {
		return nil, fmt.Errorf("action: %w", err)
	}

	entities := buildEntities(in.Principal, in.Resource)

	req := cedar.Request{
		Principal: principalUID,
		Action:    actionUID,
		Resource:  resourceUID,
		Context:   toRecord(in.Context),
	}

	dec, diag := ps.IsAuthorized(entities, req)

	res := &Result{}
	if bool(dec) {
		res.Decision = DecisionAllow
		res.Reason = "explicit allow"
	} else {
		res.Decision = DecisionDeny
		res.Reason = "no matching permit (or explicit forbid)"
	}
	for _, r := range diag.Reasons {
		res.MatchedPolicies = append(res.MatchedPolicies, string(r.PolicyID))
	}
	for _, er := range diag.Errors {
		res.Errors = append(res.Errors, er.String())
	}
	return res, nil
}

// ─── helpers ───────────────────────────────────────────

func uidFromEntity(e Entity) (cedartypes.EntityUID, error) {
	if e.Type == "" || e.ID == "" {
		return cedartypes.EntityUID{}, errors.New("missing type or id")
	}
	return cedartypes.NewEntityUID(cedartypes.EntityType(e.Type), cedartypes.String(e.ID)), nil
}

// Action format: "wiki:Page::read" → entity type "Action", id "wiki:Page::read"
func uidFromAction(action string) (cedartypes.EntityUID, error) {
	if action == "" {
		return cedartypes.EntityUID{}, errors.New("empty action")
	}
	return cedartypes.NewEntityUID("Action", cedartypes.String(action)), nil
}

func buildEntities(principal, resource Entity) cedartypes.EntityMap {
	em := cedartypes.EntityMap{}
	addEntity(em, principal)
	addEntity(em, resource)
	return em
}

func addEntity(em cedartypes.EntityMap, e Entity) {
	uid, err := uidFromEntity(e)
	if err != nil {
		return
	}
	parentUIDs := make([]cedartypes.EntityUID, 0, len(e.Parents))
	for _, p := range e.Parents {
		parentUIDs = append(parentUIDs, cedartypes.NewEntityUID(
			cedartypes.EntityType(p.Type), cedartypes.String(p.ID)))
	}
	em[uid] = cedartypes.Entity{
		UID:        uid,
		Parents:    cedartypes.NewEntityUIDSet(parentUIDs...),
		Attributes: toRecord(e.Attributes),
	}
}

// toRecord converts Go map[string]any to Cedar record.
func toRecord(m map[string]any) cedartypes.Record {
	if len(m) == 0 {
		return cedartypes.NewRecord(nil)
	}
	rm := make(cedartypes.RecordMap, len(m))
	for k, v := range m {
		rm[cedartypes.String(k)] = toValue(v)
	}
	return cedartypes.NewRecord(rm)
}

func toValue(v any) cedartypes.Value {
	switch x := v.(type) {
	case nil:
		return cedartypes.String("")
	case string:
		return cedartypes.String(x)
	case bool:
		return cedartypes.Boolean(x)
	case int:
		return cedartypes.Long(x)
	case int64:
		return cedartypes.Long(x)
	case float64:
		// Cedar has no float; truncate to Long (acceptable for counts/limits/scores)
		return cedartypes.Long(int64(x))
	case []string:
		out := make([]cedartypes.Value, len(x))
		for i, s := range x {
			out[i] = cedartypes.String(s)
		}
		return cedartypes.NewSet(out...)
	case []any:
		out := make([]cedartypes.Value, len(x))
		for i, e := range x {
			out[i] = toValue(e)
		}
		return cedartypes.NewSet(out...)
	default:
		return cedartypes.String(fmt.Sprintf("%v", x))
	}
}

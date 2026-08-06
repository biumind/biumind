// Package authz is Runtime's HTTP client for the central Authz
// service. Two callers today:
//
//   - services/runtime/internal/agent/skill_tools.go gates each
//     high-risk Skills tool call on a Cedar policy decision.
//   - services/runtime/internal/api/skills_propose_handlers.go
//     gates approve / reject / share-org on the same.
//
// We isolate the wire shape behind a tiny interface (Decider) so
// tests can swap a fixed-decision stub without spinning up an
// httptest server. Production uses NewHTTP(url).

package authz

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Decision mirrors the AllowDeny semantic the Authz service
// returns on /v1/authz/check. We model it as a string so callers can
// log "ALLOW" / "DENY" verbatim without an extra mapping.
type Decision string

const (
	Allow Decision = "ALLOW"
	Deny  Decision = "DENY"
)

// Decider is the slice of Authz's surface our tools need. Production
// implementation talks HTTP; tests use AlwaysAllow / Stub for
// determinism.
type Decider interface {
	Check(ctx context.Context, req Request) (*Result, error)
}

// Request shape mirrors services/authz/internal/api/api.go.
type Request struct {
	Principal Entity
	Action    string
	Resource  Entity
}

type Entity struct {
	Type       string
	ID         string
	Attributes map[string]any
}

type Result struct {
	Decision Decision
	Reason   string
}

// HTTP is the wire-bound Decider — POSTs JSON to /v1/authz/check.
// 3-second timeout matches the Realtime + Realtime->Authz pattern;
// callers should treat any error as fail-closed (deny) so a
// transient Authz outage doesn't accidentally permit high-risk
// tool calls.
type HTTP struct {
	URL string
	HC  *http.Client
}

func NewHTTP(url string) *HTTP {
	return &HTTP{
		URL: strings.TrimRight(url, "/"),
		HC:  &http.Client{Timeout: 3 * time.Second},
	}
}

type checkReq struct {
	Principal map[string]any `json:"principal"`
	Action    string         `json:"action"`
	Resource  map[string]any `json:"resource"`
}

type checkResp struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
}

func (c *HTTP) Check(ctx context.Context, req Request) (*Result, error) {
	if c.URL == "" {
		return nil, errors.New("authz: no URL configured")
	}
	body, _ := json.Marshal(checkReq{
		Principal: entityJSON(req.Principal),
		Action:    req.Action,
		Resource:  entityJSON(req.Resource),
	})
	httpReq, err := http.NewRequestWithContext(ctx,
		http.MethodPost, c.URL+"/v1/authz/check", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := c.HC.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("authz: http: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("authz: status %d body=%s", resp.StatusCode, string(raw))
	}
	var out checkResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &Result{Decision: Decision(out.Decision), Reason: out.Reason}, nil
}

func entityJSON(e Entity) map[string]any {
	out := map[string]any{
		"type":       e.Type,
		"id":         e.ID,
		"attributes": e.Attributes,
	}
	return out
}

// AlwaysAllow returns Allow for every Check — used in tests + dev
// modes when Authz isn't wired yet. Callers should NEVER use this
// in production paths; the runtime daemon's main.go logs a startup
// warning if AUTHZ_URL is empty.
type AlwaysAllow struct{}

func (AlwaysAllow) Check(_ context.Context, _ Request) (*Result, error) {
	return &Result{Decision: Allow, Reason: "always-allow stub"}, nil
}

// AlwaysDeny — the opposite stub, useful for verifying that a
// missed permit branch doesn't sneak through.
type AlwaysDeny struct{}

func (AlwaysDeny) Check(_ context.Context, _ Request) (*Result, error) {
	return &Result{Decision: Deny, Reason: "always-deny stub"}, nil
}

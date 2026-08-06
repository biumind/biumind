// Package authz is App Center's HTTP client for the central Authz
// service. Today's caller: the RSS BiuApp's org-scoped action handlers
// (M11) gate org reads/writes on a Cedar decision — org members may
// read org-scoped RSS resources, only org admins may write.
//
// The wire shape mirrors services/authz/internal/api/api.go. We isolate
// it behind the tiny Decider interface so the RSS app + tests can swap
// a fixed-decision stub without spinning up an httptest server. This is
// a deliberate re-implementation of the same pattern runtime/realtime
// use (services/runtime/internal/authz/client.go) — the repo convention
// is one client per service, not a shared go-sdk client.

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

// Decision mirrors the ALLOW/DENY semantic the Authz service returns on
// /v1/authz/check.
type Decision string

const (
	Allow Decision = "ALLOW"
	Deny  Decision = "DENY"
)

// Decider is the slice of Authz's surface we need. Production talks
// HTTP; tests use AlwaysAllow / AlwaysDeny for determinism.
type Decider interface {
	Check(ctx context.Context, req Request) (*Result, error)
}

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

// HTTP is the wire-bound Decider — POSTs JSON to /v1/authz/check. The
// 3-second timeout matches runtime/realtime. Callers MUST treat any
// error as fail-closed (deny) so a transient Authz outage never
// accidentally permits an org write.
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
	return map[string]any{
		"type":       e.Type,
		"id":         e.ID,
		"attributes": e.Attributes,
	}
}

// AlwaysAllow returns Allow for every Check — used in dev when AUTHZ_URL
// is unset. app_center main.go logs a startup WARN in that mode. NEVER
// in production org paths.
type AlwaysAllow struct{}

func (AlwaysAllow) Check(_ context.Context, _ Request) (*Result, error) {
	return &Result{Decision: Allow, Reason: "always-allow stub"}, nil
}

// AlwaysDeny — the opposite stub, for verifying a missed permit branch
// doesn't sneak through.
type AlwaysDeny struct{}

func (AlwaysDeny) Check(_ context.Context, _ Request) (*Result, error) {
	return &Result{Decision: Deny, Reason: "always-deny stub"}, nil
}

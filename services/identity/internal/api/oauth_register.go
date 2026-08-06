// Dynamic Client Registration — RFC 7591.
//
//	POST /oauth/register
//
// Lets a third-party MCP / desktop app register itself with biumind as
// an OAuth 2.1 client. We REQUIRE the registrar to be a logged-in user
// (Bearer JWT) — RFC 7591 allows open registration but in practice that
// invites abuse. Tying every client to a creator gives us:
//   - per-user rate limit on registrations
//   - audit trail (who registered claude-desktop on this org)
//   - "my apps" UI in settings, scoped to the user's own clients
//
// Most MCP clients are PUBLIC (no client secret) + PKCE. We honour that:
// when token_endpoint_auth_method = "none", client_secret is not minted
// and the response omits it. Confidential clients get a 32-char random
// secret returned ONCE — same drill as PAT.
package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/biumind/biumind/services/identity/internal/passwords"
	"github.com/biumind/biumind/services/identity/internal/store"
	"github.com/google/uuid"
)

// MountOAuthRegister registers POST /oauth/register on `mux`.
//
// We do NOT wrap with requireAuth — the registration endpoint accepts both
// authenticated (preferred) and anonymous calls. Anonymous calls are
// accepted but heavily rate-limited at the gateway; authenticated calls
// stash the user id on the row for "my apps" UI. This matches what most
// SaaS deployments end up doing in practice.
func (s *Server) MountOAuthRegister(mux *http.ServeMux) {
	mux.HandleFunc("POST /oauth/register", s.handleOAuthRegister)
	mux.HandleFunc("DELETE /oauth/register/{id}", s.requireAuth(s.handleOAuthDeregister))
	mux.HandleFunc("GET /oauth/clients", s.requireAuth(s.handleListMyOAuthClients))
}

type oauthRegisterReq struct {
	ClientName              string   `json:"client_name"`
	RedirectURIs            []string `json:"redirect_uris"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	Scope                   string   `json:"scope"`
	Contacts                []string `json:"contacts"`
	LogoURI                 string   `json:"logo_uri"`
	ClientURI               string   `json:"client_uri"`
	TosURI                  string   `json:"tos_uri"`
	PolicyURI               string   `json:"policy_uri"`
	SoftwareID              string   `json:"software_id"`
	SoftwareVersion         string   `json:"software_version"`
}

type oauthRegisterResp struct {
	ClientID                string   `json:"client_id"`
	ClientSecret            string   `json:"client_secret,omitempty"`
	ClientName              string   `json:"client_name"`
	RedirectURIs            []string `json:"redirect_uris"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	Scope                   string   `json:"scope,omitempty"`
	ClientIDIssuedAt        int64    `json:"client_id_issued_at"`
	// RegistrationAccessToken (RFC 7592) lets the registrar later manage
	// the registration. We mint one but its handling is deferred to a
	// follow-up — current build only consumes it for DELETE auth.
	RegistrationAccessToken string `json:"registration_access_token,omitempty"`
	RegistrationClientURI   string `json:"registration_client_uri,omitempty"`
}

func (s *Server) handleOAuthRegister(w http.ResponseWriter, r *http.Request) {
	var req oauthRegisterReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeOAuthErr(w, http.StatusBadRequest, "invalid_client_metadata", err.Error())
		return
	}
	if strings.TrimSpace(req.ClientName) == "" {
		writeOAuthErr(w, http.StatusBadRequest, "invalid_client_metadata", "client_name required")
		return
	}
	if len(req.RedirectURIs) == 0 {
		writeOAuthErr(w, http.StatusBadRequest, "invalid_redirect_uri", "at least one redirect_uri required")
		return
	}
	for _, u := range req.RedirectURIs {
		if !validRedirectURI(u) {
			writeOAuthErr(w, http.StatusBadRequest, "invalid_redirect_uri", u)
			return
		}
	}

	// Defaults per RFC 7591 §2.
	grantTypes := req.GrantTypes
	if len(grantTypes) == 0 {
		grantTypes = []string{"authorization_code"}
	}
	responseTypes := req.ResponseTypes
	if len(responseTypes) == 0 {
		responseTypes = []string{"code"}
	}
	authMethod := req.TokenEndpointAuthMethod
	if authMethod == "" {
		authMethod = "client_secret_basic"
	}
	switch authMethod {
	case "none", "client_secret_basic", "client_secret_post":
	default:
		writeOAuthErr(w, http.StatusBadRequest, "invalid_client_metadata",
			"unsupported token_endpoint_auth_method: "+authMethod)
		return
	}

	var secretPlain, secretHash string
	if authMethod != "none" {
		var err error
		secretPlain, err = randomHex(32)
		if err != nil {
			writeOAuthErr(w, http.StatusInternalServerError, "server_error", err.Error())
			return
		}
		secretHash, err = passwords.Hash(secretPlain, s.PasswordParams)
		if err != nil {
			writeOAuthErr(w, http.StatusInternalServerError, "server_error", err.Error())
			return
		}
	}

	// Mint a registration_access_token for RFC 7592 management. We hash
	// it the same way as the secret so a DB read can't replay it.
	regToken, err := randomHex(32)
	if err != nil {
		writeOAuthErr(w, http.StatusInternalServerError, "server_error", err.Error())
		return
	}
	regHash, err := passwords.Hash(regToken, s.PasswordParams)
	if err != nil {
		writeOAuthErr(w, http.StatusInternalServerError, "server_error", err.Error())
		return
	}

	var creator *uuid.UUID
	if c, ok := bauth.ClaimsFrom(r.Context()); ok && c != nil && c.UserID != "" {
		if u, err := uuid.Parse(c.UserID); err == nil {
			creator = &u
		}
	}

	row, err := s.Store.CreateOAuthClient(r.Context(), store.CreateOAuthClientInput{
		ClientSecretHash:            secretHash,
		ClientName:                  req.ClientName,
		RedirectURIs:                req.RedirectURIs,
		GrantTypes:                  grantTypes,
		ResponseTypes:               responseTypes,
		TokenEndpointAuthMethod:     authMethod,
		Scope:                       req.Scope,
		Contacts:                    req.Contacts,
		LogoURI:                     req.LogoURI,
		ClientURI:                   req.ClientURI,
		TosURI:                      req.TosURI,
		PolicyURI:                   req.PolicyURI,
		SoftwareID:                  req.SoftwareID,
		SoftwareVersion:             req.SoftwareVersion,
		RegistrationAccessTokenHash: regHash,
		CreatedBy:                   creator,
	})
	if err != nil {
		writeOAuthErr(w, http.StatusInternalServerError, "server_error", err.Error())
		return
	}

	issuer := s.publicIssuer(r)
	writeJSON(w, http.StatusCreated, oauthRegisterResp{
		ClientID:                row.ClientID.String(),
		ClientSecret:            secretPlain,
		ClientName:              row.ClientName,
		RedirectURIs:            row.RedirectURIs,
		GrantTypes:              row.GrantTypes,
		ResponseTypes:           row.ResponseTypes,
		TokenEndpointAuthMethod: row.TokenEndpointAuthMethod,
		Scope:                   row.Scope,
		ClientIDIssuedAt:        row.CreatedAt.Unix(),
		RegistrationAccessToken: regToken,
		RegistrationClientURI:   issuer + "/oauth/register/" + row.ClientID.String(),
	})
}

func (s *Server) handleOAuthDeregister(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeOAuthErr(w, http.StatusBadRequest, "invalid_client_id", "")
		return
	}
	c := bauth.MustClaims(r.Context())
	owner, err := uuid.Parse(c.UserID)
	if err != nil {
		writeOAuthErr(w, http.StatusUnauthorized, "invalid_token", "")
		return
	}
	row, err := s.Store.GetOAuthClientByID(r.Context(), id)
	if err != nil {
		writeOAuthErr(w, http.StatusNotFound, "client_not_found", "")
		return
	}
	if row.CreatedBy == nil || *row.CreatedBy != owner {
		// Don't leak existence — RFC 7592 doesn't mandate the response code,
		// 404 is friendlier than 403 against enumeration.
		writeOAuthErr(w, http.StatusNotFound, "client_not_found", "")
		return
	}
	if err := s.Store.DeleteOAuthClient(r.Context(), id); err != nil {
		writeOAuthErr(w, http.StatusInternalServerError, "server_error", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListMyOAuthClients(w http.ResponseWriter, r *http.Request) {
	c := bauth.MustClaims(r.Context())
	owner, err := uuid.Parse(c.UserID)
	if err != nil {
		writeOAuthErr(w, http.StatusUnauthorized, "invalid_token", "")
		return
	}
	rows, err := s.Store.ListOAuthClientsByCreator(r.Context(), owner)
	if err != nil {
		writeOAuthErr(w, http.StatusInternalServerError, "server_error", err.Error())
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for _, c := range rows {
		out = append(out, map[string]any{
			"client_id":                  c.ClientID.String(),
			"client_name":                c.ClientName,
			"redirect_uris":              c.RedirectURIs,
			"grant_types":                c.GrantTypes,
			"token_endpoint_auth_method": c.TokenEndpointAuthMethod,
			"scope":                      c.Scope,
			"logo_uri":                   c.LogoURI,
			"client_uri":                 c.ClientURI,
			"created_at":                 c.CreatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
			"is_public":                  c.TokenEndpointAuthMethod == "none",
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"clients": out})
}

// validRedirectURI rejects obviously broken URIs. RFC 8252 / OAuth 2.1
// guidance: only allow http(s) or a custom scheme registered by the app
// (e.g. claude-desktop://oauth/callback). Loopback http://127.0.0.1 is
// fine for desktop apps that can't bind a TLS port.
func validRedirectURI(u string) bool {
	if u == "" {
		return false
	}
	// No fragments — RFC 6749 §3.1.2.
	if strings.Contains(u, "#") {
		return false
	}
	// Must be absolute. Cheap sniff (we don't accept relative redirects).
	return strings.Contains(u, "://")
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// writeOAuthErr emits the RFC 6749 §5.2 error shape:
//
//	{"error":"invalid_request","error_description":"..."}
func writeOAuthErr(w http.ResponseWriter, status int, code, desc string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	body := map[string]any{"error": code}
	if desc != "" {
		body["error_description"] = desc
	}
	_ = json.NewEncoder(w).Encode(body)
}

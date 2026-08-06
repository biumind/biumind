// OAuth 2.1 Authorization Server metadata — RFC 8414.
//
//	GET /.well-known/oauth-authorization-server
//
// Third-party clients (and well-behaved MCP libraries) discover our
// endpoints by fetching this document. Issuer comes from the configured
// public base URL — in dev that's http://localhost:7004, in prod it's
// the public identity URL.
package api

import (
	"net/http"
	"strings"
)

// MountOAuthMetadata registers the discovery endpoint.
func (s *Server) MountOAuthMetadata(mux *http.ServeMux) {
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", s.handleOAuthMetadata)
}

func (s *Server) handleOAuthMetadata(w http.ResponseWriter, r *http.Request) {
	issuer := s.publicIssuer(r)
	doc := map[string]any{
		"issuer":                                issuer,
		"authorization_endpoint":                issuer + "/oauth/authorize",
		"token_endpoint":                        issuer + "/oauth/token",
		"revocation_endpoint":                   issuer + "/oauth/revoke",
		"registration_endpoint":                 issuer + "/oauth/register",
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":      []string{"S256"},
		"token_endpoint_auth_methods_supported": []string{"none", "client_secret_basic", "client_secret_post"},
		"scopes_supported":                      []string{"read", "write", "openid"},
		"service_documentation":                 "https://github.com/biumind/biumind",
	}
	writeJSON(w, http.StatusOK, doc)
}

// publicIssuer derives the AS issuer from the request URL. We can't hardcode
// the base because the same service is reachable via multiple hosts (dev
// LAN IP, prod fqdn, ngrok tunnel during integration); using the request
// URL keeps the metadata document self-consistent for whoever fetched it.
func (s *Server) publicIssuer(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	host := r.Host
	if h := r.Header.Get("X-Forwarded-Host"); h != "" {
		host = h
	}
	return scheme + "://" + host
}

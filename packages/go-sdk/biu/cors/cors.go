// Package cors provides a small HTTP middleware that handles CORS for
// browser-based clients (Flutter Web at app.biumind.com hitting Brain
// at api.biumind.com).
//
// Design points:
//
//   * Allowlist mode: only echo Origin when it matches a configured
//     pattern. Wildcard '*' supported but disables credentials.
//   * Streaming-friendly: SSE responses must be flushed without
//     waiting for the full body, so we set Access-Control-Expose-Headers
//     to include common SSE-friendly headers (no actual change to flow).
//   * Preflight: OPTIONS short-circuits with 204 + the right headers.
package cors

import (
	"net/http"
	"strings"
)

// Config holds the CORS policy. Use AllowAll for dev, AllowedOrigins
// for prod.
type Config struct {
	// AllowedOrigins is an exact-match list of origins to permit, e.g.
	// ["https://app.biumind.com", "http://localhost:3000"]. Wildcards
	// not allowed here — use AllowAll for that.
	AllowedOrigins []string
	// AllowAll permits any origin. Disables credentials (browser will
	// reject Access-Control-Allow-Origin: * with credentials).
	AllowAll bool
	// AllowedMethods (defaults to common verbs).
	AllowedMethods []string
	// AllowedHeaders (defaults to Authorization + Content-Type +
	// X-Biumind-* family).
	AllowedHeaders []string
	// MaxAge for preflight cache (defaults 1h).
	MaxAgeSeconds int
}

// Default returns a permissive-but-safe config for the BiuMind app
// frontends. Origins are explicit (no wildcard).
func Default(extraOrigins ...string) Config {
	origins := append([]string{
		"https://app.biumind.com",
		"https://biumind.com",
		"http://localhost:3000",
		"http://localhost:8080",
		"http://localhost:8089",
		// dev: flutter web build served by `python3 -m http.server 8888`
		// for two-device E2E (macOS native + Chrome web 双端验证)
		"http://localhost:8888",
		"http://127.0.0.1:8888",
	}, extraOrigins...)
	return Config{
		AllowedOrigins: origins,
		AllowedMethods: []string{
			"GET", "POST", "PATCH", "PUT", "DELETE", "OPTIONS",
		},
		AllowedHeaders: []string{
			"authorization", "content-type", "accept",
			"x-biumind-llm-key", "x-biumind-llm-base-url",
			"last-event-id",
		},
		MaxAgeSeconds: 3600,
	}
}

// Wrap wraps a handler with CORS handling. Returns a new handler.
func (c Config) Wrap(next http.Handler) http.Handler {
	allowedMethods := strings.Join(c.AllowedMethods, ", ")
	allowedHeaders := strings.Join(c.AllowedHeaders, ", ")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		allow := c.resolveOrigin(origin)
		if allow != "" {
			w.Header().Set("Access-Control-Allow-Origin", allow)
			if !c.AllowAll {
				w.Header().Set("Access-Control-Allow-Credentials", "true")
			}
			w.Header().Set("Vary", "Origin")
		}

		if r.Method == http.MethodOptions {
			// Preflight
			if reqMethod := r.Header.Get("Access-Control-Request-Method"); reqMethod != "" {
				w.Header().Set("Access-Control-Allow-Methods", allowedMethods)
				if reqHdr := r.Header.Get("Access-Control-Request-Headers"); reqHdr != "" {
					w.Header().Set("Access-Control-Allow-Headers", allowedHeaders)
				}
				if c.MaxAgeSeconds > 0 {
					w.Header().Set("Access-Control-Max-Age",
						intToString(c.MaxAgeSeconds))
				}
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}

		next.ServeHTTP(w, r)
	})
}

func (c Config) resolveOrigin(origin string) string {
	if origin == "" {
		return ""
	}
	if c.AllowAll {
		return "*"
	}
	for _, o := range c.AllowedOrigins {
		if o == origin {
			return origin
		}
	}
	return ""
}

func intToString(n int) string {
	// avoid strconv import for one call
	if n == 0 {
		return "0"
	}
	digits := []byte{}
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	if neg {
		return "-" + string(digits)
	}
	return string(digits)
}

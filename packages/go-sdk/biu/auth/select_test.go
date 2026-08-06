package auth

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

// captureLogs replaces slog default for the duration of fn, returning
// whatever was written to stderr-equivalent.
func captureLogs(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })
	fn()
	return buf.String()
}

func TestSelectVerifier_JWKSPathLogsInfo(t *testing.T) {
	logs := captureLogs(t, func() {
		// Don't actually fetch — NewJWKSVerifier returns a verifier
		// that lazy-fetches on first Verify call. SelectVerifier just
		// chooses the path. Empty secret → pure JWKS path (config ①);
		// a non-empty secret would legitimately enter the hybrid branch
		// that also logs "HS256 fallback" (service-to-service self-signed).
		_ = SelectVerifier("http://identity:7004/.well-known/jwks.json",
			"", "iss", "aud")
	})
	if !strings.Contains(logs, "RS256 + JWKS") {
		t.Errorf("expected RS256+JWKS info log; got %q", logs)
	}
	if strings.Contains(logs, "HS256 fallback") {
		t.Errorf("did not expect HS256 fallback log; got %q", logs)
	}
}

func TestSelectVerifier_HS256FallbackInDev(t *testing.T) {
	t.Setenv("BIUMIND_ENV", "dev")
	logs := captureLogs(t, func() {
		_ = SelectVerifier("", "secret-32-chars-or-longer-for-tests",
			"iss", "aud")
	})
	if !strings.Contains(logs, "WARN") {
		t.Errorf("dev fallback should be WARN; got %q", logs)
	}
	if !strings.Contains(logs, "HS256 fallback") {
		t.Errorf("expected fallback message; got %q", logs)
	}
	if strings.Contains(logs, "ERROR") {
		t.Errorf("dev fallback should not be ERROR; got %q", logs)
	}
}

func TestSelectVerifier_HS256FallbackInProdEscalates(t *testing.T) {
	for _, env := range []string{"prod", "production", "live", "PROD", " PROD "} {
		t.Run(env, func(t *testing.T) {
			t.Setenv("BIUMIND_ENV", env)
			logs := captureLogs(t, func() {
				_ = SelectVerifier("", "secret-32-chars-or-longer",
					"iss", "aud")
			})
			if !strings.Contains(logs, "ERROR") {
				t.Errorf("prod fallback must emit ERROR; got %q", logs)
			}
			if !strings.Contains(logs, "HS256 fallback in production") {
				t.Errorf("expected production-specific message; got %q", logs)
			}
			if !strings.Contains(logs, "set IDENTITY_JWKS_URL") {
				t.Errorf("expected actionable hint; got %q", logs)
			}
		})
	}
}

func TestSelectVerifier_OtherEnvIsDev(t *testing.T) {
	for _, env := range []string{"", "test", "staging", "dev"} {
		t.Run("env="+env, func(t *testing.T) {
			t.Setenv("BIUMIND_ENV", env)
			logs := captureLogs(t, func() {
				_ = SelectVerifier("", "s", "i", "a")
			})
			if strings.Contains(logs, "ERROR") {
				t.Errorf("non-prod env %q should not escalate; got %q", env, logs)
			}
		})
	}
}

// SelectVerifier — convenience for service main()s.
//
// Picks the right verifier based on what's configured:
//   * If `jwksURL` is non-empty → RS256 / JWKS (production path).
//   * Otherwise → HS256 with the shared secret (dev / tests).
//
// Centralising the choice keeps every service's main.go to one line:
//
//   verifier := bauth.SelectVerifier(cfg.IdentityJWKSURL, cfg.JWTSecret,
//                                    cfg.JWTIssuer, cfg.JWTAudience)

package auth

import (
	"log/slog"
	"os"
	"strings"
	"time"
)

// DefaultJWKSRefresh — verifiers re-poll Identity at this cadence
// regardless of cache misses. 10 min picks up rotations within minutes
// without spamming Identity from every dependent service.
const DefaultJWKSRefresh = 10 * time.Minute

func SelectVerifier(jwksURL, secret, issuer, audience string) *Verifier {
	// 三种合法配置：
	//   1. 仅 jwksURL    → RS256 / JWKS（生产路径，纯用户 token）
	//   2. 仅 secret     → HS256（dev / 测试 / 单服务）
	//   3. 两者都有       → 混合：先按 alg 头分发到对应密钥
	//                      （生产路径 + 接受跨服务自签 HS256，例如 runtime
	//                      调 brain register environment 用 JWT_SECRET 签）
	if jwksURL != "" {
		v := NewJWKSVerifier(jwksURL, issuer, audience, DefaultJWKSRefresh)
		if secret != "" {
			// 混合：v.jwks 已经设了，再补 secret，让 HS256 也能验
			v.Secret = []byte(secret)
			slog.Info("auth: RS256 + JWKS + HS256 fallback",
				"jwks_url", jwksURL, "refresh", DefaultJWKSRefresh,
				"note", "service-to-service self-signed HS256 also accepted")
		} else {
			slog.Info("auth: RS256 + JWKS only",
				"jwks_url", jwksURL, "refresh", DefaultJWKSRefresh)
		}
		return v
	}

	// HS256 fallback. Loudly flag it as a misconfiguration in prod
	// — every service sharing one symmetric secret is a single point
	// of compromise: leaking JWT_SECRET from any one service hands
	// an attacker the keys to mint tokens for every service. Don't
	// crash (avoids breaking emergency rollbacks) but make the log
	// impossible to miss in dashboards.
	if isProd() {
		slog.Error("auth: HS256 fallback in production — no JWKS url",
			"action", "set IDENTITY_JWKS_URL on this service",
			"risk", "shared symmetric secret across services; one leak = full compromise")
	} else {
		slog.Warn("auth: HS256 fallback (dev / no IDENTITY_JWKS_URL)")
	}
	return NewVerifier(secret, issuer, audience)
}

// isProd inspects BIUMIND_ENV to decide whether to escalate the HS256
// fallback warning to an Error log. We accept "prod" / "production" /
// "live" — different operators name it differently, and false negatives
// (treating prod as dev) are worse than false positives.
func isProd() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("BIUMIND_ENV"))) {
	case "prod", "production", "live":
		return true
	}
	return false
}

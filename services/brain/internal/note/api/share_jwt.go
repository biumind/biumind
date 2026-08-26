// 笔记分享访问 JWT —— 访客解锁密码后签发的短期凭证（§7.6 定案：
// 短期 JWT 而非服务端会话）。
//
//   - HS256 服务端密钥（env BRAIN_SHARE_SIGNING_KEY；空则启动随机生成，
//     多实例必须显式配置——否则各实例签发的 token 互不认）。
//   - claims：share_id + credential_version，exp 2h；改密 / rotate →
//     credential_version+1 → 已签发 JWT 立即全失效（验签时按行现值比对，
//     不等 exp）。
//   - 传递双通道：Authorization: Bearer 或 ?access_token= query——
//     图片/附件是浏览器原生 <img> 请求无法带 header，query 通道必须支持。
//
// JWT 库复用 go-sdk/biu/auth 既有依赖（golang-jwt/v5）；不走
// bauth.Signer——访客凭证无 sub/aud 等 BiuMind 标准 claims，独立
// claim 集更直白。
package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/biumind/biumind/services/brain/internal/note/store"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// shareAccessTTL —— 访问凭证有效期 2h（契约冻结）。
const shareAccessTTL = 2 * time.Hour

type shareAccessClaims struct {
	ShareID           string `json:"share_id"`
	CredentialVersion int    `json:"credential_version"`
	jwt.RegisteredClaims
}

// signShareAccess —— 解锁成功签发访问 JWT。
func signShareAccess(key []byte, shareID uuid.UUID, credentialVersion int) (string, error) {
	now := time.Now()
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, shareAccessClaims{
		ShareID:           shareID.String(),
		CredentialVersion: credentialVersion,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(shareAccessTTL)),
		},
	})
	return tok.SignedString(key)
}

// shareAccessToken —— 双通道提取：Authorization: Bearer 优先，
// ?access_token= query 兜底（浏览器原生 <img> 请求）。
func shareAccessToken(r *http.Request) string {
	if auth := r.Header.Get("Authorization"); len(auth) > 7 &&
		strings.EqualFold(auth[:7], "Bearer ") {
		return auth[7:]
	}
	return r.URL.Query().Get("access_token")
}

// verifyShareAccess —— 校验访问 JWT：签名 + exp + share_id 匹配 +
// credential_version 等于 share 行现值（改密/rotate 即时失效）。
func (s *Server) verifyShareAccess(r *http.Request, sh *store.Share) bool {
	raw := shareAccessToken(r)
	if raw == "" || len(s.ShareSigningKey) == 0 {
		return false
	}
	claims := &shareAccessClaims{}
	tok, err := jwt.ParseWithClaims(raw, claims, func(t *jwt.Token) (any, error) {
		if t.Method.Alg() != "HS256" {
			return nil, errors.New("unexpected signing method")
		}
		return s.ShareSigningKey, nil
	})
	if err != nil || !tok.Valid {
		return false
	}
	return claims.ShareID == sh.ID.String() &&
		claims.CredentialVersion == sh.CredentialVersion
}

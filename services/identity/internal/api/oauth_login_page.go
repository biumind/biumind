// OAuth 浏览器登录页 — identity 首次 serve HTML.
//
//	GET  /oauth/login?return_to=<urlencoded authorize URL>   渲染登录表单
//	POST /oauth/login   (application/x-www-form-urlencoded)  校验 → cookie → 302
//
// 用途: biu CLI 打开浏览器走 /oauth/authorize 时, 用户若未登录会被 302
// 到这里 (见 oauth_authorize.go unauthenticatedAuthorize); 登录成功签发
// session JWT 写 bm_session cookie 并 302 回 return_to (即原始 authorize
// URL), 授权流继续.
//
// 安全面:
//   - return_to 严格校验 — 只允许回本服务 /oauth/authorize, 防 open redirect
//   - 内联 html/template, 无外部资源; CSP default-src 'none'
//   - 密码校验复用 /v1/auth/login 的底层函数 (store + passwords.Verify),
//     失败文案不区分 "用户不存在" / "密码错", 防账户枚举; 审计/throttle
//     与 handleLogin 同款
package api

import (
	"context"
	"html/template"
	"net/http"
	"net/url"
	"strings"

	"github.com/biumind/biumind/services/identity/internal/admin"
	"github.com/biumind/biumind/services/identity/internal/passwords"
	"github.com/biumind/biumind/services/identity/internal/store"
)

// sessionCookieName — 浏览器 OAuth 流的会话 cookie, /oauth/authorize 的
// session 提取以它为第一优先级 (cookie → Bearer → ?access_token=).
const sessionCookieName = "bm_session"

// MountOAuthLoginPage registers GET/POST /oauth/login.
func (s *Server) MountOAuthLoginPage(mux *http.ServeMux) {
	mux.HandleFunc("GET /oauth/login", s.handleOAuthLoginPage)
	mux.HandleFunc("POST /oauth/login", s.handleOAuthLoginSubmit)
}

// loginPageData — 模板入参. Error 非空时显示错误条; Email 失败时回填.
type loginPageData struct {
	ReturnTo string
	Email    string
	Error    string
}

var loginPageTmpl = template.Must(template.New("login").Parse(`<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>登录 BiuMind</title>
<style>
  body { margin: 0; min-height: 100vh; display: flex; align-items: center; justify-content: center;
         background: #f5f6f8; font-family: -apple-system, "PingFang SC", "Helvetica Neue", sans-serif; }
  .card { background: #fff; border-radius: 12px; padding: 32px; width: 320px;
          box-shadow: 0 2px 12px rgba(0,0,0,.08); }
  h1 { font-size: 20px; margin: 0 0 4px; }
  .sub { color: #888; font-size: 13px; margin: 0 0 20px; }
  label { display: block; font-size: 13px; color: #555; margin: 12px 0 4px; }
  input { width: 100%; box-sizing: border-box; padding: 9px 10px; font-size: 14px;
          border: 1px solid #ddd; border-radius: 8px; }
  input:focus { outline: none; border-color: #4f7cff; }
  button { width: 100%; margin-top: 20px; padding: 10px; font-size: 15px; color: #fff;
           background: #4f7cff; border: none; border-radius: 8px; cursor: pointer; }
  .err { background: #fef0f0; color: #d43d3d; font-size: 13px; border-radius: 8px;
         padding: 8px 10px; margin: 0 0 12px; }
</style>
</head>
<body>
<div class="card">
  <h1>登录 BiuMind</h1>
  <p class="sub">登录后将继续完成应用授权</p>
  {{if .Error}}<p class="err">{{.Error}}</p>{{end}}
  <form method="post" action="/oauth/login">
    <input type="hidden" name="return_to" value="{{.ReturnTo}}">
    <label for="email">邮箱</label>
    <input id="email" name="email" type="email" required autocomplete="username" value="{{.Email}}">
    <label for="password">密码</label>
    <input id="password" name="password" type="password" required autocomplete="current-password">
    <button type="submit">登录</button>
  </form>
</div>
</body>
</html>`))

// handleOAuthLoginPage — 渲染登录表单. return_to 缺失 / 非法 → 400.
func (s *Server) handleOAuthLoginPage(w http.ResponseWriter, r *http.Request) {
	returnTo, ok := validateReturnTo(r, r.URL.Query().Get("return_to"))
	if !ok {
		writeOAuthErr(w, http.StatusBadRequest, "invalid_request",
			"return_to missing or not allowed (must be same-origin /oauth/authorize)")
		return
	}
	s.renderLoginPage(w, http.StatusOK, loginPageData{ReturnTo: returnTo})
}

// handleOAuthLoginSubmit — 表单提交. 成功: bm_session cookie + 302 回
// return_to; 失败: 重渲染表单 + 错误文案 (不泄露用户是否存在).
func (s *Server) handleOAuthLoginSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeOAuthErr(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	returnTo, ok := validateReturnTo(r, r.PostForm.Get("return_to"))
	if !ok {
		writeOAuthErr(w, http.StatusBadRequest, "invalid_request",
			"return_to missing or not allowed (must be same-origin /oauth/authorize)")
		return
	}
	email := strings.ToLower(strings.TrimSpace(r.PostForm.Get("email")))
	password := r.PostForm.Get("password")
	ip := admin.ClientIP(r)
	ua := r.UserAgent()

	u, credOK := s.checkOAuthLoginCredentials(r.Context(), email, password)
	if !credOK {
		// 与 handleLogin 同款: 不区分用户不存在 / 无密码 / 密码错.
		s.appendAudit(admin.AuditEvent{
			ActorEmail: email, ActorIP: ip, ActorUA: ua,
			Action: "auth.login.failed", Resource: "session",
			Success: false, ErrorCode: "invalid_credentials",
			ErrorMessage: "oauth login page: bad credentials",
		})
		if s.LoginThrottle != nil {
			s.LoginThrottle.recordFailure(email, ip, ua, s)
		}
		s.renderLoginPage(w, http.StatusUnauthorized, loginPageData{
			ReturnTo: returnTo, Email: email,
			Error: "邮箱或密码不正确",
		})
		return
	}
	// 与 handleLogin 一致: 邮箱未验证不放行 (产品流程, 不喂 throttle).
	if u.EmailVerifiedAt == nil {
		s.appendAudit(admin.AuditEvent{
			ActorID: u.ID.String(), ActorEmail: email, ActorRole: u.Role,
			ActorIP: ip, ActorUA: ua,
			Action: "auth.login.email_not_verified", Resource: "session",
			Success: false, ErrorCode: "email_not_verified",
			ErrorMessage: "user has not completed email verification",
		})
		s.renderLoginPage(w, http.StatusForbidden, loginPageData{
			ReturnTo: returnTo, Email: email,
			Error: "邮箱尚未验证, 请先完成邮箱验证后再登录",
		})
		return
	}

	// 签发与 /v1/auth/login 同等 TTL/claims 的 session JWT (无 refresh —
	// cookie 只服务授权弹跳这一个短窗口, 过期后用户重新登录即可).
	accessTok, err := s.Signer.Sign(buildClaims(u, ""))
	if err != nil {
		s.logErr("oauth login: sign session jwt", err, "user_id", u.ID.String())
		writeOAuthErr(w, http.StatusInternalServerError, "server_error", err.Error())
		return
	}
	s.appendAudit(admin.AuditEvent{
		ActorID: u.ID.String(), ActorEmail: email, ActorRole: u.Role,
		ActorIP: ip, ActorUA: ua,
		Action: "auth.login.success", Resource: "session",
		Success: true, Detail: "via=oauth_login_page",
	})
	if s.LoginThrottle != nil {
		s.LoginThrottle.recordSuccess(email, ip)
	}

	// Secure 仅在 https 时置位 — 本地 dev (http://127.0.0.1:7004 直连) 不
	// 置, 否则浏览器拒存; 生产经 nginx/LB 终结 TLS, 以 X-Forwarded-Proto 为准.
	secure := r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    accessTok,
		Path:     "/",
		MaxAge:   int(s.AccessTTL.Seconds()),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, returnTo, http.StatusFound)
}

// checkOAuthLoginCredentials — 复用 /v1/auth/login 的底层校验
// (Store.GetUserByEmail + passwords.Verify), 不复制哈希逻辑. 返
// (user, true) 仅当用户存在 + 有密码 + 密码匹配.
func (s *Server) checkOAuthLoginCredentials(ctx context.Context, email, password string) (*store.User, bool) {
	u, err := s.Store.GetUserByEmail(ctx, email)
	if err != nil || u.PasswordHash == nil {
		return nil, false
	}
	ok, err := passwords.Verify(password, *u.PasswordHash)
	if err != nil || !ok {
		return nil, false
	}
	return u, true
}

// validateReturnTo — 只允许回到本服务 /oauth/authorize (同源), 防 open
// redirect. 接受相对 URL (/oauth/authorize?...) 或与请求同 host 的 http(s)
// 绝对 URL; 其余 (外域 / javascript: / 其他路径 / 空 query) 一律拒绝.
// 通过时一律返回相对 URL — 同源由构造保证, 也不依赖 Host 头.
func validateReturnTo(r *http.Request, raw string) (string, bool) {
	if raw == "" {
		return "", false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", false
	}
	if u.Host != "" {
		// 绝对 URL: scheme 必须 http(s), host 必须与请求一致.
		if u.Scheme != "https" && u.Scheme != "http" {
			return "", false
		}
		if !strings.EqualFold(u.Host, r.Host) {
			return "", false
		}
	} else if u.Scheme != "" {
		// 无 host 的 scheme (javascript: / data:) 拒绝.
		return "", false
	}
	if u.Path != "/oauth/authorize" || u.RawQuery == "" {
		return "", false
	}
	return u.RequestURI(), true
}

// renderLoginPage — 写安全响应头 + 渲染模板. CSP 按方案: default-src 'none';
// style-src 'unsafe-inline' (form-action 不回落 default-src, 表单 POST 同源
// 不受影响).
func (s *Server) renderLoginPage(w http.ResponseWriter, status int, data loginPageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := loginPageTmpl.Execute(w, data); err != nil {
		s.logErr("oauth login: render template", err)
	}
}

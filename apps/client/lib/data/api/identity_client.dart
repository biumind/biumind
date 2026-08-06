// IdentityClient — thin wrapper for the Identity service auth endpoints.
//
// Mirrors services/identity/internal/api contract:
//
//   POST /v1/auth/register             create account, returns
//                                      {user_id, email, verification_required}
//                                      (no tokens — must verify email first)
//   POST /v1/auth/verify-email         {email, code} → access+refresh tokens
//   POST /v1/auth/resend-verification  {email} → {email_sent}
//   POST /v1/auth/login                email + password → tokens
//                                      (returns 403 email_not_verified when pending)
//   POST /v1/auth/refresh              refresh_token → new access_token
//
// Stdlib http via _http_helpers (matches memory_client / wiki_client pattern).

import 'dart:convert';

import '_http_helpers.dart';

class IdentityAuthResult {
  final String accessToken;
  final String refreshToken;
  final int expiresInSeconds;
  /// Refresh token sliding window 剩余秒数。0 = 服务端没返回(老协议
  /// /v1/auth/login 暂不返,只 /v1/auth/refresh 返)。客户端用它推导
  /// `refreshTokenExpiresAt` 用于 UI 显示"会话还能续多久"。
  final int refreshExpiresInSeconds;
  /// Server-side refresh_token 行 ID = 当前 access JWT 的 DeviceID。
  /// 客户端可以用它在 Settings → 安全 页高亮"本设备"。空 = 老协议未返。
  final String sessionId;
  final String userId;
  final String email;
  final bool emailVerified;

  const IdentityAuthResult({
    required this.accessToken,
    required this.refreshToken,
    required this.expiresInSeconds,
    required this.refreshExpiresInSeconds,
    required this.sessionId,
    required this.userId,
    required this.email,
    required this.emailVerified,
  });

  factory IdentityAuthResult.fromJson(Map<String, dynamic> j) {
    final user = (j['user'] as Map?)?.cast<String, dynamic>() ?? const {};
    return IdentityAuthResult(
      accessToken: j['access_token'] as String? ?? '',
      refreshToken: j['refresh_token'] as String? ?? '',
      expiresInSeconds: (j['expires_in_seconds'] as num?)?.toInt() ?? 0,
      refreshExpiresInSeconds:
          (j['refresh_expires_in_seconds'] as num?)?.toInt() ?? 0,
      sessionId: j['session_id'] as String? ?? '',
      userId: user['id'] as String? ?? '',
      email: user['email'] as String? ?? '',
      emailVerified: user['email_verified'] as bool? ?? false,
    );
  }

  /// UTC timestamp when the access_token expires. Useful for storing
  /// alongside settings so the UI can warn before re-login.
  DateTime get expiresAt =>
      DateTime.now().toUtc().add(Duration(seconds: expiresInSeconds));

  /// UTC timestamp when refresh_token sliding window expires. Null when
  /// server didn't return a value (login response 暂不带 refresh expiry)。
  DateTime? get refreshExpiresAt => refreshExpiresInSeconds > 0
      ? DateTime.now().toUtc().add(Duration(seconds: refreshExpiresInSeconds))
      : null;
}

/// Result of a `register` call. Identity no longer issues tokens at
/// registration — the user must complete email verification first.
/// Pass [email] back to [verifyEmail] / [resendVerification].
class IdentityRegisterResult {
  final String userId;
  final String email;
  final bool verificationRequired;
  /// True when the SMTP send actually went out. False when the server
  /// is in dev fallback mode (operator must read `docker logs identity`
  /// to retrieve the code).
  final bool emailSent;

  const IdentityRegisterResult({
    required this.userId,
    required this.email,
    required this.verificationRequired,
    required this.emailSent,
  });

  factory IdentityRegisterResult.fromJson(Map<String, dynamic> j) {
    return IdentityRegisterResult(
      userId: j['user_id'] as String? ?? '',
      email: j['email'] as String? ?? '',
      verificationRequired: j['verification_required'] as bool? ?? true,
      emailSent: j['email_sent'] as bool? ?? false,
    );
  }
}

class IdentityClient {
  IdentityClient(this.baseUrl);
  final Uri baseUrl;

  Future<IdentityAuthResult> login(
    String email,
    String password, {
    String? deviceName,
    String? installationId,
  }) async {
    final raw = await _post('/v1/auth/login', {
      'email': email,
      'password': password,
      if (deviceName != null && deviceName.isNotEmpty)
        'device_name': deviceName,
      if (installationId != null && installationId.isNotEmpty)
        'installation_id': installationId,
    });
    return IdentityAuthResult.fromJson(raw);
  }

  /// Server-side logout — revokes the refresh_token immediately. Does NOT
  /// throw on 4xx/5xx; the caller still wants to clear local state even
  /// if the network is down or the token was already invalid.
  Future<void> logout(String refreshToken) async {
    try {
      await _post('/v1/auth/logout', {'refresh_token': refreshToken});
    } on IdentityApiError {
      // best-effort: refresh_token already revoked / network blip — caller
      // should still wipe local credentials.
    }
  }

  // ── Sessions (已登录设备 self-serve) ──

  /// Lists all active sessions for the current bearer. Returns the raw
  /// JSON list from the server; UI maps it to its own widget model.
  Future<List<Map<String, dynamic>>> listSessions(String accessToken) async {
    final raw = await _get('/v1/identity/me/sessions', accessToken);
    final list = (raw['sessions'] as List?) ?? const [];
    return list.cast<Map<String, dynamic>>();
  }

  /// Revokes one session by id. Returns whether the revoked session was
  /// the current (caller's) one — UI uses this to decide whether to drop
  /// the local creds + redirect to login.
  Future<bool> revokeSession(String accessToken, String sessionId) async {
    final raw = await _delete(
      '/v1/identity/me/sessions/${Uri.encodeComponent(sessionId)}',
      accessToken,
    );
    return raw['self'] as bool? ?? false;
  }

  /// Revokes every session except the current one. Returns the count of
  /// revoked sessions.
  Future<int> revokeOtherSessions(String accessToken) async {
    final raw = await _delete('/v1/identity/me/sessions/others', accessToken);
    return (raw['revoked'] as num?)?.toInt() ?? 0;
  }

  // ─── API Tokens (PAT) — P2-I-1 ────────────────────────────────

  /// List the user's PATs (no secrets). Returns the raw JSON shape;
  /// UI maps to its own widget model.
  Future<List<Map<String, dynamic>>> listApiTokens(String accessToken) async {
    final raw = await _get('/v1/identity/me/tokens', accessToken);
    final list = (raw['tokens'] as List?) ?? const [];
    return list.cast<Map<String, dynamic>>();
  }

  /// Create a new PAT. The returned `secret` is the FULL bearer the
  /// user pastes into MCP / CI; we never see it again.
  Future<Map<String, dynamic>> createApiToken(
    String accessToken, {
    required String name,
    List<String>? scopes,
    int? ttlSeconds,
    String? workspaceId,
    String? projectId,
  }) async {
    final body = <String, dynamic>{'name': name};
    if (scopes != null && scopes.isNotEmpty) body['scopes'] = scopes;
    if (ttlSeconds != null && ttlSeconds > 0) body['ttl_seconds'] = ttlSeconds;
    if (workspaceId != null && workspaceId.isNotEmpty) {
      body['workspace_id'] = workspaceId;
    }
    if (projectId != null && projectId.isNotEmpty) {
      body['project_id'] = projectId;
    }
    return _postAuthed('/v1/identity/me/tokens', accessToken, body);
  }

  /// Revoke a PAT by id. Idempotent (re-revoking is a no-op on the
  /// server). Returns true on success.
  Future<bool> revokeApiToken(String accessToken, String id) async {
    final raw = await _delete(
      '/v1/identity/me/tokens/${Uri.encodeComponent(id)}',
      accessToken,
    );
    return raw['revoked'] as bool? ?? false;
  }

  // ─── Activity Feed — P2-I-3 ───────────────────────────────────

  /// List the user's activity events newest-first. `before` is the
  /// cursor returned in `next` from the previous page (RFC3339); pass
  /// null for the first page.
  Future<({List<Map<String, dynamic>> events, String? next})> listActivity(
    String accessToken, {
    String? before,
    int limit = 50,
  }) async {
    final qp = <String, String>{'limit': '$limit'};
    if (before != null && before.isNotEmpty) qp['before'] = before;
    final url = baseUrl
        .replace(path: '/v1/identity/me/activity', queryParameters: qp);
    try {
      final raw = await apiRequest(
        method: 'GET',
        url: url,
        bearerToken: accessToken,
      );
      final list = (raw['events'] as List?) ?? const [];
      return (
        events: list.cast<Map<String, dynamic>>(),
        next: raw['next'] as String?,
      );
    } on ApiError catch (e) {
      throw IdentityApiError(path: e.path, status: e.status, body: e.body);
    }
  }

  /// Creates the account and triggers a verification email. The result
  /// contains no tokens — call [verifyEmail] with the 6-digit code to
  /// obtain access/refresh tokens.
  Future<IdentityRegisterResult> register(
    String email,
    String password, {
    String? displayName,
  }) async {
    final raw = await _post('/v1/auth/register', {
      'email': email,
      'password': password,
      if (displayName != null && displayName.isNotEmpty)
        'display_name': displayName,
    });
    return IdentityRegisterResult.fromJson(raw);
  }

  /// Submits the 6-digit verification code; on success returns the same
  /// shape as [login] (access_token + refresh_token).
  Future<IdentityAuthResult> verifyEmail(
    String email,
    String code, {
    String? deviceName,
    String? installationId,
  }) async {
    final raw = await _post('/v1/auth/verify-email', {
      'email': email,
      'code': code,
      if (deviceName != null && deviceName.isNotEmpty)
        'device_name': deviceName,
      if (installationId != null && installationId.isNotEmpty)
        'installation_id': installationId,
    });
    return IdentityAuthResult.fromJson(raw);
  }

  /// Requests a fresh code for [email]. 200 OK with `email_sent=true`
  /// means SMTP went out; `false` = dev fallback (code in server logs).
  /// 429 surfaces as [IdentityApiError] with `code == 'rate_limited'`.
  Future<bool> resendVerification(String email) async {
    final raw = await _post('/v1/auth/resend-verification', {'email': email});
    return raw['email_sent'] as bool? ?? false;
  }

  /// Requests a password-reset 6-digit code via email. Always 200 OK
  /// regardless of whether the email exists (anti-enumeration). Returns
  /// `email_sent`: true = SMTP went out, false = dev fallback / unknown email.
  Future<bool> forgotPassword(String email) async {
    final raw = await _post('/v1/auth/forgot-password', {'email': email});
    return raw['email_sent'] as bool? ?? false;
  }

  /// Submits the reset code + new password.
  ///
  /// 撤销策略 (B2-a, BiuMind-Identity-Session-Design):
  ///   - [keepSessionId] != null + 服务端校通过 → 只撤其他 session,本设备
  ///                                              保留登录态(改密无感)
  ///   - [keepSessionId] == null / 校验失败 → 撤所有(向后兼容)
  ///
  /// 客户端约定:用户在**登录态**走改密(Settings → 安全)时传当前
  /// settings.sessionId;邮箱忘密 reset 流程不传(用户不在登录态)。
  Future<void> resetPassword({
    required String email,
    required String code,
    required String newPassword,
    String? keepSessionId,
  }) async {
    await _post('/v1/auth/reset-password', {
      'email': email,
      'code': code,
      'new_password': newPassword,
      if (keepSessionId != null && keepSessionId.isNotEmpty)
        'keep_session_id': keepSessionId,
    });
  }

  /// 列出当前用户最近的安全事件 (B2-c reuse banner / 安全活动页用)。
  /// 当前 kind = 'refresh_token_reuse'。limit ≤ 200,默认 50。
  Future<List<Map<String, dynamic>>> listSecurityEvents(
      String accessToken, {int limit = 50}) async {
    final res = await apiRequest(
      method: 'GET',
      url: baseUrl.replace(
        path: '/v1/identity/me/security-events',
        queryParameters: {'limit': '$limit'},
      ),
      bearerToken: accessToken,
    );
    final raw = res['events'];
    if (raw is! List) return const [];
    return raw
        .whereType<Map>()
        .map((m) => m.cast<String, dynamic>())
        .toList(growable: false);
  }

  /// Exchanges a refresh_token for a new access_token. Returns the new
  /// access_token and the same (or rotated) refresh_token.
  Future<IdentityAuthResult> refresh(String refreshToken) async {
    final raw = await _post('/v1/auth/refresh', {
      'refresh_token': refreshToken,
    });
    return IdentityAuthResult.fromJson(raw);
  }

  Future<Map<String, dynamic>> _post(
    String path,
    Map<String, dynamic> body,
  ) async {
    try {
      return await apiRequest(
        method: 'POST',
        url: baseUrl.replace(path: path),
        bearerToken: null,
        body: body,
      );
    } on ApiError catch (e) {
      throw IdentityApiError(path: e.path, status: e.status, body: e.body);
    }
  }

  Future<Map<String, dynamic>> _postAuthed(
    String path,
    String accessToken,
    Map<String, dynamic> body,
  ) async {
    try {
      return await apiRequest(
        method: 'POST',
        url: baseUrl.replace(path: path),
        bearerToken: accessToken,
        body: body,
      );
    } on ApiError catch (e) {
      throw IdentityApiError(path: e.path, status: e.status, body: e.body);
    }
  }

  Future<Map<String, dynamic>> _get(String path, String accessToken) async {
    try {
      return await apiRequest(
        method: 'GET',
        url: baseUrl.replace(path: path),
        bearerToken: accessToken,
      );
    } on ApiError catch (e) {
      throw IdentityApiError(path: e.path, status: e.status, body: e.body);
    }
  }

  Future<Map<String, dynamic>> _delete(String path, String accessToken) async {
    try {
      return await apiRequest(
        method: 'DELETE',
        url: baseUrl.replace(path: path),
        bearerToken: accessToken,
      );
    } on ApiError catch (e) {
      throw IdentityApiError(path: e.path, status: e.status, body: e.body);
    }
  }
}

class IdentityApiError implements Exception {
  final String path;
  final int status;
  final String body;
  const IdentityApiError({
    required this.path,
    required this.status,
    required this.body,
  });

  bool get isUnauthorized => status == 401;
  bool get isConflict => status == 409;     // duplicate email on register
  bool get isForbidden => status == 403;    // e.g. email_not_verified
  bool get isRateLimited => status == 429;  // resend throttle hit

  /// Server-supplied error code (e.g. "invalid_credentials"). Empty
  /// when the response body wasn't shaped like our error envelope.
  /// UIs can map this to a localized string and only fall back to
  /// [friendlyMessage] when the code is unknown.
  String get code {
    try {
      final parsed = jsonDecode(body);
      if (parsed is Map && parsed['error'] is Map) {
        final c = (parsed['error'] as Map)['code'];
        if (c is String) return c;
      }
    } catch (_) {}
    return '';
  }

  /// Tries to extract a useful message from a typical
  /// `{"error":{"code":"...","message":"..."}}` payload. Falls back to
  /// the code (when message is blank — Identity does this for
  /// invalid_credentials) or the HTTP status as last resort.
  String get friendlyMessage {
    try {
      final parsed = jsonDecode(body);
      if (parsed is Map) {
        final err = parsed['error'];
        if (err is Map) {
          final msg = err['message'];
          if (msg is String && msg.isNotEmpty) return msg;
          final c = err['code'];
          if (c is String && c.isNotEmpty) return c;
        }
        if (err is String && err.isNotEmpty) return err;
      }
    } catch (_) {/* fall through */}
    return 'http $status';
  }

  @override
  String toString() =>
      'IdentityApiError $status $path: $friendlyMessage';
}

// SettingsController — async loads + persists AppSettings via SettingsRepo.
//
// UI listens to [settingsControllerProvider]; mutations go through
// the small set of update* methods which call repo.save() and update
// the AsyncNotifier state.
//
// Connectivity diagnostic (`pingHub`) is exposed as a separate method
// so the Settings page can surface results without coupling to
// controller state.

import 'dart:async';
import 'dart:io' as io show Platform;

import 'package:flutter/foundation.dart' show kIsWeb;
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:http/http.dart' as http;
import 'package:shared_preferences/shared_preferences.dart';

import '../../../app/theme/font_size.dart';
import '../../../app/theme/palettes.dart';
import '../../../data/api/device_name.dart';
import '../../../data/api/identity_client.dart';
import '../../../services/auth_logout.dart';
import '../../../services/settings_repo.dart';

class SettingsController extends AsyncNotifier<AppSettings> {
  late final SettingsRepo _repo;

  @override
  Future<AppSettings> build() async {
    _repo = ref.watch(settingsRepoProvider);
    return _repo.load();
  }

  // ── Mutators ────────────────────────────────────────────────
  Future<void> updateIdentityUrl(String url) async {
    final cur = await future;
    await _save(cur.copyWith(identityUrl: url));
  }

  Future<void> updateChatModel(String? model) async {
    final cur = await future;
    await _save(cur.copyWith(defaultChatModel: model));
  }

  /// 设置 → 搜索「在统一搜索中包含笔记」开关 — 立即生效，下次 /v1/search
  /// 请求即带 include_notes=true。
  Future<void> updateSearchIncludeNotes(bool v) async {
    final cur = await future;
    await _save(cur.copyWith(searchIncludeNotes: v));
  }

  Future<void> updateTheme(ThemePreference t) async {
    final cur = await future;
    await _save(cur.copyWith(theme: t));
  }

  /// 用户在 设置 → 外观 切换色板 — 立刻生效,无需 restart。
  /// MaterialApp.router 会随 settings stream 重 build,buildTheme 重新跑。
  Future<void> updatePalette(PaletteId p) async {
    final cur = await future;
    await _save(cur.copyWith(palette: p));
  }

  /// 字体大小档切换 — 全套联动 (字号 + 间距 + 列表宽度 + avatar)。
  Future<void> updateFontSize(FontSize f) async {
    final cur = await future;
    await _save(cur.copyWith(fontSize: f));
  }

  /// Coding workbench paths — 空字符串视为"清空到默认值"。null 字段不更新。
  Future<void> updateCodingPaths({
    String? workingDir,
    String? biuPath,
    String? claudePath,
    String? codexPath,
    bool? useWorktree,
  }) async {
    final cur = await future;
    String? norm(String? v, String? old) {
      if (v == null) return old;
      final t = v.trim();
      return t.isEmpty ? null : t;
    }

    await _save(
      AppSettings(
        identityUrl: cur.identityUrl,
        accessToken: cur.accessToken,
        refreshToken: cur.refreshToken,
        tokenExpiresAt: cur.tokenExpiresAt,
        userEmail: cur.userEmail,
        defaultChatModel: cur.defaultChatModel,
        searchIncludeNotes: cur.searchIncludeNotes,
        theme: cur.theme,
        palette: cur.palette,
        fontSize: cur.fontSize,
        codeWorkingDir: norm(workingDir, cur.codeWorkingDir),
        codeBiuPath: norm(biuPath, cur.codeBiuPath),
        codeClaudePath: norm(claudePath, cur.codeClaudePath),
        codeCodexPath: norm(codexPath, cur.codeCodexPath),
        codeUseWorktree: useWorktree ?? cur.codeUseWorktree,
        codeOriginDeviceId: cur.codeOriginDeviceId,
        codeOriginDeviceLabel: cur.codeOriginDeviceLabel,
      ),
    );
  }

  /// 首次启动时自动初始化 origin device id (uuid v4) + label
  /// (Platform.localHostname). 仅当字段为空时设置, 后续不变。
  Future<void> ensureOriginDevice({
    required String generatedId,
    required String label,
  }) async {
    final cur = await future;
    if (cur.codeOriginDeviceId != null && cur.codeOriginDeviceId!.isNotEmpty) {
      return;
    }
    await _save(
      cur.copyWith(
        codeOriginDeviceId: generatedId,
        codeOriginDeviceLabel: label,
      ),
    );
  }

  /// SharedPreferences 里 installation_id 的兜底 key — settings 存储挂掉/
  /// 被清时下次启动仍能找回同一个 id。
  static const _installationIdPrefsKey = 'biumind.installation_id';

  /// 设备授权 ID (UUID v4). 仅当为空时初始化, 永久持久化, 不随登出清除.
  /// 同 (user, installationId) 在 identity 端复用同一行 refresh_token.
  ///
  /// 解析顺序: settings → SharedPreferences → 新生成。结果永远写回
  /// SharedPreferences (双写兜底) — 存储丢失时若重新生成 id, 服务端会把
  /// 本设备当成新 family, 旧 family 变孤儿。
  Future<void> ensureInstallationId(String generatedId) async {
    final cur = await future;
    final prefs = await SharedPreferences.getInstance();
    final fromPrefs = prefs.getString(_installationIdPrefsKey);
    final id = (cur.installationId != null && cur.installationId!.isNotEmpty)
        ? cur.installationId!
        : (fromPrefs != null && fromPrefs.isNotEmpty)
        ? fromPrefs
        : generatedId;
    if (id != cur.installationId) {
      await _save(cur.copyWith(installationId: id));
    }
    await prefs.setString(_installationIdPrefsKey, id);
  }

  // ── Auth flow ───────────────────────────────────────────────

  /// Logs in against `${identityUrl}/v1/auth/login`, persists the
  /// returned access_token + refresh_token + expiry. model-relay URL is no
  /// longer stored separately — it's derived from identity_url at
  /// read time.
  ///
  /// Throws [IdentityApiError] on 4xx (UI catches and shows the
  /// `.friendlyMessage`).
  Future<IdentityAuthResult> login({
    required String identityUrl,
    required String email,
    required String password,
  }) async {
    final url = _requireUrl(identityUrl);
    final client = IdentityClient(Uri.parse(url));
    final cur0 = await future;
    final result = await client.login(
      email.trim(),
      password,
      deviceName: _deviceName(),
      installationId: cur0.installationId,
    );
    final cur = await future;
    await _save(
      cur.copyWith(
        identityUrl: url,
        accessToken: result.accessToken,
        userEmail: result.email,
        refreshToken: result.refreshToken,
        tokenExpiresAt: result.expiresAt.toIso8601String(),
        accessTtlSeconds: result.expiresInSeconds,
      ),
    );
    return result;
  }

  /// Computes a friendly device_name for the current process to pass at
  /// login. Failures (host lookup) fall through to bare OS label.
  String _deviceName() {
    if (kIsWeb) return currentDeviceName();
    String? host;
    try {
      host = io.Platform.localHostname;
    } catch (_) {
      /* sandboxed iOS sometimes refuses */
    }
    return currentDeviceName(hostname: host);
  }

  /// Creates the account. Server no longer returns tokens — caller must
  /// follow up with [verifyEmail] using the 6-digit code sent to the
  /// email. Persists `identityUrl` + `userEmail` so the verification
  /// page picks them up automatically.
  Future<IdentityRegisterResult> register({
    required String identityUrl,
    required String email,
    required String password,
    String? displayName,
  }) async {
    final url = _requireUrl(identityUrl);
    final client = IdentityClient(Uri.parse(url));
    final result = await client.register(
      email.trim(),
      password,
      displayName: displayName,
    );
    final cur = await future;
    await _save(
      cur.copyWith(
        identityUrl: url,
        userEmail: result.email,
        // Tokens NOT set — login is gated on verification.
      ),
    );
    return result;
  }

  /// Submits the 6-digit code for [email]. On success persists the
  /// returned access+refresh tokens (same shape as [login]).
  Future<IdentityAuthResult> verifyEmail({
    required String identityUrl,
    required String email,
    required String code,
  }) async {
    final url = _requireUrl(identityUrl);
    final client = IdentityClient(Uri.parse(url));
    final cur0 = await future;
    final result = await client.verifyEmail(
      email.trim(),
      code.trim(),
      deviceName: _deviceName(),
      installationId: cur0.installationId,
    );
    final cur = await future;
    await _save(
      cur.copyWith(
        identityUrl: url,
        accessToken: result.accessToken,
        userEmail: result.email,
        refreshToken: result.refreshToken,
        tokenExpiresAt: result.expiresAt.toIso8601String(),
        accessTtlSeconds: result.expiresInSeconds,
      ),
    );
    return result;
  }

  /// Asks the server to send a fresh code. Returns whether SMTP went
  /// out — `false` means dev fallback (code in server logs).
  Future<bool> resendVerification({
    required String identityUrl,
    required String email,
  }) async {
    final url = _requireUrl(identityUrl);
    final client = IdentityClient(Uri.parse(url));
    return client.resendVerification(email.trim());
  }

  /// Asks the server to send a password-reset code. Same anti-enumeration
  /// semantics as resendVerification: always succeeds, `false` return
  /// means dev fallback (or the email was unknown — caller can't tell).
  Future<bool> forgotPassword({
    required String identityUrl,
    required String email,
  }) async {
    final url = _requireUrl(identityUrl);
    final client = IdentityClient(Uri.parse(url));
    return client.forgotPassword(email.trim());
  }

  /// Submits the reset code + new password.
  ///
  /// 行为分两种(B2-a, BiuMind-Identity-Session-Design §5.2):
  ///   - 用户在登录态走改密 (settings.sessionId 非空) → 传 keepSessionId,
  ///     服务端只撤其他 session, 本设备保留 token, **不清本地 creds**
  ///   - 邮箱忘密 reset 流程 (未登录,sessionId 空) → 服务端撤所有 + 本地
  ///     清 token,用户用新密码重新登录
  Future<void> resetPassword({
    required String identityUrl,
    required String email,
    required String code,
    required String newPassword,
  }) async {
    final url = _requireUrl(identityUrl);
    final client = IdentityClient(Uri.parse(url));
    final cur0 = await future;
    final keepID = cur0.sessionId;
    await client.resetPassword(
      email: email.trim(),
      code: code.trim(),
      newPassword: newPassword,
      keepSessionId: keepID,
    );
    if (keepID != null && keepID.isNotEmpty) {
      // 登录态改密:服务端已经撤了其他 session, 本设备 token 保留;
      // 只更新 userEmail (用户可能改了邮箱大小写) + identityUrl。
      final cur = await future;
      await _save(cur.copyWith(identityUrl: url, userEmail: email.trim()));
    } else {
      // 忘密 reset 流程:全撤,清本地 token,LoginPage 会自动 redirect。
      final cur = await future;
      await _save(
        cur.copyWith(identityUrl: url, userEmail: email.trim()).clearTokens(),
      );
    }
  }

  String _requireUrl(String identityUrl) {
    final url = identityUrl.trim();
    if (url.isEmpty) {
      throw const IdentityApiError(
        path: '/v1/auth',
        status: 0,
        body: '{"error":{"message":"identity URL is required"}}',
      );
    }
    return url;
  }

  /// Persists a refreshed token bundle without touching the rest of
  /// the settings. Called by the background TokenRefresher.
  ///
  /// [refreshTokenExpiresAt] / [sessionId] 服务端 rotation 后才返(老协议
  /// /v1/auth/login 不带, /v1/auth/refresh 带)。null = 不动旧值,而不是清掉。
  ///
  /// [accessTtlSeconds] 是服务端声明的 access TTL,refresher 用它推导比例化
  /// margin/tick(A4)。
  Future<void> applyRefreshed({
    required String accessToken,
    required String refreshToken,
    required String tokenExpiresAt,
    int? accessTtlSeconds,
    String? refreshTokenExpiresAt,
    String? sessionId,
  }) async {
    // 合并基线用磁盘最新而不是内存 state — 多实例下别的实例可能刚写入
    // 更新的字段(如 installationId / userEmail), 以内存为基线会把它们
    // 覆盖回旧值。
    final cur = await _repo.load();
    await _save(
      cur.copyWith(
        accessToken: accessToken,
        refreshToken: refreshToken,
        tokenExpiresAt: tokenExpiresAt,
        accessTtlSeconds: accessTtlSeconds,
        refreshTokenExpiresAt: refreshTokenExpiresAt,
        sessionId: sessionId,
      ),
    );
  }

  /// Wipes auth-related fields. Identity URL + UI prefs preserved (the
  /// operator may want to keep the same server but log a different
  /// user in next time).
  ///
  /// Also calls /v1/auth/logout to revoke the refresh_token server-side
  /// — a previous version only cleared local state, leaving the token
  /// usable for the full 30-day TTL if it had been intercepted.
  /// Network failures don't block the local sign-out (best-effort).
  ///
  /// [compareAndClear] 用于 TokenManager 自动踢人路径(refresh 401):先对盘 —
  /// 若磁盘上的 refresh_token 非空且与内存不同,说明同机另一实例已写入新
  /// 凭证(共享 settings 存储),本会话只是拿旧 rt 被拒。此时收编磁盘值,
  /// **不清盘、不调 logout**,返回 false。UI 主动登出走默认 false,维持
  /// 原行为。返回值 = 是否真的清了盘。
  Future<bool> signOut({bool compareAndClear = false}) async {
    final cur = await future;
    if (compareAndClear) {
      final disk = await _repo.load();
      final diskRt = disk.refreshToken;
      if (diskRt != null && diskRt.isNotEmpty && diskRt != cur.refreshToken) {
        state = AsyncData(disk);
        return false;
      }
    }
    final url = (cur.identityUrl ?? '').trim();
    final rt = (cur.refreshToken ?? '').trim();
    if (url.isNotEmpty && rt.isNotEmpty) {
      try {
        // 加 5s 兜底超时, 避免离线 / 假 URL 时把用户卡死;
        // logout 是 best-effort, 失败也要清本地 creds.
        await IdentityClient(
          Uri.parse(url),
        ).logout(rt).timeout(const Duration(seconds: 5));
      } catch (_) {
        /* best-effort */
      }
    }
    // R2: 清本地用户数据 (DAO + 连接 + provider 缓存) 再 clearTokens —
    // 4 条 signOut 路径 (3 UI + token_manager 自动踢人) 统一经此, 修跨用户
    // 数据泄露. code DAO 不清 (零云同步 SoT), rss 按 scopeId 隔离不必清.
    await purgeUserData(ref);
    await _save(cur.clearTokens());
    return true;
  }

  Future<void> _save(AppSettings s) async {
    state = AsyncData(s);
    await _repo.save(s);
  }

  // ── Connectivity diagnostic ─────────────────────────────────

  /// pingHub probes the configured model-relay /healthz endpoint; returns
  /// elapsed duration on success or throws on failure.
  ///
  /// [overrideUrl] takes either a model-relay URL directly or an Identity URL
  /// (we'll derive the model-relay from it). Lets the Settings UI test what
  /// the user typed without first persisting.
  Future<Duration> pingHub({
    Duration timeout = const Duration(seconds: 3),
    String? overrideUrl,
  }) async {
    Uri? relayUri;
    final raw = overrideUrl?.trim();
    if (raw != null && raw.isNotEmpty) {
      try {
        // 单 origin: 用户填的就是统一入口(site) URL, 直接用, 不换端口。
        relayUri = Uri.parse(raw);
      } catch (_) {
        throw const HubPingError('invalid URL');
      }
    } else {
      final cur = await future;
      relayUri = cur.hubUri;
    }
    if (relayUri == null) {
      throw const HubPingError('endpoint not configured');
    }
    final probe = relayUri.replace(path: '/healthz');
    final stop = Stopwatch()..start();
    try {
      final resp = await http.get(probe).timeout(timeout);
      stop.stop();
      if (resp.statusCode != 200) {
        throw HubPingError('healthz status ${resp.statusCode}');
      }
      return stop.elapsed;
    } on TimeoutException {
      throw const HubPingError('timeout');
    } on http.ClientException catch (e) {
      throw HubPingError('connection failed: ${e.message}');
    }
  }
}

class HubPingError implements Exception {
  final String message;
  const HubPingError(this.message);
  @override
  String toString() => 'HubPingError: $message';
}

final settingsControllerProvider =
    AsyncNotifierProvider<SettingsController, AppSettings>(
      SettingsController.new,
    );

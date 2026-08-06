// SettingsRepo — single source of truth for user-configurable settings.
//
// Two implementations:
//   SecureSettingsRepo   — production; flutter_secure_storage (Keychain/Keystore)
//   InMemorySettingsRepo — tests
//
// We persist *everything* via secure storage including the non-secret URL
// fields. The minor overhead vs SharedPreferences is acceptable, and the
// tokens absolutely must be encrypted at rest.
//
// Per-provider LLM credentials (Anthropic API key, etc.) are NOT in
// AppSettings any more — they live server-side under chat.providers
// (encrypted with KEYVAULTS_SECRET) and are loaded by ProvidersClient.
// This file deals only with identity (whom the user signed in as) and
// UI preferences.

import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:flutter/foundation.dart' show kIsWeb;
import 'package:flutter/services.dart' show PlatformException;
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:meta/meta.dart';
import 'package:path_provider/path_provider.dart';

import '../app/theme/font_size.dart';
import '../app/theme/palettes.dart';

/// LLM dispatch mode. There are exactly two cases now:
///
///   * cloud  — Brain proxies the LLM call. Server holds the decrypted
///              key; client just streams over /v1/threads/{id}/send.
///   * direct — Client opens the LLM stream itself with the user's key
///              (BYOK). After streaming ends, the client PATCHes the
///              final message back so other devices can sync.
///
/// The previous `byoEndpoint` value is migrated to `cloud` (it really
/// just meant "self-hosted model-relay URL", which is now a property of the
/// identity URL, not a separate mode).
@immutable
class AppSettings {
  // ── Identity / connectivity ─────────────────────────────────
  /// Single source of truth for "where my data lives". model-relay :7001 and
  /// Brain :7003 are derived by replacing the port. Settings UI shows
  /// only this.
  final String? identityUrl;

  /// JWT issued by Identity at /v1/auth/login. Same value used as the
  /// Bearer token for model-relay + Brain requests.
  final String? accessToken;

  /// Long-lived refresh token (`rt-live-...`). UI uses it to renew
  /// expired access tokens before the user notices.
  final String? refreshToken;

  /// ISO-8601 UTC expiry of [accessToken].
  final String? tokenExpiresAt;

  /// 服务端最近一次签发 access_token 时声明的 TTL(秒)。token_refresher 用
  /// 它推导比例化 margin (= ttl × 0.1) 与 watchdog tick (= ttl × 0.05),
  /// 让客户端按服务端实际配置自适应而不是写死。
  /// 0 / null = 服务端没返,refresher 用 fallback 静态值。
  final int? accessTtlSeconds;

  /// ISO-8601 UTC expiry of [refreshToken] (sliding window). 由
  /// /v1/auth/refresh 响应的 `refresh_expires_in_seconds` 推导。
  /// Settings → 安全 页用它显示"会话还能续多久"。
  /// null = 服务端未返(老协议)或还没首次刷过。
  final String? refreshTokenExpiresAt;

  /// 服务端 refresh_tokens.id = 当前 access JWT 的 DeviceID。
  /// Settings → 安全 页用它高亮"本设备"; 撤销其他设备时排除自己。
  /// null = 服务端未返(老协议)。
  final String? sessionId;

  /// Last-logged-in email. Pre-fills the form. Not a credential.
  final String? userEmail;

  // ── Chat preferences ────────────────────────────────────────
  /// Default model id for new chat threads. Provider is derived via
  /// the static catalog (lib/core/llm/provider_catalog.dart).
  final String? defaultChatModel;

  // ── Search preferences ─────────────────────────────────────
  /// 统一搜索（POST /v1/search）是否带 include_notes=true（响应附带笔记
  /// 分组）。默认关。与笔记内的中栏搜索（/v1/notes/search）无关。
  final bool searchIncludeNotes;

  // ── UI ───────────────────────────────────────────────────────
  final ThemePreference theme;

  /// 用户在 设置 → 外观 选择的色板。默认 紫+橘 (purpleOrange)。
  /// 详见 docs/BiuMind-Theme-System-Design.md §4。
  final PaletteId palette;

  /// 用户在 设置 → 外观 选择的字体大小档。默认 small (紧凑)。
  /// 三档同时联动间距 / 列表宽度 / avatar 尺寸 — 见 §3.4。
  final FontSize fontSize;

  // ── Coding workbench (lib/features/code/) ─────────────────────
  /// 任务执行的工作目录。null/空 = 用 user HOME。
  final String? codeWorkingDir;

  /// biu binary 的路径或命令名。null/空 = 'biu' (走 PATH 查找)。
  final String? codeBiuPath;

  /// claude (Claude Code) binary 路径。null/空 = 'claude'。
  final String? codeClaudePath;

  /// codex (OpenAI Codex CLI) binary 路径。null/空 = 'codex'。
  final String? codeCodexPath;

  /// 编码任务隔离: true = 每个任务独立 git worktree + 分支; false = 全部
  /// 共享 codeWorkingDir (并发任务可能互相覆盖)。默认 true。
  final bool codeUseWorktree;

  /// 本机 device id (uuid v4), 用于跨端区分 task origin。app 首次启动生成,
  /// 持久化到 settings。给 task.origin_device_id / task_command.issued_by_device_id 用。
  final String? codeOriginDeviceId;

  /// 用户可见的 device 标签 (e.g. "MacBook Pro")。Platform.localHostname。
  final String? codeOriginDeviceLabel;

  /// 设备授权 ID (UUID v4). 首次启动生成, 永久持久化, 跨登入登出不变.
  /// 同 (user, installationId) 在 identity 端只有一行 active session —
  /// 反复登录复用同一行, 撤销 = 永久 (除非用户重新登录, 那就建新行).
  final String? installationId;

  const AppSettings({
    this.identityUrl,
    this.accessToken,
    this.refreshToken,
    this.tokenExpiresAt,
    this.accessTtlSeconds,
    this.refreshTokenExpiresAt,
    this.sessionId,
    this.userEmail,
    this.defaultChatModel,
    this.searchIncludeNotes = false,
    this.theme = ThemePreference.system,
    // 默认色板:跟 prototype v3 默认一致(墨蓝 + 信号橙)— Vercel 风极简
    // 商务感,优于早期紫橘"少女粉"基调。已登录用户保留原选,只影响新用户。
    this.palette = PaletteId.inkblueOrange,
    this.fontSize = FontSize.small,
    this.codeWorkingDir,
    this.codeBiuPath,
    this.codeClaudePath,
    this.codeCodexPath,
    this.codeUseWorktree = true,
    this.codeOriginDeviceId,
    this.codeOriginDeviceLabel,
    this.installationId,
  });

  AppSettings copyWith({
    String? identityUrl,
    String? accessToken,
    String? refreshToken,
    String? tokenExpiresAt,
    int? accessTtlSeconds,
    String? refreshTokenExpiresAt,
    String? sessionId,
    String? userEmail,
    String? defaultChatModel,
    bool? searchIncludeNotes,
    ThemePreference? theme,
    PaletteId? palette,
    FontSize? fontSize,
    String? codeWorkingDir,
    String? codeBiuPath,
    String? codeClaudePath,
    String? codeCodexPath,
    bool? codeUseWorktree,
    String? codeOriginDeviceId,
    String? codeOriginDeviceLabel,
    String? installationId,
  }) => AppSettings(
    identityUrl: identityUrl ?? this.identityUrl,
    accessToken: accessToken ?? this.accessToken,
    refreshToken: refreshToken ?? this.refreshToken,
    tokenExpiresAt: tokenExpiresAt ?? this.tokenExpiresAt,
    accessTtlSeconds: accessTtlSeconds ?? this.accessTtlSeconds,
    refreshTokenExpiresAt: refreshTokenExpiresAt ?? this.refreshTokenExpiresAt,
    sessionId: sessionId ?? this.sessionId,
    userEmail: userEmail ?? this.userEmail,
    defaultChatModel: defaultChatModel ?? this.defaultChatModel,
    searchIncludeNotes: searchIncludeNotes ?? this.searchIncludeNotes,
    theme: theme ?? this.theme,
    palette: palette ?? this.palette,
    fontSize: fontSize ?? this.fontSize,
    codeWorkingDir: codeWorkingDir ?? this.codeWorkingDir,
    codeBiuPath: codeBiuPath ?? this.codeBiuPath,
    codeClaudePath: codeClaudePath ?? this.codeClaudePath,
    codeCodexPath: codeCodexPath ?? this.codeCodexPath,
    codeUseWorktree: codeUseWorktree ?? this.codeUseWorktree,
    codeOriginDeviceId: codeOriginDeviceId ?? this.codeOriginDeviceId,
    codeOriginDeviceLabel: codeOriginDeviceLabel ?? this.codeOriginDeviceLabel,
    installationId: installationId ?? this.installationId,
  );

  AppSettings clearTokens() => AppSettings(
    identityUrl: identityUrl,
    userEmail: userEmail,
    defaultChatModel: defaultChatModel,
    searchIncludeNotes: searchIncludeNotes,
    theme: theme,
    palette: palette,
    fontSize: fontSize,
    codeWorkingDir: codeWorkingDir,
    codeBiuPath: codeBiuPath,
    codeClaudePath: codeClaudePath,
    codeCodexPath: codeCodexPath,
    codeUseWorktree: codeUseWorktree,
    codeOriginDeviceId: codeOriginDeviceId,
    codeOriginDeviceLabel: codeOriginDeviceLabel,
    // installationId 跨登出保留 — 同设备再登录可复用同一 device row.
    installationId: installationId,
  );

  Map<String, dynamic> toJson() => {
    if (identityUrl != null) 'identity_url': identityUrl,
    if (accessToken != null) 'access_token': accessToken,
    if (refreshToken != null) 'refresh_token': refreshToken,
    if (tokenExpiresAt != null) 'token_expires_at': tokenExpiresAt,
    if (accessTtlSeconds != null) 'access_ttl_seconds': accessTtlSeconds,
    if (refreshTokenExpiresAt != null)
      'refresh_token_expires_at': refreshTokenExpiresAt,
    if (sessionId != null) 'session_id': sessionId,
    if (userEmail != null) 'user_email': userEmail,
    if (defaultChatModel != null) 'default_chat_model': defaultChatModel,
    'search_include_notes': searchIncludeNotes,
    'theme': theme.name,
    'palette': palette.wireId,
    'font_size': fontSize.wireId,
    if (codeWorkingDir != null) 'code_working_dir': codeWorkingDir,
    if (codeBiuPath != null) 'code_biu_path': codeBiuPath,
    if (codeClaudePath != null) 'code_claude_path': codeClaudePath,
    if (codeCodexPath != null) 'code_codex_path': codeCodexPath,
    'code_use_worktree': codeUseWorktree,
    if (codeOriginDeviceId != null) 'code_origin_device_id': codeOriginDeviceId,
    if (codeOriginDeviceLabel != null)
      'code_origin_device_label': codeOriginDeviceLabel,
    if (installationId != null) 'installation_id': installationId,
  };

  /// Migrates legacy field names. Old shape used hub_url + hub_token +
  /// provider_keys; we keep the migration logic here so a user upgrading
  /// from an older build doesn't lose their session.
  factory AppSettings.fromJson(Map<String, dynamic> j) {
    // Identity URL: prefer the new field, then derive from old hub_url.
    var identity = j['identity_url'] as String?;
    if ((identity == null || identity.isEmpty) && j['hub_url'] != null) {
      identity = _deriveIdentityFromHub(j['hub_url'] as String);
    }
    return AppSettings(
      identityUrl: identity,
      accessToken:
          (j['access_token'] as String?) ?? (j['hub_token'] as String?),
      refreshToken: j['refresh_token'] as String?,
      tokenExpiresAt: j['token_expires_at'] as String?,
      accessTtlSeconds: (j['access_ttl_seconds'] as num?)?.toInt(),
      refreshTokenExpiresAt: j['refresh_token_expires_at'] as String?,
      sessionId: j['session_id'] as String?,
      userEmail: j['user_email'] as String?,
      defaultChatModel: j['default_chat_model'] as String?,
      searchIncludeNotes: (j['search_include_notes'] as bool?) ?? false,
      theme: _themeFromName(j['theme'] as String?),
      palette: PaletteId.byWireId(j['palette'] as String?),
      fontSize: FontSize.byWireId(j['font_size'] as String?),
      codeWorkingDir: j['code_working_dir'] as String?,
      codeBiuPath: j['code_biu_path'] as String?,
      codeClaudePath: j['code_claude_path'] as String?,
      codeCodexPath: j['code_codex_path'] as String?,
      codeUseWorktree: (j['code_use_worktree'] as bool?) ?? true,
      codeOriginDeviceId: j['code_origin_device_id'] as String?,
      codeOriginDeviceLabel: j['code_origin_device_label'] as String?,
      installationId: j['installation_id'] as String?,
    );
    // Old `provider_keys` map is intentionally dropped: keys belong on
    // the server now. Users with a legacy key re-enter it once via
    // Settings → 模型供应商. The auto-migration could POST it for them
    // but we don't want to silently upload secrets the user might
    // assume stayed local.
  }

  static String? _deriveIdentityFromHub(String hubUrl) {
    try {
      final u = Uri.parse(hubUrl);
      return u.replace(port: 7004).toString();
    } catch (_) {
      return null;
    }
  }

  static ThemePreference _themeFromName(String? n) => switch (n) {
    'light' => ThemePreference.light,
    'dark' => ThemePreference.dark,
    _ => ThemePreference.system,
  };

  // ── Derived URLs ──────────────────────────────────────────────
  // 单 origin 寻址:client(桌面 + web)一律指向同一个入口(官网 site nginx),
  // 由 nginx 按 /v1/* 路径反代到各后端。client 侧不再换端口 —— 以前 native
  // 换端口(model-relay:7001 / brain:7003 / aigc:7012)只在「每服务各自暴露
  // 端口」的本地 dev 成立, 对生产反代域名(只开 :443)会 timeout。
  // 本地 origin = http://localhost:8088(site), 生产 = https://your-biumind.example.com。
  // hubUri/brainUri/aigcUri 保留作语义别名, 全部等于 identityUri(同 origin)。

  /// model-relay endpoint —— 同 origin(经 site nginx /v1/messages 等反代)。
  Uri? get hubUri => identityUri;

  /// Brain endpoint —— 同 origin(经 site nginx /v1/threads /v1/providers 等反代)。
  Uri? get brainUri => identityUri;

  /// AIGC endpoint —— 同 origin(经 site nginx /v1/generations /v1/models 等反代)。
  Uri? get aigcUri => identityUri;

  /// Identity URL — 用户态 endpoint(含 /v1/auth/* /v1/identity/me/* 等)。
  /// 单一事实源, 其余服务 endpoint 都等于它(同 origin)。
  Uri? get identityUri {
    final id = identityUrl;
    if (id == null || id.isEmpty) return null;
    try {
      return Uri.parse(id);
    } catch (_) {
      return null;
    }
  }

  bool get signedIn =>
      accessToken != null &&
      accessToken!.isNotEmpty &&
      identityUrl != null &&
      identityUrl!.isNotEmpty;
}

enum ThemePreference { system, light, dark }

abstract interface class SettingsRepo {
  Future<AppSettings> load();
  Future<void> save(AppSettings s);
  Stream<AppSettings> watch();
}

/// Secure-storage backed implementation. Stores the whole AppSettings as one
/// JSON blob under a single key.
///
/// Falls back to a plain JSON file (getApplicationSupportDirectory()/
/// settings.json) when the platform's keychain rejects the operation. Common
/// case: unsigned macOS dev builds where flutter_secure_storage hits
/// errSecMissingEntitlement (-34018), and Android ROMs where the legacy
/// RSA/ECB Keystore breaks after backup/restore. Production builds (signed
/// with `keychain-access-groups` entitlement) always go through the keychain
/// branch.
///
/// P0 加固要点:
///   * 不再终身锁死 — 每次 load/save 都先试 secure storage,PlatformException
///     只让当次操作走 fallback,下次自动重试 secure(备份恢复等瞬时故障自愈)。
///   * fallback 写入是原子的(先 .tmp 再 rename),且 save 后做写后读校验,
///     校验失败会向另一个存储双写兜底并触发 [onPersistWarning] 钩子。
///   * load 容忍 macOS 无签名构建的读写不对称 — 写 keychain 抛
///     errSecMissingEntitlement,读不存在的 key 却只返回 null;secure 读空
///     时继续走 fallback 文件, 否则文件里的会话会被"藏"住再被启动写入覆盖。
class SecureSettingsRepo implements SettingsRepo {
  SecureSettingsRepo({
    FlutterSecureStorage? storage,
    String? fallbackPath,
    String? legacyFallbackPath,
  }) : _storage = storage ?? _defaultStorage(),
       _fallbackPath = fallbackPath,
       _legacyFallbackPath = legacyFallbackPath;
  final FlutterSecureStorage _storage;
  final String? _fallbackPath;
  final String? _legacyFallbackPath;
  static const _key = 'biumind.app_settings';

  /// 持久化异常(写后读校验失败)钩子 — 留给后续遥测接入;默认 null 时只 print。
  static void Function(String message)? onPersistWarning;

  /// Android 用 resetOnError 自愈(encryptedSharedPreferences 在 v10 已废弃,
  /// 首访自动迁移到 custom ciphers);iOS/macOS 用
  /// first_unlock_this_device(不随备份迁移, 重启后首次解锁即可读)。
  static FlutterSecureStorage _defaultStorage() => const FlutterSecureStorage(
    aOptions: AndroidOptions(
      resetOnError: true,
    ),
    iOptions: IOSOptions(
      accessibility: KeychainAccessibility.first_unlock_this_device,
    ),
    mOptions: MacOsOptions(
      accessibility: KeychainAccessibility.first_unlock_this_device,
    ),
  );

  final StreamController<AppSettings> _ctrl = StreamController.broadcast();

  Future<String> _resolveFallbackPath() async {
    final p = _fallbackPath;
    if (p != null) return p;
    // Web has no real filesystem — just an in-memory marker. The web
    // path through this branch shouldn't fire because flutter_secure_storage
    // works on web (LocalStorage-backed), but defend anyway.
    if (kIsWeb) return '/biumind-settings.json';
    // ApplicationSupport 目录 — Android 上 HOME 未设, 旧的 ~/.biumind 路径
    // 会解析到根目录写不进; path_provider 给的是各平台可写目录。
    final dir = await getApplicationSupportDirectory();
    return '${dir.path}/settings.json';
  }

  String _resolveLegacyFallbackPath() {
    final p = _legacyFallbackPath;
    if (p != null) return p;
    if (kIsWeb) return '/biumind-settings.json';
    final home = Platform.environment['HOME'] ?? '';
    return '$home/.biumind/settings.json';
  }

  @override
  Future<AppSettings> load() async {
    // 先试 secure storage; PlatformException 时该次走 fallback 文件,
    // 不置任何锁死标志 — 下次操作自动重试 secure(自愈)。
    try {
      final s = await _loadFromSecure();
      if (s != null) return s;
      // secure 读出 null(key 不存在)时不能就此返回空 — macOS 无签名构建的
      // 不对称行为: 写 keychain 抛 errSecMissingEntitlement(上次会话落在
      // fallback 文件), 但读不存在的 key 只返回 null 而不抛。若返回空
      // settings, 文件里的会话被"藏"住(启动即登录页), 随后启动写入
      // (ensureOriginDevice/ensureInstallationId)还会把文件覆盖销毁 —
      // 表现为"每次重开都要重新登录"。
    } on PlatformException catch (e) {
      // ignore: avoid_print
      print(
        'SecureSettingsRepo: keychain unavailable (${e.code}); '
        'falling back to plaintext settings file for this operation. '
        'INSECURE — only acceptable in dev builds.',
      );
    } catch (e) {
      // 非 PlatformException(插件层未预期的错误类型)同样走文件 —
      // 文件是最后的兜底, 任何 secure 读失败都不该让会话"消失"。
      // ignore: avoid_print
      print(
        'SecureSettingsRepo: unexpected keychain read error '
        '(${e.runtimeType}: $e); falling back to settings file.',
      );
    }
    return _loadFromFile();
  }

  /// keychain 里有实际写入过的 blob 才返回非 null; key 不存在(读出 null)
  /// 时返回 null, 由 [load] 继续走 fallback 文件。
  Future<AppSettings?> _loadFromSecure() async {
    final raw = await _storage.read(key: _key);
    if (raw == null || raw.isEmpty) return null;
    return AppSettings.fromJson(jsonDecode(raw) as Map<String, dynamic>);
  }

  Future<AppSettings> _loadFromFile() async {
    try {
      final f = File(await _resolveFallbackPath());
      if (await f.exists()) {
        final s = await _parseFile(f);
        // 0.1.3→0.1.4 迁移补救: 新文件从没见过登录(identity_url 为空 —
        // 典型是 keychain 读写不对称期间被启动写入覆盖出的空壳), 而旧
        // 路径还留着完整会话 → 捞回来。主动登出后的新文件会保留
        // identity_url, 不命中此分支 — 已登出的会话不会被复活。
        if (s.identityUrl == null) {
          final legacy = File(_resolveLegacyFallbackPath());
          if (legacy.path != f.path && await legacy.exists()) {
            final l = await _parseFile(legacy);
            if (l.refreshToken != null) return l;
          }
        }
        return s;
      }
      // 旧路径迁移: 新文件还没写过时, 若老的 $HOME/.biumind/settings.json
      // 存在则读它一次(下次 save 自然写到新位置)。
      final legacy = File(_resolveLegacyFallbackPath());
      if (legacy.path != f.path && await legacy.exists()) {
        return _parseFile(legacy);
      }
      return const AppSettings();
    } catch (e) {
      // ignore: avoid_print
      print('SecureSettingsRepo: _loadFromFile failed ($e); returning empty.');
      return const AppSettings();
    }
  }

  Future<AppSettings> _parseFile(File f) async {
    try {
      final raw = await f.readAsString();
      if (raw.isEmpty) return const AppSettings();
      return AppSettings.fromJson(jsonDecode(raw) as Map<String, dynamic>);
    } catch (_) {
      return const AppSettings();
    }
  }

  @override
  Future<void> save(AppSettings s) async {
    final payload = jsonEncode(s.toJson());
    var usedSecure = true;
    try {
      await _storage.write(key: _key, value: payload);
    } on PlatformException catch (e) {
      // ignore: avoid_print
      print(
        'SecureSettingsRepo: keychain write failed (${e.code}); '
        'writing plaintext settings file instead.',
      );
      usedSecure = false;
      await _writeToFile(payload);
    }
    _ctrl.add(s);
    await _verifyPersist(s, payload, usedSecure: usedSecure);
  }

  /// 原子写: 先写同目录 .tmp 再 rename 覆盖, 避免半截 JSON 把整份 settings
  /// (含 token) 搞丢。
  Future<void> _writeToFile(String payload) async {
    final path = await _resolveFallbackPath();
    final f = File(path);
    await f.parent.create(recursive: true);
    final tmp = File('$path.tmp');
    await tmp.writeAsString(payload);
    await tmp.rename(path);
  }

  /// 写后读校验: 从刚写入的存储读回, refreshToken / identityUrl 必须与写入
  /// 一致; 不一致或读回抛异常 → 向另一个存储补写一份(双写兜底)并触发
  /// [onPersistWarning](留给遥测)。读回失败不抛 — save 本身已经成功。
  Future<void> _verifyPersist(
    AppSettings s,
    String payload, {
    required bool usedSecure,
  }) async {
    AppSettings? readBack;
    try {
      readBack = usedSecure ? await _loadFromSecure() : await _loadFromFile();
    } catch (_) {
      readBack = null;
    }
    if (readBack != null &&
        readBack.refreshToken == s.refreshToken &&
        readBack.identityUrl == s.identityUrl) {
      return;
    }
    const msg =
        'SecureSettingsRepo: persist verification failed '
        '(read-back mismatch) — dual-writing to the other store.';
    // ignore: avoid_print
    print(msg);
    onPersistWarning?.call(msg);
    try {
      if (usedSecure) {
        await _writeToFile(payload);
      } else {
        await _storage.write(key: _key, value: payload);
      }
    } catch (_) {
      /* 双写是兜底, 失败不再放大 */
    }
  }

  @override
  Stream<AppSettings> watch() => _ctrl.stream;
}

/// In-memory implementation for tests / dev runs without a keychain backend.
class InMemorySettingsRepo implements SettingsRepo {
  InMemorySettingsRepo([AppSettings? initial])
    : _current = initial ?? const AppSettings();
  AppSettings _current;
  final StreamController<AppSettings> _ctrl = StreamController.broadcast();

  @override
  Future<AppSettings> load() async => _current;

  @override
  Future<void> save(AppSettings s) async {
    _current = s;
    _ctrl.add(s);
  }

  @override
  Stream<AppSettings> watch() => _ctrl.stream;
}

final settingsRepoProvider = Provider<SettingsRepo>((ref) {
  return SecureSettingsRepo();
});

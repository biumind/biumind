// AccountRegistry — P2 多账号 Step 1: 凭证层账号注册表。
//
// 设计意图:
// - registry 存所有登录过账号的「凭证切片」(identityUrl + access/refresh
//   token + 过期时间 + sessionId + email)。
// - **active 账号的凭证仍以 AppSettings blob (settings_repo.dart) 为单一
//   事实源** —— registry 里 active 槽位只是它的镜像, 其余槽位是非活跃
//   账号的休眠凭证。事实源不分裂: 全 App 的凭证消费仍走
//   settingsControllerProvider 响应式派生, 切账号 = 对 settings 做一次
//   原子换 slice (见 SettingsController.switchAccount)。
// - accountId 与本地 Drift 库 chat/notes 五表的 ownerKey 完全同构
//   (sha256(normalize(identityUrl)) + ':' + JWT sub, 见
//   features/chat/data/chat_scope.dart), 切账号后 scope 过滤天然命中
//   对应账号的本地数据。
//
// 存储哲学对齐 SecureSettingsRepo (secure storage 单 key JSON blob,
// PlatformException 时当次操作 fallback 到 ApplicationSupport 文件,
// 原子写 tmp+rename; web 走 flutter_secure_storage 的 LocalStorage 后端),
// 但刻意精简: 不需要 legacy 路径迁移, 不需要写后读双写校验 —— registry
// 损坏的最坏结果是丢休眠凭证 (用户重新登录即可), active 会话存在
// AppSettings 里不受影响。

import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:flutter/foundation.dart' show kIsWeb;
import 'package:flutter/services.dart' show PlatformException;
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:meta/meta.dart';
import 'package:path_provider/path_provider.dart';

/// 一个登录过账号的凭证切片。immutable, JSON 字段名 snake_case
/// (风格对齐 AppSettings)。
@immutable
class AccountRecord {
  /// 账号主键 = 本地 Drift ownerKey:
  /// sha256(normalize(identityUrl)) + ':' + userId。
  final String accountId;
  final String identityUrl;

  /// JWT sub (accountId 冒号后那段, 冗余存一份省得每次解 token)。
  final String userId;
  final String? email;
  final String? accessToken;
  final String? refreshToken;

  /// ISO-8601 UTC expiry of [accessToken]。
  final String? tokenExpiresAt;

  /// 服务端声明的 access TTL(秒), 语义同 AppSettings.accessTtlSeconds。
  final int? accessTtlSeconds;

  /// ISO-8601 UTC expiry of [refreshToken] (sliding window)。
  final String? refreshTokenExpiresAt;

  /// 服务端 refresh_tokens.id, 语义同 AppSettings.sessionId。
  final String? sessionId;

  /// 最近一次成为 active 的时间 (ISO-8601 UTC) — 登录 / 切账号时刷新,
  /// 给账号列表排序用。
  final String lastActiveAt;

  const AccountRecord({
    required this.accountId,
    required this.identityUrl,
    required this.userId,
    this.email,
    this.accessToken,
    this.refreshToken,
    this.tokenExpiresAt,
    this.accessTtlSeconds,
    this.refreshTokenExpiresAt,
    this.sessionId,
    required this.lastActiveAt,
  });

  AccountRecord copyWith({
    String? accountId,
    String? identityUrl,
    String? userId,
    String? email,
    String? accessToken,
    String? refreshToken,
    String? tokenExpiresAt,
    int? accessTtlSeconds,
    String? refreshTokenExpiresAt,
    String? sessionId,
    String? lastActiveAt,
  }) => AccountRecord(
    accountId: accountId ?? this.accountId,
    identityUrl: identityUrl ?? this.identityUrl,
    userId: userId ?? this.userId,
    email: email ?? this.email,
    accessToken: accessToken ?? this.accessToken,
    refreshToken: refreshToken ?? this.refreshToken,
    tokenExpiresAt: tokenExpiresAt ?? this.tokenExpiresAt,
    accessTtlSeconds: accessTtlSeconds ?? this.accessTtlSeconds,
    refreshTokenExpiresAt: refreshTokenExpiresAt ?? this.refreshTokenExpiresAt,
    sessionId: sessionId ?? this.sessionId,
    lastActiveAt: lastActiveAt ?? this.lastActiveAt,
  );

  Map<String, dynamic> toJson() => {
    'account_id': accountId,
    'identity_url': identityUrl,
    'user_id': userId,
    if (email != null) 'email': email,
    if (accessToken != null) 'access_token': accessToken,
    if (refreshToken != null) 'refresh_token': refreshToken,
    if (tokenExpiresAt != null) 'token_expires_at': tokenExpiresAt,
    if (accessTtlSeconds != null) 'access_ttl_seconds': accessTtlSeconds,
    if (refreshTokenExpiresAt != null)
      'refresh_token_expires_at': refreshTokenExpiresAt,
    if (sessionId != null) 'session_id': sessionId,
    'last_active_at': lastActiveAt,
  };

  factory AccountRecord.fromJson(Map<String, dynamic> j) => AccountRecord(
    accountId: j['account_id'] as String,
    identityUrl: j['identity_url'] as String,
    userId: j['user_id'] as String,
    email: j['email'] as String?,
    accessToken: j['access_token'] as String?,
    refreshToken: j['refresh_token'] as String?,
    tokenExpiresAt: j['token_expires_at'] as String?,
    accessTtlSeconds: (j['access_ttl_seconds'] as num?)?.toInt(),
    refreshTokenExpiresAt: j['refresh_token_expires_at'] as String?,
    sessionId: j['session_id'] as String?,
    lastActiveAt: j['last_active_at'] as String? ?? '',
  );
}

abstract interface class AccountRegistryStore {
  Future<List<AccountRecord>> load();
  Future<void> save(List<AccountRecord> accounts);
  Stream<List<AccountRecord>> watch();
}

/// Secure-storage backed implementation. 整个 registry 序列化为一个 JSON
/// blob (`{"version":1,"accounts":[...]}`) 存单 key; 平台 keychain 拒绝时
/// 当次操作 fallback 到 ApplicationSupport/accounts.json (原子写)。
///
/// 与 SecureSettingsRepo 不同的是 load/save 永不抛 —— 任何存储故障
/// (含测试环境插件缺 binding) 都退化为空列表 / 仅内存 emit, registry 是
/// 镜像层, 不该因为自身故障把登录主流程搞挂。
class SecureAccountRegistryStore implements AccountRegistryStore {
  SecureAccountRegistryStore({FlutterSecureStorage? storage, String? fallbackPath})
    : _storage = storage ?? _defaultStorage(),
      _fallbackPath = fallbackPath;
  final FlutterSecureStorage _storage;
  final String? _fallbackPath;
  static const _key = 'biumind.accounts';

  /// 构造参数与 SecureSettingsRepo 保持一致 (Android resetOnError 自愈;
  /// iOS/macOS first_unlock_this_device 不随备份迁移)。
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

  final StreamController<List<AccountRecord>> _ctrl =
      StreamController.broadcast();

  Future<String> _resolveFallbackPath() async {
    final p = _fallbackPath;
    if (p != null) return p;
    // Web 没有真实文件系统, 只是个内存占位 — web 上 flutter_secure_storage
    // 走 LocalStorage 后端, 正常不会进这个分支, 防御而已。
    if (kIsWeb) return '/biumind-accounts.json';
    final dir = await getApplicationSupportDirectory();
    return '${dir.path}/accounts.json';
  }

  @override
  Future<List<AccountRecord>> load() async {
    // 先试 secure storage; PlatformException (或无 binding 的
    // MissingPluginException 等) 只让当次操作走 fallback 文件。
    try {
      final raw = await _storage.read(key: _key);
      if (raw != null && raw.isNotEmpty) return _parse(raw);
    } on PlatformException catch (e) {
      // ignore: avoid_print
      print(
        'SecureAccountRegistryStore: keychain unavailable (${e.code}); '
        'falling back to plaintext accounts file for this operation. '
        'INSECURE — only acceptable in dev builds.',
      );
    } catch (e) {
      // ignore: avoid_print
      print(
        'SecureAccountRegistryStore: unexpected keychain read error '
        '(${e.runtimeType}: $e); falling back to accounts file.',
      );
    }
    try {
      final f = File(await _resolveFallbackPath());
      if (await f.exists()) {
        final raw = await f.readAsString();
        if (raw.isNotEmpty) return _parse(raw);
      }
    } catch (_) {
      /* 文件也读不到就当空 registry — 镜像层故障不放大 */
    }
    return const [];
  }

  List<AccountRecord> _parse(String raw) {
    try {
      final j = jsonDecode(raw) as Map<String, dynamic>;
      final list = (j['accounts'] as List?) ?? const [];
      return list
          .whereType<Map>()
          .map((m) => AccountRecord.fromJson(m.cast<String, dynamic>()))
          .toList();
    } catch (_) {
      // 损坏的 blob 按空处理 — 最坏丢休眠凭证, active 会话在 AppSettings。
      return const [];
    }
  }

  @override
  Future<void> save(List<AccountRecord> accounts) async {
    final payload = jsonEncode({
      'version': 1,
      'accounts': accounts.map((a) => a.toJson()).toList(),
    });
    try {
      await _storage.write(key: _key, value: payload);
    } on PlatformException catch (e) {
      // ignore: avoid_print
      print(
        'SecureAccountRegistryStore: keychain write failed (${e.code}); '
        'writing plaintext accounts file instead.',
      );
      await _writeToFile(payload);
    } catch (e) {
      // ignore: avoid_print
      print(
        'SecureAccountRegistryStore: unexpected keychain write error '
        '(${e.runtimeType}: $e); writing accounts file instead.',
      );
      await _writeToFile(payload);
    }
    _ctrl.add(accounts);
  }

  /// 原子写: 先写同目录 .tmp 再 rename 覆盖, 避免半截 JSON。文件写也失败
  /// (如测试环境无 binding) 时吞掉 — save 不退化为主流程异常。
  Future<void> _writeToFile(String payload) async {
    try {
      final path = await _resolveFallbackPath();
      final f = File(path);
      await f.parent.create(recursive: true);
      final tmp = File('$path.tmp');
      await tmp.writeAsString(payload);
      await tmp.rename(path);
    } catch (_) {
      /* 镜像层写失败不放大 */
    }
  }

  @override
  Stream<List<AccountRecord>> watch() => _ctrl.stream;
}

/// In-memory implementation for tests.
class InMemoryAccountRegistryStore implements AccountRegistryStore {
  InMemoryAccountRegistryStore([List<AccountRecord>? initial])
    : _current = initial ?? const [];
  List<AccountRecord> _current;
  final StreamController<List<AccountRecord>> _ctrl =
      StreamController.broadcast();

  @override
  Future<List<AccountRecord>> load() async => List.of(_current);

  @override
  Future<void> save(List<AccountRecord> accounts) async {
    _current = List.unmodifiable(accounts);
    _ctrl.add(_current);
  }

  @override
  Stream<List<AccountRecord>> watch() => _ctrl.stream;
}

final accountRegistryStoreProvider = Provider<AccountRegistryStore>((ref) {
  return SecureAccountRegistryStore();
});

import 'dart:convert';
import 'dart:io';

import 'package:biumind/services/settings_repo.dart';
import 'package:flutter/services.dart' show PlatformException;
import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:flutter_test/flutter_test.dart';

/// 可控故障的 fake secure storage — 模拟 Android ROM/备份恢复后 keychain
/// 读写抛 PlatformException,以及读回旧值的场景。
class _FakeSecureStorage extends FlutterSecureStorage {
  _FakeSecureStorage({
    this.alwaysFail = false,
    this.failWrites = 0,
    this.failReads = 0,
    this.staleRead = false,
  });

  /// 读写一律抛 PlatformException。
  final bool alwaysFail;

  /// 前 N 次 write 抛 PlatformException,之后恢复正常。
  int failWrites;

  /// 前 N 次 read 抛 PlatformException,之后恢复正常。
  int failReads;

  /// write 成功但 read 永远返回旧值 — 触发写后读校验失败。
  final bool staleRead;

  final Map<String, String> store = {};

  bool get _failNow => alwaysFail;

  @override
  Future<String?> read({
    required String key,
    AppleOptions? iOptions,
    AndroidOptions? aOptions,
    LinuxOptions? lOptions,
    WebOptions? webOptions,
    AppleOptions? mOptions,
    WindowsOptions? wOptions,
  }) async {
    if (_failNow || failReads > 0) {
      if (failReads > 0) failReads--;
      throw PlatformException(code: 'read_error', message: 'keystore gone');
    }
    final v = store[key];
    if (v == null) return null;
    if (staleRead) {
      return jsonEncode(
        const AppSettings(
          identityUrl: 'http://stale',
          refreshToken: 'stale-rt',
        ).toJson(),
      );
    }
    return v;
  }

  @override
  Future<void> write({
    required String key,
    required String? value,
    AppleOptions? iOptions,
    AndroidOptions? aOptions,
    LinuxOptions? lOptions,
    WebOptions? webOptions,
    AppleOptions? mOptions,
    WindowsOptions? wOptions,
  }) async {
    if (_failNow || failWrites > 0) {
      if (failWrites > 0) failWrites--;
      throw PlatformException(code: 'write_error', message: 'keystore gone');
    }
    if (value == null) {
      store.remove(key);
    } else {
      store[key] = value;
    }
  }
}

Future<Directory> _tempDir() async {
  final d = await Directory.systemTemp.createTemp('settings_repo_test');
  addTearDown(() => d.delete(recursive: true));
  return d;
}

void main() {
  test('AppSettings json round-trip', () {
    const s = AppSettings(
      identityUrl: 'http://localhost:7004',
      accessToken: 'tok',
      refreshToken: 'rt',
      tokenExpiresAt: '2026-05-24T10:00:00Z',
      userEmail: 'u@e.com',
      defaultChatModel: 'claude-haiku-4-5',
      theme: ThemePreference.dark,
    );
    final j = s.toJson();
    final r = AppSettings.fromJson(j);
    expect(r.identityUrl, 'http://localhost:7004');
    expect(r.accessToken, 'tok');
    expect(r.refreshToken, 'rt');
    expect(r.userEmail, 'u@e.com');
    expect(r.defaultChatModel, 'claude-haiku-4-5');
    expect(r.theme, ThemePreference.dark);
  });

  test('AppSettings empty json → defaults', () {
    final s = AppSettings.fromJson({});
    expect(s.identityUrl, isNull);
    expect(s.theme, ThemePreference.system);
    expect(s.signedIn, isFalse);
  });

  test('legacy hub_url + hub_token migrate to identity_url + access_token', () {
    // Old shape (pre-P3.2): hub_url:7001, hub_token, provider_keys.
    final s = AppSettings.fromJson({
      'hub_url': 'http://my-host:7001',
      'hub_token': 'legacy-jwt',
      'provider_keys': {'anthropic': 'sk-ant-old'},
      'mode': 'byo_endpoint', // also legacy
    });
    expect(s.identityUrl, 'http://my-host:7004');
    expect(s.accessToken, 'legacy-jwt');
  });


  test('hubUri/brainUri/aigcUri 同 origin (单入口, 不换端口)', () {
    // 单 origin 寻址: 各服务 endpoint 全等于 identityUrl, 由 site nginx
    // 按路径反代。以前 native 换端口 (7001/7003) 已废弃。
    const s = AppSettings(identityUrl: 'http://localhost:8088');
    expect(s.hubUri.toString(), 'http://localhost:8088');
    expect(s.brainUri.toString(), 'http://localhost:8088');
    expect(s.aigcUri.toString(), 'http://localhost:8088');
  });

  test('hubUri null when identityUrl unset', () {
    const s = AppSettings();
    expect(s.hubUri, isNull);
  });

  test('clearTokens preserves identity URL + email', () {
    const s = AppSettings(
      identityUrl: 'http://x',
      accessToken: 't',
      refreshToken: 'r',
      tokenExpiresAt: 'now',
      userEmail: 'u',
    );
    final c = s.clearTokens();
    expect(c.identityUrl, 'http://x');
    expect(c.userEmail, 'u');
    expect(c.accessToken, isNull);
    expect(c.refreshToken, isNull);
  });

  test('InMemorySettingsRepo load / save / watch', () async {
    final r = InMemorySettingsRepo();
    expect((await r.load()).identityUrl, isNull);

    final updates = <AppSettings>[];
    final sub = r.watch().listen(updates.add);

    await r.save(
      const AppSettings(identityUrl: 'http://x'),
    );
    expect((await r.load()).identityUrl, 'http://x');

    await Future<void>.delayed(const Duration(milliseconds: 5));
    expect(updates.length, 1);
    expect(updates.first.identityUrl, 'http://x');

    await sub.cancel();
  });

  test('copyWith preserves untouched fields', () {
    const s = AppSettings(identityUrl: 'a', accessToken: 'b');
    final r = s.copyWith(accessToken: 'c');
    expect(r.identityUrl, 'a');
    expect(r.accessToken, 'c');
  });

  test('signedIn requires both identity URL and token', () {
    expect(const AppSettings(identityUrl: 'x').signedIn, isFalse);
    expect(const AppSettings(accessToken: 't').signedIn, isFalse);
    expect(
      const AppSettings(identityUrl: 'x', accessToken: 't').signedIn,
      isTrue,
    );
  });

  group('SecureSettingsRepo P0 加固', () {
    test('secure 故障不锁死: 当次走 fallback, 下次自动恢复 secure', () async {
      final dir = await _tempDir();
      final fallback = '${dir.path}/settings.json';
      final storage = _FakeSecureStorage(failWrites: 1, failReads: 0);
      final repo = SecureSettingsRepo(storage: storage, fallbackPath: fallback);

      // 第一次 save: secure write 抛 PlatformException → 当次写 fallback 文件
      await repo.save(
        const AppSettings(identityUrl: 'http://x', refreshToken: 'rt1'),
      );
      expect(
        await File(fallback).exists(),
        isTrue,
        reason: 'secure 故障当次应落 fallback 文件',
      );

      // 第二次 save: secure 已恢复 → 写进 secure, 不再锁死 fallback
      await repo.save(
        const AppSettings(identityUrl: 'http://x', refreshToken: 'rt2'),
      );
      expect(
        storage.store['biumind.app_settings'],
        contains('rt2'),
        reason: '第二次 save 应重试 secure 并成功(自愈)',
      );
    });

    test('load 同理: secure read 故障当次读文件, 下次重试 secure', () async {
      final dir = await _tempDir();
      final fallback = '${dir.path}/settings.json';
      final storage = _FakeSecureStorage(failReads: 1);
      storage.store['biumind.app_settings'] = jsonEncode(
        const AppSettings(
          identityUrl: 'http://secure',
          refreshToken: 'rt-s',
        ).toJson(),
      );
      await File(fallback).writeAsString(
        jsonEncode(
          const AppSettings(
            identityUrl: 'http://file',
            refreshToken: 'rt-f',
          ).toJson(),
        ),
      );
      final repo = SecureSettingsRepo(storage: storage, fallbackPath: fallback);

      final first = await repo.load();
      expect(
        first.identityUrl,
        'http://file',
        reason: '第一次 read 抛异常 → 当次读 fallback 文件',
      );

      final second = await repo.load();
      expect(
        second.identityUrl,
        'http://secure',
        reason: '第二次 load 应重试 secure(不锁死)',
      );
    });

    test('fallback 迁移: 新文件不存在 + 旧 ~/.biumind 路径有文件 → 读旧文件', () async {
      final dir = await _tempDir();
      final legacy = File('${dir.path}/home/.biumind/settings.json');
      await legacy.parent.create(recursive: true);
      await legacy.writeAsString(
        jsonEncode(
          const AppSettings(
            identityUrl: 'http://legacy',
            refreshToken: 'rt-legacy',
          ).toJson(),
        ),
      );
      final repo = SecureSettingsRepo(
        storage: _FakeSecureStorage(alwaysFail: true),
        fallbackPath: '${dir.path}/support/settings.json',
        legacyFallbackPath: legacy.path,
      );

      final s = await repo.load();
      expect(s.identityUrl, 'http://legacy');
      expect(s.refreshToken, 'rt-legacy');
    });

    test('原子写: save 后无 .tmp 残留', () async {
      final dir = await _tempDir();
      final fallback = '${dir.path}/settings.json';
      final repo = SecureSettingsRepo(
        storage: _FakeSecureStorage(alwaysFail: true),
        fallbackPath: fallback,
      );

      await repo.save(
        const AppSettings(identityUrl: 'http://x', refreshToken: 'rt1'),
      );

      expect(await File(fallback).exists(), isTrue);
      expect(
        await File('$fallback.tmp').exists(),
        isFalse,
        reason: 'rename 后临时文件不应残留',
      );
    });

    test('写后读校验失败 → 双写另一存储 + onPersistWarning 触发', () async {
      final warnings = <String>[];
      SecureSettingsRepo.onPersistWarning = warnings.add;
      addTearDown(() => SecureSettingsRepo.onPersistWarning = null);
      final dir = await _tempDir();
      final fallback = '${dir.path}/settings.json';
      // write 成功但 read 回旧值 → 校验不匹配
      final storage = _FakeSecureStorage(staleRead: true);
      final repo = SecureSettingsRepo(storage: storage, fallbackPath: fallback);

      await repo.save(
        const AppSettings(identityUrl: 'http://x', refreshToken: 'rt1'),
      );

      expect(warnings, isNotEmpty, reason: '读回不匹配应触发 onPersistWarning(遥测钩子)');
      final f = File(fallback);
      expect(await f.exists(), isTrue, reason: '校验失败应向另一个存储(fallback 文件)双写兜底');
      final s = AppSettings.fromJson(jsonDecode(await f.readAsString()));
      expect(s.refreshToken, 'rt1');
      expect(s.identityUrl, 'http://x');
    });

    test('macOS 无签名不对称: secure 读 null + 文件有会话 → 读文件(不被藏)', () async {
      final dir = await _tempDir();
      final fallback = '${dir.path}/settings.json';
      // store 为空 → read 返回 null(不抛) — 复现 macOS 无签名构建:
      // 写抛 errSecMissingEntitlement 会话落文件, 读却只返回 null。
      final storage = _FakeSecureStorage();
      await File(fallback).writeAsString(
        jsonEncode(
          const AppSettings(
            identityUrl: 'http://file',
            refreshToken: 'rt-f',
          ).toJson(),
        ),
      );
      final repo = SecureSettingsRepo(storage: storage, fallbackPath: fallback);

      final s = await repo.load();
      expect(s.identityUrl, 'http://file');
      expect(s.refreshToken, 'rt-f');
    });

    test('secure 有 blob 时优先 secure, 忽略文件里的旧会话', () async {
      final dir = await _tempDir();
      final fallback = '${dir.path}/settings.json';
      final storage = _FakeSecureStorage();
      storage.store['biumind.app_settings'] = jsonEncode(
        const AppSettings(
          identityUrl: 'http://secure',
          refreshToken: 'rt-s',
        ).toJson(),
      );
      await File(fallback).writeAsString(
        jsonEncode(
          const AppSettings(
            identityUrl: 'http://file',
            refreshToken: 'rt-f',
          ).toJson(),
        ),
      );
      final repo = SecureSettingsRepo(storage: storage, fallbackPath: fallback);

      final s = await repo.load();
      expect(s.identityUrl, 'http://secure');
      expect(s.refreshToken, 'rt-s');
    });

    test('新文件是空壳(无 identity_url) + 旧路径有会话 → 捞回旧会话', () async {
      final dir = await _tempDir();
      final fallback = File('${dir.path}/support/settings.json');
      await fallback.parent.create(recursive: true);
      // keychain 不对称期间被启动写入覆盖出的空壳: 只有 installation_id,
      // 从未见过登录。
      await fallback.writeAsString(
        jsonEncode(const AppSettings(installationId: 'inst-1').toJson()),
      );
      final legacy = File('${dir.path}/home/.biumind/settings.json');
      await legacy.parent.create(recursive: true);
      await legacy.writeAsString(
        jsonEncode(
          const AppSettings(
            identityUrl: 'http://legacy',
            refreshToken: 'rt-legacy',
            installationId: 'inst-0',
          ).toJson(),
        ),
      );
      final repo = SecureSettingsRepo(
        storage: _FakeSecureStorage(alwaysFail: true),
        fallbackPath: fallback.path,
        legacyFallbackPath: legacy.path,
      );

      final s = await repo.load();
      expect(s.identityUrl, 'http://legacy');
      expect(s.refreshToken, 'rt-legacy');
    });

    test('新文件是登出态(有 identity_url 无 rt) + 旧路径有会话 → 尊重登出, 不复活', () async {
      final dir = await _tempDir();
      final fallback = File('${dir.path}/support/settings.json');
      await fallback.parent.create(recursive: true);
      // clearTokens 形态: identity_url/user_email 保留, token 已清。
      await fallback.writeAsString(
        jsonEncode(
          const AppSettings(
            identityUrl: 'http://x',
            userEmail: 'u@e.com',
          ).toJson(),
        ),
      );
      final legacy = File('${dir.path}/home/.biumind/settings.json');
      await legacy.parent.create(recursive: true);
      await legacy.writeAsString(
        jsonEncode(
          const AppSettings(
            identityUrl: 'http://legacy',
            refreshToken: 'rt-legacy',
          ).toJson(),
        ),
      );
      final repo = SecureSettingsRepo(
        storage: _FakeSecureStorage(alwaysFail: true),
        fallbackPath: fallback.path,
        legacyFallbackPath: legacy.path,
      );

      final s = await repo.load();
      expect(s.identityUrl, 'http://x');
      expect(s.refreshToken, isNull, reason: '主动登出后不得从旧路径复活会话');
    });
  });
}

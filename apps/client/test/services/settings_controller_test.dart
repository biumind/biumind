import 'package:biumind/data/local/db.dart';
import 'package:biumind/data/wiki_providers.dart' show appDbProvider;
import 'package:biumind/features/chat/data/file_bytes_cache.dart'
    show fileBytesCacheProvider;
import 'package:biumind/features/settings/application/settings_controller.dart';
import 'package:biumind/services/settings_repo.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

void main() {
  late InMemorySettingsRepo repo;
  late AppDb db;
  late ProviderContainer container;

  setUp(() {
    repo = InMemorySettingsRepo();
    // signOut 调 purgeUserData (清本地数据), 需 appDb (内存) + secure storage
    // (mock) 就绪; fileBytesCache override null 跳过 creds 链.
    db = AppDb.memory();
    FlutterSecureStorage.setMockInitialValues({});
    container = ProviderContainer(
      overrides: [
        settingsRepoProvider.overrideWithValue(repo),
        appDbProvider.overrideWithValue(db),
        fileBytesCacheProvider.overrideWithValue(null),
      ],
    );
  });

  tearDown(() async {
    container.dispose();
    await db.close();
  });

  test('initial load reads repo', () async {
    await repo.save(
      const AppSettings(identityUrl: 'http://x'),
    );
    final s = await container.read(settingsControllerProvider.future);
    expect(s.identityUrl, 'http://x');
  });


  test('updateChatModel persists', () async {
    await container.read(settingsControllerProvider.future);
    final ctl = container.read(settingsControllerProvider.notifier);
    await ctl.updateChatModel('claude-haiku-4-5-20251001');
    final s = await container.read(settingsControllerProvider.future);
    expect(s.defaultChatModel, 'claude-haiku-4-5-20251001');
  });

  test('updateSearchIncludeNotes persists（默认关，可开可关）', () async {
    final initial = await container.read(settingsControllerProvider.future);
    expect(initial.searchIncludeNotes, isFalse, reason: '默认关');

    final ctl = container.read(settingsControllerProvider.notifier);
    await ctl.updateSearchIncludeNotes(true);
    var s = await container.read(settingsControllerProvider.future);
    expect(s.searchIncludeNotes, isTrue);
    expect((await repo.load()).searchIncludeNotes, isTrue,
        reason: '落盘持久化');

    await ctl.updateSearchIncludeNotes(false);
    s = await container.read(settingsControllerProvider.future);
    expect(s.searchIncludeNotes, isFalse);
  });

  test('updateIdentityUrl persists', () async {
    await container.read(settingsControllerProvider.future);
    final ctl = container.read(settingsControllerProvider.notifier);
    await ctl.updateIdentityUrl('http://my-server:7004');
    final s = await container.read(settingsControllerProvider.future);
    expect(s.identityUrl, 'http://my-server:7004');
    // 单 origin: hubUri 等于 identityUrl, 不换端口。
    expect(s.hubUri.toString(), 'http://my-server:7004');
  });

  test('signOut clears tokens but keeps identity URL', () async {
    await repo.save(
      const AppSettings(
        identityUrl: 'http://x',
        accessToken: 't',
        refreshToken: 'r',
        userEmail: 'u@e.com',
      ),
    );
    container.invalidate(settingsControllerProvider);
    await container.read(settingsControllerProvider.future);
    await container.read(settingsControllerProvider.notifier).signOut();
    final s = await container.read(settingsControllerProvider.future);
    expect(s.identityUrl, 'http://x');
    expect(s.userEmail, 'u@e.com');
    expect(s.accessToken, isNull);
    expect(s.refreshToken, isNull);
  });

  test('pingHub fails when no URL', () async {
    await container.read(settingsControllerProvider.future);
    expect(
      () => container.read(settingsControllerProvider.notifier).pingHub(),
      throwsA(isA<HubPingError>()),
    );
  });

  test('signOut compareAndClear: 磁盘有不同 rt → 收编磁盘值, 不清盘', () async {
    await repo.save(
      const AppSettings(
        identityUrl: 'http://x',
        accessToken: 't',
        refreshToken: 'r-old',
      ),
    );
    await container.read(settingsControllerProvider.future);
    // 模拟同机另一实例写入新凭证
    await repo.save(
      const AppSettings(
        identityUrl: 'http://x',
        accessToken: 't2',
        refreshToken: 'r-new',
        userEmail: 'u@e.com',
      ),
    );

    final cleared = await container
        .read(settingsControllerProvider.notifier)
        .signOut(compareAndClear: true);

    expect(cleared, isFalse, reason: '收编路径不算清盘');
    final s = await container.read(settingsControllerProvider.future);
    expect(s.refreshToken, 'r-new', reason: 'state 应收编成磁盘最新值');
    expect((await repo.load()).refreshToken, 'r-new', reason: '磁盘上的新凭证不能被抹掉');
  });

  test('signOut compareAndClear: 磁盘同 rt → 走原清盘路径', () async {
    await repo.save(
      const AppSettings(
        identityUrl: 'http://x',
        accessToken: 't',
        refreshToken: 'r',
        userEmail: 'u@e.com',
      ),
    );
    await container.read(settingsControllerProvider.future);

    final cleared = await container
        .read(settingsControllerProvider.notifier)
        .signOut(compareAndClear: true);

    expect(cleared, isTrue);
    final s = await container.read(settingsControllerProvider.future);
    expect(s.refreshToken, isNull);
    expect(s.accessToken, isNull);
    expect(s.identityUrl, 'http://x', reason: '清 token 但保留 identity URL');
  });

  test('ensureInstallationId: settings 丢失时从 SharedPreferences 恢复', () async {
    SharedPreferences.setMockInitialValues({
      'biumind.installation_id': 'inst-from-prefs',
    });
    await container.read(settingsControllerProvider.future);

    await container
        .read(settingsControllerProvider.notifier)
        .ensureInstallationId('generated-new');

    final s = await container.read(settingsControllerProvider.future);
    expect(
      s.installationId,
      'inst-from-prefs',
      reason: 'settings 空 → 从 prefs 找回同一个 id, 不生成新 family',
    );
    expect(
      (await repo.load()).installationId,
      'inst-from-prefs',
      reason: '找回的 id 应 _save 回 settings',
    );
  });

  test('ensureInstallationId: 全新 → 用生成值并双写 prefs', () async {
    SharedPreferences.setMockInitialValues({});
    await container.read(settingsControllerProvider.future);

    await container
        .read(settingsControllerProvider.notifier)
        .ensureInstallationId('generated-1');

    final s = await container.read(settingsControllerProvider.future);
    expect(s.installationId, 'generated-1');
    final prefs = await SharedPreferences.getInstance();
    expect(
      prefs.getString('biumind.installation_id'),
      'generated-1',
      reason: '永远写回 prefs 做兜底',
    );
  });

  test('ensureInstallationId: settings 已有 → 不变, 但同步回 prefs', () async {
    SharedPreferences.setMockInitialValues({});
    await repo.save(const AppSettings(installationId: 'inst-existing'));
    container.invalidate(settingsControllerProvider);
    await container.read(settingsControllerProvider.future);

    await container
        .read(settingsControllerProvider.notifier)
        .ensureInstallationId('generated-2');

    final s = await container.read(settingsControllerProvider.future);
    expect(s.installationId, 'inst-existing');
    final prefs = await SharedPreferences.getInstance();
    expect(prefs.getString('biumind.installation_id'), 'inst-existing');
  });
}

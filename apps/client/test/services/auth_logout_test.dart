// auth_logout 集成测 — purgeUserData 清本地用户数据.
//
// purgeUserData 对 wiki/aigc/sse DAO 直接构造 (XxxDao(db), 不经 provider),
// 故只 override appDbProvider; fileBytesCacheProvider 依赖 creds 链,
// override 为 null 跳过; sidebarOutbox 走 FlutterSecureStorage, 用
// setMockInitialValues 内存化. 其余 provider (biuDaemon/credits/threads)
// 仅 invalidate (未 build 过, 不触发 dispose/build, 安全).

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:biumind/data/local/db.dart';
import 'package:biumind/data/sse/sse_cursors_dao.dart';
import 'package:biumind/data/wiki_providers.dart' show appDbProvider;
import 'package:biumind/features/chat/data/file_bytes_cache.dart'
    show fileBytesCacheProvider;
import 'package:biumind/services/auth_logout.dart';

/// 测试钩子: 拿 ref 调 purgeUserData (Ref 不能在测试直接构造).
final _testPurge = FutureProvider<void>((ref) => purgeUserData(ref));

void main() {
  // invalidate creditsBalance 等会顺链触发 settingsController → SettingsRepo
  // 读 disk, 需 binding 就绪 (否则 _loadFromFile 抛 Binding 未初始化, 虽容错
  // 返空但留噪音). 显式初始化消除.
  TestWidgetsFlutterBinding.ensureInitialized();

  test('purgeUserData 清 sse cursors (登出防下个用户续接旧 cursor)', () async {
    FlutterSecureStorage.setMockInitialValues({});
    final db = AppDb.memory();
    addTearDown(db.close);

    // 预填多 scope cursor (模拟上个用户的 realtime 续接状态).
    await SseCursorsDao(db).save('aigc.tasks', 'A1');
    await SseCursorsDao(db).save('skills.events', 'B2');
    expect(await SseCursorsDao(db).load('aigc.tasks'), 'A1'); // 预填确认

    final container = ProviderContainer(overrides: [
      appDbProvider.overrideWithValue(db),
      fileBytesCacheProvider.overrideWithValue(null),
    ]);
    addTearDown(container.dispose);

    await container.read(_testPurge.future);

    // clearAll 经 purgeUserData 触发 → 全 scope 清空.
    expect(await SseCursorsDao(db).load('aigc.tasks'), isNull);
    expect(await SseCursorsDao(db).load('skills.events'), isNull);
  });

  test('purgeUserData 不抛 (DAO 清 + sidebar outbox + provider invalidate 全跑通)',
      () async {
    FlutterSecureStorage.setMockInitialValues({});
    final db = AppDb.memory();
    addTearDown(db.close);

    final container = ProviderContainer(overrides: [
      appDbProvider.overrideWithValue(db),
      fileBytesCacheProvider.overrideWithValue(null),
    ]);
    addTearDown(container.dispose);

    // 空库 + 各清理步骤 (wiki.wipe/aigc.deleteAll/sse.clearAll/sidebar.clear/
    // invalidate 全套) 不应抛.
    await expectLater(container.read(_testPurge.future), completes);
  });
}

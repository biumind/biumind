// Sidebar outbox serde + flush logic.
//
// 不打实际 SecureStorage / 网络 — 只测纯逻辑层 (PendingSidebarEdit
// JSON roundtrip + flushSidebarOutbox 决策 path)。P2 多账号的 namespace
// 隔离用内存 fake storage 覆盖。

import 'package:biumind/data/api/sidebar_client.dart';
import 'package:biumind/data/sidebar_outbox.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:flutter_test/flutter_test.dart';

/// 内存 fake secure storage (同 settings_repo_test 的模式)。
class _FakeSecureStorage extends FlutterSecureStorage {
  final Map<String, String> store = {};

  @override
  Future<String?> read({
    required String key,
    AppleOptions? iOptions,
    AndroidOptions? aOptions,
    LinuxOptions? lOptions,
    WebOptions? webOptions,
    AppleOptions? mOptions,
    WindowsOptions? wOptions,
  }) async =>
      store[key];

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
    if (value == null) {
      store.remove(key);
    } else {
      store[key] = value;
    }
  }

  @override
  Future<void> delete({
    required String key,
    AppleOptions? iOptions,
    AndroidOptions? aOptions,
    LinuxOptions? lOptions,
    WebOptions? webOptions,
    AppleOptions? mOptions,
    WindowsOptions? wOptions,
  }) async {
    store.remove(key);
  }
}

PendingSidebarEdit _edit(String scope) => PendingSidebarEdit(
      scope: scope,
      items: const [SidebarItem(kind: 'app', ref: 'i-1')],
      expectedVersion: 3,
      queuedAt: DateTime.utc(2026, 8, 10),
    );

void main() {
  group('PendingSidebarEdit', () {
    test('toJson/fromJson roundtrip', () {
      final ts = DateTime.utc(2026, 1, 15, 12, 30);
      final orig = PendingSidebarEdit(
        scope: 'desktop',
        items: [
          const SidebarItem(kind: 'system', ref: 'chat'),
          const SidebarItem(kind: 'app', ref: 'i-1'),
          const SidebarItem(kind: 'system', ref: 'wiki', hidden: true),
        ],
        expectedVersion: 7,
        queuedAt: ts,
      );
      final j = orig.toJson();
      final back = PendingSidebarEdit.fromJson(j);
      expect(back.scope, 'desktop');
      expect(back.expectedVersion, 7);
      expect(back.queuedAt.toUtc(), ts);
      expect(back.items.length, 3);
      expect(back.items[0].kind, 'system');
      expect(back.items[0].ref, 'chat');
      expect(back.items[2].hidden, true);
    });

    test('fromJson 容错: 缺字段默认填充', () {
      final back = PendingSidebarEdit.fromJson({});
      expect(back.scope, 'desktop');
      expect(back.expectedVersion, 1);
      expect(back.items, isEmpty);
    });

    test('hidden 为 false 不 emit (节省 payload)', () {
      const item = SidebarItem(kind: 'system', ref: 'chat');
      final j = PendingSidebarEdit(
        scope: 'desktop',
        items: const [item],
        expectedVersion: 1,
        queuedAt: DateTime.utc(2026, 1, 1),
      ).toJson();
      final itemJson = (j['items'] as List).first as Map;
      expect(itemJson.containsKey('hidden'), false);
    });
  });

  group('P2 多账号: namespace 隔离', () {
    test('各账号的 pending 互不可见; 旧裸 key 留在原地不被读', () async {
      final storage = _FakeSecureStorage();
      final outboxA = SidebarOutbox(storage: storage, namespace: 'hash-a:user-a');
      final outboxB = SidebarOutbox(storage: storage, namespace: 'hash-b:user-b');
      final legacy = SidebarOutbox(storage: storage); // 无 namespace = 旧格式

      // 旧版本遗留: 裸 key 下有一条 pending。
      await legacy.save(_edit('desktop'));
      // account A 新版本也存了一条。
      await outboxA.save(_edit('desktop'));

      // account A 读到自己 namespace 下的; account B 看不到 A 的
      // (switchAccount 后不会把 A 的 pending flush 到 B 的账号)。
      expect(await outboxA.load('desktop'), isNotNull);
      expect(await outboxB.load('desktop'), isNull);

      // 旧裸 key 的 pending 归属不明 —— 留在原地, 带 namespace 的读写
      // 不再碰它 (不猜归属 = 不跨账号写入)。
      expect(
        storage.store.containsKey('biumind.sidebar_outbox.desktop'),
        isTrue,
        reason: 'legacy key 原样保留, 不被覆盖也不被清',
      );
      // A 的写入落在带前缀的 key, 没碰裸 key。
      expect(
        storage.store.containsKey(
            'biumind.sidebar_outbox.hash-a:user-a.desktop'),
        isTrue,
      );

      // clear (logout purge 路径) 只清自己 namespace 的 key。
      await outboxA.clear('desktop');
      expect(await outboxA.load('desktop'), isNull);
      expect(
        storage.store.containsKey('biumind.sidebar_outbox.desktop'),
        isTrue,
        reason: '清 A 的 pending 不动 legacy key',
      );
    });

    test('PendingSidebarEdit.scope 保持裸 scope (namespace 不进服务端 PUT)',
        () async {
      final storage = _FakeSecureStorage();
      final outbox = SidebarOutbox(storage: storage, namespace: 'hash-a:user-a');
      await outbox.save(_edit('desktop'));
      final loaded = await outbox.load('desktop');
      expect(loaded!.scope, 'desktop',
          reason: 'flush 用 pending.scope 做 PUT —— 必须是服务端裸 scope');
    });
  });
}

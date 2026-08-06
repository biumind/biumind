// Sidebar outbox serde + flush logic.
//
// 不打实际 SecureStorage / 网络 — 只测纯逻辑层 (PendingSidebarEdit
// JSON roundtrip + flushSidebarOutbox 决策 path)。

import 'package:biumind/data/api/sidebar_client.dart';
import 'package:biumind/data/sidebar_outbox.dart';
import 'package:flutter_test/flutter_test.dart';

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
}

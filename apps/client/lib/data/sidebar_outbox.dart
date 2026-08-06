// SidebarOutbox — 离线 / 网络抖动期间的 sidebar 编辑队列。
//
// 设计 §10A.12: 客户端编辑后立刻乐观应用本地, 写入 outbox; 重连时 PUT
// /v1/sidebar/layout, 200 → 清 outbox; 409 → 拉服务端最新, 弹 "另一
// 设备已改动" 走既有冲突 UX。
//
// v1.5 范围:
//   - 写入: togglePinnedApp / reorderPinnedApp / SidebarCustomizePage
//     PUT 撞 ApiError 之外的 exception (≈ network) → 写 outbox
//   - 乐观读: sidebarLayoutProvider 加载时若本地有 pending, 用 pending
//     的 items 覆盖服务端 fetched layout (服务端 version 保留, 给后续
//     PUT 用)
//   - flush: 应用启动 + Realtime listener start() 各调一次, 把 pending
//     PUT 出去
//
// 持久化用 FlutterSecureStorage 一致 (项目其他偏好都走它); 单 scope
// 单 entry — 后写覆盖前写 (item-level 合并交给服务端 expected_version
// 乐观锁判断)。

import 'dart:convert';

import 'package:flutter/foundation.dart' show debugPrint;
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';

import 'api/sidebar_client.dart';

/// 一条等待提交的 sidebar 编辑。expectedVersion 是用户编辑时本地 cache
/// 的服务端 version, flush PUT 用这个值;  撞 409 表示服务端已被其他
/// device 改写, 走冲突 UX (UI 重新载入 layout)。
class PendingSidebarEdit {
  final String scope;
  final List<SidebarItem> items;
  final int expectedVersion;
  final DateTime queuedAt;

  const PendingSidebarEdit({
    required this.scope,
    required this.items,
    required this.expectedVersion,
    required this.queuedAt,
  });

  Map<String, dynamic> toJson() => {
        'scope': scope,
        'items': items.map((i) => i.toJson()).toList(),
        'expected_version': expectedVersion,
        'queued_at': queuedAt.toUtc().toIso8601String(),
      };

  factory PendingSidebarEdit.fromJson(Map<String, dynamic> j) {
    final list = (j['items'] as List?) ?? const [];
    return PendingSidebarEdit(
      scope: j['scope'] as String? ?? 'desktop',
      items: list
          .whereType<Map<String, dynamic>>()
          .map(SidebarItem.fromJson)
          .toList(growable: false),
      expectedVersion: j['expected_version'] as int? ?? 1,
      queuedAt: DateTime.tryParse(j['queued_at'] as String? ?? '')?.toUtc()
          ?? DateTime.fromMillisecondsSinceEpoch(0),
    );
  }
}

/// 持久层 — key = `biumind.sidebar_outbox.<scope>`。一个 scope 至多一
/// 条 pending; 后写覆盖前写。
class SidebarOutbox {
  SidebarOutbox({FlutterSecureStorage? storage})
      : _storage = storage ?? const FlutterSecureStorage();
  final FlutterSecureStorage _storage;

  String _key(String scope) => 'biumind.sidebar_outbox.$scope';

  Future<PendingSidebarEdit?> load(String scope) async {
    try {
      final raw = await _storage.read(key: _key(scope));
      if (raw == null || raw.isEmpty) return null;
      final j = jsonDecode(raw);
      if (j is! Map<String, dynamic>) return null;
      return PendingSidebarEdit.fromJson(j);
    } catch (e) {
      debugPrint('SidebarOutbox.load failed: $e');
      return null;
    }
  }

  Future<void> save(PendingSidebarEdit edit) async {
    try {
      await _storage.write(
        key: _key(edit.scope),
        value: jsonEncode(edit.toJson()),
      );
    } catch (e) {
      debugPrint('SidebarOutbox.save failed: $e');
    }
  }

  Future<void> clear(String scope) async {
    try {
      await _storage.delete(key: _key(scope));
    } catch (e) {
      debugPrint('SidebarOutbox.clear failed: $e');
    }
  }
}

final sidebarOutboxProvider =
    Provider<SidebarOutbox>((_) => SidebarOutbox());

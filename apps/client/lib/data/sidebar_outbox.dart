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
//
// P2 多账号: storage key 带账号 namespace (= chat/notes 同一把 ownerKey
// 隔离键), 即 `biumind.sidebar_outbox.<ownerKey>.<scope>`。不隔离的话
// switchAccount (不登出) 后 listener start() 的 flush 会用新账号 creds
// 把旧账号的 pending 编辑 PUT 上去 (logout 路径有 purgeUserData 兜底,
// switch 没有)。
//
// 存量迁移: 老版本留在裸 key `biumind.sidebar_outbox.desktop` 下的 pending
// 编辑归属不明 (可能是任一历史账号的), 选择**留在原地不再 flush** ——
// 不猜归属 (猜错 = 跨账号写入), 也不删 (保留现场, 且内容只是 pinned app
// id 列表, 遗留无害)。影响: 升级前恰好有离线 pending 的用户那次编辑不再
// 补传, UI 回落到服务端 layout 后重做一次即可。

import 'dart:convert';

import 'package:flutter/foundation.dart' show debugPrint;
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';

import '../features/chat/data/chat_scope.dart' show chatOwnerScopeProvider;
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

/// 持久层 — key = `biumind.sidebar_outbox.<namespace>.<scope>`（namespace
/// = 当前账号 ownerKey，见文件头注释；未登录/测试不传 namespace 时退化为
/// 旧格式 `biumind.sidebar_outbox.<scope>`）。一个 scope 至多一条 pending;
/// 后写覆盖前写。
///
/// 注意 [PendingSidebarEdit.scope] 仍是裸的服务端 scope ('desktop') ——
/// namespace 只作用于本地 storage key, 不进 PUT 请求体。
class SidebarOutbox {
  SidebarOutbox({
    FlutterSecureStorage? storage,
    String? namespace,
    String? Function()? namespaceResolver,
  })  : _storage = storage ?? const FlutterSecureStorage(),
        _namespace = namespace,
        _namespaceResolver = namespaceResolver;
  final FlutterSecureStorage _storage;

  /// 固定 namespace (测试用)。
  final String? _namespace;

  /// 懒解析当前账号 ownerKey (生产路径) — 每次操作时现读, 见
  /// sidebarOutboxProvider 注释 (不能直接 watch, 会 CircularDependencyError)。
  final String? Function()? _namespaceResolver;

  String? get _ns => _namespaceResolver?.call() ?? _namespace;

  String _key(String scope) {
    final ns = _ns;
    if (ns == null || ns.isEmpty) return 'biumind.sidebar_outbox.$scope';
    return 'biumind.sidebar_outbox.$ns.$scope';
  }

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
    Provider<SidebarOutbox>((ref) {
  // P2 多账号: namespace = 当前账号 ownerKey, 每次操作现读。
  //
  // 为什么是 resolver 而不是 ref.watch: purgeUserData 在
  // SettingsController.signOut 里用 controller 自己的 ref 读本 provider;
  // 若这里 watch chatOwnerScopeProvider (传递依赖 settingsControllerProvider)
  // 就构成 settingsController → sidebarOutbox → chatOwnerScope →
  // settingsController 的循环, riverpod 抛 CircularDependencyError (同一
  // 陷阱见 auth_logout.dart 头注释)。outbox 本身无状态, 不需要响应式重建。
  return SidebarOutbox(
    namespaceResolver: () => ref.read(chatOwnerScopeProvider),
  );
});

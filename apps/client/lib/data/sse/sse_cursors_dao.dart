// SseCursorsDao — 持久化 RealtimeHub 的 last-event-id, 重启秒续接.
//
// 多个 RealtimeHub 实例共用此表, 用 [scope] 区分。P2 多账号起 scope =
// 'ownerKey:topic' (ownerKey = sha256(normalize(identityUrl)) + ':' + JWT
// sub, 与 chat/notes 本地数据同一把隔离键) — 切账号后各账号的 cursor 互不
// 污染。服务端 ledger 全局 ID ordering, replay 时 sinceID + topic filter 取
// 正确范围, 客户端只需把 cursor 持久化即可.

import 'package:drift/drift.dart';

import '../local/db.dart';

part 'sse_cursors_dao.g.dart';

@DriftAccessor(tables: [SseCursors])
class SseCursorsDao extends DatabaseAccessor<AppDb> with _$SseCursorsDaoMixin {
  SseCursorsDao(super.db);

  /// 读 [scope] 的 last-event-id; 第一次启动 / 未写过返 null.
  Future<String?> load(String scope) async {
    final row = await (select(sseCursors)..where((t) => t.scope.equals(scope)))
        .getSingleOrNull();
    return row?.lastEventId;
  }

  /// 覆写 [scope] 的 cursor. 同 scope 多次写 last-write-wins.
  Future<void> save(String scope, String eventId) async {
    await into(sseCursors).insertOnConflictUpdate(SseCursorsCompanion.insert(
      scope: scope,
      lastEventId: eventId,
      updatedAt: DateTime.now(),
    ));
  }

  /// 清掉 [scope] 的 cursor — 一般不用, 留给 desync / signOut 路径.
  Future<void> clear(String scope) async {
    await (delete(sseCursors)..where((t) => t.scope.equals(scope))).go();
  }

  /// 清掉所有 scope 的 cursor — 登出时调 (auth_logout.purgeUserData)。
  /// 各账号的 cursor 按 'ownerKey:topic' 前缀共存, 登出清全表省事且安全
  /// (switchAccount 不登出, 靠 scope 前缀天然隔离, 不经这里)。
  Future<void> clearAll() async {
    await delete(sseCursors).go();
  }
}

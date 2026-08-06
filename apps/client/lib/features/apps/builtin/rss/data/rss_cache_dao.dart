// RssCacheDao — RSS feeds/entries 本地缓存读写层 (M10.1).
//
// 缓存策略:
//   - 存原始 JSON payload (服务端返回的 map), 读出时交给 Feed.fromJson /
//     Entry.fromJson —— 模型加字段无需改 schema
//   - scopeId (JWT sub) 隔离, 防止切账号串数据
//   - upsert 用 insertOnConflictUpdate (按 id 主键)
//   - TTL: feeds 60d / entries 30d; entries 总数上限 1000 (超出按 fetchedAt
//     最旧裁剪). prune 在每次 upsert 后调一次, 成本可忽略 (本地 sqlite)
//
// 不做的事:
//   - 不缓存 rules/hits/today/boards —— 这些要么实时性强(hits)要么服务端
//     算(today/boards), 缓存收益低且容易陈旧. M10.1 只缓存 feeds+entries
//     这两个"列表骨架", 命中"杀进程重启秒显上次列表"的核心诉求.

import 'dart:convert';

import 'package:drift/drift.dart';

import '../../../../../data/local/db.dart' as drift;
import '../models.dart';

const _entriesMaxRows = 1000;
const _feedsTtl = Duration(days: 60);
const _entriesTtl = Duration(days: 30);

class RssCacheDao {
  RssCacheDao(this._db, this._now);
  final drift.AppDb _db;
  // now 注入便于测试; 生产传 DateTime.now.
  final DateTime Function() _now;

  // ─── feeds ───────────────────────────────────────────────────────

  Future<List<Feed>> readFeeds(String scopeId) async {
    final rows = await (_db.select(_db.rssFeedsCache)
          ..where((t) => t.scopeId.equals(scopeId))
          ..orderBy([
            (t) => OrderingTerm(
                expression: t.cachedAt, mode: OrderingMode.desc),
          ]))
        .get();
    return rows.map(_feedFromRow).where((f) => f != null).cast<Feed>().toList();
  }

  /// 用一批服务端 JSON 覆盖该 scope 的 feeds 缓存. replace 语义 —
  /// feeds 列表是全量返回的, 直接替换比 diff 简单且正确 (取消订阅的源
  /// 会从缓存消失).
  Future<void> replaceFeeds(String scopeId, List<Map<String, dynamic>> raw) async {
    final now = _now();
    await _db.transaction(() async {
      await (_db.delete(_db.rssFeedsCache)
            ..where((t) => t.scopeId.equals(scopeId)))
          .go();
      for (final m in raw) {
        final id = (m['id'] ?? '').toString();
        if (id.isEmpty) continue;
        await _db.into(_db.rssFeedsCache).insertOnConflictUpdate(
              drift.RssFeedsCacheCompanion.insert(
                id: id,
                scopeId: scopeId,
                payloadJson: jsonEncode(m),
                cachedAt: now,
              ),
            );
      }
    });
    await _pruneFeeds(scopeId);
  }

  // ─── entries ─────────────────────────────────────────────────────

  /// 读某 scope 下的 entries; feedId 非空时按源过滤. 按 fetchedAt 降序
  /// (跟网络路径排序一致).
  Future<List<Entry>> readEntries(String scopeId, {String? feedId}) async {
    final q = _db.select(_db.rssEntriesCache)
      ..where((t) => t.scopeId.equals(scopeId))
      ..orderBy([
        (t) => OrderingTerm(expression: t.fetchedAt, mode: OrderingMode.desc),
      ]);
    if (feedId != null && feedId.isNotEmpty && feedId != 'all') {
      q.where((t) => t.feedId.equals(feedId));
    }
    final rows = await q.get();
    return rows
        .map(_entryFromRow)
        .where((e) => e != null)
        .cast<Entry>()
        .toList();
  }

  /// upsert 一批 entries (不删旧). 网络返回的是某个 query 的窗口 (limit
  /// 100), 不能 replace 整个 scope —— 否则切 feed 会丢别的 feed 缓存.
  /// 所以用 upsert, 让缓存累积; prune 控制总量.
  Future<void> upsertEntries(
      String scopeId, List<Map<String, dynamic>> raw) async {
    if (raw.isEmpty) return;
    final now = _now();
    await _db.batch((b) {
      for (final m in raw) {
        final id = (m['id'] ?? '').toString();
        if (id.isEmpty) continue;
        b.insert(
          _db.rssEntriesCache,
          drift.RssEntriesCacheCompanion.insert(
            id: id,
            scopeId: scopeId,
            feedId: (m['feed_id'] ?? '').toString(),
            payloadJson: jsonEncode(m),
            fetchedAt: Value(_parseTime(m['fetched_at'])),
            cachedAt: now,
          ),
          onConflict: DoUpdate((_) => drift.RssEntriesCacheCompanion(
                scopeId: Value(scopeId),
                feedId: Value((m['feed_id'] ?? '').toString()),
                payloadJson: Value(jsonEncode(m)),
                fetchedAt: Value(_parseTime(m['fetched_at'])),
                cachedAt: Value(now),
              )),
        );
      }
    });
    await _pruneEntries(scopeId);
  }

  /// 清某 scope 全部缓存 (登出 / 切账号时调).
  Future<void> clearScope(String scopeId) async {
    await (_db.delete(_db.rssFeedsCache)
          ..where((t) => t.scopeId.equals(scopeId)))
        .go();
    await (_db.delete(_db.rssEntriesCache)
          ..where((t) => t.scopeId.equals(scopeId)))
        .go();
  }

  // ─── prune (TTL + 总量上限) ──────────────────────────────────────

  Future<void> _pruneFeeds(String scopeId) async {
    final cutoff = _now().subtract(_feedsTtl);
    await (_db.delete(_db.rssFeedsCache)
          ..where((t) => t.scopeId.equals(scopeId) & t.cachedAt.isSmallerThanValue(cutoff)))
        .go();
  }

  Future<void> _pruneEntries(String scopeId) async {
    // 1. TTL
    final cutoff = _now().subtract(_entriesTtl);
    await (_db.delete(_db.rssEntriesCache)
          ..where((t) =>
              t.scopeId.equals(scopeId) &
              t.cachedAt.isSmallerThanValue(cutoff)))
        .go();
    // 2. 总量上限 — 超出按 fetchedAt 最旧裁剪. count 先查, 超了再删.
    final countExp = _db.rssEntriesCache.id.count();
    final cntQ = _db.selectOnly(_db.rssEntriesCache)
      ..addColumns([countExp])
      ..where(_db.rssEntriesCache.scopeId.equals(scopeId));
    final total = await cntQ.map((r) => r.read(countExp) ?? 0).getSingle();
    if (total <= _entriesMaxRows) return;
    // 找第 1000 旧的 fetchedAt 当阈值, 删更旧的. 简化: 取要保留的 id 集合
    // 之外的删掉.
    final keep = await (_db.select(_db.rssEntriesCache)
          ..where((t) => t.scopeId.equals(scopeId))
          ..orderBy([
            (t) => OrderingTerm(
                expression: t.fetchedAt, mode: OrderingMode.desc),
          ])
          ..limit(_entriesMaxRows))
        .get();
    final keepIds = keep.map((r) => r.id).toSet();
    final all = await (_db.select(_db.rssEntriesCache)
          ..where((t) => t.scopeId.equals(scopeId)))
        .get();
    final toDelete =
        all.where((r) => !keepIds.contains(r.id)).map((r) => r.id).toList();
    if (toDelete.isEmpty) return;
    await (_db.delete(_db.rssEntriesCache)
          ..where((t) => t.id.isIn(toDelete)))
        .go();
  }

  // ─── row ↔ model ─────────────────────────────────────────────────

  Feed? _feedFromRow(drift.LocalRssFeed r) {
    try {
      final m = jsonDecode(r.payloadJson) as Map<String, dynamic>;
      return Feed.fromJson(m);
    } catch (_) {
      return null; // 坏行直接跳过, 不阻塞整批
    }
  }

  Entry? _entryFromRow(drift.LocalRssEntry r) {
    try {
      final m = jsonDecode(r.payloadJson) as Map<String, dynamic>;
      return Entry.fromJson(m);
    } catch (_) {
      return null;
    }
  }

  static DateTime? _parseTime(Object? v) {
    if (v is String && v.isNotEmpty) return DateTime.tryParse(v);
    return null;
  }
}

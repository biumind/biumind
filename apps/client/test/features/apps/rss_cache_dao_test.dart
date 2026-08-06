// M10.1 RssCacheDao 单测 — 用 AppDb.memory() 跑真 sqlite (内存), 验证
// upsert / read / scope 隔离 / TTL / 总量裁剪 / replace 语义.

import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:biumind/data/local/db.dart';
import 'package:biumind/features/apps/builtin/rss/data/rss_cache_dao.dart';

void main() {
  late AppDb db;
  // 固定 now 便于测 TTL.
  var now = DateTime.utc(2026, 6, 26, 12);
  RssCacheDao dao() => RssCacheDao(db, () => now);

  setUp(() => db = AppDb.memory());
  tearDown(() => db.close());

  Map<String, dynamic> entry(String id, String feedId, {String? fetchedAt}) => {
        'id': id,
        'feed_id': feedId,
        'title': 'Entry $id',
        'fetched_at': fetchedAt ?? '2026-06-26T10:00:00Z',
      };

  test('feeds replace + read round-trips', () async {
    final d = dao();
    await d.replaceFeeds('user1', [
      {'id': 'f1', 'title': 'Feed One'},
      {'id': 'f2', 'title': 'Feed Two'},
    ]);
    final feeds = await d.readFeeds('user1');
    expect(feeds.length, 2);
    expect(feeds.map((f) => f.id).toSet(), {'f1', 'f2'});
  });

  test('feeds replace removes stale (unsubscribed) rows', () async {
    final d = dao();
    await d.replaceFeeds('user1', [
      {'id': 'f1', 'title': 'One'},
      {'id': 'f2', 'title': 'Two'},
    ]);
    // 第二次只返 f1 — f2 应从缓存消失
    await d.replaceFeeds('user1', [
      {'id': 'f1', 'title': 'One'},
    ]);
    final feeds = await d.readFeeds('user1');
    expect(feeds.map((f) => f.id).toList(), ['f1']);
  });

  test('scope isolation — user2 never sees user1 data', () async {
    final d = dao();
    await d.replaceFeeds('user1', [
      {'id': 'f1', 'title': 'One'},
    ]);
    await d.upsertEntries('user1', [entry('e1', 'f1')]);
    expect(await d.readFeeds('user2'), isEmpty);
    expect(await d.readEntries('user2'), isEmpty);
  });

  test('entries upsert accumulates across feeds (no replace)', () async {
    final d = dao();
    await d.upsertEntries('user1', [entry('e1', 'f1')]);
    await d.upsertEntries('user1', [entry('e2', 'f2')]);
    final all = await d.readEntries('user1');
    expect(all.length, 2);
    // 按 feed 过滤
    final f1 = await d.readEntries('user1', feedId: 'f1');
    expect(f1.map((e) => e.id).toList(), ['e1']);
  });

  test('entries upsert updates existing row (same id)', () async {
    final d = dao();
    await d.upsertEntries('user1', [
      {'id': 'e1', 'feed_id': 'f1', 'title': 'Old', 'fetched_at': '2026-06-26T10:00:00Z'},
    ]);
    await d.upsertEntries('user1', [
      {'id': 'e1', 'feed_id': 'f1', 'title': 'New', 'fetched_at': '2026-06-26T10:00:00Z'},
    ]);
    final all = await d.readEntries('user1');
    expect(all.length, 1);
    expect(all.first.title, 'New');
  });

  test('entries TTL prunes rows older than 30d', () async {
    final d = dao();
    // 先写一条, cachedAt = now
    await d.upsertEntries('user1', [entry('old', 'f1')]);
    // 时间前进 31 天后再写一条新的 → prune 应删掉 old
    now = now.add(const Duration(days: 31));
    await d.upsertEntries('user1', [entry('fresh', 'f1')]);
    final all = await d.readEntries('user1');
    expect(all.map((e) => e.id).toList(), ['fresh']);
  });

  test('clearScope wipes both tables for that scope only', () async {
    final d = dao();
    await d.replaceFeeds('user1', [{'id': 'f1', 'title': 'One'}]);
    await d.upsertEntries('user1', [entry('e1', 'f1')]);
    await d.replaceFeeds('user2', [{'id': 'f9', 'title': 'Nine'}]);

    await d.clearScope('user1');
    expect(await d.readFeeds('user1'), isEmpty);
    expect(await d.readEntries('user1'), isEmpty);
    // user2 不受影响
    expect((await d.readFeeds('user2')).length, 1);
  });
}

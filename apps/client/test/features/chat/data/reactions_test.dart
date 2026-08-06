// ChatRepo reactions —— P0-1 单测。
// 设计文档 docs/BiuMind-Chat-UI-Benchmark-Optimization.md。

import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:biumind/data/local/db.dart';
import 'package:biumind/features/chat/data/chat_repo.dart';

void main() {
  late AppDb db;
  late ChatRepo repo;

  setUp(() {
    db = AppDb.memory();
    repo = ChatRepo(db);
  });

  tearDown(() async {
    await db.close();
  });

  test('toggleReaction adds row when none exists', () async {
    await repo.toggleReaction(messageId: 'm1', threadId: 't1', kind: 'like');
    final rows = await repo.watchReactionsForMessage('m1').first;
    expect(rows.length, 1);
    expect(rows.first.kind, 'like');
  });

  test('toggleReaction removes existing row of same kind', () async {
    await repo.toggleReaction(messageId: 'm1', threadId: 't1', kind: 'like');
    await repo.toggleReaction(messageId: 'm1', threadId: 't1', kind: 'like');
    final rows = await repo.watchReactionsForMessage('m1').first;
    expect(rows, isEmpty);
  });

  test('star coexists with like', () async {
    await repo.toggleReaction(messageId: 'm1', threadId: 't1', kind: 'like');
    await repo.toggleReaction(messageId: 'm1', threadId: 't1', kind: 'star');
    final kinds = (await repo.watchReactionsForMessage('m1').first)
        .map((r) => r.kind)
        .toSet();
    expect(kinds, {'like', 'star'});
  });

  test('watchStarredMessages returns only star rows newest first', () async {
    await repo.toggleReaction(messageId: 'm1', threadId: 't1', kind: 'like');
    await repo.toggleReaction(messageId: 'm2', threadId: 't1', kind: 'star');
    await repo.toggleReaction(messageId: 'm3', threadId: 't1', kind: 'star');
    final stars = await repo.watchStarredMessages().first;
    expect(stars.length, 2);
    expect(stars.map((r) => r.messageId).toSet(), {'m2', 'm3'});
  });

  test('clearReactionsForMessage wipes all rows for that message', () async {
    await repo.toggleReaction(messageId: 'm1', threadId: 't1', kind: 'like');
    await repo.toggleReaction(messageId: 'm1', threadId: 't1', kind: 'star');
    await repo.toggleReaction(messageId: 'm2', threadId: 't1', kind: 'star');
    await repo.clearReactionsForMessage('m1');
    final m1 = await repo.watchReactionsForMessage('m1').first;
    final m2 = await repo.watchReactionsForMessage('m2').first;
    expect(m1, isEmpty);
    expect(m2.length, 1);
  });
}

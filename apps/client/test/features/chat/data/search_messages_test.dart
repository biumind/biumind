// ChatRepo.searchMessages —— 跨会话搜索单测。

import 'package:drift/drift.dart' show Value;
import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:biumind/data/local/db.dart';
import 'package:biumind/features/chat/data/chat_repo.dart';
import 'package:biumind/features/chat/domain/chat_models.dart';

void main() {
  late AppDb db;
  late ChatRepo repo;

  setUp(() async {
    db = AppDb.memory();
    repo = ChatRepo(db);
    // Seed: 两个 thread 各一条 message + text block
    await repo.createThread(
        id: 't1', mode: ThreadMode.chat, title: 'Wiki design');
    await repo.createThread(
        id: 't2', mode: ThreadMode.chat, title: 'Cooking');
    final now = DateTime.utc(2026, 6, 1);
    await db.into(db.chatMessagesV2).insert(ChatMessagesV2Companion.insert(
          id: 'm1',
          threadId: 't1',
          role: 'assistant',
          status: 'completed',
          seq: 1,
          createdAt: now,
        ));
    await db.into(db.chatContentBlocks).insert(ChatContentBlocksCompanion.insert(
          id: 'b1',
          messageId: 'm1',
          blockIndex: 0,
          type: 'text',
          textContent: const Value('Vector database design pattern.'),
          createdAt: now,
          updatedAt: now,
        ));
    await db.into(db.chatMessagesV2).insert(ChatMessagesV2Companion.insert(
          id: 'm2',
          threadId: 't2',
          role: 'user',
          status: 'completed',
          seq: 1,
          createdAt: now.add(const Duration(seconds: 1)),
        ));
    await db.into(db.chatContentBlocks).insert(ChatContentBlocksCompanion.insert(
          id: 'b2',
          messageId: 'm2',
          blockIndex: 0,
          type: 'text',
          textContent: const Value('How to make pasta sauce?'),
          createdAt: now,
          updatedAt: now,
        ));
  });

  tearDown(() async {
    await db.close();
  });

  test('empty query returns no hits', () async {
    expect(await repo.searchMessages(query: ''), isEmpty);
    expect(await repo.searchMessages(query: '   '), isEmpty);
  });

  test('matches case-insensitive across threads', () async {
    final hits = await repo.searchMessages(query: 'DATABASE');
    expect(hits.length, 1);
    expect(hits.first.threadId, 't1');
    expect(hits.first.threadTitle, 'Wiki design');
  });

  test('returns multiple hits ordered by createdAt desc', () async {
    final hits = await repo.searchMessages(query: 'a');
    // Both messages contain 'a' (database, pasta) → 2 hits, newer first
    expect(hits.length, 2);
    expect(hits.first.threadId, 't2'); // newer
    expect(hits.last.threadId, 't1');
  });

  test('snippet contains the matched query', () async {
    final hits = await repo.searchMessages(query: 'pasta');
    expect(hits.length, 1);
    expect(hits.first.snippet.toLowerCase().contains('pasta'), true);
  });

  test('LIKE wildcard chars are escaped', () async {
    // 给一条带百分号的消息 —— 旧实现会把它当通配符
    final now = DateTime.utc(2026, 6, 2);
    await db.into(db.chatMessagesV2).insert(ChatMessagesV2Companion.insert(
          id: 'm3',
          threadId: 't1',
          role: 'user',
          status: 'completed',
          seq: 2,
          createdAt: now,
        ));
    await db.into(db.chatContentBlocks).insert(ChatContentBlocksCompanion.insert(
          id: 'b3',
          messageId: 'm3',
          blockIndex: 0,
          type: 'text',
          textContent: const Value('save 50% off coupon'),
          createdAt: now,
          updatedAt: now,
        ));
    // 搜 "50%" 应当只 match 含 50% 的那条
    final hits = await repo.searchMessages(query: '50%');
    expect(hits.length, 1);
    expect(hits.first.messageId, 'm3');
  });

  test('limit caps result count', () async {
    // 填 10 条更老消息
    final base = DateTime.utc(2025, 1, 1);
    for (var i = 0; i < 10; i++) {
      final t = base.add(Duration(seconds: i));
      await db.into(db.chatMessagesV2).insert(ChatMessagesV2Companion.insert(
            id: 'mx$i',
            threadId: 't1',
            role: 'user',
            status: 'completed',
            seq: 100 + i,
            createdAt: t,
          ));
      await db.into(db.chatContentBlocks).insert(
          ChatContentBlocksCompanion.insert(
            id: 'bx$i',
            messageId: 'mx$i',
            blockIndex: 0,
            type: 'text',
            textContent: const Value('repeated keyword foo'),
            createdAt: t,
            updatedAt: t,
          ));
    }
    final hits = await repo.searchMessages(query: 'foo', limit: 5);
    expect(hits.length, 5);
  });
}

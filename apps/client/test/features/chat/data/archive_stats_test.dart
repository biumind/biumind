// ChatRepo —— 归档管理 + 统计单测。

import 'package:drift/drift.dart' show Value;
import 'package:flutter_test/flutter_test.dart';

import 'package:biumind/data/local/db.dart';
import 'package:biumind/features/chat/data/chat_repo.dart';
import 'package:biumind/features/chat/domain/chat_models.dart';

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

  group('archive / unarchive', () {
    test('watchArchivedThreads returns only archived ones', () async {
      await repo.createThread(id: 't1', mode: ThreadMode.chat, title: 'a');
      await repo.createThread(id: 't2', mode: ThreadMode.chat, title: 'b');
      await repo.archiveThread('t2');
      final archived = await repo.watchArchivedThreads().first;
      expect(archived.length, 1);
      expect(archived.first.id, 't2');
    });

    test('unarchiveThread brings it back to main list', () async {
      await repo.createThread(id: 't1', mode: ThreadMode.chat, title: 'a');
      await repo.archiveThread('t1');
      expect((await repo.watchArchivedThreads().first).length, 1);
      await repo.unarchiveThread('t1');
      expect((await repo.watchArchivedThreads().first), isEmpty);
      // 恢复到主列表
      final main = await repo.watchThreads().first;
      expect(main.length, 1);
      expect(main.first.id, 't1');
    });
  });

  group('threadStats', () {
    test('empty db → 0/0', () async {
      final s = await repo.threadStats();
      expect(s.threadCount, 0);
      expect(s.messageCount, 0);
    });

    test('recentStats counts only completed messages within last N days', () async {
      await repo.createThread(id: 't1', mode: ThreadMode.chat);
      // 8 天前消息（不算）
      final old = DateTime.now().subtract(const Duration(days: 8));
      // 2 天前消息（算）
      final recent = DateTime.now().subtract(const Duration(days: 2));
      await db.into(db.chatMessagesV2).insert(ChatMessagesV2Companion.insert(
            id: 'mOld',
            threadId: 't1',
            role: 'user',
            status: 'completed',
            seq: 1,
            createdAt: old,
          ));
      await db.into(db.chatMessagesV2).insert(ChatMessagesV2Companion.insert(
            id: 'mRecent',
            threadId: 't1',
            role: 'user',
            status: 'completed',
            seq: 2,
            createdAt: recent,
          ));
      // streaming 不算
      await db.into(db.chatMessagesV2).insert(ChatMessagesV2Companion.insert(
            id: 'mStreaming',
            threadId: 't1',
            role: 'assistant',
            status: 'streaming',
            seq: 3,
            createdAt: recent,
          ));
      final r = await repo.recentStats(days: 7);
      expect(r.messages, 1);
      expect(r.activeThreads, 1);
      expect(r.days, 7);
    });

    test('recentModels returns DISTINCT model ordered by last_used desc',
        () async {
      await repo.createThread(id: 't1', mode: ThreadMode.chat);
      // 用 ChatMessagesV2Companion 直接插，控制 createdAt
      final base = DateTime.utc(2026, 6, 1);
      Future<void> insertMsg(
          String id, String? model, int seqOff) async {
        await db.into(db.chatMessagesV2).insert(
              ChatMessagesV2Companion.insert(
                id: id,
                threadId: 't1',
                role: 'assistant',
                status: 'completed',
                seq: seqOff,
                model: Value(model),
                createdAt: base.add(Duration(seconds: seqOff)),
              ),
            );
      }

      await insertMsg('m1', 'claude-opus-4-7', 1);
      await insertMsg('m2', 'gpt-4o', 2);
      await insertMsg('m3', 'claude-opus-4-7', 3); // 最近一次 claude
      await insertMsg('m4', null, 4); // 无 model 跳过
      await insertMsg('m5', '', 5); // 空 model 跳过

      final list = await repo.recentModels();
      expect(list.map((m) => m.code), ['claude-opus-4-7', 'gpt-4o']);
      // claude 最后用过的时间 = m3 的 createdAt
      expect(list.first.lastUsed.isAfter(list.last.lastUsed), true);
    });

    test('dailyStreak: 0 when no recent messages', () async {
      expect(await repo.dailyStreak(), 0);
    });

    test('dailyStreak counts consecutive days from today backward', () async {
      await repo.createThread(id: 't1', mode: ThreadMode.chat);
      // 在今天 / 昨天 / 前天 各放一条
      final now = DateTime.now();
      DateTime atDay(int daysAgo) =>
          DateTime(now.year, now.month, now.day, 12).subtract(Duration(days: daysAgo));
      Future<void> insertOn(int id, DateTime when) async {
        await db.into(db.chatMessagesV2).insert(ChatMessagesV2Companion.insert(
              id: 'm$id',
              threadId: 't1',
              role: 'user',
              status: 'completed',
              seq: id,
              createdAt: when,
            ));
      }

      await insertOn(1, atDay(0));
      await insertOn(2, atDay(1));
      await insertOn(3, atDay(2));
      expect(await repo.dailyStreak(), 3);
    });

    test('dailyStreak breaks on missing day', () async {
      await repo.createThread(id: 't1', mode: ThreadMode.chat);
      final now = DateTime.now();
      DateTime atDay(int daysAgo) =>
          DateTime(now.year, now.month, now.day, 12).subtract(Duration(days: daysAgo));
      // 今天有 + 前天有，但昨天没 → streak 断在今天，只算 1
      await db.into(db.chatMessagesV2).insert(ChatMessagesV2Companion.insert(
            id: 'm1',
            threadId: 't1',
            role: 'user',
            status: 'completed',
            seq: 1,
            createdAt: atDay(0),
          ));
      await db.into(db.chatMessagesV2).insert(ChatMessagesV2Companion.insert(
            id: 'm2',
            threadId: 't1',
            role: 'user',
            status: 'completed',
            seq: 2,
            createdAt: atDay(2),
          ));
      expect(await repo.dailyStreak(), 1);
    });

    test('counts threads (incl. archived) + completed messages only', () async {
      await repo.createThread(id: 't1', mode: ThreadMode.chat);
      await repo.createThread(id: 't2', mode: ThreadMode.chat);
      await repo.archiveThread('t2');
      // 2 messages: 1 completed, 1 streaming（不计）
      final now = DateTime.now();
      await db.into(db.chatMessagesV2).insert(ChatMessagesV2Companion.insert(
            id: 'm1',
            threadId: 't1',
            role: 'user',
            status: 'completed',
            seq: 1,
            createdAt: now,
            completedAt: Value(now),
          ));
      await db.into(db.chatMessagesV2).insert(ChatMessagesV2Companion.insert(
            id: 'm2',
            threadId: 't1',
            role: 'assistant',
            status: 'streaming',
            seq: 2,
            createdAt: now,
          ));
      final s = await repo.threadStats();
      expect(s.threadCount, 2); // 含 archived
      expect(s.messageCount, 1); // 只 completed
    });
  });
}

// Drift 迁移 v30 → v32 验证（P1.2 + P1.3）。
//
// 设计文档 docs/BiuMind-Local-Data-Isolation-Design.md §4：
//   - v31 加 chat_sync_state（per-scope 下行游标：threads_cursor /
//     tombstone_since）；
//   - v32 加 chat_outbox（上行写盒：scope 隔离的删除/归档/重命名重试队列）。
//
// 两张都是纯新表、无存量数据迁移 —— 本测试断言：v30 老库升上来后两表
// 存在且列齐全、存量 chat 行不受影响、新表可正常读写。
//
// 手搓旧库的原因见 migration_v22_to_v26_test.dart 头注释：AppDb.memory()
// 走 onCreate 永远跑不到 onUpgrade。这里只需建一张 v30 形态的
// chat_threads_v2（含 owner_key 列）种一行数据 —— from=30 时 onUpgrade
// 只跑 from<31 / from<32 两个 createTable，不碰其他表。

import 'package:biumind/data/local/db.dart';
import 'package:biumind/features/chat/data/chat_repo.dart';
import 'package:drift/native.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:sqlite3/sqlite3.dart';

void main() {
  test('v30 旧库迁移到 v32：chat_sync_state / chat_outbox 建表，存量行保留', () async {
    // ── 1. 手建 v30 形态的 chat_threads_v2（当前 schema）+ 种一行 ──
    final raw = sqlite3.openInMemory();
    raw.execute('''
      CREATE TABLE chat_threads_v2 (
        id TEXT NOT NULL PRIMARY KEY,
        title TEXT NOT NULL DEFAULT '',
        mode TEXT NOT NULL,
        environment_id TEXT,
        pool_tag TEXT,
        model TEXT,
        provider_id TEXT,
        system_prompt TEXT,
        project_id TEXT,
        workdir TEXT,
        auto_approve TEXT NOT NULL DEFAULT 'manual',
        runtime_env_mode TEXT NOT NULL DEFAULT 'none',
        backend TEXT NOT NULL DEFAULT 'biumindkit',
        pinned INTEGER NOT NULL DEFAULT 0,
        archived INTEGER NOT NULL DEFAULT 0,
        created_at INTEGER NOT NULL,
        updated_at INTEGER NOT NULL,
        remote_updated_at_us INTEGER,
        owner_key TEXT NOT NULL DEFAULT ''
      );
    ''');
    final ts = DateTime.utc(2026, 8, 1).millisecondsSinceEpoch ~/ 1000;
    raw.execute(
      "INSERT INTO chat_threads_v2 (id,title,mode,created_at,updated_at,owner_key) "
      "VALUES ('th1','旧会话','chat',$ts,$ts,'env-hash:user-1')",
    );
    raw.userVersion = 30;

    // ── 2. 同一句柄交给 drift，首次查询触发 onUpgrade(30→32) ──
    final db = AppDb.executor(NativeDatabase.opened(raw));
    addTearDown(db.close);
    await db.customSelect('SELECT 1').get();

    // ── 3. 断言 ──
    expect(raw.userVersion, 32, reason: '迁移后 schema 版本应为 32');

    final syncCols = raw
        .select('PRAGMA table_info(chat_sync_state)')
        .map((r) => r['name'] as String)
        .toSet();
    expect(syncCols, {'scope', 'threads_cursor', 'tombstone_since'},
        reason: 'v31 应建 chat_sync_state');

    final outboxCols = raw
        .select('PRAGMA table_info(chat_outbox)')
        .map((r) => r['name'] as String)
        .toSet();
    expect(outboxCols, {
      'id',
      'scope',
      'op',
      'thread_id',
      'payload_json',
      'attempts',
      'last_error',
      'created_at',
      'next_attempt_at',
    }, reason: 'v32 应建 chat_outbox');

    // 存量行不受影响（纯新表迁移）。
    final repo = ChatRepo(db, scope: 'env-hash:user-1');
    final threads = await repo.listAllThreads();
    expect(threads.map((t) => t.id), ['th1']);

    // 新表可读写（drift 生成代码与迁移建表 schema 一致）。
    await repo.saveChatSyncState(
      threadsCursor: '2026-08-01T00:00:00Z',
      tombstoneSince: null,
    );
    final state = await repo.chatSyncState();
    expect(state!.threadsCursor, '2026-08-01T00:00:00Z');
    expect(state.tombstoneSince, isNull);

    await repo.enqueueOutbox(
      op: ChatRepo.outboxOpDeleteThread,
      threadId: 'th1',
    );
    expect(
      await repo.dueChatOutbox(now: DateTime.utc(2100)),
      hasLength(1),
    );
  });
}

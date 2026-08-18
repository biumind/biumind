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
// 走 onCreate 永远跑不到 onUpgrade。这里需建一张 v30 形态的
// chat_threads_v2（含 owner_key 列）种一行数据，外加真实 v30 库必有的
// notes 五表（v28 建，无 owner_key；最小骨架即可）与 sse_cursors（v17 建）
// —— 迁移链会跑到 v34：v33 对 note_* 做 addColumn + DELETE，v34 DELETE
// sse_cursors，fixture 缺了会报 no such table。from=30 时 v31/v32 两个
// createTable 不碰其他表。

import 'package:biumind/data/local/db.dart';
import 'package:biumind/features/chat/data/chat_repo.dart';
import 'package:drift/native.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:sqlite3/sqlite3.dart';

void main() {
  test('v30 旧库迁移到 v34：chat_sync_state / chat_outbox 建表，存量行保留', () async {
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
    // 真实 v30 库必有的 notes 五表（v28 建，无 owner_key；v33 才补列）。
    const noteTables = [
      'note_notes',
      'note_notebooks',
      'note_tags',
      'note_note_tags',
      'note_outbox',
    ];
    for (final t in noteTables) {
      raw.execute('CREATE TABLE $t (id TEXT NOT NULL PRIMARY KEY)');
      raw.execute("INSERT INTO $t (id) VALUES ('stale-$t')");
    }
    // 真实 v30 库必有 sse_cursors（v17 建）—— v34 会 DELETE 它（scope 升级
    // 'ownerKey:topic'），fixture 缺了会 no such table。
    raw.execute('''
      CREATE TABLE sse_cursors (
        scope TEXT NOT NULL PRIMARY KEY,
        last_event_id TEXT NOT NULL,
        updated_at INTEGER NOT NULL
      );
    ''');
    raw.execute(
      "INSERT INTO sse_cursors (scope,last_event_id,updated_at) "
      "VALUES ('notes.changes','42',1780000000)",
    );
    final ts = DateTime.utc(2026, 8, 1).millisecondsSinceEpoch ~/ 1000;
    raw.execute(
      "INSERT INTO chat_threads_v2 (id,title,mode,created_at,updated_at,owner_key) "
      "VALUES ('th1','旧会话','chat',$ts,$ts,'env-hash:user-1')",
    );
    raw.userVersion = 30;

    // ── 2. 同一句柄交给 drift，首次查询触发 onUpgrade(30→33) ──
    final db = AppDb.executor(NativeDatabase.opened(raw));
    addTearDown(db.close);
    await db.customSelect('SELECT 1').get();

    // ── 3. 断言 ──
    expect(raw.userVersion, 36, reason: '迁移后 schema 版本应为 36（v33 笔记五表加 owner_key，v34 清 sse_cursors）');

    // v34:sse_cursors 旧的裸 topic 行已清。
    expect(
      raw.select('SELECT COUNT(*) AS c FROM sse_cursors').first['c'],
      0,
      reason: 'v34 应清空 sse_cursors 存量行',
    );

    // v33:notes 五表补 owner_key 并清空存量。
    for (final t in noteTables) {
      final cols = raw
          .select('PRAGMA table_info($t)')
          .map((r) => r['name'] as String)
          .toSet();
      expect(cols, contains('owner_key'), reason: 'v33 应给 $t 加 owner_key 列');
      expect(
        raw.select('SELECT COUNT(*) AS c FROM $t').first['c'],
        0,
        reason: 'v33 应清空 $t 存量行',
      );
    }

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

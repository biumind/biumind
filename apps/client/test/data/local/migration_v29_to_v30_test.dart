// Drift 迁移 v29 → v30 验证（P0 本地数据隔离）。
//
// 设计文档 docs/BiuMind-Local-Data-Isolation-Design.md §3.1：
//   - chat 五表各加 owner_key TEXT NOT NULL DEFAULT ''；
//   - migration 内清空五表全部存量行 —— 存量行归属不明（本身就是跨账号
//     泄露源），禁止「猜归属」；服务端有权威副本，清空后全量 hydrate 恢复。
//
// 手搓旧库的原因见 migration_v22_to_v26_test.dart 头注释：AppDb.memory()
// 走 onCreate 永远跑不到 onUpgrade。本测试用 package:sqlite3 手建 v29
// 形态的五张表 + 种数据，经 NativeDatabase.opened 触发 onUpgrade(29→当前)，
// 断言：升到当前版本、五表清空、owner_key 列存在、新写入的行带 ownerKey。
//
// 注意：真实 v29 库必有 notes 五表（v28 createTable 建，v29 加
// archived_at/promoted_page_id，均无 owner_key）—— 迁移链跑到 v33 时会
// 对 note_* 做 addColumn + DELETE，fixture 缺了会报 no such table。这里
// 用最小骨架（同 migration_v32_to_v33_test：Phase 33 的 addColumn/DELETE
// 不依赖其他列形态），各种一行脏数据验证清空。

import 'package:biumind/data/local/db.dart';
import 'package:biumind/features/chat/data/chat_repo.dart';
import 'package:biumind/features/chat/domain/chat_models.dart';
import 'package:drift/native.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:sqlite3/sqlite3.dart';

void main() {
  test('v29 旧库迁移到 v30：五表加 owner_key 列且存量行全部清空', () async {
    // ── 1. 手建 v29 形态的五张 chat 表（= 当前 schema 减 owner_key 列）──
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
        remote_updated_at_us INTEGER
      );
    ''');
    raw.execute('''
      CREATE TABLE chat_messages_v2 (
        id TEXT NOT NULL PRIMARY KEY,
        thread_id TEXT NOT NULL,
        role TEXT NOT NULL,
        status TEXT NOT NULL,
        session_id TEXT,
        stop_reason TEXT,
        model TEXT,
        input_tokens INTEGER,
        output_tokens INTEGER,
        seq INTEGER NOT NULL,
        error_message TEXT,
        created_at INTEGER NOT NULL,
        completed_at INTEGER
      );
    ''');
    raw.execute('''
      CREATE TABLE chat_content_blocks (
        id TEXT NOT NULL PRIMARY KEY,
        message_id TEXT NOT NULL,
        block_index INTEGER NOT NULL,
        type TEXT NOT NULL,
        text_content TEXT,
        tool_use_id TEXT,
        tool_use_name TEXT,
        tool_use_input_json TEXT,
        tool_result_id TEXT,
        tool_result_is_error INTEGER,
        tool_result_content_json TEXT,
        image_mime_type TEXT,
        image_data TEXT,
        state TEXT NOT NULL DEFAULT 'closed',
        created_at INTEGER NOT NULL,
        updated_at INTEGER NOT NULL
      );
    ''');
    raw.execute('''
      CREATE TABLE chat_sessions (
        session_id TEXT NOT NULL PRIMARY KEY,
        thread_id TEXT NOT NULL,
        mode TEXT NOT NULL,
        session_token TEXT NOT NULL,
        token_expires_at INTEGER NOT NULL,
        last_seen_seq INTEGER NOT NULL DEFAULT 0,
        status TEXT NOT NULL,
        created_at INTEGER NOT NULL,
        closed_at INTEGER
      );
    ''');
    raw.execute('''
      CREATE TABLE message_reactions_v2 (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        message_id TEXT NOT NULL,
        thread_id TEXT NOT NULL,
        kind TEXT NOT NULL,
        created_at INTEGER NOT NULL
      );
    ''');

    // 真实 v29 库必有的 notes 五表（v28 建、v29 补列，均无 owner_key）——
    // 最小骨架即可（Phase 33 的 addColumn/DELETE 不依赖其他列形态，
    // 同 migration_v32_to_v33_test 的论证）。
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
    // 真实 v29 库必有 sse_cursors（v17 建）—— v34 会 DELETE 它（scope 升级
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
      "VALUES ('aigc.tasks','evt-1',1780000000)",
    );

    // 种数据：五表各 1 行 —— 模拟真实旧库里的存量聊天记录。
    final ts = DateTime.utc(2026, 8, 1).millisecondsSinceEpoch ~/ 1000;
    raw.execute(
      "INSERT INTO chat_threads_v2 (id,title,mode,created_at,updated_at) "
      "VALUES ('th1','旧会话','chat',$ts,$ts)",
    );
    raw.execute(
      "INSERT INTO chat_messages_v2 (id,thread_id,role,status,seq,created_at) "
      "VALUES ('m1','th1','user','completed',1,$ts)",
    );
    raw.execute(
      "INSERT INTO chat_content_blocks (id,message_id,block_index,type,text_content,created_at,updated_at) "
      "VALUES ('b1','m1',0,'text','hello',$ts,$ts)",
    );
    raw.execute(
      "INSERT INTO chat_sessions (session_id,thread_id,mode,session_token,token_expires_at,status,created_at) "
      "VALUES ('s1','th1','chat','tok',$ts,'completed',$ts)",
    );
    raw.execute(
      "INSERT INTO message_reactions_v2 (message_id,thread_id,kind,created_at) "
      "VALUES ('m1','th1','star',$ts)",
    );

    raw.userVersion = 29;

    // ── 2. 同一句柄交给 drift，首次查询触发 onUpgrade(29→30) ──
    final db = AppDb.executor(NativeDatabase.opened(raw));
    addTearDown(db.close);

    // 先做一次 drift 查询触发 ensureOpen → onUpgrade（drift 惰性打开，
    // 直接读 raw.userVersion 时迁移可能还没跑）。
    await db.customSelect('SELECT 1').get();

    // ── 3. 断言 ──
    expect(raw.userVersion, 36, reason: '迁移后 schema 版本应为 36（v31/v32 加 chat_sync_state / chat_outbox，v33 笔记五表加 owner_key，v34 清 sse_cursors）');

    // v34:sse_cursors 旧的裸 topic 行已清。
    expect(
      raw.select('SELECT COUNT(*) AS c FROM sse_cursors').first['c'],
      0,
      reason: 'v34 应清空 sse_cursors 存量行',
    );

    // v33:notes 五表 owner_key 列已加、存量行清空（from>=28 走 addColumn）。
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

    // 五表 owner_key 列已加。
    for (final t in [
      'chat_threads_v2',
      'chat_messages_v2',
      'chat_content_blocks',
      'chat_sessions',
      'message_reactions_v2',
    ]) {
      final cols = raw
          .select('PRAGMA table_info($t)')
          .map((r) => r['name'] as String)
          .toSet();
      expect(cols, contains('owner_key'), reason: 'v30 应给 $t 加 owner_key 列');
    }

    // 五表存量行全部清空（归属不明 = 泄露源；服务端有权威副本，全量
    // hydrate 无感恢复 —— 禁止猜归属）。
    final repo = ChatRepo(db, scope: 'env-hash:user-1');
    expect(await repo.listAllThreads(), isEmpty, reason: 'v30 应清空 chat_threads_v2');
    expect(
      raw.select('SELECT COUNT(*) AS c FROM chat_messages_v2').first['c'],
      0,
      reason: 'v30 应清空 chat_messages_v2',
    );
    expect(
      raw.select('SELECT COUNT(*) AS c FROM chat_content_blocks').first['c'],
      0,
      reason: 'v30 应清空 chat_content_blocks',
    );
    expect(
      raw.select('SELECT COUNT(*) AS c FROM chat_sessions').first['c'],
      0,
      reason: 'v30 应清空 chat_sessions',
    );
    expect(
      raw.select('SELECT COUNT(*) AS c FROM message_reactions_v2').first['c'],
      0,
      reason: 'v30 应清空 message_reactions_v2',
    );

    // 迁移后新写入的行带 ownerKey。
    await repo.createThread(id: 'th2', mode: ThreadMode.chat, title: '新会话');
    final row = raw
        .select("SELECT owner_key FROM chat_threads_v2 WHERE id = 'th2'")
        .first;
    expect(row['owner_key'], 'env-hash:user-1', reason: '新写入行必须带当前 scope');
  });
}

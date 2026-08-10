// Drift 迁移链 v22 → v27 验证 (P0-a / CORE-2 / 多端聊天同步 P0)。
//
// 为什么必须手搓旧库:生成的 db.g.dart 只懂当前 schema,AppDb.memory()
// 走 onCreate 直接建到当前版本,**永远跑不到 onUpgrade**。真机上的旧库(v22-v25)
// 升级时才触发迁移链 —— 单测覆盖不到,而迁移失败会被 tasks_controller 的
// _hydrate catch 静默吞掉(退内存模式 + 丢历史任务),症状极隐蔽。
//
// 本测试用 package:sqlite3 手建 v22 形态的库 + 种数据,经 NativeDatabase.opened
// 把同一句柄交给 drift 触发 onUpgrade(22→当前),断言迁移链都跑通且数据不丢:
//   v23: code_tasks 加 model 列
//   v24: DROP code_task_outbox / code_sync_cursors(codeSync 废弃)
//   v25: code_task_artifacts DROP COLUMN cloud_file_id / cloud_uploaded_at
//   v26: code_tasks 加 starred 列(默认 false,CORE-2 任务星标)
//   v27: chat_threads_v2 加 remote_updated_at_us 列(多端聊天同步精确比较基准)
//   v28: 建笔记域 5 表(note_notes 含当前全部列,含 v29 的 archived_at/
//        promoted_page_id —— v29 的 addColumn 只对 from>=28 的老库执行)
//   v29: note_notes 加 archived_at / promoted_page_id（仅 from>=28 老库补列）
//   v30: chat 五表加 owner_key 并**刻意清空存量**（P0 数据隔离 —— 归属不明
//        的存量行是跨账号泄露源；服务端有权威副本，清空后全量 hydrate 恢复。
//        详细断言见 migration_v29_to_v30_test.dart）
//
// 注:v25 的 DROP COLUMN 需 SQLite ≥3.35。host `flutter test` 用系统
// libsqlite3(macOS/Ubuntu 均 ≥3.37),真机用 sqlite3_flutter_libs 打包的更新
// 版本 —— 二者都满足,故本测试能代表真机行为。

import 'package:biumind/data/local/db.dart';
import 'package:drift/native.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:sqlite3/sqlite3.dart';

void main() {
  test(
    'v22 旧库迁移到 v30:加 model/starred/remote_updated_at_us/owner_key 列 / DROP 同步表 / DROP 云字段,非 chat 数据保留(chat 存量由 v30 刻意清空)',
    () async {
      // ── 1. 手建 v22 形态的库(drift snake_case 列名,匹配生成的 mapper)──
      final raw = sqlite3.openInMemory();

      // v22 的 code_tasks:全部当前列 **除了 model**(model 是 v23 才加的)。
      raw.execute('''
      CREATE TABLE code_tasks (
        id TEXT NOT NULL PRIMARY KEY,
        title TEXT NOT NULL,
        prompt TEXT NOT NULL,
        agent TEXT NOT NULL,
        mode TEXT NOT NULL,
        status TEXT NOT NULL,
        events_json TEXT NOT NULL DEFAULT '[]',
        cost_usd REAL NOT NULL DEFAULT 0.0,
        input_tokens INTEGER NOT NULL DEFAULT 0,
        output_tokens INTEGER NOT NULL DEFAULT 0,
        created_at INTEGER NOT NULL,
        completed_at INTEGER,
        error_message TEXT,
        workspace_json TEXT,
        compare_group_id TEXT,
        origin_device_id TEXT,
        origin_device_label TEXT,
        project_id TEXT,
        updated_at INTEGER
      );
    ''');

      // v22 的 code_task_artifacts:全部当前列 **外加** 已废弃的云上传两列
      // (cloud_file_id / cloud_uploaded_at 是 v25 才 DROP 的)。
      raw.execute('''
      CREATE TABLE code_task_artifacts (
        id TEXT NOT NULL PRIMARY KEY,
        task_id TEXT NOT NULL,
        kind TEXT NOT NULL,
        rel_path TEXT NOT NULL,
        mime_type TEXT,
        size_bytes INTEGER NOT NULL DEFAULT 0,
        sha256 TEXT NOT NULL,
        op TEXT NOT NULL,
        preview_summary TEXT,
        preview_data_b64 TEXT,
        preview_mime_type TEXT,
        cloud_file_id TEXT,
        cloud_uploaded_at INTEGER,
        created_at INTEGER NOT NULL
      );
    ''');

      // 已废弃的 codeSync 表(v4 建,v24 DROP)—— 建出来确认迁移真把它们删掉。
      raw.execute('''
      CREATE TABLE code_task_outbox (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        op TEXT NOT NULL,
        payload_json TEXT NOT NULL DEFAULT '{}',
        created_at INTEGER NOT NULL
      );
    ''');
      raw.execute('''
      CREATE TABLE code_sync_cursors (
        scope TEXT NOT NULL PRIMARY KEY,
        cursor TEXT NOT NULL,
        updated_at INTEGER NOT NULL
      );
    ''');

      // v22 的 chat_threads_v2:v10 建表、v11-v19 陆续加列后的形态 —— 比当前
      // schema 少 remote_updated_at_us(v27 才加)。真实旧库必有此表,fixture
      // 不能缺,否则 v27 的 addColumn 会报 no such table。
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
        updated_at INTEGER NOT NULL
      );
    ''');

      // v22 真实旧库同样必有：chat_messages_v2 / chat_content_blocks /
      // chat_sessions（v10 建）、message_reactions_v2（v13 建）。v30 的
      // owner_key addColumn 对这四张表同样执行，fixture 缺了会
      // no such table。
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

      // 种数据:1 个历史任务 + 1 个带云字段的产物 + 同步表各 1 行。
      final ts = DateTime.utc(2026, 5, 1).millisecondsSinceEpoch ~/ 1000;
      raw.execute(
        "INSERT INTO code_tasks (id,title,prompt,agent,mode,status,created_at) "
        "VALUES ('t1','旧任务','do x','claudeCode','ask','done',$ts)",
      );
      raw.execute(
        "INSERT INTO code_task_artifacts "
        "(id,task_id,kind,rel_path,sha256,op,cloud_file_id,cloud_uploaded_at,created_at) "
        "VALUES ('a1','t1','codeFile','lib/main.dart','deadbeef','modified','cas:old',$ts,$ts)",
      );
      raw.execute(
        "INSERT INTO code_task_outbox (op,created_at) VALUES ('task_upsert',$ts)",
      );
      raw.execute(
        "INSERT INTO code_sync_cursors (scope,cursor,updated_at) "
        "VALUES ('code.tasks','c1',$ts)",
      );
      raw.execute(
        "INSERT INTO chat_threads_v2 (id,title,mode,created_at,updated_at) "
        "VALUES ('th1','旧会话','chat',$ts,$ts)",
      );

      raw.userVersion = 22;

      // ── 2. 把同一句柄交给 drift,首次查询触发 ensureOpen → onUpgrade(22→28)──
      final db = AppDb.executor(NativeDatabase.opened(raw));
      addTearDown(db.close);

      final tasks = await db.select(db.codeTasks).get();

      // ── 3. 断言 ──
      // 迁移真的跑到了当前最新版本（v30：v28 建笔记域 5 表；v29 加
      // note_notes.archived_at/promoted_page_id —— from<28 时 createTable
      // 已含新列，v29 步骤跳过；v30 chat 五表加 owner_key 并清空存量；
      // v31/v32 加 chat_sync_state / chat_outbox）。
      expect(raw.userVersion, 32, reason: '迁移后 schema 版本应为 32');

      // v28:笔记域 5 张表已建（note_notebooks/note_notes/note_tags/
      // note_note_tags/note_outbox）。
      final noteTables = raw
          .select("SELECT name FROM sqlite_master WHERE type='table'")
          .map((r) => r['name'] as String)
          .toSet();
      for (final t in [
        'note_notebooks',
        'note_notes',
        'note_tags',
        'note_note_tags',
        'note_outbox',
      ]) {
        expect(noteTables, contains(t), reason: 'v28 应建表 $t');
      }

      // v29:note_notes 含归档/转知识库两列（from<28 走 createTable 全列路径）。
      final noteCols = raw
          .select('PRAGMA table_info(note_notes)')
          .map((r) => r['name'] as String)
          .toSet();
      expect(noteCols, contains('archived_at'));
      expect(noteCols, contains('promoted_page_id'));

      // v23:model 列已加;旧行该列为 null;其余字段完整保留。
      expect(tasks, hasLength(1), reason: '历史任务不应在迁移中丢失');
      expect(tasks.first.id, 't1');
      expect(tasks.first.title, '旧任务');
      expect(tasks.first.model, isNull, reason: '旧任务无 model,迁移后应为 null');
      // v26:starred 列已加;旧行取默认 false。
      expect(
        tasks.first.starred,
        isFalse,
        reason: '旧任务无 starred,迁移后应为默认 false',
      );

      // v24:codeSync 同步表已 DROP。
      final tables = raw
          .select("SELECT name FROM sqlite_master WHERE type='table'")
          .map((r) => r['name'] as String)
          .toSet();
      expect(tables, isNot(contains('code_task_outbox')));
      expect(tables, isNot(contains('code_sync_cursors')));

      // v25:产物云字段列已 DROP;产物行本身保留。
      final artCols = raw
          .select('PRAGMA table_info(code_task_artifacts)')
          .map((r) => r['name'] as String)
          .toSet();
      expect(artCols, isNot(contains('cloud_file_id')));
      expect(artCols, isNot(contains('cloud_uploaded_at')));

      final arts = await db.select(db.codeTaskArtifacts).get();
      expect(arts, hasLength(1), reason: '产物元数据不应在 DROP COLUMN 时丢失');
      expect(arts.first.sha256, 'deadbeef');
      expect(arts.first.relPath, 'lib/main.dart');

      // v27:chat_threads_v2 已加 remote_updated_at_us。
      final threadCols = raw
          .select('PRAGMA table_info(chat_threads_v2)')
          .map((r) => r['name'] as String)
          .toSet();
      expect(threadCols, contains('remote_updated_at_us'));

      // v30:owner_key 列已加；存量 chat 行被**刻意清空**（P0 数据隔离 ——
      // 归属不明的存量是泄露源，服务端有权威副本，清空后全量 hydrate 恢复；
      // 详细断言见 migration_v29_to_v30_test.dart）。这里仅验证老链路
      // （v22 起）升到 v30 时清空步骤同样生效。
      expect(threadCols, contains('owner_key'));
      final threads = await db.select(db.chatThreadsV2).get();
      expect(threads, isEmpty, reason: 'v30 刻意清空存量无归属 chat 行');
    },
  );
}

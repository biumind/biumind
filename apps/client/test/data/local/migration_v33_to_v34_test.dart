// Drift 迁移 v33 → v34 验证（P2 多账号 Step 2: SseCursors scope 升级）。
//
// v34 前 sse_cursors.scope 是裸 topic（'chat.sync' / 'aigc.tasks' /
// 'notes.changes'），不含账号身份 —— 「不登出直接 switchAccount」时新账号
// 的 RealtimeHub / NotesSyncPoller 会拿上一个账号的 last-event-id 续接。
// v34 起 scope = 'ownerKey:topic'（ownerKey = sha256(环境)+':'+JWT sub，与
// chat/notes 本地数据同一把隔离键）。旧行没有归属信息，且 cursor 拿错的
// 代价只是重连后从头收（服务端 replay 纠偏），故迁移直接清存量行。
//
// 手建旧库（不走 onCreate）的原因见 migration_v22_to_v26_test.dart 头注释。
// v34 只有一句 DELETE FROM sse_cursors，不依赖其他表形态。

import 'package:biumind/data/local/db.dart';
import 'package:biumind/data/sse/sse_cursors_dao.dart';
import 'package:drift/native.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:sqlite3/sqlite3.dart';

void main() {
  test('v33 旧库迁移到 v34：sse_cursors 裸 topic 存量行全部清空', () async {
    // ── 1. 手建 v33 形态的 sse_cursors（v17 起 schema 未变）+ 三条旧形态行 ──
    final raw = sqlite3.openInMemory();
    raw.execute('''
      CREATE TABLE sse_cursors (
        scope TEXT NOT NULL PRIMARY KEY,
        last_event_id TEXT NOT NULL,
        updated_at INTEGER NOT NULL
      );
    ''');
    for (final scope in ['chat.sync', 'aigc.tasks', 'notes.changes']) {
      raw.execute(
        "INSERT INTO sse_cursors (scope,last_event_id,updated_at) "
        "VALUES ('$scope','evt-1',1780000000)",
      );
    }
    // v35 给 note_notes 加 base_content_md / base_version 两列（3-way merge
    // 共同祖先）—— fixture 必须先有此表，否则 v35 addColumn 报 no such table。
    raw.execute('CREATE TABLE note_notes (id TEXT NOT NULL PRIMARY KEY)');
    // v36 给 note_notebooks 加 parent_id（多级目录）—— 同上，先补桩表。
    raw.execute('CREATE TABLE note_notebooks (id TEXT NOT NULL PRIMARY KEY)');
    raw.userVersion = 33;

    // ── 2. 同一句柄交给 drift，首次查询触发 onUpgrade(33→34) ──
    final db = AppDb.executor(NativeDatabase.opened(raw));
    addTearDown(db.close);
    await db.customSelect('SELECT 1').get();

    // ── 3. 断言 ──
    expect(raw.userVersion, 36, reason: '迁移后 schema 版本应为 36');
    expect(
      raw.select('SELECT COUNT(*) AS c FROM sse_cursors').first['c'],
      0,
      reason: 'v34 应清空全部裸 topic 存量行（归属不明，禁止猜归属）',
    );

    // 迁移后表可正常读写新形态 scope（drift 生成代码与表 schema 一致）。
    final dao = SseCursorsDao(db);
    await dao.save('hash-abcd:user-1:chat.sync', 'evt-9');
    expect(await dao.load('hash-abcd:user-1:chat.sync'), 'evt-9');
    expect(
      await dao.load('chat.sync'),
      isNull,
      reason: '裸 topic 与 ownerKey 前缀 scope 是不同行',
    );
  });
}

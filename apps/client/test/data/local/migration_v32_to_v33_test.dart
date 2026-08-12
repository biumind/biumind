// Drift 迁移 v32 → v33 验证（笔记域 P0 本地数据隔离）。
//
// 设计文档 docs/BiuMind-Local-Data-Isolation-Design.md §2/§3：笔记五表此前无
// 任何用户隔离（chat 在 Phase 30 已修，笔记被漏掉），重新部署 + 重新注册登录
// 后，桌面端会把上一账号 / 上一套部署的笔记直接展示给新账号（跨账号泄露）。
// Phase 33 给 note_notes / note_notebooks / note_tags / note_note_tags /
// note_outbox 五表加 owner_key scope 列，并清空全部存量行（存量无归属信息，
// 本身就是泄露源，禁止「猜归属」；服务端有权威副本，清空后下次 hydrate 无感恢复）。
//
// 本测试断言：v32 老库升到 v33 后，五表都多出 owner_key 列，且存量行被清空。
// 这正是报告的 bug（重新部署后桌面端看到上一账号笔记）的修复点。
//
// 手建旧库（不走 onCreate）的原因见 migration_v22_to_v26_test.dart 头注释：
// AppDb.memory() 走 onCreate 永远跑不到 onUpgrade。Phase 33 的 addColumn +
// DELETE 不依赖其他列形态，故只建五表的最小骨架（含主键）种一行，断言用 raw
// SQL（不走 Drift 类型查询，故无需完整复刻 schema）。

import 'package:biumind/data/local/db.dart';
import 'package:drift/native.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:sqlite3/sqlite3.dart';

void main() {
  // Phase 33 涉及的五张表（与 db.dart migration 顺序一致）。
  const tables = <String>[
    'note_notes',
    'note_notebooks',
    'note_tags',
    'note_note_tags',
    'note_outbox',
  ];

  test('v32 旧库迁移到 v34：笔记五表加 owner_key 列，存量行清空', () async {
    // ── 1. 手建 v32 形态的五表（均无 owner_key）+ 各种一行脏数据 ──
    final raw = sqlite3.openInMemory();
    for (final t in tables) {
      raw.execute('CREATE TABLE $t (id TEXT NOT NULL PRIMARY KEY)');
      raw.execute("INSERT INTO $t (id) VALUES ('stale-$t')");
    }
    // 真实 v32 库必有 sse_cursors（v17 建）—— v34 会 DELETE 它（scope 升级
    // 'ownerKey:topic'），fixture 缺了会 no such table。种一条旧形态脏数据。
    raw.execute('''
      CREATE TABLE sse_cursors (
        scope TEXT NOT NULL PRIMARY KEY,
        last_event_id TEXT NOT NULL,
        updated_at INTEGER NOT NULL
      );
    ''');
    raw.execute(
      "INSERT INTO sse_cursors (scope,last_event_id,updated_at) "
      "VALUES ('chat.sync','evt-1',1780000000)",
    );
    raw.userVersion = 32;

    // ── 2. 同一句柄交给 drift，首次查询触发 onUpgrade(32→34) ──
    final db = AppDb.executor(NativeDatabase.opened(raw));
    addTearDown(db.close);
    await db.customSelect('SELECT 1').get();

    // ── 3. 断言 ──
    expect(raw.userVersion, 35, reason: '迁移后 schema 版本应为 35');

    // v34:sse_cursors 旧的裸 topic 行已清（详细断言见
    // migration_v33_to_v34_test.dart）。
    expect(
      raw.select('SELECT COUNT(*) AS c FROM sse_cursors').first['c'],
      0,
      reason: 'v34 应清空 sse_cursors 存量行',
    );

    for (final t in tables) {
      final cols = raw
          .select('PRAGMA table_info($t)')
          .map((r) => r['name'] as String)
          .toSet();
      expect(
        cols,
        contains('owner_key'),
        reason: '$t 应加 owner_key scope 列（Phase 33 addColumn）',
      );

      final count =
          raw.select('SELECT COUNT(*) AS c FROM $t').first['c'] as int;
      expect(
        count,
        0,
        reason: '$t 存量行应被 Phase 33 DELETE 清空（杜绝跨账号泄露源）',
      );
    }
  });
}

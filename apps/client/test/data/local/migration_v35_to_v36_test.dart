// Drift 迁移 v35 → v36 验证（笔记本多级目录 parent_id）。
//
// 服务端 brain migration 00003 给 note_notebooks 加了 parent_id（多级目录，
// 上限 5 层）；客户端本地镜像 Phase 36 同步加可空 parent_id 列（服务端
// uuid 或本地 'local-' 占位 id），null = 根级。平铺存储，树由 UI 组装。
//
// 本测试断言：v35 老库升到 v36 后 note_notebooks 多出 parent_id 列，
// 存量行 parent_id 为 null（等价全部升根，行为与升级前一致），新行可
// 正常带 parent_id 写入。
//
// 手建旧库（不走 onCreate）的原因见 migration_v22_to_v26_test.dart 头注释：
// AppDb.memory() 走 onCreate 永远跑不到 onUpgrade。v36 只动
// note_notebooks 一张表，fixture 只需它的 v35 形态骨架。

import 'package:biumind/data/local/db.dart';
import 'package:drift/native.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:sqlite3/sqlite3.dart';

void main() {
  test('v35 旧库迁移到 v36：note_notebooks 加 parent_id 列，存量行为 null', () async {
    // ── 1. 手建 v35 形态的 note_notebooks（无 parent_id）+ 一行存量 ──
    final raw = sqlite3.openInMemory();
    raw.execute('''
      CREATE TABLE note_notebooks (
        id TEXT NOT NULL PRIMARY KEY,
        name TEXT NOT NULL,
        position REAL NOT NULL DEFAULT 0.0,
        updated_at INTEGER NOT NULL,
        owner_key TEXT NOT NULL DEFAULT ''
      );
    ''');
    raw.execute(
      "INSERT INTO note_notebooks (id, name, position, updated_at, owner_key) "
      "VALUES ('nb-old', '旧本子', 0.0, 1780000000, 'scope')",
    );
    raw.userVersion = 35;

    // ── 2. 同一句柄交给 drift，首次查询触发 onUpgrade(35→36) ──
    final db = AppDb.executor(NativeDatabase.opened(raw));
    addTearDown(db.close);
    await db.customSelect('SELECT 1').get();

    // ── 3. 断言 ──
    expect(raw.userVersion, 36, reason: '迁移后 schema 版本应为 36');

    final cols = raw
        .select('PRAGMA table_info(note_notebooks)')
        .map((r) => r['name'] as String)
        .toSet();
    expect(
      cols,
      contains('parent_id'),
      reason: 'note_notebooks 应加 parent_id 列（Phase 36 addColumn）',
    );

    final oldRow =
        raw.select("SELECT parent_id FROM note_notebooks WHERE id = 'nb-old'").first;
    expect(
      oldRow['parent_id'],
      isNull,
      reason: '存量行 parent_id 应为 null（升级后等价根级，行为不变）',
    );

    // 新行可带子父关系写入（走 Drift 类型 API，验证生成代码与 schema 一致）。
    final dao = db.noteNotebooks;
    await db.into(dao).insert(LocalNoteNotebook(
          id: 'nb-child',
          name: '子本子',
          parentId: 'nb-old',
          position: 0.0,
          ownerKey: 'scope',
          updatedAt: DateTime.fromMillisecondsSinceEpoch(1780000000 * 1000, isUtc: true),
        ));
    final child = await (db.select(dao)
          ..where((t) => t.id.equals('nb-child')))
        .getSingle();
    expect(child.parentId, 'nb-old');
  });
}

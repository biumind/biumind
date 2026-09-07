// Drift 迁移 v36 → v37 验证（表单沉淀消息流 P3-b form_payload_json）。
//
// ChatContentBlocks 加可空 form_payload_json 列，存 form 块整块 payload
//（{request_id, question, header, multi_select, options, action, content}，
// 与服务端 chat.messages.parts[{type:'form'}] 同形）。
//
// 本测试断言：v36 老库升到 v37 后 chat_content_blocks 多出
// form_payload_json 列，存量行该列为 null，form 块可正常写入读出。
//
// 手建旧库（不走 onCreate）的原因见 migration_v22_to_v26_test.dart 头注释：
// AppDb.memory() 走 onCreate 永远跑不到 onUpgrade。v37 只动
// chat_content_blocks 一张表，fixture 只需它的 v36 形态骨架。

import 'package:biumind/data/local/db.dart';
import 'package:drift/native.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:sqlite3/sqlite3.dart';

void main() {
  test('v36 旧库迁移到 v37：chat_content_blocks 加 form_payload_json 列',
      () async {
    // ── 1. 手建 v36 形态的 chat_content_blocks（无 form_payload_json）──
    final raw = sqlite3.openInMemory();
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
        updated_at INTEGER NOT NULL,
        owner_key TEXT NOT NULL DEFAULT ''
      );
    ''');
    raw.execute(
      "INSERT INTO chat_content_blocks (id, message_id, block_index, type, "
      "text_content, created_at, updated_at, owner_key) "
      "VALUES ('b-old', 'm1', 0, 'text', 'hi', 1780000000, 1780000000, 'scope')",
    );
    raw.userVersion = 36;

    // ── 2. 同一句柄交给 drift，首次查询触发 onUpgrade(36→37) ──
    final db = AppDb.executor(NativeDatabase.opened(raw));
    addTearDown(db.close);
    await db.customSelect('SELECT 1').get();

    // ── 3. 断言 ──
    expect(raw.userVersion, 37, reason: '迁移后 schema 版本应为 37');

    final cols = raw
        .select('PRAGMA table_info(chat_content_blocks)')
        .map((r) => r['name'] as String)
        .toSet();
    expect(
      cols,
      contains('form_payload_json'),
      reason: 'chat_content_blocks 应加 form_payload_json 列（Phase 37）',
    );

    final oldRow = raw
        .select(
            "SELECT form_payload_json FROM chat_content_blocks WHERE id = 'b-old'")
        .first;
    expect(oldRow['form_payload_json'], isNull, reason: '存量行该列应为 null');

    // form 块可写入读出（走 Drift 类型 API，验证生成代码与 schema 一致）。
    final dao = db.chatContentBlocks;
    await db.into(dao).insert(LocalChatContentBlock(
          id: 'b-form',
          messageId: 'm1',
          blockIndex: 1,
          type: 'form',
          formPayloadJson: '{"request_id":"r1","action":"accept"}',
          state: 'closed',
          createdAt: DateTime.fromMillisecondsSinceEpoch(1780000000 * 1000,
              isUtc: true),
          updatedAt: DateTime.fromMillisecondsSinceEpoch(1780000000 * 1000,
              isUtc: true),
          ownerKey: 'scope',
        ));
    final row =
        await (db.select(dao)..where((t) => t.id.equals('b-form'))).getSingle();
    expect(row.formPayloadJson, '{"request_id":"r1","action":"accept"}');
  });
}

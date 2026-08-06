// thread_export_json —— 会话导入导出 round-trip 单测。

import 'package:flutter_test/flutter_test.dart';

import 'package:biumind/features/chat/domain/chat_models.dart';
import 'package:biumind/features/chat/domain/thread_export_json.dart';

Thread _thread() => Thread(
      id: 't1',
      title: 'Wiki design',
      mode: ThreadMode.chat,
      model: 'claude-opus-4-7',
      systemPrompt: '你是 Flutter 架构师',
      createdAt: DateTime.utc(2026, 6, 1, 9),
      updatedAt: DateTime.utc(2026, 6, 1, 10),
    );

Message _msg({
  required String id,
  required MessageRole role,
  required int seq,
  required List<Block> blocks,
  MessageStatus status = MessageStatus.completed,
}) {
  return Message(
    id: id,
    threadId: 't1',
    role: role,
    status: status,
    seq: seq,
    createdAt: DateTime.utc(2026, 6, 1, 9, seq),
    completedAt: DateTime.utc(2026, 6, 1, 9, seq, 30),
    blocks: blocks,
    inputTokens: role == MessageRole.assistant ? 50 : null,
    outputTokens: role == MessageRole.assistant ? 200 : null,
  );
}

void main() {
  group('exportThreadAsJson', () {
    test('contains thread + filtered messages', () {
      final t = _thread();
      final messages = [
        _msg(
          id: 'm1',
          role: MessageRole.user,
          seq: 1,
          blocks: [
            const TextBlock(
              id: 'm1b0',
              index: 0,
              state: BlockState.closed,
              text: 'hello',
            ),
          ],
        ),
        _msg(
          id: 'm2',
          role: MessageRole.assistant,
          seq: 2,
          blocks: [
            const TextBlock(
              id: 'm2b0',
              index: 0,
              state: BlockState.closed,
              text: 'hi back',
            ),
          ],
        ),
      ];
      final s = exportThreadAsJson(thread: t, messages: messages);
      expect(s.contains('"schemaVersion": 1'), true);
      expect(s.contains('"title": "Wiki design"'), true);
      expect(s.contains('"text": "hello"'), true);
      expect(s.contains('"text": "hi back"'), true);
    });

    test('skips toolResult / non-completed messages', () {
      final t = _thread();
      final messages = [
        _msg(
          id: 'tr',
          role: MessageRole.toolResult,
          seq: 1,
          blocks: [],
        ),
        _msg(
          id: 'failed',
          role: MessageRole.assistant,
          seq: 2,
          status: MessageStatus.failed,
          blocks: [],
        ),
        _msg(
          id: 'ok',
          role: MessageRole.user,
          seq: 3,
          blocks: [
            const TextBlock(
              id: 'okb0',
              index: 0,
              state: BlockState.closed,
              text: 'ok',
            ),
          ],
        ),
      ];
      final parsed =
          parseThreadExportJson(exportThreadAsJson(thread: t, messages: messages));
      expect(parsed.messages.length, 1);
      expect(parsed.messages.first.id, 'ok');
    });
  });

  group('round-trip', () {
    test('preserves thread + text/image blocks', () {
      final t = _thread();
      final messages = [
        _msg(
          id: 'm1',
          role: MessageRole.user,
          seq: 1,
          blocks: [
            const TextBlock(
              id: 'b0',
              index: 0,
              state: BlockState.closed,
              text: 'hello world',
            ),
            const ImageBlock(
              id: 'b1',
              index: 1,
              state: BlockState.closed,
              mimeType: 'image/png',
              data: 'AQIDBA==',
            ),
          ],
        ),
      ];
      final json = exportThreadAsJson(thread: t, messages: messages);
      final parsed = parseThreadExportJson(json);
      expect(parsed.thread.title, 'Wiki design');
      expect(parsed.thread.systemPrompt, '你是 Flutter 架构师');
      expect(parsed.messages.length, 1);
      final m = parsed.messages.first;
      expect(m.blocks.length, 2);
      expect(m.blocks[0], isA<TextBlock>());
      expect((m.blocks[0] as TextBlock).text, 'hello world');
      expect(m.blocks[1], isA<ImageBlock>());
      expect((m.blocks[1] as ImageBlock).data, 'AQIDBA==');
    });

    test('preserves tool_use / tool_result blocks', () {
      final t = _thread();
      final messages = [
        _msg(
          id: 'm1',
          role: MessageRole.assistant,
          seq: 1,
          blocks: [
            const ToolUseBlock(
              id: 'tu0',
              index: 0,
              state: BlockState.closed,
              toolUseId: 'tu_abc',
              toolName: 'bash',
              input: {'command': 'ls'},
            ),
            const ToolResultBlock(
              id: 'tr0',
              index: 1,
              state: BlockState.closed,
              toolResultId: 'tu_abc',
              isError: false,
              content: 'file1\nfile2',
            ),
          ],
        ),
      ];
      final parsed = parseThreadExportJson(
          exportThreadAsJson(thread: t, messages: messages));
      final blocks = parsed.messages.first.blocks;
      expect(blocks[0], isA<ToolUseBlock>());
      expect((blocks[0] as ToolUseBlock).toolName, 'bash');
      expect((blocks[0] as ToolUseBlock).input?['command'], 'ls');
      expect(blocks[1], isA<ToolResultBlock>());
      expect((blocks[1] as ToolResultBlock).toolResultId, 'tu_abc');
      expect((blocks[1] as ToolResultBlock).content, 'file1\nfile2');
    });
  });

  group('exportAllAsJson', () {
    test('emits kind=bulk + threads array', () {
      final t1 = _thread();
      final t2 = Thread(
        id: 't2',
        title: 'Cooking',
        mode: ThreadMode.chat,
        createdAt: DateTime.utc(2026, 6, 1),
        updatedAt: DateTime.utc(2026, 6, 2),
      );
      final s = exportAllAsJson(entries: [
        (
          thread: t1,
          messages: [
            _msg(
              id: 'm',
              role: MessageRole.user,
              seq: 1,
              blocks: [
                const TextBlock(
                  id: 'b',
                  index: 0,
                  state: BlockState.closed,
                  text: 'hi',
                ),
              ],
            ),
          ],
        ),
        (thread: t2, messages: const <Message>[]),
      ]);
      expect(s.contains('"kind": "bulk"'), true);
      expect(s.contains('"title": "Wiki design"'), true);
      expect(s.contains('"title": "Cooking"'), true);
    });
  });

  group('parseThreadExportJson errors', () {
    test('non-object source throws', () {
      expect(() => parseThreadExportJson('"a string"'),
          throwsA(isA<FormatException>()));
      expect(() => parseThreadExportJson('[1,2]'),
          throwsA(isA<FormatException>()));
    });

    test('wrong schemaVersion throws', () {
      const bad = '''{"schemaVersion":999,"thread":{},"messages":[]}''';
      expect(() => parseThreadExportJson(bad),
          throwsA(isA<FormatException>()));
    });

    test('missing thread / messages throws', () {
      expect(
          () => parseThreadExportJson('{"schemaVersion":1,"messages":[]}'),
          throwsA(isA<FormatException>()));
    });

    test('isBulkExport detects kind=bulk', () {
      const single = '{"schemaVersion":1,"thread":{},"messages":[]}';
      const bulk =
          '{"schemaVersion":1,"kind":"bulk","threads":[]}';
      expect(isBulkExport(single), false);
      expect(isBulkExport(bulk), true);
      expect(isBulkExport('not-json'), false);
      expect(isBulkExport('"a string"'), false);
    });

    test('parseBulkExportJson roundtrips multiple threads', () {
      final t1 = _thread();
      final t2 = Thread(
        id: 't2',
        title: 'Cooking',
        mode: ThreadMode.chat,
        createdAt: DateTime.utc(2026, 6, 1),
        updatedAt: DateTime.utc(2026, 6, 2),
      );
      final src = exportAllAsJson(entries: [
        (
          thread: t1,
          messages: [
            _msg(
              id: 'm1',
              role: MessageRole.user,
              seq: 1,
              blocks: [
                const TextBlock(
                  id: 'b',
                  index: 0,
                  state: BlockState.closed,
                  text: 'hi',
                ),
              ],
            ),
          ],
        ),
        (thread: t2, messages: const <Message>[]),
      ]);
      final parsed = parseBulkExportJson(src);
      expect(parsed.length, 2);
      expect(parsed[0].thread.id, 't1');
      expect(parsed[0].messages.length, 1);
      expect(parsed[1].thread.id, 't2');
      expect(parsed[1].messages, isEmpty);
    });

    test('parseBulkExportJson rejects non-bulk source', () {
      const single = '{"schemaVersion":1,"thread":{},"messages":[]}';
      expect(() => parseBulkExportJson(single),
          throwsA(isA<FormatException>()));
    });

    test('unknown block type silently dropped', () {
      const src = '''
        {
          "schemaVersion": 1,
          "thread": {
            "id": "t1",
            "title": "x",
            "mode": "chat",
            "createdAt": "2026-06-01T00:00:00Z",
            "updatedAt": "2026-06-01T00:00:00Z"
          },
          "messages": [
            {
              "id": "m1",
              "role": "user",
              "status": "completed",
              "seq": 1,
              "createdAt": "2026-06-01T00:00:00Z",
              "blocks": [
                {"type": "alien", "x": 1},
                {"type": "text", "text": "ok"}
              ]
            }
          ]
        }
      ''';
      final parsed = parseThreadExportJson(src);
      expect(parsed.messages.first.blocks.length, 1);
      expect(parsed.messages.first.blocks.first, isA<TextBlock>());
    });
  });
}

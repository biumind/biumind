// FormBlock（P3-b 表单沉淀）payload 编解码测试。
//
// 覆盖：
//   - fromPayload ← 服务端 parts[{type:'form'}] / form_answer 帧 payload
//   - content.answer 三种形态（String / List<String> / 缺省）flatten 成摘要
//   - toPayload 反向编码 round-trip（Drift form_payload_json / 导出用）

import 'package:flutter_test/flutter_test.dart';

import 'package:biumind/features/chat/domain/chat_models.dart';

void main() {
  group('FormBlock.fromPayload', () {
    test('单选 accept：answer String 直接作摘要', () {
      final b = FormBlock.fromPayload(
        {
          'type': 'form',
          'request_id': 'req-1',
          'question': 'Pick a color?',
          'header': 'Color',
          'multi_select': false,
          'options': [
            {'label': 'red', 'description': 'warm'},
            {'label': 'blue', 'description': 'cool'},
          ],
          'action': 'accept',
          'content': {'answer': 'blue'},
        },
        id: 'b0',
        index: 0,
      );
      expect(b.requestId, 'req-1');
      expect(b.question, 'Pick a color?');
      expect(b.header, 'Color');
      expect(b.multiSelect, isFalse);
      expect(b.options.map((o) => o.label), ['red', 'blue']);
      expect(b.options.first.description, 'warm');
      expect(b.action, 'accept');
      expect(b.answerSummary, 'blue');
      expect(b.notes, isNull);
      expect(b.state, BlockState.closed);
    });

    test('多选 accept：answer List 顿号拼接', () {
      final b = FormBlock.fromPayload(
        {
          'request_id': 'req-2',
          'question': 'q',
          'multi_select': true,
          'action': 'accept',
          'content': {
            'answer': ['red', 'green'],
            'notes': '都喜欢',
          },
        },
        id: 'b0',
        index: 0,
      );
      expect(b.multiSelect, isTrue);
      expect(b.answerSummary, 'red、green');
      expect(b.notes, '都喜欢');
    });

    test('decline / timeout：无 content → 无摘要', () {
      for (final action in ['decline', 'timeout', 'cancel']) {
        final b = FormBlock.fromPayload(
          {'request_id': 'r', 'question': 'q', 'action': action},
          id: 'b0',
          index: 0,
        );
        expect(b.action, action);
        expect(b.answerSummary, isNull);
      }
    });

    test('脏 payload 防御：缺字段不炸，给默认值', () {
      final b = FormBlock.fromPayload(const {}, id: 'b0', index: 1);
      expect(b.requestId, '');
      expect(b.question, '');
      expect(b.options, isEmpty);
      expect(b.action, 'cancel');
    });
  });

  group('FormBlock.toPayload round-trip', () {
    test('toPayload → fromPayload 字段保真', () {
      final original = FormBlock.fromPayload(
        {
          'request_id': 'req-1',
          'question': 'Pick a color?',
          'header': 'Color',
          'multi_select': true,
          'options': [
            {'label': 'red', 'description': 'warm'},
          ],
          'action': 'accept',
          'content': {
            'answer': ['red'],
            'notes': 'n',
          },
        },
        id: 'b0',
        index: 0,
      );
      final restored = FormBlock.fromPayload(original.toPayload(),
          id: 'b0', index: 0);
      expect(restored.requestId, original.requestId);
      expect(restored.question, original.question);
      expect(restored.header, original.header);
      expect(restored.multiSelect, original.multiSelect);
      expect(restored.options.first.label, 'red');
      expect(restored.options.first.description, 'warm');
      expect(restored.action, 'accept');
      // 多选 list 形状 flatten 后不再复原（本地 SoT 只需展示保真）。
      expect(restored.answerSummary, 'red');
      expect(restored.notes, 'n');
    });
  });
}

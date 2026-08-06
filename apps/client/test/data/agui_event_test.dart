import 'package:biumind/data/sse/agui_event.dart';
import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  group('AgUiEvent.parse', () {
    test('lifecycle', () {
      final ev = AgUiEvent.parse('RUN_STARTED', {'threadId': 't1', 'runId': 'r1'});
      expect(ev, isA<RunStarted>());
      final s = ev as RunStarted;
      expect(s.runId, 'r1');
      expect(s.threadId, 't1');
    });

    test('text content', () {
      final ev = AgUiEvent.parse('TEXT_MESSAGE_CONTENT', {'messageId': 'm1', 'delta': 'hi'});
      expect(ev, isA<TextMessageContent>());
      expect((ev as TextMessageContent).delta, 'hi');
    });

    test('tool call lifecycle', () {
      final s = AgUiEvent.parse('TOOL_CALL_START', {
        'toolCallId': 'tc1', 'toolCallName': 'read', 'parentMessageId': 'm1',
      });
      expect((s as ToolCallStart).toolCallName, 'read');

      final a = AgUiEvent.parse('TOOL_CALL_ARGS', {'toolCallId': 'tc1', 'delta': '{"path":'});
      expect((a as ToolCallArgs).delta, '{"path":');

      final e = AgUiEvent.parse('TOOL_CALL_END', {'toolCallId': 'tc1'});
      expect((e as ToolCallEnd).toolCallId, 'tc1');

      final r = AgUiEvent.parse('TOOL_CALL_RESULT', {'toolCallId': 'tc1', 'content': 'hello'});
      expect((r as ToolCallResult).content, 'hello');
    });

    test('CUSTOM dispatches biumind.cost.update', () {
      final ev = AgUiEvent.parse('CUSTOM', {
        'name': 'biumind.cost.update',
        'value': {'tokens_in': 100, 'tokens_out': 50, 'cost_micro_usd': 1234, 'model': 'sonnet'},
      });
      expect(ev, isA<CostUpdate>());
      final c = ev as CostUpdate;
      expect(c.tokensIn, 100);
      expect(c.costMicroUsd, 1234);
      expect(c.model, 'sonnet');
    });

    test('CUSTOM unknown name preserved', () {
      final ev = AgUiEvent.parse('CUSTOM', {'name': 'vendor.x', 'value': {'foo': 1}});
      expect(ev, isA<UnregisteredCustomEvent>());
      final u = ev as UnregisteredCustomEvent;
      expect(u.name, 'vendor.x');
      expect(u.value['foo'], 1);
    });

    test('unknown event type preserved', () {
      final ev = AgUiEvent.parse('FUTURE_EVENT', {'extra': 1});
      expect(ev, isA<UnknownEvent>());
      expect((ev as UnknownEvent).type, 'FUTURE_EVENT');
    });
  });

  group('ToolCallArgs.parseArgs', () {
    test('parses accumulated json', () {
      expect(ToolCallArgs.parseArgs('{"path":"x.txt"}'), {'path': 'x.txt'});
    });
    test('returns null for invalid', () {
      expect(ToolCallArgs.parseArgs('{"path":'), null);
    });
    test('returns null for empty', () {
      expect(ToolCallArgs.parseArgs(''), null);
    });
  });
}

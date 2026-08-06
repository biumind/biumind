import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:biumind/features/code/domain/code_task.dart';

CodeTask _task({
  required List<AgentEvent> events,
  CodeTaskStatus status = CodeTaskStatus.interrupted,
}) =>
    CodeTask(
      id: 't1',
      title: 't',
      prompt: 'p',
      agent: AgentKind.claudeCode,
      mode: PermissionMode.ask,
      status: status,
      events: events,
      cost: const TaskCost(),
      createdAt: DateTime(2026, 6, 28),
    );

void main() {
  test('session_info fromJson', () {
    final e = AgentEvent.fromJson({
      'type': 'session_info',
      'ts': '2026-06-28T10:00:00Z',
      'agent': 'claude',
      'session_id': 'sid-123',
    });
    expect(e, isA<SessionInfo>());
    expect((e as SessionInfo).sessionId, 'sid-123');
    expect(e.agent, 'claude');
  });

  test('resumeSessionId 取最后一条 SessionInfo', () {
    final t = _task(events: [
      SessionInfo(ts: DateTime(2026, 6, 28), agent: 'claude', sessionId: 'a'),
      TextDelta(ts: DateTime(2026, 6, 28), text: 'hi'),
      SessionInfo(ts: DateTime(2026, 6, 28), agent: 'claude', sessionId: 'b'),
    ]);
    expect(t.resumeSessionId, 'b');
    expect(t.canResume, isTrue); // interrupted + 有 sid
  });

  test('无 SessionInfo → 不可续跑', () {
    final t = _task(events: [TextDelta(ts: DateTime(2026, 6, 28), text: 'x')]);
    expect(t.resumeSessionId, isNull);
    expect(t.canResume, isFalse);
  });

  test('终态 done 即便有 sid 也不显续跑', () {
    final t = _task(
      status: CodeTaskStatus.done,
      events: [
        SessionInfo(ts: DateTime(2026, 6, 28), agent: 'claude', sessionId: 'a')
      ],
    );
    expect(t.canResume, isFalse);
  });
}

// AgentStatus 事件解析/回写测试(PERI-1d)。daemon hook watcher 发
// {type:agent_status,status:running|input_required},客户端据此可靠切换任务状态。
import 'package:biumind/features/code/domain/code_task.dart';
import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test('fromJson 解析 agent_status', () {
    final ev = AgentEvent.fromJson({
      'type': 'agent_status',
      'status': 'input_required',
      'ts': '2026-01-01T00:00:00.000Z',
    });
    expect(ev, isA<AgentStatus>());
    expect((ev as AgentStatus).status, 'input_required');
  });

  test('agent_status 缺 status → 空串(不崩)', () {
    final ev = AgentEvent.fromJson({'type': 'agent_status'});
    expect(ev, isA<AgentStatus>());
    expect((ev as AgentStatus).status, '');
  });

  test('toJson round-trip', () {
    final ts = DateTime.utc(2026, 1, 1);
    final ev = AgentStatus(ts: ts, status: 'running');
    final back = AgentEvent.fromJson(ev.toJson());
    expect(back, isA<AgentStatus>());
    expect((back as AgentStatus).status, 'running');
  });
}

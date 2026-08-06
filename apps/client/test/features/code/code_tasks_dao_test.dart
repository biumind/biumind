// CodeTasksDao mapping tests — round-trip + edge cases. In-memory drift.

import 'package:biumind/data/local/db.dart';
import 'package:biumind/features/code/data/code_tasks_dao.dart';
import 'package:biumind/features/code/domain/code_task.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  late AppDb db;
  late CodeTasksDao dao;

  setUp(() {
    db = AppDb.memory();
    dao = CodeTasksDao(db);
  });
  tearDown(() => db.close());

  CodeTask makeTask({
    String id = 'task-A',
    AgentKind agent = AgentKind.biu,
    PermissionMode mode = PermissionMode.ask,
    CodeTaskStatus status = CodeTaskStatus.queued,
    List<AgentEvent> events = const [],
    String? originDeviceId = 'dev-1',
    String? originDeviceLabel = 'macbook',
  }) {
    return CodeTask(
      id: id,
      title: 'a task',
      prompt: 'do it',
      agent: agent,
      mode: mode,
      status: status,
      events: events,
      cost: const TaskCost(),
      createdAt: DateTime.utc(2026, 5, 27, 9, 0),
      originDeviceId: originDeviceId,
      originDeviceLabel: originDeviceLabel,
    );
  }

  test('upsert + loadAll round-trips origin device fields', () async {
    await dao.upsert(makeTask());
    final all = await dao.loadAll();
    expect(all, hasLength(1));
    final got = all.first;
    expect(got.originDeviceId, 'dev-1');
    expect(got.originDeviceLabel, 'macbook');
  });

  test('upsert preserves null origin device (legacy / pre-schema-v5 row)',
      () async {
    await dao.upsert(makeTask(originDeviceId: null, originDeviceLabel: null));
    final got = (await dao.loadAll()).first;
    expect(got.originDeviceId, isNull);
    expect(got.originDeviceLabel, isNull);
  });

  test('upsert is idempotent on same id (replace, not duplicate)', () async {
    await dao.upsert(makeTask(originDeviceId: 'dev-1'));
    await dao.upsert(makeTask(originDeviceId: 'dev-2')); // same id 'task-A'
    final all = await dao.loadAll();
    expect(all, hasLength(1));
    expect(all.first.originDeviceId, 'dev-2');
  });

  test('upsert + loadAll round-trips events list', () async {
    final ts = DateTime.utc(2026, 5, 27, 9, 5);
    final task = makeTask(events: [
      TextDelta(ts: ts, text: 'hello '),
      TextDelta(ts: ts, text: 'world'),
      ToolUseStart(ts: ts, toolId: 'tu-1', name: 'Bash', args: const {'cmd': 'ls'}),
      ToolUseResult(ts: ts, toolId: 'tu-1', result: 'a.txt', isError: false),
    ]);
    await dao.upsert(task);
    final got = (await dao.loadAll()).first;
    expect(got.events, hasLength(4));
    expect(got.events[0], isA<TextDelta>());
    expect((got.events[0] as TextDelta).text, 'hello ');
    expect(got.events[2], isA<ToolUseStart>());
    expect((got.events[2] as ToolUseStart).name, 'Bash');
    expect(got.events[3], isA<ToolUseResult>());
    expect((got.events[3] as ToolUseResult).result, 'a.txt');
  });

  test('deleteById removes the task', () async {
    await dao.upsert(makeTask(id: 'a'));
    await dao.upsert(makeTask(id: 'b'));
    await dao.deleteById('a');
    final all = await dao.loadAll();
    expect(all.map((t) => t.id), ['b']);
  });

  test('loadAll orders by createdAt desc (newest first)', () async {
    await dao.upsert(CodeTask(
      id: 'old',
      title: 'old',
      prompt: '',
      agent: AgentKind.biu,
      mode: PermissionMode.ask,
      status: CodeTaskStatus.done,
      events: const [],
      cost: const TaskCost(),
      createdAt: DateTime.utc(2026, 5, 25),
    ));
    await dao.upsert(CodeTask(
      id: 'new',
      title: 'new',
      prompt: '',
      agent: AgentKind.biu,
      mode: PermissionMode.ask,
      status: CodeTaskStatus.queued,
      events: const [],
      cost: const TaskCost(),
      createdAt: DateTime.utc(2026, 5, 27),
    ));
    final all = await dao.loadAll();
    expect(all.map((t) => t.id).toList(), ['new', 'old']);
  });

  test('clear truncates the table', () async {
    await dao.upsert(makeTask(id: 'a'));
    await dao.upsert(makeTask(id: 'b'));
    await dao.clear();
    expect(await dao.loadAll(), isEmpty);
  });
}

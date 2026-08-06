// EnvironmentRepo 单测 —— 用一个 fake AgentPlaneClient 控制 listEnvironments
// 返回值，不启 HttpServer。验 cache + 后台刷新 + watch 流。
//
// 真 HTTP wire 由 agent_plane_client_test.dart 覆盖（in-process HttpServer）。


import 'package:flutter_test/flutter_test.dart';

import 'package:biumind/data/agent_plane/agent_plane_client.dart';
import 'package:biumind/data/agent_plane/environment.dart';
import 'package:biumind/data/agent_plane/environment_repo.dart';

class _FakeClient extends AgentPlaneClient {
  _FakeClient(this._envs)
      : super(baseUrl: 'http://test', tokenProvider: () async => 'tok');

  final List<AgentEnvironment> _envs;
  int listCalls = 0;
  Object? nextError;

  @override
  Future<List<AgentEnvironment>> listEnvironments({String state = ''}) async {
    listCalls++;
    if (nextError != null) {
      final e = nextError;
      nextError = null;
      throw e!;
    }
    if (state == 'online') {
      return _envs.where((e) => e.isOnline).toList();
    }
    return _envs;
  }
}

AgentEnvironment _env(String id, {String state = 'online'}) {
  return AgentEnvironment(
    environmentId: id,
    workerKind: 'biu_daemon',
    machineName: 'machine-$id',
    state: state,
  );
}

void main() {
  test('list() triggers background fetch on first call', () async {
    final client = _FakeClient([_env('a'), _env('b')]);
    final repo = EnvironmentRepo(
      client: client,
      refreshInterval: const Duration(seconds: 30),
    );

    // 首次：cache 还空，立即返空，但触发 fetch
    expect(repo.list(), isEmpty);
    expect(client.listCalls, 1);

    // 等 fetch 完成
    await Future.delayed(const Duration(milliseconds: 30));
    expect(repo.list().length, 2);
  });

  test('online() filters to online envs', () async {
    final client = _FakeClient([
      _env('a', state: 'online'),
      _env('b', state: 'offline'),
      _env('c', state: 'online'),
    ]);
    final repo = EnvironmentRepo(client: client);
    await repo.refresh();

    final on = repo.online();
    expect(on.length, 2);
    expect(on.every((e) => e.isOnline), true);
  });

  test('byId hits cache only', () async {
    final client = _FakeClient([_env('alpha'), _env('beta')]);
    final repo = EnvironmentRepo(client: client);
    await repo.refresh();

    expect(repo.byId('alpha')?.machineName, 'machine-alpha');
    expect(repo.byId('not-there'), isNull);
    // byId 不发新请求
    expect(client.listCalls, 1);
  });

  test('refresh() coalesces concurrent calls', () async {
    final client = _FakeClient([_env('a')]);
    final repo = EnvironmentRepo(client: client);

    final f1 = repo.refresh();
    final f2 = repo.refresh();
    final f3 = repo.refresh();
    await Future.wait([f1, f2, f3]);

    expect(client.listCalls, 1, reason: 'concurrent calls dedupe to one HTTP req');
  });

  test('watch() emits new list after refresh', () async {
    final client = _FakeClient([_env('a')]);
    final repo = EnvironmentRepo(client: client);

    final received = <List<AgentEnvironment>>[];
    final sub = repo.watch().listen(received.add);

    await repo.refresh();
    await Future.delayed(const Duration(milliseconds: 10));

    expect(received.length, 1);
    expect(received.first.first.environmentId, 'a');

    await sub.cancel();
    await repo.dispose();
  });

  test('refresh() error does not corrupt cache', () async {
    final client = _FakeClient([_env('a')]);
    final repo = EnvironmentRepo(client: client);
    await repo.refresh();
    expect(repo.list().length, 1);

    client.nextError = StateError('network down');
    try {
      await repo.refresh();
      fail('expected error');
    } on StateError {
      // expected
    }
    // cache stays intact —— UI 仍显示老数据
    expect(repo.list().length, 1);
    expect(repo.list().first.environmentId, 'a');
  });

  test('start() schedules periodic refresh', () async {
    final client = _FakeClient([_env('a')]);
    final repo = EnvironmentRepo(
      client: client,
      refreshInterval: const Duration(milliseconds: 80),
    );
    repo.start();

    // 等过 3 个 tick
    await Future.delayed(const Duration(milliseconds: 280));
    await repo.dispose();

    // 至少 2 次（首次 kick + 至少 1 个定时 tick）
    expect(client.listCalls >= 2, true,
        reason: 'periodic refresh should fire; got ${client.listCalls}');
  });

  test('refreshInterval=Duration.zero disables PERIODIC refresh', () async {
    final client = _FakeClient([_env('a')]);
    final repo = EnvironmentRepo(
      client: client,
      refreshInterval: Duration.zero,
    );
    repo.start();
    // 初始 kick 仍会发生（_kickFetchIfStale 的 last==null 条件先 hit）；
    // 但 Timer.periodic 不应该 schedule，所以等多久 list calls 都不再涨
    await Future.delayed(const Duration(milliseconds: 50));
    final after50 = client.listCalls;
    await Future.delayed(const Duration(milliseconds: 100));
    await repo.dispose();

    expect(after50, 1, reason: 'initial kick fires once');
    expect(client.listCalls, 1, reason: 'no periodic timer with zero interval');
  });
}

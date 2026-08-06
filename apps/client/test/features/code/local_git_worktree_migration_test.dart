// LocalGitWorktreeWorkspace 迁移后(git 走 bridge)的单测。
// 用自动应答 transport 喂 canned git.createWorktree / removeWorktree / worktreeDiffStats /
// changedFiles / listUntracked,验证 setup/teardown/diffSummary/collectArtifacts 的 bridge 接线。

import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:biumind/features/code/data/code_bridge_client.dart';
import 'package:biumind/features/code/workspace/local_git_worktree.dart';
import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_test/flutter_test.dart';

class AutoRespondTransport implements CodeTransport {
  AutoRespondTransport(this.responder);
  final Map<String, dynamic>? Function(String method, Map<String, dynamic>? p)
      responder;
  final sent = <Map<String, dynamic>>[];
  final _ctrl = StreamController<dynamic>.broadcast();

  @override
  Stream<dynamic> get frames => _ctrl.stream;

  @override
  void send(String data) {
    final f = jsonDecode(data) as Map<String, dynamic>;
    if (f['type'] != 'code_request') return;
    sent.add(f);
    final id = f['request_id'] as String;
    final method = f['method'] as String;
    final result = responder(method, f['params'] as Map<String, dynamic>?);
    scheduleMicrotask(() {
      if (_ctrl.isClosed) return;
      _ctrl.add(jsonEncode({
        'type': 'code_response',
        'request_id': id,
        'ok': result != null,
        if (result != null) 'result': result,
        if (result == null) 'error': 'no canned for $method',
      }));
    });
  }

  @override
  Future<void> close() async {
    if (!_ctrl.isClosed) await _ctrl.close();
  }
}

void main() {
  test('setup creates worktree via bridge + writes meta + builds ref', () async {
    final tmp = await Directory.systemTemp.createTemp('wt-mig-');
    addTearDown(() => tmp.delete(recursive: true));
    final repoRoot = tmp.path;
    final worktreePath = '${tmp.path}/wt';
    // 模拟 daemon 的 git worktree add 已建好目录(fake bridge 不会真建)。
    await Directory(worktreePath).create(recursive: true);

    final t = AutoRespondTransport((m, p) {
      switch (m) {
        case 'git.createWorktree':
          return {
            'worktree_path': p?['worktree_path'],
            'branch': 'biu/biu-abc123',
            'base_branch': 'main',
            'base_commit': 'deadbeef',
          };
        default:
          return null;
      }
    });
    final bridge = CodeBridgeClient(bridgeUrl: 'http://x', connector: (_) => t);
    await bridge.connect();

    final ws = LocalGitWorktreeWorkspace(
      bridge: bridge,
      taskId: 'task-1',
      shortId: 'abc123',
      agent: 'biu',
      repoRoot: repoRoot,
      worktreePath: worktreePath,
      preferredBranchName: 'biu/biu-abc123',
      metadata: {'prompt_first_line': 'hello'},
    );
    await ws.setup();

    // createWorktree 用对了参数。
    final create = t.sent.firstWhere((s) => s['method'] == 'git.createWorktree');
    expect(create['params']['project_path'], repoRoot);
    expect(create['params']['worktree_path'], worktreePath);
    expect(create['params']['preferred_branch'], 'biu/biu-abc123');

    // ref 来自响应。
    expect(ws.ref.branchName, 'biu/biu-abc123');
    expect(ws.ref.baseCommit, 'deadbeef');
    expect(ws.ref.baseBranch, 'main');

    // .biu-meta.json 落盘。
    final meta =
        jsonDecode(await File('$worktreePath/.biu-meta.json').readAsString());
    expect(meta['branch'], 'biu/biu-abc123');
    expect(meta['base_commit'], 'deadbeef');
    expect(meta['prompt_first_line'], 'hello');

    await bridge.close();
  });

  test('setup resumes from existing meta without calling createWorktree',
      () async {
    final tmp = await Directory.systemTemp.createTemp('wt-resume-');
    addTearDown(() => tmp.delete(recursive: true));
    final worktreePath = '${tmp.path}/wt';
    await Directory(worktreePath).create(recursive: true);
    await File('$worktreePath/.biu-meta.json').writeAsString(jsonEncode({
      'branch': 'biu/resumed',
      'base_commit': 'cafe',
      'base_branch': 'main',
      'created_at': DateTime.now().toIso8601String(),
    }));

    final t = AutoRespondTransport((m, p) => null); // 任何 RPC 都失败
    final bridge = CodeBridgeClient(bridgeUrl: 'http://x', connector: (_) => t);
    await bridge.connect();

    final ws = LocalGitWorktreeWorkspace(
      bridge: bridge,
      taskId: 'task-2',
      shortId: 'r',
      agent: 'biu',
      repoRoot: tmp.path,
      worktreePath: worktreePath,
      preferredBranchName: 'biu/x',
      metadata: const {},
    );
    await ws.setup();

    expect(ws.ref.branchName, 'biu/resumed');
    expect(ws.ref.baseCommit, 'cafe');
    // 复用路径不应调 createWorktree。
    expect(t.sent.any((s) => s['method'] == 'git.createWorktree'), false);
    await bridge.close();
  });

  test('teardown(keepBranch:false) removes worktree via bridge', () async {
    final tmp = await Directory.systemTemp.createTemp('wt-rm-');
    addTearDown(() => tmp.delete(recursive: true));
    final worktreePath = '${tmp.path}/wt';
    await Directory(worktreePath).create(recursive: true);

    final t = AutoRespondTransport((m, p) {
      if (m == 'git.createWorktree') {
        return {
          'worktree_path': p?['worktree_path'],
          'branch': 'biu/rm',
          'base_branch': 'main',
          'base_commit': 'c0',
        };
      }
      if (m == 'git.removeWorktree') return {};
      return null;
    });
    final bridge = CodeBridgeClient(bridgeUrl: 'http://x', connector: (_) => t);
    await bridge.connect();

    final ws = LocalGitWorktreeWorkspace(
      bridge: bridge,
      taskId: 'task-3',
      shortId: 'rm',
      agent: 'biu',
      repoRoot: tmp.path,
      worktreePath: worktreePath,
      preferredBranchName: 'biu/rm',
      metadata: const {},
    );
    await ws.setup();
    await ws.teardown(keepBranch: false);

    final rm = t.sent.firstWhere((s) => s['method'] == 'git.removeWorktree');
    expect(rm['params']['worktree_path'], worktreePath);
    expect(rm['params']['branch'], 'biu/rm');
    await bridge.close();
  });
}

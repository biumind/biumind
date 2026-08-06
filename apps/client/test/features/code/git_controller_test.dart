// Git 模型解析 + 类型化 bridge 方法 + GitController 行为单测。
//
// 用一个「自动应答」的 FakeCodeTransport:send 收到 code_request 后按 method 立刻
// 回一条 canned code_response,从而无需真 daemon 就能驱动 controller.refresh 等。

import 'dart:async';
import 'dart:convert';

import 'package:biumind/features/code/application/git_controller.dart';
import 'package:biumind/features/code/data/code_bridge_client.dart';
import 'package:biumind/features/code/domain/git_models.dart';
import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_test/flutter_test.dart';

/// 按 method 给 canned result 的自动应答 transport。
class AutoRespondTransport implements CodeTransport {
  AutoRespondTransport(this.responder);

  /// method → result map(返回 null 表示回 ok:false)。
  final Map<String, dynamic>? Function(String method, Map<String, dynamic>? params)
      responder;
  final _ctrl = StreamController<dynamic>.broadcast();
  bool closed = false;

  @override
  Stream<dynamic> get frames => _ctrl.stream;

  @override
  void send(String data) {
    final f = jsonDecode(data) as Map<String, dynamic>;
    if (f['type'] != 'code_request') return;
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
        if (result == null) 'error': 'no canned response for $method',
      }));
    });
  }

  @override
  Future<void> close() async {
    closed = true;
    if (!_ctrl.isClosed) await _ctrl.close();
  }
}

void main() {
  group('git_models fromJson', () {
    test('GitFileChange / untracked flag', () {
      final f = GitFileChange.fromJson(
          {'path': 'a/b.go', 'status': '?', 'staged': false});
      expect(f.path, 'a/b.go');
      expect(f.isUntracked, true);
    });

    test('GitCommitDetail nested files + totals', () {
      final d = GitCommitDetail.fromJson({
        'hash': 'abc',
        'short_hash': 'abc123',
        'author': 'T',
        'date': '1h ago',
        'message': 'feat: x',
        'files': [
          {'path': 'a.go', 'status': 'M', 'additions': 3, 'deletions': 1},
        ],
        'total_additions': 3,
        'total_deletions': 1,
      });
      expect(d.files.single.path, 'a.go');
      expect(d.totalAdditions, 3);
      expect(d.shortHash, 'abc123');
    });

    test('GitRemoteCounts defaults', () {
      final c = GitRemoteCounts.fromJson({'branch': 'main'});
      expect(c.ahead, 0);
      expect(c.behind, 0);
      expect(c.branch, 'main');
    });
  });

  group('typed bridge methods', () {
    test('gitStatusFiles parses files array', () async {
      final t = AutoRespondTransport((m, p) {
        if (m == 'git.statusFiles') {
          return {
            'files': [
              {'path': 'x.go', 'status': 'M', 'staged': true},
              {'path': 'y.txt', 'status': '?', 'staged': false},
            ]
          };
        }
        return null;
      });
      final c = CodeBridgeClient(bridgeUrl: 'http://x', connector: (_) => t);
      await c.connect();
      final files = await c.gitStatusFiles('/repo');
      expect(files.length, 2);
      expect(files[0].staged, true);
      expect(files[1].isUntracked, true);
      await c.close();
    });

    test('gitGenerateCommitMessage returns message', () async {
      final t = AutoRespondTransport((m, p) {
        if (m == 'git.generateCommitMessage') {
          return {'message': 'feat(x): do thing'};
        }
        return null;
      });
      final c = CodeBridgeClient(bridgeUrl: 'http://x', connector: (_) => t);
      await c.connect();
      expect(await c.gitGenerateCommitMessage('/repo'), 'feat(x): do thing');
      await c.close();
    });
  });

  group('GitController', () {
    CodeBridgeClient buildClient() {
      final t = AutoRespondTransport((m, p) {
        switch (m) {
          case 'git.statusFiles':
            return {
              'files': [
                {'path': 'staged.go', 'status': 'M', 'staged': true},
                {'path': 'mod.go', 'status': 'M', 'staged': false},
                {'path': 'new.txt', 'status': '?', 'staged': false},
              ]
            };
          case 'git.status':
            return {'branch': 'main', 'clean': false};
          case 'git.remoteCounts':
            return {'ahead': 2, 'behind': 0, 'branch': 'main'};
          case 'git.fileDiff':
            return {'diff': '@@ -1 +1 @@\n-old\n+new\n'};
          case 'git.commit':
            return {'output': 'committed'};
          case 'git.stage':
          case 'git.unstage':
            return {};
          default:
            return null;
        }
      });
      return CodeBridgeClient(bridgeUrl: 'http://x', connector: (_) => t);
    }

    test('refresh splits staged / unstaged + branch + counts', () async {
      final client = buildClient();
      await client.connect();
      final c = GitController(bridge: client, cwd: '/repo');
      await c.refresh();

      expect(c.state.staged.map((f) => f.path), ['staged.go']);
      expect(c.state.unstaged.map((f) => f.path).toSet(),
          {'mod.go', 'new.txt'});
      expect(c.state.branch, 'main');
      expect(c.state.counts.ahead, 2);
      expect(c.state.clean, false);
      expect(c.state.hasStaged, true);
      await client.close();
    });

    test('selectFile loads diff', () async {
      final client = buildClient();
      await client.connect();
      final c = GitController(bridge: client, cwd: '/repo');
      await c.refresh();
      await c.selectFile('mod.go', false);
      expect(c.state.selectedPath, 'mod.go');
      expect(c.state.diff, contains('+new'));
      await client.close();
    });

    test('disabled state when no cwd', () async {
      final c = GitController(bridge: null, cwd: null);
      await c.refresh();
      expect(c.state.error, isNotNull);
      expect(c.state.clean, true);
    });

    test('commit returns true and refreshes', () async {
      final client = buildClient();
      await client.connect();
      final c = GitController(bridge: client, cwd: '/repo');
      await c.refresh();
      final ok = await c.commit('feat: x');
      expect(ok, true);
      await client.close();
    });
  });
}

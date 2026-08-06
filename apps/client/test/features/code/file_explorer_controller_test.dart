// FileExplorerController 单测 —— 懒加载树扁平化 + 选中文件内容分派(文本/图片/提示)。
// 用自动应答 transport 喂 canned fs.list / fs.read / fs.imagePreview。

import 'dart:async';
import 'dart:convert';

import 'package:biumind/features/code/application/file_explorer_controller.dart';
import 'package:biumind/features/code/data/code_bridge_client.dart';
import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_test/flutter_test.dart';

class AutoRespondTransport implements CodeTransport {
  AutoRespondTransport(this.responder);
  final Map<String, dynamic>? Function(String method, Map<String, dynamic>? p)
      responder;
  final _ctrl = StreamController<dynamic>.broadcast();

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
        if (result == null) 'error': 'no canned for $method',
      }));
    });
  }

  @override
  Future<void> close() async {
    if (!_ctrl.isClosed) await _ctrl.close();
  }
}

/// pump microtasks 让 controller 的异步加载完成。
Future<void> _settle() async {
  for (var i = 0; i < 5; i++) {
    await Future<void>.delayed(Duration.zero);
  }
}

void main() {
  CodeBridgeClient buildClient() {
    final t = AutoRespondTransport((m, p) {
      final path = p?['path'] as String?;
      switch (m) {
        case 'fs.list':
          if (path == '/repo') {
            return {
              'entries': [
                {'name': 'lib', 'is_dir': true, 'size': 0},
                {'name': 'main.go', 'is_dir': false, 'size': 42},
                {'name': 'logo.png', 'is_dir': false, 'size': 100},
              ]
            };
          }
          if (path == '/repo/lib') {
            return {
              'entries': [
                {'name': 'util.go', 'is_dir': false, 'size': 10},
              ]
            };
          }
          return {'entries': []};
        case 'fs.read':
          return {'content': 'package main\n', 'size': 13, 'truncated': false};
        case 'fs.imagePreview':
          return {
            'data_url': 'data:image/png;base64,AAAA',
            'mime_type': 'image/png',
            'byte_length': 100,
          };
        default:
          return null;
      }
    });
    return CodeBridgeClient(bridgeUrl: 'http://x', connector: (_) => t);
  }

  test('initial load lists root; visibleNodes reflects it', () async {
    final client = buildClient();
    await client.connect();
    final c = FileExplorerController(bridge: client, root: '/repo');
    await _settle();

    final nodes = c.visibleNodes();
    expect(nodes.map((n) => n.name), ['lib', 'main.go', 'logo.png']);
    expect(nodes.first.isDir, true);
    expect(nodes.every((n) => n.depth == 0), true);
    await client.close();
  });

  test('toggleDir lazily loads + nests children at depth+1', () async {
    final client = buildClient();
    await client.connect();
    final c = FileExplorerController(bridge: client, root: '/repo');
    await _settle();

    await c.toggleDir('/repo/lib');
    await _settle();

    final nodes = c.visibleNodes();
    final util = nodes.firstWhere((n) => n.name == 'util.go');
    expect(util.depth, 1);
    expect(util.path, '/repo/lib/util.go');

    // 收起后子项消失。
    await c.toggleDir('/repo/lib');
    expect(c.visibleNodes().any((n) => n.name == 'util.go'), false);
    await client.close();
  });

  test('selectFile text → TextFileContent', () async {
    final client = buildClient();
    await client.connect();
    final c = FileExplorerController(bridge: client, root: '/repo');
    await _settle();

    await c.selectFile('/repo/main.go', size: 42);
    expect(c.state.content, isA<TextFileContent>());
    expect((c.state.content as TextFileContent).text, contains('package main'));
    await client.close();
  });

  test('selectFile image → ImageFileContent', () async {
    final client = buildClient();
    await client.connect();
    final c = FileExplorerController(bridge: client, root: '/repo');
    await _settle();

    await c.selectFile('/repo/logo.png', size: 100);
    expect(c.state.content, isA<ImageFileContent>());
    expect((c.state.content as ImageFileContent).preview.mimeType, 'image/png');
    await client.close();
  });

  test('oversized file → NoticeFileContent without read', () async {
    final client = buildClient();
    await client.connect();
    final c = FileExplorerController(bridge: client, root: '/repo');
    await _settle();

    await c.selectFile('/repo/big.bin', size: 5 << 20);
    expect(c.state.content, isA<NoticeFileContent>());
    await client.close();
  });

  test('disabled when no root', () async {
    final c = FileExplorerController(bridge: null, root: null);
    await _settle();
    expect(c.visibleNodes(), isEmpty);
  });
}

// 独立 shell 终端实机 e2e:真 biu serve + pty.open($SHELL -l) + 发命令 + 看回显。
//
// 验证「终端」Tab 背后的链路:ShellTerminalController.open 用的 pty.open(开任意
// 命令、server 自分配 pty_id)在真 daemon 上能拉起真登录 shell,字节流双向通 ——
// 这跟任务(code.runTask)无关,证明终端是独立的。
//
// 只门控 BIU_BIN(不需要 claude)。本地手动跑:
//   (cd apps/cli/biu && go build -o /tmp/biu ./cmd/biu)
//   BIU_BIN=/tmp/biu flutter test test/features/code/shell_terminal_e2e_test.dart

import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:biumind/features/code/data/code_bridge_client.dart';
import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  final biuBin = Platform.environment['BIU_BIN'];
  if (biuBin == null || !File(biuBin).existsSync()) {
    test('shell terminal e2e (skipped: set BIU_BIN to a built biu binary)', () {},
        skip: 'BIU_BIN not set or not found');
    return;
  }

  late Process serve;
  late String bridgeUrl;
  late Directory workDir;

  setUpAll(() async {
    workDir = await Directory.systemTemp.createTemp('code_shell_e2e_');
    serve = await Process.start(
      biuBin,
      ['serve', '--port', '0'],
      environment: {
        ...Platform.environment,
        'BIUMIND_MODEL_RELAY_URL': 'http://127.0.0.1:9',
        'BIUMIND_TOKEN': 'dummy-e2e',
      },
    );
    final urlCompleter = Completer<String>();
    serve.stdout
        .transform(utf8.decoder)
        .transform(const LineSplitter())
        .listen((line) {
      if (line.startsWith('BIU_BRIDGE_URL=') && !urlCompleter.isCompleted) {
        urlCompleter.complete(line.substring('BIU_BRIDGE_URL='.length).trim());
      }
    });
    serve.stderr.drain<void>();
    bridgeUrl = await urlCompleter.future
        .timeout(const Duration(seconds: 10), onTimeout: () {
      throw StateError('biu serve did not print BIU_BRIDGE_URL within 10s');
    });
  });

  tearDownAll(() async {
    serve.kill(ProcessSignal.sigterm);
    await serve.exitCode.timeout(const Duration(seconds: 5), onTimeout: () {
      serve.kill(ProcessSignal.sigkill);
      return -1;
    });
    await workDir.delete(recursive: true);
  });

  test('pty.open with \$SHELL -l runs a real login shell; command echoes back',
      () async {
    final client = CodeBridgeClient(bridgeUrl: bridgeUrl);
    await client.connect();
    addTearDown(client.close);

    final shell = Platform.environment['SHELL'] ??
        (Platform.isMacOS ? '/bin/zsh' : '/bin/bash');

    // 与 ShellTerminalController.open 同款调用。
    final ptyId = await client.openPty(
      cmd: shell,
      args: const ['-l'],
      cwd: workDir.path,
      cols: 100,
      rows: 30,
    );
    expect(ptyId, isNotEmpty);

    final seen = StringBuffer();
    final marked = Completer<void>();
    final sub = client.ptyChunks.listen((c) {
      if (c.ptyId != ptyId) return;
      seen.write(utf8.decode(c.data, allowMalformed: true));
      if (seen.toString().contains('BIU_SHELL_OK') && !marked.isCompleted) {
        marked.complete();
      }
    });
    addTearDown(sub.cancel);

    // 在真 shell 里跑命令,看输出回流。
    client.sendInput(ptyId, utf8.encode('echo BIU_SHELL_OK\n'));
    await marked.future.timeout(const Duration(seconds: 10), onTimeout: () {
      throw StateError('did not see shell command output; got "${seen.toString()}"');
    });

    expect(seen.toString(), contains('BIU_SHELL_OK'));
    await client.killPty(ptyId);
  }, timeout: const Timeout(Duration(seconds: 30)));
}

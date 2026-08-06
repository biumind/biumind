// M2 DoD 端到端验证:Flutter 侧用真实 CodeBridgeClient 连真实 `biu serve`,经
// code.runTask 在真 PTY 里拉起真实 Claude Code,观测其终端字节流(code_pty_chunk)
// 实时回推 —— 即「点新建任务 → 真实跑 Claude Code → 终端实时输出」的自动化下半场
// (GUI 点击那一步无法无头驱动,此处锁定其下方的全链路:WS→detect→BuildLaunch→
//  pty.Open(claude)→字节流回推)。
//
// 跨进程 / 跨语言 / 跨真实二进制 e2e。需预构建 biu 二进制经 BIU_BIN 注入,且本机
// PATH 上有真实 `claude`(exec.LookPath 可达,如 ~/.local/bin/claude)。未满足则 skip。
//
// 本地手动跑:
//   (cd apps/cli/biu && go build -o /tmp/biu ./cmd/biu)
//   BIU_BIN=/tmp/biu flutter test test/features/code/code_runtask_e2e_test.dart
//
// 设计要点:不要求 claude 成功完成编码任务(那会真调模型 / 花钱 / 不确定),只要求
// 看到它的 *真实终端输出在流动* —— 任意字节(banner / TUI 帧 / 即便是认证错误)都
// 证明 PTY 字节流链路通。观测到首块后立即 killPty,保持有界、零残留。

import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:biumind/features/code/data/code_bridge_client.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  final biuBin = Platform.environment['BIU_BIN'];
  // exec.LookPath 等价探测:PATH 上有真实 claude 可执行?
  final claudeOnPath = _which('claude');

  if (biuBin == null || !File(biuBin).existsSync()) {
    test('runTask e2e (skipped: set BIU_BIN to a built biu binary)', () {},
        skip: 'BIU_BIN not set or not found');
    return;
  }
  if (claudeOnPath == null) {
    test('runTask e2e (skipped: no real `claude` binary on PATH)', () {},
        skip: 'claude not found on PATH (exec.LookPath would fail)');
    return;
  }

  late Process serve;
  late String bridgeUrl;
  late Directory repoDir;

  setUpAll(() async {
    repoDir = await Directory.systemTemp.createTemp('code_m2_e2e_');
    await Process.run('git', ['-C', repoDir.path, 'init']);
    await File('${repoDir.path}/README.md').writeAsString('# e2e fixture\n');

    serve = await Process.start(
      biuBin,
      ['serve', '--port', '0'],
      environment: {
        ...Platform.environment,
        // 编码模块不调 LLM;dummy relay 让 cloud-mode SDK probe 通过。
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
    await repoDir.delete(recursive: true);
  });

  test('code.runTask spawns real Claude Code in a PTY; bytes stream live',
      () async {
    final client = CodeBridgeClient(bridgeUrl: bridgeUrl);
    await client.connect();
    addTearDown(client.close);

    const taskId = 'e2e-task-claude-1';
    final seen = StringBuffer();
    final firstChunk = Completer<void>();
    final exited = Completer<int>();

    final chunkSub = client.ptyChunks.listen((c) {
      if (c.ptyId != taskId) return;
      seen.write(utf8.decode(c.data, allowMalformed: true));
      if (!firstChunk.isCompleted) firstChunk.complete();
    });
    final exitSub = client.ptyExits.listen((e) {
      if (e.ptyId == taskId && !exited.isCompleted) exited.complete(e.exitCode);
    });
    addTearDown(() async {
      await chunkSub.cancel();
      await exitSub.cancel();
    });

    // 真实拉起 claude:ask 模式(--permission-mode default,不自动改文件),给一个
    // 无害只读 prompt。pty_id 应 = taskId。
    await client.runTask(
      taskId: taskId,
      agentType: 'claude',
      permissionMode: 'ask',
      prompt: 'Reply with the single word READY and stop.',
      cwd: repoDir.path,
      cols: 100,
      rows: 30,
    );

    // 观测真实 claude 终端输出在流动(任意字节即证明链路通)。冷启动给足时间。
    await firstChunk.future.timeout(const Duration(seconds: 20), onTimeout: () {
      throw StateError(
          'no PTY bytes from claude within 20s; captured so far: "${seen.toString()}"');
    });

    // ignore: avoid_print
    print('── 真实 Claude Code PTY 首批输出(前 240 字节)──\n'
        '${_preview(seen.toString(), 240)}\n────────────────────────────');

    expect(seen.isNotEmpty, true,
        reason: 'should have observed live PTY bytes from real claude');

    // 有界收尾:杀掉进程,观测 exit 帧(归一化链路的另一半)。
    await client.killPty(taskId);
    final code = await exited.future.timeout(
      const Duration(seconds: 8),
      onTimeout: () => -999, // 没收到 exit 也不让测试卡死;字节流已证明 DoD
    );
    // ignore: avoid_print
    print('claude PTY 退出 code=$code (kill 后)');
  }, timeout: const Timeout(Duration(seconds: 45)));
}

/// exec.LookPath 等价:在 PATH 各目录找可执行 [bin]。返回首个命中绝对路径或 null。
String? _which(String bin) {
  final pathEnv = Platform.environment['PATH'] ?? '';
  for (final dir in pathEnv.split(Platform.isWindows ? ';' : ':')) {
    if (dir.isEmpty) continue;
    final cand = File('$dir${Platform.pathSeparator}$bin');
    if (cand.existsSync()) {
      try {
        final mode = cand.statSync().mode;
        if (Platform.isWindows || (mode & 0x49) != 0) return cand.path; // 0o111
      } catch (_) {/* skip */}
    }
  }
  return null;
}

String _preview(String s, int max) {
  final clean = s.replaceAll('\x1b', '·'); // ESC 可视化,免污染终端
  return clean.length <= max ? clean : '${clean.substring(0, max)}…';
}

// M0 DoD 端到端验证：Flutter 侧用真实 CodeBridgeClient 连真实 `biu serve` 的
// loopback bridge，跑通 PTY echo + git.status + fs.read（不绕云端）。
//
// 这是跨进程 / 跨语言 e2e：spawn 真 biu 二进制 → 解析 BIU_BRIDGE_URL → 真 WS。
// 需要预构建的 biu 二进制路径经 BIU_BIN 环境变量传入；未设或文件不存在则整组
// skip（CI 在装好 Go 工具链的 job 里 `go build` 后注入 BIU_BIN 即可跑）。
//
// 本地手动跑：
//   (cd apps/cli/biu && go build -o /tmp/biu ./cmd/biu)
//   BIU_BIN=/tmp/biu flutter test test/features/code/code_bridge_loopback_test.dart

import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:biumind/features/code/data/code_bridge_client.dart';
import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  final biuBin = Platform.environment['BIU_BIN'];
  if (biuBin == null || !File(biuBin).existsSync()) {
    test('loopback e2e (skipped: set BIU_BIN to a built biu binary)', () {},
        skip: 'BIU_BIN not set or not found');
    return;
  }

  late Process serve;
  late String bridgeUrl;
  late Directory repoDir;

  setUpAll(() async {
    // 临时 git 仓库 + 一个未跟踪文件，供 git.status / fs.read 用。
    repoDir = await Directory.systemTemp.createTemp('code_m0_e2e_');
    await Process.run('git', ['-C', repoDir.path, 'init']);
    await File('${repoDir.path}/hello.txt').writeAsString('loopback-body-99');

    // 起 biu serve --port 0。注 dummy relay 凭证让 cloud-mode SDK probe 通过
    // （编码模块本身不调 LLM；详见 M0 调研笔记）。
    serve = await Process.start(
      biuBin,
      ['serve', '--port', '0'],
      environment: {
        ...Platform.environment,
        'BIUMIND_MODEL_RELAY_URL': 'http://127.0.0.1:9',
        'BIUMIND_TOKEN': 'dummy-e2e',
      },
    );

    // 解析 stdout 第一行的 BIU_BRIDGE_URL=...
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

  test('git.status + fs.read + PTY echo over real loopback bridge', () async {
    final client = CodeBridgeClient(bridgeUrl: bridgeUrl);
    await client.connect();
    addTearDown(client.close);

    // 1) git.status —— 未跟踪文件应出现
    final status = await client.gitStatus(repoDir.path);
    expect(status['clean'], false, reason: 'repo has an untracked file');
    expect((status['untracked'] as List).cast<String>(), contains('hello.txt'));

    // 2) fs.read —— 内容回来
    final read = await client.fsRead('${repoDir.path}/hello.txt');
    expect(read['content'], 'loopback-body-99');
    expect(read['truncated'], false);

    // 3) PTY echo —— open cat、发输入、PTY 行规程回显
    final ptyId = await client.openPty(cmd: 'cat');
    final seen = StringBuffer();
    final done = Completer<void>();
    final sub = client.ptyChunks.listen((c) {
      if (c.ptyId != ptyId) return;
      seen.write(utf8.decode(c.data, allowMalformed: true));
      if (seen.toString().contains('echo-loopback') && !done.isCompleted) {
        done.complete();
      }
    });
    client.sendInput(ptyId, utf8.encode('echo-loopback\n'));
    await done.future.timeout(const Duration(seconds: 5), onTimeout: () {
      throw StateError('did not observe PTY echo; got "${seen.toString()}"');
    });
    await sub.cancel();
    await client.killPty(ptyId);
  });
}

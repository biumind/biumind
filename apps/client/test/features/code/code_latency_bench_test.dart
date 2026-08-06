// loopback 延迟基准 —— 实测 Code 桌面直连的两层延迟:
//   ① 纯传输 RPC 往返(pty.active):WS + 成帧 + dispatch(服务端读 map,近零耗时)
//      → 直接验证设计目标「PTY 字节流 loopback < 5ms」的传输底座。
//   ② PTY echo 往返(cat):sendInput → PTY 行规程回显 → 读循环 → batcher → WS 回
//      → 用户真实感知的「输入→屏幕」延迟,含 runBatcher 的 16ms flush 代价。
//
// 不是 pass/fail 断言测试,是测量 + 打印分布(min/p50/p90/p99/max)。只门控 BIU_BIN。
// 本地跑:
//   (cd apps/cli/biu && go build -o /tmp/biu ./cmd/biu)
//   BIU_BIN=/tmp/biu flutter test test/features/code/code_latency_bench_test.dart

import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:biumind/features/code/data/code_bridge_client.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  final biuBin = Platform.environment['BIU_BIN'];
  if (biuBin == null || !File(biuBin).existsSync()) {
    test('latency bench (skipped: set BIU_BIN to a built biu binary)', () {},
        skip: 'BIU_BIN not set or not found');
    return;
  }

  late Process serve;
  late String bridgeUrl;

  setUpAll(() async {
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
  });

  test('① 纯传输 RPC 往返 (pty.active) — 验证 <5ms loopback', () async {
    final client = CodeBridgeClient(bridgeUrl: bridgeUrl);
    await client.connect();
    addTearDown(client.close);

    const warmup = 20;
    const iters = 300;
    for (var i = 0; i < warmup; i++) {
      await client.ptyActive();
    }
    final samples = <int>[]; // microseconds
    for (var i = 0; i < iters; i++) {
      final sw = Stopwatch()..start();
      await client.ptyActive();
      sw.stop();
      samples.add(sw.elapsedMicroseconds);
    }
    _report('① RPC 往返 (pty.active, 纯传输+成帧+dispatch)', samples, iters);
  }, timeout: const Timeout(Duration(seconds: 30)));

  test('② PTY echo 往返 (cat) — 输入→屏幕,含 16ms batcher', () async {
    final client = CodeBridgeClient(bridgeUrl: bridgeUrl);
    await client.connect();
    addTearDown(client.close);

    final ptyId = await client.openPty(cmd: 'cat');

    // 单一监听器:按当前 trial 的 token 累积匹配。
    var buf = StringBuffer();
    Completer<void>? hit;
    String? token;
    final sub = client.ptyChunks.listen((c) {
      if (c.ptyId != ptyId) return;
      buf.write(utf8.decode(c.data, allowMalformed: true));
      if (token != null && buf.toString().contains(token!) && hit != null && !hit!.isCompleted) {
        hit!.complete();
      }
    });
    addTearDown(sub.cancel);

    Future<int> roundTrip(int i) async {
      token = 'M${i}Z';
      buf = StringBuffer();
      hit = Completer<void>();
      final sw = Stopwatch()..start();
      client.sendInput(ptyId, utf8.encode('$token\n'));
      await hit!.future.timeout(const Duration(seconds: 5));
      sw.stop();
      return sw.elapsedMicroseconds;
    }

    const warmup = 10;
    const iters = 120;
    for (var i = 0; i < warmup; i++) {
      await roundTrip(i);
    }
    final samples = <int>[];
    for (var i = 0; i < iters; i++) {
      samples.add(await roundTrip(warmup + i));
      // 间隔 > flushInterval(16ms),模拟真实孤立打字:键与键之间 batcher 已回空闲,
      // 每次按键都走「首字节直发」。人类打字键间 ~100ms+,这里 30ms 已足够。
      await Future<void>.delayed(const Duration(milliseconds: 30));
    }
    _report('② PTY echo 往返 (cat, 含 batcher 16ms flush)', samples, iters);

    await client.killPty(ptyId);
  }, timeout: const Timeout(Duration(seconds: 60)));
}

void _report(String title, List<int> usMicros, int n) {
  final sorted = [...usMicros]..sort();
  double ms(int us) => us / 1000.0;
  int pct(int p) => sorted[((p / 100.0) * (sorted.length - 1)).round()];
  final sum = sorted.fold<int>(0, (a, b) => a + b);
  final mean = sum / sorted.length;
  // ignore: avoid_print
  print('\n──────── $title ────────\n'
      '  样本数      : $n\n'
      '  min        : ${ms(sorted.first).toStringAsFixed(3)} ms\n'
      '  mean       : ${ms(mean.round()).toStringAsFixed(3)} ms\n'
      '  p50        : ${ms(pct(50)).toStringAsFixed(3)} ms\n'
      '  p90        : ${ms(pct(90)).toStringAsFixed(3)} ms\n'
      '  p99        : ${ms(pct(99)).toStringAsFixed(3)} ms\n'
      '  max        : ${ms(sorted.last).toStringAsFixed(3)} ms\n'
      '────────────────────────────────────────');
}

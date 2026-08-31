// DocprocBridgeController + 协议序列化测试（不依赖真 WebView）。
//
// 锁的语义：
//   * parse 在 ready 未到时排队，ready 到达后才发 parse 消息；
//   * result / error 按 id 路由到对应 Future；
//   * TS 错误码（unsupported/encrypted/corrupt/oom）映射为
//     DocprocException(code, retryable)；
//   * parseTimeout 超时 → code: timeout, retryable: true；
//   * cancel → 发 cancel 消息 + Future 以 cancelled 失败；
//   * hasLocalDocproc=false 的平台 → UnsupportedError。

import 'dart:convert' show base64Encode;

import 'package:biumind/core/docproc/docproc_bridge_controller.dart';
import 'package:biumind/core/docproc/docproc_bridge_protocol.dart';
import 'package:biumind/core/platform/platform_caps.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter_test/flutter_test.dart';

PlatformCaps _caps({bool localDocproc = true}) => PlatformCaps(
      hasLocalPty: true,
      hasFileSystem: true,
      hasNotifications: true,
      supportsBackgroundIsolates: true,
      hasPersistentSqlite: true,
      hasEmbeddedWebView: true,
      hasRepoAppRunner: false,
      hasLocalDocproc: localDocproc,
    );

void main() {
  group('DocprocMessage', () {
    test('toJson/fromJson 往返', () {
      final msg = parseMessage(
        id: 'p1',
        fileName: 'a.pdf',
        mimeHint: 'application/pdf',
        dataBase64: base64Encode(Uint8List.fromList([1, 2, 3])),
      );
      final json = msg.toJson();
      expect(json['type'], 'parse');
      expect(json['v'], kDocprocProtocolVersion);
      expect(json['id'], 'p1');
      expect(json['payload']['fileName'], 'a.pdf');
      expect(json['payload']['mimeHint'], 'application/pdf');

      final back = DocprocMessage.fromJson(json);
      expect(back.type, 'parse');
      expect(back.id, 'p1');
      expect(back.payload['dataBase64'], 'AQID');
    });

    test('mimeHint 缺省时不出现在 payload', () {
      final msg = parseMessage(id: 'p1', fileName: 'a.txt', dataBase64: 'eA==');
      expect(msg.payload.containsKey('mimeHint'), isFalse);
    });

    test('ping / cancel 形态', () {
      expect(pingMessage().type, 'ping');
      final c = cancelMessage('p1');
      expect(c.type, 'cancel');
      expect(c.payload['id'], 'p1');
    });

    test('缺字段抛 FormatException', () {
      expect(
        () => DocprocMessage.fromJson({'type': 'ready'}),
        throwsFormatException,
      );
    });
  });

  group('DocprocResult.fromJson', () {
    test('完整字段', () {
      final r = DocprocResult.fromJson({
        'text': 'hello',
        'format': 'pdf',
        'pageCount': 3,
        'parserVersion': 'docproc-web@0.1.0',
        'warnings': ['no-text-layer: x'],
      });
      expect(r.text, 'hello');
      expect(r.format, 'pdf');
      expect(r.pageCount, 3);
      expect(r.parserVersion, 'docproc-web@0.1.0');
      expect(r.warnings, ['no-text-layer: x']);
    });

    test('缺省字段有安全默认', () {
      final r = DocprocResult.fromJson({'text': 'x', 'format': 'txt'});
      expect(r.pageCount, isNull);
      expect(r.warnings, isEmpty);
    });
  });

  group('DocprocBridgeController', () {
    late DocprocBridgeController controller;
    late List<DocprocMessage> sent;

    setUp(() {
      sent = <DocprocMessage>[];
      controller = DocprocBridgeController(
        caps: _caps(),
        parseTimeout: const Duration(milliseconds: 200),
        readyTimeout: const Duration(milliseconds: 200),
      );
      controller.attach((msg) async => sent.add(msg));
    });

    tearDown(() => controller.detach());

    void deliverReady() {
      controller.onIncomingMessage(
        const DocprocMessage(
          type: 'ready',
          payload: {
            'version': 'docproc-web@0.1.0',
            'formats': ['pdf', 'docx', 'html', 'md', 'txt'],
          },
        ),
      );
    }

    test('attach 时主动 ping', () {
      expect(sent.single.type, 'ping');
    });

    test('ready 前 parse 排队，ready 后才发 parse 消息', () async {
      var parseSent = false;
      final future = controller
          .parse(fileName: 'a.txt', bytes: Uint8List.fromList([120]))
          .then((r) {
        expect(r.text, 'x');
        return r;
      });
      // 让事件循环转过 attach 的 ping；parse 应仍在等 ready。
      await Future<void>.delayed(Duration.zero);
      expect(sent.where((m) => m.type == 'parse'), isEmpty);
      expect(parseSent, isFalse);

      deliverReady();
      await Future<void>.delayed(Duration.zero);
      final parse = sent.where((m) => m.type == 'parse').single;
      expect(parse.payload['fileName'], 'a.txt');

      controller.onIncomingMessage(
        DocprocMessage(
          type: 'result',
          id: parse.id,
          payload: {
            'text': 'x',
            'format': 'txt',
            'parserVersion': 'docproc-web@0.1.0',
            'warnings': <String>[],
          },
        ),
      );
      await future;
    });

    test('ready 记录 bundle 版本与格式清单', () {
      deliverReady();
      expect(controller.bundleVersion, 'docproc-web@0.1.0');
      expect(controller.formats, containsAll(['pdf', 'docx']));
    });

    test('error 消息映射为 DocprocException', () async {
      deliverReady();
      final future = controller.parse(
        fileName: 'locked.pdf',
        bytes: Uint8List.fromList([1]),
      );
      await Future<void>.delayed(Duration.zero);
      final parse = sent.where((m) => m.type == 'parse').single;
      controller.onIncomingMessage(
        DocprocMessage(
          type: 'error',
          id: parse.id,
          payload: {
            'code': 'encrypted',
            'message': 'PDF 已加密',
            'retryable': false,
          },
        ),
      );
      final err = await future
          .then<Object?>((_) => null)
          .catchError((Object e) => e);
      expect(err, isA<DocprocException>());
      expect((err as DocprocException).code, 'encrypted');
      expect(err.retryable, isFalse);
    });

    test('parse 超时 → code timeout + retryable', () async {
      deliverReady();
      final err = await controller
          .parse(fileName: 'a.txt', bytes: Uint8List.fromList([1]))
          .then<Object?>((_) => null)
          .catchError((Object e) => e);
      expect(err, isA<DocprocException>());
      expect((err as DocprocException).code, 'timeout');
      expect(err.retryable, isTrue);
    });

    test('cancel 发 cancel 消息且 future 以 cancelled 失败', () async {
      deliverReady();
      final future = controller.parse(
        fileName: 'a.txt',
        bytes: Uint8List.fromList([1]),
      );
      await Future<void>.delayed(Duration.zero);
      final parse = sent.where((m) => m.type == 'parse').single;
      controller.cancel(parse.id!);
      expect(sent.last.type, 'cancel');
      expect(sent.last.payload['id'], parse.id);
      final err = await future
          .then<Object?>((_) => null)
          .catchError((Object e) => e);
      expect((err as DocprocException).code, 'cancelled');
    });

    test('progress 回调按 id 透传', () async {
      deliverReady();
      final seen = <String>[];
      controller.onProgress = (id, phase, percent) {
        seen.add('$phase:$percent');
      };
      final future = controller.parse(
        fileName: 'a.pdf',
        bytes: Uint8List.fromList([1]),
      );
      await Future<void>.delayed(Duration.zero);
      final parse = sent.where((m) => m.type == 'parse').single;
      controller.onIncomingMessage(
        DocprocMessage(
          type: 'progress',
          id: parse.id,
          payload: {'phase': 'extract', 'percent': 42},
        ),
      );
      controller.onIncomingMessage(
        DocprocMessage(
          type: 'result',
          id: parse.id,
          payload: {
            'text': 'x',
            'format': 'pdf',
            'parserVersion': 'v',
            'warnings': <String>[],
          },
        ),
      );
      await future;
      expect(seen, ['extract:42']);
    });

    test('hasLocalDocproc=false 抛 UnsupportedError', () {
      final c = DocprocBridgeController(caps: _caps(localDocproc: false));
      c.attach((msg) async {});
      expect(c.ensureReady(), throwsA(isA<UnsupportedError>()));
    });

    test('版本不匹配的消息被丢弃', () {
      deliverReady();
      controller.onIncomingMessage(
        const DocprocMessage(type: 'error', v: 99, payload: {}),
      );
      // 不抛异常即通过（debugPrint 后丢弃）。
    });
  });
}

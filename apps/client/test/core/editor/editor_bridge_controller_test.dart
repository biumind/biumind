// EditorBridgeController 的 presignGet 请求/回复测试。
//
// 渲染时解析链路：编辑器渲染 biu-file:// 图片前发 `presignGet` 请求，
// controller 调宿主的 resolvePresignGet 换临时 URL 并回
// `presignGet.reply`。锁的语义：
//   * 正常换取：reply 带同一 id 和 URL；
//   * resolver 抛异常 / 未接线：reply 回空串（编辑器按失败处理，不崩）；
//   * 请求缺 id 或 fileId 非法：直接忽略，不回复。

import 'package:biumind/core/editor/editor_bridge_controller.dart';
import 'package:biumind/core/editor/editor_bridge_protocol.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  group('presignGet', () {
    late EditorBridgeController controller;
    late List<BridgeMessage> sent;

    setUp(() {
      sent = <BridgeMessage>[];
      controller = EditorBridgeController(
        initialMarkdown: '',
        theme: BridgeTheme.light,
      );
      controller.attach((msg) async => sent.add(msg));
    });

    BridgeMessage presignGetRequest({String? id = 'r1', Object? fileId}) {
      return BridgeMessage(
        type: 'presignGet',
        id: id,
        payload: {'fileId': ?fileId},
      );
    }

    test('正常换取：reply 带同一 id 和临时 URL', () async {
      controller.resolvePresignGet =
          (fileId) async => 'https://minio.example/obj?sig=$fileId';
      await controller.onIncomingMessage(
        presignGetRequest(fileId: 'file-1'),
      );
      expect(sent, hasLength(1));
      final reply = sent.single;
      expect(reply.type, 'presignGet.reply');
      expect(reply.id, 'r1');
      expect(
        reply.payload['url'],
        'https://minio.example/obj?sig=file-1',
      );
    });

    test('resolver 抛异常：reply 回空串', () async {
      controller.resolvePresignGet = (_) async => throw StateError('未连接');
      await controller.onIncomingMessage(
        presignGetRequest(fileId: 'file-1'),
      );
      expect(sent.single.payload['url'], '');
    });

    test('resolver 未接线：reply 回空串', () async {
      await controller.onIncomingMessage(
        presignGetRequest(fileId: 'file-1'),
      );
      expect(sent.single.type, 'presignGet.reply');
      expect(sent.single.payload['url'], '');
    });

    test('请求缺 id：忽略不回复', () async {
      controller.resolvePresignGet = (_) async => 'x';
      await controller.onIncomingMessage(
        presignGetRequest(id: null, fileId: 'file-1'),
      );
      expect(sent, isEmpty);
    });

    test('fileId 非字符串：忽略不回复', () async {
      controller.resolvePresignGet = (_) async => 'x';
      await controller.onIncomingMessage(presignGetRequest(fileId: 42));
      expect(sent, isEmpty);
    });
  });
}

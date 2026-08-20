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

  group('docEpoch（跨笔记防串内容）', () {
    late EditorBridgeController controller;
    late List<BridgeMessage> sent;
    late List<String> changed;

    setUp(() {
      sent = <BridgeMessage>[];
      changed = <String>[];
      controller = EditorBridgeController(
        initialMarkdown: 'A',
        theme: BridgeTheme.light,
      );
      controller.attach((msg) async => sent.add(msg));
      controller.onMarkdownChanged = changed.add;
    });

    BridgeMessage docChanged(String md, int revision, [int? epoch]) {
      return BridgeMessage(
        type: 'docChanged',
        payload: {
          'markdown': md,
          'revision': revision,
          'epoch': ?epoch,
        },
      );
    }

    test('当前纪元的 docChanged 放行', () async {
      await controller.onIncomingMessage(docChanged('A edit', 1, 0));
      expect(changed, ['A edit']);
    });

    test('setDoc 后旧纪元的迟到 docChanged 被丢弃，新纪元放行', () async {
      await controller.setDoc('B'); // _hostRevision → 1，纪元切换
      // 上一篇笔记的迟到变更（防抖/在途 postMessage，epoch 0）——即使
      // revision 计数很大也丢。
      await controller.onIncomingMessage(docChanged('A 的内容', 99, 0));
      expect(changed, isEmpty);
      // 新笔记上的正常编辑（epoch 1）放行。
      await controller.onIncomingMessage(docChanged('B edit', 100, 1));
      expect(changed, ['B edit']);
    });

    test('旧版编辑器（无 epoch 字段）退回 revision 守卫', () async {
      await controller.setDoc('B'); // _hostRevision → 1
      await controller.onIncomingMessage(docChanged('stale', 0));
      expect(changed, isEmpty);
      await controller.onIncomingMessage(docChanged('fresh', 1));
      expect(changed, ['fresh']);
    });

    test('未 attach 时 setDoc 不推进纪元（防纪元失配误杀）', () async {
      controller.detach();
      await controller.setDoc('B');
      expect(sent, isEmpty);
      // 纪元仍是 0，编辑器的 epoch 0 变更应照常放行。
      await controller.onIncomingMessage(docChanged('edit', 1, 0));
      expect(changed, ['edit']);
    });
  });
}

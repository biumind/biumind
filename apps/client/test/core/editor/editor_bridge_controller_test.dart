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
import 'package:biumind/core/editor/editor_locale.dart';
import 'package:flutter/services.dart';
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

  group('clipboard（自绘右键菜单）', () {
    late EditorBridgeController controller;
    late List<BridgeMessage> sent;

    setUp(() {
      TestWidgetsFlutterBinding.ensureInitialized();
      sent = <BridgeMessage>[];
      controller = EditorBridgeController(
        initialMarkdown: '',
        theme: BridgeTheme.light,
      );
      controller.attach((msg) async => sent.add(msg));
    });

    test('clipboardWrite：默认实现写系统剪贴板', () async {
      String? written;
      TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
          .setMockMethodCallHandler(SystemChannels.platform, (call) async {
        if (call.method == 'Clipboard.setData') {
          written = (call.arguments as Map)['text'] as String?;
        }
        return null;
      });
      await controller.onIncomingMessage(
        BridgeMessage(type: 'clipboardWrite', payload: {'text': 'md **b**'}),
      );
      expect(written, 'md **b**');
      TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
          .setMockMethodCallHandler(SystemChannels.platform, null);
    });

    test('clipboardWrite：宿主注入 handler 时走 handler（含 html）', () async {
      final received = <List<String?>>[];
      controller.onClipboardWrite =
          (text, html) async => received.add([text, html]);
      await controller.onIncomingMessage(
        BridgeMessage(
          type: 'clipboardWrite',
          payload: {'text': 'abc', 'html': '<p>abc</p>'},
        ),
      );
      expect(received, [
        ['abc', '<p>abc</p>'],
      ]);
    });

    test('clipboardWrite：text 非字符串忽略', () async {
      await controller.onIncomingMessage(
        BridgeMessage(type: 'clipboardWrite', payload: {'text': 42}),
      );
      // 不崩、无回复
      expect(sent, isEmpty);
    });

    test('clipboardRead：reply 带同一 id 和剪贴板文本', () async {
      TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
          .setMockMethodCallHandler(SystemChannels.platform, (call) async {
        if (call.method == 'Clipboard.getData') {
          return <String, dynamic>{'text': 'pasted'};
        }
        return null;
      });
      await controller.onIncomingMessage(
        BridgeMessage(type: 'clipboardRead', id: 'c1', payload: const {}),
      );
      expect(sent, hasLength(1));
      expect(sent.single.type, 'clipboardRead.reply');
      expect(sent.single.id, 'c1');
      expect(sent.single.payload['text'], 'pasted');
      TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
          .setMockMethodCallHandler(SystemChannels.platform, null);
    });

    test('clipboardRead：剪贴板为空回 text null（粘贴置灰）', () async {
      TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
          .setMockMethodCallHandler(SystemChannels.platform, (call) async {
        if (call.method == 'Clipboard.getData') {
          return <String, dynamic>{'text': ''};
        }
        return null;
      });
      await controller.onIncomingMessage(
        BridgeMessage(type: 'clipboardRead', id: 'c2', payload: const {}),
      );
      expect(sent.single.payload['text'], isNull);
      TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
          .setMockMethodCallHandler(SystemChannels.platform, null);
    });

    test('clipboardRead：缺 id 不回复', () async {
      await controller.onIncomingMessage(
        BridgeMessage(type: 'clipboardRead', payload: const {}),
      );
      expect(sent, isEmpty);
    });

    test('clipboardWrite 带 html + 双格式成功：走 channel，不写纯文本', () async {
      var plainWritten = false;
      TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
          .setMockMethodCallHandler(SystemChannels.platform, (call) async {
        if (call.method == 'Clipboard.setData') plainWritten = true;
        return null;
      });
      final richCalls = <List<String>>[];
      controller.richClipboardWriter = (text, html) async {
        richCalls.add([text, html]);
        return true;
      };
      await controller.onIncomingMessage(
        BridgeMessage(
          type: 'clipboardWrite',
          payload: {'text': 'md **b**', 'html': '<p>md <strong>b</strong></p>'},
        ),
      );
      expect(richCalls, [
        ['md **b**', '<p>md <strong>b</strong></p>'],
      ]);
      expect(plainWritten, isFalse);
      TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
          .setMockMethodCallHandler(SystemChannels.platform, null);
    });

    test('clipboardWrite 带 html 但双格式失败/不支持：回退纯文本', () async {
      String? plainText;
      TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
          .setMockMethodCallHandler(SystemChannels.platform, (call) async {
        if (call.method == 'Clipboard.setData') {
          plainText = (call.arguments as Map)['text'] as String?;
        }
        return null;
      });
      controller.richClipboardWriter = (_, _) async => false;
      await controller.onIncomingMessage(
        BridgeMessage(
          type: 'clipboardWrite',
          payload: {'text': 'md', 'html': '<p>md</p>'},
        ),
      );
      expect(plainText, 'md');
      TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
          .setMockMethodCallHandler(SystemChannels.platform, null);
    });

    test('clipboardWrite 无 html：直接纯文本（不调 channel）', () async {
      String? plainText;
      TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
          .setMockMethodCallHandler(SystemChannels.platform, (call) async {
        if (call.method == 'Clipboard.setData') {
          plainText = (call.arguments as Map)['text'] as String?;
        }
        return null;
      });
      var richCalled = false;
      controller.richClipboardWriter = (_, _) async {
        richCalled = true;
        return true;
      };
      await controller.onIncomingMessage(
        BridgeMessage(type: 'clipboardWrite', payload: {'text': 'plain'}),
      );
      expect(plainText, 'plain');
      expect(richCalled, isFalse);
      TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
          .setMockMethodCallHandler(SystemChannels.platform, null);
    });
  });

  group('imageUpload（替换图片 P2）', () {
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

    BridgeMessage imageUploadRequest({String? id = 'u1'}) {
      return BridgeMessage(type: 'imageUpload', id: id, payload: const {});
    }

    test('resolver 返回规范 URI：reply 带同一 id 和 uri', () async {
      controller.resolveImageUpload =
          () async => 'biu-file://12345678-1234-1234-1234-123456789abc';
      await controller.onIncomingMessage(imageUploadRequest());
      expect(sent.single.type, 'imageUpload.reply');
      expect(sent.single.id, 'u1');
      expect(
        sent.single.payload['uri'],
        'biu-file://12345678-1234-1234-1234-123456789abc',
      );
    });

    test('resolver 未接线：reply uri null（编辑器不动节点）', () async {
      await controller.onIncomingMessage(imageUploadRequest());
      expect(sent.single.payload['uri'], isNull);
    });

    test('resolver 抛异常：reply uri null（不崩）', () async {
      controller.resolveImageUpload = () async => throw StateError('未连接');
      await controller.onIncomingMessage(imageUploadRequest());
      expect(sent.single.payload['uri'], isNull);
    });

    test('缺 id：不回复', () async {
      controller.resolveImageUpload = () async => 'biu-file://x';
      await controller.onIncomingMessage(imageUploadRequest(id: null));
      expect(sent, isEmpty);
    });
  });

  group('aiAction（P2 预留）', () {
    test('未接线时不崩', () async {
      final controller = EditorBridgeController(
        initialMarkdown: '',
        theme: BridgeTheme.light,
      );
      await controller.onIncomingMessage(
        BridgeMessage(
          type: 'aiAction',
          payload: {'action': 'ask', 'from': 1, 'to': 5, 'text': '选区'},
        ),
      );
    });

    test('注入 onAiAction 时收到解析后的动作', () async {
      final controller = EditorBridgeController(
        initialMarkdown: '',
        theme: BridgeTheme.light,
      );
      EditorAiAction? got;
      controller.onAiAction = (a) => got = a;
      await controller.onIncomingMessage(
        BridgeMessage(
          type: 'aiAction',
          payload: {'action': 'edit', 'from': 2, 'to': 8, 'text': '选区文本'},
        ),
      );
      expect(got?.action, 'edit');
      expect(got?.from, 2);
      expect(got?.to, 8);
      expect(got?.text, '选区文本');
    });
  });

  group('setLocale（运行时语言跟随）', () {
    test('推送 setOptions.locale 并更新 controller.locale', () async {
      final sent = <BridgeMessage>[];
      final controller = EditorBridgeController(
        initialMarkdown: '',
        theme: BridgeTheme.light,
      )..attach((msg) async => sent.add(msg));
      await controller.setLocale('en');
      expect(controller.locale, 'en');
      expect(sent.single.type, 'setOptions');
      expect(sent.single.payload['locale'], 'en');
    });

    test('同语言不重复推送', () async {
      final sent = <BridgeMessage>[];
      final controller = EditorBridgeController(
        initialMarkdown: '',
        theme: BridgeTheme.light,
        locale: 'en',
      )..attach((msg) async => sent.add(msg));
      await controller.setLocale('en');
      expect(sent, isEmpty);
    });
  });

  group('resolveEditorLocale', () {
    test('localeOverride 优先：zh 系归一到 zh-Hans', () {
      expect(resolveEditorLocale('zh'), 'zh-Hans');
      expect(resolveEditorLocale('zh-CN'), 'zh-Hans');
      expect(resolveEditorLocale('zh-Hans'), 'zh-Hans');
    });

    test('非 zh override 落到 en', () {
      expect(resolveEditorLocale('en'), 'en');
      expect(resolveEditorLocale('fr'), 'en');
    });

    test('空 override 跟系统 locale（测试环境 en_US → en）', () {
      expect(resolveEditorLocale(null), 'en');
      expect(resolveEditorLocale(''), 'en');
    });
  });

  group('BridgeFeatures.platform（M1 平台标记）', () {
    test('platform 序列化进 features', () {
      final j = const BridgeFeatures(platform: 'ios').toJson();
      expect(j['platform'], 'ios');
      expect(const BridgeFeatures(platform: 'android').toJson()['platform'],
          'android');
    });

    test('缺省 null：key 不下发（老 host 行为不变）', () {
      final j = const BridgeFeatures().toJson();
      expect(j.containsKey('platform'), isFalse);
      // 既有字段默认值不变
      expect(j['contextMenu'], 'custom');
      expect(j['aiActions'], false);
      expect(j['imageUpload'], false);
    });
  });
}

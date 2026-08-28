// repo_app_window —— 参数编解码 + 平台分支单测。
//
// 子窗口 engine 只经 JSON 字符串拿参数，编解码是双 engine 契约 —
// pin 死。UI（RepoAppWindowApp / 原生通道自检配）需真机验证，见
// 落地报告。

import 'package:biumind/core/platform/platform_caps.dart';
import 'package:biumind/features/apps/host/repo_app_window.dart';
import 'package:flutter_test/flutter_test.dart';

PlatformCaps _caps({required bool runner, required bool webview}) =>
    PlatformCaps(
      hasLocalPty: true,
      hasFileSystem: true,
      hasNotifications: true,
      supportsBackgroundIsolates: true,
      hasPersistentSqlite: true,
      hasEmbeddedWebView: webview,
      hasRepoAppRunner: runner,
    );

void main() {
  group('RepoAppWindowArgs 编解码', () {
    test('encode → tryDecode 往返', () {
      const args = RepoAppWindowArgs(
        title: 'OpenMontage',
        url: 'http://127.0.0.1:8800',
        installId: 'ins-1',
      );
      final decoded = RepoAppWindowArgs.tryDecode(args.encode());
      expect(decoded, isNotNull);
      expect(decoded!.title, 'OpenMontage');
      expect(decoded.url, 'http://127.0.0.1:8800');
      expect(decoded.installId, 'ins-1');
    });

    test('特殊字符（中文/emoji/URL query）不炸', () {
      const args = RepoAppWindowArgs(
        title: '视频混剪 🎬',
        url: 'http://127.0.0.1:8800/?token=a%20b&lang=zh',
        installId: 'ins-2',
      );
      final decoded = RepoAppWindowArgs.tryDecode(args.encode());
      expect(decoded!.title, '视频混剪 🎬');
      expect(decoded.url, contains('token=a%20b'));
    });

    test('防御性解析：坏输入 → null', () {
      expect(RepoAppWindowArgs.tryDecode(''), isNull);
      expect(RepoAppWindowArgs.tryDecode('not json'), isNull);
      expect(RepoAppWindowArgs.tryDecode('{}'), isNull, reason: '缺 url');
      expect(RepoAppWindowArgs.tryDecode('{"url":""}'), isNull,
          reason: '空 url');
      expect(RepoAppWindowArgs.tryDecode('[]'), isNull);
    });

    test('缺可选字段给默认值', () {
      final decoded =
          RepoAppWindowArgs.tryDecode('{"url":"http://127.0.0.1:1"}');
      expect(decoded!.title, '');
      expect(decoded.installId, '');
    });

    test('isSubWindowEngineArgs / fromEngineArgs', () {
      expect(RepoAppWindowArgs.isSubWindowEngineArgs(const []), isFalse);
      expect(
          RepoAppWindowArgs.isSubWindowEngineArgs(const ['multi_window']),
          isFalse);
      expect(
        RepoAppWindowArgs.isSubWindowEngineArgs(
            const ['multi_window', '1', '{}']),
        isTrue,
      );
      expect(
        RepoAppWindowArgs.isSubWindowEngineArgs(const ['other', '1', '{}']),
        isFalse,
      );
      final args = RepoAppWindowArgs.fromEngineArgs(
          const ['multi_window', 'w1', '{"url":"http://127.0.0.1:9"}']);
      expect(args!.url, 'http://127.0.0.1:9');
      expect(RepoAppWindowArgs.fromEngineArgs(const []), isNull);
    });
  });

  group('shouldUseNativeRepoWindow 平台分支', () {
    test('macOS（runner + webview）→ true', () {
      expect(shouldUseNativeRepoWindow(_caps(runner: true, webview: true)),
          isTrue);
    });
    test('Linux（runner 无 webview）→ false（保留应用内全屏 + fallback）',
        () {
      expect(shouldUseNativeRepoWindow(_caps(runner: true, webview: false)),
          isFalse);
    });
    test('Windows / 移动端（无 runner）→ false', () {
      expect(shouldUseNativeRepoWindow(_caps(runner: false, webview: false)),
          isFalse);
      expect(shouldUseNativeRepoWindow(_caps(runner: false, webview: true)),
          isFalse);
    });
  });
}

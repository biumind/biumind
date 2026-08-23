// WebViewPanel —— caps 分支 + sameRegistrableSuffix 单测（M1.14）。
//
// caps 分支：hasEmbeddedWebView=false（Windows/Linux/Web）时必须渲染
// 外部浏览器 fallback，且不创建 WebViewController（在测试环境会直接
// 抛 MissingPluginException —— 能 pump 成功本身就证明 guard 生效）。
//
// sameRegistrableSuffix：eTLD+1 修复的 pin —— com.cn 类二级公共后缀
// 不再误判同站；IP 字面量（repo app 的 127.0.0.1）不参与后缀比较。

import 'package:biumind/core/platform/platform_caps.dart';
import 'package:biumind/features/apps/host/webview_panel.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  group('sameRegistrableSuffix', () {
    test('普通域名单段公共后缀：末两段相同即同站', () {
      expect(sameRegistrableSuffix('accounts.kimi.com', 'kimi.com'), isTrue);
      expect(sameRegistrableSuffix('a.kimi.com', 'b.kimi.com'), isTrue);
      expect(sameRegistrableSuffix('kimi.com', 'moonshot.cn'), isFalse);
    });

    test('二级公共后缀回退末三段：a.com.cn ≠ b.com.cn', () {
      expect(sameRegistrableSuffix('a.com.cn', 'b.com.cn'), isFalse);
      expect(sameRegistrableSuffix('x.a.com.cn', 'y.a.com.cn'), isTrue);
      expect(sameRegistrableSuffix('a.co.jp', 'b.co.jp'), isFalse);
      expect(sameRegistrableSuffix('a.co.uk', 'b.co.uk'), isFalse);
      // 中文站常见
      expect(sameRegistrableSuffix('foo.net.cn', 'bar.net.cn'), isFalse);
    });

    test('修复前误判用例回归：accounts.kimi.moonshot.cn 仍判同站', () {
      expect(
        sameRegistrableSuffix('accounts.kimi.moonshot.cn', 'kimi.moonshot.cn'),
        isTrue,
      );
    });

    test('IP 字面量不做后缀比较（127.0.0.1 是 repo app 本机地址）', () {
      expect(sameRegistrableSuffix('127.0.0.1', '192.168.0.1'), isFalse);
      expect(sameRegistrableSuffix('10.0.0.1', '127.0.0.1'), isFalse);
    });

    test('大小写不敏感 + 非法输入安全', () {
      expect(sameRegistrableSuffix('A.Kimi.COM', 'b.kimi.com'), isTrue);
      expect(sameRegistrableSuffix('localhost', 'x'), isFalse);
      expect(sameRegistrableSuffix('', 'a.com'), isFalse);
    });
  });

  group('WebViewPanel caps 分支', () {
    const noWebViewCaps = PlatformCaps(
      hasLocalPty: true,
      hasFileSystem: true,
      hasNotifications: true,
      supportsBackgroundIsolates: true,
      hasPersistentSqlite: true,
      hasEmbeddedWebView: false,
      hasRepoAppRunner: true,
    );

    testWidgets('hasEmbeddedWebView=false → 渲染外部浏览器 fallback',
        (tester) async {
      await tester.pumpWidget(
        ProviderScope(
          overrides: [
            platformCapsProvider.overrideWithValue(noWebViewCaps),
          ],
          child: const MaterialApp(
            home: Scaffold(
              body: WebViewPanel(initialUrl: 'http://127.0.0.1:8800'),
            ),
          ),
        ),
      );
      await tester.pump();
      expect(find.text('在浏览器中打开'), findsOneWidget);
      expect(find.text('打开'), findsOneWidget);
    });
  });
}

// add_webview_dialog — form validation tests.
//
// We don't exercise the network roundtrip (separate integration concern);
// these tests pin the URL/title client-side validation so a regression
// doesn't accidentally let through malformed inputs that the server
// would reject anyway. Server-side rejection is covered by
// services/app_center/internal/installs/user_webview_test.go.

import 'package:biumind/features/apps/presentation/add_webview_dialog.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

Widget _wrap(Widget child) => ProviderScope(
      child: MaterialApp(home: Scaffold(body: child)),
    );

void main() {
  testWidgets('shows form when opened', (tester) async {
    await tester.pumpWidget(_wrap(Builder(builder: (ctx) {
      return ElevatedButton(
        onPressed: () => showAddWebViewDialog(ctx),
        child: const Text('open'),
      );
    })));
    await tester.tap(find.text('open'));
    await tester.pumpAndSettle();
    expect(find.text('添加 WebView 应用'), findsOneWidget);
    expect(find.text('名称 *'), findsOneWidget);
    expect(find.text('URL *'), findsOneWidget);
  });

  testWidgets('rejects empty title', (tester) async {
    await tester.pumpWidget(_wrap(Builder(builder: (ctx) {
      return ElevatedButton(
        onPressed: () => showAddWebViewDialog(ctx),
        child: const Text('open'),
      );
    })));
    await tester.tap(find.text('open'));
    await tester.pumpAndSettle();
    // Click submit without filling anything.
    await tester.tap(find.text('创建'));
    await tester.pumpAndSettle();
    expect(find.text('请填写名称'), findsOneWidget);
  });

  testWidgets('rejects javascript scheme', (tester) async {
    await tester.pumpWidget(_wrap(Builder(builder: (ctx) {
      return ElevatedButton(
        onPressed: () => showAddWebViewDialog(ctx),
        child: const Text('open'),
      );
    })));
    await tester.tap(find.text('open'));
    await tester.pumpAndSettle();
    await tester.enterText(find.widgetWithText(TextFormField, '名称 *'), 'X');
    await tester.enterText(
        find.widgetWithText(TextFormField, 'URL *'), 'javascript:alert(1)');
    await tester.tap(find.text('创建'));
    await tester.pumpAndSettle();
    expect(find.text('协议必须是 http 或 https'), findsOneWidget);
  });

  testWidgets('rejects bare hostname', (tester) async {
    await tester.pumpWidget(_wrap(Builder(builder: (ctx) {
      return ElevatedButton(
        onPressed: () => showAddWebViewDialog(ctx),
        child: const Text('open'),
      );
    })));
    await tester.tap(find.text('open'));
    await tester.pumpAndSettle();
    await tester.enterText(find.widgetWithText(TextFormField, '名称 *'), 'X');
    await tester.enterText(
        find.widgetWithText(TextFormField, 'URL *'), 'https://kimi');
    await tester.tap(find.text('创建'));
    await tester.pumpAndSettle();
    expect(
      find.text('主机名必须是完整域名（如 kimi.moonshot.cn）或 localhost'),
      findsOneWidget,
    );
  });

  testWidgets('shows shared-profile FAQ box', (tester) async {
    await tester.pumpWidget(_wrap(Builder(builder: (ctx) {
      return ElevatedButton(
        onPressed: () => showAddWebViewDialog(ctx),
        child: const Text('open'),
      );
    })));
    await tester.tap(find.text('open'));
    await tester.pumpAndSettle();
    expect(
      find.textContaining('共享一份登录态'),
      findsOneWidget,
      reason: 'users must see the single-profile note before submitting',
    );
  });
}

// form_factor / phone_nav 的断点与平台判定测试 — 手机适配 P0 基础设施。
// 方案: docs/BiuMind-Mobile-Adaptation-Plan.md §4.1/§4.2

import 'package:flutter/material.dart';
import 'package:biumind/l10n/app_localizations.dart';
import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:biumind/core/layout/form_factor.dart';
import 'package:biumind/core/layout/phone_nav.dart';

void main() {
  void setWidth(WidgetTester tester, double width) {
    tester.view.physicalSize = Size(width, 800);
    tester.view.devicePixelRatio = 1.0;
    addTearDown(tester.view.reset);
  }

  Widget probe(ValueChanged<BuildContext> onBuild) {
    return MaterialApp(home: Builder(builder: (ctx) {
      onBuild(ctx);
      return const SizedBox.shrink();
    }));
  }

  testWidgets('isPhoneLayout: 宽 <600 为 phone, >=600 为 desktop',
      (tester) async {
    late BuildContext ctx;
    await tester.pumpWidget(probe((c) => ctx = c));

    setWidth(tester, 390);
    await tester.pump();
    expect(isPhoneLayout(ctx), isTrue);
    expect(formFactorOf(ctx), AppFormFactor.phone);

    setWidth(tester, kPhoneBreakpoint);
    await tester.pump();
    expect(isPhoneLayout(ctx), isFalse);
    expect(formFactorOf(ctx), AppFormFactor.desktop);

    setWidth(tester, 1280);
    await tester.pump();
    expect(isPhoneLayout(ctx), isFalse);
  });

  testWidgets('platformHasHover: iOS/Android 无 hover, 桌面有', (tester) async {
    late BuildContext ctx;
    // 同类型根 widget 重复 pumpWidget 会复用元素导致 platform 不更新 —
    // 加 ValueKey 强制重建 (probe 实测)。
    Future<void> pumpPlatform(TargetPlatform p) => tester.pumpWidget(
          MaterialApp(
            key: ValueKey(p),
            theme: ThemeData(platform: p),
            home: Builder(builder: (c) {
              ctx = c;
              return const SizedBox.shrink();
            }),
          ),
        );

    await pumpPlatform(TargetPlatform.android);
    expect(platformHasHover(ctx), isFalse);
    await pumpPlatform(TargetPlatform.iOS);
    expect(platformHasHover(ctx), isFalse);
    await pumpPlatform(TargetPlatform.macOS);
    expect(platformHasHover(ctx), isTrue);
    await pumpPlatform(TargetPlatform.windows);
    expect(platformHasHover(ctx), isTrue);
  });

  testWidgets('PhoneMenuButton: 手机渲染更多 (more_vert), 桌面 shrink',
      (tester) async {
    // R1.6: PhoneMenuButton 从「开 Drawer 的 IconButton」改为「更多」
    // PopupMenuButton (搜索 / 帮助反馈); 不再依赖 appShellScaffoldKey。
    setWidth(tester, 390);
    await tester.pumpWidget(const MaterialApp(home: PhoneMenuButton()));
    expect(find.byIcon(Icons.more_vert), findsOneWidget);

    setWidth(tester, 1280);
    await tester.pumpWidget(const MaterialApp(home: PhoneMenuButton()));
    expect(find.byIcon(Icons.more_vert), findsNothing);
  });
}

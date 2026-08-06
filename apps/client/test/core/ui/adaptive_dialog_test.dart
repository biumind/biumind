// adaptive_dialog 的形态分支测试 — 手机适配 P1 基础设施。
// 方案: docs/BiuMind-Mobile-Adaptation-Plan.md §4.5

import 'package:flutter/material.dart' hide showAdaptiveDialog;
import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:biumind/core/ui/adaptive_dialog.dart';

void main() {
  void setScreen(WidgetTester tester, Size size) {
    tester.view.physicalSize = size;
    tester.view.devicePixelRatio = 1.0;
    addTearDown(tester.view.reset);
  }

  Widget harness() {
    return MaterialApp(
      home: Scaffold(
        body: Builder(
          builder: (ctx) => FilledButton(
            onPressed: () => showAdaptiveDialog<void>(
              context: ctx,
              builder: (_) => const AdaptiveDialogFrame(child: Text('x')),
            ),
            child: const Text('open'),
          ),
        ),
      ),
    );
  }

  testWidgets('手机(390 宽): 走 modal bottom sheet, 不渲染 Dialog', (tester) async {
    setScreen(tester, const Size(390, 844));
    await tester.pumpWidget(harness());

    await tester.tap(find.text('open'));
    await tester.pumpAndSettle();

    expect(find.text('x'), findsOneWidget);
    expect(find.byType(BottomSheet), findsOneWidget);
    expect(find.byType(Dialog), findsNothing);
  });

  testWidgets('桌面(1200 宽): 走 Dialog', (tester) async {
    setScreen(tester, const Size(1200, 800));
    await tester.pumpWidget(harness());

    await tester.tap(find.text('open'));
    await tester.pumpAndSettle();

    expect(find.byType(Dialog), findsOneWidget);
    expect(find.text('x'), findsOneWidget);
  });
}

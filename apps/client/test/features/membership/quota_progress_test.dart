// W4-7 quota_progress widget tests — 进度条 0/half/full + free fallback.

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:biumind/features/membership/domain/subscription.dart';
import 'package:biumind/features/membership/presentation/widgets/quota_progress.dart';

Future<void> _pump(WidgetTester tester, QuotaUsage usage) async {
  await tester.pumpWidget(MaterialApp(
    home: Scaffold(
      body: QuotaProgress(usage: usage, label: 'Chat'),
    ),
  ));
}

void main() {
  testWidgets('quota=0 (free) 显示提示文案, 不渲染进度条', (tester) async {
    await _pump(tester, QuotaUsage.empty);
    expect(find.text('Chat · 不在套餐配额内'), findsOneWidget);
    expect(find.byType(LinearProgressIndicator), findsNothing);
  });

  testWidgets('半进度 50% 渲染绿色进度条 + 比例文案', (tester) async {
    await _pump(tester, const QuotaUsage(used: 2500, monthly: 5000));
    expect(find.byType(LinearProgressIndicator), findsOneWidget);
    expect(find.text('2500/5000 (50%)'), findsOneWidget);

    final bar = tester.widget<LinearProgressIndicator>(
      find.byType(LinearProgressIndicator),
    );
    expect(bar.value, closeTo(0.5, 0.001));
  });

  testWidgets('满进度 100% 渲染红色进度条', (tester) async {
    await _pump(tester, const QuotaUsage(used: 5000, monthly: 5000));
    expect(find.text('5000/5000 (100%)'), findsOneWidget);
    final bar = tester.widget<LinearProgressIndicator>(
      find.byType(LinearProgressIndicator),
    );
    expect(bar.value, 1.0);
  });
}

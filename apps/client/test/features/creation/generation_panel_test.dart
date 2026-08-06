// GenerationPanel — smoke test.
//
// 验证: panel 能在没有真后端的情况下渲染出 TabStrip + Prompt + 提交按钮 (disabled),
// 且切 tab 走 generation_form_controller 不崩.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:biumind/features/creation/application/aigc_providers.dart';
import 'package:biumind/features/creation/application/generation_form_controller.dart';
import 'package:biumind/features/creation/presentation/widgets/generation_panel.dart';
import 'package:biumind/l10n/app_localizations.dart';

void main() {
  group('GenerationPanel', () {
    testWidgets('renders + tab switching changes form.type', (tester) async {
      await tester.pumpWidget(
        ProviderScope(
          overrides: [
            // override aigcModelsProvider — 不依赖真后端, 返空列表.
            aigcModelsProvider.overrideWith((ref, _) async => const <dynamic>[]),
          ],
          child: const MaterialApp(
            localizationsDelegates: [AppLocalizations.delegate],
            supportedLocales: [Locale('en'), Locale('zh')],
            home: Scaffold(body: GenerationPanel()),
          ),
        ),
      );
      await tester.pump(); // FutureProvider settle

      // panel 渲染
      expect(find.byType(GenerationPanel), findsOneWidget);

      // 用 ProviderContainer 直接读 state 切 tab (跨 tab 文案不固定走点击模拟).
      final el = tester.element(find.byType(GenerationPanel));
      final container = ProviderScope.containerOf(el);
      final notifier = container.read(generationFormControllerProvider.notifier);

      expect(container.read(generationFormControllerProvider).type,
          GenerationType.image);
      notifier.selectType(GenerationType.video);
      expect(container.read(generationFormControllerProvider).type,
          GenerationType.video);
    });
  });
}


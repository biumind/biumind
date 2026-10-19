// BlockRenderer SystemBlock 分支的 widget 测试（设计 §7-4 客户端半）。
//
// 覆盖：
//   - '[auto]' / '[auto-guard]' 前缀 → ⏵⏵ 图标 + dim 灰字
//   - 其他 local_command_output 内容 → 纯 dim 文本行,无图标

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:biumind/features/chat/domain/chat_models.dart';
import 'package:biumind/features/chat/presentation/v2/block_renderer.dart';

Future<void> pumpBlock(WidgetTester tester, String content) {
  return tester.pumpWidget(MaterialApp(
    home: Scaffold(
      body: BlockRenderer(
        block: SystemBlock(
          id: 's1',
          index: 0,
          state: BlockState.closed,
          content: content,
        ),
        role: MessageRole.assistant,
      ),
    ),
  ));
}

void main() {
  testWidgets('[auto] 前缀审计行带 ⏵⏵ 图标', (tester) async {
    await pumpBlock(tester, '[auto] Bash approved without prompting');
    expect(find.text('⏵⏵'), findsOneWidget);
    expect(
      find.text('[auto] Bash approved without prompting'),
      findsOneWidget,
    );
  });

  testWidgets('[auto-guard] 前缀审计行带 ⏵⏵ 图标', (tester) async {
    await pumpBlock(tester, '[auto-guard] Write held for confirmation: 敏感路径');
    expect(find.text('⏵⏵'), findsOneWidget);
    expect(
      find.text('[auto-guard] Write held for confirmation: 敏感路径'),
      findsOneWidget,
    );
  });

  testWidgets('其他 local_command_output 内容纯文本行,无图标', (tester) async {
    await pumpBlock(tester, 'some local command output');
    expect(find.text('⏵⏵'), findsNothing);
    expect(find.text('some local command output'), findsOneWidget);
  });
}

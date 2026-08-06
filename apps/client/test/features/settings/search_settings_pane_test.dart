// N3 设置页「在统一搜索中包含笔记」开关 widget 测试。
//
// 形态对齐 settings_controller_test.dart：InMemorySettingsRepo override，
// 真 SettingsController 跑异步加载；点开关走完整 update → 持久化链路。

import 'package:biumind/features/settings/presentation/search_settings_pane.dart';
import 'package:biumind/services/settings_repo.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  testWidgets('开关默认关，点开后持久化并反映到 UI', (tester) async {
    final repo = InMemorySettingsRepo();
    await tester.pumpWidget(ProviderScope(
      overrides: [settingsRepoProvider.overrideWithValue(repo)],
      child: const MaterialApp(home: Scaffold(body: SearchSettingsPane())),
    ));
    await tester.pumpAndSettle();

    SwitchListTile tile() =>
        tester.widget<SwitchListTile>(find.byType(SwitchListTile));

    expect(find.text('在统一搜索中包含笔记'), findsOneWidget);
    expect(tile().value, isFalse, reason: '默认关');

    await tester.tap(find.byType(SwitchListTile));
    await tester.pumpAndSettle();

    expect(tile().value, isTrue);
    expect((await repo.load()).searchIncludeNotes, isTrue,
        reason: '开关状态落到 settings 存储');

    await tester.tap(find.byType(SwitchListTile));
    await tester.pumpAndSettle();
    expect(tile().value, isFalse);
  });
}

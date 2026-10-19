// confirmAutoModeOptIn —— auto 模式首次启用 opt-in guard 的 widget 测试
// （设计 docs/BiuMind-Auto-Permission-Mode-Design.md §6 opt-in 段、§7-3）。
//
// 覆盖三条路径：
//   - 未确认 + 取消 → false，标记不落
//   - 未确认 + 确认 → true，标记写入 ChatPreferences 并持久化
//   - 已确认 → 不弹窗直接 true

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'package:biumind/features/chat/application/auto_mode_opt_in.dart';
import 'package:biumind/features/chat/application/chat_preferences.dart';
import 'package:biumind/l10n/app_localizations.dart';

class _Harness extends ConsumerWidget {
  const _Harness({required this.onResult});
  final void Function(bool) onResult;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    return Scaffold(
      body: TextButton(
        onPressed: () async {
          final ok = await confirmAutoModeOptIn(context, ref);
          onResult(ok);
        },
        child: const Text('go'),
      ),
    );
  }
}

Future<ProviderContainer> pumpOptIn(
  WidgetTester tester,
  void Function(bool) onResult,
) async {
  final container = ProviderContainer();
  addTearDown(container.dispose);
  await tester.pumpWidget(UncontrolledProviderScope(
    container: container,
    child: MaterialApp(
      locale: const Locale('zh'),
      localizationsDelegates: AppLocalizations.localizationsDelegates,
      supportedLocales: AppLocalizations.supportedLocales,
      home: _Harness(onResult: onResult),
    ),
  ));
  // ChatPreferencesNotifier._load 是异步的(SharedPreferences),多拍一帧
  // 保证种子值已落进 state。
  await tester.pumpAndSettle();
  await tester.pump();
  return container;
}

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  testWidgets('未确认 + 取消 → false，标记不落', (tester) async {
    SharedPreferences.setMockInitialValues({});
    bool? result;
    final container = await pumpOptIn(tester, (ok) => result = ok);

    await tester.tap(find.text('go'));
    await tester.pumpAndSettle();

    expect(find.text('启用自动执行？'), findsOneWidget);
    expect(find.textContaining('工具调用将不再逐一确认'), findsOneWidget);

    await tester.tap(find.text('取消'));
    await tester.pumpAndSettle();

    expect(result, isFalse);
    expect(container.read(chatPreferencesProvider).autoModeOptedIn, isFalse);
  });

  testWidgets('未确认 + 确认 → true，标记写入并持久化', (tester) async {
    SharedPreferences.setMockInitialValues({});
    bool? result;
    final container = await pumpOptIn(tester, (ok) => result = ok);

    await tester.tap(find.text('go'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('启用自动执行'));
    await tester.pumpAndSettle();

    expect(result, isTrue);
    expect(container.read(chatPreferencesProvider).autoModeOptedIn, isTrue);
    // 持久化:SharedPreferences 里的 biu.chat.prefs JSON 已写入标记
    // (fake async 下不能 new notifier + Future.delayed 验证跨实例,直接
    // 断言落盘内容)。
    final prefs = await SharedPreferences.getInstance();
    expect(prefs.getString('biu.chat.prefs'),
        contains('"autoModeOptedIn":true'));
  });

  testWidgets('已确认 → 不弹窗直接 true', (tester) async {
    SharedPreferences.setMockInitialValues({
      'biu.chat.prefs': '{"autoModeOptedIn":true}',
    });
    bool? result;
    final container = await pumpOptIn(tester, (ok) => result = ok);
    // provider 懒加载:先显式 read 触发 _load 并等种子值落进 state,
    // 否则首次 read 拿到的是默认 false。
    container.read(chatPreferencesProvider);
    await tester.pump();
    expect(container.read(chatPreferencesProvider).autoModeOptedIn, isTrue);

    await tester.tap(find.text('go'));
    await tester.pumpAndSettle();

    expect(find.text('启用自动执行？'), findsNothing);
    expect(result, isTrue);
  });
}

// FormCard（agent 提问表单卡）widget 测试。
//
// 覆盖（设计 §2.5 客户端部分）:
//   - 单选 chip 渲染 + 提交锁定显示已选
//   - 多选 chip（FilterChip）+ 数组形态 content
//   - 无 options → 自由文本框
//   - decline / cancel 回传对应 action
//   - 未选择时提交按钮禁用
//   - FormSpec.parse 的纯 JSON Schema 降级路径（无 x-biumind-question）

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:biumind/features/chat/application/chat_controller.dart';
import 'package:biumind/features/chat/data/biu_session_connection.dart';
import 'package:biumind/features/chat/presentation/v2/form_card.dart';
import 'package:biumind/l10n/app_localizations.dart';

Map<String, dynamic> schemaFor({
  required bool multi,
  bool withExt = true,
  bool withOptions = true,
}) {
  final labels = ['red', 'blue', 'green'];
  return {
    'type': 'object',
    'title': 'Pick a color?',
    'properties': {
      'answer': multi
          ? {
              'type': 'array',
              'items': {'type': 'string', 'enum': labels},
              'minItems': 1,
            }
          : {'type': 'string', 'enum': labels},
    },
    'required': ['answer'],
    if (withExt)
      'x-biumind-question': {
        'question': 'Pick a color?',
        'header': 'Color',
        'multi_select': multi,
        if (withOptions)
          'options': [
            {'label': 'red', 'description': 'warm'},
            {'label': 'blue', 'description': 'cool'},
            {'label': 'green', 'description': 'fresh'},
          ]
        else
          'options': <dynamic>[],
      },
  };
}

class RespondSpy {
  final calls = <(String, Map<String, dynamic>?)>[];
  void call(String action, [Map<String, dynamic>? content]) {
    calls.add((action, content));
  }
}

/// 把一条提问塞进 provider 并渲染 FormCard。返回 spy。
Future<RespondSpy> pumpCard(
  WidgetTester tester,
  Map<String, dynamic> schema,
) async {
  final spy = RespondSpy();
  final container = ProviderContainer();
  addTearDown(container.dispose);
  container.read(pendingElicitationsProvider.notifier).add(
        't1',
        ElicitationRequested(
          requestId: 'req-1',
          message: 'Pick a color?',
          schema: schema,
          respond: spy.call,
        ),
      );
  await tester.pumpWidget(UncontrolledProviderScope(
    container: container,
    child: MaterialApp(
      locale: const Locale('zh'),
      localizationsDelegates: AppLocalizations.localizationsDelegates,
      supportedLocales: AppLocalizations.supportedLocales,
      home: const Scaffold(body: FormCard(threadId: 't1')),
    ),
  ));
  await tester.pumpAndSettle();
  return spy;
}

void main() {
  testWidgets('单选:渲染 chip + 提交后锁定显示已选', (tester) async {
    final spy = await pumpCard(tester, schemaFor(multi: false));
    expect(find.text('Pick a color?'), findsOneWidget);
    expect(find.text('Color'), findsOneWidget);
    expect(find.widgetWithText(ChoiceChip, 'red'), findsOneWidget);
    expect(find.widgetWithText(ChoiceChip, 'blue'), findsOneWidget);

    // 未选择 → 提交禁用。
    FilledButton submit = tester.widget(find.widgetWithText(FilledButton, '提交'));
    expect(submit.onPressed, isNull);

    await tester.tap(find.widgetWithText(ChoiceChip, 'blue'));
    await tester.pump();
    submit = tester.widget(find.widgetWithText(FilledButton, '提交'));
    expect(submit.onPressed, isNotNull);
    await tester.tap(find.widgetWithText(FilledButton, '提交'));
    await tester.pumpAndSettle();

    expect(spy.calls, hasLength(1));
    expect(spy.calls.single.$1, 'accept');
    expect(spy.calls.single.$2, {'answer': 'blue'});
    // 锁定卡:显示已选,不再有按钮。
    expect(find.text('已回答：blue'), findsOneWidget);
    expect(find.widgetWithText(FilledButton, '提交'), findsNothing);
  });

  testWidgets('多选:FilterChip 数组形态 content', (tester) async {
    final spy = await pumpCard(tester, schemaFor(multi: true));
    expect(find.text('可多选'), findsOneWidget);
    await tester.tap(find.widgetWithText(FilterChip, 'red'));
    await tester.pump();
    await tester.tap(find.widgetWithText(FilterChip, 'green'));
    await tester.pump();
    await tester.tap(find.widgetWithText(FilledButton, '提交'));
    await tester.pumpAndSettle();

    expect(spy.calls.single.$1, 'accept');
    expect(spy.calls.single.$2, {
      'answer': ['red', 'green'],
    });
    expect(find.text('已回答：red、green'), findsOneWidget);
  });

  testWidgets('无 options → 自由文本框,输入后可提交', (tester) async {
    final spy = await pumpCard(
        tester, schemaFor(multi: false, withOptions: false));
    expect(find.byType(TextField), findsOneWidget);
    FilledButton submit = tester.widget(find.widgetWithText(FilledButton, '提交'));
    expect(submit.onPressed, isNull);

    await tester.enterText(find.byType(TextField), '紫色,谢谢');
    await tester.pump();
    submit = tester.widget(find.widgetWithText(FilledButton, '提交'));
    expect(submit.onPressed, isNotNull);
    await tester.tap(find.widgetWithText(FilledButton, '提交'));
    await tester.pumpAndSettle();

    expect(spy.calls.single.$1, 'accept');
    expect(spy.calls.single.$2, {'answer': '紫色,谢谢'});
    expect(find.text('已回答：紫色,谢谢'), findsOneWidget);
  });

  testWidgets('跳过 → decline;取消 → cancel;卡片锁定', (tester) async {
    final spy = await pumpCard(tester, schemaFor(multi: false));
    await tester.tap(find.text('跳过'));
    await tester.pumpAndSettle();
    expect(spy.calls.single.$1, 'decline');
    expect(spy.calls.single.$2, isNull);
    expect(find.text('已跳过'), findsOneWidget);
  });

  testWidgets('取消 → cancel 回包', (tester) async {
    final spy = await pumpCard(tester, schemaFor(multi: false));
    await tester.tap(find.text('取消'));
    await tester.pumpAndSettle();
    expect(spy.calls.single.$1, 'cancel');
    expect(find.text('已取消'), findsOneWidget);
  });

  group('FormSpec.parse', () {
    test('x-biumind-question 优先(带 option 描述)', () {
      final spec = FormSpec.parse(schemaFor(multi: false), 'fallback');
      expect(spec, isNotNull);
      expect(spec!.question, 'Pick a color?');
      expect(spec.header, 'Color');
      expect(spec.multiSelect, isFalse);
      expect(spec.options.map((o) => o.label), ['red', 'blue', 'green']);
      expect(spec.options.first.description, 'warm');
    });

    test('无扩展字段 → 从 JSON Schema 主体降级(单选)', () {
      final spec = FormSpec.parse(schemaFor(multi: false, withExt: false), 'fb');
      expect(spec, isNotNull);
      expect(spec!.question, 'Pick a color?');
      expect(spec.multiSelect, isFalse);
      expect(spec.options.map((o) => o.label), ['red', 'blue', 'green']);
      expect(spec.options.first.description, '');
    });

    test('无扩展字段 → 多选 array 形态识别', () {
      final spec = FormSpec.parse(schemaFor(multi: true, withExt: false), 'fb');
      expect(spec, isNotNull);
      expect(spec!.multiSelect, isTrue);
      expect(spec.options, hasLength(3));
    });

    test('空 schema + 空 message → null(卡片只给跳过/取消)', () {
      expect(FormSpec.parse(const {}, ''), isNull);
    });
  });
}

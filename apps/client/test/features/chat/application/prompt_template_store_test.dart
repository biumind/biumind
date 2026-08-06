// PromptTemplateStore —— system prompt 模板单测。

import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'package:biumind/features/chat/application/prompt_template_store.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  setUp(() {
    SharedPreferences.setMockInitialValues({});
  });

  test('starts empty', () async {
    final n = PromptTemplateNotifier();
    await Future.delayed(Duration.zero);
    expect(n.state, isEmpty);
  });

  test('create returns new id and adds to state', () async {
    final n = PromptTemplateNotifier();
    await Future.delayed(Duration.zero);
    final id = await n.create(name: 'A', content: 'aaa');
    expect(id.isNotEmpty, true);
    expect(n.state.length, 1);
    expect(n.state.first.id, id);
    expect(n.state.first.name, 'A');
  });

  test('update modifies an existing template', () async {
    final n = PromptTemplateNotifier();
    await Future.delayed(Duration.zero);
    final id = await n.create(name: 'A', content: 'aaa');
    await n.update(id, name: 'A2', content: 'bbb');
    expect(n.state.first.name, 'A2');
    expect(n.state.first.content, 'bbb');
  });

  test('remove deletes the template', () async {
    final n = PromptTemplateNotifier();
    await Future.delayed(Duration.zero);
    final id1 = await n.create(name: 'A', content: 'a');
    await n.create(name: 'B', content: 'b');
    await n.remove(id1);
    expect(n.state.length, 1);
    expect(n.state.first.name, 'B');
  });

  test('persists across notifier instances (SharedPreferences round-trip)',
      () async {
    final a = PromptTemplateNotifier();
    await Future.delayed(Duration.zero);
    await a.create(name: 'X', content: 'xxx');
    // 新 notifier 直接 _load —— mock prefs 共享
    final b = PromptTemplateNotifier();
    await Future.delayed(const Duration(milliseconds: 10));
    expect(b.state.length, 1);
    expect(b.state.first.name, 'X');
  });

  test('PromptTemplate.toJson / fromJson round-trip', () {
    const t = PromptTemplate(id: 'i', name: 'n', content: 'c');
    final j = t.toJson();
    final back = PromptTemplate.fromJson(j);
    expect(back, isNotNull);
    expect(back!.id, 'i');
    expect(back.name, 'n');
    expect(back.content, 'c');
  });

  test('PromptTemplate.fromJson rejects missing fields', () {
    expect(PromptTemplate.fromJson({}), isNull);
    expect(PromptTemplate.fromJson({'id': 'x'}), isNull);
  });
}

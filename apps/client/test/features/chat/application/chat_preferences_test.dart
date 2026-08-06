// ChatPreferences —— 全局偏好单测。

import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'package:biumind/features/chat/application/chat_preferences.dart';
import 'package:biumind/features/chat/domain/chat_models.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  setUp(() {
    SharedPreferences.setMockInitialValues({});
  });

  test('starts with defaults', () async {
    final n = ChatPreferencesNotifier();
    await Future.delayed(Duration.zero);
    expect(n.state.defaultModel, isNull);
    // 出厂默认 = agent（智能模式），见 chat_preferences.dart。
    expect(n.state.defaultMode, ThreadMode.agent);
  });

  test('setDefaultModel toggles correctly', () async {
    final n = ChatPreferencesNotifier();
    await Future.delayed(Duration.zero);
    await n.setDefaultModel('claude-opus-4-7');
    expect(n.state.defaultModel, 'claude-opus-4-7');
    await n.setDefaultModel(null);
    expect(n.state.defaultModel, isNull);
  });

  test('setDefaultMode persists', () async {
    final n = ChatPreferencesNotifier();
    await Future.delayed(Duration.zero);
    await n.setDefaultMode(ThreadMode.chat);
    expect(n.state.defaultMode, ThreadMode.chat);
  });

  test('JSON round-trip preserves all fields', () {
    const p = ChatPreferences(
      defaultModel: 'gpt-4o',
      defaultMode: ThreadMode.task,
    );
    final back = ChatPreferences.fromJson(p.toJson());
    expect(back.defaultModel, 'gpt-4o');
    expect(back.defaultMode, ThreadMode.task);
  });

  test('persists across notifier instances', () async {
    final a = ChatPreferencesNotifier();
    await Future.delayed(Duration.zero);
    await a.setDefaultModel('gpt-4o');
    await a.setDefaultMode(ThreadMode.task);
    final b = ChatPreferencesNotifier();
    await Future.delayed(const Duration(milliseconds: 10));
    expect(b.state.defaultModel, 'gpt-4o');
    expect(b.state.defaultMode, ThreadMode.task);
  });

  test('setLocaleOverride toggles correctly', () async {
    final n = ChatPreferencesNotifier();
    await Future.delayed(Duration.zero);
    expect(n.state.localeOverride, isNull);
    await n.setLocaleOverride('zh');
    expect(n.state.localeOverride, 'zh');
    await n.setLocaleOverride('en');
    expect(n.state.localeOverride, 'en');
    await n.setLocaleOverride(null);
    expect(n.state.localeOverride, isNull);
  });

  test('localeOverride round-trips through JSON', () {
    const p = ChatPreferences(
      defaultMode: ThreadMode.chat,
      localeOverride: 'zh',
    );
    final back = ChatPreferences.fromJson(p.toJson());
    expect(back.localeOverride, 'zh');
  });

  test('cloudTtsConfigured requires both model + voice', () {
    expect(const ChatPreferences().cloudTtsConfigured, isFalse);
    expect(
      const ChatPreferences(ttsModel: 'cosyvoice-v3-plus').cloudTtsConfigured,
      isFalse,
    );
    expect(
      const ChatPreferences(ttsVoice: 'longanyang').cloudTtsConfigured,
      isFalse,
    );
    expect(
      const ChatPreferences(
        ttsModel: 'cosyvoice-v3-plus',
        ttsVoice: 'longanyang',
      ).cloudTtsConfigured,
      isTrue,
    );
  });

  test('setTtsModel(null) clears both model + voice', () async {
    final n = ChatPreferencesNotifier();
    await Future.delayed(Duration.zero);
    await n.setTtsModel('cosyvoice-v3-plus');
    await n.setTtsVoice('longanyang');
    expect(n.state.cloudTtsConfigured, isTrue);
    await n.setTtsModel(null);
    expect(n.state.ttsModel, isNull);
    expect(n.state.ttsVoice, isNull);
    expect(n.state.cloudTtsConfigured, isFalse);
  });

  test('tts fields round-trip through JSON', () {
    const p = ChatPreferences(
      ttsModel: 'cosyvoice-v3-plus',
      ttsVoice: 'longanyang',
    );
    final back = ChatPreferences.fromJson(p.toJson());
    expect(back.ttsModel, 'cosyvoice-v3-plus');
    expect(back.ttsVoice, 'longanyang');
  });

  test('resetAll restores defaults + clears prefs', () async {
    final a = ChatPreferencesNotifier();
    await Future.delayed(Duration.zero);
    await a.setDefaultModel('gpt-4o');
    await a.setDefaultMode(ThreadMode.task);
    await a.resetAll();
    expect(a.state.defaultModel, isNull);
    expect(a.state.defaultMode, ThreadMode.agent);
    // 新实例也读到默认（key 已 remove）
    final b = ChatPreferencesNotifier();
    await Future.delayed(const Duration(milliseconds: 10));
    expect(b.state.defaultModel, isNull);
    expect(b.state.defaultMode, ThreadMode.agent);
  });
}

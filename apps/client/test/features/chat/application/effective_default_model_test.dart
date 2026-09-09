// resolveDefaultModel —— 生效默认模型解析单测（纯函数，无 provider 依赖）。

import 'package:flutter_test/flutter_test.dart';

import 'package:biumind/features/chat/application/chat_controller.dart';
import 'package:biumind/features/chat/application/chat_preferences.dart';
import 'package:biumind/features/chat/application/effective_default_model.dart';

AvailableChatModel _m(
  String code,
  String providerId, {
  bool isOfficial = false,
  bool isClientSide = false,
}) =>
    AvailableChatModel(
      code: code,
      displayName: code,
      providerId: providerId,
      providerDisplayName: providerId,
      isOfficial: isOfficial,
      isClientSide: isClientSide,
    );

final _catalog = [
  _m('kimi-k2', 'biumind-official', isOfficial: true),
  _m('gpt-4o', 'biumind-official', isOfficial: true),
  _m('gpt-4o', 'openai'), // 同 code 的 server BYOK
];

void main() {
  test('未配置默认模型 → 系统默认', () {
    const prefs = ChatPreferences();
    final eff = resolveDefaultModel(prefs, _catalog);
    expect(eff.code, isNull);
    expect(eff.providerId, isNull);
  });

  test('默认模型为空串 → 系统默认', () {
    const prefs = ChatPreferences(defaultModel: '');
    expect(resolveDefaultModel(prefs, _catalog).code, isNull);
  });

  test('有效配置 + providerId 精确命中 → 用户模型', () {
    const prefs = ChatPreferences(
      defaultModel: 'gpt-4o',
      defaultProviderId: 'openai',
    );
    final eff = resolveDefaultModel(prefs, _catalog);
    expect(eff.code, 'gpt-4o');
    expect(eff.providerId, 'openai');
  });

  test('老 prefs 无 providerId → 同 code 第一项并补全 providerId', () {
    const prefs = ChatPreferences(defaultModel: 'kimi-k2');
    final eff = resolveDefaultModel(prefs, _catalog);
    expect(eff.code, 'kimi-k2');
    expect(eff.providerId, 'biumind-official');
  });

  test('模型已下线（目录无此 code）→ 系统默认', () {
    const prefs = ChatPreferences(
      defaultModel: 'gpt-3.5-turbo',
      defaultProviderId: 'biumind-official',
    );
    final eff = resolveDefaultModel(prefs, _catalog);
    expect(eff.code, isNull);
    expect(eff.providerId, isNull);
  });

  test('providerId 指向的条目已下线但同 code 仍在 → 回退同 code 末项（与设置页下拉同规则）', () {
    const prefs = ChatPreferences(
      defaultModel: 'gpt-4o',
      defaultProviderId: 'azure-gone',
    );
    final eff = resolveDefaultModel(prefs, _catalog);
    expect(eff.code, 'gpt-4o');
    expect(eff.providerId, 'openai');
  });

  test('目录未加载（catalog 为 null）→ 信任 prefs 原样返回', () {
    const prefs = ChatPreferences(
      defaultModel: 'anything',
      defaultProviderId: 'any-provider',
    );
    final eff = resolveDefaultModel(prefs, null);
    expect(eff.code, 'anything');
    expect(eff.providerId, 'any-provider');
  });
}

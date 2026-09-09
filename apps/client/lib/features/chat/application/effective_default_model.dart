// EffectiveDefaultModel —— 新建会话时"生效默认模型"的单一解析入口。
//
// 语义：
//   1. 用户配了默认模型且仍在当前可用目录（availableChatModelsProvider）中
//      → 用用户的模型（含 providerId 路由消歧）。
//   2. 未配置，或配置的模型已下线（目录里找不到）→ 系统默认（code = null，
//      thread.model 落 null，由服务端默认链解析）。
//   3. 下线是临时回退：只读 prefs 不写回，模型重新上线后自动恢复生效。
//
// 目录匹配规则与设置页 / NewThreadDialog 下拉一致：优先 (code, providerId)
// 精确匹配；无精确命中（老 prefs 无 providerId / providerId 已失效）时取同
// code 末项；命中后用目录项的 providerId 补全路由。

import 'package:flutter_riverpod/flutter_riverpod.dart';

import 'chat_controller.dart';
import 'chat_preferences.dart';

class EffectiveDefaultModel {
  /// wire model id；null = 系统默认（BiuMind 默认，服务端解析）。
  final String? code;

  /// 路由消歧 provider slug；code 为 null 时恒 null。
  final String? providerId;

  const EffectiveDefaultModel(this.code, this.providerId);

  static const systemDefault = EffectiveDefaultModel(null, null);
}

/// 纯函数版解析。[catalog] 为 null（目录未加载 / 加载失败）时信任 prefs
/// 原样返回——不阻塞建会话，目录可用后校验才生效。
EffectiveDefaultModel resolveDefaultModel(
  ChatPreferences prefs,
  List<AvailableChatModel>? catalog,
) {
  final code = prefs.defaultModel;
  if (code == null || code.isEmpty) return EffectiveDefaultModel.systemDefault;
  if (catalog == null) return EffectiveDefaultModel(code, prefs.defaultProviderId);
  AvailableChatModel? match;
  for (final m in catalog) {
    if (m.code != code) continue;
    match = m; // 与设置页/对话框下拉同规则：无精确命中时取同 code 末项
    if (m.providerId == prefs.defaultProviderId) break;
  }
  if (match == null) return EffectiveDefaultModel.systemDefault;
  return EffectiveDefaultModel(match.code, match.providerId);
}

/// 异步版：等目录首加载完成再校验（目录 provider 非 autoDispose，首载后
/// 零开销）；拉取失败退回已缓存值，仍无缓存则信任 prefs。
/// 参数取 `ref.read` 函数而非 Ref，兼容 WidgetRef / Ref 两种调用方。
Future<EffectiveDefaultModel> resolveEffectiveDefaultModel(
  T Function<T>(ProviderListenable<T> provider) read,
) async {
  final prefs = read(chatPreferencesProvider);
  List<AvailableChatModel>? catalog;
  try {
    catalog = await read(availableChatModelsProvider.future);
  } catch (_) {
    catalog = read(availableChatModelsProvider).valueOrNull;
  }
  return resolveDefaultModel(prefs, catalog);
}

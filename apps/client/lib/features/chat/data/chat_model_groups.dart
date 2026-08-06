// Chat model groups for the model picker — the single source of "which
// models can this user actually chat with".
//
// P6 (catalog 收窄): global 模型清单单源 model-relay —— official (BiuMind
// Cloud) 模型直读 model-relay /v1/me/models (markup 后实际价), 不再经 brain
// 缓存。per-user BYOK 仍读 brain (custom provider + 上游 /models refresh):
//
//   * official — model-relay global catalog (mode=='chat'), markup 后实际价。
//   * server BYOK — identity 有 valid key; 模型从 brain per-user provider 行
//     (custom / 上游 refresh) 取; 无 brain 行 → 空组 (不再静态 catalog 兜底)。
//   * client-side BYOK (P5) — 本机 keychain 有 key; custom 用 model_globs,
//     standard 空组 (静态 catalog P6 删)。
//
// A model is only listed when its provider is official OR the user holds a
// valid identity key — picker 永不提供用户用不了的模型。

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../data/providers_providers.dart';
import '../../settings/application/api_keys_providers.dart';
import '../../settings/data/api_keys_client.dart';

class ChatModelEntry {
  final String code; // wire model id
  final String displayName;
  final int? contextWindow;
  // P6: official 从 model-relay 取的 markup 后实际价 chip (如 "$5/M"); null 不显。
  final String? priceLabel;
  const ChatModelEntry({
    required this.code,
    required this.displayName,
    this.contextWindow,
    this.priceLabel,
  });
}

class ChatModelGroup {
  final String providerId; // slug, thread.providerId routing
  final String displayName;
  final bool isOfficial;
  // P5: client-side BYOK 组 (本机直连). 驱动 routeKey source 去重 —— 同
  // providerId+code 可在 official/server/client 三源并存, 不加 source 前缀
  // 会让 DropdownButton 撞值崩溃.
  final bool isClientSide;
  final List<ChatModelEntry> models;
  const ChatModelGroup({
    required this.providerId,
    required this.displayName,
    required this.isOfficial,
    this.isClientSide = false,
    required this.models,
  });
}

final chatModelGroupsProvider =
    FutureProvider<List<ChatModelGroup>>((ref) async {
  final brainProviders = await ref.watch(providersListProvider.future);
  final groups = <ChatModelGroup>[];

  // 1. official (BiuMind Cloud) — P6: 直读 model-relay global catalog
  //    (mode=='chat', markup 后实际价), 不再经 brain 缓存。
  final relay = await ref.watch(relayCatalogListProvider.future);
  final officialModels = [
    for (final m in relay.where((m) => m.mode == 'chat'))
      ChatModelEntry(
        code: m.code,
        displayName: m.displayName.isEmpty ? m.code : m.displayName,
        contextWindow: m.contextWindow,
        priceLabel: m.inputPriceLabel,
      ),
  ];
  if (officialModels.isNotEmpty) {
    groups.add(ChatModelGroup(
      providerId: 'biumind-official',
      displayName: 'BiuMind Cloud',
      isOfficial: true,
      models: officialModels,
    ));
  }

  // 2. identity BYOK providers — server BYOK (valid key in identity) 或
  //    client-side BYOK (需本机出口: 桌面 daemon 直连, 手机端不可用).
  final keys = await ref.watch(apiKeysListProvider.future);
  final brainBySlug = {for (final p in brainProviders) p.providerId: p};
  for (final k in keys) {
    if (k.isClientSide) {
      // client-side BYOK: 需本机出口, key 加密存 identity. 显示供桌面 daemon 用.
      groups.add(ChatModelGroup(
        providerId: k.provider,
        displayName: '${_providerLabel(k.provider)} (本机直连)',
        isOfficial: false,
        isClientSide: true,
        models: _clientSideModels(k),
      ));
      continue;
    }
    if (k.status != ApiKeyStatus.valid) continue;
    final brainRow = brainBySlug[k.provider];
    // P6: 静态 catalog 删; BYOK server 模型从 brain per-user 行取, 无行 → 空
    // (用户需在设置页 refresh 上游 /models 或配 custom 模型).
    final models = await (brainRow != null
        ? _brainChatModels(ref, brainRow.id)
        : Future.value(const <ChatModelEntry>[]));
    groups.add(ChatModelGroup(
      providerId: k.provider,
      displayName: _providerLabel(k.provider),
      isOfficial: false,
      models: models,
    ));
  }

  // official first, then by display name.
  groups.sort((a, b) {
    if (a.isOfficial != b.isOfficial) return a.isOfficial ? -1 : 1;
    return a.displayName.compareTo(b.displayName);
  });
  return groups;
});

Future<List<ChatModelEntry>> _brainChatModels(Ref ref, String rowId) async {
  final models = await ref.watch(modelsListProvider(rowId).future);
  return models
      .where((m) => m.enabled && m.type == 'chat')
      .map((m) => ChatModelEntry(
            code: m.modelId,
            displayName: m.displayName.isEmpty ? m.modelId : m.displayName,
            contextWindow: m.contextWindow,
          ))
      .toList();
}

/// P5 client-side BYOK 模型清单: custom 用 model_globs 里具体模型名 (非通配);
/// standard provider P6 后无静态 catalog → 空 (用户需填 custom + globs)。
List<ChatModelEntry> _clientSideModels(ApiKeyEntry k) {
  if (k.provider == 'custom') {
    return [
      for (final g in k.modelGlobs)
        if (!g.contains('*')) ChatModelEntry(code: g, displayName: g),
    ];
  }
  return const [];
}

String _providerLabel(String providerId) {
  return byokProviderLabels[providerId] ?? providerId;
}

/// Flatten the groups into a pick list (code + provider for routing). Used by
/// NewThreadDialog + "default model" settings (formerly availableChatModelsProvider).
List<AvailableChatModelItem> flattenChatModelGroups(
    List<ChatModelGroup> groups) {
  final out = <AvailableChatModelItem>[];
  for (final g in groups) {
    for (final m in g.models) {
      out.add(AvailableChatModelItem(
        code: m.code,
        displayName: m.displayName,
        providerId: g.providerId,
        providerDisplayName: g.displayName,
        isOfficial: g.isOfficial,
        isClientSide: g.isClientSide,
      ));
    }
  }
  return out;
}

class AvailableChatModelItem {
  final String code;
  final String displayName;
  final String providerId;
  final String providerDisplayName;
  final bool isOfficial;
  final bool isClientSide;
  const AvailableChatModelItem({
    required this.code,
    required this.displayName,
    required this.providerId,
    required this.providerDisplayName,
    required this.isOfficial,
    this.isClientSide = false,
  });

  /// dropdown 唯一值. 加 source 前缀 (o=official / s=server byok / c=client-side)
  /// 防同 providerId+code 跨源撞值致 DropdownButton 崩溃. 与 chat_controller
  /// AvailableChatModel.routeKey 公式逐字一致.
  String get routeKey =>
      '${isOfficial ? 'o' : (isClientSide ? 'c' : 's')}|$providerId|$code';

  String get label =>
      isOfficial ? displayName : '$displayName · $providerDisplayName';
}

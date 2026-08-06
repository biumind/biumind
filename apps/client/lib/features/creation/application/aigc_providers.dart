// AIGC client + 全局 providers — 与 hub_credentials 同生命周期.

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../services/auth_service.dart';
import '../../settings/application/settings_controller.dart';
import '../data/aigc_client.dart';

/// AigcClient — services/aigc REST 客户端. 未登录 / 未配 aigcUri 时返 null.
final aigcClientProvider = Provider<AigcClient?>((ref) {
  final settings = ref.watch(settingsControllerProvider).valueOrNull;
  final creds = ref.watch(hubCredentialsProvider);
  if (settings == null || creds == null) return null;
  final base = settings.aigcUri;
  if (base == null) return null;
  return AigcClient(
    baseUrl: base,
    // ref.read 现读: aigcClient 被 tasksController 长持有, 闭包不能捕 build-time
    // creds (轮换后 stale REST token). 与 chat agentPlane tokenProvider 同理.
    bearerProvider: () => ref.read(hubCredentialsProvider)?.bearerToken ?? '',
  );
});

/// 模型字典 — 公开端点; 未登录也能拉. 按 type 过滤.
final aigcModelsProvider = FutureProvider.autoDispose
    .family<List<dynamic>, String?>((ref, type) async {
  ref.watch(aigcClientProvider.select((c) => c?.baseUrl));
  final client = ref.read(aigcClientProvider);
  if (client == null) return const [];
  return client.fetchModels(type: type);
});

/// 公开画廊 — autoDispose, 离开 gallery 页就释放.
final aigcGalleryProvider = FutureProvider.autoDispose
    .family<List<dynamic>, GalleryQuery>((ref, q) async {
  ref.watch(aigcClientProvider.select((c) => c?.baseUrl));
  final client = ref.read(aigcClientProvider);
  if (client == null) return const [];
  return client.fetchGallery(
    type: q.type,
    keyword: q.keyword,
    limit: q.limit,
    offset: q.offset,
  );
});

/// 数字人角色列表 — 已登录时返自己 + 系统内置 + 公开. 未登录返 const [].
final aigcCharactersProvider =
    FutureProvider.autoDispose<List<dynamic>>((ref) async {
  ref.watch(aigcClientProvider.select((c) => c?.baseUrl));
  final client = ref.read(aigcClientProvider);
  if (client == null) return const [];
  return client.fetchCharacters();
});

/// 音色字典 — provider 为空 = 全部.
final aigcVoicesProvider = FutureProvider.autoDispose
    .family<List<dynamic>, String?>((ref, provider) async {
  ref.watch(aigcClientProvider.select((c) => c?.baseUrl));
  final client = ref.read(aigcClientProvider);
  if (client == null) return const [];
  return client.fetchVoices(provider: provider);
});

class GalleryQuery {
  final String? type;
  final String? keyword;
  final int limit;
  final int offset;
  const GalleryQuery({this.type, this.keyword, this.limit = 50, this.offset = 0});

  @override
  bool operator ==(Object other) =>
      other is GalleryQuery &&
      other.type == type &&
      other.keyword == keyword &&
      other.limit == limit &&
      other.offset == offset;

  @override
  int get hashCode => Object.hash(type, keyword, limit, offset);
}

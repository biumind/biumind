// Riverpod plumbing for the LLM provider catalog.
//
//   providersClientProvider — REST client (null when not signed in)
//   providersListProvider   — async list, refreshable
//   providerByIdProvider    — synchronous lookup keyed by provider_id
//                             (e.g. 'anthropic'), used by SendController
//                             to route a model dispatch.

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../services/auth_service.dart';
import 'api/providers_client.dart';
import 'api/relay_catalog_client.dart';

final providersClientProvider = Provider<ProvidersClient?>((ref) {
  final creds = ref.watch(hubCredentialsProvider);
  if (creds == null) return null;
  // 单 origin: /v1/providers 由 site nginx 反代到 brain, client 不换端口.
  return ProvidersClient(creds.endpoint, creds.bearerToken);
});

/// Async list of the user's configured providers. UI binds to this for
/// the settings view; auto-refreshes whenever credentials change.
final providersListProvider =
    AsyncNotifierProvider<ProvidersListController, List<ProviderConfig>>(
        ProvidersListController.new);

class ProvidersListController extends AsyncNotifier<List<ProviderConfig>> {
  @override
  Future<List<ProviderConfig>> build() async {
    // select(endpoint): token 轮换不重拉 (见 skillsListProvider 同款注释)。
    ref.watch(providersClientProvider.select((c) => c?.baseUrl));
    final client = ref.read(providersClientProvider);
    if (client == null) return const [];
    return client.list();
  }

  Future<void> refresh() async {
    state = const AsyncLoading();
    state = await AsyncValue.guard(() async {
      final client = ref.read(providersClientProvider);
      if (client == null) return const <ProviderConfig>[];
      return client.list();
    });
  }

  Future<ProviderConfig> create({
    required String providerId,
    String displayName = '',
    String? baseUrl,
    bool enabled = true,
    String? apiKey,
    String source = 'builtin',
  }) async {
    final client = ref.read(providersClientProvider);
    if (client == null) {
      throw StateError('providers client not configured');
    }
    final p = await client.create(
      providerId: providerId,
      displayName: displayName,
      baseUrl: baseUrl,
      enabled: enabled,
      apiKey: apiKey,
      source: source,
    );
    final cur = state.valueOrNull ?? const <ProviderConfig>[];
    state = AsyncData([...cur, p]);
    return p;
  }

  Future<ProviderConfig> patch(
    String id, {
    String? displayName,
    String? baseUrl,
    bool? enabled,
    String? apiKey,
  }) async {
    final client = ref.read(providersClientProvider);
    if (client == null) {
      throw StateError('providers client not configured');
    }
    final updated = await client.patch(
      id,
      displayName: displayName,
      baseUrl: baseUrl,
      enabled: enabled,
      apiKey: apiKey,
    );
    final cur = state.valueOrNull ?? const <ProviderConfig>[];
    state = AsyncData([
      for (final p in cur) p.id == id ? updated : p,
    ]);
    return updated;
  }

  Future<void> delete(String id) async {
    final client = ref.read(providersClientProvider);
    if (client == null) return;
    await client.delete(id);
    final cur = state.valueOrNull ?? const <ProviderConfig>[];
    state = AsyncData([for (final p in cur) if (p.id != id) p]);
  }
}

/// Synchronous lookup: returns the user's configured ProviderConfig for
/// a given `provider_id` slug ('anthropic'), or null. Reads from
/// providersListProvider's current snapshot — does not trigger a fetch.
final providerByIdProvider =
    Provider.family<ProviderConfig?, String>((ref, providerId) {
  final list = ref.watch(providersListProvider).valueOrNull;
  if (list == null) return null;
  for (final p in list) {
    if (p.providerId == providerId) return p;
  }
  return null;
});

/// Models for a given provider (server uuid). Lazy — triggers the
/// builtin-catalog seed on first read for builtin/official providers.
final modelsListProvider = AsyncNotifierProvider.family<
    ModelsListController, List<ChatModel>, String>(
  ModelsListController.new,
);

class ModelsListController
    extends FamilyAsyncNotifier<List<ChatModel>, String> {
  @override
  Future<List<ChatModel>> build(String providerId) async {
    // select(endpoint): token 轮换不重拉。
    ref.watch(providersClientProvider.select((c) => c?.baseUrl));
    final client = ref.read(providersClientProvider);
    if (client == null) return const [];
    return client.listModels(providerId);
  }

  Future<void> refresh() async {
    state = const AsyncLoading();
    state = await AsyncValue.guard(() async {
      final client = ref.read(providersClientProvider);
      if (client == null) return const <ChatModel>[];
      await client.refreshModels(arg);
      return client.listModels(arg);
    });
  }

  Future<ChatModel> patch(
    String modelId, {
    bool? enabled,
    int? sortOrder,
  }) async {
    final client = ref.read(providersClientProvider);
    if (client == null) {
      throw StateError('providers client not configured');
    }
    final updated = await client.patchModel(
      arg,
      modelId,
      enabled: enabled,
      sortOrder: sortOrder,
    );
    final cur = state.valueOrNull ?? const <ChatModel>[];
    state = AsyncData([
      for (final m in cur) m.id == modelId ? updated : m,
    ]);
    return updated;
  }
}

// ── P6: model-relay global catalog (official 模型直读, 跳 brain) ──────

final relayCatalogClientProvider = Provider<RelayCatalogClient?>((ref) {
  final creds = ref.watch(hubCredentialsProvider);
  if (creds == null) return null;
  // 单 origin: /v1/me/models 由 site nginx 反代到 model-relay.
  return RelayCatalogClient(creds.endpoint, creds.bearerToken);
});

/// Async list of model-relay public catalog (global official models, markup
/// 后实际价). chat picker (mode=='chat') + TTS picker (mode=='audio_speech')
/// 共用. select(endpoint): token 轮换不重拉 (chat picker 不每小时闪)。
final relayCatalogListProvider =
    FutureProvider<List<RelayCatalogModel>>((ref) async {
  ref.watch(relayCatalogClientProvider.select((c) => c?.baseUrl));
  final client = ref.read(relayCatalogClientProvider);
  if (client == null) return const [];
  return client.list();
});

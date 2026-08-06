// API Keys (BYOK) Riverpod providers.

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../data/cache_for.dart';
import '../../../services/auth_service.dart';
import '../data/api_keys_client.dart';
import 'settings_controller.dart';

final apiKeysClientProvider = Provider<ApiKeysClient?>((ref) {
  final settings = ref.watch(settingsControllerProvider).valueOrNull;
  final creds = ref.watch(hubCredentialsProvider);
  final identityUri = settings?.identityUri;
  if (identityUri == null || creds == null) return null;
  return ApiKeysClient(
    baseUrl: identityUri,
    bearerProvider: () => creds.bearerToken,
  );
});

final apiKeysListProvider =
    FutureProvider.autoDispose<List<ApiKeyEntry>>((ref) async {
  // select(endpoint): token 轮换不重拉 (BYOK 列表不每小时闪)。
  ref.watch(apiKeysClientProvider.select((c) => c?.baseUrl));
  final client = ref.read(apiKeysClientProvider);
  if (client == null) return const [];
  // BYOK keys 仅用户操作时变; cacheFor 2min 避免设置页来回切重拉。
  ref.cacheFor(const Duration(minutes: 2));
  return client.list();
});

/// 两栏 MasterDetail 选中态. 值编码: 'server:`<providerSlug>`' (云端) /
/// 'client:`<entryId>`' (本地). null 时 UI 默认选首个云端 provider.
final selectedApiKeyProvider = StateProvider<String?>((ref) => null);

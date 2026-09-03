// Wiki 生成偏好（B2）Riverpod providers。
//
// 装配仿 api_keys_providers.dart：client 的 baseUrl 取 settings.identityUri
// （单 origin），bearer 取 hubCredentialsProvider。状态由
// IngestModelNotifier 持有（AsyncValue<String?>，null = 跟随平台默认），
// 初次 watch 时拉取；切换时 PUT，失败向调用方 rethrow 由 UI 弹反馈。

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../services/auth_service.dart';
import '../data/wiki_settings_client.dart';
import 'settings_controller.dart';

final wikiSettingsClientProvider = Provider<WikiSettingsClient?>((ref) {
  final settings = ref.watch(settingsControllerProvider).valueOrNull;
  final creds = ref.watch(hubCredentialsProvider);
  final identityUri = settings?.identityUri;
  if (identityUri == null || creds == null) return null;
  return WikiSettingsClient(
    baseUrl: identityUri,
    bearerProvider: () => creds.bearerToken,
  );
});

class IngestModelNotifier extends StateNotifier<AsyncValue<String?>> {
  /// [_clientReader] 每次调用现读而不是构造时快照：token 轮换会重建
  /// wikiSettingsClientProvider（bearerProvider 闭包捕获的是当时 creds），
  /// 快照会让 PUT 带上过期 token。同 apiKeysListProvider 的 ref.read 模式。
  IngestModelNotifier(this._clientReader)
    : super(const AsyncValue.loading()) {
    _load();
  }

  final WikiSettingsClient? Function() _clientReader;

  Future<void> _load() async {
    final client = _clientReader();
    if (client == null) {
      // 未配置服务器 / 未登录：视为未设置偏好，UI 呈「跟随平台默认」。
      state = const AsyncValue.data(null);
      return;
    }
    try {
      state = AsyncValue.data(await client.getIngestModel());
    } catch (e, st) {
      state = AsyncValue.error(e, st);
    }
  }

  Future<void> refresh() => _load();

  /// 切换偏好：PUT 成功后才更新本地状态；失败 rethrow（状态不变），
  /// 由 UI 给用户可见反馈。
  Future<void> setModel(String? model) async {
    final client = _clientReader();
    if (client == null) return;
    await client.putIngestModel(model);
    state = AsyncValue.data(model);
  }
}

final ingestModelProvider =
    StateNotifierProvider<IngestModelNotifier, AsyncValue<String?>>((ref) {
      // select(baseUrl): token 轮换不重建 notifier，仅 endpoint 变化 /
      // 登录态翻转时重拉。
      ref.watch(wikiSettingsClientProvider.select((c) => c?.baseUrl));
      return IngestModelNotifier(() => ref.read(wikiSettingsClientProvider));
    });

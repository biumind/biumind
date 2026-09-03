// WikiSettingsClient — Wiki 生成的用户级服务端偏好客户端（B2）。
//
//   GET /v1/identity/me/settings/ingest-model   → {"model":"..."}（空串 = 未设置）
//   PUT /v1/identity/me/settings/ingest-model   body {"model":"..."}（空串 = 清除）
//
// 这是首个落服务端的用户级生成偏好：Wiki 页生成在云端 worker 执行，
// 本地 SharedPreferences 拿不到，必须存 identity（Bearer 用户 JWT），
// 天然跨端同步。契约与 services/identity 并行开发中的
// /v1/identity/me/settings/ingest-model 对齐。

import '../../../data/api/_http_helpers.dart';

class WikiSettingsClient {
  final Uri baseUrl; // identity :7004
  final String? Function() bearerProvider;

  WikiSettingsClient({required this.baseUrl, required this.bearerProvider});

  Uri _u(String path) {
    final base = baseUrl.toString().replaceAll(RegExp(r'/+$'), '');
    return Uri.parse('$base$path');
  }

  /// 当前 Wiki 生成模型偏好；null = 未设置（跟随平台默认）。
  Future<String?> getIngestModel() async {
    final resp = await apiRequest(
      method: 'GET',
      url: _u('/v1/identity/me/settings/ingest-model'),
      bearerToken: bearerProvider(),
    );
    final model = (resp['model'] as String?) ?? '';
    return model.isEmpty ? null : model;
  }

  /// 设置 Wiki 生成模型偏好；null / 空串 = 清除（回到跟随平台默认）。
  Future<void> putIngestModel(String? model) async {
    await apiRequest(
      method: 'PUT',
      url: _u('/v1/identity/me/settings/ingest-model'),
      bearerToken: bearerProvider(),
      body: {'model': model ?? ''},
    );
  }
}

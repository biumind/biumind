// AuthService — credential resolver.
//
// Single source of truth: AppSettings.identityUrl + accessToken.
// model-relay URL is derived from identityUrl by replacing the port (:7004 →
// :7001). This collapses what used to be three duplicated fields
// (hubUrl / relayToken / identityUrl) into one configured value.
//
// Env var fallback (BIUMIND_MODEL_RELAY_URL / BIUMIND_TOKEN) is kept for CI /
// scripted dev runs but is rarely needed once the user has signed in
// through the UI.

import 'dart:io' show Platform;

import 'package:flutter/foundation.dart' show kIsWeb;
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../features/settings/application/settings_controller.dart';
import 'settings_repo.dart';

class HubCredentials {
  final Uri endpoint; // model-relay :7001
  final String bearerToken;
  const HubCredentials({required this.endpoint, required this.bearerToken});
}

class AuthService {
  AuthService(this._settings);
  final AppSettings _settings;

  HubCredentials? resolve() {
    final relayUri = _settings.hubUri;
    final tok = _settings.accessToken;
    if (relayUri != null && tok != null && tok.isNotEmpty) {
      return HubCredentials(endpoint: relayUri, bearerToken: tok);
    }
    // Env-var fallback only on native — Platform.environment throws on
    // Flutter Web. Web users always go through the settings UI.
    if (kIsWeb) return null;
    final envUrl = Platform.environment['BIUMIND_MODEL_RELAY_URL'];
    final envTok = Platform.environment['BIUMIND_TOKEN'];
    if (envUrl != null && envUrl.isNotEmpty && envTok != null && envTok.isNotEmpty) {
      return HubCredentials(endpoint: Uri.parse(envUrl), bearerToken: envTok);
    }
    return null;
  }
}

final authServiceProvider = Provider<AuthService>((ref) {
  final settings =
      ref.watch(settingsControllerProvider).valueOrNull ?? const AppSettings();
  return AuthService(settings);
});

final hubCredentialsProvider = Provider<HubCredentials?>((ref) {
  return ref.watch(authServiceProvider).resolve();
});

/// 登录态（bool derived）—— 只在 登录↔登出 翻转时变化。
///
/// **为什么单独抽 bool**：hubCredentialsProvider 每次 token 轮换（每小时）
/// 都重 emit 一个新 HubCredentials 对象（resolve() 每次返新实例 + 该类无
/// == 重写），下游任何 watch/listen 它的都会被判定"变了"。router 的
/// refreshListenable 若听原值，每次轮换 → GoRouter refresh → 整路由栈
/// rebuild → 所有页面闪动。听这个 bool 就只在真正 登录/登出 时 refresh。
///
/// 规则：router / authz / 只关心"是否登录"的 consumer 用这个；要 token
/// 字符串做 HTTP 的 transport 用 hubCredentialsProvider（且配合 select
/// endpoint + ref.read token，见各 *_providers.dart）。
final isAuthenticatedProvider = Provider<bool>(
  (ref) => ref.watch(hubCredentialsProvider) != null,
);

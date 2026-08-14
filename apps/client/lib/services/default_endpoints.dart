// Default endpoints for first-run UX.
//
// 单 origin 寻址: client 一律指向 site 这一个统一入口, 由 site nginx
// 按 /v1/* 路径反代到各后端。client 侧不换端口。
//
// Native (desktop / mobile) → 预置官方云端入口 https://biumind.xxlab.tech;
//                              打包期可用 --dart-define=BIUMIND_DEFAULT_SERVER_URL=...
//                              覆盖 (如自托管部署方或本地全栈开发填 http://localhost:8088,
//                              即 deploy/docker-compose 的 site 服务, SITE_PORT 默认 8088)。
// Web                       → 同源 (Uri.base.origin), 由托管方决定。

import 'package:flutter/foundation.dart' show kIsWeb;

/// Native 端首启的默认服务器地址, 打包期可用 --dart-define 覆盖.
const String _defaultServerUrl = String.fromEnvironment(
  'BIUMIND_DEFAULT_SERVER_URL',
  defaultValue: 'https://biumind.xxlab.tech',
);

/// 客户端首次启动时填充到登录页 / 设置页的默认 identity URL.
/// Native 返回预置的默认服务器地址; Web 取同源 origin.
/// 用户改过之后, 这个函数就不再被调用 (走 SettingsRepo 持久化值).
String defaultIdentityUrl() {
  if (kIsWeb) {
    // 同源, /v1/* 落到 site nginx 反代.
    return Uri.base.origin;
  }
  return _defaultServerUrl;
}

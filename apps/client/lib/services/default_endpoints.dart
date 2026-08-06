// Default endpoints for first-run UX.
//
// 单 origin 寻址: client 一律指向 site 这一个统一入口, 由 site nginx
// 按 /v1/* 路径反代到各后端。client 侧不换端口。
//
// Native (desktop / mobile) → 无预设（首启登录页/设置页留空, 用户填自己的服务器地址;
//                              本地全栈开发填 http://localhost:8088, 即 deploy/docker-compose
//                              的 site 服务, SITE_PORT 默认 8088）。
// Web                       → 同源 (Uri.base.origin), 由托管方决定。
//
// 不硬编任何运营方域名: 自托管 / 云端 SaaS 都由用户或部署方显式配置。

import 'package:flutter/foundation.dart' show kIsWeb;

/// 客户端首次启动时填充到登录页 / 设置页的默认 identity URL.
/// Native 返回空串(首启留空, 强制用户填自己的服务器); Web 取同源 origin.
/// 用户改过之后, 这个函数就不再被调用 (走 SettingsRepo 持久化值).
String defaultIdentityUrl() {
  if (kIsWeb) {
    // 同源, /v1/* 落到 site nginx 反代.
    return Uri.base.origin;
  }
  // Native: 不硬编运营方域名, 首启留空让用户填自己的服务器地址.
  return '';
}

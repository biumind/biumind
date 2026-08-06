// app_icon_resolver — 统一解析 manifest.icon 字段。
//
// manifest.icon 接受 4 种值:
//
//   1. 空字符串 — 客户端按 fallback (首字母 / 默认 IconData) 渲染。
//   2. emoji / 短文字 (e.g. "📰") — 直接渲染文本。
//   3. http(s):// URL — 公网图标 (用户自填 webview / 第三方 marketplace
//      catalog) — Image.network 直接拉。
//   4. cas:<sha256> — 已上传到 Files CAS 的二进制; 客户端拼成 brain
//      `/v1/brain/files-by-sha/<sha>` URL + Bearer header 拉 (单 origin, site nginx 反代)。
//
// 这里给所有展示 app 的 surface (sidebar / catalog tile / detail page /
// install dialog) 共用一个解析路径, 避免逻辑分散导致行为不一致。

import '../../../services/auth_service.dart';

/// 解析 [icon] 字段成 (imageUrl, headers) 元组。
///
/// 返回 (null, null) 表示调用方应该走 fallback (emoji 文本 / 首字母 /
/// 默认 IconData)。
(String?, Map<String, String>?) resolveAppIcon(
    String icon, HubCredentials? creds) {
  if (icon.isEmpty) return (null, null);
  if (icon.startsWith('cas:')) {
    final sha = icon.substring(4);
    if (creds == null || sha.length != 64) return (null, null);
    final url =
        creds.endpoint.replace(path: '/v1/brain/files-by-sha/$sha').toString();
    return (url, {'Authorization': 'Bearer ${creds.bearerToken}'});
  }
  if (icon.startsWith('http')) {
    return (icon, null);
  }
  // emoji / 短文字 — 调用方按文本渲。
  return (null, null);
}

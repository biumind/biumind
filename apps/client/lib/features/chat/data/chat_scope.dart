// ChatOwnerScope —— 本地 chat 数据的「环境 × 账号」隔离键（P0 数据隔离）。
//
// 设计文档 docs/BiuMind-Local-Data-Isolation-Design.md §2：
//
//   ownerKey = sha256(normalize(identityUrl)) + ":" + userId
//
// - identityUrl 用 hubCredentialsProvider 的 endpoint 代替 —— endpoint 由
//   identityUrl 1:1 派生（auth_service.dart，端口 :7004 → :7001），同一环境
//   恒定、不同环境必不同，隔离语义等价。
// - userId = JWT sub（复用 chat_events_realtime.decodeJwtUserId，与 realtime
//   topic `chat:user:<sub>` 同一约定）。
// - normalize：scheme/host 小写（Uri.parse 已做）、去尾部 `/`、去默认端口
//   （80/443）—— 同一环境的地址写法差异不得产生不同 scope。
// - 未登录 / token 非 JWT（无 sub）时 scope 为 null：调用方退化为空流 /
//   空结果 / 不写库。
//
// 消费方：chatControllerDepsProvider / chatSyncServiceProvider 构造 ChatRepo
// 时把 scope 绑进实例（编译期必填，不存在「不过滤」的调用路径）。

import 'dart:convert';

import 'package:crypto/crypto.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../services/auth_service.dart';
import '../sync/chat_events_realtime.dart' show decodeJwtUserId;

/// 从登录凭据派生 ownerKey；无法派生（token 缺 sub）返回 null。
String? chatOwnerKeyFromCredentials(HubCredentials creds) {
  return accountIdFromEndpoint(creds.endpoint, creds.bearerToken);
}

/// 从 endpoint + access JWT 派生 accountId —— 与 chat ownerKey 完全同构
/// （sha256(normalize(endpoint)) + ':' + JWT sub）。P2 多账号的
/// account_registry 用它做账号主键，保证切账号后 Drift scope 过滤天然
/// 命中对应账号的本地数据。token 非 JWT（解不出 sub）返回 null。
String? accountIdFromEndpoint(Uri endpoint, String accessToken) {
  final userId = decodeJwtUserId(accessToken);
  if (userId == null) return null;
  final envHash = sha256
      .convert(utf8.encode(normalizeIdentityUrl(endpoint)))
      .toString();
  return '$envHash:$userId';
}

/// 环境地址归一化：小写（Uri 已保证）+ 去默认端口 + 去尾部 `/`。
/// 纯函数导出以便单测直接覆盖。
String normalizeIdentityUrl(Uri uri) {
  final hasNonDefaultPort = uri.hasPort &&
      !((uri.scheme == 'http' && uri.port == 80) ||
          (uri.scheme == 'https' && uri.port == 443));
  var s = '${uri.scheme}://${uri.host}';
  if (hasNonDefaultPort) s += ':${uri.port}';
  var path = uri.path;
  while (path.endsWith('/')) {
    path = path.substring(0, path.length - 1);
  }
  return s + path;
}

/// 当前登录态的 chat owner scope；未登录 / token 非 JWT 时为 null。
final chatOwnerScopeProvider = Provider<String?>((ref) {
  final creds = ref.watch(hubCredentialsProvider);
  if (creds == null) return null;
  return chatOwnerKeyFromCredentials(creds);
});

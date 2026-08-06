// 数据统计 client provider —— 单 origin: brain (/v1/chat/stats) 与 relay
// (/v1/me/usage) 都走同一个 endpoint, 由 site nginx 按路径反代。creds == null
// (未登录) → null,UI 提示登录。

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../services/auth_service.dart';
import '../data/stats_client.dart';

final statsClientProvider = Provider<StatsClient?>((ref) {
  final creds = ref.watch(hubCredentialsProvider);
  if (creds == null) return null;
  return StatsClient(
    brainBaseUrl: creds.endpoint,
    relayBaseUrl: creds.endpoint,
    bearerProvider: () => creds.bearerToken,
  );
});

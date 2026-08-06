// Riverpod providers for the runtime Skills surface.
//
// The client is null when no model-relay credentials are configured —
// mirrors memory_providers.dart so the UI shows the same
// "configure Settings first" state across all cloud-backed
// features instead of crashing.

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../services/auth_service.dart';
import 'api/skill_client.dart';

final skillClientProvider = Provider<SkillClient?>((ref) {
  final creds = ref.watch(hubCredentialsProvider);
  if (creds == null) return null;
  // 单 origin: Skills routes (/v1/skills/*) mount on Runtime; site nginx
  // 反代到 runtime, client 不换端口(:7002 从不公网直连).
  return SkillClient(creds.endpoint, creds.bearerToken);
});

/// SWR-style auto-refresh of the cloud skills list。
///
/// select(endpoint) 做 rebuild key —— token 轮换不触发重拉 (baseUrl 不变),
/// 只有登录登出 / 换服务器才重拉。client 经 ref.read 取 (轮换后新实例带
/// 新 token, 但不作 rebuild 依赖)。避免 skills 页每小时闪一次。
/// UI calls `ref.refresh(skillsListProvider)` after install / delete to
/// surface the change immediately.
final skillsListProvider = FutureProvider<List<Skill>>((ref) async {
  ref.watch(skillClientProvider.select((c) => c?.baseUrl));
  final client = ref.read(skillClientProvider);
  if (client == null) return const [];
  return client.list();
});

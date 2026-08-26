// 笔记分享（S1）的 Riverpod providers。
//
// 设计定案：分享状态**不进 Drift**，全部实时拉服务端（brain 管理端 5 个
// 接口，见 notes_client.dart 的 Share 区；契约 docs/BiuMind-Technical-
// Architecture.md §7.6「API 契约（S1 冻结）」）。
//
//   * noteShareClientProvider  NotesClient 实例（跟随 hub credentials）
//   * noteShareProvider        单篇分享状态（分享弹层用；404 归一为 null）
//   * myNoteSharesProvider     我的分享列表 —— 笔记列表徽标与设置页
//                              「我的分享」管理列表共用同一数据源（契约要求）
//   * activeNoteShareMapProvider  活跃分享的 noteId→item 映射（列表徽标）
//
// 失效刷新：创建 / 停用 / 恢复 / rotate / 改配置后由调用方走
// invalidateNoteShareProviders（单篇 + 列表一起失效）。

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../data/api/notes_client.dart' as api;
import '../../../services/auth_service.dart';

/// 分享管理端 HTTP client —— 跟随登录态重建（同 notes_providers 的接线）。
final noteShareClientProvider = Provider<api.NotesClient?>((ref) {
  final creds = ref.watch(hubCredentialsProvider);
  if (creds == null) return null;
  return api.NotesClient(creds.endpoint, creds.bearerToken);
});

/// 单篇笔记的分享状态；无分享（服务端 404）归一为 null。
final noteShareProvider = FutureProvider.autoDispose
    .family<api.NoteShare?, String>((ref, noteId) async {
      final client = ref.watch(noteShareClientProvider);
      if (client == null) return null;
      try {
        return await client.getShare(noteId);
      } on api.NotesApiError catch (e) {
        if (e.isNotFound) return null;
        rethrow;
      }
    });

/// 我的分享列表（所有状态：active / disabled / expired）。
final myNoteSharesProvider = FutureProvider<List<api.NoteShareListItem>>((
  ref,
) async {
  final client = ref.watch(noteShareClientProvider);
  if (client == null) return const <api.NoteShareListItem>[];
  return client.listShares();
});

/// 活跃分享的 noteId → 列表项映射 —— 笔记列表项的外链徽标数据源。
/// 只有 status == active 的分享显示徽标（已停用 / 已过期不显示）。
final activeNoteShareMapProvider =
    Provider<AsyncValue<Map<String, api.NoteShareListItem>>>((ref) {
      return ref
          .watch(myNoteSharesProvider)
          .whenData(
            (items) => {
              for (final item in items)
                if (item.status == api.NoteShareStatus.active)
                  item.noteId: item,
            },
          );
    });

/// 分享 URL 拼接 —— 契约：服务端不返回 url 字段，客户端用 origin 自行
/// 拼接 `${origin}/s/${token}`（自托管 origin 各异；单 origin 寻址下
/// endpoint 即站点 origin，落地页 /s/ 由 site nginx 反代）。
String noteShareUrl(Uri origin, String token) =>
    origin.replace(path: '/s/$token').toString();

/// 从 expires_at 反推有效期档位（1d/7d/30d/never）—— 契约的 PUT body
/// `expires_in` 每次必传，恢复分享 / 回显有效期选择器时按剩余时间归桶。
/// 已过期的归 'never'（恢复时给永久，让用户自行再选）。
String noteShareExpiresInOf(DateTime? expiresAt, DateTime now) {
  if (expiresAt == null) return 'never';
  final rem = expiresAt.difference(now);
  if (rem <= Duration.zero) return 'never';
  if (rem <= const Duration(hours: 36)) return '1d';
  if (rem <= const Duration(days: 8)) return '7d';
  if (rem <= const Duration(days: 31)) return '30d';
  return 'never';
}

/// 有效期展示文案（分享弹层 / 我的分享列表共用）。
String noteShareExpiryLabel(DateTime? expiresAt, DateTime now) {
  if (expiresAt == null) return '永久有效';
  final rem = expiresAt.difference(now);
  if (rem <= Duration.zero) return '已过期';
  if (rem.inHours < 1) return '1 小时内到期';
  if (rem.inDays < 1) return '${rem.inHours} 小时后到期';
  return '${rem.inDays} 天后到期';
}

/// 任何分享变更（创建 / 停用 / 恢复 / rotate / 改密码 / 改有效期）后
/// 统一失效：单篇状态 + 列表（徽标与管理页同源）。
void invalidateNoteShareProviders(WidgetRef ref, String noteId) {
  ref.invalidate(noteShareProvider(noteId));
  ref.invalidate(myNoteSharesProvider);
}

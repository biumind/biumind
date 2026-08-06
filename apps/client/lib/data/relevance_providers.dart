// Riverpod providers for RelevanceClient.
//
// The `relatedPagesProvider(pageId)` family auto-fetches when the
// active page changes; consumers (the wiki editor's "see also" rail)
// just watch with the current page id and React-style re-render.

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../services/auth_service.dart';
import 'api/relevance_client.dart';

/// Relatedness REST client — null when no model-relay credentials are
/// configured.
final relevanceClientProvider = Provider<RelevanceClient?>((ref) {
  final creds = ref.watch(hubCredentialsProvider);
  if (creds == null) return null;
  return RelevanceClient(creds.endpoint, creds.bearerToken);
});

/// Top-K related pages for one page id. Empty list when the relevance
/// worker hasn't populated rows yet (or model-relay creds missing).
///
/// select(endpoint) 做 rebuild key —— token 轮换 (每小时) 不触发重拉
/// (baseUrl 没变), 只有换服务器 / 登录登出 (endpoint 变) 才重拉。client
/// 实例经 ref.read 取 (轮换后是带新 token 的新实例, 但不作为 rebuild
/// 依赖)。避免 wiki 编辑器 "see also" 栏每小时闪一次。
final relatedPagesProvider =
    FutureProvider.family.autoDispose<List<RelatedPage>, String>(
  (ref, pageId) async {
    ref.watch(relevanceClientProvider.select((c) => c?.baseUrl));
    final client = ref.read(relevanceClientProvider);
    if (client == null || pageId.isEmpty) return const [];
    return client.listRelated(pageId, limit: 8);
  },
);

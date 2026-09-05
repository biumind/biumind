/// Riverpod providers for the per-project page list + page detail.
///
/// 适配 biumind 体系：knowcode 原版直连 apiRepositoryProvider 拉
/// WikiPageSummary / WikiPage；biumind 走 wikiControllerProvider
/// （含本地 Drift + outbox 优先、网络背书）。这里把 family provider
/// 实现成对 wikiControllerProvider 状态的派生选择器，让 project_browser_page
/// 等迁过来的 widget 能继续用熟悉的 `pagesListProvider(projectId)` 接口。
library;

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../../data/wiki_providers.dart' show wikiRepositoryProvider;
import '../../../../data/wiki_repository.dart' show RepoPage;
import '../../application/sync_provider.dart' show wikiSyncEventsProvider;
import '../../application/wiki_controller.dart';

/// 当前项目的 page list。
///
/// 切到非 activeProject 时返回空 — 调用方负责（通过 wiki_controller.selectProject）
/// 把该项目设成 active；ProjectBrowserPage 已在 _sync() 里处理。
final pagesListProvider =
    Provider.family<List<RepoPage>, String>((ref, projectId) {
  final state = ref.watch(wikiControllerProvider);
  final s = state.valueOrNull;
  if (s == null) return const <RepoPage>[];
  if (s.activeProject?.id != projectId) return const <RepoPage>[];
  return s.pages;
});

class PageRequest {
  const PageRequest({required this.projectId, required this.pageId});
  final String projectId;
  final String pageId;

  @override
  bool operator ==(Object other) =>
      other is PageRequest &&
      other.projectId == projectId &&
      other.pageId == pageId;
  @override
  int get hashCode => Object.hash(projectId, pageId);
}

/// 单页 metadata（不含 blocks）。
final pageDetailProvider = Provider.family<RepoPage?, PageRequest>((ref, req) {
  final list = ref.watch(pagesListProvider(req.projectId));
  for (final p in list) {
    if (p.id == req.pageId) return p;
  }
  return null;
});

/// 单页 body_md（权威 markdown 正文）—— read 模式 WikiReaderView 的数据源。
///
/// 本地 Drift 不存 body_md，走 server getPage 拉取（与编辑态 _ensureBody
/// 同链路）。缓存策略与编辑态一致：每页拉一次，autoDispose 切页回收；
/// syncws `page.updated` / `page.created` 命中本页时 invalidateSelf 重拉，
/// 本地编辑保存成功也由 WikiPageDetail._saveBody 主动 invalidate。
final pageBodyMdProvider =
    FutureProvider.family.autoDispose<String, PageRequest>((ref, req) async {
  ref.listen<AsyncValue<Map<Object?, Object?>>>(
    wikiSyncEventsProvider(req.projectId),
    (_, next) => next.whenData((event) {
      final entity = event['entity'];
      final op = event['op'];
      if (entity == 'page' &&
          (op == 'updated' || op == 'created') &&
          event['entity_id'] == req.pageId) {
        ref.invalidateSelf();
      }
    }),
  );
  final repo = ref.watch(wikiRepositoryProvider);
  if (repo == null) return '';
  final page = await repo.getPage(req.projectId, req.pageId);
  return page.bodyMd;
});

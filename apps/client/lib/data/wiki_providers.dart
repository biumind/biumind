// Riverpod providers for the Wiki data stack.
//
// AppDb → WikiDao → WikiRepository (with WikiClient) → WikiOutboxFlusher.
// All four providers rebuild when the user's model-relay credentials change so that
// switching workspaces / logging out fully resets the stack.

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../services/auth_service.dart';
import 'api/wiki_client.dart' as api;
import 'local/db.dart';
import 'local/wiki_dao.dart';
import 'outbox/wiki_outbox_flusher.dart';
import 'wiki_repository.dart';

/// Singleton sqlite/Drift database. Survives credential changes.
final appDbProvider = Provider<AppDb>((ref) {
  final db = AppDb();
  ref.onDispose(db.close);
  return db;
});

final wikiDaoProvider = Provider<WikiDao>((ref) {
  return WikiDao(ref.watch(appDbProvider));
});

/// Repository — null when no model-relay credentials are configured.
final wikiRepositoryProvider = Provider<WikiRepository?>((ref) {
  final creds = ref.watch(hubCredentialsProvider);
  if (creds == null) return null;
  final client = api.WikiClient(creds.endpoint, creds.bearerToken);
  return WikiRepository(
    dao: ref.watch(wikiDaoProvider),
    client: client,
  );
});

/// Outbox flusher — auto-starts when a repository becomes available, stops
/// (and disposes) when credentials are cleared.
final wikiOutboxFlusherProvider = Provider<WikiOutboxFlusher?>((ref) {
  final repo = ref.watch(wikiRepositoryProvider);
  if (repo == null) return null;
  final flusher = WikiOutboxFlusher(dao: repo.dao, client: repo.client)..start();
  ref.onDispose(flusher.dispose);
  return flusher;
});

/// Live count of pending outbox entries — UI shows it as a "syncing" badge.
final pendingWriteCountProvider = StreamProvider<int>((ref) {
  final dao = ref.watch(wikiDaoProvider);
  return dao.watchOutboxCount();
});

/// Reverse `[[wikilinks]]` for the active page. autoDispose so a new
/// page selection drops the old request immediately.
///
/// select(repo.client.baseUrl): token 轮换不重拉 —— wiki 编辑器反链栏
/// 不每小时闪。
final backlinksProvider = FutureProvider.family
    .autoDispose<List<api.WikiBacklink>, _BacklinkArgs>(
  (ref, args) async {
    ref.watch(wikiRepositoryProvider.select((r) => r?.client.baseUrl));
    final repo = ref.read(wikiRepositoryProvider);
    if (repo == null || args.pageId.isEmpty || args.projectId.isEmpty) {
      return const [];
    }
    try {
      return await repo.client.listBacklinks(args.projectId, args.pageId);
    } catch (_) {
      // Hide errors from the rail; the editor is the focal point.
      return const [];
    }
  },
);

class _BacklinkArgs {
  final String projectId;
  final String pageId;
  const _BacklinkArgs(this.projectId, this.pageId);
  @override
  bool operator ==(Object other) =>
      other is _BacklinkArgs &&
      other.projectId == projectId &&
      other.pageId == pageId;
  @override
  int get hashCode => Object.hash(projectId, pageId);
}

/// Helper to build the family key without exposing _BacklinkArgs.
({String projectId, String pageId}) backlinkKey(
        String projectId, String pageId) =>
    (projectId: projectId, pageId: pageId);

/// Convenience: pass (projectId, pageId) directly via record.
final backlinksFor = Provider.family.autoDispose<
    AsyncValue<List<api.WikiBacklink>>,
    ({String projectId, String pageId})>(
  (ref, key) => ref.watch(backlinksProvider(_BacklinkArgs(
    key.projectId,
    key.pageId,
  ))),
);

// ─── Page Revisions (页版本历史，迁移 00065) ─────────────────
//
// revisions 纯服务端（本地不镜像）；UI 弹层用此 provider 拉列表，详情按需走
// repo.getPageRevision（列表响应不含 blocks_json）。restore/save-as-copy 后
// ref.invalidate 刷新。

final pageRevisionsProvider = FutureProvider.autoDispose
    .family<List<api.WikiPageRevision>, ({String projectId, String pageId})>(
  (ref, key) async {
    final repo = ref.watch(wikiRepositoryProvider);
    if (repo == null) return const <api.WikiPageRevision>[];
    return repo.listPageRevisions(key.projectId, key.pageId, limit: 50);
  },
);

// ─── Sources (B2) ─────────────────────────────────────────────
//
// 当前简化方案（B2.3）：直接走网络，不下 Drift。sources_page UI 用
// FutureProvider + ref.invalidate 做手动刷新；待 B2.6 接 ingest SSE 后
// 把 source.parse_status 推流改为实时更新。

final sourcesListProvider =
    FutureProvider.family<List<api.WikiSource>, String>(
  (ref, projectId) async {
    // select(endpoint): token 轮换不重拉。
    ref.watch(wikiRepositoryProvider.select((r) => r?.client.baseUrl));
    final repo = ref.read(wikiRepositoryProvider);
    if (repo == null || projectId.isEmpty) return const [];
    return repo.client.listSources(projectId);
  },
);

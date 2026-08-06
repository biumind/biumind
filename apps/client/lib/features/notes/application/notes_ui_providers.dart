// Notes UI 层 providers —— 列表过滤 / 选中态 / watch 流封装。
//
// 数据层（repository / flusher / poller）在 data/notes_providers.dart，
// 这里只做 presentation 需要的状态派生：
//   * notesFilterProvider    左栏过滤（全部 / 未归档 / 待办 / 某笔记本 / 某
//                            标签，N2 起互斥单选，选中即切换）
//   * selectedNoteIdProvider 中栏选中的笔记（桌面三栏右栏编辑器用）
//   * notesNotebooksProvider / notesListProvider / notesTrashProvider /
//     notesTagsProvider     repo watch 流的 StreamProvider 封装（repo ==
//                           null 时空流）
//   * noteByIdProvider       单笔记 watch（编辑器远端覆盖检测用）
//   * noteTagIdsProvider     单笔记标签 id（数据层只有 Future 版，
//                           FutureProvider + invalidate 绕行）
//
// 列表排序：设计 §6.1 按 updatedAt 倒序（DAO 主序是 position，目前全 0，
// 这里显式再排一次保证语义）；待办视图走 sortTodoNotes（未完成在前按
// position，已完成在后按完成时间倒序）。

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../data/api/notes_client.dart' as api;
import '../../../data/notes_providers.dart';
import '../../../data/notes_repository.dart';
import '../../../data/wiki_providers.dart';
import '../../../data/wiki_repository.dart';

/// 左栏过滤选择。N2 起互斥单选：全部 / 未归档 / 待办 / 某笔记本 / 某标签。
enum NotesListKind { all, unfiled, todo, notebook, tag }

class NotesFilter {
  const NotesFilter.all()
      : kind = NotesListKind.all,
        notebookId = null,
        tagId = null;
  const NotesFilter.unfiled()
      : kind = NotesListKind.unfiled,
        notebookId = null,
        tagId = null;
  const NotesFilter.todo()
      : kind = NotesListKind.todo,
        notebookId = null,
        tagId = null;
  const NotesFilter.notebook(String id)
      : kind = NotesListKind.notebook,
        notebookId = id,
        tagId = null;
  const NotesFilter.tag(String id)
      : kind = NotesListKind.tag,
        notebookId = null,
        tagId = id;

  final NotesListKind kind;
  final String? notebookId;
  final String? tagId;

  @override
  bool operator ==(Object other) =>
      other is NotesFilter &&
      other.kind == kind &&
      other.notebookId == notebookId &&
      other.tagId == tagId;

  @override
  int get hashCode => Object.hash(kind, notebookId, tagId);
}

final notesFilterProvider =
    StateProvider<NotesFilter>((ref) => const NotesFilter.all());

/// 中栏当前选中的笔记 id（桌面三栏 / 手机详情页共用）。
final selectedNoteIdProvider = StateProvider<String?>((ref) => null);

/// 搜索关键词（搜索框防抖 ~300ms 后写入）。空串 = 未在搜索，中栏显示
/// 原过滤源列表；非空时中栏切换为搜索结果视图，清空即回原视图。
/// 与 notesFilterProvider 并存、互不影响。
final notesSearchQueryProvider = StateProvider<String>((ref) => '');

/// 全文搜索结果 —— 纯服务端调用（brain GET /v1/notes/search），不经本地
/// Drift。无本地降级：离线/报错时由 UI 显示错误态 + 重试（本地 FTS 降级
/// 属 N2 范围外，后续再议）。
final notesSearchResultsProvider =
    FutureProvider<List<api.NoteSearchResult>>((ref) async {
  final q = ref.watch(notesSearchQueryProvider).trim();
  if (q.isEmpty) return const <api.NoteSearchResult>[];
  final repo = ref.watch(notesRepositoryProvider);
  if (repo == null) return const <api.NoteSearchResult>[];
  return repo.searchNotes(q);
});

final notesNotebooksProvider = StreamProvider<List<RepoNotebook>>((ref) {
  final repo = ref.watch(notesRepositoryProvider);
  if (repo == null) return const Stream.empty();
  return repo.watchNotebooks();
});

final notesListProvider = StreamProvider<List<RepoNote>>((ref) {
  final repo = ref.watch(notesRepositoryProvider);
  if (repo == null) return const Stream.empty();
  final filter = ref.watch(notesFilterProvider);
  if (filter.kind == NotesListKind.todo) {
    return repo.watchNotes(todoOnly: true).map(sortTodoNotes);
  }
  final stream = switch (filter.kind) {
    NotesListKind.all => repo.watchNotes(),
    NotesListKind.unfiled => repo.watchNotes(rootOnly: true),
    NotesListKind.notebook => repo.watchNotes(notebookId: filter.notebookId),
    NotesListKind.tag => repo.watchNotesForTag(filter.tagId ?? ''),
    NotesListKind.todo => throw StateError('unreachable'),
  };
  return stream.map((notes) => notes
    ..sort((a, b) => b.updatedAt.compareTo(a.updatedAt)));
});

/// 待办视图排序：未完成在前（position 升序），已完成在后（完成时间倒序）。
/// 纯函数，供 notesListProvider 与单测复用。
List<RepoNote> sortTodoNotes(List<RepoNote> notes) {
  final pending = <RepoNote>[];
  final done = <RepoNote>[];
  for (final n in notes) {
    (n.todoCompletedAt == null ? pending : done).add(n);
  }
  pending.sort((a, b) => a.position.compareTo(b.position));
  done.sort((a, b) => b.todoCompletedAt!.compareTo(a.todoCompletedAt!));
  return <RepoNote>[...pending, ...done];
}

final notesTrashProvider = StreamProvider<List<RepoNote>>((ref) {
  final repo = ref.watch(notesRepositoryProvider);
  if (repo == null) return const Stream.empty();
  return repo.watchTrash();
});

/// 全部标签（左栏「标签」区 + 编辑器标签行共用）。
final notesTagsProvider = StreamProvider<List<RepoTag>>((ref) {
  final repo = ref.watch(notesRepositoryProvider);
  if (repo == null) return const Stream.empty();
  return repo.watchTags();
});

/// 单笔记的标签 id。数据层 listTagIdsForNote 只有 Future 版（DAO 无
/// note-tag 关联的 watch），按约束用 FutureProvider + invalidate 绕行：
/// 本地 setNoteTags 后由调用方 invalidate；远端 note.tags_updated 事件
/// 落库不触发刷新，需重开编辑器才反映（N2 可接受缺口，已在汇报说明）。
final noteTagIdsProvider =
    FutureProvider.family<List<String>, String>((ref, noteId) async {
  final repo = ref.watch(notesRepositoryProvider);
  if (repo == null) return const <String>[];
  return repo.listTagIdsForNote(noteId);
});

/// 单笔记 watch —— 编辑器用它检测「远端版本推进」（轮询落库后 version
/// 变化且非本机 pending），再经 EditorBridgeController.setDoc 覆盖。
final noteByIdProvider = StreamProvider.family<RepoNote?, String>((ref, id) {
  final repo = ref.watch(notesRepositoryProvider);
  if (repo == null) return const Stream.empty();
  return repo.watchNotes().map((notes) {
    for (final n in notes) {
      if (n.id == id) return n;
    }
    return null;
  });
});

/// 版本历史列表（N3）—— 纯服务端调用，不进本地镜像。restore 后由调用方
/// invalidate（恢复会新增一条恢复点）。
final noteRevisionsProvider =
    FutureProvider.autoDispose.family<List<api.NoteRevision>, String>(
        (ref, noteId) async {
  final repo = ref.watch(notesRepositoryProvider);
  if (repo == null) return const <api.NoteRevision>[];
  return repo.listRevisions(noteId, limit: 50);
});

/// 转入知识库的 wiki project 选项（N3）—— 复用 wiki 数据栈：先 best-effort
/// 从服务端刷新，再读本地镜像（离线时用缓存，为空则由 UI 提示）。
final wikiProjectsForPromoteProvider =
    FutureProvider.autoDispose<List<RepoProject>>((ref) async {
  final repo = ref.watch(wikiRepositoryProvider);
  if (repo == null) return const <RepoProject>[];
  try {
    await repo.refreshProjects();
  } catch (_) {
    // 离线/网络失败时用本地缓存，与 WikiController 同一策略。
  }
  return repo.watchProjects().first;
});

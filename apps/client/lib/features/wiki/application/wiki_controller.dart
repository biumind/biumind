// WikiController — local-first wiki state.
//
// Reads come from Drift via [WikiRepository.watchProjects/Pages/Blocks].
// Writes are optimistic: the repository updates Drift immediately and
// enqueues an outbox entry; the flusher uploads in the background and the
// stream re-fires once server-issued ids land.
//
// Refresh from the network is best-effort: if it fails (offline, model-relay down)
// the cached data still renders.

import 'dart:async';

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:meta/meta.dart';

import '../../../data/api/wiki_client.dart' show WikiApiError;
import '../../../data/wiki_providers.dart';
import '../../../data/wiki_repository.dart';

@immutable
class WikiState {
  final List<RepoProject> projects;
  final RepoProject? activeProject;
  final List<RepoPage> pages;
  final RepoPage? activePage;
  final List<RepoBlock> blocks;
  final bool noCredentials;
  final String? lastError;

  const WikiState({
    this.projects = const [],
    this.activeProject,
    this.pages = const [],
    this.activePage,
    this.blocks = const [],
    this.noCredentials = false,
    this.lastError,
  });

  WikiState copyWith({
    List<RepoProject>? projects,
    Object? activeProject = _unset,
    List<RepoPage>? pages,
    Object? activePage = _unset,
    List<RepoBlock>? blocks,
    bool? noCredentials,
    Object? lastError = _unset,
  }) {
    return WikiState(
      projects: projects ?? this.projects,
      activeProject: activeProject == _unset
          ? this.activeProject
          : activeProject as RepoProject?,
      pages: pages ?? this.pages,
      activePage:
          activePage == _unset ? this.activePage : activePage as RepoPage?,
      blocks: blocks ?? this.blocks,
      noCredentials: noCredentials ?? this.noCredentials,
      lastError: lastError == _unset ? this.lastError : lastError as String?,
    );
  }
}

const Object _unset = Object();

class WikiController extends AsyncNotifier<WikiState> {
  WikiRepository? _repo;
  StreamSubscription<List<RepoProject>>? _projectsSub;
  StreamSubscription<List<RepoPage>>? _pagesSub;
  StreamSubscription<List<RepoBlock>>? _blocksSub;

  @override
  Future<WikiState> build() async {
    ref.onDispose(() async {
      await _projectsSub?.cancel();
      await _pagesSub?.cancel();
      await _blocksSub?.cancel();
    });
    final repo = ref.watch(wikiRepositoryProvider);
    _repo = repo;
    if (repo == null) {
      return const WikiState(noCredentials: true);
    }

    // Best-effort refresh from server, then start streaming local rows.
    String? bootstrapError;
    try {
      await repo.refreshProjects();
    } catch (e) {
      bootstrapError = 'refresh projects: $e';
    }

    await _projectsSub?.cancel();
    final completer = Completer<List<RepoProject>>();
    _projectsSub = repo.watchProjects().listen((projects) {
      if (!completer.isCompleted) completer.complete(projects);
      _onProjectsTick(projects);
    });
    final initialProjects = await completer.future;

    if (initialProjects.isEmpty) {
      return WikiState(lastError: bootstrapError);
    }

    final active = initialProjects.first;
    final initialPages = await _attachPagesStream(active);
    final activePage = initialPages.isEmpty ? null : initialPages.first;
    final initialBlocks = activePage == null
        ? const <RepoBlock>[]
        : await _attachBlocksStream(activePage);

    return WikiState(
      projects: initialProjects,
      activeProject: active,
      pages: initialPages,
      activePage: activePage,
      blocks: initialBlocks,
      lastError: bootstrapError,
    );
  }

  WikiRepository get _api {
    final r = _repo;
    if (r == null) throw const WikiNoCredentialsError();
    return r;
  }

  Future<List<RepoPage>> _attachPagesStream(RepoProject p) async {
    await _pagesSub?.cancel();
    try {
      await _api.refreshPages(p.id);
    } catch (_) {}
    final completer = Completer<List<RepoPage>>();
    _pagesSub = _api.watchPages(p.id).listen((pages) {
      if (!completer.isCompleted) completer.complete(pages);
      _onPagesTick(pages);
    });
    return completer.future;
  }

  Future<List<RepoBlock>> _attachBlocksStream(RepoPage page) async {
    await _blocksSub?.cancel();
    try {
      await _api.refreshBlocks(page.projectId, page.id);
    } catch (_) {}
    final completer = Completer<List<RepoBlock>>();
    _blocksSub = _api.watchBlocks(page.id).listen((blocks) {
      if (!completer.isCompleted) completer.complete(blocks);
      _onBlocksTick(blocks);
    });
    return completer.future;
  }

  void _onProjectsTick(List<RepoProject> projects) {
    final st = state.value;
    if (st == null) return;
    // If the active project was a local placeholder that just got swapped
    // for a server id, re-pick by name.
    RepoProject? active = st.activeProject;
    if (active != null) {
      active = projects.firstWhere(
        (p) => p.id == active!.id,
        orElse: () => projects.firstWhere(
          (p) => p.name == active!.name,
          orElse: () => projects.isNotEmpty ? projects.first : active!,
        ),
      );
    } else if (projects.isNotEmpty) {
      active = projects.first;
    }
    state = AsyncData(st.copyWith(projects: projects, activeProject: active));
  }

  void _onPagesTick(List<RepoPage> pages) {
    final st = state.value;
    if (st == null) return;
    RepoPage? active = st.activePage;
    if (active != null) {
      active = pages.firstWhere(
        (p) => p.id == active!.id,
        orElse: () => pages.firstWhere(
          (p) => p.title == active!.title,
          orElse: () => pages.isEmpty ? active! : pages.first,
        ),
      );
    } else if (pages.isNotEmpty) {
      active = pages.first;
    }
    state = AsyncData(st.copyWith(pages: pages, activePage: active));
  }

  void _onBlocksTick(List<RepoBlock> blocks) {
    final st = state.value;
    if (st == null) return;
    state = AsyncData(st.copyWith(blocks: blocks));
  }

  Future<void> selectProject(RepoProject p) async {
    final pages = await _attachPagesStream(p);
    final activePage = pages.isEmpty ? null : pages.first;
    final blocks = activePage == null
        ? const <RepoBlock>[]
        : await _attachBlocksStream(activePage);
    final st = state.value ?? const WikiState();
    state = AsyncData(st.copyWith(
      activeProject: p,
      pages: pages,
      activePage: activePage,
      blocks: blocks,
    ));
  }

  Future<void> selectPage(RepoPage page) async {
    final blocks = await _attachBlocksStream(page);
    final st = state.value ?? const WikiState();
    state = AsyncData(st.copyWith(activePage: page, blocks: blocks));
  }

  /// Deep-link entry: jump to a page by id without the user having to
  /// click through projects + page list. Used by the reviews queue
  /// (P2-H-reviews) — clicking a page chip on a dedup/lint/sweep
  /// finding navigates to /wiki?pageId=… and we land directly on it.
  ///
  /// Lookup order:
  ///   1. current active project's `pages` list — the cheap case
  ///   2. other projects: refresh each project's pages, find the
  ///      page, swap projects then select. Worst-case O(N projects)
  ///      but N is small (single-digit) for current users.
  ///
  /// Returns true on success. False (no-op) when the page can't be
  /// found anywhere — caller can show a toast / fall back to the
  /// default landing.
  Future<bool> selectPageById(String pageId) async {
    final st = state.value;
    if (st == null) return false;

    // Fast path: page already in the loaded list of the active project.
    for (final p in st.pages) {
      if (p.id == pageId) {
        if (st.activePage?.id == pageId) return true; // already selected
        await selectPage(p);
        return true;
      }
    }

    // Slow path: scan other projects. We refresh each project's pages
    // (network + sqlite) before checking — the local cache could be
    // empty for a project the user has never opened in this session.
    for (final proj in st.projects) {
      if (st.activeProject != null && proj.id == st.activeProject!.id) {
        continue; // already scanned above
      }
      try {
        await _api.refreshPages(proj.id);
        final pages = await _api.watchPages(proj.id).first;
        for (final p in pages) {
          if (p.id == pageId) {
            await selectProject(proj);
            await selectPage(p);
            return true;
          }
        }
      } catch (_) {
        // Network / DB failure for one project shouldn't abort the
        // search — try the next one. Worst case the page is unreachable
        // and we return false to let the caller surface a friendly
        // error.
        continue;
      }
    }
    return false;
  }

  Future<RepoProject> createProject(String name, {String? templateId}) async {
    final p = await _api.createProject(name, templateId: templateId);
    await ref.read(wikiOutboxFlusherProvider)?.kick();
    return p;
  }

  Future<RepoPage> createPage(String title) async {
    final st = state.value;
    if (st?.activeProject == null) throw StateError('no active project');
    final page = await _api.createPage(st!.activeProject!.id, title: title);
    await ref.read(wikiOutboxFlusherProvider)?.kick();
    return page;
  }

  Future<RepoBlock> appendBlock({
    required String type,
    required Map<String, dynamic> content,
  }) async {
    final st = state.value;
    if (st == null || st.activeProject == null || st.activePage == null) {
      throw StateError('no active page');
    }
    final pos = st.blocks.isEmpty ? 1.0 : st.blocks.last.position + 1.0;
    final b = await _api.createBlock(
      st.activeProject!.id,
      st.activePage!.id,
      type: type,
      position: pos,
      content: content,
    );
    await ref.read(wikiOutboxFlusherProvider)?.kick();
    return b;
  }

  Future<void> updateBlockContent(
    RepoBlock block, {
    required Map<String, dynamic> content,
  }) async {
    final st = state.value!;
    try {
      await _api.updateBlock(
        st.activeProject!.id,
        block.id,
        content: content,
      );
      await ref.read(wikiOutboxFlusherProvider)?.kick();
    } on WikiApiError catch (e) {
      state = AsyncData(st.copyWith(lastError: 'update failed: ${e.body}'));
      rethrow;
    }
  }

  Future<void> deleteBlock(RepoBlock block) async {
    final st = state.value!;
    await _api.deleteBlock(st.activeProject!.id, block.id);
    await ref.read(wikiOutboxFlusherProvider)?.kick();
  }
}

class WikiNoCredentialsError implements Exception {
  const WikiNoCredentialsError();
  @override
  String toString() => 'Wiki not available: configure model-relay URL + token in Settings.';
}

final wikiControllerProvider =
    AsyncNotifierProvider<WikiController, WikiState>(WikiController.new);

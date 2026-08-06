import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../app/theme.dart';
import '../../../core/editor/editor_bridge_controller.dart';
import '../../../core/editor/editor_bridge_protocol.dart';
import '../../../core/editor/page_autosave.dart';
import '../../../core/editor/page_editor_view.dart';
import '../../../core/layout/form_factor.dart';
import '../../../data/api/relevance_client.dart';
import '../../../data/api/wiki_client.dart' show WikiBacklink;
import '../../../data/api/wiki_client.dart' as api;
import '../../../data/relevance_providers.dart';
import '../../../data/wiki_providers.dart';
import '../../../data/wiki_repository.dart' show RepoProject;
import '../../../l10n/app_localizations.dart';
import '../../../shared/page_scaffold.dart';
import '../application/wiki_controller.dart';
import 'changelog_dialog.dart';
import 'page_revisions_dialog.dart';
import 'chat/project_chat_panel.dart';
import 'frontmatter/frontmatter_dialog.dart';
import 'frontmatter/frontmatter_panel.dart';
import 'reader/block_to_markdown.dart';
import 'reader/outline_panel.dart';
import 'reader/wiki_reader_view.dart';
import 'research_dialog.dart';
import 'maintain_dialog.dart';
import 'selection_edit/selection_edit_overlay.dart';
import 'search/wiki_search_dialog.dart';

class WikiPage extends ConsumerStatefulWidget {
  const WikiPage({super.key});

  @override
  ConsumerState<WikiPage> createState() => _WikiPageState();
}

class _WikiPageState extends ConsumerState<WikiPage> {
  // The pageId we last applied. Tracking it lets a repeated build with
  // the same query param skip the controller call (it's a no-op anyway,
  // but preventing the call avoids spurious network refreshes).
  String? _appliedPageId;

  void _maybeApplyPageIdFromQuery(BuildContext context) {
    final pageId = GoRouterState.of(context).uri.queryParameters['pageId'];
    if (pageId == null || pageId.isEmpty) return;
    if (pageId == _appliedPageId) return;
    _appliedPageId = pageId;

    // Defer the async controller call out of the build phase. After the
    // page lands, strip ?pageId= from the URL so a hot-reload / back-
    // forward doesn't re-apply a possibly-stale id.
    WidgetsBinding.instance.addPostFrameCallback((_) async {
      final ok = await ref
          .read(wikiControllerProvider.notifier)
          .selectPageById(pageId);
      // context.mounted (not just `mounted`) is what the analyzer
      // tracks for BuildContext-bound async-gap safety.
      if (!context.mounted) return;
      if (!ok) {
        // Surface a brief snackbar so the user knows the deep link didn't
        // resolve (e.g. the page was already merged away).
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('找不到页面 ${pageId.substring(0, 8)}…')),
        );
      }
      // Clear the param. go() with a clean path triggers another build
      // but _appliedPageId still equals pageId so we're stable.
      context.go('/wiki');
    });
  }

  void _openSearch(BuildContext context, String projectId) {
    showWikiSearchDialog(context, projectId: projectId);
  }

  /// 手机形态：页面列表（桌面 280px 左栏）收进 bottom sheet 按需查看。
  /// 选中页面后 sheet 自动关闭；写法照 §2.4 既有 bottom sheet 范式。
  void _openPageListSheet(BuildContext context) {
    showModalBottomSheet<void>(
      context: context,
      isScrollControlled: true,
      showDragHandle: true,
      builder: (sheetCtx) => Consumer(
        builder: (ctx, ref, _) {
          // 听 controller 而不是捕获 build 时的 state —— sheet 打开期间
          // 新建页面/切项目能实时反映。
          final s = ref.watch(wikiControllerProvider).valueOrNull;
          if (s == null) return const SizedBox.shrink();
          return SizedBox(
            height: MediaQuery.sizeOf(ctx).height * 0.7,
            child: _LeftPane(
              state: s,
              onPageSelected: () => Navigator.of(ctx).pop(),
            ),
          );
        },
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    _maybeApplyPageIdFromQuery(context);
    final t = AppLocalizations.of(context)!;
    final state = ref.watch(wikiControllerProvider);
    final pending = ref.watch(pendingWriteCountProvider).valueOrNull ?? 0;
    final activeProject = state.valueOrNull?.activeProject;
    final activePage = state.valueOrNull?.activePage;
    final phone = isPhoneLayout(context);
    final scaffold = PageScaffold(
      title: t.wikiTitle,
      actions: [
        // 手机形态：页面列表入口（桌面是常驻 280px 左栏，手机上收进
        // bottom sheet）。放在最左，与抽屉/列表的移动导航习惯一致。
        if (activeProject != null && phone)
          IconButton(
            tooltip: '页面列表',
            visualDensity: VisualDensity.compact,
            icon: const Icon(Icons.format_list_bulleted, size: 16),
            onPressed: () => _openPageListSheet(context),
          ),
        if (activeProject != null)
          IconButton(
            tooltip: '搜索 (⌘K)',
            visualDensity: VisualDensity.compact,
            icon: const Icon(Icons.manage_search, size: 16),
            onPressed: () => _openSearch(context, activeProject.id),
          ),
        if (activeProject != null && activePage != null)
          IconButton(
            tooltip: '历史记录',
            visualDensity: VisualDensity.compact,
            icon: const Icon(Icons.history, size: 16),
            onPressed: () => showChangelogDialog(
              context,
              projectId: activeProject.id,
              pageId: activePage.id,
              pageTitle: activePage.title,
            ),
          ),
        if (activeProject != null && activePage != null)
          IconButton(
            tooltip: '版本历史',
            visualDensity: VisualDensity.compact,
            icon: const Icon(Icons.restore, size: 16),
            onPressed: () => PageRevisionsDialog.show(
              context,
              projectId: activeProject.id,
              pageId: activePage.id,
              pageTitle: activePage.title,
            ),
          ),
        if (activeProject != null)
          IconButton(
            tooltip: 'Deep Research',
            visualDensity: VisualDensity.compact,
            icon: const Icon(Icons.travel_explore, size: 16),
            onPressed: () async {
              final newPageId = await showResearchDialog(
                context,
                projectId: activeProject.id,
              );
              if (newPageId != null && context.mounted) {
                // Refresh + jump to the new page.
                await ref
                    .read(wikiControllerProvider.notifier)
                    .selectPageById(newPageId);
              }
            },
          ),
        if (activeProject != null)
          IconButton(
            tooltip: '维护 (agent)',
            visualDensity: VisualDensity.compact,
            icon: const Icon(Icons.auto_fix_high, size: 16),
            onPressed: () =>
                showMaintainDialog(context, projectId: activeProject.id),
          ),
        // 审阅队列入口 — dedup/lint/sweep workers 的产出落在这里。
        // wiki shell 子树内 /wiki/p/:pid/reviews 才是规范路径；旧的
        // /wiki/reviews 顶层路由已 redirect 到 /wiki 工作区列表（B6 收口）。
        // 没 active project 时禁用按钮（不再走顶层 ReviewsPage 老路径）。
        if (activeProject != null)
          IconButton(
            tooltip: '审阅队列',
            visualDensity: VisualDensity.compact,
            icon: const Icon(Icons.fact_check_outlined, size: 16),
            onPressed: () => context.go('/wiki/p/${activeProject.id}/reviews'),
          ),
        // /wiki/cleanup 老入口已删除 — dedup / lint 子页已替代其功能，从
        // NavRail 直接进入 /wiki/p/:pid/{dedup,lint}。这里不再保留按钮。
        if (pending > 0)
          Tooltip(
            message: '$pending pending write(s) waiting to upload',
            child: Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                Icon(
                  Icons.cloud_upload_outlined,
                  size: 14,
                  color: BiuTokens.textMuted,
                ),
                const SizedBox(width: 4),
                Text(
                  '$pending',
                  style: TextStyle(fontSize: 12, color: BiuTokens.textMuted),
                ),
                const SizedBox(width: BiuTokens.space2),
              ],
            ),
          ),
      ],
      child: state.when(
        loading: () => const Center(child: CircularProgressIndicator()),
        error: (e, _) => _ErrorView(message: e.toString()),
        data: (s) {
          if (s.noCredentials) {
            return const _NoCredsView();
          }
          if (s.projects.isEmpty) {
            return _EmptyProjectsView();
          }
          return _TwoPane(state: s);
        },
      ),
    );
    // ⌘K / Ctrl+K opens the in-project search. We register both
    // accelerators (meta for macOS, control for Linux/Windows) so the
    // muscle memory is the same as VSCode / IntelliJ / Notion.
    if (activeProject == null) return scaffold;
    return CallbackShortcuts(
      bindings: {
        const SingleActivator(LogicalKeyboardKey.keyK, meta: true): () =>
            _openSearch(context, activeProject.id),
        const SingleActivator(LogicalKeyboardKey.keyK, control: true): () =>
            _openSearch(context, activeProject.id),
      },
      child: Focus(autofocus: true, child: scaffold),
    );
  }
}

class _NoCredsView extends StatelessWidget {
  const _NoCredsView();
  @override
  Widget build(BuildContext c) {
    final t = AppLocalizations.of(c)!;
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(24),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            const Icon(Icons.cloud_off, size: 48),
            const SizedBox(height: 16),
            Text(t.wikiNoCreds),
            const SizedBox(height: 8),
            Text(t.memoryHintNoCreds, textAlign: TextAlign.center),
            const SizedBox(height: 16),
            FilledButton.icon(
              onPressed: () => Navigator.of(c).maybePop(),
              icon: const Icon(Icons.settings),
              label: Text(t.wikiOpenSettings),
            ),
          ],
        ),
      ),
    );
  }
}

class _ErrorView extends StatelessWidget {
  const _ErrorView({required this.message});
  final String message;
  @override
  Widget build(BuildContext c) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(24),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            const Icon(Icons.error_outline, size: 48),
            const SizedBox(height: 16),
            SelectableText(message, textAlign: TextAlign.center),
          ],
        ),
      ),
    );
  }
}

class _EmptyProjectsView extends ConsumerStatefulWidget {
  @override
  ConsumerState<_EmptyProjectsView> createState() => _EmptyProjectsViewState();
}

class _EmptyProjectsViewState extends ConsumerState<_EmptyProjectsView> {
  final _ctrl = TextEditingController();
  bool _busy = false;

  @override
  void dispose() {
    _ctrl.dispose();
    super.dispose();
  }

  Future<void> _create() async {
    final name = _ctrl.text.trim();
    if (name.isEmpty) return;
    setState(() => _busy = true);
    try {
      await ref.read(wikiControllerProvider.notifier).createProject(name);
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext c) {
    final t = AppLocalizations.of(c)!;
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(24),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            const Icon(Icons.folder_outlined, size: 48),
            const SizedBox(height: 16),
            Text(t.wikiNoProjects),
            const SizedBox(height: 16),
            SizedBox(
              // 手机形态撑满可用宽（320 固定宽在 <360px 屏上会溢出）。
              width: isPhoneLayout(c) ? double.infinity : 320,
              child: TextField(
                controller: _ctrl,
                decoration: const InputDecoration(
                  labelText: 'Project name',
                  border: OutlineInputBorder(),
                ),
                onSubmitted: (_) => _create(),
              ),
            ),
            const SizedBox(height: 12),
            FilledButton.icon(
              onPressed: _busy ? null : _create,
              icon: const Icon(Icons.add),
              label: Text(t.wikiCreateProject),
            ),
          ],
        ),
      ),
    );
  }
}

class _TwoPane extends ConsumerWidget {
  const _TwoPane({required this.state});
  final WikiState state;

  @override
  Widget build(BuildContext c, WidgetRef ref) {
    // 手机形态：280px 固定左栏 + 内容并排不可用 —— 内容全宽，页面列表
    // 由 header 的「页面列表」按钮以 bottom sheet 打开（信息不丢）。
    if (isPhoneLayout(c)) return _RightPane(state: state);
    return Row(
      children: [
        SizedBox(width: 280, child: _LeftPane(state: state)),
        const VerticalDivider(width: 1),
        Expanded(child: _RightPane(state: state)),
      ],
    );
  }
}

class _LeftPane extends ConsumerStatefulWidget {
  const _LeftPane({required this.state, this.onPageSelected});
  final WikiState state;

  /// 选中页面后的额外回调 —— 手机 bottom sheet 形态用来关 sheet；
  /// 桌面常驻左栏不传（行为不变）。
  final VoidCallback? onPageSelected;
  @override
  ConsumerState<_LeftPane> createState() => _LeftPaneState();
}

class _LeftPaneState extends ConsumerState<_LeftPane> {
  Future<void> _newPage() async {
    final t = AppLocalizations.of(context)!;
    final ctrl = TextEditingController();
    final name = await showDialog<String>(
      context: context,
      builder: (c) => AlertDialog(
        title: Text(t.wikiNewPageDialogTitle),
        content: TextField(
          controller: ctrl,
          autofocus: true,
          decoration: const InputDecoration(labelText: 'Title'),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(c),
            child: Text(t.commonCancel),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(c, ctrl.text.trim()),
            child: Text(t.commonCreate),
          ),
        ],
      ),
    );
    if (name == null || name.isEmpty) return;
    await ref.read(wikiControllerProvider.notifier).createPage(name);
  }

  /// Prompts for a project name and creates one. Reused by the
  /// switcher's "+ 新建项目" entry; kept inline so we don't have to
  /// thread the controller into a static helper.
  Future<void> _newProject() async {
    final t = AppLocalizations.of(context)!;
    final ctrl = TextEditingController();
    final name = await showDialog<String>(
      context: context,
      builder: (c) => AlertDialog(
        title: Text(t.wikiCreateProject),
        content: TextField(
          controller: ctrl,
          autofocus: true,
          decoration: const InputDecoration(labelText: 'Project name'),
          onSubmitted: (_) => Navigator.pop(c, ctrl.text.trim()),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(c),
            child: Text(t.commonCancel),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(c, ctrl.text.trim()),
            child: Text(t.commonCreate),
          ),
        ],
      ),
    );
    if (name == null || name.isEmpty) return;
    await ref.read(wikiControllerProvider.notifier).createProject(name);
  }

  @override
  Widget build(BuildContext c) {
    final s = widget.state;
    return Column(
      children: [
        _ProjectSwitcher(
          projects: s.projects,
          active: s.activeProject,
          onSelect: (p) =>
              ref.read(wikiControllerProvider.notifier).selectProject(p),
          onCreate: _newProject,
        ),
        const Divider(height: 1),
        Expanded(
          child: s.pages.isEmpty
              ? const _EmptyPageList()
              : ListView.builder(
                  itemCount: s.pages.length,
                  itemBuilder: (_, i) {
                    final p = s.pages[i];
                    final selected = p.id == s.activePage?.id;
                    // 包一层透明 Material：左栏容器是 ColoredBox 背景，
                    // ListTile 的选中底色/水波纹画在最近 Material 祖先上，
                    // 新版 Flutter 对此有断言（背景会被 ColoredBox 盖住）。
                    return Material(
                      type: MaterialType.transparency,
                      child: ListTile(
                        title: Text(p.title.isEmpty ? '(untitled)' : p.title),
                        trailing: p.pendingCreate
                            ? const Icon(Icons.cloud_upload_outlined, size: 16)
                            : null,
                        selected: selected,
                        onTap: () {
                          ref.read(wikiControllerProvider.notifier).selectPage(p);
                          // 手机 bottom sheet 形态选中后关 sheet（桌面为 null）。
                          widget.onPageSelected?.call();
                        },
                      ),
                    );
                  },
                ),
        ),
        const Divider(height: 1),
        Padding(
          padding: const EdgeInsets.all(8),
          child: SizedBox(
            width: double.infinity,
            child: FilledButton.icon(
              onPressed: _newPage,
              icon: const Icon(Icons.add),
              label: Text(AppLocalizations.of(context)!.wikiNewPageButton),
            ),
          ),
        ),
      ],
    );
  }
}

/// View mode for the active page — read-only markdown vs block editor.
/// Defaults to `read` for non-empty pages, `edit` for empty ones (so
/// brand-new pages start in a writable state).
enum _PageMode { read, edit }

class _RightPane extends ConsumerStatefulWidget {
  const _RightPane({required this.state});
  final WikiState state;

  @override
  ConsumerState<_RightPane> createState() => _RightPaneState();
}

class _RightPaneState extends ConsumerState<_RightPane> {
  /// User-overridden mode; null means "follow blocks emptiness default".
  _PageMode? _mode;

  /// Last seen page id — when it changes we drop the user's mode
  /// override so a freshly-opened page picks the auto default.
  String? _lastPageId;

  /// Map of blockId → key, used by editor mode to scroll-to-heading
  /// when the outline is tapped.
  final Map<String, GlobalKey> _blockKeys = {};

  // ─── §⑤ Milkdown body_md 编辑（PageEditorView）──────────────
  EditorBridgeController? _editorController;
  late final AutoSaveController _bodyAutosave;

  /// 最近拉到的 server body_md；controller（再）就绪时推入编辑器。
  String _bodyMd = '';

  /// 已拉 bodyMd 的 page id，避免每帧重 fetch。
  String? _bodyLoadedPageId;

  @override
  void initState() {
    super.initState();
    _bodyAutosave = AutoSaveController(saver: _saveBody)
      ..addListener(() {
        if (mounted) setState(() {});
      });
  }

  @override
  void dispose() {
    unawaited(_bodyAutosave.flush());
    _bodyAutosave.dispose();
    super.dispose();
  }

  /// PageEditorView controller 就绪回调：把已知 bodyMd 推入（首屏 + 切回 edit）。
  void _onController(EditorBridgeController c) {
    _editorController = c;
    c.onSelectionChange = _onSelectionChange;
    if (_bodyMd.isNotEmpty) unawaited(c.setDoc(_bodyMd));
  }

  /// S3 P1-6: 当前编辑器选区。非空（非 caret）→ 显示 selection-edit 浮层。
  EditorSelection? _selection;

  void _onSelectionChange(EditorSelection sel) {
    if (!mounted) return;
    final next = sel.empty ? null : sel;
    if (next == _selection) return;
    setState(() => _selection = next);
  }

  /// 首次进 edit 模式拉 server body_md（本地 Drift 不存）。同页只拉一次。
  void _ensureBody(String projectId, String pageId) {
    if (_bodyLoadedPageId == pageId) return;
    _bodyLoadedPageId = pageId;
    final repo = ref.read(wikiRepositoryProvider);
    if (repo == null) return;
    repo.getPage(projectId, pageId).then((p) {
      if (!mounted || p.bodyMd.isEmpty) return;
      _bodyMd = p.bodyMd;
      final c = _editorController;
      if (c != null) unawaited(c.setDoc(p.bodyMd));
    });
  }

  Future<AutoSaveOutcome> _saveBody(String md) async {
    final s = widget.state;
    final page = s.activePage;
    final projectId = s.activeProject?.id;
    final repo = ref.read(wikiRepositoryProvider);
    if (repo == null || page == null || projectId == null) {
      return const AutoSaveOutcome(
        status: AutoSaveStatus.error,
        errorMessage: '未连接 hub',
      );
    }
    try {
      await repo.updatePageBody(projectId, page.id, md, page.version);
      return const AutoSaveOutcome(status: AutoSaveStatus.saved);
    } on Exception catch (e) {
      return AutoSaveOutcome(status: AutoSaveStatus.error, errorMessage: '$e');
    }
  }

  /// §⑤ edit 模式正文：Milkdown WYSIWYG（body_md 权威）。首屏拉 server bodyMd
  /// 推入 controller；onMarkdownChanged → 防抖 → PUT body_md（server 重算 blocks
  /// 投影，syncws 推 block 变更，read 模式 reader 自动反映）。
  Widget _buildEditor(WikiState s, BuildContext c) {
    _ensureBody(s.activeProject!.id, s.activePage!.id);
    final theme = Theme.of(c).brightness == Brightness.dark
        ? BridgeTheme.dark
        : BridgeTheme.light;
    return Stack(
      clipBehavior: Clip.none,
      children: [
        PageEditorView(
          initialMarkdown: '',
          theme: theme,
          onMarkdownChanged: _bodyAutosave.schedule,
          controllerRef: _onController,
        ),
        // S3 P1-6 selection-edit follow overlay。coords 是 editor 视口相对
        // （PM coordsAtPos），Stack 同视口坐标系，直接用。empty 已在监听处过滤。
        if (_selection != null && _editorController != null)
          Positioned(
            left: _selection!.coords.left < 0 ? 0 : _selection!.coords.left,
            top: _selection!.coords.bottom + 8,
            child: SelectionEditOverlay(
              selection: _selection!,
              controller: _editorController!,
              projectId: s.activeProject!.id,
              pageId: s.activePage!.id,
            ),
          ),
      ],
    );
  }

  _PageMode _effectiveMode(WikiState s) {
    if (_mode != null) return _mode!;
    return s.blocks.isEmpty ? _PageMode.edit : _PageMode.read;
  }

  void _onOutlineTap(String blockId) {
    final key = _blockKeys[blockId];
    if (key?.currentContext == null) return;
    Scrollable.ensureVisible(
      key!.currentContext!,
      duration: const Duration(milliseconds: 250),
      alignment: 0.05,
    );
  }

  /// 手机形态：大纲（桌面右侧 220px OutlinePanel）以 bottom sheet 临时
  /// 查看；可点性与桌面一致 —— 仅编辑模式点击滚动定位，阅读模式纯查看。
  void _openOutlineSheet(List<WikiHeading> headings, _PageMode mode) {
    showModalBottomSheet<void>(
      context: context,
      showDragHandle: true,
      builder: (sheetCtx) => SafeArea(
        child: ListView.builder(
          shrinkWrap: true,
          itemCount: headings.length,
          itemBuilder: (_, i) {
            final h = headings[i];
            return ListTile(
              dense: true,
              contentPadding: EdgeInsets.fromLTRB(
                16.0 + (h.level - 1) * 12,
                0,
                16,
                0,
              ),
              title: Text(h.text, maxLines: 2, overflow: TextOverflow.ellipsis),
              onTap: mode == _PageMode.edit
                  ? () {
                      Navigator.of(sheetCtx).pop();
                      _onOutlineTap(h.blockId);
                    }
                  : null,
            );
          },
        ),
      ),
    );
  }

  @override
  Widget build(BuildContext c) {
    final s = widget.state;
    // No page selected → the project's Chat panel takes the canvas.
    // Mirrors llm_wiki: chat is the project's default workspace; reading
    // / editing a wiki page is a focused mode the user opts into by
    // picking a page from the left list.
    if (s.activePage == null) {
      final p = s.activeProject;
      if (p == null) {
        return Center(child: Text(AppLocalizations.of(c)!.wikiSelectPageHint));
      }
      return ProjectChatPanel(projectId: p.id, projectName: p.name);
    }
    if (s.activePage!.id != _lastPageId) {
      _lastPageId = s.activePage!.id;
      // 切页重置 bodyMd 缓存，新页首次 edit 重拉 server。
      _bodyLoadedPageId = null;
      _bodyMd = '';
      // Drop the per-page user override on navigation — but defer the
      // setState until after build to avoid setState-during-build.
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (!mounted) return;
        if (_mode != null) setState(() => _mode = null);
      });
    }
    final mode = _effectiveMode(s);
    final headings = extractHeadings(s.blocks);
    final phone = isPhoneLayout(c);

    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        // ── Header: breadcrumb + title + mode toggle ─────────
        Padding(
          padding: const EdgeInsets.fromLTRB(16, 12, 16, 0),
          child: _Breadcrumb(
            project: s.activeProject!.name,
            page: s.activePage!.title,
          ),
        ),
        Padding(
          padding: const EdgeInsets.fromLTRB(16, 4, 8, 0),
          child: Row(
            crossAxisAlignment: CrossAxisAlignment.center,
            children: [
              Expanded(
                child: Text(
                  s.activePage!.title.isEmpty ? '(未命名)' : s.activePage!.title,
                  style: Theme.of(c).textTheme.headlineSmall,
                ),
              ),
              // 手机形态：大纲栏不占常驻宽度，入口收进标题行按钮。
              if (phone && headings.length >= 2)
                IconButton(
                  tooltip: '大纲',
                  icon: const Icon(Icons.list_alt, size: 18),
                  onPressed: () => _openOutlineSheet(headings, mode),
                ),
              _ModeToggle(
                mode: mode,
                onChanged: (m) => setState(() => _mode = m),
              ),
            ],
          ),
        ),
        const SizedBox(height: 8),
        _FrontmatterStrip(
          projectId: s.activeProject!.id,
          pageId: s.activePage!.id,
        ),
        _RelatedRail(pageId: s.activePage!.id),
        _BacklinksRail(
          projectId: s.activeProject!.id,
          pageId: s.activePage!.id,
        ),
        const Divider(height: 16),
        // ── Body: reader / editor + optional outline rail ────
        Expanded(
          child: Row(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Expanded(
                child: mode == _PageMode.read
                    ? WikiReaderView(blocks: s.blocks)
                    : _buildEditor(s, c),
              ),
              // 手机形态：阅读区全宽，大纲走标题行按钮的 bottom sheet。
              if (!phone && headings.length >= 2)
                OutlinePanel(
                  headings: headings,
                  onTap: mode == _PageMode.edit ? _onOutlineTap : null,
                ),
            ],
          ),
        ),
        if (s.lastError != null)
          Container(
            color: Theme.of(c).colorScheme.errorContainer,
            padding: const EdgeInsets.all(8),
            child: Row(
              children: [
                Icon(
                  Icons.warning_amber_outlined,
                  color: Theme.of(c).colorScheme.error,
                  size: 16,
                ),
                const SizedBox(width: 6),
                Expanded(child: Text(s.lastError!)),
              ],
            ),
          ),
      ],
    );
  }
}

/// Project switcher card — replaces the old `Project` dropdown at the
/// top of the left pane. Shows the active project name with a chevron;
/// tapping pops a menu that lists every project (with ✓ on active) and
/// a divider + "+ 新建项目…" entry. Without this entry the user has no
/// way to create a second project once `_EmptyProjectsView` is past.
class _ProjectSwitcher extends StatelessWidget {
  const _ProjectSwitcher({
    required this.projects,
    required this.active,
    required this.onSelect,
    required this.onCreate,
  });

  final List<RepoProject> projects;
  final RepoProject? active;
  final ValueChanged<RepoProject> onSelect;
  final VoidCallback onCreate;

  static const String _kCreateSentinel = '__create__';

  Future<void> _open(BuildContext context) async {
    final box = context.findRenderObject() as RenderBox?;
    if (box == null) return;
    final overlay = Overlay.of(context).context.findRenderObject() as RenderBox;
    final origin = box.localToGlobal(Offset.zero, ancestor: overlay);
    final rect = origin & box.size;
    final selected = await showMenu<String>(
      context: context,
      position: RelativeRect.fromLTRB(
        rect.left + 4,
        rect.bottom + 2,
        overlay.size.width - rect.right - 4,
        overlay.size.height - rect.bottom,
      ),
      constraints: BoxConstraints(minWidth: rect.width, maxWidth: rect.width),
      items: [
        for (final p in projects)
          PopupMenuItem<String>(
            value: p.id,
            child: Row(
              children: [
                Icon(
                  p.id == active?.id ? Icons.check : Icons.folder_outlined,
                  size: 14,
                  color: p.id == active?.id
                      ? BiuTokens.purple
                      : BiuTokens.textMuted,
                ),
                const SizedBox(width: 8),
                Expanded(
                  child: Text(
                    p.name,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: TextStyle(
                      fontSize: 13,
                      fontWeight: p.id == active?.id
                          ? FontWeight.w600
                          : FontWeight.w400,
                    ),
                  ),
                ),
                if (p.pendingCreate)
                  Padding(
                    padding: EdgeInsets.only(left: 4),
                    child: Icon(
                      Icons.cloud_upload_outlined,
                      size: 12,
                      color: BiuTokens.textMuted,
                    ),
                  ),
              ],
            ),
          ),
        const PopupMenuDivider(),
        PopupMenuItem<String>(
          value: _kCreateSentinel,
          child: Row(
            children: [
              Icon(Icons.add, size: 14, color: BiuTokens.purple),
              const SizedBox(width: 8),
              Text(
                '新建项目…',
                style: TextStyle(
                  fontSize: 13,
                  color: BiuTokens.purple,
                  fontWeight: FontWeight.w600,
                ),
              ),
            ],
          ),
        ),
      ],
    );
    if (selected == null) return;
    if (selected == _kCreateSentinel) {
      onCreate();
      return;
    }
    final p = projects.where((x) => x.id == selected).firstOrNull;
    if (p != null) onSelect(p);
  }

  @override
  Widget build(BuildContext context) {
    final activeName = active?.name.isNotEmpty == true ? active!.name : '选择项目';
    return Padding(
      padding: const EdgeInsets.fromLTRB(8, 8, 8, 8),
      child: Material(
        color: BiuTokens.surfaceMuted,
        borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
        child: InkWell(
          borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
          onTap: () => _open(context),
          child: Padding(
            padding: const EdgeInsets.fromLTRB(10, 8, 8, 8),
            child: Row(
              children: [
                Icon(
                  Icons.folder_outlined,
                  size: 14,
                  color: BiuTokens.textMuted,
                ),
                const SizedBox(width: 8),
                Expanded(
                  child: Text(
                    activeName,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: TextStyle(
                      fontSize: 13,
                      fontWeight: FontWeight.w600,
                      color: BiuTokens.text,
                    ),
                  ),
                ),
                if (active?.pendingCreate == true)
                  Padding(
                    padding: EdgeInsets.only(right: 4),
                    child: Icon(
                      Icons.cloud_upload_outlined,
                      size: 12,
                      color: BiuTokens.textMuted,
                    ),
                  ),
                Icon(Icons.unfold_more, size: 14, color: BiuTokens.textMuted),
              ],
            ),
          ),
        ),
      ),
    );
  }
}

/// Empty-list state for the left-pane page list. Hints the user that
/// pages live alongside the chat — kept minimal: no big illustration,
/// no separate CTA (the "+ 新建页面" button below the list is already
/// the canonical entry point).
class _EmptyPageList extends StatelessWidget {
  const _EmptyPageList();
  @override
  Widget build(BuildContext context) {
    return Center(
      child: Padding(
        padding: EdgeInsets.symmetric(horizontal: 16),
        child: Text(
          '暂无页面\n聊一聊或点下方「新建页面」开始',
          textAlign: TextAlign.center,
          style: TextStyle(
            fontSize: 11,
            color: BiuTokens.textMuted,
            height: 1.5,
          ),
        ),
      ),
    );
  }
}

/// Lightweight breadcrumb row — `Project › Page`. Project is muted
/// and tappable (returns to project page list); page title is muted
/// too since it's repeated in the headline below.
class _Breadcrumb extends StatelessWidget {
  const _Breadcrumb({required this.project, required this.page});
  final String project;
  final String page;

  @override
  Widget build(BuildContext context) {
    return DefaultTextStyle(
      style: TextStyle(fontSize: 11, color: BiuTokens.textMuted, height: 1.2),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          Flexible(
            child: Text(project, maxLines: 1, overflow: TextOverflow.ellipsis),
          ),
          Padding(
            padding: EdgeInsets.symmetric(horizontal: 4),
            child: Icon(
              Icons.chevron_right,
              size: 12,
              color: BiuTokens.textMuted,
            ),
          ),
          Flexible(
            child: Text(
              page.isEmpty ? '(未命名)' : page,
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
            ),
          ),
        ],
      ),
    );
  }
}

class _ModeToggle extends StatelessWidget {
  const _ModeToggle({required this.mode, required this.onChanged});
  final _PageMode mode;
  final ValueChanged<_PageMode> onChanged;

  @override
  Widget build(BuildContext context) {
    return SegmentedButton<_PageMode>(
      segments: const [
        ButtonSegment(
          value: _PageMode.read,
          icon: Icon(Icons.menu_book_outlined, size: 14),
          label: Text('阅读', style: TextStyle(fontSize: 12)),
        ),
        ButtonSegment(
          value: _PageMode.edit,
          icon: Icon(Icons.edit_outlined, size: 14),
          label: Text('编辑', style: TextStyle(fontSize: 12)),
        ),
      ],
      selected: {mode},
      onSelectionChanged: (s) => onChanged(s.first),
      showSelectedIcon: false,
      style: ButtonStyle(
        visualDensity: VisualDensity.compact,
        tapTargetSize: MaterialTapTargetSize.shrinkWrap,
        padding: WidgetStateProperty.all(
          const EdgeInsets.symmetric(horizontal: 10, vertical: 4),
        ),
      ),
    );
  }
}

// ─── Related rail (P2-H-graph) ────────────────────────────────

/// Compact horizontal chip rail showing the top-K pages most related
/// to the active page (per `brain.page_relevance`). Renders nothing
/// when the API returns empty / errors / hasn't been populated yet —
/// we'd rather hide the rail than show an "empty" UI element on every
/// freshly-ingested project.
class _RelatedRail extends ConsumerWidget {
  const _RelatedRail({required this.pageId});
  final String pageId;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final async = ref.watch(relatedPagesProvider(pageId));
    return async.when(
      // Loading: show nothing — the editor is the focal point, the
      // rail is auxiliary. A spinner here would just shake the layout
      // every time the user clicks a different page.
      loading: () => const SizedBox.shrink(),
      // Errors: hide the rail; surface the underlying request via
      // logs (apiRequest already logs auth retries). The user gets
      // the wiki editor regardless.
      error: (_, _) => const SizedBox.shrink(),
      data: (related) {
        if (related.isEmpty) return const SizedBox.shrink();
        return Padding(
          padding: const EdgeInsets.fromLTRB(16, 8, 16, 0),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(
                '相关页面',
                style: TextStyle(
                  fontSize: 11,
                  fontWeight: FontWeight.w600,
                  color: BiuTokens.textMuted,
                  letterSpacing: 0.4,
                ),
              ),
              const SizedBox(height: 4),
              SizedBox(
                height: 32,
                child: ListView.separated(
                  scrollDirection: Axis.horizontal,
                  itemCount: related.length,
                  separatorBuilder: (_, _) => const SizedBox(width: 6),
                  itemBuilder: (_, i) => _RelatedChip(related: related[i]),
                ),
              ),
            ],
          ),
        );
      },
    );
  }
}

class _RelatedChip extends StatelessWidget {
  const _RelatedChip({required this.related});
  final RelatedPage related;

  // Pick a tooltip line per dominant signal so the user can tell
  // why a page surfaced. We display the strongest non-zero signal;
  // falling back to the raw score keeps the tooltip useful even
  // when the JSON lacks signals (older worker rows).
  String _tooltip() {
    String reason = '';
    double best = 0;
    for (final e in related.signals.entries) {
      if (e.value > best) {
        best = e.value;
        reason = e.key;
      }
    }
    final pretty = switch (reason) {
      'direct_link' => '直接 wikilink',
      'adamic_adar' => '共享邻居',
      'type_affinity' => '类型亲和',
      _ => 'score',
    };
    return '$pretty · ${related.score.toStringAsFixed(2)}';
  }

  @override
  Widget build(BuildContext context) {
    return Tooltip(
      message: _tooltip(),
      child: ActionChip(
        label: Text(
          related.title.isEmpty ? '(未命名)' : related.title,
          style: const TextStyle(fontSize: 12),
        ),
        visualDensity: VisualDensity.compact,
        materialTapTargetSize: MaterialTapTargetSize.shrinkWrap,
        onPressed: () => context.go('/wiki?pageId=${related.pageId}'),
      ),
    );
  }
}

/// Inbound `[[wikilink]]` references — "what other pages mention this
/// one?". Sits under the related rail. Hidden when empty / loading /
/// errored, same minimalist policy as the related rail.
class _BacklinksRail extends ConsumerWidget {
  const _BacklinksRail({required this.projectId, required this.pageId});
  final String projectId;
  final String pageId;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final async = ref.watch(
      backlinksFor((projectId: projectId, pageId: pageId)),
    );
    return async.when(
      loading: () => const SizedBox.shrink(),
      error: (_, _) => const SizedBox.shrink(),
      data: (links) {
        if (links.isEmpty) return const SizedBox.shrink();
        return Padding(
          padding: const EdgeInsets.fromLTRB(16, 8, 16, 0),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(
                '反向链接 · ${links.length}',
                style: TextStyle(
                  fontSize: 11,
                  fontWeight: FontWeight.w600,
                  color: BiuTokens.textMuted,
                  letterSpacing: 0.4,
                ),
              ),
              const SizedBox(height: 4),
              SizedBox(
                height: 56,
                child: ListView.separated(
                  scrollDirection: Axis.horizontal,
                  itemCount: links.length,
                  separatorBuilder: (_, _) => const SizedBox(width: 6),
                  itemBuilder: (_, i) => _BacklinkCard(link: links[i]),
                ),
              ),
            ],
          ),
        );
      },
    );
  }
}

class _BacklinkCard extends StatelessWidget {
  const _BacklinkCard({required this.link});
  final WikiBacklink link;

  @override
  Widget build(BuildContext context) {
    return Container(
      // 手机屏窄：卡片略收窄，让横滚 rail 露出更多下一张边缘（可滑动
      // 提示）；桌面固定 220 不变。
      width: isPhoneLayout(context) ? 200 : 220,
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
      decoration: BoxDecoration(
        color: BiuTokens.surfaceMuted,
        border: Border.all(color: BiuTokens.borderSubtle),
        borderRadius: BorderRadius.circular(6),
      ),
      child: InkWell(
        onTap: () => context.go('/wiki?pageId=${link.pageId}'),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          mainAxisSize: MainAxisSize.min,
          children: [
            Text(
              link.pageTitle.isEmpty ? '(未命名)' : link.pageTitle,
              style: TextStyle(
                fontSize: 11,
                fontWeight: FontWeight.w600,
                color: BiuTokens.text,
              ),
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
            ),
            const SizedBox(height: 2),
            Text(
              link.snippet,
              style: TextStyle(
                fontSize: 10,
                color: BiuTokens.textMuted,
                height: 1.3,
              ),
              maxLines: 2,
              overflow: TextOverflow.ellipsis,
            ),
          ],
        ),
      ),
    );
  }
}

/// Reads pages.frontmatter from the server (the local Drift cache
/// stores only title + version), shows the read-only panel, and
/// opens [FrontmatterDialog] on the pencil icon. After save, evicts
/// its own cached future so the next build refetches.
class _FrontmatterStrip extends ConsumerStatefulWidget {
  const _FrontmatterStrip({required this.projectId, required this.pageId});
  final String projectId;
  final String pageId;

  @override
  ConsumerState<_FrontmatterStrip> createState() => _FrontmatterStripState();
}

class _FrontmatterStripState extends ConsumerState<_FrontmatterStrip> {
  Future<api.WikiPage?>? _future;

  @override
  void initState() {
    super.initState();
    _future = _load();
  }

  @override
  void didUpdateWidget(covariant _FrontmatterStrip old) {
    super.didUpdateWidget(old);
    if (old.pageId != widget.pageId || old.projectId != widget.projectId) {
      setState(() => _future = _load());
    }
  }

  Future<api.WikiPage?> _load() async {
    final repo = ref.read(wikiRepositoryProvider);
    if (repo == null) return null;
    try {
      return await repo.client.getPage(widget.projectId, widget.pageId);
    } catch (_) {
      return null;
    }
  }

  Future<void> _onEdit(api.WikiPage page) async {
    // The editor needs title in the working map so the form can edit
    // it via the same widget; we strip it back out before PUT in
    // FrontmatterDialog._save.
    final initial = <String, dynamic>{'title': page.title, ...page.frontmatter};
    final saved = await showFrontmatterDialog(
      context,
      projectId: widget.projectId,
      pageId: widget.pageId,
      version: page.version,
      initial: initial,
    );
    if (saved && mounted) {
      setState(() => _future = _load());
      // Refresh the project's page list so wiki_controller picks up
      // the new title in the sidebar without a manual click.
      final repo = ref.read(wikiRepositoryProvider);
      if (repo != null) {
        await repo.refreshPages(widget.projectId);
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    return FutureBuilder<api.WikiPage?>(
      future: _future,
      builder: (_, snap) {
        // Loading / errored / no-creds: render nothing — the page
        // still works; the panel is auxiliary.
        final page = snap.data;
        if (page == null) return const SizedBox.shrink();
        return FrontmatterPanel(
          frontmatter: page.frontmatter,
          onEdit: () => _onEdit(page),
          onRelatedTap: (slug) {
            // Resolve a related entry as a wikilink target (page title).
            final pages =
                ref.read(wikiControllerProvider).valueOrNull?.pages ?? const [];
            for (final p in pages) {
              if (p.title.toLowerCase() == slug.toLowerCase()) {
                ref.read(wikiControllerProvider.notifier).selectPageById(p.id);
                return;
              }
            }
          },
        );
      },
    );
  }
}

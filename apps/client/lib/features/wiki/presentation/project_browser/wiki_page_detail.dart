/// 单页 detail 面板 —— ProjectBrowserPage 右栏的读/编辑主体。
///
/// 从旧 wiki_page.dart（死代码）的 `_RightPane` 抽取而来，一次性接回
/// 以下已有实现（原先全部不可达）：
///   * PageEditorView（Milkdown WYSIWYG）+ autosave → PUT body_md
///   * SelectionEditOverlay（划词改写，服务端 selection_edit 真实现）
///   * changelog / 版本历史 dialog（标题行工具条入口）
///   * frontmatter 查看/编辑条
///   * related / backlinks rail（跳转走 /wiki/p/:pid/pages/:pageId 深链）
///   * 大纲面板（桌面内联 220px / 手机 bottom sheet）
///   * maintain agent dialog + 页内搜索 dialog（标题行工具条入口）
///
/// 无选中页面时（项目默认工作区 = 对话）由 ProjectBrowserPage 自己决定
/// 渲染 ProjectChatPanel，本组件只在 activePage != null 时挂载。
library;

import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../../app/theme.dart';
import '../../../../core/editor/editor_bridge_controller.dart';
import '../../../../core/editor/editor_bridge_protocol.dart';
import '../../../../core/editor/editor_locale.dart';
import '../../../../core/editor/page_autosave.dart';
import '../../../../core/editor/page_editor_view.dart';
import '../../../../core/layout/form_factor.dart';
import '../../../../core/layout/phone_nav.dart';
import '../../../../core/platform/platform_caps.dart';
import '../../../../data/api/relevance_client.dart';
import '../../../../data/api/wiki_client.dart' show WikiBacklink;
import '../../../../data/api/wiki_client.dart' as api;
import '../../../../data/relevance_providers.dart';
import '../../../../data/wiki_providers.dart';
import '../../../chat/application/chat_preferences.dart';
import '../../application/wiki_controller.dart';
import '../changelog_dialog.dart';
import '../page_revisions_dialog.dart';
import '../maintain_dialog.dart';
import '../frontmatter/frontmatter_dialog.dart';
import '../frontmatter/frontmatter_panel.dart';
import '../reader/block_to_markdown.dart';
import '../reader/outline_panel.dart';
import '../reader/wiki_reader_view.dart';
import '../selection_edit/selection_edit_overlay.dart';
import '../search/wiki_search_dialog.dart';

/// View mode for the active page — read-only markdown vs block editor.
/// Defaults to `read` for non-empty pages, `edit` for empty ones (so
/// brand-new pages start in a writable state).
enum _PageMode { read, edit }

class WikiPageDetail extends ConsumerStatefulWidget {
  const WikiPageDetail({super.key, required this.state});

  /// 当前 wiki controller 状态；调用方保证 activePage / activeProject
  /// 均非 null（无选中页时 ProjectBrowserPage 渲染对话面板而非本组件）。
  final WikiState state;

  @override
  ConsumerState<WikiPageDetail> createState() => _WikiPageDetailState();
}

class _WikiPageDetailState extends ConsumerState<WikiPageDetail> {
  /// User-overridden mode; null means "follow blocks emptiness default".
  _PageMode? _mode;

  /// Last seen page id — when it changes we drop the user's mode
  /// override so a freshly-opened page picks the auto default.
  String? _lastPageId;

  /// Map of blockId → key, used by editor mode to scroll-to-heading
  /// when the outline is tapped.
  final Map<String, GlobalKey> _blockKeys = {};

  // ─── Milkdown body_md 编辑（PageEditorView）──────────────────
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

  /// edit 模式正文：Milkdown WYSIWYG（body_md 权威）。首屏拉 server bodyMd
  /// 推入 controller；onMarkdownChanged → 防抖 → PUT body_md（server 重算 blocks
  /// 投影，syncws 推 block 变更，read 模式 reader 自动反映）。
  Widget _buildEditor(WikiState s, BuildContext c) {
    _ensureBody(s.activeProject!.id, s.activePage!.id);
    final theme = Theme.of(c).brightness == Brightness.dark
        ? BridgeTheme.dark
        : BridgeTheme.light;
    // 编辑器 UI 语言跟随 App 内语言设置；右键菜单载体：移动端维持系统
    // callout，桌面/Web 用 bundle 自绘菜单。
    final editorLocale = resolveEditorLocale(
      ref.watch(chatPreferencesProvider.select((p) => p.localeOverride)),
    );
    final contextMenu =
        ref.watch(platformCapsProvider).isMobile ? 'native' : 'custom';
    return Stack(
      clipBehavior: Clip.none,
      children: [
        PageEditorView(
          initialMarkdown: '',
          theme: theme,
          locale: editorLocale,
          features: BridgeFeatures(contextMenu: contextMenu),
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

  // ─── 标题行工具条动作（搜索 / 历史 / 版本 / 维护）────────────

  void _onToolbarAction(_ToolbarAction action, WikiState s) {
    final projectId = s.activeProject!.id;
    final page = s.activePage!;
    switch (action) {
      case _ToolbarAction.search:
        showWikiSearchDialog(context, projectId: projectId);
      case _ToolbarAction.changelog:
        showChangelogDialog(
          context,
          projectId: projectId,
          pageId: page.id,
          pageTitle: page.title,
        );
      case _ToolbarAction.revisions:
        PageRevisionsDialog.show(
          context,
          projectId: projectId,
          pageId: page.id,
          pageTitle: page.title,
        );
      case _ToolbarAction.maintain:
        showMaintainDialog(context, projectId: projectId);
    }
  }

  @override
  Widget build(BuildContext c) {
    final s = widget.state;
    if (s.activePage == null || s.activeProject == null) {
      return const SizedBox.shrink();
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
        // ── Header: title + toolbar + mode toggle ─────────────
        Padding(
          padding: const EdgeInsets.fromLTRB(16, 12, 8, 0),
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
              // 工具条：桌面四个紧凑图标平铺；手机收进 ⋮ popup 避免
              // 与读/编切换挤占标题宽度（§4.6 移动适配范式）。
              if (phone)
                PopupMenuButton<_ToolbarAction>(
                  tooltip: '页面操作',
                  icon: const Icon(Icons.more_vert, size: 18),
                  onSelected: (a) => _onToolbarAction(a, s),
                  itemBuilder: (_) => [
                    for (final a in _ToolbarAction.values)
                      PopupMenuItem(
                        value: a,
                        child: ListTile(
                          leading: Icon(a.icon, size: 18),
                          title: Text(a.label),
                          dense: true,
                          contentPadding: EdgeInsets.zero,
                        ),
                      ),
                  ],
                )
              else ...[
                for (final a in _ToolbarAction.values)
                  IconButton(
                    tooltip: a.label,
                    visualDensity: VisualDensity.compact,
                    icon: Icon(a.icon, size: 16),
                    onPressed: () => _onToolbarAction(a, s),
                  ),
              ],
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
        _RelatedRail(
          projectId: s.activeProject!.id,
          pageId: s.activePage!.id,
        ),
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

/// 标题行工具条动作 —— 图标 / 文案单处定义，桌面平铺与手机 popup 共用。
enum _ToolbarAction {
  search(Icons.manage_search, '页内搜索'),
  changelog(Icons.history, '历史记录'),
  revisions(Icons.restore, '版本历史'),
  maintain(Icons.auto_fix_high, '维护 (agent)');

  const _ToolbarAction(this.icon, this.label);
  final IconData icon;
  final String label;
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
  const _RelatedRail({required this.projectId, required this.pageId});
  final String projectId;
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
                  itemBuilder: (_, i) => _RelatedChip(
                    related: related[i],
                    projectId: projectId,
                  ),
                ),
              ),
            ],
          ),
        );
      },
    );
  }
}

class _RelatedChip extends ConsumerWidget {
  const _RelatedChip({required this.related, required this.projectId});
  final RelatedPage related;
  final String projectId;

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
  Widget build(BuildContext context, WidgetRef ref) {
    return Tooltip(
      message: _tooltip(),
      child: ActionChip(
        label: Text(
          related.title.isEmpty ? '(未命名)' : related.title,
          style: const TextStyle(fontSize: 12),
        ),
        visualDensity: VisualDensity.compact,
        materialTapTargetSize: MaterialTapTargetSize.shrinkWrap,
        // 规范深链 /wiki/p/:pid/pages/:pageId（旧 /wiki?pageId= 已随
        // wiki_page.dart 退役）；selectPageById 与 _PageRow 的
        // enterSubPage + selectPage 同序。
        onPressed: () {
          enterSubPage(
            context,
            '/wiki/p/$projectId/pages/${related.pageId}',
          );
          unawaited(
            ref
                .read(wikiControllerProvider.notifier)
                .selectPageById(related.pageId),
          );
        },
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
                  itemBuilder: (_, i) => _BacklinkCard(
                    link: links[i],
                    projectId: projectId,
                  ),
                ),
              ),
            ],
          ),
        );
      },
    );
  }
}

class _BacklinkCard extends ConsumerWidget {
  const _BacklinkCard({required this.link, required this.projectId});
  final WikiBacklink link;
  final String projectId;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
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
        onTap: () {
          enterSubPage(context, '/wiki/p/$projectId/pages/${link.pageId}');
          unawaited(
            ref
                .read(wikiControllerProvider.notifier)
                .selectPageById(link.pageId),
          );
        },
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

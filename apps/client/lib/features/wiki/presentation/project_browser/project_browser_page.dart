/// Per-project page browser —— knowcode 风格 master/detail 双栏。
///
///   ┌────────────────┬───────────────────────────┐
///   │ 项目名 + 搜索   │                          │
///   ├────────────────┤  右栏: WikiPageDetail     │
///   │ 过滤框         │  （读/编切换 + 工具条）    │
///   ├────────────────┤                          │
///   │ ▼ 概览  (2)    │                          │
///   │   Wiki 总览    │                          │
///   │   Wiki 索引    │                          │
///   │ ▼ 实体  (9)    │                          │
///   │   Anthropic    │                          │
///   │   ...          │                          │
///   └────────────────┴───────────────────────────┘
///
/// 手机形态 (<600px)：320px master 收进 bottom sheet（顶行列表按钮打开，
/// 选中自动关），detail 全宽 —— 与 WikiShell 同一套范式
/// (docs/BiuMind-Mobile-Adaptation-Plan.md §4.6)。
///
/// Master 部分采用分组 page list（按 frontmatter.type 聚合）+ 展开/收起
/// 状态持久化到 SharedPreferences。Detail 部分接 [WikiPageDetail]：默认
/// WikiReaderView 只读，标题行切「编辑」进 PageEditorView（Milkdown
/// WYSIWYG + autosave → PUT body_md），并挂 selection-edit / 版本历史 /
/// frontmatter / related / 大纲等全套已有能力。无选中页面时 detail 落
/// ProjectChatPanel（项目默认工作区 = 对话）。
library;

import 'dart:async';
import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:shared_preferences/shared_preferences.dart';

import '../../../../app/theme.dart';
import '../../../../core/layout/form_factor.dart';
import '../../../../core/layout/phone_nav.dart';
import '../../../../data/wiki_providers.dart' show wikiRepositoryProvider;
import '../../../../data/wiki_repository.dart' show RepoPage;
import '../../application/sync_provider.dart' show wikiSyncEventsProvider;
import '../../application/wiki_controller.dart';
import '../chat/project_chat_panel.dart';
import '../pages/page_type_config.dart';
import '../pages/pages_providers.dart';
import '../sync/page_banners.dart';
import 'wiki_page_detail.dart';

class ProjectBrowserPage extends ConsumerStatefulWidget {
  const ProjectBrowserPage({
    super.key,
    required this.projectId,
    this.pageId,
  });

  final String projectId;
  final String? pageId;

  @override
  ConsumerState<ProjectBrowserPage> createState() =>
      _ProjectBrowserPageState();
}

class _ProjectBrowserPageState extends ConsumerState<ProjectBrowserPage> {
  String? _appliedProjectId;
  String? _appliedPageId;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) => _sync());
  }

  @override
  void didUpdateWidget(covariant ProjectBrowserPage old) {
    super.didUpdateWidget(old);
    if (old.projectId != widget.projectId || old.pageId != widget.pageId) {
      WidgetsBinding.instance.addPostFrameCallback((_) => _sync());
    }
  }

  /// 把 URL 上的 projectId / pageId 同步到 wiki_controller 状态。
  /// Phase 0 已实现的逻辑保持不变。
  Future<void> _sync() async {
    if (!mounted) return;
    final pid = widget.projectId;
    if (pid.isEmpty) return;

    final controller = ref.read(wikiControllerProvider.notifier);
    final state = ref.read(wikiControllerProvider).valueOrNull;
    if (state == null) return;

    if (_appliedProjectId != pid && state.activeProject?.id != pid) {
      final proj = state.projects.where((p) => p.id == pid).firstOrNull;
      if (proj != null) {
        await controller.selectProject(proj);
      }
    }
    _appliedProjectId = pid;

    final wantedPageId = widget.pageId;
    if (wantedPageId != null &&
        wantedPageId.isNotEmpty &&
        wantedPageId != _appliedPageId) {
      _appliedPageId = wantedPageId;
      await controller.selectPageById(wantedPageId);
    }
  }

  /// 收到 brain.events live frame 时按 entity 派发 refresh：
  ///   - entity == "page"  → repo.refreshPages(projectId)
  ///   - entity == "block" → repo.refreshBlocks(projectId, activePageId)
  ///   - 其他 entity（ingest_task / research_task / lint_run / ...）
  ///     由 activity_provider / ingest_stream_controller 各自消费，
  ///     这里不重复 refresh。
  ///
  /// 仅匹配当前 widget.projectId 的事件 — wikiSyncEventsProvider 已经
  /// 是 family，所以本来就只会拿到这个项目的 events。但跨项目幽灵事件
  /// 防御一下成本极低，同时可读性更高。
  Future<void> _onSyncEvent(Map<Object?, Object?> event) async {
    final entity = event['entity'];
    if (entity != 'page' && entity != 'block') return;
    final repo = ref.read(wikiRepositoryProvider);
    if (repo == null) return;
    final pid = widget.projectId;
    if (pid.isEmpty) return;
    try {
      if (entity == 'page') {
        await repo.refreshPages(pid);
      } else {
        // block 事件：拉取当前 active page 的 blocks（如有）。
        final state = ref.read(wikiControllerProvider).valueOrNull;
        final activePageId = state?.activePage?.id;
        if (activePageId != null && activePageId.isNotEmpty) {
          await repo.refreshBlocks(pid, activePageId);
        }
      }
    } on Exception {
      // 实时刷新失败不致命 — UI 还有手动刷新路径（重选项目 / 切页）。
    }
  }

  /// 手机形态：master（桌面 320px 左栏）收进 bottom sheet 按需查看，
  /// 选中页面后自动关 sheet（§4.6 bottom sheet 范式）。
  void _openMasterSheet() {
    showModalBottomSheet<void>(
      context: context,
      isScrollControlled: true,
      showDragHandle: true,
      builder: (sheetCtx) => Consumer(
        builder: (ctx, ref, _) {
          // 在 Consumer 内 watch —— sheet 打开期间 pages / activePage 变化
          // 能实时反映（直接捕获外层 build 的局部变量只是打开时的快照）。
          final pages = ref.watch(pagesListProvider(widget.projectId));
          final state = ref.watch(wikiControllerProvider).valueOrNull;
          return SizedBox(
            height: MediaQuery.sizeOf(ctx).height * 0.7,
            child: _MasterColumn(
              projectName: state?.activeProject?.name ?? '加载中…',
              projectId: widget.projectId,
              pages: pages,
              selectedPageId: widget.pageId ?? state?.activePage?.id,
              onSearch: () =>
                  enterSubPage(context, '/wiki/p/${widget.projectId}/search'),
              onPageSelected: () => Navigator.of(ctx).pop(),
            ),
          );
        },
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    // 监听项目级实时事件流：page/block 变更时让仓库重新拉一次，
    // 让左侧 page list / 右侧 reader blocks 跟着 brain.events 实时刷新。
    // ref.listen 内部按 (provider, listener) 去重，每次 build 调用安全。
    ref.listen<AsyncValue<Map<Object?, Object?>>>(
      wikiSyncEventsProvider(widget.projectId),
      (_, next) {
        next.whenData((event) => _onSyncEvent(event));
      },
    );

    final pages = ref.watch(pagesListProvider(widget.projectId));
    final state = ref.watch(wikiControllerProvider);
    final s = state.valueOrNull;
    final activeProject = s?.activeProject;
    final phone = isPhoneLayout(context);

    final projectName = activeProject?.name ?? '加载中…';
    void openSearch() => enterSubPage(context, '/wiki/p/${widget.projectId}/search');

    // 无选中页面时 detail 落 ProjectChatPanel（项目默认工作区 = 对话，
    // 与旧 wiki_page 行为一致）；controller 还没 load 完时给占位。
    final Widget detail;
    if (s?.activePage != null) {
      detail = _PageDetail(state: s!, projectId: widget.projectId);
    } else if (activeProject != null) {
      detail = ProjectChatPanel(
        projectId: activeProject.id,
        projectName: activeProject.name,
      );
    } else {
      detail = const _DetailPlaceholder();
    }

    // 手机形态：320px master 不常驻 —— 顶行留项目名 + 搜索 + 页面列表
    // 入口（开 bottom sheet），detail 全宽；桌面 master/detail 双栏原样。
    if (phone) {
      return Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: <Widget>[
          _ContextHeader(
            projectName: projectName,
            onSearch: openSearch,
            onOpenPages: _openMasterSheet,
          ),
          Divider(height: 1, color: BiuTokens.borderSubtle),
          Expanded(child: Container(color: BiuTokens.bg, child: detail)),
        ],
      );
    }

    return Row(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: <Widget>[
        SizedBox(
          width: 320,
          child: _MasterColumn(
            projectName: projectName,
            projectId: widget.projectId,
            pages: pages,
            selectedPageId: widget.pageId ?? s?.activePage?.id,
            onSearch: openSearch,
          ),
        ),
        Container(width: 1, color: BiuTokens.borderSubtle),
        Expanded(child: Container(color: BiuTokens.bg, child: detail)),
      ],
    );
  }
}

/// master 栏主体 —— 项目名头 + 过滤框 + 分组页面树。
/// 桌面：外层套 320px SizedBox 常驻；手机：收进 bottom sheet。
/// 抽成独立 widget 并让过滤词自持（它只被 _PageList 消费），sheet
/// 形态下输入过滤不依赖外层页面的 setState。
class _MasterColumn extends StatefulWidget {
  const _MasterColumn({
    required this.projectName,
    required this.projectId,
    required this.pages,
    required this.selectedPageId,
    required this.onSearch,
    this.onPageSelected,
  });

  final String projectName;
  final String projectId;
  final List<RepoPage> pages;
  final String? selectedPageId;
  final VoidCallback onSearch;

  /// 选中页面后的额外回调 —— 手机 bottom sheet 形态用来关 sheet；
  /// 桌面常驻左栏不传（行为不变）。
  final VoidCallback? onPageSelected;

  @override
  State<_MasterColumn> createState() => _MasterColumnState();
}

class _MasterColumnState extends State<_MasterColumn> {
  String _query = '';

  @override
  Widget build(BuildContext context) {
    return Container(
      color: BiuTokens.surface,
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: <Widget>[
          _ContextHeader(
            projectName: widget.projectName,
            onSearch: widget.onSearch,
          ),
          Divider(height: 1, color: BiuTokens.borderSubtle),
          _SearchBox(onChanged: (v) => setState(() => _query = v)),
          Divider(height: 1, color: BiuTokens.borderSubtle),
          Expanded(
            child: _PageList(
              projectId: widget.projectId,
              selectedPageId: widget.selectedPageId,
              pages: widget.pages,
              query: _query,
              onPageSelected: widget.onPageSelected,
            ),
          ),
        ],
      ),
    );
  }
}

class _ContextHeader extends StatelessWidget {
  const _ContextHeader({
    required this.projectName,
    required this.onSearch,
    this.onOpenPages,
  });
  final String projectName;
  final VoidCallback onSearch;

  /// 手机形态：非空时在最左 prepend 页面列表入口（开 bottom sheet）；
  /// 桌面 master 头不传（渲染与原来一致）。
  final VoidCallback? onOpenPages;

  @override
  Widget build(BuildContext context) {
    return Container(
      height: 44,
      padding: const EdgeInsets.symmetric(horizontal: 12),
      child: Row(
        children: <Widget>[
          // 子页头左位 ← (手机形态; 桌面 shrink 不占位, §3.3)。
          const PhoneBackButton(),
          if (onOpenPages != null) ...<Widget>[
            IconButton(
              tooltip: '页面列表',
              onPressed: onOpenPages,
              icon: const Icon(Icons.format_list_bulleted, size: 16),
              padding: EdgeInsets.zero,
              // 手机专属按钮 —— 触摸目标给到 40 (同行的桌面搜索按钮
              // 28 不改, 避免桌面回归)。
              constraints: const BoxConstraints(minWidth: 40, minHeight: 40),
            ),
            const SizedBox(width: 4),
          ],
          Icon(Icons.book_outlined, size: 14, color: BiuTokens.textSecondary),
          const SizedBox(width: 8),
          Expanded(
            child: Text(
              projectName,
              style: TextStyle(
                color: BiuTokens.text,
                fontSize: 13,
                fontWeight: FontWeight.w600,
              ),
              overflow: TextOverflow.ellipsis,
            ),
          ),
          IconButton(
            tooltip: '搜索 (⌘P)',
            onPressed: onSearch,
            icon: const Icon(Icons.search, size: 16),
            padding: EdgeInsets.zero,
            constraints: const BoxConstraints(minWidth: 28, minHeight: 28),
          ),
        ],
      ),
    );
  }
}

class _SearchBox extends StatelessWidget {
  const _SearchBox({required this.onChanged});
  final ValueChanged<String> onChanged;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
      child: SizedBox(
        height: 26,
        child: TextField(
          onChanged: onChanged,
          style: TextStyle(fontSize: 12, color: BiuTokens.text),
          decoration: InputDecoration(
            isDense: true,
            prefixIcon: Icon(
              Icons.filter_list,
              size: 13,
              color: BiuTokens.textMuted,
            ),
            prefixIconConstraints:
                const BoxConstraints(minWidth: 28, minHeight: 0),
            hintText: '过滤页面…',
            hintStyle:
                TextStyle(fontSize: 12, color: BiuTokens.textMuted),
            contentPadding: EdgeInsets.zero,
          ),
        ),
      ),
    );
  }
}

class _PageList extends StatefulWidget {
  const _PageList({
    required this.projectId,
    required this.selectedPageId,
    required this.pages,
    required this.query,
    this.onPageSelected,
  });
  final String projectId;
  final String? selectedPageId;
  final List<RepoPage> pages;
  final String query;

  /// 手机 bottom sheet 形态：选中页面后关 sheet；桌面常驻左栏不传。
  final VoidCallback? onPageSelected;

  @override
  State<_PageList> createState() => _PageListState();
}

class _PageListState extends State<_PageList> {
  /// null until SharedPreferences 异步读取完成。期间用 default 渲染避免空白。
  Set<String>? _expanded;

  String get _prefsKey => 'wiki_pages_tree_expanded:${widget.projectId}';

  @override
  void initState() {
    super.initState();
    _loadExpanded();
  }

  @override
  void didUpdateWidget(covariant _PageList oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.projectId != widget.projectId) {
      _expanded = null;
      _loadExpanded();
    }
  }

  Future<void> _loadExpanded() async {
    final prefs = await SharedPreferences.getInstance();
    final raw = prefs.getString(_prefsKey);
    if (!mounted) return;
    if (raw == null) {
      setState(
        () => _expanded = Set<String>.from(kDefaultExpandedPageTypes),
      );
      return;
    }
    try {
      final decoded = jsonDecode(raw);
      if (decoded is List) {
        setState(() => _expanded = decoded.whereType<String>().toSet());
        return;
      }
    } catch (_) {/* fall through */}
    setState(
      () => _expanded = Set<String>.from(kDefaultExpandedPageTypes),
    );
  }

  Future<void> _persistExpanded() async {
    final set = _expanded;
    if (set == null) return;
    final prefs = await SharedPreferences.getInstance();
    await prefs.setString(_prefsKey, jsonEncode(set.toList()));
  }

  void _toggle(String type) {
    setState(() {
      final set = _expanded ?? Set<String>.from(kDefaultExpandedPageTypes);
      if (set.contains(type)) {
        set.remove(type);
      } else {
        set.add(type);
      }
      _expanded = set;
    });
    unawaited(_persistExpanded());
  }

  /// 从 RepoPage.frontmatter 取 type 字段；缺失归 'other' 桶。
  /// frontmatter 由 WikiRepository 内存 cache 在 listPages 时回填，
  /// 重启后首次 listPages 之前会全部归 'other'。
  String _typeOf(RepoPage p) {
    final t = p.frontmatter['type'];
    if (t is String && t.isNotEmpty) return t.toLowerCase();
    return 'other';
  }

  @override
  Widget build(BuildContext context) {
    final pages = widget.pages;
    if (pages.isEmpty) {
      return const _EmptyPages();
    }

    final q = widget.query.trim().toLowerCase();
    final visible = q.isEmpty
        ? pages
        : pages.where((p) {
            final title = p.title.toLowerCase();
            final path =
                (p.frontmatter['path'] as String?)?.toLowerCase() ?? '';
            return title.contains(q) || path.contains(q);
          }).toList(growable: false);

    if (visible.isEmpty) {
      return Center(
        child: Padding(
          padding: const EdgeInsets.all(16),
          child: Text(
            '没有匹配 "${widget.query.trim()}" 的页面',
            style: TextStyle(color: BiuTokens.textMuted, fontSize: 12),
            textAlign: TextAlign.center,
          ),
        ),
      );
    }

    final grouped = <String, List<RepoPage>>{};
    for (final p in visible) {
      grouped.putIfAbsent(_typeOf(p), () => []).add(p);
    }
    for (final group in grouped.values) {
      group.sort((a, b) => a.title.compareTo(b.title));
    }

    final groupKeys = grouped.keys.toList()
      ..sort((a, b) {
        final oa = pageTypeConfigOf(a).order;
        final ob = pageTypeConfigOf(b).order;
        if (oa != ob) return oa.compareTo(ob);
        return a.compareTo(b);
      });

    final searching = q.isNotEmpty;
    final expanded = _expanded ?? Set<String>.from(kDefaultExpandedPageTypes);

    final entries = <_ListEntry>[];
    for (final key in groupKeys) {
      final isExpanded = searching || expanded.contains(key);
      entries.add(_HeaderEntry(
        type: key,
        count: grouped[key]!.length,
        expanded: isExpanded,
      ));
      if (isExpanded) {
        for (final p in grouped[key]!) {
          entries.add(_PageEntry(page: p));
        }
      }
    }

    return ListView.builder(
      padding: const EdgeInsets.symmetric(vertical: 8),
      itemCount: entries.length,
      itemBuilder: (context, i) {
        final entry = entries[i];
        if (entry is _HeaderEntry) {
          return _GroupHeader(
            type: entry.type,
            count: entry.count,
            expanded: entry.expanded,
            onTap: searching ? null : () => _toggle(entry.type),
          );
        }
        final p = (entry as _PageEntry).page;
        final selected = widget.selectedPageId == p.id;
        return _PageRow(
          projectId: widget.projectId,
          page: p,
          selected: selected,
          onPageSelected: widget.onPageSelected,
        );
      },
    );
  }
}

class _GroupHeader extends StatelessWidget {
  const _GroupHeader({
    required this.type,
    required this.count,
    required this.expanded,
    required this.onTap,
  });
  final String type;
  final int count;
  final bool expanded;
  final VoidCallback? onTap;

  @override
  Widget build(BuildContext context) {
    final cfg = pageTypeConfigOf(type);
    return InkWell(
      onTap: onTap,
      child: Padding(
        padding: const EdgeInsets.fromLTRB(12, 8, 12, 8),
        child: Row(
          children: <Widget>[
            Icon(
              expanded ? Icons.expand_more : Icons.chevron_right,
              size: 14,
              color: BiuTokens.textMuted,
            ),
            const SizedBox(width: 4),
            Icon(cfg.icon, size: 13, color: cfg.color),
            const SizedBox(width: 8),
            Expanded(
              child: Text(
                cfg.label,
                style: TextStyle(
                  color: BiuTokens.text,
                  fontSize: 12,
                  fontWeight: FontWeight.w600,
                ),
              ),
            ),
            Text(
              '$count',
              style:
                  TextStyle(color: BiuTokens.textMuted, fontSize: 11),
            ),
          ],
        ),
      ),
    );
  }
}

class _PageRow extends ConsumerStatefulWidget {
  const _PageRow({
    required this.projectId,
    required this.page,
    required this.selected,
    this.onPageSelected,
  });
  final String projectId;
  final RepoPage page;
  final bool selected;

  /// 手机 bottom sheet 形态：选中后关 sheet；桌面为 null。
  final VoidCallback? onPageSelected;

  @override
  ConsumerState<_PageRow> createState() => _PageRowState();
}

class _PageRowState extends ConsumerState<_PageRow> {
  bool _hover = false;

  @override
  Widget build(BuildContext context) {
    final p = widget.page;
    final selected = widget.selected;
    final bg = selected
        ? SemanticTokens.successSoft
        : (_hover ? BiuTokens.surfaceMuted : Colors.transparent);
    final relPath = (p.frontmatter['path'] as String?) ?? '';
    return MouseRegion(
      onEnter: (_) => setState(() => _hover = true),
      onExit: (_) => setState(() => _hover = false),
      cursor: SystemMouseCursors.click,
      child: GestureDetector(
        onTap: () {
          // 手机 bottom sheet 形态：先关 sheet 再跳转（与 _NavTile 同序；
          // 桌面该回调为 null，顺序与原来一致）。
          widget.onPageSelected?.call();
          enterSubPage(
              context, '/wiki/p/${widget.projectId}/pages/${p.id}');
          ref.read(wikiControllerProvider.notifier).selectPage(p);
        },
        child: Container(
          margin: const EdgeInsets.symmetric(horizontal: 8, vertical: 1),
          padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 8),
          decoration: BoxDecoration(
            color: bg,
            borderRadius: BorderRadius.circular(7),
          ),
          child: Row(
            children: <Widget>[
              Icon(
                Icons.description_outlined,
                size: 13,
                color: selected
                    ? Theme.of(context).colorScheme.primary
                    : BiuTokens.textSecondary,
              ),
              const SizedBox(width: 8),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: <Widget>[
                    Text(
                      p.title.isEmpty ? '(未命名)' : p.title,
                      style: TextStyle(
                        color: BiuTokens.text,
                        fontSize: 13,
                        fontWeight:
                            selected ? FontWeight.w600 : FontWeight.w400,
                      ),
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                    ),
                    if (relPath.isNotEmpty && relPath != p.title) ...<Widget>[
                      const SizedBox(height: 1),
                      Text(
                        relPath,
                        style: TextStyle(
                          color: BiuTokens.textMuted,
                          fontSize: 11,
                        ),
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                      ),
                    ],
                  ],
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

class _EmptyPages extends StatelessWidget {
  const _EmptyPages();

  @override
  Widget build(BuildContext context) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(24),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: <Widget>[
            Container(
              width: 40,
              height: 40,
              decoration: BoxDecoration(
                color: BiuTokens.surfaceMuted,
                borderRadius: BorderRadius.circular(10),
                border: Border.all(color: BiuTokens.borderSubtle),
              ),
              alignment: Alignment.center,
              child: Icon(
                Icons.description_outlined,
                size: 18,
                color: BiuTokens.textSecondary,
              ),
            ),
            const SizedBox(height: 12),
            Text(
              '暂无页面',
              style: TextStyle(
                color: BiuTokens.text,
                fontSize: 13,
                fontWeight: FontWeight.w600,
              ),
            ),
            const SizedBox(height: 4),
            Text(
              '上传源文件或新建页面开始构建知识库',
              textAlign: TextAlign.center,
              style: TextStyle(color: BiuTokens.textMuted, fontSize: 12),
            ),
          ],
        ),
      ),
    );
  }
}

sealed class _ListEntry {}

class _HeaderEntry extends _ListEntry {
  _HeaderEntry({
    required this.type,
    required this.count,
    required this.expanded,
  });
  final String type;
  final int count;
  final bool expanded;
}

class _PageEntry extends _ListEntry {
  _PageEntry({required this.page});
  final RepoPage page;
}

class _DetailPlaceholder extends StatelessWidget {
  const _DetailPlaceholder();

  @override
  Widget build(BuildContext context) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(24),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: <Widget>[
            Container(
              width: 56,
              height: 56,
              decoration: BoxDecoration(
                color: BiuTokens.surfaceMuted,
                borderRadius: BorderRadius.circular(14),
                border: Border.all(color: BiuTokens.borderSubtle),
              ),
              alignment: Alignment.center,
              child: Icon(
                Icons.menu_book_outlined,
                size: 24,
                color: BiuTokens.textSecondary,
              ),
            ),
            const SizedBox(height: 16),
            Text(
              '选择一个页面',
              style: TextStyle(
                color: BiuTokens.text,
                fontSize: 16,
                fontWeight: FontWeight.w600,
              ),
            ),
            const SizedBox(height: 6),
            Text(
              // 手机没有左侧栏 —— 页面列表收在顶行按钮的 bottom sheet 里。
              isPhoneLayout(context)
                  ? '点上方列表按钮选页阅读'
                  : '在左侧点开页面阅读；⌘K 快速跳转',
              style: TextStyle(color: BiuTokens.textMuted, fontSize: 13),
            ),
          ],
        ),
      ),
    );
  }
}

/// 右栏 detail —— 实时条幅 + [WikiPageDetail]（读/编切换、工具条、
/// frontmatter / related / backlinks / 大纲全套）。调用方保证
/// state.activePage != null。
class _PageDetail extends StatelessWidget {
  const _PageDetail({
    required this.state,
    required this.projectId,
  });

  final WikiState state;
  final String projectId;

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: <Widget>[
        // 实时条幅：ingest 进行中 / 刚 LLM 重写完。
        PageRealtimeBanners(projectId: projectId, pageId: state.activePage!.id),
        Expanded(child: WikiPageDetail(state: state)),
      ],
    );
  }
}

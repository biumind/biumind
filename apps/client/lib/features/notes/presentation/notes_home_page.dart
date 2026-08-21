/// NotesHomePage —— 笔记主页，三栏心智（参考 wiki project_browser）：
///
///   ┌──────────┬──────────────┬─────────────────────┐
///   │ 笔记本    │ 笔记列表      │ 编辑器              │
///   │ 全部      │ 标题+摘要+    │ NoteEditorView      │
///   │ 未归档    │ 更新时间      │ (Milkdown)          │
///   │ …笔记本树 │              │                     │
///   └──────────┴──────────────┴─────────────────────┘
///
/// 笔记本多级目录（PR4）：左栏按 parentId 组装成树渲染（缩进 + 展开/收起，
/// 收起集合 SharedPreferences 持久化，key `notes_notebooks_tree_collapsed`），
/// 右键 / 长按 / 行尾「⋯」共用操作菜单（新建子目录 / 移动到… / 升到根级 /
/// 删除笔记本——连带软删：本内活笔记进回收站，子本上移一层，笔记本不可
/// 恢复）。树组装纯函数见 application/notebook_tree.dart（悬空 parent /
/// 环防御，不丢节点）。
///
/// 手机形态（<600px，core/layout/form_factor.dart）退化为列表 ↔ 详情：
/// 笔记本收进 bottom sheet，点笔记 push NoteEditorPage。
///
/// 顶部动作：新建笔记（中栏头）/ 新建笔记本（左栏头）/ 回收站入口。
/// pendingCreate（未同步）的笔记本/笔记灰显。
///
/// 中栏头部下方是全文搜索框（N2）：输入防抖 ~300ms 后走服务端
/// /v1/notes/search，中栏切换为搜索结果视图（与原过滤源并存，清空搜索
/// 即回原视图）。搜索是纯服务端调用，离线无本地降级（N2 范围外）。
library;

import 'dart:async';
import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import 'package:shared_preferences/shared_preferences.dart';

import '../../../app/theme.dart';
import '../../../core/editor/editor_native_view.dart';
import '../../../core/layout/form_factor.dart';
import '../../../core/ui/popup_position.dart';
import '../../../data/api/notes_client.dart' as api;
import '../../../data/notes_providers.dart';
import '../../../data/notes_repository.dart';
import '../application/notebook_tree.dart';
import '../application/notes_ui_providers.dart';
import 'note_editor_view.dart';

class NotesHomePage extends ConsumerStatefulWidget {
  const NotesHomePage({super.key});

  @override
  ConsumerState<NotesHomePage> createState() => _NotesHomePageState();
}

class _NotesHomePageState extends ConsumerState<NotesHomePage> {
  @override
  void initState() {
    super.initState();
    // 提前把编辑器共享的 localhost server 跑起来（fire-and-forget），
    // 首个笔记编辑器 webview 不必再等 server 启动。
    unawaited(EditorNativeView.warmup());
  }

  Future<void> _createNote() async {
    final repo = ref.read(notesRepositoryProvider);
    if (repo == null) return;
    final filter = ref.read(notesFilterProvider);
    final phone = isPhoneLayout(context);
    final note = await repo.createNote(
      title: '',
      notebookId: filter.kind == NotesListKind.notebook
          ? filter.notebookId
          : null,
      // 待办视图下新建直接是待办，否则建完不在当前列表里。
      isTodo: filter.kind == NotesListKind.todo,
    );
    ref.read(selectedNoteIdProvider.notifier).state = note.id;
    if (phone && mounted) {
      await _pushEditor(note.id);
    }
  }

  Future<void> _pushEditor(String noteId) {
    ref.read(selectedNoteIdProvider.notifier).state = noteId;
    return Navigator.of(context).push(MaterialPageRoute<void>(
      builder: (_) => NoteEditorPage(noteId: noteId),
    ));
  }

  @override
  Widget build(BuildContext context) {
    // 拉起 flusher + changes 轮询（跟随 credentials，离开笔记页自动停）。
    ref.watch(notesSyncPollerProvider);

    final repo = ref.watch(notesRepositoryProvider);
    if (repo == null) {
      return const Center(child: Text('连接 hub 后可使用笔记'));
    }

    final phone = isPhoneLayout(context);
    final selectedId = ref.watch(selectedNoteIdProvider);
    final searching = ref.watch(notesSearchQueryProvider).trim().isNotEmpty;

    if (phone) {
      return Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: <Widget>[
          _NoteListHeader(
            onCreateNote: _createNote,
            onOpenNotebooks: _openNotebookSheet,
            compact: true,
          ),
          const _NoteSearchField(),
          Divider(height: 1, color: BiuTokens.borderSubtle),
          Expanded(
            child: searching
                ? _SearchResultsView(onOpen: _pushEditor)
                : _NoteListView(
                    selectedNoteId: null,
                    onTap: (note) => _pushEditor(note.id),
                  ),
          ),
        ],
      );
    }

    return Row(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: <Widget>[
        const SizedBox(width: 200, child: NotebookColumn()),
        Container(width: 1, color: BiuTokens.borderSubtle),
        SizedBox(
          width: 300,
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: <Widget>[
              _NoteListHeader(onCreateNote: _createNote),
              const _NoteSearchField(),
              Divider(height: 1, color: BiuTokens.borderSubtle),
              Expanded(
                child: searching
                    ? _SearchResultsView(
                        onOpen: (id) => ref
                            .read(selectedNoteIdProvider.notifier)
                            .state = id,
                      )
                    : _NoteListView(
                        selectedNoteId: selectedId,
                        onTap: (note) => ref
                            .read(selectedNoteIdProvider.notifier)
                            .state = note.id,
                      ),
              ),
            ],
          ),
        ),
        Container(width: 1, color: BiuTokens.borderSubtle),
        Expanded(
          child: Container(
            color: BiuTokens.bg,
            child: selectedId == null
                ? const _DetailPlaceholder()
                // 不带 key：切笔记走 NoteEditorView.didUpdateWidget 复用
                // 同一编辑器 webview，不为每条笔记重载 2MB bundle。
                : NoteEditorView(noteId: selectedId),
          ),
        ),
      ],
    );
  }

  void _openNotebookSheet() {
    showModalBottomSheet<void>(
      context: context,
      showDragHandle: true,
      builder: (sheetCtx) => SizedBox(
        height: MediaQuery.sizeOf(sheetCtx).height * 0.6,
        child: NotebookColumn(
          onSelected: () => Navigator.of(sheetCtx).pop(),
        ),
      ),
    );
  }
}

/// 手机详情页包装 —— 桌面三栏右栏与手机 push 路由共用 NoteEditorView。
class NoteEditorPage extends ConsumerWidget {
  const NoteEditorPage({super.key, required this.noteId});

  final String noteId;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    ref.watch(notesSyncPollerProvider);
    return Scaffold(
      appBar: AppBar(
        title: const Text('笔记'),
        leading: IconButton(
          icon: const Icon(Icons.arrow_back),
          onPressed: () => Navigator.of(context).pop(),
        ),
      ),
      body: NoteEditorView(noteId: noteId),
    );
  }
}

class _DetailPlaceholder extends StatelessWidget {
  const _DetailPlaceholder();

  @override
  Widget build(BuildContext context) {
    return Center(
      child: Text(
        '选择一条笔记，或新建一条',
        style: TextStyle(color: BiuTokens.textMuted, fontSize: 13),
      ),
    );
  }
}

// ─── 左栏：笔记本 ────────────────────────────────────────────

/// 左栏「笔记本」列（桌面三栏 / 手机 bottom sheet 共用）。public 以便
/// widget test 独立 pump（test/features/notes/notebook_tree_ui_test.dart）。
class NotebookColumn extends ConsumerStatefulWidget {
  const NotebookColumn({super.key, this.onSelected});

  /// 手机 bottom sheet 形态：选中后关 sheet。
  final VoidCallback? onSelected;

  @override
  ConsumerState<NotebookColumn> createState() => NotebookColumnState();
}

class NotebookColumnState extends ConsumerState<NotebookColumn> {
  /// 收起集合 —— 持久化在 SharedPreferences（不落服务端）。null = 尚未
  /// 读取完成，期间按全展开渲染（默认展开；存「收起」而非「展开」集合，
  /// 新建的父目录天然默认展开，参照 wiki _PageList 的持久化模式）。
  Set<String>? _collapsed;

  static const _prefsKey = 'notes_notebooks_tree_collapsed';

  @override
  void initState() {
    super.initState();
    _loadCollapsed();
  }

  Future<void> _loadCollapsed() async {
    final prefs = await SharedPreferences.getInstance();
    final raw = prefs.getString(_prefsKey);
    if (!mounted) return;
    setState(() {
      if (raw == null) {
        _collapsed = {};
        return;
      }
      try {
        final decoded = jsonDecode(raw);
        _collapsed =
            decoded is List ? decoded.whereType<String>().toSet() : {};
      } catch (_) {
        _collapsed = {};
      }
    });
  }

  Future<void> _toggleCollapsed(String id) async {
    final next = {...?_collapsed};
    if (next.contains(id)) {
      next.remove(id);
    } else {
      next.add(id);
    }
    setState(() => _collapsed = next);
    final prefs = await SharedPreferences.getInstance();
    await prefs.setString(_prefsKey, jsonEncode(next.toList()));
  }

  Future<void> _createNotebook({String? parentId}) async {
    final name = await showDialog<String>(
      context: context,
      builder: (ctx) => _NamePromptDialog(
        title: parentId == null ? '新建笔记本' : '新建子目录',
        hint: '笔记本名称',
      ),
    );
    if (name == null || name.trim().isEmpty) return;
    final repo = ref.read(notesRepositoryProvider);
    await repo?.createNotebook(name.trim(), parentId: parentId);
    if (parentId != null && mounted) {
      // 自动展开父节点，新建的子目录立刻可见。
      final next = {...?_collapsed}..remove(parentId);
      setState(() => _collapsed = next);
      final prefs = await SharedPreferences.getInstance();
      await prefs.setString(_prefsKey, jsonEncode(next.toList()));
    }
  }

  Future<void> _createTag() async {
    final name = await showDialog<String>(
      context: context,
      builder: (ctx) => const _NamePromptDialog(
        title: '新建标签',
        hint: '标签名称',
      ),
    );
    if (name == null || name.trim().isEmpty) return;
    final repo = ref.read(notesRepositoryProvider);
    await repo?.createTag(name.trim());
  }

  /// 「移动到…」：目标选择弹窗（平铺树形列出可选目录 + 根级选项，排除
  /// 自身与后代）。返回 '' = 升根（对齐服务端空串约定），id = 移到该目录。
  Future<void> _moveNotebook(RepoNotebook nb, List<RepoNotebook> flat) async {
    final exclude = notebookSubtreeIds(flat, nb.id);
    final target = await showDialog<String>(
      context: context,
      builder: (ctx) => _MoveNotebookDialog(
        notebooks: [
          for (final other in flat)
            if (!exclude.contains(other.id)) other,
        ],
      ),
    );
    if (target == null || !mounted) return;
    final repo = ref.read(notesRepositoryProvider);
    if (target.isEmpty) {
      await repo?.updateNotebook(nb.id, moveToRoot: true);
    } else {
      await repo?.updateNotebook(nb.id, parentId: target);
    }
  }

  /// 笔记本操作菜单项 —— 右键 / 长按 / 行尾「⋯」三入口共用。删除项放
  /// 分隔线后用危险色（threads_shell_page 删除项同款惯例）。
  List<PopupMenuEntry<String>> _notebookMenuItems(RepoNotebook nb) {
    final error = Theme.of(context).colorScheme.error;
    return <PopupMenuEntry<String>>[
      const PopupMenuItem(value: 'child', child: Text('新建子目录')),
      const PopupMenuItem(value: 'move', child: Text('移动到…')),
      if (nb.parentId != null)
        const PopupMenuItem(value: 'root', child: Text('升到根级')),
      const PopupMenuDivider(height: 8),
      PopupMenuItem(
        value: 'delete',
        child: Text('删除笔记本', style: TextStyle(color: error)),
      ),
    ];
  }

  /// 右键 / 长按弹上下文菜单（锚定指针位置）。
  Future<void> _showNotebookMenu(
      NotebookTreeNode node, Offset globalPos, List<RepoNotebook> flat) async {
    final action = await showMenu<String>(
      context: context,
      position: popupPositionAt(context, globalPos),
      items: _notebookMenuItems(node.notebook),
    );
    if (action == null || !mounted) return;
    await _onNotebookAction(node.notebook, action, flat);
  }

  /// 「删除笔记本」：二次确认 → 连带软删（本内活笔记进回收站，子本上移
  /// 一层；笔记本本身无回收站、不可恢复）。删完清理选中态 —— 当前过滤
  /// 指向被删本或其子树时重置为「全部笔记」（_trashThisNote 同款模式）。
  Future<void> _confirmDeleteNotebook(
      RepoNotebook nb, List<RepoNotebook> flat) async {
    final repo = ref.read(notesRepositoryProvider);
    if (repo == null) return;
    final noteCount =
        (await repo.watchNotes(notebookId: nb.id).first).length;
    if (!mounted) return;
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('删除笔记本'),
        content: Text('删除笔记本「${nb.name}」？'
            '${noteCount > 0 ? '本内 $noteCount 条笔记将移入回收站，' : ''}'
            '子笔记本将上移一层。笔记本删除后不可恢复'
            '${noteCount > 0 ? '（笔记可在回收站还原）' : ''}。'),
        actions: <Widget>[
          TextButton(
            onPressed: () => Navigator.of(ctx).pop(false),
            child: const Text('取消'),
          ),
          FilledButton(
            onPressed: () => Navigator.of(ctx).pop(true),
            child: const Text('删除'),
          ),
        ],
      ),
    );
    if (confirmed != true || !mounted) return;
    final filter = ref.read(notesFilterProvider);
    if (filter.kind == NotesListKind.notebook &&
        notebookSubtreeIds(flat, nb.id).contains(filter.notebookId)) {
      ref.read(notesFilterProvider.notifier).state = const NotesFilter.all();
      ref.read(selectedNoteIdProvider.notifier).state = null;
    }
    await repo.deleteNotebook(nb.id);
  }

  Future<void> _onNotebookAction(
      RepoNotebook nb, String action, List<RepoNotebook> flat) async {
    switch (action) {
      case 'child':
        await _createNotebook(parentId: nb.id);
      case 'move':
        await _moveNotebook(nb, flat);
      case 'root':
        final repo = ref.read(notesRepositoryProvider);
        await repo?.updateNotebook(nb.id, moveToRoot: true);
      case 'delete':
        await _confirmDeleteNotebook(nb, flat);
    }
  }

  @override
  Widget build(BuildContext context) {
    final notebooks = ref.watch(notesNotebooksProvider).valueOrNull ??
        const <RepoNotebook>[];
    final tags =
        ref.watch(notesTagsProvider).valueOrNull ?? const <RepoTag>[];
    final filter = ref.watch(notesFilterProvider);
    final collapsed = _collapsed ?? const <String>{};
    final visible =
        flattenNotebookTree(buildNotebookTree(notebooks), collapsed);

    void select(NotesFilter f) {
      ref.read(notesFilterProvider.notifier).state = f;
      widget.onSelected?.call();
    }

    return Container(
      color: BiuTokens.surface,
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: <Widget>[
          _ColumnHeader(
            title: '笔记本',
            action: IconButton(
              tooltip: '新建笔记本',
              onPressed: () => _createNotebook(),
              icon: const Icon(Icons.create_new_folder_outlined, size: 16),
              padding: EdgeInsets.zero,
              constraints: const BoxConstraints(minWidth: 28, minHeight: 28),
            ),
          ),
          Divider(height: 1, color: BiuTokens.borderSubtle),
          Expanded(
            child: ListView(
              children: <Widget>[
                _FilterTile(
                  icon: Icons.notes,
                  label: '全部笔记',
                  selected: filter.kind == NotesListKind.all,
                  onTap: () => select(const NotesFilter.all()),
                ),
                _FilterTile(
                  icon: Icons.inbox_outlined,
                  label: '未归档',
                  selected: filter.kind == NotesListKind.unfiled,
                  onTap: () => select(const NotesFilter.unfiled()),
                ),
                _FilterTile(
                  icon: Icons.check_box_outlined,
                  label: '待办',
                  selected: filter.kind == NotesListKind.todo,
                  onTap: () => select(const NotesFilter.todo()),
                ),
                if (notebooks.isNotEmpty)
                  Divider(height: 1, color: BiuTokens.borderSubtle),
                for (final node in visible)
                  _NotebookTile(
                    node: node,
                    selected: filter.kind == NotesListKind.notebook &&
                        filter.notebookId == node.notebook.id,
                    collapsed: collapsed.contains(node.notebook.id),
                    onTap: () =>
                        select(NotesFilter.notebook(node.notebook.id)),
                    onToggle: () => _toggleCollapsed(node.notebook.id),
                    onContextMenu: (pos) =>
                        _showNotebookMenu(node, pos, notebooks),
                    menuItemsBuilder: () =>
                        _notebookMenuItems(node.notebook),
                    onAction: (action) =>
                        _onNotebookAction(node.notebook, action, notebooks),
                  ),
                Divider(height: 1, color: BiuTokens.borderSubtle),
                _TagsSectionHeader(
                  onCreateTag: _createTag,
                ),
                for (final tag in tags)
                  _FilterTile(
                    icon: Icons.sell_outlined,
                    label: tag.name,
                    dimmed: tag.pendingCreate,
                    selected: filter.kind == NotesListKind.tag &&
                        filter.tagId == tag.id,
                    onTap: () => select(NotesFilter.tag(tag.id)),
                  ),
                if (tags.isEmpty)
                  Padding(
                    padding: const EdgeInsets.symmetric(
                        horizontal: 12, vertical: 6),
                    child: Text(
                      '暂无标签',
                      style: TextStyle(
                          fontSize: 12, color: BiuTokens.textMuted),
                    ),
                  ),
              ],
            ),
          ),
        ],
      ),
    );
  }
}

/// 树形笔记本行：缩进 + 展开/收起箭头 + 行尾操作菜单（新建子目录 /
/// 移动到… / 升到根级 / 删除笔记本）。桌面右键、触屏长按弹同一菜单
/// （锚定指针位置，见 popup_position.dart）。选中态/灰显语义与
/// _FilterTile 一致。
class _NotebookTile extends StatelessWidget {
  const _NotebookTile({
    required this.node,
    required this.selected,
    required this.collapsed,
    required this.onTap,
    required this.onToggle,
    required this.onContextMenu,
    required this.menuItemsBuilder,
    required this.onAction,
  });

  final NotebookTreeNode node;
  final bool selected;
  final bool collapsed;
  final VoidCallback onTap;
  final VoidCallback onToggle;

  /// 右键 / 长按（参数为指针屏幕坐标，父级用 popupPositionAt 锚定弹菜单）。
  final ValueChanged<Offset> onContextMenu;

  /// 菜单项构建（与右键/长按菜单共用，由父级注入保证三入口一致）。
  final List<PopupMenuEntry<String>> Function() menuItemsBuilder;

  /// 行尾菜单动作：'child'（新建子目录）/ 'move'（移动到…）/ 'root'
  /// （升根）/ 'delete'（删除笔记本）。
  final ValueChanged<String> onAction;

  @override
  Widget build(BuildContext context) {
    final nb = node.notebook;
    final color = selected ? BiuTokens.purple : BiuTokens.text;
    return Opacity(
      opacity: nb.pendingCreate ? 0.5 : 1,
      child: GestureDetector(
        onSecondaryTapDown: (d) => onContextMenu(d.globalPosition),
        onLongPressStart: (d) => onContextMenu(d.globalPosition),
        child: InkWell(
          onTap: onTap,
          child: Container(
            height: 34,
            padding: EdgeInsets.only(left: 12 + node.depth * 14),
            color: selected ? BiuTokens.purpleLight : null,
            child: Row(
              children: <Widget>[
                SizedBox(
                  width: 16,
                  child: node.children.isEmpty
                      ? null
                      : InkWell(
                          onTap: onToggle,
                          child: Icon(
                            collapsed
                                ? Icons.chevron_right
                                : Icons.expand_more,
                            size: 14,
                            color: BiuTokens.textSecondary,
                          ),
                        ),
                ),
                Icon(Icons.folder_outlined,
                    size: 15,
                    color: selected
                        ? BiuTokens.purple
                        : BiuTokens.textSecondary),
                const SizedBox(width: 8),
                Expanded(
                  child: Text(
                    nb.name,
                    style: TextStyle(
                      fontSize: 13,
                      color: color,
                      fontWeight:
                          selected ? FontWeight.w600 : FontWeight.normal,
                    ),
                    overflow: TextOverflow.ellipsis,
                  ),
                ),
                SizedBox(
                  width: 24,
                  height: 24,
                  child: PopupMenuButton<String>(
                    tooltip: '笔记本操作',
                    icon: Icon(Icons.more_horiz,
                        size: 14, color: BiuTokens.textSecondary),
                    padding: EdgeInsets.zero,
                    iconSize: 14,
                    itemBuilder: (ctx) => menuItemsBuilder(),
                    onSelected: onAction,
                  ),
                ),
                const SizedBox(width: 8),
              ],
            ),
          ),
        ),
      ),
    );
  }
}

/// 「移动到…」目标选择弹窗：根级 + 可选目录（树形缩进；调用方已排除自身
/// 与后代）。pop 返回值：'' = 根级，其它 = 目标目录 id，null = 取消。
class _MoveNotebookDialog extends StatelessWidget {
  const _MoveNotebookDialog({required this.notebooks});

  final List<RepoNotebook> notebooks;

  @override
  Widget build(BuildContext context) {
    final visible =
        flattenNotebookTree(buildNotebookTree(notebooks), const {});
    return SimpleDialog(
      title: const Text('移动到…'),
      children: <Widget>[
        SimpleDialogOption(
          onPressed: () => Navigator.of(context).pop(''),
          child: Row(
            children: <Widget>[
              Icon(Icons.drive_file_move_outlined,
                  size: 15, color: BiuTokens.textSecondary),
              const SizedBox(width: 8),
              const Text('根级（移到顶层）'),
            ],
          ),
        ),
        for (final node in visible)
          SimpleDialogOption(
            onPressed: () => Navigator.of(context).pop(node.notebook.id),
            child: Padding(
              padding: EdgeInsets.only(left: node.depth * 14),
              child: Row(
                children: <Widget>[
                  Icon(Icons.folder_outlined,
                      size: 15, color: BiuTokens.textSecondary),
                  const SizedBox(width: 8),
                  Flexible(
                    child: Text(
                      node.notebook.name,
                      overflow: TextOverflow.ellipsis,
                    ),
                  ),
                ],
              ),
            ),
          ),
      ],
    );
  }
}

class _FilterTile extends StatelessWidget {
  const _FilterTile({
    required this.icon,
    required this.label,
    required this.selected,
    required this.onTap,
    this.dimmed = false,
  });

  final IconData icon;
  final String label;
  final bool selected;
  final bool dimmed;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final color = selected ? BiuTokens.purple : BiuTokens.text;
    return Opacity(
      opacity: dimmed ? 0.5 : 1,
      child: InkWell(
        onTap: onTap,
        child: Container(
          height: 34,
          padding: const EdgeInsets.symmetric(horizontal: 12),
          color: selected ? BiuTokens.purpleLight : null,
          child: Row(
            children: <Widget>[
              Icon(icon, size: 15,
                  color: selected ? BiuTokens.purple : BiuTokens.textSecondary),
              const SizedBox(width: 8),
              Expanded(
                child: Text(
                  label,
                  style: TextStyle(
                    fontSize: 13,
                    color: color,
                    fontWeight:
                        selected ? FontWeight.w600 : FontWeight.normal,
                  ),
                  overflow: TextOverflow.ellipsis,
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

// ─── 中栏：笔记列表 ──────────────────────────────────────────

/// 左栏「标签」分区头（标题 + 新建标签）。
class _TagsSectionHeader extends StatelessWidget {
  const _TagsSectionHeader({required this.onCreateTag});

  final VoidCallback onCreateTag;

  @override
  Widget build(BuildContext context) {
    return Container(
      height: 32,
      padding: const EdgeInsets.symmetric(horizontal: 12),
      child: Row(
        children: <Widget>[
          Expanded(
            child: Text(
              '标签',
              style: TextStyle(
                color: BiuTokens.textMuted,
                fontSize: 11,
                fontWeight: FontWeight.w600,
              ),
            ),
          ),
          InkWell(
            onTap: onCreateTag,
            borderRadius: BorderRadius.circular(4),
            child: Padding(
              padding: const EdgeInsets.all(2),
              child: Icon(Icons.add, size: 14, color: BiuTokens.textSecondary),
            ),
          ),
        ],
      ),
    );
  }
}

class _NoteListHeader extends ConsumerWidget {
  const _NoteListHeader({
    required this.onCreateNote,
    this.onOpenNotebooks,
    this.compact = false,
  });

  final VoidCallback onCreateNote;

  /// 手机形态：非空时在最左 prepend 笔记本入口（开 bottom sheet）。
  final VoidCallback? onOpenNotebooks;
  final bool compact;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final filter = ref.watch(notesFilterProvider);
    final notebooks = ref.watch(notesNotebooksProvider).valueOrNull;
    final tags = ref.watch(notesTagsProvider).valueOrNull;
    final title = switch (filter.kind) {
      NotesListKind.all => '全部笔记',
      NotesListKind.unfiled => '未归档',
      NotesListKind.todo => '待办',
      NotesListKind.notebook => notebooks
              ?.where((nb) => nb.id == filter.notebookId)
              .firstOrNull
              ?.name ??
          '笔记本',
      NotesListKind.tag => tags
              ?.where((t) => t.id == filter.tagId)
              .firstOrNull
              ?.name ??
          '标签',
    };
    return Container(
      height: 44,
      color: compact ? BiuTokens.surface : null,
      padding: const EdgeInsets.symmetric(horizontal: 8),
      child: Row(
        children: <Widget>[
          if (onOpenNotebooks != null) ...<Widget>[
            IconButton(
              tooltip: '笔记本',
              onPressed: onOpenNotebooks,
              icon: const Icon(Icons.folder_outlined, size: 16),
              padding: EdgeInsets.zero,
              constraints: const BoxConstraints(minWidth: 40, minHeight: 40),
            ),
            const SizedBox(width: 4),
          ] else
            const SizedBox(width: 4),
          Expanded(
            child: Text(
              title,
              style: TextStyle(
                color: BiuTokens.text,
                fontSize: 13,
                fontWeight: FontWeight.w600,
              ),
              overflow: TextOverflow.ellipsis,
            ),
          ),
          IconButton(
            tooltip: '回收站',
            onPressed: () => context.go('/notes/trash'),
            icon: Icon(Icons.delete_outline,
                size: 16, color: BiuTokens.textSecondary),
            padding: EdgeInsets.zero,
            constraints: const BoxConstraints(minWidth: 28, minHeight: 28),
          ),
          IconButton(
            tooltip: '新建笔记',
            onPressed: onCreateNote,
            icon: Icon(Icons.add, size: 18, color: BiuTokens.textSecondary),
            padding: EdgeInsets.zero,
            constraints: const BoxConstraints(minWidth: 28, minHeight: 28),
          ),
        ],
      ),
    );
  }
}

/// 中栏搜索框 —— 输入防抖 ~300ms 写入 notesSearchQueryProvider；非空时
/// 中栏由 _SearchResultsView 接管。清空（手动删空或点 ×）即回原列表。
class _NoteSearchField extends ConsumerStatefulWidget {
  const _NoteSearchField();

  @override
  ConsumerState<_NoteSearchField> createState() => _NoteSearchFieldState();
}

class _NoteSearchFieldState extends ConsumerState<_NoteSearchField> {
  late final TextEditingController _controller;
  Timer? _debounce;

  @override
  void initState() {
    super.initState();
    // 页面重建（如手机形态返回）时回填进行中的搜索词，保持视图一致。
    _controller =
        TextEditingController(text: ref.read(notesSearchQueryProvider));
  }

  @override
  void dispose() {
    _debounce?.cancel();
    _controller.dispose();
    super.dispose();
  }

  void _onChanged(String v) {
    _debounce?.cancel();
    _debounce = Timer(const Duration(milliseconds: 300), () {
      ref.read(notesSearchQueryProvider.notifier).state = v;
    });
    // 仅驱动 × 按钮显隐。
    setState(() {});
  }

  void _submit(String v) {
    _debounce?.cancel();
    ref.read(notesSearchQueryProvider.notifier).state = v;
  }

  void _clear() {
    _debounce?.cancel();
    _controller.clear();
    ref.read(notesSearchQueryProvider.notifier).state = '';
    setState(() {});
  }

  @override
  Widget build(BuildContext context) {
    return Container(
      height: 40,
      color: BiuTokens.surface,
      padding: const EdgeInsets.symmetric(horizontal: 8),
      alignment: Alignment.center,
      child: TextField(
        controller: _controller,
        onChanged: _onChanged,
        onSubmitted: _submit,
        style: TextStyle(fontSize: 13, color: BiuTokens.text),
        decoration: InputDecoration(
          isDense: true,
          hintText: '搜索笔记',
          hintStyle: TextStyle(fontSize: 13, color: BiuTokens.textMuted),
          prefixIcon:
              Icon(Icons.search, size: 16, color: BiuTokens.textSecondary),
          prefixIconConstraints:
              const BoxConstraints(minWidth: 28, minHeight: 28),
          suffixIcon: _controller.text.isEmpty
              ? null
              : IconButton(
                  tooltip: '清空搜索',
                  onPressed: _clear,
                  icon: Icon(Icons.close,
                      size: 14, color: BiuTokens.textSecondary),
                  padding: EdgeInsets.zero,
                  constraints:
                      const BoxConstraints(minWidth: 28, minHeight: 28),
                ),
          border: InputBorder.none,
        ),
      ),
    );
  }
}

/// 搜索结果视图 —— 数据走 notesSearchResultsProvider（纯服务端）。
/// 无网络/报错：错误态 + 重试；不做本地降级（N2 范围外）。
class _SearchResultsView extends ConsumerWidget {
  const _SearchResultsView({required this.onOpen});

  /// 点击结果打开笔记（桌面选中 / 手机 push 编辑器，由调用方决定）。
  final ValueChanged<String> onOpen;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final results = ref.watch(notesSearchResultsProvider);
    return Container(
      color: BiuTokens.surface,
      child: results.when(
        loading: () => const Center(child: CircularProgressIndicator()),
        error: (_, _) => Center(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: <Widget>[
              Text(
                '搜索失败，请检查网络后重试',
                style: TextStyle(fontSize: 13, color: BiuTokens.textMuted),
              ),
              const SizedBox(height: 8),
              TextButton.icon(
                onPressed: () => ref.invalidate(notesSearchResultsProvider),
                icon: const Icon(Icons.refresh, size: 16),
                label: const Text('重试'),
              ),
            ],
          ),
        ),
        data: (items) {
          if (items.isEmpty) {
            return Center(
              child: Text(
                '无匹配结果',
                style: TextStyle(fontSize: 13, color: BiuTokens.textMuted),
              ),
            );
          }
          return ListView.separated(
            itemCount: items.length,
            separatorBuilder: (_, _) =>
                Divider(height: 1, indent: 12, color: BiuTokens.borderSubtle),
            itemBuilder: (context, i) => _SearchResultTile(
              result: items[i],
              onTap: () => onOpen(items[i].id),
            ),
          );
        },
      ),
    );
  }
}

class _SearchResultTile extends StatelessWidget {
  const _SearchResultTile({required this.result, required this.onTap});

  final api.NoteSearchResult result;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return InkWell(
      onTap: onTap,
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: <Widget>[
            Text(
              result.title.isEmpty ? '无标题笔记' : result.title,
              style: TextStyle(
                fontSize: 13,
                fontWeight: FontWeight.w600,
                color: BiuTokens.text,
              ),
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
            ),
            const SizedBox(height: 3),
            RichText(
              maxLines: 2,
              overflow: TextOverflow.ellipsis,
              text: TextSpan(
                style:
                    TextStyle(fontSize: 12, color: BiuTokens.textSecondary),
                children: searchSnippetSpans(
                  result.snippet,
                  markStyle: TextStyle(
                    fontSize: 12,
                    color: BiuTokens.purple,
                    fontWeight: FontWeight.w600,
                    backgroundColor: BiuTokens.purpleLight,
                  ),
                ),
              ),
            ),
            const SizedBox(height: 4),
            Text(
              relativeTime(result.updatedAt),
              style: TextStyle(fontSize: 11, color: BiuTokens.textMuted),
            ),
          ],
        ),
      ),
    );
  }
}

class _NoteListView extends ConsumerWidget {
  const _NoteListView({required this.selectedNoteId, required this.onTap});

  final String? selectedNoteId;
  final ValueChanged<RepoNote> onTap;

  Future<void> _toggleTodoCompleted(WidgetRef ref, RepoNote note) async {
    final repo = ref.read(notesRepositoryProvider);
    if (repo == null) return;
    if (note.todoCompletedAt == null) {
      await repo.updateNote(note.id,
          todoCompletedAt: DateTime.now().toUtc());
    } else {
      await repo.updateNote(note.id, clearTodoCompleted: true);
    }
  }

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final notesAsync = ref.watch(notesListProvider);
    final isTodoView =
        ref.watch(notesFilterProvider).kind == NotesListKind.todo;
    final notes = notesAsync.valueOrNull;
    if (notes == null) {
      return const Center(child: CircularProgressIndicator());
    }
    if (notes.isEmpty) {
      return Center(
        child: Text(
          isTodoView ? '暂无待办' : '暂无笔记',
          style: TextStyle(color: BiuTokens.textMuted, fontSize: 13),
        ),
      );
    }
    return Container(
      color: BiuTokens.surface,
      child: ListView.separated(
        itemCount: notes.length,
        separatorBuilder: (_, _) =>
            Divider(height: 1, indent: 12, color: BiuTokens.borderSubtle),
        itemBuilder: (context, i) {
          final note = notes[i];
          return _NoteTile(
            note: note,
            selected: note.id == selectedNoteId,
            onTap: () => onTap(note),
            todoMode: isTodoView,
            onToggleCompleted: () => _toggleTodoCompleted(ref, note),
          );
        },
      ),
    );
  }
}

class _NoteTile extends StatelessWidget {
  const _NoteTile({
    required this.note,
    required this.selected,
    required this.onTap,
    this.todoMode = false,
    this.onToggleCompleted,
  });

  final RepoNote note;
  final bool selected;
  final VoidCallback onTap;

  /// 待办视图：行首显示完成 checkbox。
  final bool todoMode;
  final VoidCallback? onToggleCompleted;

  @override
  Widget build(BuildContext context) {
    final completed = note.todoCompletedAt != null;
    return Opacity(
      opacity: note.pendingCreate ? 0.5 : 1,
      child: InkWell(
        onTap: onTap,
        child: Container(
          color: selected ? BiuTokens.purpleLight : null,
          padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
          child: Row(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: <Widget>[
              if (todoMode) ...<Widget>[
                SizedBox(
                  width: 20,
                  height: 20,
                  child: Checkbox(
                    value: completed,
                    onChanged:
                        onToggleCompleted == null ? null : (_) => onToggleCompleted!(),
                    materialTapTargetSize: MaterialTapTargetSize.shrinkWrap,
                    visualDensity: VisualDensity.compact,
                  ),
                ),
                const SizedBox(width: 8),
              ],
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: <Widget>[
                    Text(
                      note.title.isEmpty ? '无标题笔记' : note.title,
                      style: TextStyle(
                        fontSize: 13,
                        fontWeight: FontWeight.w600,
                        color: completed
                            ? BiuTokens.textMuted
                            : BiuTokens.text,
                        decoration:
                            completed ? TextDecoration.lineThrough : null,
                      ),
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                    ),
                    const SizedBox(height: 3),
                    Text(
                      noteExcerpt(note.contentMd),
                      style: TextStyle(
                          fontSize: 12, color: BiuTokens.textSecondary),
                      maxLines: 2,
                      overflow: TextOverflow.ellipsis,
                    ),
                    const SizedBox(height: 4),
                    Text(
                      relativeTime(note.updatedAt),
                      style:
                          TextStyle(fontSize: 11, color: BiuTokens.textMuted),
                    ),
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

// ─── 共用 ───────────────────────────────────────────────────

class _ColumnHeader extends StatelessWidget {
  const _ColumnHeader({required this.title, required this.action});

  final String title;
  final Widget action;

  @override
  Widget build(BuildContext context) {
    return Container(
      height: 44,
      padding: const EdgeInsets.symmetric(horizontal: 12),
      child: Row(
        children: <Widget>[
          Expanded(
            child: Text(
              title,
              style: TextStyle(
                color: BiuTokens.text,
                fontSize: 13,
                fontWeight: FontWeight.w600,
              ),
            ),
          ),
          action,
        ],
      ),
    );
  }
}

class _NamePromptDialog extends StatefulWidget {
  const _NamePromptDialog({required this.title, required this.hint});

  final String title;
  final String hint;

  @override
  State<_NamePromptDialog> createState() => _NamePromptDialogState();
}

class _NamePromptDialogState extends State<_NamePromptDialog> {
  final _controller = TextEditingController();

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: Text(widget.title),
      content: TextField(
        controller: _controller,
        autofocus: true,
        decoration: InputDecoration(hintText: widget.hint),
        onSubmitted: (v) => Navigator.of(context).pop(v),
      ),
      actions: <Widget>[
        TextButton(
          onPressed: () => Navigator.of(context).pop(),
          child: const Text('取消'),
        ),
        FilledButton(
          onPressed: () => Navigator.of(context).pop(_controller.text),
          child: const Text('确定'),
        ),
      ],
    );
  }
}

/// 摘要：取正文第一行非空文本，去掉行首 markdown 记号，截 60 字。
String noteExcerpt(String contentMd) {
  for (final line in contentMd.split('\n')) {
    final t = line
        .trimLeft()
        .replaceFirst(RegExp(r'^(#{1,6}|>|[-*+]|\d+\.)\s*'), '')
        .trim();
    if (t.isNotEmpty) {
      return t.length > 60 ? '${t.substring(0, 60)}…' : t;
    }
  }
  return '无内容';
}

/// 解析服务端搜索 snippet：笔记内容已 HTML 转义，命中词包在
/// `<mark>...</mark>` 里。mark 段用 [markStyle] 高亮，其余段解码实体、
/// 剥离残留标签 —— 绝不把尖括号原样显示给用户。
List<TextSpan> searchSnippetSpans(String snippet, {TextStyle? markStyle}) {
  final spans = <TextSpan>[];
  var rest = snippet;
  var inMark = false;
  while (rest.isNotEmpty) {
    final tag = inMark ? '</mark>' : '<mark>';
    final i = rest.indexOf(tag);
    final chunk = i < 0 ? rest : rest.substring(0, i);
    if (chunk.isNotEmpty) {
      spans.add(TextSpan(
        text: _decodeSnippetText(chunk),
        style: inMark ? markStyle : null,
      ));
    }
    if (i < 0) break;
    inMark = !inMark;
    rest = rest.substring(i + tag.length);
  }
  return spans;
}

String _decodeSnippetText(String s) {
  // 剥离 <mark> 以外的残留标签，再解码常见 HTML 实体（&amp; 必须最后解）。
  final stripped = s.replaceAll(RegExp(r'<[^>]*>'), '');
  return stripped
      .replaceAll('&lt;', '<')
      .replaceAll('&gt;', '>')
      .replaceAll('&quot;', '"')
      .replaceAll('&#39;', "'")
      .replaceAll('&amp;', '&');
}

/// 相对时间（本期不接 l10n，硬编码中文）。
String relativeTime(DateTime dt) {
  final diff = DateTime.now().difference(dt.toLocal());
  if (diff.inMinutes < 1) return '刚刚';
  if (diff.inHours < 1) return '${diff.inMinutes} 分钟前';
  if (diff.inDays < 1) return '${diff.inHours} 小时前';
  if (diff.inDays < 7) return '${diff.inDays} 天前';
  final l = dt.toLocal();
  return '${l.year}-${l.month.toString().padLeft(2, '0')}-'
      '${l.day.toString().padLeft(2, '0')}';
}

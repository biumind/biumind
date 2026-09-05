/// ⌘K 命令面板 —— wiki shell 范围。
///
/// Linear 风：顶部搜索框 + 过滤后的 action 列表（按 group 分隔）+
/// 上下键导航 + Enter 跳转 + Esc 关闭。
///
/// 两种模式：
///   - 命令模式（⌘K）：12 条 navigation action（页面 / 源文件 / 搜索 /
///     图谱 / 对话 / 研究 / 审查 / 去重 / 镜像 / 工作区 / 反馈 / 全局设置）。
///   - 页面跳转模式（⌘P，jumpToPage=true）：列出当前项目所有页面，按
///     页面名 / 路径过滤，Enter 跳到 `/wiki/p/:pid/pages/:pageId`。
library;

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../app/theme.dart';
import '../application/wiki_controller.dart';
import '../presentation/pages/pages_providers.dart';

class WikiCommandPalette {
  WikiCommandPalette._();

  /// 弹出命令面板。projectId 为空时只显示跨项目级别的命令（工作区 / 反馈）。
  ///
  /// [jumpToPage] true 时进入页面跳转模式（⌘P）：列表换成当前项目的
  /// 页面，按名称过滤跳页。projectId 为空（工作区模式）没有页面可跳，
  /// 自动退化为命令模式。
  static Future<void> show(
    BuildContext context, {
    required String projectId,
    bool jumpToPage = false,
  }) {
    final pagesMode = jumpToPage && projectId.isNotEmpty;
    return showGeneralDialog<void>(
      context: context,
      barrierLabel: 'Command palette',
      barrierDismissible: true,
      barrierColor: Colors.black.withValues(alpha: 0.45),
      transitionDuration: const Duration(milliseconds: 120),
      pageBuilder: (ctx, _, _) =>
          _PaletteSurface(projectId: projectId, jumpToPage: pagesMode),
      transitionBuilder: (ctx, anim, _, child) => FadeTransition(
        opacity: anim,
        child: ScaleTransition(
          scale: Tween(begin: 0.98, end: 1.0).animate(anim),
          child: child,
        ),
      ),
    );
  }
}

class _PaletteAction {
  const _PaletteAction({
    required this.label,
    required this.icon,
    required this.group,
    required this.run,
    this.subtitle,
    this.shortcut,
  });
  final String label;
  final String? subtitle;
  final IconData icon;
  final String group; // 导航 / 项目 / 应用
  final void Function(BuildContext) run;
  final String? shortcut; // ⌘P / ⌘, 等
}

class _PaletteSurface extends ConsumerStatefulWidget {
  const _PaletteSurface({required this.projectId, required this.jumpToPage});
  final String projectId;

  /// true = 页面跳转模式（⌘P）：列表为当前项目页面而非命令。
  final bool jumpToPage;

  @override
  ConsumerState<_PaletteSurface> createState() => _PaletteSurfaceState();
}

class _PaletteSurfaceState extends ConsumerState<_PaletteSurface> {
  final _controller = TextEditingController();
  final _focus = FocusNode();
  int _selected = 0;

  /// 当前模式的完整 action 列表，每次 build 重算（页面模式跟随
  /// pagesListProvider 实时刷新）。
  List<_PaletteAction> _actions = const [];

  @override
  void initState() {
    super.initState();
    if (widget.jumpToPage) {
      WidgetsBinding.instance.addPostFrameCallback((_) => _activateProject());
    }
  }

  /// pagesListProvider 只在 project 是 activeProject 时返回数据（见
  /// pages_providers.dart 注释）—— 深链进 /wiki/p/:pid/* 而未经过
  /// ProjectBrowserPage._sync 时，这里补一次 selectProject。
  Future<void> _activateProject() async {
    if (!mounted) return;
    final state = ref.read(wikiControllerProvider).valueOrNull;
    if (state == null || state.activeProject?.id == widget.projectId) return;
    for (final p in state.projects) {
      if (p.id == widget.projectId) {
        await ref.read(wikiControllerProvider.notifier).selectProject(p);
        return;
      }
    }
  }

  /// 页面跳转模式：当前项目页面映射为 action，复用过滤 / 键导航 / 渲染。
  List<_PaletteAction> _pageActions() {
    final pid = widget.projectId;
    final base = '/wiki/p/$pid';
    return ref.watch(pagesListProvider(pid)).map((p) {
      final relPath = (p.frontmatter['path'] as String?) ?? '';
      return _PaletteAction(
        label: p.title.isEmpty ? '(未命名)' : p.title,
        subtitle: relPath.isNotEmpty && relPath != p.title ? relPath : null,
        icon: Icons.description_outlined,
        group: '页面',
        run: (ctx) => ctx.go('$base/pages/${p.id}'),
      );
    }).toList(growable: false);
  }

  List<_PaletteAction> _buildActions() {
    final pid = widget.projectId;
    const navGroup = '导航';
    const projGroup = '项目';
    const appGroup = '应用';

    final actions = <_PaletteAction>[];

    if (pid.isNotEmpty) {
      final base = '/wiki/p/$pid';
      actions.addAll([
        _PaletteAction(
          label: '页面',
          subtitle: '查看本项目所有 wiki 页面',
          icon: Icons.description_outlined,
          // ⌘P = 按页面名跳页（WikiShell 绑定打开本面板的页面跳转模式）。
          shortcut: '⌘P',
          group: navGroup,
          run: (ctx) => ctx.go(base),
        ),
        _PaletteAction(
          label: '源文件',
          subtitle: '上传 PDF / MD / URL 解析为页面',
          icon: Icons.upload_file_outlined,
          group: navGroup,
          run: (ctx) => ctx.go('$base/sources'),
        ),
        _PaletteAction(
          label: '搜索',
          subtitle: 'BM25 + 语义 + 图谱三路命中',
          icon: Icons.search,
          group: navGroup,
          run: (ctx) => ctx.go('$base/search'),
        ),
        _PaletteAction(
          label: '图谱',
          subtitle: '页面关系网络可视化',
          icon: Icons.hub_outlined,
          group: navGroup,
          run: (ctx) => ctx.go('$base/graph'),
        ),
        _PaletteAction(
          label: '对话',
          subtitle: '基于本项目的对话',
          icon: Icons.chat_bubble_outline,
          group: navGroup,
          run: (ctx) => ctx.go('$base/chat'),
        ),
        _PaletteAction(
          label: '研究',
          subtitle: 'Deep Research 任务',
          icon: Icons.travel_explore_outlined,
          group: navGroup,
          run: (ctx) => ctx.go('$base/research'),
        ),
        _PaletteAction(
          label: '审查队列',
          subtitle: 'dedup / lint / sweep 产出',
          icon: Icons.fact_check_outlined,
          group: navGroup,
          run: (ctx) => ctx.go('$base/reviews'),
        ),
        _PaletteAction(
          label: '镜像 / 导出',
          subtitle: '导出为 markdown 包',
          icon: Icons.folder_copy_outlined,
          group: navGroup,
          run: (ctx) => ctx.go('$base/mirror'),
        ),
        _PaletteAction(
          label: '切换工作区',
          subtitle: '回到项目列表',
          icon: Icons.swap_horiz,
          group: projGroup,
          run: (ctx) => ctx.go('/wiki'),
        ),
      ]);
    } else {
      actions.add(
        _PaletteAction(
          label: '工作区',
          subtitle: '查看所有项目',
          icon: Icons.home_outlined,
          group: navGroup,
          run: (ctx) => ctx.go('/wiki'),
        ),
      );
    }

    actions.addAll([
      _PaletteAction(
        label: '反馈',
        subtitle: '产品反馈 + 路线图',
        icon: Icons.feedback_outlined,
        group: appGroup,
        run: (ctx) => ctx.go('/suggestions'),
      ),
      _PaletteAction(
        label: 'LLM 设置',
        subtitle: '模型与 provider 配置',
        icon: Icons.tune,
        shortcut: '⌘,',
        group: appGroup,
        run: (ctx) => ctx.go('/settings'),
      ),
      _PaletteAction(
        label: '全局设置',
        subtitle: '账号 / 主题 / 同步',
        icon: Icons.settings_outlined,
        group: appGroup,
        run: (ctx) => ctx.go('/settings'),
      ),
    ]);
    return actions;
  }

  List<_PaletteAction> get _filtered {
    final q = _controller.text.trim().toLowerCase();
    if (q.isEmpty) return _actions;
    return _actions
        .where((a) =>
            a.label.toLowerCase().contains(q) ||
            (a.subtitle ?? '').toLowerCase().contains(q))
        .toList(growable: false);
  }

  @override
  void dispose() {
    _controller.dispose();
    _focus.dispose();
    super.dispose();
  }

  void _runSelected(BuildContext ctx) {
    final list = _filtered;
    if (list.isEmpty) return;
    final action = list[_selected.clamp(0, list.length - 1)];
    Navigator.of(ctx).pop();
    action.run(ctx);
  }

  KeyEventResult _onKey(FocusNode node, KeyEvent event) {
    if (event is! KeyDownEvent) return KeyEventResult.ignored;
    final list = _filtered;
    if (list.isEmpty) {
      if (event.logicalKey == LogicalKeyboardKey.escape) {
        Navigator.of(context).pop();
        return KeyEventResult.handled;
      }
      return KeyEventResult.ignored;
    }
    if (event.logicalKey == LogicalKeyboardKey.arrowDown) {
      setState(() {
        _selected = (_selected + 1).clamp(0, list.length - 1);
      });
      return KeyEventResult.handled;
    }
    if (event.logicalKey == LogicalKeyboardKey.arrowUp) {
      setState(() {
        _selected = (_selected - 1).clamp(0, list.length - 1);
      });
      return KeyEventResult.handled;
    }
    if (event.logicalKey == LogicalKeyboardKey.enter ||
        event.logicalKey == LogicalKeyboardKey.numpadEnter) {
      _runSelected(context);
      return KeyEventResult.handled;
    }
    if (event.logicalKey == LogicalKeyboardKey.escape) {
      Navigator.of(context).pop();
      return KeyEventResult.handled;
    }
    return KeyEventResult.ignored;
  }

  @override
  Widget build(BuildContext context) {
    _actions = widget.jumpToPage ? _pageActions() : _buildActions();
    final list = _filtered;
    final selectedIdx = _selected.clamp(0, list.isEmpty ? 0 : list.length - 1);

    String? lastGroup;
    final children = <Widget>[];
    for (var i = 0; i < list.length; i++) {
      final action = list[i];
      if (action.group != lastGroup) {
        if (children.isNotEmpty) children.add(const SizedBox(height: 4));
        children.add(_GroupLabel(text: action.group));
        lastGroup = action.group;
      }
      children.add(_PaletteRow(
        action: action,
        selected: i == selectedIdx,
        onHover: () => setState(() => _selected = i),
        onTap: () => _runSelected(context),
      ));
    }
    if (children.isEmpty) {
      children.add(Padding(
        padding: const EdgeInsets.all(24),
        child: Text(
          widget.jumpToPage ? '没有匹配的页面' : '没有匹配的命令',
          style: TextStyle(color: BiuTokens.textMuted, fontSize: 13),
        ),
      ));
    }

    return Center(
      child: Material(
        type: MaterialType.transparency,
        child: Container(
          width: 560,
          constraints: const BoxConstraints(maxHeight: 480),
          decoration: BoxDecoration(
            color: BiuTokens.surface,
            borderRadius: BorderRadius.circular(BiuTokens.radiusLg),
            border: Border.all(color: BiuTokens.border),
            boxShadow: [
              BoxShadow(
                color: Colors.black.withValues(alpha: 0.25),
                blurRadius: 24,
                offset: const Offset(0, 8),
              ),
            ],
          ),
          child: Focus(
            focusNode: _focus,
            onKeyEvent: _onKey,
            autofocus: true,
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                Container(
                  padding: const EdgeInsets.symmetric(
                    horizontal: 16,
                    vertical: 10,
                  ),
                  decoration: BoxDecoration(
                    border: Border(
                      bottom: BorderSide(color: BiuTokens.borderSubtle),
                    ),
                  ),
                  child: Row(
                    children: [
                      Icon(Icons.search, size: 16, color: BiuTokens.textSecondary),
                      const SizedBox(width: 10),
                      Expanded(
                        child: TextField(
                          controller: _controller,
                          autofocus: true,
                          onChanged: (_) => setState(() => _selected = 0),
                          onSubmitted: (_) => _runSelected(context),
                          decoration: InputDecoration(
                            hintText:
                                widget.jumpToPage ? '输入页面名 …' : '输入命令或页面名 …',
                            hintStyle: TextStyle(
                              color: BiuTokens.textMuted,
                              fontSize: 13,
                            ),
                            border: InputBorder.none,
                            isDense: true,
                          ),
                          style: TextStyle(
                            color: BiuTokens.text,
                            fontSize: 14,
                          ),
                        ),
                      ),
                      Container(
                        padding: const EdgeInsets.symmetric(
                          horizontal: 5,
                          vertical: 1,
                        ),
                        decoration: BoxDecoration(
                          color: BiuTokens.surfaceMuted,
                          border: Border.all(color: BiuTokens.borderSubtle),
                          borderRadius: BorderRadius.circular(4),
                        ),
                        child: Text(
                          'esc',
                          style: TextStyle(
                            color: BiuTokens.textMuted,
                            fontSize: 11,
                          ),
                        ),
                      ),
                    ],
                  ),
                ),
                Flexible(
                  child: SingleChildScrollView(
                    padding:
                        const EdgeInsets.symmetric(vertical: 8),
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.stretch,
                      children: children,
                    ),
                  ),
                ),
                Container(
                  padding: const EdgeInsets.symmetric(
                    horizontal: 12,
                    vertical: 6,
                  ),
                  decoration: BoxDecoration(
                    border: Border(
                      top: BorderSide(color: BiuTokens.borderSubtle),
                    ),
                    color: BiuTokens.surfaceMuted,
                  ),
                  child: Row(
                    children: [
                      _Hint(label: '↑↓', text: '选择'),
                      const SizedBox(width: 12),
                      _Hint(label: '⏎', text: '执行'),
                      const SizedBox(width: 12),
                      _Hint(label: 'esc', text: '关闭'),
                      const Spacer(),
                      Text(
                        widget.jumpToPage
                            ? '${list.length} 个页面'
                            : '${list.length} 项命令',
                        style: TextStyle(
                          color: BiuTokens.textMuted,
                          fontSize: 11,
                        ),
                      ),
                    ],
                  ),
                ),
              ],
            ),
          ),
        ),
      ),
    );
  }
}

class _GroupLabel extends StatelessWidget {
  const _GroupLabel({required this.text});
  final String text;
  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 8, 16, 4),
      child: Text(
        text,
        style: TextStyle(
          color: BiuTokens.textMuted,
          fontSize: 11,
          fontWeight: FontWeight.w600,
          letterSpacing: 0.5,
        ),
      ),
    );
  }
}

class _PaletteRow extends StatelessWidget {
  const _PaletteRow({
    required this.action,
    required this.selected,
    required this.onHover,
    required this.onTap,
  });
  final _PaletteAction action;
  final bool selected;
  final VoidCallback onHover;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final bg = selected ? BiuTokens.purpleSoft : Colors.transparent;
    final fg = selected ? BiuTokens.text : BiuTokens.text;
    return MouseRegion(
      cursor: SystemMouseCursors.click,
      onEnter: (_) => onHover(),
      child: GestureDetector(
        onTap: onTap,
        child: Container(
          margin: const EdgeInsets.symmetric(horizontal: 8, vertical: 1),
          padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 8),
          decoration: BoxDecoration(
            color: bg,
            borderRadius: BorderRadius.circular(6),
          ),
          child: Row(
            children: [
              Icon(action.icon, size: 16, color: BiuTokens.textSecondary),
              const SizedBox(width: 12),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      action.label,
                      style: TextStyle(
                        color: fg,
                        fontSize: 13,
                        fontWeight: FontWeight.w500,
                      ),
                    ),
                    if (action.subtitle != null)
                      Text(
                        action.subtitle!,
                        style: TextStyle(
                          color: BiuTokens.textMuted,
                          fontSize: 11,
                        ),
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                      ),
                  ],
                ),
              ),
              if (action.shortcut != null) ...[
                const SizedBox(width: 8),
                Container(
                  padding: const EdgeInsets.symmetric(
                    horizontal: 5,
                    vertical: 1,
                  ),
                  decoration: BoxDecoration(
                    color: BiuTokens.surfaceMuted,
                    border: Border.all(color: BiuTokens.borderSubtle),
                    borderRadius: BorderRadius.circular(4),
                  ),
                  child: Text(
                    action.shortcut!,
                    style: TextStyle(
                      color: BiuTokens.textMuted,
                      fontSize: 11,
                    ),
                  ),
                ),
              ],
            ],
          ),
        ),
      ),
    );
  }
}

class _Hint extends StatelessWidget {
  const _Hint({required this.label, required this.text});
  final String label;
  final String text;

  @override
  Widget build(BuildContext context) {
    return Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        Container(
          padding: const EdgeInsets.symmetric(horizontal: 5, vertical: 1),
          decoration: BoxDecoration(
            color: BiuTokens.surface,
            border: Border.all(color: BiuTokens.borderSubtle),
            borderRadius: BorderRadius.circular(4),
          ),
          child: Text(
            label,
            style: TextStyle(color: BiuTokens.textMuted, fontSize: 11),
          ),
        ),
        const SizedBox(width: 4),
        Text(
          text,
          style: TextStyle(color: BiuTokens.textMuted, fontSize: 11),
        ),
      ],
    );
  }
}

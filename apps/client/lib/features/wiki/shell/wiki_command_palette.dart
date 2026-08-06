/// ⌘K 命令面板 —— wiki shell 范围。
///
/// Linear 风：顶部搜索框 + 过滤后的 action 列表（按 group 分隔）+
/// 上下键导航 + Enter 跳转 + Esc 关闭。
///
/// MVP 范围：12 条 navigation action（页面 / 源文件 / 搜索 / 图谱 / 对话
/// / 研究 / 审查 / 去重 / 镜像 / 工作区 / 反馈 / 全局设置）。
/// 后续可扩 "页面深链 (按 page title 模糊搜)" / "最近访问" / "所有命令"
/// 等扩展，留 Action 接口可继续注入。
library;

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:go_router/go_router.dart';

import '../../../app/theme.dart';

class WikiCommandPalette {
  WikiCommandPalette._();

  /// 弹出命令面板。projectId 为空时只显示跨项目级别的命令（工作区 / 反馈）。
  static Future<void> show(
    BuildContext context, {
    required String projectId,
  }) {
    return showGeneralDialog<void>(
      context: context,
      barrierLabel: 'Command palette',
      barrierDismissible: true,
      barrierColor: Colors.black.withValues(alpha: 0.45),
      transitionDuration: const Duration(milliseconds: 120),
      pageBuilder: (ctx, _, _) => _PaletteSurface(projectId: projectId),
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

class _PaletteSurface extends StatefulWidget {
  const _PaletteSurface({required this.projectId});
  final String projectId;

  @override
  State<_PaletteSurface> createState() => _PaletteSurfaceState();
}

class _PaletteSurfaceState extends State<_PaletteSurface> {
  final _controller = TextEditingController();
  final _focus = FocusNode();
  int _selected = 0;
  late List<_PaletteAction> _allActions;

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    _allActions = _buildActions();
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
          shortcut: '⌘P',
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
    if (q.isEmpty) return _allActions;
    return _allActions
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
          '没有匹配的命令',
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
                            hintText: '输入命令或页面名 …',
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
                        '${list.length} 项命令',
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

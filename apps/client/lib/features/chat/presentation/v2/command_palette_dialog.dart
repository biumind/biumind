// CommandPaletteDialog —— Cmd+K 全局命令面板。
// 设计文档 docs/BiuMind-Chat-UI-Benchmark-Optimization.md（v2 命令面板）。
//
// 行为：
//   * 输入框在顶部，下方过滤后的动作列表
//   * 子序列模糊匹配（label / id），不要求连续字符
//   * ↑↓ 选择，Enter 执行（关闭对话框 + 触发 action.run）
//   * Esc 关闭
//   * 动作分组渲染（操作 / 切换对话 / ...）
//
// 调用方传入 contextActions（依赖当前页面：clearError / 设置 sheet / 多选 等）
// + threads（用于"切换到 thread X"列表），dialog 自己合并 + 过滤。

import 'package:flutter/material.dart' hide showAdaptiveDialog;
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../../core/ui/adaptive_dialog.dart';
import '../../../../l10n/app_localizations.dart';
import '../../domain/palette_actions.dart';

Future<void> showCommandPaletteDialog(
  BuildContext context, {
  required List<PaletteAction> actions,
}) {
  return showAdaptiveDialog<void>(
    context: context,
    builder: (_) => _CommandPaletteDialog(actions: actions),
  );
}

class _CommandPaletteDialog extends ConsumerStatefulWidget {
  const _CommandPaletteDialog({required this.actions});
  final List<PaletteAction> actions;

  @override
  ConsumerState<_CommandPaletteDialog> createState() =>
      _CommandPaletteDialogState();
}

class _CommandPaletteDialogState extends ConsumerState<_CommandPaletteDialog> {
  final _ctrl = TextEditingController();
  final _focus = FocusNode();
  String _query = '';
  int _cursor = 0;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (mounted) _focus.requestFocus();
    });
  }

  @override
  void dispose() {
    _ctrl.dispose();
    _focus.dispose();
    super.dispose();
  }

  KeyEventResult _onKey(FocusNode node, KeyEvent event) {
    if (event is! KeyDownEvent) return KeyEventResult.ignored;
    final filtered = filterPaletteActions(widget.actions, _query);
    if (event.logicalKey == LogicalKeyboardKey.arrowUp) {
      if (filtered.isEmpty) return KeyEventResult.handled;
      setState(() {
        _cursor = (_cursor - 1).clamp(0, filtered.length - 1);
      });
      return KeyEventResult.handled;
    }
    if (event.logicalKey == LogicalKeyboardKey.arrowDown) {
      if (filtered.isEmpty) return KeyEventResult.handled;
      setState(() {
        _cursor = (_cursor + 1).clamp(0, filtered.length - 1);
      });
      return KeyEventResult.handled;
    }
    if (event.logicalKey == LogicalKeyboardKey.enter) {
      if (filtered.isEmpty) return KeyEventResult.handled;
      final pick = filtered[_cursor.clamp(0, filtered.length - 1)];
      Navigator.of(context).pop();
      pick.run();
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
    final theme = Theme.of(context);
    final l = AppLocalizations.of(context)!;
    final filtered = filterPaletteActions(widget.actions, _query);

    return AdaptiveDialogFrame(
      maxWidth: 560,
      maxHeight: 480,
      insetPadding: const EdgeInsets.symmetric(horizontal: 80, vertical: 100),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          // 输入框
          Container(
            padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
            decoration: BoxDecoration(
              border: Border(
                bottom: BorderSide(color: theme.colorScheme.outlineVariant),
              ),
            ),
            child: Row(
              children: [
                Icon(
                  Icons.bolt_outlined,
                  size: 18,
                  color: theme.colorScheme.onSurfaceVariant,
                ),
                const SizedBox(width: 10),
                Expanded(
                  child: Focus(
                    onKeyEvent: _onKey,
                    child: TextField(
                      controller: _ctrl,
                      focusNode: _focus,
                      decoration: InputDecoration(
                        hintText: l.chatV2PaletteSearchHint,
                        border: InputBorder.none,
                        isDense: true,
                        contentPadding: const EdgeInsets.symmetric(vertical: 6),
                      ),
                      style: theme.textTheme.bodyLarge,
                      onChanged: (v) => setState(() {
                        _query = v;
                        _cursor = 0;
                      }),
                    ),
                  ),
                ),
                IconButton(
                  icon: const Icon(Icons.close, size: 18),
                  tooltip: l.chatV2CrossSearchCloseTooltip,
                  onPressed: () => Navigator.of(context).pop(),
                ),
              ],
            ),
          ),
          // 列表
          Expanded(
            child: filtered.isEmpty
                ? Center(
                    child: Text(
                      l.chatV2PaletteNoMatch,
                      style: theme.textTheme.bodyMedium?.copyWith(
                        color: theme.colorScheme.onSurfaceVariant,
                      ),
                    ),
                  )
                : ListView.builder(
                    padding: const EdgeInsets.symmetric(vertical: 4),
                    itemCount: filtered.length,
                    itemBuilder: (_, i) => _buildRow(theme, filtered, i),
                  ),
          ),
        ],
      ),
    );
  }

  Widget _buildRow(ThemeData theme, List<PaletteAction> filtered, int i) {
    final a = filtered[i];
    final prev = i > 0 ? filtered[i - 1] : null;
    final showGroup =
        a.group != null && (prev == null || prev.group != a.group);
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        if (showGroup)
          Padding(
            padding: const EdgeInsets.fromLTRB(16, 8, 16, 4),
            child: Text(
              a.group!,
              style: theme.textTheme.labelSmall?.copyWith(
                color: theme.colorScheme.onSurfaceVariant,
                fontWeight: FontWeight.w600,
              ),
            ),
          ),
        InkWell(
          onTap: () {
            Navigator.of(context).pop();
            a.run();
          },
          child: Container(
            color: i == _cursor
                ? theme.colorScheme.primaryContainer.withValues(alpha: 0.4)
                : null,
            padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
            child: Row(
              children: [
                if (a.icon != null)
                  Icon(a.icon, size: 16, color: theme.colorScheme.primary),
                if (a.icon != null) const SizedBox(width: 12),
                Expanded(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text(
                        a.label,
                        style: theme.textTheme.bodyMedium?.copyWith(
                          fontWeight: FontWeight.w500,
                        ),
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                      ),
                      if (a.hint != null && a.hint!.isNotEmpty)
                        Text(
                          a.hint!,
                          style: theme.textTheme.labelSmall?.copyWith(
                            color: theme.colorScheme.onSurfaceVariant,
                          ),
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                        ),
                    ],
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

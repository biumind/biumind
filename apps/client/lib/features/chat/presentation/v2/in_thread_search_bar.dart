// InThreadSearchBarV2 —— Cmd+F 弹出的搜索栏，浮在 AppBar 下。
//
// 输入：threadId（决定 inThreadSearchProvider 的 family）。
// 行为：
//   * 输入即查（debounce 不必，本地 in-memory）
//   * Enter 跳下一条；Shift+Enter 上一条
//   * Esc 关闭并 reset
//   * 显示 "1/3" 计数

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../../l10n/app_localizations.dart';
import '../../application/in_thread_search_controller.dart';

class InThreadSearchBarV2 extends ConsumerStatefulWidget {
  const InThreadSearchBarV2({super.key, required this.threadId});
  final String threadId;

  @override
  ConsumerState<InThreadSearchBarV2> createState() =>
      _InThreadSearchBarV2State();
}

class _InThreadSearchBarV2State extends ConsumerState<InThreadSearchBarV2> {
  final _ctrl = TextEditingController();
  final _focus = FocusNode();

  @override
  void initState() {
    super.initState();
    // 打开 search bar 时自动 focus 让用户立刻能打字。
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
    final notifier =
        ref.read(inThreadSearchProvider(widget.threadId).notifier);
    if (event.logicalKey == LogicalKeyboardKey.enter) {
      if (HardwareKeyboard.instance.isShiftPressed) {
        notifier.prev();
      } else {
        notifier.next();
      }
      return KeyEventResult.handled;
    }
    if (event.logicalKey == LogicalKeyboardKey.escape) {
      notifier.close();
      return KeyEventResult.handled;
    }
    return KeyEventResult.ignored;
  }

  @override
  Widget build(BuildContext context) {
    final state = ref.watch(inThreadSearchProvider(widget.threadId));
    final notifier =
        ref.read(inThreadSearchProvider(widget.threadId).notifier);
    final theme = Theme.of(context);
    final l = AppLocalizations.of(context)!;
    final hits = state.hits;
    final counter = hits.isEmpty
        ? (state.query.isEmpty ? '' : '0/0')
        : '${state.currentIndex + 1}/${hits.length}';
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
      decoration: BoxDecoration(
        color: theme.colorScheme.surfaceContainerLow,
        border: Border(bottom: BorderSide(color: theme.colorScheme.outlineVariant)),
      ),
      child: Row(
        children: [
          Icon(Icons.search, size: 18, color: theme.colorScheme.onSurfaceVariant),
          const SizedBox(width: 8),
          Expanded(
            child: Focus(
              onKeyEvent: _onKey,
              child: TextField(
                controller: _ctrl,
                focusNode: _focus,
                decoration: InputDecoration(
                  hintText: l.chatV2InThreadSearchHint,
                  border: InputBorder.none,
                  isDense: true,
                  contentPadding: const EdgeInsets.symmetric(vertical: 6),
                ),
                onChanged: notifier.setQuery,
              ),
            ),
          ),
          if (counter.isNotEmpty)
            Padding(
              padding: const EdgeInsets.symmetric(horizontal: 8),
              child: Text(
                counter,
                style: theme.textTheme.labelSmall?.copyWith(
                  color: hits.isEmpty
                      ? theme.colorScheme.error
                      : theme.colorScheme.onSurfaceVariant,
                  fontFeatures: const [FontFeature.tabularFigures()],
                ),
              ),
            ),
          IconButton(
            icon: const Icon(Icons.keyboard_arrow_up, size: 18),
            tooltip: l.chatV2InThreadSearchPrev,
            visualDensity: VisualDensity.compact,
            onPressed: hits.isEmpty ? null : notifier.prev,
          ),
          IconButton(
            icon: const Icon(Icons.keyboard_arrow_down, size: 18),
            tooltip: l.chatV2InThreadSearchNext,
            visualDensity: VisualDensity.compact,
            onPressed: hits.isEmpty ? null : notifier.next,
          ),
          IconButton(
            icon: const Icon(Icons.close, size: 18),
            tooltip: l.chatV2CrossSearchCloseTooltip,
            visualDensity: VisualDensity.compact,
            onPressed: notifier.close,
          ),
        ],
      ),
    );
  }
}

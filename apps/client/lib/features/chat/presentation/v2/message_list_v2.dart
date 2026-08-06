// MessageListV2 —— Chat 重构 R4 + P0-3 多选包装。
//
// watch [messagesProvider]，反向 ListView（最新在底）+ 自动滚动到底。
// 空消息渲染占位提示；error / loading 走 AsyncValue.when。
//
// 多选模式：watch selectionModeProvider；active 时用 _SelectionWrapper 包裹
// 每条 bubble（leading checkbox + 整行点击切换选中 + 选中态左侧紫色 indicator）。

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../../app/theme/category_colors.dart';
import '../../../../core/layout/form_factor.dart';
import '../../application/chat_controller.dart';
import '../../application/in_thread_search_controller.dart';
import '../../application/pending_scroll_provider.dart';
import '../../application/selection_mode_controller.dart';
import '../../domain/chat_models.dart';
import 'chat_minimap.dart';
import 'empty_thread_view.dart';
import 'message_bubble_v2.dart';

class MessageListV2 extends ConsumerStatefulWidget {
  const MessageListV2({
    super.key,
    required this.threadId,
    this.modelHint,
    this.userName,
  });

  final String threadId;
  final String? modelHint;
  final String? userName;

  @override
  ConsumerState<MessageListV2> createState() => _MessageListV2State();
}

class _MessageListV2State extends ConsumerState<MessageListV2> {
  final _ctrl = ScrollController();

  /// 每条 bubble 的高度估算（GlobalKey + RenderBox.size.height），
  /// 给 ChatMiniMap 跳转用。新消息到来时增量收集。
  final _itemKeys = <int, GlobalKey>{};
  int _lastLen = 0;

  /// 用户是否已经滚离底部（>200px）—— 流式中决定是否跟着新内容自动 scroll。
  /// 同时用于 streaming 时显示"回到 AI 正在打字"按钮。
  bool _scrolledAway = false;

  @override
  void initState() {
    super.initState();
    _ctrl.addListener(_onScroll);
  }

  @override
  void dispose() {
    _ctrl.removeListener(_onScroll);
    _ctrl.dispose();
    super.dispose();
  }

  void _onScroll() {
    if (!_ctrl.hasClients) return;
    final near = _ctrl.position.maxScrollExtent - _ctrl.position.pixels < 200;
    if (near == _scrolledAway) {
      setState(() => _scrolledAway = !near);
    }
  }

  void _scheduleScrollToBottom() {
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!_ctrl.hasClients) return;
      _ctrl.animateTo(
        _ctrl.position.maxScrollExtent,
        duration: const Duration(milliseconds: 180),
        curve: Curves.easeOut,
      );
    });
  }

  /// MiniMap 点击时调。用 GlobalKey 拿真实位置后 scrollTo；找不到 key 时按
  /// index/total 的比例近似跳。
  void _scrollToIndex(int index) {
    final key = _itemKeys[index];
    final ctx = key?.currentContext;
    if (ctx != null) {
      Scrollable.ensureVisible(
        ctx,
        duration: const Duration(milliseconds: 220),
        alignment: 0.1,
        curve: Curves.easeOut,
      );
      return;
    }
    if (!_ctrl.hasClients) return;
    final total = _lastLen;
    if (total <= 0) return;
    final ratio = index / total;
    _ctrl.animateTo(
      _ctrl.position.maxScrollExtent * ratio,
      duration: const Duration(milliseconds: 220),
      curve: Curves.easeOut,
    );
  }

  @override
  Widget build(BuildContext context) {
    final async = ref.watch(messagesProvider(widget.threadId));
    final selMode = ref.watch(selectionModeProvider);
    final selecting = selMode.active && selMode.threadId == widget.threadId;

    // P0-补 in-thread 搜索：消息变化时同步给 search controller，让 hits 重算。
    ref.listen(messagesProvider(widget.threadId), (prev, next) {
      next.whenData((msgs) {
        ref
            .read(inThreadSearchProvider(widget.threadId).notifier)
            .setMessages(msgs);
        // 检查 pendingScroll —— 跨会话搜索切过来后，等消息流到位再跳。
        final pending = ref.read(pendingScrollProvider);
        if (pending != null && pending.threadId == widget.threadId) {
          final idx = msgs.indexWhere((m) => m.id == pending.messageId);
          if (idx >= 0) {
            WidgetsBinding.instance.addPostFrameCallback((_) {
              _scrollToIndex(idx);
              ref.read(pendingScrollProvider.notifier).consume();
            });
          }
        }
      });
    });
    // 同 thread 内连点同条搜索结果时，messagesProvider 不重发，listen 不触发。
    // 单独 listen pendingScrollProvider 兜底。
    ref.listen<PendingScroll?>(pendingScrollProvider, (prev, next) {
      if (next == null) return;
      if (next.threadId != widget.threadId) return;
      final msgs = ref.read(messagesProvider(widget.threadId)).valueOrNull;
      if (msgs == null) return;
      final idx = msgs.indexWhere((m) => m.id == next.messageId);
      if (idx < 0) return;
      WidgetsBinding.instance.addPostFrameCallback((_) {
        _scrollToIndex(idx);
        ref.read(pendingScrollProvider.notifier).consume();
      });
    });
    final search = ref.watch(inThreadSearchProvider(widget.threadId));
    // search.currentMessageId 改变时滚动到对应 bubble。
    ref.listen<InThreadSearchState>(inThreadSearchProvider(widget.threadId), (
      prev,
      next,
    ) {
      final id = next.currentMessageId;
      if (id == null) return;
      if (prev?.currentMessageId == id) return;
      final msgs = ref.read(messagesProvider(widget.threadId)).valueOrNull;
      if (msgs == null) return;
      final idx = msgs.indexWhere((m) => m.id == id);
      if (idx >= 0) {
        WidgetsBinding.instance.addPostFrameCallback(
          (_) => _scrollToIndex(idx),
        );
      }
    });

    final ctlAsync = ref.watch(chatControllerProvider(widget.threadId));
    final isStreaming = ctlAsync.value?.isStreaming ?? false;

    return async.when(
      data: (messages) {
        if (messages.length != _lastLen) {
          _lastLen = messages.length;
          // 流式中如果用户主动滚离底部 → 不抢用户焦点；非 streaming 状态
          // 维持原行为（每次新消息回底）。
          if (!selecting && !search.open && !_scrolledAway) {
            _scheduleScrollToBottom();
          }
        }
        if (messages.isEmpty) {
          return const EmptyThreadViewV2();
        }
        // 消息文字字号跟随全局字号(设置 > 外观 > 字体大小,经 theme TextTheme
        // 生效),不再有聊天专属 textScaler 叠加。
        return Stack(
          children: [
            ListView.builder(
              controller: _ctrl,
              padding: const EdgeInsets.symmetric(vertical: 12),
              itemCount: messages.length,
              itemBuilder: (ctx, i) {
                final m = messages[i];
                // tool_result 隐藏在 assistant block 内（由 BlockRenderer 处理），
                // 这里只渲染 user / assistant / system。
                if (m.role == MessageRole.toolResult) {
                  return const SizedBox.shrink();
                }
                final key = _itemKeys.putIfAbsent(i, () => GlobalKey());
                // FollowUp chips 只在最后一条 assistant + completed 消息下出。
                final isLastAssistant =
                    m.role == MessageRole.assistant &&
                    m.status == MessageStatus.completed &&
                    !messages
                        .skip(i + 1)
                        .any((later) => later.role == MessageRole.assistant);
                // 可见序号 = 数前面有几条非 toolResult 消息 + 1。
                // 隐藏 toolResult 不计数让 #N 跟用户看到的一致。
                final visibleIndex =
                    messages
                        .take(i)
                        .where((p) => p.role != MessageRole.toolResult)
                        .length +
                    1;
                Widget bubble = KeyedSubtree(
                  key: key,
                  child: MessageBubbleV2(
                    message: m,
                    threadId: widget.threadId,
                    modelHint: widget.modelHint,
                    userName: widget.userName,
                    isLastAssistant: isLastAssistant,
                    visibleIndex: visibleIndex,
                  ),
                );
                // P0-补：搜索命中高亮。current = 强黄；其他 hit = 浅黄。
                if (search.hits.isNotEmpty && search.messageHasHit(m.id)) {
                  final isCurrent = search.currentMessageId == m.id;
                  final tint = Theme.of(ctx).brightness == Brightness.dark
                      ? StarredColors.textOnHighlight
                      : StarredColors.highlight;
                  bubble = Container(
                    decoration: BoxDecoration(
                      color: tint.withValues(alpha: isCurrent ? 0.45 : 0.18),
                      border: Border(
                        left: BorderSide(
                          color: isCurrent
                              ? Theme.of(ctx).colorScheme.primary
                              : Colors.transparent,
                          width: 3,
                        ),
                      ),
                    ),
                    child: bubble,
                  );
                }
                if (!selecting) return bubble;
                return _SelectionWrapper(
                  messageId: m.id,
                  selected: selMode.contains(m.id),
                  onToggle: () =>
                      ref.read(selectionModeProvider.notifier).toggle(m.id),
                  child: bubble,
                );
              },
            ),
            // P1-9 ChatMiniMap —— 右侧细导览条，>6 条才出。手机端不渲染
            // (18px 浮条在窄屏价值低且遮挡文字 — 方案 §4.4)。
            if (!isPhoneLayout(context))
              Positioned(
                right: 4,
                top: 0,
                bottom: 0,
                child: ChatMiniMapV2(
                  messages: messages,
                  onJump: _scrollToIndex,
                ),
              ),
            // 流式中用户向上翻看历史时浮起"跟着 AI 打字"按钮。
            if (isStreaming && _scrolledAway)
              Positioned(
                left: 0,
                right: 0,
                bottom: 16,
                child: Center(
                  child: _FollowStreamingButton(
                    onTap: () {
                      _scrolledAway = false;
                      _scheduleScrollToBottom();
                    },
                  ),
                ),
              ),
          ],
        );
      },
      loading: () => const Center(child: CircularProgressIndicator()),
      error: (e, _) => Center(child: Text('加载失败: $e')),
    );
  }
}

/// 多选包装 —— 在原 bubble 外层加 leading checkbox + 整行点击切换 + 选中
/// 态左侧紫色 indicator + 浅紫底色。
class _SelectionWrapper extends StatelessWidget {
  const _SelectionWrapper({
    required this.messageId,
    required this.selected,
    required this.onToggle,
    required this.child,
  });

  final String messageId;
  final bool selected;
  final VoidCallback onToggle;
  final Widget child;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return InkWell(
      onTap: onToggle,
      child: Container(
        decoration: BoxDecoration(
          border: Border(
            left: BorderSide(
              color: selected ? theme.colorScheme.primary : Colors.transparent,
              width: 3,
            ),
          ),
          color: selected
              ? theme.colorScheme.primary.withValues(alpha: 0.04)
              : Colors.transparent,
        ),
        child: Row(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Padding(
              padding: const EdgeInsets.only(top: 14, left: 4),
              child: Checkbox(
                value: selected,
                onChanged: (_) => onToggle(),
                visualDensity: VisualDensity.compact,
                materialTapTargetSize: MaterialTapTargetSize.shrinkWrap,
              ),
            ),
            // bubble 内部本身有交互（hover bar / footer），多选模式下整行点击
            // 等价于 toggle，IgnorePointer 屏蔽掉冲突。
            Expanded(child: IgnorePointer(child: child)),
          ],
        ),
      ),
    );
  }
}

/// 流式中向上翻看历史时浮起的"跟着 AI 打字"按钮。点击 → 回底 + 自动跟流。
class _FollowStreamingButton extends StatelessWidget {
  const _FollowStreamingButton({required this.onTap});
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Material(
      color: theme.colorScheme.primaryContainer,
      borderRadius: BorderRadius.circular(999),
      elevation: 4,
      child: InkWell(
        onTap: onTap,
        borderRadius: BorderRadius.circular(999),
        child: Container(
          padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 8),
          child: Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              SizedBox(
                width: 12,
                height: 12,
                child: CircularProgressIndicator(
                  strokeWidth: 2,
                  valueColor: AlwaysStoppedAnimation<Color>(
                    theme.colorScheme.primary,
                  ),
                ),
              ),
              const SizedBox(width: 8),
              Text(
                '跟着 AI 打字',
                style: theme.textTheme.labelMedium?.copyWith(
                  color: theme.colorScheme.onPrimaryContainer,
                  fontWeight: FontWeight.w600,
                ),
              ),
              const SizedBox(width: 4),
              Icon(
                Icons.arrow_downward,
                size: 14,
                color: theme.colorScheme.onPrimaryContainer,
              ),
            ],
          ),
        ),
      ),
    );
  }
}

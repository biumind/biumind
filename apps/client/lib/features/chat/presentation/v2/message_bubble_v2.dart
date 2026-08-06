// MessageBubbleV2 —— Chat 重构 R4。
//
// 一条 [Message] 渲染：左 avatar + 右内容。assistant 显 model logo；
// user 显首字。内容是垂直堆叠的 [Block]。
//
// 故意不用气泡背景 —— 跟 ChatGPT / Claude 桌面端一致，纯纵向流。
//
// v2 footer 一次成型（docs/BiuMind-Chat-UI-Benchmark-Optimization.md）：
//   * assistant + completed 显示 AssistantFooterV2（10+ 个 action）
//   * 鼠标悬停 assistant 时右上角浮 HoverActionBarV2（3 个高频）

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../../core/layout/form_factor.dart';
import '../../../../core/ui/popup_position.dart';
import '../../application/chat_controller.dart';
import '../../domain/chat_models.dart';
import '../../domain/message_outline.dart';
import '../message_avatar.dart';
import 'block_renderer.dart';
import 'follow_up_chips.dart';
import 'message_actions_v2.dart';
import 'message_debug_sheet.dart';
import 'message_outline_view.dart';

class MessageBubbleV2 extends ConsumerStatefulWidget {
  const MessageBubbleV2({
    super.key,
    required this.message,
    required this.threadId,
    this.modelHint,
    this.userName,
    this.isLastAssistant = false,
    this.visibleIndex,
  });

  final Message message;
  final String threadId;
  /// assistant 头像用的 model id（通常从 thread.lastModel / message.metadata 来）
  final String? modelHint;
  /// user 头像首字符来源
  final String? userName;
  /// 当前 thread 末尾的 assistant + completed 消息时为 true，FollowUp chips
  /// 只在末尾消息下方出，避免每条都有 chip 的视觉噪音。
  final bool isLastAssistant;
  /// 1-based 可见序号（跳过 toolResult 等隐藏消息）；null 时不显。
  final int? visibleIndex;

  @override
  ConsumerState<MessageBubbleV2> createState() => _MessageBubbleV2State();
}

class _MessageBubbleV2State extends ConsumerState<MessageBubbleV2> {
  bool _hovered = false;
  bool _editing = false;

  @override
  Widget build(BuildContext context) {
    final m = widget.message;
    final isUser = m.role == MessageRole.user;
    final isAssistantCompleted =
        m.role == MessageRole.assistant && m.status == MessageStatus.completed;
    final theme = Theme.of(context);
    final avatarOnly = isUser
        ? MessageAvatar.user(name: widget.userName)
        : MessageAvatar.assistant(model: m.model ?? widget.modelHint);
    // 长按 avatar / serial：弹 popup menu 提供 debug / 删除 / 复制 ID
     // 等高级操作。不抢 hover 主路径，移动端长按习惯一致。
    final avatar = GestureDetector(
      onLongPressStart: (d) => _showLongPressMenu(context, m, d.globalPosition),
      behavior: HitTestBehavior.opaque,
      child: Tooltip(
        message: _formatAbsoluteTime(m.createdAt),
        waitDuration: const Duration(milliseconds: 400),
        preferBelow: false,
        child: widget.visibleIndex == null
            ? avatarOnly
            : Column(
                mainAxisSize: MainAxisSize.min,
                children: [
                  avatarOnly,
                  const SizedBox(height: 4),
                  Text(
                    '#${widget.visibleIndex}',
                    style: theme.textTheme.labelSmall?.copyWith(
                      color: theme.colorScheme.onSurfaceVariant,
                      fontFeatures: const [FontFeature.tabularFigures()],
                    ),
                  ),
                ],
              ),
      ),
    );

    // 大纲只对 assistant + completed + 含 ≥3 个 markdown heading 的消息出。
    final outline = isAssistantCompleted
        ? parseOutline(m.assembledText)
        : const <OutlineItem>[];

    final core = Padding(
      padding: const EdgeInsets.symmetric(vertical: 8, horizontal: 12),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          avatar,
          const SizedBox(width: 12),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                if (outline.isNotEmpty)
                  MessageOutlineView(items: outline),
                if (_editing) ...[
                  _InlineMessageEditor(
                    initialText: m.assembledText,
                    onSave: (text) => _saveEdit(m, text),
                    onCancel: () => setState(() => _editing = false),
                  ),
                  // 编辑态：非 TextBlock（tool_use / result / image）仍展示，
                  // 只把 text 部分换成内联编辑器。
                  for (final b in m.blocks)
                    if (b is! TextBlock) BlockRenderer(block: b, role: m.role),
                ] else if (m.blocks.isEmpty)
                  _StatusPlaceholder(status: m.status)
                else
                  for (final b in m.blocks)
                    BlockRenderer(block: b, role: m.role),
                if (m.status == MessageStatus.failed)
                  _ErrorTrailer(error: m.errorMessage),
                if (m.status == MessageStatus.cancelled)
                  const _CancelledTrailer(),
                if (isAssistantCompleted)
                  AssistantFooterV2(
                    message: m,
                    threadId: widget.threadId,
                  ),
                if (isAssistantCompleted && widget.isLastAssistant)
                  const FollowUpChipsV2(),
              ],
            ),
          ),
        ],
      ),
    );

    // 编辑态优先 —— 不浮 hover bar，聚焦内联编辑器（core 已渲染编辑分支）。
    if (_editing) return core;
    // assistant 已完成 或 user 消息：悬停浮 mini bar。user 走
    // UserMessageHoverBar（复制 / 编辑 / 重新生成 / 删除），与 assistant 对称。
    final canHover = isAssistantCompleted || isUser;
    if (!canHover) return core;
    return MouseRegion(
      onEnter: (_) => setState(() => _hovered = true),
      onExit: (_) => setState(() => _hovered = false),
      child: Stack(
        children: [
          core,
          Positioned(
            right: 16,
            top: 4,
            child: AnimatedOpacity(
              duration: const Duration(milliseconds: 150),
              opacity: _hovered ? 1.0 : 0.0,
              child: IgnorePointer(
                ignoring: !_hovered,
                child: isAssistantCompleted
                    ? HoverActionBarV2(
                        message: m,
                        threadId: widget.threadId,
                      )
                    : UserMessageHoverBar(
                        message: m,
                        threadId: widget.threadId,
                        onEdit: () => setState(() => _editing = true),
                      ),
              ),
            ),
          ),
        ],
      ),
    );
  }

  Future<void> _saveEdit(Message m, String newText) async {
    // 即时退出编辑态（不等落库）；后台 editMessageText 写完，watchMessages
    // stream 自动把新文本刷回 block 渲染。
    setState(() => _editing = false);
    final ctl = ref.read(chatControllerProvider(widget.threadId).notifier);
    await ctl.editMessageText(m.id, newText);
  }

  Future<void> _showLongPressMenu(
      BuildContext context, Message m, Offset pos) async {
    // R1.7: 手机长按 → bottom sheet (IM 标准, 对标微信/ChatGPT mobile;
    // popup 在触屏位置不准且小)。桌面维持 popup (定位精确)。sheet 加
    // 「复制」高频项 (footer 已有, 长按镜像方便单手)。
    if (!context.mounted) return;
    final phone = isPhoneLayout(context);
    final String? picked;
    if (phone) {
      picked = await _showLongPressSheet(context, m);
    } else {
      picked = await showMenu<String>(
            context: context,
            position: popupPositionAt(context, pos),
            items: [
              const PopupMenuItem(
                value: 'debug',
                child: Row(children: [
                  Icon(Icons.bug_report_outlined, size: 16),
                  SizedBox(width: 8),
                  Text('查看原始结构'),
                ]),
              ),
              const PopupMenuItem(
                value: 'copy-id',
                child: Row(children: [
                  Icon(Icons.tag, size: 16),
                  SizedBox(width: 8),
                  Text('复制消息 ID'),
                ]),
              ),
              const PopupMenuItem(
                value: 'star',
                child: Row(children: [
                  Icon(Icons.star_outline, size: 16),
                  SizedBox(width: 8),
                  Text('切换收藏'),
                ]),
              ),
              const PopupMenuDivider(),
              PopupMenuItem(
                value: 'delete',
                child: Row(children: [
                  Icon(Icons.delete_outline,
                      size: 16,
                      color: Theme.of(context).colorScheme.error),
                  const SizedBox(width: 8),
                  Text(
                    '删除此条',
                    style: TextStyle(
                      color: Theme.of(context).colorScheme.error,
                    ),
                  ),
                ]),
              ),
            ],
          );
    }
    if (!mounted || picked == null) return;
    final repo = ref.read(chatControllerDepsProvider).repo;
    switch (picked) {
      case 'copy':
        final text = m.assembledText;
        if (text.trim().isEmpty) return;
        await Clipboard.setData(ClipboardData(text: text));
        if (!context.mounted) return;
        ScaffoldMessenger.of(context).showSnackBar(const SnackBar(
          content: Text('已复制'),
          duration: Duration(seconds: 1),
        ));
      case 'debug':
        if (context.mounted) showMessageDebugSheet(context, m);
      case 'copy-id':
        await Clipboard.setData(ClipboardData(text: m.id));
        if (context.mounted) {
          ScaffoldMessenger.of(context).showSnackBar(const SnackBar(
            content: Text('已复制消息 ID'),
            duration: Duration(seconds: 1),
          ));
        }
      case 'star':
        await repo.toggleReaction(
            messageId: m.id, threadId: m.threadId, kind: 'star');
      case 'delete':
        if (context.mounted) await _confirmDelete(context, m);
    }
  }

  /// 手机长按消息的 bottom sheet (R1.7)。ListTile 大触摸目标, 对标微信 /
  /// ChatGPT mobile 长按菜单。返回选中项 id (null = 取消)。
  Future<String?> _showLongPressSheet(BuildContext context, Message m) {
    final error = Theme.of(context).colorScheme.error;
    return showModalBottomSheet<String>(
      context: context,
      showDragHandle: true,
      builder: (ctx) => SafeArea(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            ListTile(
              leading: const Icon(Icons.copy_outlined),
              title: const Text('复制'),
              enabled: m.assembledText.trim().isNotEmpty,
              onTap: () => Navigator.of(ctx).pop('copy'),
            ),
            ListTile(
              leading: const Icon(Icons.bug_report_outlined),
              title: const Text('查看原始结构'),
              onTap: () => Navigator.of(ctx).pop('debug'),
            ),
            ListTile(
              leading: const Icon(Icons.tag),
              title: const Text('复制消息 ID'),
              onTap: () => Navigator.of(ctx).pop('copy-id'),
            ),
            ListTile(
              leading: const Icon(Icons.star_outline),
              title: const Text('切换收藏'),
              onTap: () => Navigator.of(ctx).pop('star'),
            ),
            const Divider(height: 1),
            ListTile(
              leading: Icon(Icons.delete_outline, color: error),
              title: Text('删除此条', style: TextStyle(color: error)),
              onTap: () => Navigator.of(ctx).pop('delete'),
            ),
          ],
        ),
      ),
    );
  }

  Future<void> _confirmDelete(BuildContext context, Message m) async {
    final ok = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('删除消息'),
        content: const Text('删除该条消息？该操作不可恢复。'),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(ctx).pop(false),
            child: const Text('取消'),
          ),
          TextButton(
            style: TextButton.styleFrom(
              foregroundColor: Theme.of(ctx).colorScheme.error,
            ),
            onPressed: () => Navigator.of(ctx).pop(true),
            child: const Text('删除'),
          ),
        ],
      ),
    );
    if (ok != true) return;
    await ref
        .read(chatControllerProvider(widget.threadId).notifier)
        .deleteMessage(m.id);
  }
}

class _StatusPlaceholder extends StatelessWidget {
  const _StatusPlaceholder({required this.status});
  final MessageStatus status;

  @override
  Widget build(BuildContext context) {
    if (status == MessageStatus.streaming || status == MessageStatus.pending) {
      return const Padding(
        padding: EdgeInsets.symmetric(vertical: 4),
        child: SizedBox(
          height: 14, width: 14,
          child: CircularProgressIndicator(strokeWidth: 2),
        ),
      );
    }
    return const SizedBox.shrink();
  }
}

class _ErrorTrailer extends StatelessWidget {
  const _ErrorTrailer({this.error});
  final String? error;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Padding(
      padding: const EdgeInsets.only(top: 4),
      child: Text(
        error == null ? 'failed' : 'failed: $error',
        style: theme.textTheme.bodySmall?.copyWith(color: theme.colorScheme.error),
      ),
    );
  }
}

/// 用户主动 stop 后,brain 走完 clean-stop 路径(F5/d186112)回来的 message。
/// 区别于 _ErrorTrailer:这不是失败,是用户操作,所以用 onSurfaceVariant 灰
/// 而不是 colorScheme.error 红。
///
/// 实测延迟数据走 debug console (BiuSessionConnection._logCancelLatency 那
/// 边),UI 这层不展示具体毫秒数 — 持久化 latency 需要 Message schema 加列,
/// 性价比不高。
class _CancelledTrailer extends StatelessWidget {
  const _CancelledTrailer();

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Padding(
      padding: const EdgeInsets.only(top: 4),
      child: Row(
        children: [
          Icon(
            Icons.stop_circle_outlined,
            size: 14,
            color: theme.colorScheme.onSurfaceVariant,
          ),
          const SizedBox(width: 4),
          Text(
            '已停止',
            style: theme.textTheme.bodySmall?.copyWith(
              color: theme.colorScheme.onSurfaceVariant,
              fontStyle: FontStyle.italic,
            ),
          ),
        ],
      ),
    );
  }
}

/// "2026-06-01 09:32:14"，本地时区。Tooltip 用纯文本即可。
String _formatAbsoluteTime(DateTime t) {
  final l = t.toLocal();
  String two(int n) => n.toString().padLeft(2, '0');
  return '${l.year}-${two(l.month)}-${two(l.day)} '
      '${two(l.hour)}:${two(l.minute)}:${two(l.second)}';
}

/// 内联消息编辑器 —— user / assistant 通用。多行 TextField + 保存 / 取消。
/// 保存按钮在文本非空且相对 initial 有变化时才可点（避免空存 / 无效写）。
/// onSave 由 bubble 层接住调 ChatController.editMessageText 落库；onCancel 弃改。
class _InlineMessageEditor extends StatefulWidget {
  const _InlineMessageEditor({
    required this.initialText,
    required this.onSave,
    required this.onCancel,
  });
  final String initialText;
  final void Function(String) onSave;
  final VoidCallback onCancel;

  @override
  State<_InlineMessageEditor> createState() => _InlineMessageEditorState();
}

class _InlineMessageEditorState extends State<_InlineMessageEditor> {
  late final TextEditingController _controller;
  late final FocusNode _focus;

  @override
  void initState() {
    super.initState();
    _controller = TextEditingController(text: widget.initialText);
    _focus = FocusNode()..requestFocus();
  }

  @override
  void dispose() {
    _controller.dispose();
    _focus.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final trimmed = _controller.text.trim();
    final canSave =
        trimmed.isNotEmpty && trimmed != widget.initialText.trim();
    final border = BorderRadius.circular(8);
    final side = BorderSide(color: theme.colorScheme.primary.withValues(alpha: 0.5));
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      mainAxisSize: MainAxisSize.min,
      children: [
        Padding(
          padding: const EdgeInsets.symmetric(vertical: 4),
          child: TextField(
            controller: _controller,
            focusNode: _focus,
            maxLines: null,
            autofocus: true,
            style: theme.textTheme.bodyMedium,
            decoration: InputDecoration(
              isDense: true,
              filled: true,
              fillColor: theme.colorScheme.surface,
              contentPadding: const EdgeInsets.symmetric(
                horizontal: 12,
                vertical: 10,
              ),
              enabledBorder: OutlineInputBorder(borderRadius: border, borderSide: side),
              focusedBorder: OutlineInputBorder(
                borderRadius: border,
                borderSide: BorderSide(
                  color: theme.colorScheme.primary,
                  width: 1.5,
                ),
              ),
            ),
            onChanged: (_) => setState(() {}),
          ),
        ),
        Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            FilledButton.tonal(
              onPressed: canSave ? () => widget.onSave(_controller.text) : null,
              child: const Text('保存'),
            ),
            const SizedBox(width: 8),
            TextButton(
              onPressed: widget.onCancel,
              child: const Text('取消'),
            ),
          ],
        ),
      ],
    );
  }
}

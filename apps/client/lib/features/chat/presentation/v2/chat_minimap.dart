// ChatMiniMapV2 —— 长对话右侧消息导览条。
// 设计文档 docs/BiuMind-Chat-UI-Benchmark-Optimization.md P1-9。
//
// 每条消息一格：user 灰色、assistant 紫色、failed 红色。鼠标悬停显示
// "第 N 条 · 4h 前" tooltip；点击让父级 ListView 滚到对应 index。
// > 6 条才显示，避免短对话也占地方。

import 'package:flutter/material.dart';

import '../../../../core/ui/biu_glass.dart';
import '../../domain/chat_models.dart';

class ChatMiniMapV2 extends StatelessWidget {
  const ChatMiniMapV2({
    super.key,
    required this.messages,
    required this.onJump,
    this.minMessages = 6,
  });

  final List<Message> messages;
  /// 调用方传索引（messages 列表中的下标），父级负责 scrollToIndex。
  final ValueChanged<int> onJump;
  final int minMessages;

  @override
  Widget build(BuildContext context) {
    final visible = messages
        .where((m) => m.role != MessageRole.toolResult)
        .toList(growable: false);
    if (visible.length < minMessages) return const SizedBox.shrink();
    final theme = Theme.of(context);
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 24),
      child: SizedBox(
        width: 18,
        child: LayoutBuilder(
          builder: (ctx, c) {
            final h = c.maxHeight;
            // 每条消息平分高度，最大 14 像素，最小 3 像素。
            final per = (h / visible.length).clamp(3.0, 14.0);
            return Column(
              mainAxisAlignment: MainAxisAlignment.start,
              children: [
                for (var i = 0; i < visible.length; i++)
                  _Tick(
                    message: visible[i],
                    height: per - 1, // 留 1px 间隙
                    onTap: () {
                      // 找回到 messages 原列表的 index（含 toolResult 行）。
                      final origIdx = messages.indexWhere(
                        (m) => m.id == visible[i].id,
                      );
                      onJump(origIdx >= 0 ? origIdx : i);
                    },
                  ),
              ]
                  .map((w) => Padding(
                        padding: const EdgeInsets.only(bottom: 1),
                        child: w,
                      ))
                  .toList(),
            );
          },
        ),
      ),
    ).withDecoration(theme);
  }
}

extension on Padding {
  /// 整条 minimap 包一层玻璃磨砂(BiuGlassLight),让它在消息流上有"浮在
  /// 顶层"的层次感 — backdrop-filter saturate 1.6 + blur 16px。
  Widget withDecoration(ThemeData theme) {
    return BiuGlassLight(
      borderRadius: BorderRadius.circular(4),
      tintAlpha: 0.55,
      child: this,
    );
  }
}

class _Tick extends StatelessWidget {
  const _Tick({
    required this.message,
    required this.height,
    required this.onTap,
  });

  final Message message;
  final double height;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final color = switch (message.status) {
      MessageStatus.failed => theme.colorScheme.error,
      _ => switch (message.role) {
          MessageRole.assistant => theme.colorScheme.primary,
          MessageRole.user =>
            theme.colorScheme.onSurfaceVariant.withValues(alpha: 0.5),
          _ => theme.colorScheme.onSurfaceVariant.withValues(alpha: 0.3),
        },
    };
    final label = switch (message.role) {
      MessageRole.user => '👤 用户',
      MessageRole.assistant => '🤖 AI',
      MessageRole.system => '⚙️ 系统',
      MessageRole.toolResult => '🔧 工具',
    };
    final preview = message.assembledText.trim();
    final snippet = preview.isEmpty
        ? ''
        : '\n${preview.length > 80 ? '${preview.substring(0, 80)}…' : preview}';
    return Tooltip(
      message: '$label$snippet',
      waitDuration: const Duration(milliseconds: 200),
      child: GestureDetector(
        onTap: onTap,
        child: MouseRegion(
          cursor: SystemMouseCursors.click,
          child: Container(
            width: 6,
            height: height,
            margin: const EdgeInsets.symmetric(horizontal: 6),
            decoration: BoxDecoration(
              color: color,
              borderRadius: BorderRadius.circular(2),
            ),
          ),
        ),
      ),
    );
  }
}

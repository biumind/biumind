/// 项目内 chat 的三件视觉组件 —— 简化版。
///
/// 1. ThinkingBlock：解析 `<thinking>…</thinking>` 标签 → 收起的可展开
///    思考过程卡（流式时未闭合 → 自动展开；闭合 → 自动收起）。
/// 2. CitedReferences：从 message metadata['cited_pages'] 渲染一排
///    可点的 page chip → 跳 /wiki/p/:pid/pages/:pageId。
/// 3. MessageActions：消息长按 / 右键弹菜单（复制 / 重新生成 / 删除）。
///
/// 三个 widget 都接 ProjectChatPage._MessageBubble，让对话从"能用"升级
/// 为"好用"。
library;

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../../../../app/theme.dart';
import '../../../../core/layout/phone_nav.dart';
import '../../../../core/ui/popup_position.dart';

// ─── 1. ThinkingBlock ─────────────────────────────────────────────

class ThinkingSplit {
  const ThinkingSplit({
    required this.thinking,
    required this.answer,
    required this.isClosed,
  });
  final String thinking;
  final String answer;
  final bool isClosed;
}

final _thinkingRe = RegExp(r'<thinking>([\s\S]*?)</thinking>', multiLine: true);
final _openThinkingRe = RegExp(r'<thinking>([\s\S]*)$');

/// 解析消息文本，把 `<thinking>...</thinking>` 抽出来。
/// 流式时可能没闭合，把 open tag 之后所有内容当 thinking 提示用户思考中。
ThinkingSplit splitThinking(String src) {
  final closed = _thinkingRe.allMatches(src).toList();
  if (closed.isNotEmpty) {
    final thinking = closed.map((m) => m.group(1) ?? '').join('\n').trim();
    var answer = src.replaceAll(_thinkingRe, '').trim();
    return ThinkingSplit(thinking: thinking, answer: answer, isClosed: true);
  }
  final open = _openThinkingRe.firstMatch(src);
  if (open != null) {
    final thinking = (open.group(1) ?? '').trim();
    final answer = src.substring(0, open.start).trim();
    return ThinkingSplit(thinking: thinking, answer: answer, isClosed: false);
  }
  return ThinkingSplit(thinking: '', answer: src, isClosed: true);
}

class ThinkingBlock extends StatefulWidget {
  const ThinkingBlock({
    super.key,
    required this.text,
    required this.streaming,
  });
  final String text;
  final bool streaming;

  @override
  State<ThinkingBlock> createState() => _ThinkingBlockState();
}

class _ThinkingBlockState extends State<ThinkingBlock> {
  bool? _userExpanded;

  bool get _expanded => _userExpanded ?? widget.streaming;

  @override
  Widget build(BuildContext context) {
    return Container(
      margin: const EdgeInsets.only(bottom: 6),
      decoration: BoxDecoration(
        color: BiuTokens.surfaceMuted,
        borderRadius: BorderRadius.circular(8),
        border: Border.all(color: BiuTokens.borderSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          InkWell(
            onTap: () => setState(() => _userExpanded = !_expanded),
            borderRadius: BorderRadius.circular(8),
            child: Padding(
              padding:
                  const EdgeInsets.symmetric(horizontal: 10, vertical: 6),
              child: Row(
                children: [
                  Icon(
                    widget.streaming
                        ? Icons.psychology_alt_outlined
                        : Icons.psychology_outlined,
                    size: 14,
                    color: BiuTokens.textSecondary,
                  ),
                  const SizedBox(width: 6),
                  Text(
                    widget.streaming ? '思考中…' : '思考过程',
                    style: TextStyle(
                      color: BiuTokens.textSecondary,
                      fontSize: 11,
                      fontWeight: FontWeight.w500,
                    ),
                  ),
                  const Spacer(),
                  Icon(
                    _expanded ? Icons.expand_less : Icons.expand_more,
                    size: 14,
                    color: BiuTokens.textMuted,
                  ),
                ],
              ),
            ),
          ),
          if (_expanded)
            Container(
              width: double.infinity,
              padding: const EdgeInsets.fromLTRB(10, 0, 10, 8),
              child: Text(
                widget.text,
                style: TextStyle(
                  color: BiuTokens.textSecondary,
                  fontSize: 11,
                  height: 1.5,
                  fontStyle: FontStyle.italic,
                ),
              ),
            ),
        ],
      ),
    );
  }
}

// ─── 2. CitedReferences ───────────────────────────────────────────

/// 一条 metadata 中 cited_pages 的最简形态：
/// `[{"page_id": "...", "title": "..."}]`
class CitedPage {
  const CitedPage({required this.pageId, required this.title, this.snippet});
  final String pageId;
  final String title;
  final String? snippet;

  factory CitedPage.fromJson(Map<String, dynamic> j) => CitedPage(
        pageId: j['page_id']?.toString() ?? j['id']?.toString() ?? '',
        title: j['title']?.toString() ?? '',
        snippet: j['snippet'] as String?,
      );
}

List<CitedPage> citedPagesFromMetadata(Map<String, dynamic>? metadata) {
  if (metadata == null) return const [];
  final raw = metadata['cited_pages'];
  if (raw is! List) return const [];
  return raw
      .whereType<Map>()
      .map((m) => CitedPage.fromJson(m.cast()))
      .where((p) => p.pageId.isNotEmpty)
      .toList();
}

class CitedReferences extends StatelessWidget {
  const CitedReferences({
    super.key,
    required this.pages,
    required this.projectId,
  });
  final List<CitedPage> pages;
  final String projectId;

  @override
  Widget build(BuildContext context) {
    if (pages.isEmpty) return const SizedBox.shrink();
    return Padding(
      padding: const EdgeInsets.only(top: 6),
      child: Wrap(
        spacing: 6,
        runSpacing: 4,
        children: [
          for (final p in pages)
            InkWell(
              onTap: () => enterSubPage(
                  context, '/wiki/p/$projectId/pages/${p.pageId}'),
              borderRadius: BorderRadius.circular(4),
              child: Container(
                padding: const EdgeInsets.symmetric(
                  horizontal: 8,
                  vertical: 3,
                ),
                decoration: BoxDecoration(
                  color: BiuTokens.purpleLight,
                  border: Border.all(color: BiuTokens.purpleSoft),
                  borderRadius: BorderRadius.circular(4),
                ),
                child: Row(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    Icon(Icons.description_outlined,
                        size: 11, color: BiuTokens.purple),
                    const SizedBox(width: 4),
                    ConstrainedBox(
                      constraints: const BoxConstraints(maxWidth: 220),
                      child: Text(
                        p.title.isEmpty ? '(未命名)' : p.title,
                        style: TextStyle(
                          color: BiuTokens.purple,
                          fontSize: 11,
                          fontWeight: FontWeight.w500,
                        ),
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                      ),
                    ),
                  ],
                ),
              ),
            ),
        ],
      ),
    );
  }
}

// ─── 3. MessageActions（长按 / 右键弹菜单） ────────────────────────

/// 单条消息的可执行动作。null 表示不显示该项。
typedef MessageActionCallback = Future<void> Function();

class MessageActionsWrapper extends StatelessWidget {
  const MessageActionsWrapper({
    super.key,
    required this.child,
    required this.content,
    this.onRegenerate,
    this.onDelete,
  });

  final Widget child;
  final String content;
  final MessageActionCallback? onRegenerate;
  final MessageActionCallback? onDelete;

  @override
  Widget build(BuildContext context) {
    return GestureDetector(
      onLongPressStart: (details) => _show(context, details.globalPosition),
      onSecondaryTapDown: (details) => _show(context, details.globalPosition),
      child: child,
    );
  }

  Future<void> _show(BuildContext context, Offset position) async {
    final selected = await showMenu<String>(
      context: context,
      position: popupPositionAt(context, position),
      items: [
        const PopupMenuItem<String>(
          value: 'copy',
          child: Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              Icon(Icons.copy, size: 14),
              SizedBox(width: 8),
              Text('复制', style: TextStyle(fontSize: 13)),
            ],
          ),
        ),
        if (onRegenerate != null)
          const PopupMenuItem<String>(
            value: 'regenerate',
            child: Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                Icon(Icons.refresh, size: 14),
                SizedBox(width: 8),
                Text('重新生成', style: TextStyle(fontSize: 13)),
              ],
            ),
          ),
        if (onDelete != null)
          const PopupMenuItem<String>(
            value: 'delete',
            child: Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                Icon(Icons.delete_outline, size: 14, color: BiuTokens.error),
                SizedBox(width: 8),
                Text(
                  '删除',
                  style: TextStyle(fontSize: 13, color: BiuTokens.error),
                ),
              ],
            ),
          ),
      ],
    );
    if (selected == null || !context.mounted) return;
    switch (selected) {
      case 'copy':
        await Clipboard.setData(ClipboardData(text: content));
        if (!context.mounted) return;
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(
            content: Text('已复制'),
            duration: Duration(seconds: 1),
          ),
        );
      case 'regenerate':
        await onRegenerate?.call();
      case 'delete':
        await onDelete?.call();
    }
  }
}

// MessageOutlineView —— assistant 消息上方展开式大纲。
// 设计文档 docs/BiuMind-Chat-UI-Benchmark-Optimization.md（v2 大纲）。
//
// 行为：
//   * 默认折叠，header 显 "大纲 · N 条" + 三角；点开列出标题
//   * 标题前缀按 level 缩进（H1 顶格，H2 缩 12，H3 缩 24…）
//   * 点击标题暂时只 SnackBar 提示（v1 只做导览，跳转留待 ChatMarkdownView
//     给 heading 注 GlobalKey 之后再接）

import 'package:flutter/material.dart';

import '../../domain/message_outline.dart';

class MessageOutlineView extends StatefulWidget {
  const MessageOutlineView({super.key, required this.items});
  final List<OutlineItem> items;

  @override
  State<MessageOutlineView> createState() => _MessageOutlineViewState();
}

class _MessageOutlineViewState extends State<MessageOutlineView> {
  bool _open = false;

  @override
  Widget build(BuildContext context) {
    if (widget.items.isEmpty) return const SizedBox.shrink();
    final theme = Theme.of(context);
    return Padding(
      padding: const EdgeInsets.only(bottom: 8),
      child: Container(
        decoration: BoxDecoration(
          color: theme.colorScheme.surfaceContainerLow,
          border: Border.all(color: theme.colorScheme.outlineVariant),
          borderRadius: BorderRadius.circular(6),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            InkWell(
              onTap: () => setState(() => _open = !_open),
              borderRadius: BorderRadius.circular(6),
              child: Padding(
                padding:
                    const EdgeInsets.symmetric(horizontal: 10, vertical: 6),
                child: Row(
                  children: [
                    Icon(Icons.list_alt,
                        size: 14, color: theme.colorScheme.primary),
                    const SizedBox(width: 6),
                    Text(
                      '大纲 · ${widget.items.length} 条',
                      style: theme.textTheme.labelMedium?.copyWith(
                        fontWeight: FontWeight.w600,
                      ),
                    ),
                    const Spacer(),
                    Icon(
                      _open ? Icons.expand_less : Icons.expand_more,
                      size: 16,
                      color: theme.colorScheme.onSurfaceVariant,
                    ),
                  ],
                ),
              ),
            ),
            if (_open)
              Padding(
                padding: const EdgeInsets.fromLTRB(10, 0, 10, 8),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    for (final it in widget.items)
                      Padding(
                        padding: EdgeInsets.only(
                            left: ((it.level - 1) * 12).toDouble(),
                            top: 2,
                            bottom: 2),
                        child: Text(
                          it.title,
                          style: theme.textTheme.bodySmall?.copyWith(
                            color: it.level == 1
                                ? theme.colorScheme.onSurface
                                : theme.colorScheme.onSurfaceVariant,
                            fontWeight: it.level == 1
                                ? FontWeight.w600
                                : FontWeight.normal,
                          ),
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                        ),
                      ),
                  ],
                ),
              ),
          ],
        ),
      ),
    );
  }
}

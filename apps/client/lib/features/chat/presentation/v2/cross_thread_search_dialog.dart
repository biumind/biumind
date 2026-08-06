// CrossThreadSearchDialog —— 跨所有会话搜索消息。
// 设计文档 docs/BiuMind-Chat-UI-Benchmark-Optimization.md（v2 跨会话搜索）。
//
// 行为：
//   * 全屏 modal，顶部输入框 + 计数 + ESC 关
//   * 输入即查（debounce 200ms 避免每按一键炸 SQL）
//   * 结果列表按 thread title 分组，每条显示 role / 时间 / snippet（命中
//     位置前后 30 字）
//   * 点击结果 → 关 dialog + 调 onPick(threadId, messageId) 让 host 切到
//     对应 thread 并 ensureVisible 那条消息

import 'dart:async';

import 'package:flutter/material.dart' hide showAdaptiveDialog;
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../../core/ui/adaptive_dialog.dart';
import '../../../../l10n/app_localizations.dart';
import '../../application/chat_controller.dart';
import '../../domain/chat_models.dart';
import '../../domain/greeting.dart';

Future<void> showCrossThreadSearchDialog(
  BuildContext ctx, {
  required void Function(String threadId, String messageId) onPick,
}) {
  return showAdaptiveDialog<void>(
    context: ctx,
    builder: (_) => _CrossThreadSearchDialog(onPick: onPick),
  );
}

class _CrossThreadSearchDialog extends ConsumerStatefulWidget {
  const _CrossThreadSearchDialog({required this.onPick});
  final void Function(String threadId, String messageId) onPick;

  @override
  ConsumerState<_CrossThreadSearchDialog> createState() =>
      _CrossThreadSearchDialogState();
}

class _CrossThreadSearchDialogState
    extends ConsumerState<_CrossThreadSearchDialog> {
  final _ctrl = TextEditingController();
  final _focus = FocusNode();
  Timer? _debounce;
  String _query = '';
  List<MessageSearchHit> _hits = const [];
  bool _busy = false;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (mounted) _focus.requestFocus();
    });
  }

  @override
  void dispose() {
    _debounce?.cancel();
    _ctrl.dispose();
    _focus.dispose();
    super.dispose();
  }

  void _onChanged(String v) {
    final q = v.trim();
    setState(() => _query = q);
    _debounce?.cancel();
    if (q.isEmpty) {
      setState(() => _hits = const []);
      return;
    }
    _debounce = Timer(const Duration(milliseconds: 200), () => _run(q));
  }

  Future<void> _run(String q) async {
    if (q != _query) return;
    setState(() => _busy = true);
    try {
      final repo = ref.read(chatControllerDepsProvider).repo;
      final hits = await repo.searchMessages(query: q, limit: 80);
      if (!mounted || q != _query) return;
      setState(() {
        _hits = hits;
        _busy = false;
      });
    } catch (_) {
      if (mounted) setState(() => _busy = false);
    }
  }

  KeyEventResult _onKey(FocusNode node, KeyEvent event) {
    if (event is! KeyDownEvent) return KeyEventResult.ignored;
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
    return AdaptiveDialogFrame(
      maxWidth: 720,
      maxHeight: 600,
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
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
                  Icons.search,
                  size: 20,
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
                        hintText: l.chatV2CrossSearchHint,
                        border: InputBorder.none,
                        isDense: true,
                        contentPadding: const EdgeInsets.symmetric(vertical: 6),
                      ),
                      style: theme.textTheme.bodyLarge,
                      onChanged: _onChanged,
                    ),
                  ),
                ),
                if (_query.isNotEmpty)
                  Padding(
                    padding: const EdgeInsets.symmetric(horizontal: 8),
                    child: _busy
                        ? const SizedBox(
                            width: 14,
                            height: 14,
                            child: CircularProgressIndicator(strokeWidth: 2),
                          )
                        : Text(
                            l.chatV2CrossSearchHitCount(_hits.length),
                            style: theme.textTheme.labelSmall?.copyWith(
                              color: theme.colorScheme.onSurfaceVariant,
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
          Expanded(child: _buildBody(theme)),
        ],
      ),
    );
  }

  Widget _buildBody(ThemeData theme) {
    final l = AppLocalizations.of(context)!;
    if (_query.isEmpty) {
      return Center(
        child: Padding(
          padding: const EdgeInsets.all(24),
          child: Text(
            l.chatV2CrossSearchEmptyHint,
            textAlign: TextAlign.center,
            style: theme.textTheme.bodyMedium?.copyWith(
              color: theme.colorScheme.onSurfaceVariant,
            ),
          ),
        ),
      );
    }
    if (_hits.isEmpty && !_busy) {
      return Center(
        child: Text(
          l.chatV2CrossSearchNoMatch,
          style: theme.textTheme.bodyMedium?.copyWith(
            color: theme.colorScheme.onSurfaceVariant,
          ),
        ),
      );
    }
    // 按 threadId 分组，保持每组内 createdAt desc。
    final groups = <String, _Group>{};
    for (final h in _hits) {
      final g = groups.putIfAbsent(
        h.threadId,
        () => _Group(threadId: h.threadId, title: h.threadTitle, hits: []),
      );
      g.hits.add(h);
    }
    final entries = groups.values.toList(growable: false);
    return ListView.builder(
      itemCount: entries.length,
      padding: const EdgeInsets.symmetric(vertical: 8),
      itemBuilder: (_, gi) {
        final g = entries[gi];
        return Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Padding(
              padding: const EdgeInsets.fromLTRB(16, 12, 16, 4),
              child: Text(
                g.title.isEmpty ? l.chatV2NewThreadFallback : g.title,
                style: theme.textTheme.labelLarge?.copyWith(
                  color: theme.colorScheme.primary,
                  fontWeight: FontWeight.w600,
                ),
              ),
            ),
            for (final h in g.hits)
              InkWell(
                onTap: () {
                  Navigator.of(context).pop();
                  widget.onPick(h.threadId, h.messageId);
                },
                child: Padding(
                  padding: const EdgeInsets.symmetric(
                    horizontal: 16,
                    vertical: 8,
                  ),
                  child: Row(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Icon(
                        h.role == MessageRole.user
                            ? Icons.person_outline
                            : Icons.smart_toy_outlined,
                        size: 16,
                        color: theme.colorScheme.onSurfaceVariant,
                      ),
                      const SizedBox(width: 8),
                      Expanded(
                        child: Column(
                          crossAxisAlignment: CrossAxisAlignment.start,
                          children: [
                            _highlightedSnippet(h.snippet, _query, theme),
                            Padding(
                              padding: const EdgeInsets.only(top: 2),
                              child: Text(
                                relativeTime(h.createdAt),
                                style: theme.textTheme.labelSmall?.copyWith(
                                  color: theme.colorScheme.onSurfaceVariant,
                                ),
                              ),
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
      },
    );
  }

  Widget _highlightedSnippet(String snippet, String query, ThemeData theme) {
    if (query.isEmpty) {
      return Text(snippet, style: theme.textTheme.bodyMedium);
    }
    final q = query.toLowerCase();
    final lower = snippet.toLowerCase();
    final spans = <TextSpan>[];
    var i = 0;
    while (true) {
      final idx = lower.indexOf(q, i);
      if (idx < 0) {
        if (i < snippet.length) {
          spans.add(TextSpan(text: snippet.substring(i)));
        }
        break;
      }
      if (idx > i) {
        spans.add(TextSpan(text: snippet.substring(i, idx)));
      }
      spans.add(
        TextSpan(
          text: snippet.substring(idx, idx + q.length),
          style: TextStyle(
            backgroundColor: theme.colorScheme.primary.withValues(alpha: 0.25),
            fontWeight: FontWeight.w600,
          ),
        ),
      );
      i = idx + q.length;
    }
    return RichText(
      text: TextSpan(style: theme.textTheme.bodyMedium, children: spans),
      maxLines: 2,
      overflow: TextOverflow.ellipsis,
    );
  }
}

class _Group {
  _Group({required this.threadId, required this.title, required this.hits});
  final String threadId;
  final String title;
  final List<MessageSearchHit> hits;
}

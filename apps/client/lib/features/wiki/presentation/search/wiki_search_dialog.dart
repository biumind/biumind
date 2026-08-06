// WikiSearchDialog — modal full-text search across the active project.
//
// Loads all the project's blocks once on open (via repo.listBlocksFor
// Project, hits Drift only — no network), then runs the pure-Dart
// engine on every keystroke. Hits show page title + body snippet with
// matched ranges highlighted. Enter selects the first hit; ↑/↓ navigate;
// Esc closes.

import 'package:flutter/material.dart' hide showAdaptiveDialog;
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../../app/theme.dart';
import '../../../../core/ui/adaptive_dialog.dart';
import '../../../../data/wiki_providers.dart';
import '../../../../data/wiki_repository.dart';
import '../../application/wiki_controller.dart';
import '../../application/wiki_search.dart';

/// Opens the wiki search dialog. Returns when the user dismisses it
/// (either by selecting a hit, pressing Esc, or clicking the close
/// button). Selection navigates via [WikiController.selectPageById]
/// directly — the dialog itself doesn't expose its choice via the
/// dialog return value, since callers don't need to react.
Future<void> showWikiSearchDialog(
  BuildContext context, {
  required String projectId,
}) {
  // 宽屏 = showDialog(barrierColor black54, dismissible 默认 true) 与原来
  // 一致；手机 = bottom sheet (§4.5 showAdaptiveDialog)。
  return showAdaptiveDialog<void>(
    context: context,
    builder: (_) => WikiSearchDialog(projectId: projectId),
  );
}

class WikiSearchDialog extends ConsumerStatefulWidget {
  const WikiSearchDialog({super.key, required this.projectId});
  final String projectId;

  @override
  ConsumerState<WikiSearchDialog> createState() => _WikiSearchDialogState();
}

class _WikiSearchDialogState extends ConsumerState<WikiSearchDialog> {
  final _ctrl = TextEditingController();
  final _focus = FocusNode();
  List<RepoBlock> _blocks = const [];
  List<WikiSearchHit> _hits = const [];
  int _selected = 0;
  bool _loading = true;

  @override
  void initState() {
    super.initState();
    _loadCorpus();
  }

  @override
  void dispose() {
    _ctrl.dispose();
    _focus.dispose();
    super.dispose();
  }

  Future<void> _loadCorpus() async {
    final repo = ref.read(wikiRepositoryProvider);
    if (repo == null) {
      if (mounted) setState(() => _loading = false);
      return;
    }
    final blocks = await repo.listBlocksForProject(widget.projectId);
    if (!mounted) return;
    setState(() {
      _blocks = blocks;
      _loading = false;
    });
  }

  void _runSearch(String query) {
    final pages =
        ref.read(wikiControllerProvider).valueOrNull?.pages ?? const [];
    final hits = searchWiki(pages: pages, blocks: _blocks, query: query);
    setState(() {
      _hits = hits;
      _selected = 0;
    });
  }

  Future<void> _select(int i) async {
    if (i < 0 || i >= _hits.length) return;
    final pageId = _hits[i].pageId;
    Navigator.of(context).pop();
    await ref.read(wikiControllerProvider.notifier).selectPageById(pageId);
  }

  KeyEventResult _onKey(FocusNode node, KeyEvent event) {
    if (event is! KeyDownEvent) return KeyEventResult.ignored;
    if (event.logicalKey == LogicalKeyboardKey.escape) {
      Navigator.of(context).pop();
      return KeyEventResult.handled;
    }
    if (event.logicalKey == LogicalKeyboardKey.arrowDown) {
      if (_hits.isNotEmpty) {
        setState(() => _selected = (_selected + 1) % _hits.length);
      }
      return KeyEventResult.handled;
    }
    if (event.logicalKey == LogicalKeyboardKey.arrowUp) {
      if (_hits.isNotEmpty) {
        setState(
          () => _selected = (_selected - 1 + _hits.length) % _hits.length,
        );
      }
      return KeyEventResult.handled;
    }
    if (event.logicalKey == LogicalKeyboardKey.enter ||
        event.logicalKey == LogicalKeyboardKey.numpadEnter) {
      _select(_selected);
      return KeyEventResult.handled;
    }
    return KeyEventResult.ignored;
  }

  @override
  Widget build(BuildContext context) {
    // 宽屏: Dialog(insetPadding 80/80, radiusMd shape) + 640×520 —— 与迁移前
    // 逐像素一致 (frame 的 ConstrainedBox 与内层 SizedBox 同值); 手机: sheet
    // 内全宽, 高度上限屏高 85%。
    return AdaptiveDialogFrame(
      maxWidth: 640,
      maxHeight: 520,
      insetPadding: const EdgeInsets.symmetric(horizontal: 80, vertical: 80),
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(BiuTokens.radiusMd),
      ),
      child: Focus(
        autofocus: true,
        onKeyEvent: _onKey,
        child: SizedBox(
          width: 640,
          height: 520,
          child: Column(
            children: [
              _SearchBar(
                controller: _ctrl,
                focusNode: _focus,
                onChanged: _runSearch,
                onClose: () => Navigator.of(context).pop(),
              ),
              const Divider(height: 1),
              Expanded(child: _buildBody()),
            ],
          ),
        ),
      ),
    );
  }

  Widget _buildBody() {
    if (_loading) {
      return const Center(child: CircularProgressIndicator(strokeWidth: 2));
    }
    final query = _ctrl.text.trim();
    if (query.isEmpty) {
      return Center(
        child: Text(
          '搜索本项目的页面与块。\n回车跳转到第一条结果。',
          textAlign: TextAlign.center,
          style: TextStyle(color: BiuTokens.textMuted, fontSize: 12),
        ),
      );
    }
    if (_hits.isEmpty) {
      return Center(
        child: Text(
          '没有找到「$query」',
          style: TextStyle(color: BiuTokens.textMuted, fontSize: 12),
        ),
      );
    }
    return ListView.builder(
      itemCount: _hits.length,
      itemBuilder: (_, i) => _HitTile(
        hit: _hits[i],
        selected: i == _selected,
        onTap: () => _select(i),
        onHover: (hovering) {
          if (hovering) setState(() => _selected = i);
        },
      ),
    );
  }
}

class _SearchBar extends StatelessWidget {
  const _SearchBar({
    required this.controller,
    required this.focusNode,
    required this.onChanged,
    required this.onClose,
  });

  final TextEditingController controller;
  final FocusNode focusNode;
  final ValueChanged<String> onChanged;
  final VoidCallback onClose;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(12, 8, 8, 8),
      child: Row(
        children: [
          Icon(Icons.search, size: 18, color: BiuTokens.textMuted),
          const SizedBox(width: 8),
          Expanded(
            child: TextField(
              controller: controller,
              focusNode: focusNode,
              autofocus: true,
              decoration: const InputDecoration(
                hintText: '搜索页面与内容…',
                border: InputBorder.none,
                isDense: true,
              ),
              style: const TextStyle(fontSize: 14),
              onChanged: onChanged,
            ),
          ),
          IconButton(
            icon: const Icon(Icons.close, size: 16),
            tooltip: '关闭 (Esc)',
            onPressed: onClose,
            visualDensity: VisualDensity.compact,
          ),
        ],
      ),
    );
  }
}

class _HitTile extends StatelessWidget {
  const _HitTile({
    required this.hit,
    required this.selected,
    required this.onTap,
    required this.onHover,
  });

  final WikiSearchHit hit;
  final bool selected;
  final VoidCallback onTap;
  final ValueChanged<bool> onHover;

  @override
  Widget build(BuildContext context) {
    return MouseRegion(
      onEnter: (_) => onHover(true),
      child: InkWell(
        onTap: onTap,
        child: Container(
          padding: const EdgeInsets.fromLTRB(16, 8, 16, 8),
          decoration: BoxDecoration(
            color: selected
                ? BiuTokens.purple.withValues(alpha: 0.06)
                : Colors.transparent,
            border: Border(
              left: BorderSide(
                color: selected ? BiuTokens.purple : Colors.transparent,
                width: 2,
              ),
            ),
          ),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              _HighlightedText(
                text: hit.pageTitle.isEmpty ? '(未命名)' : hit.pageTitle,
                matches: hit.titleMatches,
                style: TextStyle(
                  fontSize: 13,
                  fontWeight: FontWeight.w600,
                  color: BiuTokens.text,
                ),
              ),
              if (hit.snippet.isNotEmpty) ...[
                const SizedBox(height: 2),
                _HighlightedText(
                  text: hit.snippet,
                  matches: hit.snippetMatches,
                  maxLines: 2,
                  style: TextStyle(
                    fontSize: 11,
                    height: 1.4,
                    color: BiuTokens.textMuted,
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

class _HighlightedText extends StatelessWidget {
  const _HighlightedText({
    required this.text,
    required this.matches,
    required this.style,
    this.maxLines,
  });

  final String text;
  final List<TextRange> matches;
  final TextStyle style;
  final int? maxLines;

  @override
  Widget build(BuildContext context) {
    if (matches.isEmpty) {
      return Text(
        text,
        style: style,
        maxLines: maxLines,
        overflow: maxLines != null ? TextOverflow.ellipsis : null,
      );
    }
    final highlight = style.copyWith(
      backgroundColor: BiuTokens.purple.withValues(alpha: 0.18),
      fontWeight: FontWeight.w700,
    );
    final spans = <TextSpan>[];
    var cursor = 0;
    final sorted = [...matches]..sort((a, b) => a.start.compareTo(b.start));
    for (final m in sorted) {
      if (m.start < cursor || m.end > text.length) continue;
      if (m.start > cursor) {
        spans.add(TextSpan(text: text.substring(cursor, m.start)));
      }
      spans.add(
        TextSpan(text: text.substring(m.start, m.end), style: highlight),
      );
      cursor = m.end;
    }
    if (cursor < text.length) {
      spans.add(TextSpan(text: text.substring(cursor)));
    }
    return Text.rich(
      TextSpan(style: style, children: spans),
      maxLines: maxLines,
      overflow: maxLines != null ? TextOverflow.ellipsis : null,
    );
  }
}

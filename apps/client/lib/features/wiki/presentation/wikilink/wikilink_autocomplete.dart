// WikilinkAutocomplete — wraps a TextField with an Overlay popup that
// suggests page titles when the user types `[[`.
//
// Behaviour:
//   - On every text change we run [detectOpenWikilink]. When it
//     returns non-null, the popup appears anchored under the field
//     listing pages whose title contains the in-progress query
//     (case-insensitive, prefix matches first).
//   - Up/Down/Enter navigate + accept; Escape dismisses.
//   - Tap or Enter inserts `[[Title]]` (replacing the in-progress
//     `[[query`) and dismisses.
//   - The popup auto-dismisses when the cursor leaves the open
//     wikilink (e.g. user types `]]`, types a newline, deletes the `[[`).
//
// The widget is a transparent wrapper — callers pass any TextField
// builder via [child] and the autocomplete watches the controller.

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../../../../app/theme.dart';
import 'wikilink_parser.dart';

typedef PageTitleProvider = Future<List<String>> Function();

class WikilinkAutocomplete extends StatefulWidget {
  const WikilinkAutocomplete({
    super.key,
    required this.controller,
    required this.focusNode,
    required this.titlesProvider,
    required this.child,
    this.maxSuggestions = 8,
  });

  final TextEditingController controller;
  final FocusNode focusNode;

  /// Lazy fetch of page titles. Called once on first `[[` so we don't
  /// pre-warm a list for fields that never use wikilinks.
  final PageTitleProvider titlesProvider;

  /// The TextField (or other input widget) the autocomplete attaches to.
  /// Must use the [controller] + [focusNode] passed here.
  final Widget child;

  final int maxSuggestions;

  @override
  State<WikilinkAutocomplete> createState() => _WikilinkAutocompleteState();
}

class _WikilinkAutocompleteState extends State<WikilinkAutocomplete> {
  OverlayEntry? _overlay;
  final LayerLink _link = LayerLink();
  List<String> _titles = const [];
  List<String> _filtered = const [];
  bool _titlesLoaded = false;
  int _selected = 0;
  OpenWikilink? _open;

  @override
  void initState() {
    super.initState();
    widget.controller.addListener(_onText);
    widget.focusNode.addListener(_onFocus);
  }

  @override
  void dispose() {
    widget.controller.removeListener(_onText);
    widget.focusNode.removeListener(_onFocus);
    _hideOverlay();
    super.dispose();
  }

  void _onFocus() {
    if (!widget.focusNode.hasFocus) {
      _hideOverlay();
    }
  }

  Future<void> _ensureTitles() async {
    if (_titlesLoaded) return;
    _titlesLoaded = true;
    try {
      final t = await widget.titlesProvider();
      if (!mounted) return;
      setState(() => _titles = t);
    } catch (_) {
      // Best-effort; keep _titles empty so we just don't suggest.
    }
  }

  void _onText() {
    final sel = widget.controller.selection;
    if (!sel.isValid || !sel.isCollapsed) {
      _hideOverlay();
      return;
    }
    final open = detectOpenWikilink(widget.controller.text, sel.baseOffset);
    if (open == null) {
      _hideOverlay();
      return;
    }
    _open = open;
    _ensureTitles();
    _filter(open.query);
    _showOverlay();
  }

  void _filter(String query) {
    final q = query.trim().toLowerCase();
    final scored = <_Scored>[];
    for (final t in _titles) {
      final lo = t.toLowerCase();
      if (q.isEmpty) {
        scored.add(_Scored(t, 1));
      } else if (lo == q) {
        scored.add(_Scored(t, 4));
      } else if (lo.startsWith(q)) {
        scored.add(_Scored(t, 3));
      } else if (lo.contains(q)) {
        scored.add(_Scored(t, 2));
      }
    }
    scored.sort((a, b) {
      final c = b.score.compareTo(a.score);
      if (c != 0) return c;
      return a.title.toLowerCase().compareTo(b.title.toLowerCase());
    });
    _filtered = [
      for (final s in scored.take(widget.maxSuggestions)) s.title,
    ];
    _selected = 0;
    _overlay?.markNeedsBuild();
  }

  void _showOverlay() {
    if (_overlay != null) {
      _overlay!.markNeedsBuild();
      return;
    }
    _overlay = OverlayEntry(builder: _buildOverlay);
    Overlay.of(context).insert(_overlay!);
  }

  void _hideOverlay() {
    _overlay?.remove();
    _overlay = null;
    _open = null;
  }

  Widget _buildOverlay(BuildContext _) {
    return Positioned(
      width: 280,
      child: CompositedTransformFollower(
        link: _link,
        showWhenUnlinked: false,
        offset: const Offset(0, 24),
        child: Material(
          elevation: 6,
          color: BiuTokens.surface,
          borderRadius: BorderRadius.circular(BiuTokens.radiusMd),
          child: ConstrainedBox(
            constraints: const BoxConstraints(maxHeight: 240),
            child: _filtered.isEmpty
                ? Padding(
                    padding: const EdgeInsets.all(BiuTokens.space3),
                    child: Text(
                      _titles.isEmpty ? '加载页面…' : '没有匹配页面',
                      style: TextStyle(
                          fontSize: 12, color: BiuTokens.textMuted),
                    ),
                  )
                : ListView.builder(
                    padding: const EdgeInsets.symmetric(vertical: 4),
                    shrinkWrap: true,
                    itemCount: _filtered.length,
                    itemBuilder: (_, i) {
                      final t = _filtered[i];
                      final selected = i == _selected;
                      return InkWell(
                        onTap: () => _accept(t),
                        child: Container(
                          padding: const EdgeInsets.symmetric(
                              horizontal: BiuTokens.space3,
                              vertical: BiuTokens.space2),
                          color: selected
                              ? BiuTokens.purpleSoft
                              : Colors.transparent,
                          child: Row(
                            children: [
                              Icon(Icons.article_outlined,
                                  size: 14, color: BiuTokens.purple),
                              const SizedBox(width: BiuTokens.space2),
                              Expanded(
                                child: Text(
                                  t,
                                  style: TextStyle(
                                    fontSize: 12,
                                    fontWeight: selected
                                        ? FontWeight.w600
                                        : FontWeight.w400,
                                    color: selected
                                        ? BiuTokens.purple
                                        : BiuTokens.text,
                                  ),
                                  maxLines: 1,
                                  overflow: TextOverflow.ellipsis,
                                ),
                              ),
                            ],
                          ),
                        ),
                      );
                    },
                  ),
          ),
        ),
      ),
    );
  }

  void _accept(String title) {
    final open = _open;
    if (open == null) return;
    final ctrl = widget.controller;
    // Replace `[[query` (without the closing) with `[[title]]`.
    final text = ctrl.text;
    final replaced = text.replaceRange(
      open.openIndex,
      open.cursor,
      '[[$title]]',
    );
    final newCursor = open.openIndex + '[[$title]]'.length;
    ctrl.value = TextEditingValue(
      text: replaced,
      selection: TextSelection.collapsed(offset: newCursor),
    );
    _hideOverlay();
  }

  KeyEventResult _onKey(FocusNode node, KeyEvent event) {
    if (_overlay == null) return KeyEventResult.ignored;
    if (event is! KeyDownEvent && event is! KeyRepeatEvent) {
      return KeyEventResult.ignored;
    }
    final key = event.logicalKey;
    if (key == LogicalKeyboardKey.arrowDown) {
      setState(() {
        _selected = (_selected + 1).clamp(0, _filtered.length - 1);
      });
      _overlay?.markNeedsBuild();
      return KeyEventResult.handled;
    }
    if (key == LogicalKeyboardKey.arrowUp) {
      setState(() {
        _selected = (_selected - 1).clamp(0, _filtered.length - 1);
      });
      _overlay?.markNeedsBuild();
      return KeyEventResult.handled;
    }
    if (key == LogicalKeyboardKey.enter || key == LogicalKeyboardKey.tab) {
      if (_selected >= 0 && _selected < _filtered.length) {
        _accept(_filtered[_selected]);
        return KeyEventResult.handled;
      }
    }
    if (key == LogicalKeyboardKey.escape) {
      _hideOverlay();
      return KeyEventResult.handled;
    }
    return KeyEventResult.ignored;
  }

  @override
  Widget build(BuildContext context) {
    return CompositedTransformTarget(
      link: _link,
      child: Focus(
        onKeyEvent: _onKey,
        child: widget.child,
      ),
    );
  }
}

class _Scored {
  final String title;
  final int score;
  const _Scored(this.title, this.score);
}

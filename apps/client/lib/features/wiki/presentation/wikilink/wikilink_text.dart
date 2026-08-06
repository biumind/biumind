// WikilinkText — read-only widget that renders Obsidian-style
// `[[target]]` / `[[target|alias]]` references as styled, clickable
// spans inside paragraph text.
//
// Used in non-editing contexts where blocks need to be presented
// as-rendered: search hit previews, hover cards, future "view-only"
// page mode. The editor uses [WikilinkController] instead so the
// brackets stay editable.

import 'package:flutter/gestures.dart';
import 'package:flutter/material.dart';

import '../../../../app/theme.dart';
import 'wikilink_parser.dart';

class WikilinkText extends StatefulWidget {
  const WikilinkText({
    super.key,
    required this.text,
    this.style,
    this.onTap,
  });

  final String text;

  /// Base style for non-link text. Wikilinks override [color] and
  /// [decoration]; everything else (font / size / weight) inherits.
  final TextStyle? style;

  /// Callback fired when the user taps a wikilink. The argument is
  /// the [Wikilink.target] (page name to navigate to). Null disables
  /// click handling — the link still highlights but is not interactive.
  final void Function(String target)? onTap;

  @override
  State<WikilinkText> createState() => _WikilinkTextState();
}

class _WikilinkTextState extends State<WikilinkText> {
  final _recognizers = <TapGestureRecognizer>[];

  @override
  void dispose() {
    for (final r in _recognizers) {
      r.dispose();
    }
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    for (final r in _recognizers) {
      r.dispose();
    }
    _recognizers.clear();

    final base = widget.style ?? DefaultTextStyle.of(context).style;
    final body = widget.text;
    final links = parseWikilinks(body);
    if (links.isEmpty) {
      return Text(body, style: base);
    }

    final linkStyle = base.copyWith(
      color: BiuTokens.purple,
      fontWeight: FontWeight.w600,
      decoration: TextDecoration.underline,
      decorationColor: BiuTokens.purple,
      decorationThickness: 0.8,
    );
    final spans = <InlineSpan>[];
    var cursor = 0;
    for (final l in links) {
      if (l.start > cursor) {
        spans.add(TextSpan(text: body.substring(cursor, l.start), style: base));
      }
      final tap = widget.onTap == null
          ? null
          : (TapGestureRecognizer()..onTap = () => widget.onTap!(l.target));
      if (tap != null) _recognizers.add(tap);
      spans.add(TextSpan(
        text: l.label,
        style: linkStyle,
        recognizer: tap,
      ));
      cursor = l.end;
    }
    if (cursor < body.length) {
      spans.add(TextSpan(text: body.substring(cursor), style: base));
    }
    return SelectableText.rich(
      TextSpan(style: base, children: spans),
    );
  }
}

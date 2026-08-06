// WikilinkController — TextEditingController that styles `[[wikilinks]]`
// inline as the user types.
//
// Style policy: the bracket characters get a muted color so the eye
// reads "this thing is a link" without needing to interpret the
// brackets themselves; the target / alias text gets the accent color.
// We deliberately don't make the link CLICKABLE inside the editor —
// click-to-navigate would conflict with cursor placement. Read-only
// rendering uses [WikilinkText] for that.

import 'package:flutter/material.dart';

import '../../../../app/theme.dart';
import 'wikilink_parser.dart';

class WikilinkController extends TextEditingController {
  WikilinkController({super.text});

  @override
  TextSpan buildTextSpan({
    required BuildContext context,
    TextStyle? style,
    required bool withComposing,
  }) {
    final body = text;
    if (body.isEmpty) {
      return TextSpan(text: body, style: style);
    }
    final links = parseWikilinks(body);
    if (links.isEmpty) {
      return TextSpan(text: body, style: style);
    }

    final base = style ?? const TextStyle();
    final bracket = base.copyWith(color: BiuTokens.textMuted);
    final accent = base.copyWith(
      color: BiuTokens.purple,
      fontWeight: FontWeight.w600,
    );

    final spans = <TextSpan>[];
    var cursor = 0;
    for (final l in links) {
      if (l.start > cursor) {
        spans.add(TextSpan(text: body.substring(cursor, l.start), style: base));
      }
      // [[
      spans.add(TextSpan(text: '[[', style: bracket));
      // target (and optional |alias)
      final inner = body.substring(l.start + 2, l.end - 2);
      spans.add(TextSpan(text: inner, style: accent));
      // ]]
      spans.add(TextSpan(text: ']]', style: bracket));
      cursor = l.end;
    }
    if (cursor < body.length) {
      spans.add(TextSpan(text: body.substring(cursor), style: base));
    }
    return TextSpan(style: base, children: spans);
  }
}

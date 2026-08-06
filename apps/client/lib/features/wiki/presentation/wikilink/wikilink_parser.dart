// Wikilink parser — Obsidian-style [[target]] and [[target|alias]].
//
// Ports the regex + alias semantics from llm_wiki/src/lib/wikilink-transform.ts
// (which itself ports the Obsidian convention). Used by:
//
//   - WikilinkController: live syntax-highlighting inside TextField
//   - WikilinkText: read-only RichText display
//   - WikilinkAutocomplete: detecting an unclosed `[[…` cursor context
//
// We deliberately don't strip code-fenced or inline-code regions here.
// The renderers do that themselves so they can choose their own
// rendering policy (e.g. the controller may want to highlight inside
// inline code, the read-only widget may not).

class Wikilink {
  /// Byte offset of the opening `[[` in the original text.
  final int start;

  /// Byte offset just past the closing `]]`.
  final int end;

  /// Page name being linked to. Whitespace-trimmed.
  final String target;

  /// Display label. Equal to [target] when no `|alias` was given.
  final String label;

  /// True when the original syntax was `[[target|alias]]`.
  final bool hasAlias;

  const Wikilink({
    required this.start,
    required this.end,
    required this.target,
    required this.label,
    required this.hasAlias,
  });
}

/// Matches `[[target]]` or `[[target|alias]]`. Same shape as the TS
/// regex — target rejects `]` `|` `\n`; alias rejects `]` `\n`.
final RegExp _wikilinkRe = RegExp(r'\[\[([^\]|\n]+)(?:\|([^\]\n]*))?\]\]');

/// Parses every wikilink reference in [text]. Returns them in order
/// of appearance.
List<Wikilink> parseWikilinks(String text) {
  if (!text.contains('[[')) return const [];
  final out = <Wikilink>[];
  for (final m in _wikilinkRe.allMatches(text)) {
    final rawTarget = m.group(1) ?? '';
    final rawAlias = m.group(2);
    final target = rawTarget.trim();
    if (target.isEmpty) continue;
    final hasAlias = rawAlias != null;
    final aliasTrim = (rawAlias ?? '').trim();
    final label = hasAlias && aliasTrim.isNotEmpty ? aliasTrim : target;
    out.add(Wikilink(
      start: m.start,
      end: m.end,
      target: target,
      label: label,
      hasAlias: hasAlias,
    ));
  }
  return out;
}

/// Result of [detectOpenWikilink] — describes a `[[partial` situation
/// where the user is typing a wikilink and the closing `]]` hasn't
/// arrived yet. Returns null when the cursor is NOT inside an open
/// wikilink.
class OpenWikilink {
  /// Index of the `[[` opener in [text].
  final int openIndex;

  /// Cursor position (== text length when at end of input).
  final int cursor;

  /// Substring between `[[` and the cursor — the user's in-progress
  /// query for the autocomplete suggestion list.
  final String query;

  const OpenWikilink({
    required this.openIndex,
    required this.cursor,
    required this.query,
  });
}

/// Detects whether the cursor in [text] at byte offset [cursor] is
/// inside an unclosed `[[…` sequence. Returns null when:
///   - no `[[` precedes the cursor on the current line
///   - a closing `]]` has already appeared between `[[` and the cursor
///   - the candidate query contains a newline (we don't span lines)
///   - the candidate query contains `]` or `[` (likely malformed)
///
/// We only look at the CURRENT line — wikilink targets don't span
/// newlines, and limiting the search keeps long documents fast.
OpenWikilink? detectOpenWikilink(String text, int cursor) {
  if (cursor <= 1 || cursor > text.length) return null;
  // Find the line containing the cursor.
  final lineStart = text.lastIndexOf('\n', cursor - 1) + 1;
  // Last `[[` on this line before cursor.
  final substring = text.substring(lineStart, cursor);
  final openRel = substring.lastIndexOf('[[');
  if (openRel < 0) return null;
  final openIdx = lineStart + openRel;
  // Anything past the `[[` to the cursor is the candidate query —
  // but only if no `]]` has closed it.
  final query = text.substring(openIdx + 2, cursor);
  if (query.contains(']') || query.contains('[') || query.contains('\n')) {
    return null;
  }
  return OpenWikilink(openIndex: openIdx, cursor: cursor, query: query);
}

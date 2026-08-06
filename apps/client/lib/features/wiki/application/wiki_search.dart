// wiki_search — pure-Dart in-project full-text search.
//
// Inputs: a project's pages (with titles) + every block in that
// project, plus a query string. Outputs: ranked WikiSearchHit list
// each carrying the page id, title-match ranges, and a body snippet
// with body-match ranges so the UI can highlight without re-scanning.
//
// Ranking is intentionally simple: tokenize the query by whitespace,
// case-fold both sides, score 10× for title token hits (+5 bonus when
// a token starts at offset 0) and 1× for body token hits (+2 when a
// token starts at offset 0 of any block). Multi-block pages take the
// best-scoring block as the snippet source.
//
// We don't do TF-IDF, BM25, or fuzzy matching. The dataset is one
// project's pages — usually <500 — and substring scoring is plenty.

import '../../../data/wiki_repository.dart';

/// Half-open `[start, end)` byte range within a string.
class TextRange {
  final int start;
  final int end;
  const TextRange(this.start, this.end);

  @override
  String toString() => 'TextRange($start, $end)';

  @override
  bool operator ==(Object other) =>
      other is TextRange && other.start == start && other.end == end;

  @override
  int get hashCode => Object.hash(start, end);
}

class WikiSearchHit {
  final String pageId;
  final String pageTitle;

  /// Token-match ranges into [pageTitle] (offsets are character-based,
  /// matching `pageTitle.toLowerCase()` since both sides are case-folded
  /// for the lookup).
  final List<TextRange> titleMatches;

  /// Excerpt of the best-matching block, prefixed/suffixed with `…`
  /// when truncated. Empty string when only the title matched.
  final String snippet;

  /// Token-match ranges into [snippet].
  final List<TextRange> snippetMatches;

  /// Higher = more relevant. Used only for sort order.
  final double score;

  const WikiSearchHit({
    required this.pageId,
    required this.pageTitle,
    required this.titleMatches,
    required this.snippet,
    required this.snippetMatches,
    required this.score,
  });
}

/// Splits [query] into lowercase tokens. Empty / whitespace-only
/// queries return an empty list, which [searchWiki] treats as "no hits".
List<String> tokenizeQuery(String query) {
  final trimmed = query.trim().toLowerCase();
  if (trimmed.isEmpty) return const [];
  return trimmed.split(RegExp(r'\s+'));
}

/// Searches the active project's pages + blocks for [query].
///
/// [blocks] must be the full list of non-deleted blocks scoped to the
/// same project as [pages] — typically loaded via the repository's
/// `listBlocksByProject`. The function ignores blocks whose pageId
/// isn't represented in [pages].
List<WikiSearchHit> searchWiki({
  required List<RepoPage> pages,
  required List<RepoBlock> blocks,
  required String query,
  int limit = 50,
}) {
  final tokens = tokenizeQuery(query);
  if (tokens.isEmpty) return const [];

  // Pre-bucket blocks by page id; preserves block order within a page
  // so the snippet-picker sees the document in flow.
  final byPage = <String, List<RepoBlock>>{};
  for (final b in blocks) {
    (byPage[b.pageId] ??= []).add(b);
  }

  final hits = <WikiSearchHit>[];
  for (final p in pages) {
    final titleMatches = _findMatches(p.title, tokens);
    double score = 0;
    for (final m in titleMatches) {
      score += 10;
      if (m.start == 0) score += 5;
    }

    // Walk this page's blocks to find the best body snippet.
    String snippet = '';
    List<TextRange> snippetMatches = const [];
    int bestBlockHits = 0;
    for (final b in byPage[p.id] ?? const <RepoBlock>[]) {
      final body = _blockToSearchText(b);
      if (body.isEmpty) continue;
      final m = _findMatches(body, tokens);
      if (m.isEmpty) continue;
      for (final r in m) {
        score += 1;
        if (r.start == 0) score += 2;
      }
      if (m.length > bestBlockHits) {
        bestBlockHits = m.length;
        final excerpt = _excerpt(body, m);
        snippet = excerpt.text;
        snippetMatches = excerpt.matches;
      }
    }

    if (titleMatches.isEmpty && bestBlockHits == 0) continue;

    hits.add(WikiSearchHit(
      pageId: p.id,
      pageTitle: p.title,
      titleMatches: titleMatches,
      snippet: snippet,
      snippetMatches: snippetMatches,
      score: score,
    ));
  }

  hits.sort((a, b) {
    final byScore = b.score.compareTo(a.score);
    if (byScore != 0) return byScore;
    return a.pageTitle.toLowerCase().compareTo(b.pageTitle.toLowerCase());
  });
  if (hits.length > limit) return hits.sublist(0, limit);
  return hits;
}

/// Lifts the searchable plain text out of a block's content jsonb.
/// Heading / text / code → `content.text`. List → items joined by `\n`.
/// Anything else falls through to a stringified `text` field.
String _blockToSearchText(RepoBlock b) {
  switch (b.type) {
    case 'list':
      final items = (b.content['items'] as List? ?? const [])
          .map((e) => e?.toString() ?? '')
          .where((s) => s.isNotEmpty)
          .toList();
      return items.join('\n');
    default:
      return b.content['text']?.toString() ?? '';
  }
}

/// Finds every occurrence of every token in [text] (case-insensitively).
/// Overlapping ranges are kept (e.g. tokens `"abc"` and `"bc"` both
/// match `"abcd"` — both ranges returned). Result sorted by start.
List<TextRange> _findMatches(String text, List<String> tokens) {
  if (text.isEmpty) return const [];
  final lower = text.toLowerCase();
  final out = <TextRange>[];
  for (final tok in tokens) {
    if (tok.isEmpty) continue;
    var from = 0;
    while (from <= lower.length - tok.length) {
      final i = lower.indexOf(tok, from);
      if (i < 0) break;
      out.add(TextRange(i, i + tok.length));
      from = i + 1; // overlap-aware
    }
  }
  out.sort((a, b) => a.start.compareTo(b.start));
  return out;
}

class _Excerpt {
  final String text;
  final List<TextRange> matches;
  _Excerpt(this.text, this.matches);
}

/// Builds a ~120-char window centered on the first match. Adds `…`
/// affixes when truncated and adjusts match ranges to be relative to
/// the resulting [text].
_Excerpt _excerpt(String body, List<TextRange> matches) {
  if (matches.isEmpty) return _Excerpt(body, const []);
  const beforeCtx = 40;
  const afterCtx = 80;
  final first = matches.first;
  final rawStart = (first.start - beforeCtx).clamp(0, body.length);
  final rawEnd = (first.end + afterCtx).clamp(0, body.length);
  final leadEllipsis = rawStart > 0;
  final trailEllipsis = rawEnd < body.length;
  final core = body.substring(rawStart, rawEnd);
  final out = StringBuffer();
  if (leadEllipsis) out.write('…');
  out.write(core);
  if (trailEllipsis) out.write('…');
  final shift = (leadEllipsis ? 1 : 0) - rawStart;
  final adjusted = <TextRange>[];
  for (final m in matches) {
    if (m.start < rawStart || m.end > rawEnd) continue;
    adjusted.add(TextRange(m.start + shift, m.end + shift));
  }
  return _Excerpt(out.toString(), adjusted);
}

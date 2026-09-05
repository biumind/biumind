// block_to_markdown — turn a Wiki page's RepoBlock list into a single
// markdown string suitable for GptMarkdown rendering, plus a heading
// outline for the right-side TOC panel.
//
// Block-type contract (matches store.BlocksToMarkdown on the Go side):
//   * heading — content['text'] + content['level'] (1..6, default 2)
//   * text    — content['text'] (multi-line paragraph)
//   * list    — content['items'] as List<String>
//   * code    — content['text'] + optional content['lang']
//   * table   — content['text'] holding the raw GFM table markdown
//   * other   — falls back to content['text'] as paragraph
//
// Wikilink rewriting:
//   `[[Page]]`        → `[Page](wiki://Page)`
//   `[[slug|alias]]`  → `[alias](wiki://slug)`
//
// We rewrite OUTSIDE code/mermaid blocks only so users can document the
// `[[…]]` syntax in code samples without it linkifying.

import '../../../../data/wiki_repository.dart';
import '../wikilink/wikilink_parser.dart';

/// Custom URL scheme used to mark links produced by wikilink rewriting.
/// WikiReaderView intercepts these in onLinkTap and routes via the
/// wiki controller instead of opening an external browser.
const String kWikiLinkScheme = 'wiki';

/// One heading entry — used to drive the right-side outline panel.
class WikiHeading {
  /// 1..6, clamped from content['level'].
  final int level;
  final String text;

  /// The block's id — outline taps scroll to the block with this id.
  final String blockId;

  const WikiHeading({
    required this.level,
    required this.text,
    required this.blockId,
  });
}

/// Concatenates [blocks] into one markdown body. Blank when the list is
/// empty. Trailing newline stripped so GptMarkdown doesn't render an
/// extra blank line below the last block.
String blocksToMarkdown(List<RepoBlock> blocks) {
  if (blocks.isEmpty) return '';
  final buf = StringBuffer();
  for (final b in blocks) {
    final chunk = _renderBlock(b);
    if (chunk.isEmpty) continue;
    buf.write(chunk);
    buf.write('\n\n');
  }
  // Strip trailing blank lines.
  return buf.toString().replaceAll(RegExp(r'\n+$'), '');
}

/// Pulls all heading blocks from [blocks] in order. Non-heading blocks
/// are skipped. Empty heading text is skipped (would produce a useless
/// outline row).
List<WikiHeading> extractHeadings(List<RepoBlock> blocks) {
  final out = <WikiHeading>[];
  for (final b in blocks) {
    if (b.type != 'heading') continue;
    final text = (b.content['text'] as String? ?? '').trim();
    if (text.isEmpty) continue;
    final lvl = (b.content['level'] as num?)?.toInt() ?? 2;
    out.add(WikiHeading(level: lvl.clamp(1, 6), text: text, blockId: b.id));
  }
  return out;
}

/// Rewrites Obsidian-style `[[Page]]` / `[[slug|alias]]` references in
/// [text] to standard markdown links using the [kWikiLinkScheme]. URL-
/// encodes the target so titles with spaces / Chinese / punctuation
/// survive the round-trip through GptMarkdown's link parser.
String rewriteWikilinksToMarkdownLinks(String text) {
  final links = parseWikilinks(text);
  if (links.isEmpty) return text;
  final buf = StringBuffer();
  var cursor = 0;
  for (final l in links) {
    if (l.start > cursor) buf.write(text.substring(cursor, l.start));
    buf
      ..write('[')
      ..write(_escapeMdLinkLabel(l.label))
      ..write('](')
      ..write(kWikiLinkScheme)
      ..write('://')
      ..write(Uri.encodeComponent(l.target))
      ..write(')');
    cursor = l.end;
  }
  if (cursor < text.length) buf.write(text.substring(cursor));
  return buf.toString();
}

/// Reverses [rewriteWikilinksToMarkdownLinks] for one URL — extracts
/// the original target. Returns null when [url] doesn't use the wiki
/// scheme.
String? wikiTargetFromUrl(String url) {
  final scheme = '$kWikiLinkScheme://';
  if (!url.startsWith(scheme)) return null;
  return Uri.decodeComponent(url.substring(scheme.length));
}

/// Whole-document variant of [rewriteWikilinksToMarkdownLinks] for
/// `pages.body_md`（reader 权威数据源）。逐行扫描，fenced code block
/// （``` / ~~~，至多 3 空格前导缩进）内部不重写 —— 与 blocksToMarkdown
/// 对 code block 原样保留的策略一致（代码示例里的 `[[…]]` 是字面量）。
/// 行内 code span 不豁免 —— 与原 per-block 实现对 text block 的行为
/// 相同，保持渲染行为一致。
String rewriteWikilinksInBodyMarkdown(String md) {
  if (!md.contains('[[')) return md;
  final lines = md.split('\n');
  final buf = StringBuffer();
  String? fenceChar; // '`' 或 '~'；null = 不在 fence 内
  var fenceLen = 0;
  for (var i = 0; i < lines.length; i++) {
    final line = lines[i];
    final fence = _fenceMarker(line);
    if (fenceChar == null) {
      if (fence != null) {
        fenceChar = fence.$1;
        fenceLen = fence.$2;
        buf.write(line);
      } else {
        buf.write(rewriteWikilinksToMarkdownLinks(line));
      }
    } else {
      buf.write(line);
      // GFM：closing fence 必须与 opening 同字符且长度 ≥ opening。
      if (fence != null && fence.$1 == fenceChar && fence.$2 >= fenceLen) {
        fenceChar = null;
        fenceLen = 0;
      }
    }
    if (i < lines.length - 1) buf.write('\n');
  }
  return buf.toString();
}

/// 匹配 GFM fenced code block 标记行（至多 3 空格缩进 + ≥3 个 ` 或 ~）。
/// 返回 (fence 字符, 长度)；非 fence 行返回 null。
(String, int)? _fenceMarker(String line) {
  final m = RegExp(r'^ {0,3}(`{3,}|~{3,})').firstMatch(line);
  if (m == null) return null;
  final run = m.group(1)!;
  return (run[0], run.length);
}

// ─── internal ────────────────────────────────────────────────────

String _renderBlock(RepoBlock b) {
  switch (b.type) {
    case 'heading':
      final raw = (b.content['text'] as String? ?? '').trim();
      if (raw.isEmpty) return '';
      final lvl = (b.content['level'] as num?)?.toInt() ?? 2;
      final hashes = '#' * lvl.clamp(1, 6);
      return '$hashes ${rewriteWikilinksToMarkdownLinks(raw)}';
    case 'list':
      final items = (b.content['items'] as List? ?? const [])
          .map((e) => e?.toString() ?? '')
          .where((s) => s.trim().isNotEmpty)
          .toList();
      if (items.isEmpty) return '';
      return items
          .map((i) => '- ${rewriteWikilinksToMarkdownLinks(i)}')
          .join('\n');
    case 'code':
      final src = (b.content['text'] as String? ?? '');
      if (src.isEmpty) return '';
      final lang = (b.content['lang'] as String? ?? '').trim();
      // Code content is preserved verbatim — wikilinks inside fences
      // are intentional literals (e.g. doc samples).
      return '```$lang\n$src\n```';
    case 'table':
      // Raw GFM table markdown (mdparse keeps it verbatim). Emitted
      // as-is so GptMarkdown renders a real table; wikilinks inside
      // cells still get rewritten so they stay tappable.
      final raw = (b.content['text'] as String? ?? '');
      if (raw.trim().isEmpty) return '';
      return rewriteWikilinksToMarkdownLinks(raw);
    default:
      // text + unknown types → paragraph rendering with wikilink rewrite.
      final raw = (b.content['text'] as String? ?? '');
      if (raw.trim().isEmpty) return '';
      return rewriteWikilinksToMarkdownLinks(raw);
  }
}

/// Escapes characters in a markdown link label that would otherwise
/// terminate the `[…]` early — `]` and `\`. We don't escape `[` because
/// GptMarkdown handles nested square brackets gracefully.
String _escapeMdLinkLabel(String s) {
  return s.replaceAll(r'\', r'\\').replaceAll(']', r'\]');
}

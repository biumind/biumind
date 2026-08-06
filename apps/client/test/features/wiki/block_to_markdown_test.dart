import 'package:biumind/data/wiki_repository.dart';
import 'package:biumind/features/wiki/presentation/reader/block_to_markdown.dart';
import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_test/flutter_test.dart';

RepoBlock _block({
  required String type,
  required Map<String, dynamic> content,
  String id = 'b1',
  double position = 1,
}) =>
    RepoBlock(
      id: id,
      pageId: 'p1',
      position: position,
      type: type,
      content: content,
      version: 1,
    );

void main() {
  group('blocksToMarkdown', () {
    test('empty list returns empty string', () {
      expect(blocksToMarkdown(const []), '');
    });

    test('heading uses level prefix', () {
      final md = blocksToMarkdown([
        _block(type: 'heading', content: {'text': 'Hello', 'level': 2}),
      ]);
      expect(md, '## Hello');
    });

    test('heading level clamps to 1..6', () {
      final md = blocksToMarkdown([
        _block(type: 'heading', content: {'text': 'X', 'level': 99}),
      ]);
      expect(md.startsWith('###### '), isTrue);
    });

    test('heading defaults to level 2 when missing', () {
      final md = blocksToMarkdown([
        _block(type: 'heading', content: {'text': 'X'}),
      ]);
      expect(md, '## X');
    });

    test('text passes through as paragraph', () {
      final md = blocksToMarkdown([
        _block(type: 'text', content: {'text': 'a paragraph'}),
      ]);
      expect(md, 'a paragraph');
    });

    test('list renders dash-prefixed items', () {
      final md = blocksToMarkdown([
        _block(type: 'list', content: {
          'items': ['one', 'two', 'three']
        }),
      ]);
      expect(md, '- one\n- two\n- three');
    });

    test('list drops blank items', () {
      final md = blocksToMarkdown([
        _block(type: 'list', content: {
          'items': ['one', '', '  ', 'two']
        }),
      ]);
      expect(md, '- one\n- two');
    });

    test('code preserves verbatim including [[brackets]]', () {
      final md = blocksToMarkdown([
        _block(type: 'code', content: {
          'text': 'x = [[Page]]',
          'lang': 'python',
        }),
      ]);
      expect(md, '```python\nx = [[Page]]\n```');
    });

    test('code without lang still emits fence', () {
      final md = blocksToMarkdown([
        _block(type: 'code', content: {'text': 'echo hi'}),
      ]);
      expect(md, '```\necho hi\n```');
    });

    test('blank-content blocks are dropped, not separated', () {
      final md = blocksToMarkdown([
        _block(type: 'heading', content: {'text': 'X', 'level': 1}, id: 'a'),
        _block(type: 'text', content: {'text': ''}, id: 'b'),
        _block(type: 'text', content: {'text': 'visible'}, id: 'c'),
      ]);
      expect(md, '# X\n\nvisible');
    });

    test('multiple blocks separated by blank line', () {
      final md = blocksToMarkdown([
        _block(type: 'heading', content: {'text': 'Title', 'level': 1}, id: 'a'),
        _block(type: 'text', content: {'text': 'body'}, id: 'b'),
      ]);
      expect(md, '# Title\n\nbody');
    });

    test('unknown type falls back to paragraph', () {
      final md = blocksToMarkdown([
        _block(type: 'callout', content: {'text': 'fyi'}),
      ]);
      expect(md, 'fyi');
    });
  });

  group('rewriteWikilinksToMarkdownLinks', () {
    test('simple [[Page]] → [Page](wiki://Page)', () {
      expect(
        rewriteWikilinksToMarkdownLinks('see [[Foo]] here'),
        'see [Foo](wiki://Foo) here',
      );
    });

    test('[[slug|alias]] uses alias as label, slug as target', () {
      expect(
        rewriteWikilinksToMarkdownLinks('go to [[my-page|My Page]]'),
        'go to [My Page](wiki://my-page)',
      );
    });

    test('URL-encodes target with spaces / chinese', () {
      expect(
        rewriteWikilinksToMarkdownLinks('[[页面 一]]'),
        '[页面 一](wiki://%E9%A1%B5%E9%9D%A2%20%E4%B8%80)',
      );
    });

    test('wikilinks inside heading body get rewritten', () {
      final md = blocksToMarkdown([
        _block(type: 'heading',
            content: {'text': 'see [[X]]', 'level': 2}),
      ]);
      expect(md, '## see [X](wiki://X)');
    });

    test('wikilinks inside list items get rewritten', () {
      final md = blocksToMarkdown([
        _block(type: 'list', content: {
          'items': ['link to [[A]]', 'plain']
        }),
      ]);
      expect(md, '- link to [A](wiki://A)\n- plain');
    });

    test('escapes backslash in alias label so markdown link is valid', () {
      // The parser allows `\` inside alias (its blacklist is `]\n`).
      // We escape it so the produced `[..](..)` still parses.
      expect(
        rewriteWikilinksToMarkdownLinks(r'[[slug|a\b]]'),
        r'[a\\b](wiki://slug)',
      );
    });

    test('multiple links in one string', () {
      expect(
        rewriteWikilinksToMarkdownLinks('[[A]] and [[B|c]]'),
        '[A](wiki://A) and [c](wiki://B)',
      );
    });

    test('no [[…]] returns input unchanged', () {
      expect(rewriteWikilinksToMarkdownLinks('plain text'), 'plain text');
    });
  });

  group('wikiTargetFromUrl', () {
    test('decodes simple wiki:// URL', () {
      expect(wikiTargetFromUrl('wiki://Foo'), 'Foo');
    });
    test('decodes encoded chars', () {
      expect(wikiTargetFromUrl('wiki://%E9%A1%B5%20A'), '页 A');
    });
    test('returns null for non-wiki URL', () {
      expect(wikiTargetFromUrl('https://example.com'), isNull);
      expect(wikiTargetFromUrl('mailto:x@y'), isNull);
    });
  });

  group('extractHeadings', () {
    test('only heading blocks surface', () {
      final hs = extractHeadings([
        _block(type: 'heading',
            content: {'text': 'A', 'level': 1}, id: 'h1'),
        _block(type: 'text', content: {'text': 'p'}, id: 't1'),
        _block(type: 'heading',
            content: {'text': 'B', 'level': 3}, id: 'h2'),
      ]);
      expect(hs, hasLength(2));
      expect(hs[0].level, 1);
      expect(hs[0].text, 'A');
      expect(hs[0].blockId, 'h1');
      expect(hs[1].level, 3);
      expect(hs[1].blockId, 'h2');
    });

    test('blank-text headings skipped', () {
      final hs = extractHeadings([
        _block(type: 'heading',
            content: {'text': '   ', 'level': 1}, id: 'h1'),
      ]);
      expect(hs, isEmpty);
    });

    test('level clamped to 1..6', () {
      final hs = extractHeadings([
        _block(type: 'heading',
            content: {'text': 'X', 'level': 99}, id: 'h1'),
        _block(type: 'heading',
            content: {'text': 'Y', 'level': -1}, id: 'h2'),
      ]);
      expect(hs[0].level, 6);
      expect(hs[1].level, 1);
    });
  });
}

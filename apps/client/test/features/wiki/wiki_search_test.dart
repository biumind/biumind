import 'package:biumind/data/wiki_repository.dart';
import 'package:biumind/features/wiki/application/wiki_search.dart';
import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_test/flutter_test.dart';

RepoPage _page({
  required String id,
  required String title,
  String projectId = 'proj1',
}) =>
    RepoPage(
      id: id,
      projectId: projectId,
      title: title,
      version: 1,
      updatedAt: DateTime.utc(2026, 1, 1),
    );

RepoBlock _block({
  required String pageId,
  required String type,
  required Map<String, dynamic> content,
  String? id,
  double position = 1,
}) =>
    RepoBlock(
      id: id ?? '$pageId-${position.toInt()}',
      pageId: pageId,
      position: position,
      type: type,
      content: content,
      version: 1,
    );

void main() {
  group('tokenizeQuery', () {
    test('lowercases and splits on whitespace', () {
      expect(tokenizeQuery('Hello World'), ['hello', 'world']);
    });
    test('collapses repeated whitespace', () {
      expect(tokenizeQuery('  a   b  '), ['a', 'b']);
    });
    test('empty / whitespace returns empty list', () {
      expect(tokenizeQuery(''), <String>[]);
      expect(tokenizeQuery('   '), <String>[]);
    });
  });

  group('searchWiki', () {
    test('empty query returns empty list', () {
      final hits = searchWiki(
        pages: [_page(id: 'p1', title: 'Hello')],
        blocks: const [],
        query: '',
      );
      expect(hits, isEmpty);
    });

    test('title match surfaces page', () {
      final hits = searchWiki(
        pages: [
          _page(id: 'p1', title: 'Transformer architecture'),
          _page(id: 'p2', title: 'Cooking recipes'),
        ],
        blocks: const [],
        query: 'transformer',
      );
      expect(hits, hasLength(1));
      expect(hits[0].pageId, 'p1');
      expect(hits[0].titleMatches, hasLength(1));
      expect(hits[0].titleMatches[0].start, 0);
      expect(hits[0].titleMatches[0].end, 11);
      expect(hits[0].snippet, '');
    });

    test('body match surfaces page with snippet', () {
      final hits = searchWiki(
        pages: [_page(id: 'p1', title: 'Notes')],
        blocks: [
          _block(
              pageId: 'p1',
              type: 'text',
              content: {'text': 'self-attention is the core mechanism'}),
        ],
        query: 'attention',
      );
      expect(hits, hasLength(1));
      expect(hits[0].snippet, contains('attention'));
      expect(hits[0].snippetMatches, hasLength(1));
      // snippet may be prefixed with "…" since match is mid-string
      // when context-padding doesn't cover offset 0.
    });

    test('list block items are searchable', () {
      final hits = searchWiki(
        pages: [_page(id: 'p1', title: 'Notes')],
        blocks: [
          _block(pageId: 'p1', type: 'list', content: {
            'items': ['apple', 'banana', 'cherry']
          }),
        ],
        query: 'banana',
      );
      expect(hits, hasLength(1));
      expect(hits[0].snippet, contains('banana'));
    });

    test('code block content searchable', () {
      final hits = searchWiki(
        pages: [_page(id: 'p1', title: 'Notes')],
        blocks: [
          _block(pageId: 'p1', type: 'code', content: {
            'text': 'def foo():\n    return 42',
            'lang': 'python',
          }),
        ],
        query: 'foo',
      );
      expect(hits, hasLength(1));
      expect(hits[0].snippet, contains('foo'));
    });

    test('title hit ranks higher than body hit', () {
      final hits = searchWiki(
        pages: [
          _page(id: 'p1', title: 'just a body match here'),
          _page(id: 'p2', title: 'apple title'),
        ],
        blocks: [
          _block(
              pageId: 'p1',
              type: 'text',
              content: {'text': 'mentions apple in body'}),
        ],
        query: 'apple',
      );
      expect(hits[0].pageId, 'p2'); // title match wins
      expect(hits[1].pageId, 'p1');
    });

    test('start-of-title bonus beats mid-title hit', () {
      final hits = searchWiki(
        pages: [
          _page(id: 'p1', title: 'something about apple'),
          _page(id: 'p2', title: 'apple varieties'),
        ],
        blocks: const [],
        query: 'apple',
      );
      expect(hits[0].pageId, 'p2');
    });

    test('chinese tokens match', () {
      final hits = searchWiki(
        pages: [_page(id: 'p1', title: '变压器架构')],
        blocks: const [],
        query: '变压器',
      );
      expect(hits, hasLength(1));
      expect(hits[0].titleMatches[0].start, 0);
      expect(hits[0].titleMatches[0].end, 3);
    });

    test('multiple tokens accumulate score (OR semantics)', () {
      final hits = searchWiki(
        pages: [
          _page(id: 'p1', title: 'red apple'), // both tokens
          _page(id: 'p2', title: 'apple'), // one token
          _page(id: 'p3', title: 'unrelated'), // none
        ],
        blocks: const [],
        query: 'red apple',
      );
      expect(hits, hasLength(2)); // p3 excluded
      expect(hits[0].pageId, 'p1'); // both tokens > one token
      expect(hits[1].pageId, 'p2');
    });

    test('case-insensitive matching', () {
      final hits = searchWiki(
        pages: [_page(id: 'p1', title: 'TransFormer')],
        blocks: const [],
        query: 'transformer',
      );
      expect(hits, hasLength(1));
    });

    test('snippet truncates with ellipsis when match is far in', () {
      final long = '${'lorem ipsum ' * 20}NEEDLE here ${'tail ' * 20}';
      final hits = searchWiki(
        pages: [_page(id: 'p1', title: 'doc')],
        blocks: [
          _block(pageId: 'p1', type: 'text', content: {'text': long}),
        ],
        query: 'needle',
      );
      expect(hits, hasLength(1));
      expect(hits[0].snippet, startsWith('…'));
      expect(hits[0].snippet, endsWith('…'));
      expect(hits[0].snippet, contains('NEEDLE'));
      // Match offset is into the snippet, not the original body.
      final m = hits[0].snippetMatches.first;
      expect(hits[0].snippet.substring(m.start, m.end).toLowerCase(),
          'needle');
    });

    test('overlapping token matches both surface', () {
      final hits = searchWiki(
        pages: [_page(id: 'p1', title: 'abcabc')],
        blocks: const [],
        query: 'abc',
      );
      expect(hits[0].titleMatches, hasLength(2));
    });

    test('limit caps results', () {
      final pages = [
        for (var i = 0; i < 30; i++) _page(id: 'p$i', title: 'apple $i'),
      ];
      final hits = searchWiki(
        pages: pages,
        blocks: const [],
        query: 'apple',
        limit: 5,
      );
      expect(hits, hasLength(5));
    });

    test('blocks for unknown page are ignored', () {
      final hits = searchWiki(
        pages: [_page(id: 'p1', title: 'Notes')],
        blocks: [
          _block(
              pageId: 'p999', // not in pages list
              type: 'text',
              content: {'text': 'apple'}),
        ],
        query: 'apple',
      );
      expect(hits, isEmpty);
    });

    test('best block snippet picked (most matches)', () {
      final hits = searchWiki(
        pages: [_page(id: 'p1', title: 'Notes')],
        blocks: [
          _block(
              pageId: 'p1',
              type: 'text',
              content: {'text': 'one apple here'},
              id: 'b1'),
          _block(
              pageId: 'p1',
              type: 'text',
              content: {'text': 'apple apple apple everywhere'},
              id: 'b2',
              position: 2),
        ],
        query: 'apple',
      );
      expect(hits[0].snippet, contains('apple apple apple'));
    });
  });
}

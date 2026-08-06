import 'package:biumind/features/wiki/presentation/wikilink/wikilink_parser.dart';
import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  group('parseWikilinks', () {
    test('parses [[target]]', () {
      final got = parseWikilinks('See [[Transformer]] for more.');
      expect(got, hasLength(1));
      expect(got[0].target, 'Transformer');
      expect(got[0].label, 'Transformer');
      expect(got[0].hasAlias, isFalse);
    });

    test('parses [[target|alias]]', () {
      final got = parseWikilinks('Compare [[rnn|RNN]] vs GRU.');
      expect(got, hasLength(1));
      expect(got[0].target, 'rnn');
      expect(got[0].label, 'RNN');
      expect(got[0].hasAlias, isTrue);
    });

    test('parses multiple wikilinks', () {
      final got = parseWikilinks('[[a]] and [[b|B]] and [[c]]');
      expect(got.map((w) => w.target).toList(), ['a', 'b', 'c']);
      expect(got.map((w) => w.label).toList(), ['a', 'B', 'c']);
    });

    test('rejects newline inside target', () {
      final got = parseWikilinks('[[bad\nname]]');
      expect(got, isEmpty);
    });

    test('rejects pipe inside target', () {
      // [[a|b]] is parsed as target=a, alias=b — pipe in TARGET (left
      // of pipe) is forbidden because the regex group [^\]|\n]+ excludes |.
      final got = parseWikilinks('[[ok|alias]]');
      expect(got, hasLength(1));
      expect(got[0].target, 'ok');
    });

    test('trims whitespace inside brackets', () {
      final got = parseWikilinks('[[  spaced  ]]');
      expect(got[0].target, 'spaced');
    });

    test('empty alias falls back to target', () {
      final got = parseWikilinks('[[Transformer|]]');
      expect(got[0].label, 'Transformer');
      expect(got[0].hasAlias, isTrue);
    });

    test('returns empty when no wikilinks', () {
      expect(parseWikilinks('plain text'), isEmpty);
      expect(parseWikilinks(''), isEmpty);
    });

    test('start/end offsets are correct', () {
      final t = 'aa [[X]] bb';
      final got = parseWikilinks(t);
      expect(got, hasLength(1));
      expect(t.substring(got[0].start, got[0].end), '[[X]]');
    });
  });

  group('detectOpenWikilink', () {
    test('detects [[ at end of line', () {
      final r = detectOpenWikilink('hello [[Tr', 10);
      expect(r, isNotNull);
      expect(r!.query, 'Tr');
      expect(r.openIndex, 6);
    });

    test('returns null when [[ is closed', () {
      final r = detectOpenWikilink('hello [[X]] more', 16);
      expect(r, isNull);
    });

    test('returns null when no [[ on line', () {
      final r = detectOpenWikilink('plain text', 5);
      expect(r, isNull);
    });

    test('does not span newlines', () {
      // [[ on line 1, cursor on line 2 — line 2 has no [[ so we report
      // null even though [[ exists earlier in the buffer.
      final r = detectOpenWikilink('[[ abandoned\nnew line', 18);
      expect(r, isNull);
    });

    test('rejects when query has [ or ]', () {
      // bracket inside query likely means a malformed wikilink; bail.
      final r = detectOpenWikilink('[[a]b', 5);
      expect(r, isNull);
    });

    test('cursor immediately after [[ gives empty query', () {
      final r = detectOpenWikilink('[[', 2);
      expect(r, isNotNull);
      expect(r!.query, '');
    });
  });
}

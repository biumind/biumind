// Interpolator unit tests — pin every filter against the templated
// strings shipped in the bundled apps' manifests. A regression here
// shows up as a row of garbage strings in the App Center, so the
// asserts here are deliberately tight.

import 'package:biumind/features/apps/domain/interpolator.dart';
import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  group('path resolution', () {
    test(r'renders simple $-substitution', () {
      final r = Interpolator({'item': {'title': 'Hello'}}).render(r'${item.title}');
      expect(r, 'Hello');
    });

    test('missing path renders empty', () {
      final r = Interpolator({'item': {}}).render(r'${item.missing}');
      expect(r, '');
    });

    test('nested missing path renders empty without throwing', () {
      final r = Interpolator({'item': {}}).render(r'${item.deep.missing.path}');
      expect(r, '');
    });

    test('mixes literal text and substitutions', () {
      final r = Interpolator({'item': {'unread': 3}})
          .render(r'${item.unread} 未读');
      expect(r, '3 未读');
    });

    test(r'escapes $$ prefix as literal', () {
      final r = Interpolator({}).render(r'$${item.title}');
      expect(r, r'${item.title}');
    });

    test(r'unterminated $-open leaks raw', () {
      final r = Interpolator({}).render(r'broken ${oops');
      // Unterminated → engine writes the rest literally; we just
      // assert the prefix survives intact.
      expect(r.startsWith('broken'), isTrue);
    });
  });

  group('filters', () {
    test('truncate clamps and adds ellipsis', () {
      final r = Interpolator({'item': {'body': 'abcdefghijk'}})
          .render(r'${item.body | truncate(5)}');
      expect(r, 'abcde…');
    });

    test('truncate is no-op when shorter than limit', () {
      final r = Interpolator({'item': {'body': 'short'}})
          .render(r'${item.body | truncate(20)}');
      expect(r, 'short');
    });

    test('escape_html escapes HTML metacharacters', () {
      final r = Interpolator({'item': {'body': '<b>"x"</b>'}})
          .render(r'${item.body | escape_html}');
      expect(r, '&lt;b&gt;&quot;x&quot;&lt;/b&gt;');
    });

    test('domain extracts host from URL', () {
      final r = Interpolator({'item': {'url': 'https://news.ycombinator.com/item?id=1'}})
          .render(r'${item.url | domain}');
      expect(r, 'news.ycombinator.com');
    });

    test('default substitutes when value is empty', () {
      final r = Interpolator({'item': {'title': ''}})
          .render(r"\${item.title | default('untitled')}".replaceAll(r'\$', r'$'));
      expect(r, 'untitled');
    });

    test('default passes through non-empty value', () {
      final r = Interpolator({'item': {'title': 'real'}})
          .render(r"\${item.title | default('untitled')}".replaceAll(r'\$', r'$'));
      expect(r, 'real');
    });

    test('date applies yyyy-MM-dd format', () {
      final r = Interpolator({'item': {'at': '2026-05-29T13:14:15Z'}})
          .render(r'${item.at | date(yyyy-MM-dd)}');
      // Local time may shift, but yyyy-MM-dd remains stable enough
      // for substring assertions.
      expect(r.length, 10);
      expect(r.contains('-'), isTrue);
    });

    test('relative_time produces deterministic Chinese label', () {
      final fixedNow = DateTime.utc(2026, 5, 29, 12, 0, 0);
      final fiveMinAgo = fixedNow.subtract(const Duration(minutes: 5));
      final r = Interpolator(
        {'item': {'at': fiveMinAgo.toIso8601String()}},
        now: () => fixedNow,
      ).render(r'${item.at | relative_time}');
      expect(r, '5 分钟前');
    });

    test('chained filters apply in order', () {
      final r = Interpolator({'item': {'body': 'hello world'}})
          .render(r'${item.body | truncate(5) | escape_html}');
      // truncate("hello world", 5) = "hello…", escape_html keeps it
      expect(r, 'hello…');
    });

    test('unknown filter is a passthrough', () {
      final r = Interpolator({'item': {'body': 'hi'}})
          .render(r'${item.body | nonsense}');
      expect(r, 'hi');
    });
  });

  group('typed values', () {
    test('numbers stringify cleanly', () {
      final r = Interpolator({'item': {'n': 42}}).render(r'count=${item.n}');
      expect(r, 'count=42');
    });

    test('booleans stringify as true/false', () {
      final r = Interpolator({'item': {'b': true}}).render(r'flag=${item.b}');
      expect(r, 'flag=true');
    });

    test('maps stringify as JSON', () {
      final r = Interpolator({'item': {'m': {'a': 1}}}).render(r'${item.m}');
      expect(r, '{"a":1}');
    });
  });
}

// WebSearchHint —— 联网搜索一次性开关单测。

import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:biumind/features/chat/application/web_search_provider.dart';

void main() {
  group('WebSearchHintNotifier', () {
    test('starts off', () {
      final n = WebSearchHintNotifier();
      expect(n.state, false);
    });

    test('toggle flips state', () {
      final n = WebSearchHintNotifier();
      n.toggle();
      expect(n.state, true);
      n.toggle();
      expect(n.state, false);
    });

    test('clear when off is no-op', () {
      final n = WebSearchHintNotifier();
      n.clear();
      expect(n.state, false);
    });

    test('clear when on resets', () {
      final n = WebSearchHintNotifier();
      n.toggle();
      n.clear();
      expect(n.state, false);
    });
  });

  group('applyWebSearchHint', () {
    test('disabled returns prompt unchanged', () {
      expect(applyWebSearchHint('hello', false), 'hello');
      expect(applyWebSearchHint('', false), '');
    });

    test('enabled prepends hint with double newline', () {
      final out = applyWebSearchHint('compare price', true);
      expect(out.contains('compare price'), true);
      expect(out.indexOf('compare price') > 0, true,
          reason: 'hint should come before prompt');
    });

    test('enabled with empty prompt returns just the hint', () {
      final out = applyWebSearchHint('', true);
      expect(out.isNotEmpty, true);
      expect(out.contains('网络'), true);
    });
  });
}

// A2UI parse + validate tests — pin the v2.0 subset.
//
// Renderer widget tests live in a2ui_renderer_test.dart so this file
// stays parse-layer-only (fast, runs without a Flutter binding).

import 'package:biumind/features/apps/domain/a2ui.dart';
import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  group('parse', () {
    test('parses container with children', () {
      final n = A2UINode.fromJson({
        'kind': 'column',
        'spacing': 8,
        'children': [
          {'kind': 'text', 'text': 'hi'},
          {'kind': 'button', 'label': 'go', 'on_click': {'action': 'do'}},
        ],
      });
      expect(n.kind, A2UIKind.column);
      expect(n.isContainer, isTrue);
      expect(n.children.length, 2);
      expect(n.children[0].kind, A2UIKind.text);
      expect(n.children[0].props['text'], 'hi');
    });

    test('accepts both kind and node aliases', () {
      final n = A2UINode.fromJson({'node': 'text', 'text': 'x'});
      expect(n.kind, A2UIKind.text);
    });

    test('hoists top-level keys into props', () {
      final n = A2UINode.fromJson({'kind': 'card', 'title': 't'});
      expect(n.props['title'], 't');
    });

    test('explicit props win over hoisted', () {
      final n = A2UINode.fromJson({
        'kind': 'card',
        'title': 'top-level',
        'props': {'title': 'explicit'},
      });
      expect(n.props['title'], 'explicit');
    });

    test('unknown kind survives as A2UIKind.unknown', () {
      final n = A2UINode.fromJson({'kind': 'hologram_5d'});
      expect(n.kind, A2UIKind.unknown);
      expect(n.rawKind, 'hologram_5d');
    });

    test('parse() accepts both Map and JSON string', () {
      final m = A2UINode.parse({'kind': 'text', 'text': 'a'});
      expect(m, isNotNull);
      final s = A2UINode.parse('{"kind":"text","text":"b"}');
      expect(s, isNotNull);
      expect(s!.props['text'], 'b');
    });

    test('parse(null) returns null', () {
      expect(A2UINode.parse(null), isNull);
    });
  });

  group('validate', () {
    A2UINode tree(int depth) {
      A2UINode build(int n) {
        if (n == 0) {
          return const A2UINode(kind: A2UIKind.text, rawKind: 'text');
        }
        return A2UINode(
          kind: A2UIKind.column,
          rawKind: 'column',
          children: [build(n - 1)],
        );
      }
      return build(depth);
    }

    test('shallow tree validates clean', () {
      final n = tree(3);
      final r = validateA2UI(n, const A2UIValidationConfig());
      expect(r.ok, isTrue);
      expect(r.maxDepth, 3);
    });

    test('depth above cap reports depth_exceeded', () {
      final n = tree(20);
      final r = validateA2UI(n, const A2UIValidationConfig(maxDepth: 8));
      expect(r.ok, isFalse);
      expect(r.isFatal, isTrue);
      expect(
        r.issues.where((i) => i.code == 'depth_exceeded'),
        isNotEmpty,
      );
    });

    test('node count above cap reports node_count_exceeded', () {
      final big = A2UINode(
        kind: A2UIKind.column,
        rawKind: 'column',
        children: List.generate(50, (_) => const A2UINode(
          kind: A2UIKind.text, rawKind: 'text',
        )),
      );
      final r = validateA2UI(big, const A2UIValidationConfig(maxNodes: 10));
      expect(r.isFatal, isTrue);
    });

    test('on_click.action must be in allowedActions', () {
      final n = A2UINode.fromJson({
        'kind': 'column',
        'children': [
          {'kind': 'button', 'label': 'go', 'on_click': {'action': 'do'}},
          {'kind': 'button', 'label': 'no', 'on_click': {'action': 'evil'}},
        ],
      });
      final r = validateA2UI(n,
          const A2UIValidationConfig(allowedActions: {'do'}));
      expect(r.ok, isFalse);
      expect(r.isFatal, isFalse);
      expect(
        r.issues.firstWhere((i) => i.code == 'unknown_action').message,
        contains('evil'),
      );
    });

    test('image src must use whitelisted scheme', () {
      final n = A2UINode.fromJson({
        'kind': 'image',
        'src': 'javascript:alert(1)',
      });
      final r = validateA2UI(n, const A2UIValidationConfig());
      expect(
        r.issues.firstWhere((i) => i.code == 'unsafe_image_scheme').message,
        contains('javascript'),
      );
    });

    test('https + cas image schemes pass', () {
      for (final src in ['https://x.com/a.png', 'cas://sha256:abc']) {
        final n = A2UINode.fromJson({'kind': 'image', 'src': src});
        final r = validateA2UI(n, const A2UIValidationConfig());
        expect(
          r.issues.where((i) => i.code == 'unsafe_image_scheme'),
          isEmpty,
          reason: 'src=$src should pass',
        );
      }
    });

    test('unknown kind reported but not fatal', () {
      final n = A2UINode.fromJson({'kind': 'mystery'});
      final r = validateA2UI(n, const A2UIValidationConfig());
      expect(r.isFatal, isFalse);
      expect(
        r.issues.firstWhere((i) => i.code == 'unknown_kind').message,
        contains('mystery'),
      );
    });

    test('empty allowedActions disables action whitelist (dev mode)', () {
      final n = A2UINode.fromJson({
        'kind': 'button', 'label': 'go', 'on_click': {'action': 'anything'},
      });
      final r = validateA2UI(n, const A2UIValidationConfig());
      expect(
        r.issues.where((i) => i.code == 'unknown_action'),
        isEmpty,
      );
    });
  });
}

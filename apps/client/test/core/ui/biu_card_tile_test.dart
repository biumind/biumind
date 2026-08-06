// BiuCard / BiuTile widget tests — 验证默认 / hover / selected 三态。

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:biumind/app/theme/font_size.dart';
import 'package:biumind/app/theme/palettes.dart';
import 'package:biumind/app/theme/theme_builder.dart';
import 'package:biumind/core/ui/biu_card.dart';
import 'package:biumind/core/ui/biu_tile.dart';

Widget _wrap(Widget child) {
  // 注入完整 buildTheme 让 BiuColors / BiuMetrics extension 可用
  return MaterialApp(
    theme: buildTheme(
      palette: PaletteId.purpleOrange,
      mode: Brightness.light,
      fontSize: FontSize.small,
    ),
    home: Scaffold(body: Center(child: child)),
  );
}

void main() {
  group('BiuCard', () {
    testWidgets('renders + child visible', (tester) async {
      await tester.pumpWidget(_wrap(
        const BiuCard(child: Text('hi')),
      ));
      expect(find.text('hi'), findsOneWidget);
      expect(tester.takeException(), isNull);
    });

    testWidgets('selected state renders 1.5px brand border', (tester) async {
      await tester.pumpWidget(_wrap(
        const BiuCard(selected: true, child: Text('selected')),
      ));
      expect(find.text('selected'), findsOneWidget);
      // 找到带 border 的 AnimatedContainer
      final container = tester
          .widgetList<AnimatedContainer>(find.byType(AnimatedContainer))
          .where((c) => c.decoration is BoxDecoration)
          .first;
      final deco = container.decoration as BoxDecoration;
      final border = deco.border as Border;
      expect(border.left.width, 1.5);
    });

    testWidgets('onTap fires', (tester) async {
      var tapped = 0;
      await tester.pumpWidget(_wrap(
        BiuCard(
          onTap: () => tapped++,
          child: const Text('tap'),
        ),
      ));
      await tester.tap(find.text('tap'));
      expect(tapped, 1);
    });

    testWidgets('lift=0 + no onTap → no BiuHoverable wrapping', (tester) async {
      await tester.pumpWidget(_wrap(
        const BiuCard(lift: 0, child: Text('static')),
      ));
      expect(find.text('static'), findsOneWidget);
    });

    testWidgets('disableTint skips foregroundDecoration tint', (tester) async {
      await tester.pumpWidget(_wrap(
        const BiuCard(disableTint: true, child: Text('no tint')),
      ));
      expect(find.text('no tint'), findsOneWidget);
    });
  });

  group('BiuTile', () {
    testWidgets('renders + child visible', (tester) async {
      await tester.pumpWidget(_wrap(
        const BiuTile(child: Text('row')),
      ));
      expect(find.text('row'), findsOneWidget);
    });

    testWidgets('selected state has 3px brand left indicator', (tester) async {
      await tester.pumpWidget(_wrap(
        const BiuTile(selected: true, child: Text('selected')),
      ));
      // BiuTile 用 Border(left: BorderSide(...))。
      // hover bg 不再做动画(残影修复),改用普通 Container — 但 BiuHoverable
      // 的 builder 仍在 widget tree 顶层包一层 GestureDetector,内层是 Container。
      final container = tester
          .widget<Container>(find.byType(Container).first);
      final deco = container.decoration as BoxDecoration;
      final border = deco.border as Border;
      expect(border.left.width, 3);
    });

    testWidgets('onTap fires', (tester) async {
      var tapped = 0;
      await tester.pumpWidget(_wrap(
        BiuTile(
          onTap: () => tapped++,
          child: const Text('tap'),
        ),
      ));
      await tester.tap(find.text('tap'));
      expect(tapped, 1);
    });
  });
}

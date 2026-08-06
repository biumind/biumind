// BiuTextField + BiuChip widget tests。

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:biumind/app/theme/font_size.dart';
import 'package:biumind/app/theme/palettes.dart';
import 'package:biumind/app/theme/theme_builder.dart';
import 'package:biumind/core/ui/biu_chip.dart';
import 'package:biumind/core/ui/biu_text_field.dart';

Widget _wrap(Widget child) {
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
  group('BiuTextField', () {
    testWidgets('renders TextField with hint', (tester) async {
      await tester.pumpWidget(_wrap(
        const BiuTextField(hintText: '搜索…'),
      ));
      expect(find.byType(TextField), findsOneWidget);
      expect(find.text('搜索…'), findsOneWidget);
    });

    testWidgets('focused state adds halo box-shadow', (tester) async {
      final node = FocusNode();
      await tester.pumpWidget(_wrap(
        BiuTextField(focusNode: node, hintText: 'x'),
      ));
      // 默认无 halo
      AnimatedContainer ac = tester
          .widget<AnimatedContainer>(find.byType(AnimatedContainer).first);
      expect((ac.decoration as BoxDecoration).boxShadow, isNull);
      // 拿焦点
      node.requestFocus();
      await tester.pumpAndSettle();
      ac = tester
          .widget<AnimatedContainer>(find.byType(AnimatedContainer).first);
      final shadow = (ac.decoration as BoxDecoration).boxShadow;
      expect(shadow, isNotNull);
      expect(shadow!.first.spreadRadius, 3);
      node.dispose();
    });

    testWidgets('onChanged fires', (tester) async {
      var v = '';
      await tester.pumpWidget(_wrap(
        BiuTextField(onChanged: (s) => v = s),
      ));
      await tester.enterText(find.byType(TextField), 'hi');
      expect(v, 'hi');
    });
  });

  group('BiuChip', () {
    testWidgets('renders label + leading', (tester) async {
      await tester.pumpWidget(_wrap(
        const BiuChip(
          label: Text('chip'),
          leading: Icon(Icons.star),
        ),
      ));
      expect(find.text('chip'), findsOneWidget);
      expect(find.byIcon(Icons.star), findsOneWidget);
    });

    testWidgets('selected → 1.5px brand border', (tester) async {
      await tester.pumpWidget(_wrap(
        const BiuChip(selected: true, label: Text('on')),
      ));
      final ac = tester
          .widget<AnimatedContainer>(find.byType(AnimatedContainer).first);
      final border = (ac.decoration as BoxDecoration).border as Border;
      expect(border.top.width, 1.5);
    });

    testWidgets('onTap fires', (tester) async {
      var taps = 0;
      await tester.pumpWidget(_wrap(
        BiuChip(
          onTap: () => taps++,
          label: const Text('tap'),
        ),
      ));
      await tester.tap(find.text('tap'));
      expect(taps, 1);
    });
  });
}

// 基础组件 widget tests — 确保 Phase A helpers 不崩 + 关键属性透传。

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:biumind/app/theme/effects.dart';
import 'package:biumind/app/theme/palettes.dart';
import 'package:biumind/core/ui/biu_glass.dart';
import 'package:biumind/core/ui/biu_hoverable.dart';
import 'package:biumind/core/ui/biu_pulse_dot.dart';

void main() {
  group('colorMix', () {
    test('100% a returns a', () {
      final mixed = colorMix(
        const Color(0xFFFF0000),
        const Color(0xFF00FF00),
        1.0,
      );
      expect(mixed.r, closeTo(1.0, 0.001));
      expect(mixed.g, closeTo(0.0, 0.001));
    });

    test('0% a returns b', () {
      final mixed = colorMix(
        const Color(0xFFFF0000),
        const Color(0xFF00FF00),
        0.0,
      );
      expect(mixed.r, closeTo(0.0, 0.001));
      expect(mixed.g, closeTo(1.0, 0.001));
    });

    test('50% a returns midpoint', () {
      final mixed = colorMix(
        const Color(0xFFFF0000),
        const Color(0xFF00FF00),
        0.5,
      );
      expect(mixed.r, closeTo(0.5, 0.001));
      expect(mixed.g, closeTo(0.5, 0.001));
    });

    test('alpha is mixed too', () {
      final mixed = colorMix(
        const Color(0xFFFF0000),
        const Color(0x0000FF00), // alpha 0
        0.5,
      );
      expect(mixed.a, closeTo(0.5, 0.001));
    });
  });

  group('cardGradTint', () {
    test('default 3% brand', () {
      final g = cardGradTint(const Color(0xFF7C3AED));
      expect(g.colors, hasLength(2));
      expect(g.colors.first.a, closeTo(0.03, 0.001));
      expect(g.colors.last.a, 0.0);
    });
  });

  group('heroDecoration', () {
    testWidgets('renders without error', (tester) async {
      await tester.pumpWidget(MaterialApp(
        home: Container(
          decoration: heroDecoration(
            paletteSpecOf(PaletteId.purpleOrange),
            Brightness.light,
          ),
        ),
      ));
      expect(tester.takeException(), isNull);
    });
  });

  group('bannerLayers', () {
    test('returns 2 BoxDecorations', () {
      final spec = paletteSpecOf(PaletteId.purpleOrange);
      final layers = bannerLayers(spec.bannerLight);
      expect(layers.main, isA<BoxDecoration>());
      expect(layers.scrim, isA<BoxDecoration>());
      expect((layers.main.gradient as LinearGradient).colors,
          spec.bannerLight.gradient);
    });
  });

  group('focusHalo', () {
    test('returns single shadow with spread 3', () {
      final halo = focusHalo(const Color(0xFF7C3AED));
      expect(halo, hasLength(1));
      expect(halo.first.spreadRadius, 3);
      expect(halo.first.blurRadius, 0);
    });
  });

  group('BiuGlass', () {
    testWidgets('renders child + applies tint', (tester) async {
      await tester.pumpWidget(const MaterialApp(
        home: Scaffold(
          body: BiuGlass(
            tintColor: Colors.red,
            tintAlpha: 0.5,
            child: Text('hi'),
          ),
        ),
      ));
      expect(find.text('hi'), findsOneWidget);
      expect(tester.takeException(), isNull);
    });

    testWidgets('disableBlur skips BackdropFilter', (tester) async {
      await tester.pumpWidget(const MaterialApp(
        home: Scaffold(
          body: BiuGlass(
            disableBlur: true,
            child: Text('hi'),
          ),
        ),
      ));
      expect(find.byType(BackdropFilter), findsNothing);
      expect(find.text('hi'), findsOneWidget);
    });
  });

  group('BiuHoverable', () {
    testWidgets('builder receives initial false/false', (tester) async {
      bool? gotHovered;
      bool? gotPressed;
      await tester.pumpWidget(MaterialApp(
        home: Scaffold(
          body: BiuHoverable(
            onTap: () {},
            builder: (ctx, hovered, pressed) {
              gotHovered = hovered;
              gotPressed = pressed;
              return const Text('hi');
            },
          ),
        ),
      ));
      expect(gotHovered, false);
      expect(gotPressed, false);
      expect(find.text('hi'), findsOneWidget);
    });
  });

  group('BiuLift', () {
    testWidgets('renders + AnimatedContainer present', (tester) async {
      await tester.pumpWidget(MaterialApp(
        home: Scaffold(
          body: BiuLift(
            onTap: () {},
            child: const Text('lift me'),
          ),
        ),
      ));
      expect(find.text('lift me'), findsOneWidget);
      expect(find.byType(AnimatedContainer), findsOneWidget);
    });
  });

  group('BiuPulseDot', () {
    testWidgets('renders with color + animates', (tester) async {
      await tester.pumpWidget(const MaterialApp(
        home: Scaffold(
          body: BiuPulseDot(color: Colors.green, size: 8),
        ),
      ));
      expect(tester.takeException(), isNull);
      // pump 多帧让动画跑
      await tester.pump(const Duration(milliseconds: 100));
      await tester.pump(const Duration(milliseconds: 1200));
      expect(tester.takeException(), isNull);
    });

    testWidgets('disableAnimations renders static fallback', (tester) async {
      // 用 builder 拿到 MaterialApp 内层 MediaQuery,然后 copyWith 注入
      // disableAnimations(外层 MediaQuery 会被 MaterialApp 自建的覆盖)
      await tester.pumpWidget(MaterialApp(
        builder: (ctx, child) {
          final mq = MediaQuery.of(ctx);
          return MediaQuery(
            data: mq.copyWith(disableAnimations: true),
            child: child!,
          );
        },
        home: const Scaffold(
          body: BiuPulseDot(color: Colors.green),
        ),
      ));
      // 在 BiuPulseDot 的 subtree 内不应该有 AnimatedBuilder
      // (MaterialApp 自身有 AnimatedBuilder for theme/title 等不算)
      final pulseDot = find.byType(BiuPulseDot);
      expect(pulseDot, findsOneWidget);
      expect(
        find.descendant(of: pulseDot, matching: find.byType(AnimatedBuilder)),
        findsNothing,
      );
    });
  });
}

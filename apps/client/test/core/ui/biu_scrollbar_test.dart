// biu_scrollbar_test — 锁 C12 收口组件的平台分工:
//   桌面: 返回 child (全局 BiuScrollBehavior 已挂 Scrollbar, 防双挂)
//   移动: 包同款参数 Scrollbar (thumbVisibility/trackVisibility 双 false)

import 'package:biumind/core/ui/biu_scrollbar.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

Widget _wrap(Widget child) => MaterialApp(home: Scaffold(body: child));

void main() {
  testWidgets('桌面平台: 不包 Scrollbar (防与全局 behavior 双挂)', (tester) async {
    for (final p in [
      TargetPlatform.macOS,
      TargetPlatform.windows,
      TargetPlatform.linux,
    ]) {
      debugDefaultTargetPlatformOverride = p;
      await tester.pumpWidget(
        _wrap(const BiuScrollbar(child: SizedBox(key: Key('c')))),
      );
      expect(find.byType(Scrollbar), findsNothing, reason: '$p');
      expect(find.byKey(const Key('c')), findsOneWidget, reason: '$p');
    }
    debugDefaultTargetPlatformOverride = null;
  });

  testWidgets('移动平台: 包 overlay 参数 Scrollbar', (tester) async {
    for (final p in [TargetPlatform.iOS, TargetPlatform.android]) {
      debugDefaultTargetPlatformOverride = p;
      await tester.pumpWidget(
        _wrap(const BiuScrollbar(child: SizedBox(key: Key('c')))),
      );
      final bar = tester.widget<Scrollbar>(find.byType(Scrollbar));
      expect(bar.thumbVisibility, isFalse, reason: '$p');
      expect(bar.trackVisibility, isFalse, reason: '$p');
      expect(bar.interactive, isTrue, reason: '$p');
    }
    debugDefaultTargetPlatformOverride = null;
  });
}

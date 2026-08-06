// router_public_route_test — pure-function check of the unauthenticated
// browseable whitelist. Whole router redirect logic 涉及 GoRouter +
// Riverpod container + auth_service, 集成场景成本太高;这里只锁住白名单
// 范围避免未来误改成 /chat 也开放。

import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart' show NoTransitionPage;
import 'package:biumind/app/router.dart';

void main() {
  group('isPublicRoute', () {
    test('/skills + 子路径 → public', () {
      expect(isPublicRoute('/skills'), isTrue);
      expect(isPublicRoute('/skills/123'), isTrue);
      expect(isPublicRoute('/skills/edit'), isTrue);
    });

    test('/settings + 子路径 → public', () {
      expect(isPublicRoute('/settings'), isTrue);
      expect(isPublicRoute('/settings/about'), isTrue);
      expect(isPublicRoute('/settings/models'), isTrue);
    });

    test('/chat 必须登录 (产品价值边界)', () {
      expect(isPublicRoute('/chat'), isFalse);
      expect(isPublicRoute('/chat/search'), isFalse);
    });

    test('其他 protected route 不漏', () {
      expect(isPublicRoute('/wiki'), isFalse);
      expect(isPublicRoute('/graph'), isFalse);
      expect(isPublicRoute('/memory'), isFalse);
      expect(isPublicRoute('/code'), isFalse);
      expect(isPublicRoute('/admin'), isFalse);
      expect(isPublicRoute('/apps'), isFalse);
    });

    test('login / splash 不在判断职责内', () {
      // 这些是 redirect() 早就处理过的特殊 loc, 不应靠 isPublicRoute 兜底。
      // 这里仅记录: 它们都返回 false (走默认 protected 处理)。
      expect(isPublicRoute('/login'), isFalse);
      expect(isPublicRoute('/splash'), isFalse);
    });

    test('空字符串 / 根 / 未知 → false', () {
      expect(isPublicRoute(''), isFalse);
      expect(isPublicRoute('/'), isFalse);
      expect(isPublicRoute('/nonsense'), isFalse);
    });

    test('前缀边界: /skill 不被 /skills 误中', () {
      // /skill (单数) 不存在但作 sanity check — startsWith('/skills')
      // 拒掉这种前缀打错。
      expect(isPublicRoute('/skill'), isFalse);
      expect(isPublicRoute('/skillset'), isTrue,
          reason:
              'startsWith("/skills") 严格拼接, /skillset 命中是已知行为; '
              '若将来路由扩到此前缀需要自定义边界。');
    });
  });

  group('tabPage / subPage 平台分流 (导航设计 §3.2)', () {
    test('移动平台: subPage → MaterialPage (转场+返回栈)', () {
      debugDefaultTargetPlatformOverride = TargetPlatform.android;
      addTearDown(() => debugDefaultTargetPlatformOverride = null);
      expect(subPage(const SizedBox()), isA<MaterialPage<void>>());
      expect(subPage(const SizedBox()), isNot(isA<NoTransitionPage<void>>()));
    });

    test('iOS: subPage → MaterialPage (右滑返回手势)', () {
      debugDefaultTargetPlatformOverride = TargetPlatform.iOS;
      addTearDown(() => debugDefaultTargetPlatformOverride = null);
      expect(subPage(const SizedBox()), isA<MaterialPage<void>>());
    });

    test('tabPage 全平台永远 NoTransitionPage (tab 模型)', () {
      for (final p in TargetPlatform.values) {
        debugDefaultTargetPlatformOverride = p;
        expect(tabPage(const SizedBox()), isA<NoTransitionPage<void>>(),
            reason: '$p');
      }
      debugDefaultTargetPlatformOverride = null;
    });

    test('桌面平台: subPage 退化为 NoTransitionPage (零回归)', () {
      for (final p in [
        TargetPlatform.macOS,
        TargetPlatform.linux,
        TargetPlatform.windows,
      ]) {
        debugDefaultTargetPlatformOverride = p;
        expect(subPage(const SizedBox()), isA<NoTransitionPage<void>>(),
            reason: '$p');
      }
      debugDefaultTargetPlatformOverride = null;
    });
  });
}

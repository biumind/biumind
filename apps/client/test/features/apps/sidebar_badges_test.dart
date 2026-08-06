// Sidebar badge 数据层单测 — severity 解析 + visible 决策。
//
// 完整 controller 行为 (timer 启停 / lifecycle / invoke) 涉及多个
// provider 异步, 留 widget integration test; 这里只覆盖纯函数。

import 'package:biumind/data/sidebar_badges.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  group('parseBadgeSeverity', () {
    test('warn / error / info 映射', () {
      expect(parseBadgeSeverity('warn'), BadgeSeverity.warn);
      expect(parseBadgeSeverity('error'), BadgeSeverity.error);
      expect(parseBadgeSeverity('info'), BadgeSeverity.info);
    });

    test('未知 / null 退回 info (中性)', () {
      expect(parseBadgeSeverity(null), BadgeSeverity.info);
      expect(parseBadgeSeverity(''), BadgeSeverity.info);
      expect(parseBadgeSeverity('critical'), BadgeSeverity.info);
      expect(parseBadgeSeverity('WARN'), BadgeSeverity.info, reason: '大小写敏感');
    });
  });

  group('BadgeData.visible', () {
    test('count > 0 visible', () {
      final b = BadgeData(
        count: 3,
        severity: BadgeSeverity.info,
        fetchedAt: DateTime.now(),
      );
      expect(b.visible, true);
    });

    test('count <= 0 不渲染', () {
      final zero = BadgeData(
        count: 0,
        severity: BadgeSeverity.error,
        fetchedAt: DateTime.now(),
      );
      expect(zero.visible, false);
      final neg = BadgeData(
        count: -5,
        severity: BadgeSeverity.warn,
        fetchedAt: DateTime.now(),
      );
      expect(neg.visible, false);
    });
  });
}

// computeRefreshCadence — 比例化 margin/tick 推导逻辑(A4)。
//
// 设计:margin = ttl ÷ 10, tick = ttl ÷ 20, 下限 30s。
// fallback:ttl null/0 → 5min margin / 1min tick。

import 'package:biumind/services/token_refresher.dart';
import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  group('computeRefreshCadence', () {
    test('null → fallback (5min margin, 1min tick)', () {
      final c = computeRefreshCadence(null);
      expect(c.margin, const Duration(minutes: 5));
      expect(c.tick, const Duration(minutes: 1));
    });

    test('0 → fallback (避免 div by 0)', () {
      final c = computeRefreshCadence(0);
      expect(c.margin, const Duration(minutes: 5));
      expect(c.tick, const Duration(minutes: 1));
    });

    test('负数 → fallback', () {
      final c = computeRefreshCadence(-100);
      expect(c.margin, const Duration(minutes: 5));
      expect(c.tick, const Duration(minutes: 1));
    });

    test('1h TTL → margin 6min, tick 3min', () {
      final c = computeRefreshCadence(3600);
      expect(c.margin, const Duration(minutes: 6));
      expect(c.tick, const Duration(minutes: 3));
    });

    test('24h TTL → margin 144min, tick 72min', () {
      final c = computeRefreshCadence(86400);
      expect(c.margin, const Duration(minutes: 144));
      expect(c.tick, const Duration(minutes: 72));
    });

    test('15min TTL → margin 90s, tick 45s', () {
      final c = computeRefreshCadence(900);
      expect(c.margin, const Duration(seconds: 90));
      expect(c.tick, const Duration(seconds: 45));
    });

    test('5min TTL → tick clamp 至 30s 下限', () {
      final c = computeRefreshCadence(300);
      expect(c.margin, const Duration(seconds: 30),
          reason: '5min ÷ 10 = 30s, 等于 _minMargin 下限');
      expect(c.tick, const Duration(seconds: 30),
          reason: '5min ÷ 20 = 15s, 应被 clamp 到 _minTick 30s');
    });

    test('60s TTL(异常配置)→ tick + margin 都被 clamp 至 30s', () {
      final c = computeRefreshCadence(60);
      expect(c.margin, const Duration(seconds: 30),
          reason: '60s ÷ 10 = 6s, 应 clamp 至 30s');
      expect(c.tick, const Duration(seconds: 30),
          reason: '60s ÷ 20 = 3s, 应 clamp 至 30s');
    });
  });
}

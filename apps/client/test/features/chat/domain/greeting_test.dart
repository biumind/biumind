// Greeting —— Hero 欢迎页问候语 + 相对时间单测。

import 'package:flutter_test/flutter_test.dart';

import 'package:biumind/features/chat/domain/greeting.dart';

void main() {
  group('greetingForHour', () {
    test('time-of-day buckets', () {
      expect(greetingForHour(2), '夜深了');
      expect(greetingForHour(7), '早上好');
      expect(greetingForHour(12), '中午好');
      expect(greetingForHour(15), '下午好');
      expect(greetingForHour(20), '晚上好');
      expect(greetingForHour(23), '夜深了');
    });

    test('appends user name when given', () {
      expect(greetingForHour(7, userName: 'Alice'), '早上好，Alice');
      expect(greetingForHour(7, userName: ''), '早上好');
      expect(greetingForHour(7), '早上好');
    });
  });

  group('relativeTime', () {
    final now = DateTime.utc(2026, 6, 1, 12);
    test('< 1 minute → 刚刚', () {
      expect(relativeTime(now.subtract(const Duration(seconds: 30)), now: now),
          '刚刚');
    });
    test('< 1 hour → N 分钟前', () {
      expect(relativeTime(now.subtract(const Duration(minutes: 5)), now: now),
          '5 分钟前');
    });
    test('< 24 hours → N 小时前', () {
      expect(relativeTime(now.subtract(const Duration(hours: 3)), now: now),
          '3 小时前');
    });
    test('< 30 days → N 天前', () {
      expect(relativeTime(now.subtract(const Duration(days: 5)), now: now),
          '5 天前');
    });
    test('>= 30 days → yyyy-mm-dd', () {
      final old = DateTime.utc(2025, 12, 1);
      expect(relativeTime(old, now: now), '2025-12-01');
    });
  });
}

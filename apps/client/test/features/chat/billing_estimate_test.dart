// Test ChatEstimate display logic.

import 'package:flutter_test/flutter_test.dart';

import 'package:biumind/features/chat/data/billing_estimate_client.dart';

void main() {
  group('ChatEstimate.displayLabel', () {
    test('byok_active → "0 积分 · BYOK"', () {
      final e = ChatEstimate(
        provider: 'anthropic',
        model: 'claude-sonnet-4-5',
        byokActive: true,
        minCredits: 0,
        maxCredits: 0,
      );
      expect(e.displayLabel(), '0 积分 · BYOK');
    });

    test('warning 非空 → 隐藏', () {
      final e = ChatEstimate(
        provider: 'anthropic',
        model: 'unknown',
        byokActive: false,
        minCredits: 0,
        maxCredits: 0,
        warning: 'pricing not found',
      );
      expect(e.displayLabel(), '');
    });

    test('min == max → 单值显示', () {
      final e = ChatEstimate(
        provider: 'anthropic',
        model: 'claude-haiku-4-5',
        byokActive: false,
        minCredits: 1,
        maxCredits: 1,
      );
      expect(e.displayLabel(), '约 1 积分');
    });

    test('range → "约 N-M 积分"', () {
      final e = ChatEstimate(
        provider: 'openai',
        model: 'gpt-4o',
        byokActive: false,
        minCredits: 5,
        maxCredits: 12,
      );
      expect(e.displayLabel(), '约 5-12 积分');
    });
  });

  group('ChatEstimate.fromJson', () {
    test('完整字段', () {
      final e = ChatEstimate.fromJson({
        'provider': 'anthropic',
        'model': 'claude-sonnet-4-5',
        'byok_active': false,
        'min_credits': 3,
        'max_credits': 18,
      });
      expect(e.provider, 'anthropic');
      expect(e.minCredits, 3);
      expect(e.maxCredits, 18);
      expect(e.byokActive, false);
    });

    test('缺字段 → 默认 0 / 空字符串', () {
      final e = ChatEstimate.fromJson({});
      expect(e.provider, '');
      expect(e.byokActive, false);
      expect(e.minCredits, 0);
    });
  });
}

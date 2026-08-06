// TokenEstimate —— 启发式 token 估算单测。

import 'package:flutter_test/flutter_test.dart';

import 'package:biumind/features/chat/domain/token_estimate.dart';

void main() {
  group('estimateTokens', () {
    test('empty string is 0', () {
      expect(estimateTokens(''), 0);
    });

    test('ASCII roughly 1 token per 4 chars', () {
      expect(estimateTokens('hello world!'), 3); // 12 / 4
      expect(estimateTokens('a' * 100), 25); // 100 / 4
    });

    test('CJK weighted ~1.5 token per char', () {
      // 4 个中文字 → ceil(4 * 1.5) = 6
      expect(estimateTokens('你好世界'), 6);
    });

    test('mixed text sums both pieces', () {
      // 'hello 你好' = 5 ascii(+space=6) + 2 cjk
      // ascii=6 → ceil(6/4)=2; cjk=2 → ceil(2*1.5)=3 → 5
      expect(estimateTokens('hello 你好'), 5);
    });
  });

  group('contextWindowFor', () {
    test('null / empty falls back to 8k', () {
      expect(contextWindowFor(null), 8192);
      expect(contextWindowFor(''), 8192);
    });

    test('claude family is 200k', () {
      expect(contextWindowFor('claude-opus-4-7'), 200000);
      expect(contextWindowFor('claude-sonnet-4-6'), 200000);
      expect(contextWindowFor('claude-3-5-sonnet'), 200000);
    });

    test('gpt-4o is 128k', () {
      expect(contextWindowFor('gpt-4o-mini'), 128000);
    });

    test('gemini is 1M', () {
      expect(contextWindowFor('gemini-2.0-flash'), 1000000);
    });

    test('unknown model is 8k', () {
      expect(contextWindowFor('mystery-model'), 8192);
    });
  });
}

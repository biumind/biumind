// Test ApiKeyEntry parsing + status mapping.

import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:biumind/features/settings/data/api_keys_client.dart';

void main() {
  group('ApiKeyEntry.fromJson', () {
    test('基本字段', () {
      final e = ApiKeyEntry.fromJson({
        'id': 'uuid-1',
        'provider': 'anthropic',
        'label': 'main',
        'last4': 'AbCd',
        'status': 'valid',
        'failure_count': 0,
        'created_at': '2026-06-06T10:00:00Z',
      });
      expect(e.provider, 'anthropic');
      expect(e.last4, 'AbCd');
      expect(e.status, ApiKeyStatus.valid);
      expect(e.failureCount, 0);
    });

    test('invalid 状态', () {
      final e = ApiKeyEntry.fromJson({
        'id': 'x',
        'provider': 'openai',
        'status': 'invalid',
        'failure_count': 5,
        'created_at': '2026-06-06T10:00:00Z',
      });
      expect(e.status, ApiKeyStatus.invalid);
      expect(e.failureCount, 5);
    });

    test('revoked 状态', () {
      final e = ApiKeyEntry.fromJson({
        'id': 'x',
        'provider': 'deepseek',
        'status': 'revoked',
        'created_at': '2026-06-06T10:00:00Z',
      });
      expect(e.status, ApiKeyStatus.revoked);
    });

    test('未知状态降级 invalid', () {
      final e = ApiKeyEntry.fromJson({
        'id': 'x',
        'provider': 'doubao',
        'status': 'gibberish',
        'created_at': '2026-06-06T10:00:00Z',
      });
      expect(e.status, ApiKeyStatus.invalid);
    });
  });

  test('TestResult 分类', () {
    expect(const TestResult('valid').isValid, true);
    expect(const TestResult('valid').isInvalid, false);
    expect(const TestResult('invalid').isInvalid, true);
    expect(const TestResult('network').isValid, false);
    expect(const TestResult('network').isInvalid, false);
  });

  test('supportedByokProviders 12 项 + labels 全有', () {
    expect(supportedByokProviders.length, 12);
    for (final p in supportedByokProviders) {
      expect(byokProviderLabels[p], isNotNull, reason: 'label missing for $p');
    }
  });
}

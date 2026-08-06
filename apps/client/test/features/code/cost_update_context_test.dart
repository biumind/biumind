import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:biumind/features/code/domain/code_task.dart';

void main() {
  group('CostUpdate context/cache fields (G4)', () {
    test('fromJson 解析 cache + context + window', () {
      final e = AgentEvent.fromJson({
        'type': 'cost_update',
        'ts': '2026-06-28T10:00:00Z',
        'input_tokens': 1000,
        'output_tokens': 500,
        'cache_creation_tokens': 200,
        'cache_read_tokens': 1800,
        'context_tokens': 90000,
        'context_window': 200000,
      });
      expect(e, isA<CostUpdate>());
      final c = e as CostUpdate;
      expect(c.cacheCreationTokens, 200);
      expect(c.cacheReadTokens, 1800);
      expect(c.contextTokens, 90000);
      expect(c.contextWindow, 200000);
    });

    test('缺字段(老事件)→ 默认 0,向后兼容', () {
      final c = AgentEvent.fromJson({
        'type': 'cost_update',
        'ts': '2026-06-28T10:00:00Z',
        'input_tokens': 10,
        'output_tokens': 5,
      }) as CostUpdate;
      expect(c.contextTokens, 0);
      expect(c.contextWindow, 0);
      expect(c.cacheReadTokens, 0);
    });

    test('toJson round-trips context fields', () {
      final c = CostUpdate(
        ts: DateTime(2026, 6, 28, 10),
        totalUsd: 0,
        inputTokens: 1,
        outputTokens: 2,
        contextTokens: 123,
        contextWindow: 456,
      );
      final j = c.toJson();
      expect(j['context_tokens'], 123);
      expect(j['context_window'], 456);
    });
  });
}

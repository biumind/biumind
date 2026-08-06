// AutoSaveController 行为测试 —— 不拉 widget 树，直接驱动 controller。
//
// 重点回归：saver 抛 StateError（Error 子类，如 repository 的
// 'note not found'）时状态必须落到 error，而不是永远卡在 saving
// （P0：新建笔记 rekey 断链后内容静默丢失、状态栏卡「保存中…」）。

import 'package:biumind/core/editor/page_autosave.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  group('AutoSaveController', () {
    test('saver 成功后状态 saved', () async {
      final c = AutoSaveController(
        saver: (_) async => const AutoSaveOutcome(status: AutoSaveStatus.saved),
        debounce: const Duration(milliseconds: 10),
      );
      addTearDown(c.dispose);

      c.schedule('content');
      await Future<void>.delayed(const Duration(milliseconds: 50));
      expect(c.status, AutoSaveStatus.saved);
    });

    test('saver 抛 Exception 时状态 error', () async {
      final c = AutoSaveController(
        saver: (_) async => throw Exception('boom'),
        debounce: const Duration(milliseconds: 10),
      );
      addTearDown(c.dispose);

      c.schedule('content');
      await Future<void>.delayed(const Duration(milliseconds: 50));
      expect(c.status, AutoSaveStatus.error);
      expect(c.errorMessage, contains('boom'));
    });

    test('saver 抛 StateError（Error 子类）时状态 error 而不是卡在 saving',
        () async {
      final c = AutoSaveController(
        saver: (_) async => throw StateError('note not found: local-x'),
        debounce: const Duration(milliseconds: 10),
      );
      addTearDown(c.dispose);

      c.schedule('content');
      await Future<void>.delayed(const Duration(milliseconds: 50));
      expect(c.status, AutoSaveStatus.error,
          reason: 'StateError 漏捕会让状态永远卡在 saving，内容静默丢失');
      expect(c.errorMessage, contains('note not found'));
    });

    test('flush 同步执行 pending 保存，saver 抛 Error 同样落 error', () async {
      final c = AutoSaveController(
        saver: (_) async => throw StateError('note not found'),
      );
      addTearDown(c.dispose);

      c.schedule('content');
      await c.flush();
      expect(c.status, AutoSaveStatus.error);
    });
  });
}

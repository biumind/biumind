// PaletteAction —— Cmd+K 命令面板过滤逻辑单测。

import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:biumind/features/chat/domain/palette_actions.dart';

PaletteAction _act(String id, String label, {String? group}) {
  return PaletteAction(
    id: id,
    label: label,
    group: group,
    run: () {},
  );
}

void main() {
  group('filterPaletteActions', () {
    final all = [
      _act('new-thread', '新建对话'),
      _act('cross-search', '搜索全部对话'),
      _act('multi-select', '多选当前对话消息'),
      _act('shortcuts', '键盘快捷键'),
      _act('go-thread-1', 'Wiki 设计', group: '切换对话'),
      _act('go-thread-2', 'Cooking ideas', group: '切换对话'),
    ];

    test('empty query returns all', () {
      expect(filterPaletteActions(all, ''), all);
      expect(filterPaletteActions(all, '   '), all);
    });

    test('case-insensitive subsequence match on label (CJK)', () {
      // "对话" 出现在 3 个含"对话"的 label 中：
      // 新建对话 / 搜索全部对话 / 多选当前对话消息
      final hits = filterPaletteActions(all, '对话');
      expect(hits.length, 3);
    });

    test('subsequence match on id', () {
      // "go" 匹配 "go-thread-1", "go-thread-2"
      final hits = filterPaletteActions(all, 'go');
      expect(hits.map((a) => a.id), ['go-thread-1', 'go-thread-2']);
    });

    test('non-contiguous subsequence works (ASCII)', () {
      // "ck" 匹配 "Cooking ideas" (lowercase 'c' 在前, 'k' 在后)
      final hits = filterPaletteActions(all, 'ck');
      expect(hits.length, 1);
      expect(hits.first.label, 'Cooking ideas');
    });

    test('id-based subseq matches on hyphen-separated tokens', () {
      // "ms" 匹配 "multi-select" 的 m 和 s
      final hits = filterPaletteActions(all, 'ms');
      expect(hits.map((a) => a.id).contains('multi-select'), true);
    });

    test('no match returns empty', () {
      expect(filterPaletteActions(all, 'xyz123'), isEmpty);
    });

    test('preserves ordering of input list', () {
      final hits = filterPaletteActions(all, '对话');
      // 4 个 label 含"对话": new-thread / cross-search / multi-select +
      //   "切换对话" 是 group 不是 label，不算 → 还是看 label 中包含"对话"。
      // new-thread, cross-search, multi-select 各 1，共 3。
      // 注意 group 字段不参与匹配。
      expect(hits.first.id, 'new-thread');
    });
  });
}

// thread_filter —— sidebar 过滤 + pin 分组单测。

import 'package:flutter_test/flutter_test.dart';

import 'package:biumind/features/chat/domain/chat_models.dart';
import 'package:biumind/features/chat/domain/thread_filter.dart';

Thread _t({required String id, required String title, bool pinned = false}) {
  return Thread(
    id: id,
    title: title,
    mode: ThreadMode.chat,
    pinned: pinned,
    createdAt: DateTime.utc(2026, 6, 1),
    updatedAt: DateTime.utc(2026, 6, 1),
  );
}

void main() {
  group('filterThreadsByQuery', () {
    final all = [
      _t(id: '1', title: 'Wiki design'),
      _t(id: '2', title: 'Cooking ideas'),
      _t(id: '3', title: 'Flutter performance'),
      _t(id: '4', title: '中文对话'),
    ];

    test('empty query returns all', () {
      expect(filterThreadsByQuery(all, ''), all);
      expect(filterThreadsByQuery(all, '   '), all);
    });

    test('case-insensitive contains match', () {
      final hits = filterThreadsByQuery(all, 'flutter');
      expect(hits.length, 1);
      expect(hits.first.id, '3');
    });

    test('subsequence match (fuzzy)', () {
      // "ck" 子序列出现在 "cooking" → 命中
      final hits = filterThreadsByQuery(all, 'ck');
      expect(hits.any((t) => t.id == '2'), true);
    });

    test('CJK contains works', () {
      final hits = filterThreadsByQuery(all, '中文');
      expect(hits.length, 1);
      expect(hits.first.id, '4');
    });

    test('no match returns empty', () {
      expect(filterThreadsByQuery(all, 'xyzqq'), isEmpty);
    });
  });

  group('splitPinnedThreads', () {
    test('preserves order within each group', () {
      final list = [
        _t(id: 'a', title: 'a'),
        _t(id: 'b', title: 'b', pinned: true),
        _t(id: 'c', title: 'c'),
        _t(id: 'd', title: 'd', pinned: true),
      ];
      final r = splitPinnedThreads(list);
      expect(r.pinned.map((t) => t.id), ['b', 'd']);
      expect(r.others.map((t) => t.id), ['a', 'c']);
    });

    test('all pinned → others empty', () {
      final list = [_t(id: 'a', title: 'a', pinned: true)];
      final r = splitPinnedThreads(list);
      expect(r.pinned.length, 1);
      expect(r.others, isEmpty);
    });

    test('empty input', () {
      final r = splitPinnedThreads(const []);
      expect(r.pinned, isEmpty);
      expect(r.others, isEmpty);
    });
  });
}

// Thread sidebar 过滤 + pin 分组工具。
// 设计文档 docs/BiuMind-Chat-UI-Benchmark-Optimization.md（v2 sidebar）。
//
// 纯函数，不依赖 Flutter / Drift —— 让 widget test 不必走 db。

import 'chat_models.dart';

/// 按 query 过滤 threads。空 query → 原列表。匹配规则：
///   * 大小写不敏感
///   * title.contains(query)
///   * 子序列匹配（让用户能跳字母 "wd" 命中 "Wiki design"）
List<Thread> filterThreadsByQuery(List<Thread> threads, String query) {
  final q = query.trim().toLowerCase();
  if (q.isEmpty) return threads;
  return threads.where((t) {
    final title = t.title.toLowerCase();
    if (title.contains(q)) return true;
    return _subseq(title, q);
  }).toList(growable: false);
}

/// 把 threads 拆成 (pinned, others)。
({List<Thread> pinned, List<Thread> others}) splitPinnedThreads(
  List<Thread> threads,
) {
  final pinned = <Thread>[];
  final others = <Thread>[];
  for (final t in threads) {
    if (t.pinned) {
      pinned.add(t);
    } else {
      others.add(t);
    }
  }
  return (pinned: pinned, others: others);
}

bool _subseq(String text, String pattern) {
  if (pattern.isEmpty) return true;
  var i = 0;
  for (var j = 0; j < text.length && i < pattern.length; j++) {
    if (text.codeUnitAt(j) == pattern.codeUnitAt(i)) i++;
  }
  return i == pattern.length;
}

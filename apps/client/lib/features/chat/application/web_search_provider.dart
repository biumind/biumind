// WebSearchHint —— composer 一次性"联网搜索"开关。
// 设计文档 docs/BiuMind-Chat-UI-Benchmark-Optimization.md（v2 联网搜索 hint）。
//
// 行为：
//   * 全局单值布尔 —— 同一时间只有一个 thread 在打字，简化语义
//   * 点开 → 在 prompt 顶 prepend 一段 hint，让 brain 优先用 web 工具
//   * 发送后 ComposerV2 自动 clear（一次性）
//
// brain 端是否真有 web tool 不影响这层行为；没有时 hint 退化成普通指令。

import 'package:flutter_riverpod/flutter_riverpod.dart';

class WebSearchHintNotifier extends StateNotifier<bool> {
  WebSearchHintNotifier() : super(false);

  void toggle() {
    state = !state;
  }

  void clear() {
    if (state) state = false;
  }
}

final webSearchHintProvider =
    StateNotifierProvider<WebSearchHintNotifier, bool>(
  (ref) => WebSearchHintNotifier(),
);

/// 把 hint 前缀拼到 user prompt。开启时返回 hint+prompt；关闭时返回原文。
String applyWebSearchHint(String prompt, bool enabled) {
  if (!enabled) return prompt;
  const hint = '请优先使用网络搜索工具查询最新信息后再回答。';
  if (prompt.trim().isEmpty) return hint;
  return '$hint\n\n$prompt';
}

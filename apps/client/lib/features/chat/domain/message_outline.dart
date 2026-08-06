// MessageOutline —— 从 assistant 消息文本里抽 markdown 标题作大纲。
// 设计文档 docs/BiuMind-Chat-UI-Benchmark-Optimization.md（v2 大纲）。
//
// 规则：
//   * 行首 1-6 个 `#` + 空格 + 标题文本 是 markdown ATX heading
//   * 跳过 fenced code block 内的伪 heading（``` 之间的）
//   * 至少有 3 个 heading 才返回，否则返空（避免短消息也出条目）

class OutlineItem {
  const OutlineItem({required this.level, required this.title});
  final int level;
  final String title;
}

List<OutlineItem> parseOutline(String text) {
  if (text.isEmpty) return const [];
  final lines = text.split('\n');
  final out = <OutlineItem>[];
  var inFence = false;
  for (final raw in lines) {
    final line = raw.trimRight();
    final trimmedStart = line.trimLeft();
    if (trimmedStart.startsWith('```') || trimmedStart.startsWith('~~~')) {
      inFence = !inFence;
      continue;
    }
    if (inFence) continue;
    final m = RegExp(r'^(#{1,6})\s+(.+)$').firstMatch(line);
    if (m == null) continue;
    final level = m.group(1)!.length;
    final title = m.group(2)!.trim();
    if (title.isEmpty) continue;
    out.add(OutlineItem(level: level, title: title));
  }
  if (out.length < 3) return const [];
  return out;
}

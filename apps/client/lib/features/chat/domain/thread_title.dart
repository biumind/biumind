// ThreadTitle —— 从首条 user prompt 推一个 thread 标题。
// 设计文档 docs/BiuMind-Chat-UI-Benchmark-Optimization.md（v2 thread 自动改名）。
//
// 规则：
//   * 取第一段非空行（避免 markdown 元信息行 / 空行干扰）
//   * 去掉常见前导符号（# ## > * - 1. 之类）
//   * 截到 30 字以内（中英文都按字符数算，不去算视觉宽度）
//   * trim 末尾空白；如果末尾被截断则补省略号

String titleFromPrompt(String prompt, {int maxChars = 30}) {
  if (prompt.isEmpty) return '';
  final lines = prompt.split('\n');
  String firstNonEmpty = '';
  for (final line in lines) {
    final t = line.trim();
    if (t.isEmpty) continue;
    firstNonEmpty = t;
    break;
  }
  if (firstNonEmpty.isEmpty) return '';
  // 去 markdown / list 前导符号
  var s = firstNonEmpty
      .replaceFirst(RegExp(r'^#{1,6}\s+'), '')
      .replaceFirst(RegExp(r'^>\s+'), '')
      .replaceFirst(RegExp(r'^[*\-]\s+'), '')
      .replaceFirst(RegExp(r'^\d+\.\s+'), '')
      .trim();
  if (s.isEmpty) return '';
  if (s.length <= maxChars) return s;
  return '${s.substring(0, maxChars).trimRight()}…';
}

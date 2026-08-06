// Token 估计 —— 启发式，不调远端 tokenizer。
// 设计文档 docs/BiuMind-Chat-UI-Benchmark-Optimization.md P1-8。
//
// 规则：
//   * ASCII 文本：~1 token / 4 字符（OpenAI / Anthropic 经验值）
//   * CJK 文本：~1 token / 1.5 字符（中文一字常对应 1.5-2 token）
//   * 估两者中的较大值，宁愿略偏高，避免实际超 ctx 才发现。

int estimateTokens(String text) {
  if (text.isEmpty) return 0;
  var cjk = 0;
  for (final r in text.runes) {
    if ((r >= 0x4E00 && r <= 0x9FFF) || // CJK Unified
        (r >= 0x3040 && r <= 0x30FF) || // 日文假名
        (r >= 0xAC00 && r <= 0xD7AF)) {
      cjk++;
    }
  }
  final asciiLike = text.length - cjk;
  final tokensFromAscii = (asciiLike / 4).ceil();
  final tokensFromCjk = (cjk * 1.5).ceil();
  return tokensFromAscii + tokensFromCjk;
}

/// 主流模型的 context window 估计（粗略）。未知 model → 8k 兜底。
int contextWindowFor(String? model) {
  if (model == null || model.isEmpty) return 8192;
  final m = model.toLowerCase();
  // Anthropic
  if (m.contains('claude') && m.contains('opus-4')) return 200000;
  if (m.contains('claude') && m.contains('sonnet-4')) return 200000;
  if (m.contains('claude') && m.contains('haiku-4')) return 200000;
  if (m.contains('claude-3-5')) return 200000;
  if (m.contains('claude')) return 100000;
  // OpenAI
  if (m.contains('gpt-4o')) return 128000;
  if (m.contains('gpt-4-turbo')) return 128000;
  if (m.contains('gpt-4')) return 32000;
  if (m.contains('gpt-3.5')) return 16000;
  // Google
  if (m.contains('gemini-2') || m.contains('gemini-1.5')) return 1000000;
  // DeepSeek / Qwen / Yi 等开源
  if (m.contains('deepseek') || m.contains('qwen') || m.contains('yi-')) {
    return 32000;
  }
  return 8192;
}

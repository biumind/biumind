// ReasoningParser —— 推理模型 `<think>...</think>` 段提取。
//
// 背景:deepseek-r1 / glm-thinking / gpt-oss / qwen-r1 等开源推理模型,以及
// 多数 LiteLLM/vLLM 网关把 thinking 内容直接塞进 text content,用
// `<think>` / `</think>` 包裹 —— 这是 OpenAI Compat 路径的事实标准。
// Anthropic 原生 thinking blocks 走另一条 SDK 路径(content_block.type=
// thinking),不在本解析器范围内。
//
// 解析规则:
//   * 顶层标签:`<think>`(开)/ `</think>`(闭),大小写敏感
//   * 嵌套不支持 —— 第一层 `<think>` 内部再出现 `<think>` 当作 reasoning 文本
//   * 流式状态:isStreaming=true 时,未闭合的 `<think>` 视为"思考进行中",
//     返回 closed=false 段;客户端据此渲染 shimmer "思考中…"
//   * isStreaming=false(turn 已 done):未闭合的 `<think>` 仍当作 reasoning
//     段返回(closed=false),让 UI 仍然能展示折叠面板而不是把残文吞掉
//   * 非 reasoning 部分原样保留为 text 段
//
// 调用方:BlockRenderer 拿到 assistant TextBlock 的 text → parseReasoning →
// 按段渲染(reasoning → ExpansionTile,text → ChatMarkdownView)。

enum ReasoningSegmentKind {
  /// `<think>...</think>` 内的内容(推理过程)。
  reasoning,
  /// 标签外的普通文本(最终回答)。
  text,
}

class ReasoningSegment {
  const ReasoningSegment({
    required this.kind,
    required this.text,
    required this.closed,
  });

  final ReasoningSegmentKind kind;
  final String text;

  /// 仅对 [ReasoningSegmentKind.reasoning] 有意义:
  ///   * true  → 已经看到 `</think>`,推理段完整
  ///   * false → 流式中尚未闭合,UI 显示"思考中…"
  /// text 段始终为 true。
  final bool closed;

  bool get isReasoning => kind == ReasoningSegmentKind.reasoning;
  bool get isText => kind == ReasoningSegmentKind.text;
}

const _openTag = '<think>';
const _closeTag = '</think>';

/// 把 [text] 切成 reasoning / text 段序列。空输入返回空列表。
///
/// [isStreaming] 当前只影响日志/语义解读 —— 实现上无论 streaming 与否,
/// 未闭合 `<think>` 一律作为 closed=false 段返回。让 UI 层根据 streaming
/// 状态决定是否显示 shimmer。
List<ReasoningSegment> parseReasoning(String text, {bool isStreaming = false}) {
  if (text.isEmpty) return const [];

  final segments = <ReasoningSegment>[];
  var cursor = 0;

  while (cursor < text.length) {
    final openIdx = text.indexOf(_openTag, cursor);
    if (openIdx < 0) {
      // 没有更多 `<think>` —— 余下全部当 text。
      final tail = text.substring(cursor);
      if (tail.isNotEmpty) {
        segments.add(ReasoningSegment(
          kind: ReasoningSegmentKind.text,
          text: tail,
          closed: true,
        ));
      }
      break;
    }

    // `<think>` 之前的文本作为 text 段(可能为空,空就跳过)。
    if (openIdx > cursor) {
      segments.add(ReasoningSegment(
        kind: ReasoningSegmentKind.text,
        text: text.substring(cursor, openIdx),
        closed: true,
      ));
    }

    final innerStart = openIdx + _openTag.length;
    final closeIdx = text.indexOf(_closeTag, innerStart);
    if (closeIdx < 0) {
      // 未闭合 —— 余下全部作为 reasoning(closed=false)。
      final inner = text.substring(innerStart);
      segments.add(ReasoningSegment(
        kind: ReasoningSegmentKind.reasoning,
        text: inner,
        closed: false,
      ));
      break;
    }

    final inner = text.substring(innerStart, closeIdx);
    segments.add(ReasoningSegment(
      kind: ReasoningSegmentKind.reasoning,
      text: inner,
      closed: true,
    ));
    cursor = closeIdx + _closeTag.length;
  }

  return segments;
}

/// 检测 [text] 中是否含 `<think>` 起始标签 —— BlockRenderer 用来 fast-path
/// 跳过 parseReasoning(99% 普通模型不带 think 标签)。
bool hasReasoningTag(String text) => text.contains(_openTag);

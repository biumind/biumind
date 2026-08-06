// Stage 2 — splitSegments: 类型化分段。
//
// 输入: preNormalize 后的干净文本。
// 输出: 有序 List<Segment>。
//
// 算法 (单遍扫描):
//   1. 按 ^``` 边界切 fence 块 vs 非 fence 块
//   2. fence 块: 按 lang + 内容 + closed 决定 CodeSegment / MermaidSegment
//      / HtmlSegment / SvgSegment
//   3. 非 fence 块: 按 ^$$..$$ / ^\[..\] 边界再切 MathSegment vs MarkdownSegment
//
// 实现选择: 不引入完整 markdown parser。我们只关心 fence 边界 + 块级 math
// 边界, 行级 regex 扫描足够稳。其他 markdown 特性 (headings / lists /
// tables / inline) 全交给 MarkdownSegment 内部的 GptMarkdown 处理。

import 'segments.dart';

/// Mermaid 关键字 — 任意一条命中段首行即视为 mermaid。
/// 包含 v10+ 新图表类型 (xychart-beta / block-beta / sankey 等)。
const Set<String> _mermaidKeywords = {
  'sequenceDiagram',
  'flowchart',
  'graph',
  'classDiagram',
  'stateDiagram',
  'stateDiagram-v2',
  'erDiagram',
  'journey',
  'gantt',
  'pie',
  'gitGraph',
  'mindmap',
  'timeline',
  'requirementDiagram',
  'C4Context',
  'C4Container',
  'C4Component',
  'C4Dynamic',
  'C4Deployment',
  // v10+
  'quadrantChart',
  'xychart-beta',
  'block-beta',
  'sankey-beta',
  'sankey',
  'architecture-beta',
  'architecture',
  'packet-beta',
};

List<Segment> splitSegments(String input) {
  if (input.isEmpty) return const [];

  final out = <Segment>[];
  final lines = input.split('\n');
  var order = 0;

  // 当前积累的非 fence 缓冲。flushed 时决定走 markdown 还是 math。
  final buf = StringBuffer();
  void flushBuffer() {
    if (buf.isEmpty) return;
    final text = buf.toString();
    buf.clear();
    final segs = _splitNonFenceText(text, order);
    out.addAll(segs);
    order += segs.length;
  }

  var i = 0;
  while (i < lines.length) {
    final line = lines[i];
    // CommonMark: 开 fence 由 N (≥3) 个反引号 + 可选 lang 构成。
    // 闭 fence 必须 ≥N 个反引号且单独一行 (无其他字符)。
    // 这样 AI 用 ````markdown ... 4-tick 外层包 ``` 3-tick 内层做嵌套
    // 展示, 内层不会被误认为闭合外层。
    final fenceMatch =
        RegExp(r'^(`{3,})([a-zA-Z0-9_+\-]*)\s*$').firstMatch(line);
    if (fenceMatch == null) {
      // 普通行, 进 buffer
      if (buf.isNotEmpty) buf.write('\n');
      buf.write(line);
      i++;
      continue;
    }

    // 起 fence, flush 之前的 buffer
    flushBuffer();

    final fenceLen = (fenceMatch.group(1) ?? '').length;
    final lang = (fenceMatch.group(2) ?? '').toLowerCase().trim();
    // 闭合需要 ≥ fenceLen 个反引号; 同 N (`(`{N,})\s*$`)
    final closingPattern = RegExp('^`{$fenceLen,}\\s*\$');
    var j = i + 1;
    final body = StringBuffer();
    var closed = false;
    while (j < lines.length) {
      if (closingPattern.hasMatch(lines[j])) {
        closed = true;
        break;
      }
      if (body.isNotEmpty) body.write('\n');
      body.write(lines[j]);
      j++;
    }

    final code = body.toString();
    out.add(_classifyFence(
      order: order,
      lang: lang,
      code: code,
      closed: closed,
    ));
    order++;
    i = closed ? j + 1 : j; // 没闭合直接吃光
  }

  flushBuffer();
  return out;
}

/// 把 fence 块归类为 Mermaid / Html / Svg / Code。
Segment _classifyFence({
  required int order,
  required String lang,
  required String code,
  required bool closed,
}) {
  // R1: 显式 mermaid lang
  if (lang == 'mermaid' || lang == 'mmd') {
    return MermaidSegment(order: order, source: code, closed: closed);
  }

  // R4: html
  if (lang == 'html' || lang == 'htm') {
    return HtmlSegment(order: order, html: code, closed: closed);
  }

  // R5: svg
  if (lang == 'svg') {
    return SvgSegment(order: order, svg: code, closed: closed);
  }

  // R2/R3: lang 空 + 内容启发式
  if (lang.isEmpty) {
    final stripped = _stripLeadingMermaidLine(code);
    if (_looksLikeMermaidContent(stripped)) {
      return MermaidSegment(
          order: order, source: stripped, closed: closed);
    }
    if (_looksLikeSvg(code)) {
      return SvgSegment(order: order, svg: code, closed: closed);
    }
    if (_looksLikeHtml(code)) {
      return HtmlSegment(order: order, html: code, closed: closed);
    }
  }

  // R6: 普通 code
  return CodeSegment(
    order: order,
    language: lang,
    code: code,
    closed: closed,
  );
}

/// 内容首行是 "mermaid" 时剥掉 (允许 leading 空行 + %% 注释行)
/// 然后再让 _looksLikeMermaidContent 判别。
String _stripLeadingMermaidLine(String code) {
  final lines = code.split('\n');
  var i = 0;
  // 跳前导空行
  while (i < lines.length && lines[i].trim().isEmpty) {
    i++;
  }
  if (i >= lines.length) return code;
  final first = lines[i].trim().toLowerCase();
  if (first == 'mermaid') {
    return lines.sublist(i + 1).join('\n');
  }
  return code;
}

/// 首非空 / 非注释行匹配 mermaid 关键字。
bool _looksLikeMermaidContent(String code) {
  for (final raw in code.split('\n')) {
    final l = raw.trim();
    if (l.isEmpty) continue;
    if (l.startsWith('%%')) continue; // mermaid 注释
    return _mermaidKeywords.any((k) => l.startsWith(k));
  }
  return false;
}

bool _looksLikeSvg(String code) {
  final t = code.trimLeft();
  return t.startsWith('<svg') || t.startsWith('<?xml');
}

bool _looksLikeHtml(String code) {
  final t = code.trimLeft();
  // 简单启发式: 顶头是某种 HTML tag (含 doctype)
  return RegExp(r'^<!doctype', caseSensitive: false).hasMatch(t) ||
      RegExp(r'^<(html|body|div|section|article|main|header|footer|table|ul|ol|p|h[1-6])\b',
              caseSensitive: false)
          .hasMatch(t);
}

/// 把非 fence 文本切成 markdown / math (block 级别 $$..$$ 与 \[..\]) 段。
/// 行内 $..$ / \(..\) 不切 — 留给 GptMarkdown 内部 useDollarSignsForLatex。
List<Segment> _splitNonFenceText(String text, int startOrder) {
  if (text.isEmpty) return const [];

  final segments = <Segment>[];
  var order = startOrder;

  // 一遍扫: 行级 regex 找 ^$$ / ^\[ 边界
  final lines = text.split('\n');
  final mdBuf = StringBuffer();
  void flushMd() {
    final s = mdBuf.toString();
    mdBuf.clear();
    if (s.trim().isEmpty) return;
    segments.add(MarkdownSegment(order: order++, text: s));
  }

  var i = 0;
  while (i < lines.length) {
    final line = lines[i];
    // $$ 块开始 (允许同行写 $$..$$ 一行式)
    final inlineDoubleDollar = RegExp(r'^\s*\$\$(.+)\$\$\s*$').firstMatch(line);
    if (inlineDoubleDollar != null) {
      flushMd();
      segments.add(MathSegment(
        order: order++,
        latex: inlineDoubleDollar.group(1)!.trim(),
        display: true,
      ));
      i++;
      continue;
    }
    if (RegExp(r'^\s*\$\$\s*$').hasMatch(line)) {
      flushMd();
      // 找闭合 $$
      var j = i + 1;
      final mathBody = StringBuffer();
      while (j < lines.length && !RegExp(r'^\s*\$\$\s*$').hasMatch(lines[j])) {
        if (mathBody.isNotEmpty) mathBody.write('\n');
        mathBody.write(lines[j]);
        j++;
      }
      segments.add(MathSegment(
        order: order++,
        latex: mathBody.toString().trim(),
        display: true,
      ));
      i = (j < lines.length) ? j + 1 : j;
      continue;
    }
    // \[ ... \] 块开始 (LaTeX 转义形式)
    if (RegExp(r'^\s*\\\[\s*$').hasMatch(line)) {
      flushMd();
      var j = i + 1;
      final mathBody = StringBuffer();
      while (j < lines.length && !RegExp(r'^\s*\\\]\s*$').hasMatch(lines[j])) {
        if (mathBody.isNotEmpty) mathBody.write('\n');
        mathBody.write(lines[j]);
        j++;
      }
      segments.add(MathSegment(
        order: order++,
        latex: mathBody.toString().trim(),
        display: true,
      ));
      i = (j < lines.length) ? j + 1 : j;
      continue;
    }

    if (mdBuf.isNotEmpty) mdBuf.write('\n');
    mdBuf.write(line);
    i++;
  }
  flushMd();
  return segments;
}

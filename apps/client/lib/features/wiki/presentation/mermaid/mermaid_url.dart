// Mermaid → image URL encoder.
//
// We let mermaid.ink (or a self-hosted Kroki) render the diagram as an
// SVG/PNG image; the Flutter side just `Image.network`-s the URL. No
// in-app JS engine, no per-block webview — works the same on macOS,
// Linux, Windows, iOS, Android, and Web.
//
// Encoding格式 (mermaid.ink 现行):
//   https://mermaid.ink/img/pako:<base64url(deflate(json))>
//
// 比 legacy `/img/<base64(json)>` 更稳 — 后者对 CJK / 复杂语法
// (sequenceDiagram + 中文 participant 名等) 偶尔返回 500。pako 编码是
// mermaid.live 自己用的格式, mermaid.ink 也认。
//
// 为啥不用 dart:io ZLibCodec: 不能跨 Flutter Web。`package:archive`
// 是纯 Dart 实现, Flutter Web 也跑得动 (本来就是 drift / chat repo 等的
// 传递依赖)。

import 'dart:convert';

import 'package:archive/archive.dart';

const defaultMermaidEndpoint = 'https://mermaid.ink';

/// True when the block content is recognisably mermaid source — either
/// `content.lang == 'mermaid'` or the first non-empty line starts with
/// a mermaid diagram keyword. The keyword sniff catches research-LLM
/// output that produces fenced ```mermaid blocks without setting lang.
bool isMermaidCode({String? lang, String? text}) {
  if (lang != null && lang.toLowerCase() == 'mermaid') return true;
  if (text == null) return false;
  for (final raw in text.split('\n')) {
    final l = raw.trim();
    if (l.isEmpty) continue;
    return _mermaidKeywords.any(l.startsWith);
  }
  return false;
}

const _mermaidKeywords = <String>[
  'graph ',
  'graph\n',
  'flowchart ',
  'sequenceDiagram',
  'classDiagram',
  'stateDiagram',
  'erDiagram',
  'journey',
  'gantt',
  'pie ',
  'pie\n',
  'gitGraph',
  'mindmap',
  'timeline',
  'requirementDiagram',
  'C4Context',
];

/// Builds the mermaid.ink (or self-hosted Kroki) image URL for `source`.
///
/// `format` controls the response: 'svg' (crisp at any zoom, supports
/// Flutter's network-image SVG decoder) or 'img' (PNG, universal but
/// rasterised). We default to 'img' because Flutter's standard
/// `Image.network` doesn't decode SVG without `flutter_svg`.
String mermaidImageUrl(
  String source, {
  String endpoint = defaultMermaidEndpoint,
  String format = 'img',
}) {
  // Payload shape 匹配 mermaid.live 的 EditorState — 这样同一份 URL 既
  // 能给 mermaid.ink 渲染图, 也能让用户点击 "在 mermaid.live 打开" 调试。
  //
  // ⚠️ `mermaid` 字段必须是 **字符串** (serialized JSON), 不是对象 —
  // 服务端会 `JSON.parse(state.mermaid)` 二次解析。传对象进去会变
  // `[object Object]` 直接挂。这是 jihchi/mermaid.ink 源码的硬约束。
  final payload = jsonEncode({
    'code': source,
    'mermaid': jsonEncode({'theme': 'default'}),
    'autoSync': true,
    'updateDiagram': true,
  });
  final compressed = ZLibEncoder().encode(utf8.encode(payload));
  // base64url, 去掉 padding — mermaid.ink 文档明确要求 URL-safe 字符集。
  final encoded =
      base64UrlEncode(compressed).replaceAll(RegExp(r'=+$'), '');
  final base = endpoint.endsWith('/')
      ? endpoint.substring(0, endpoint.length - 1)
      : endpoint;
  return '$base/$format/pako:$encoded';
}

/// 给"在 mermaid.live 打开"按钮用 — pako 格式的 fragment URL。
/// mermaid.live 期望 `/edit#pako:<encoded>` (note: # fragment, 不是
/// path; / 和 base64url 字符不需要 url-encode)。
String mermaidLiveEditUrl(String source) {
  final payload = jsonEncode({
    'code': source,
    'mermaid': jsonEncode({'theme': 'default'}),
    'autoSync': true,
    'updateDiagram': true,
  });
  final compressed = ZLibEncoder().encode(utf8.encode(payload));
  final encoded =
      base64UrlEncode(compressed).replaceAll(RegExp(r'=+$'), '');
  return 'https://mermaid.live/edit#pako:$encoded';
}

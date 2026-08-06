// M10.3 (LaTeX 层) — RSS 正文 LaTeX 渲染。
//
// RSS 正文走 HtmlWidget(flutter_widget_from_html),不是 markdown,所以
// 不能直接复用 chat 侧的 splitSegments。这里走两步:
//
//   1. injectDisplayTex(html): HTML 感知地把「文本区」里的块级公式定界符
//      ($$..$$ / \[..\]) 替换成自定义元素 <x-tex>base64(latex)</x-tex>。
//      跳过 <pre>/<code>/<script>/<style> 里的内容(代码示例里的 $$ 不是公式),
//      且只在「单个文本节点内」匹配 —— 定界符若跨标签则不识别,避免吞掉 HTML。
//   2. reader_pane 把 <x-tex> 经 HtmlWidget.customWidgetBuilder 渲成 RssMathBlock,
//      customStylesBuilder 给它 display:block 强制块级。
//
// 与 chat 侧 V1 一致:只渲「块级」公式。行内 $..$ / \(..\) 不碰 —— 单 $ 极易
// 与货币($5 / $10)混淆,误转会把正文打烂,弊大于利。行内留作后续 milestone。
//
// 容错:latex 语法错误时 Math.tex 的 onErrorFallback 回原始源码,不抛异常、
// 不空白。宽公式(矩阵/长推导)横向滚动,不撑破阅读栏。

import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_math_fork/flutter_math.dart';

import '../../../../../app/theme.dart';
import '../rss_tokens.dart';

/// 不参与公式扫描的「原始文本」元素 —— 里面的 $$ 是字面量(代码/脚本/样式)。
const Set<String> _rawTextTags = {'pre', 'code', 'script', 'style'};

/// 自定义元素名 + 标记前缀。base64 放在元素文本里,避免 latex 里的
/// `<` `>` `&` `"` 破坏 HTML 属性 / 标签。
const String kTexTag = 'x-tex';

/// 把 html 里「文本区」的块级公式定界符替换成 `<x-tex>base64</x-tex>`。
///
/// 纯函数,可单测。HTML 结构(标签、属性、原始文本块)原样保留。
String injectDisplayTex(String html) {
  if (html.isEmpty || (!html.contains(r'$$') && !html.contains(r'\['))) {
    return html; // 快速短路:没有任何块级定界符
  }

  final out = StringBuffer();
  final rawTextStack = <String>[]; // 当前嵌套的原始文本标签
  var i = 0;
  final n = html.length;

  while (i < n) {
    final ch = html[i];
    if (ch == '<') {
      // 进入一个标签:整段原样拷贝到 '>',并维护原始文本栈。
      final gt = html.indexOf('>', i);
      if (gt < 0) {
        out.write(html.substring(i)); // 残缺标签,直接吐完
        break;
      }
      final tag = html.substring(i, gt + 1);
      out.write(tag);
      _trackRawText(tag, rawTextStack);
      i = gt + 1;
      continue;
    }

    // 文本区:扫到下一个 '<' 为止,作为一个文本节点处理。
    final lt = html.indexOf('<', i);
    final end = lt < 0 ? n : lt;
    final text = html.substring(i, end);
    if (rawTextStack.isEmpty) {
      out.write(_replaceDisplayMath(text));
    } else {
      out.write(text); // pre/code/script/style 内,字面保留
    }
    i = end;
  }
  return out.toString();
}

/// 维护原始文本标签栈。tag 形如 `<pre ...>` / `</pre>` / `<br/>`。
void _trackRawText(String tag, List<String> stack) {
  // 自闭合标签不入栈。
  if (tag.endsWith('/>')) return;
  final m = RegExp(r'^</?\s*([a-zA-Z0-9]+)').firstMatch(tag);
  if (m == null) return;
  final name = m.group(1)!.toLowerCase();
  if (!_rawTextTags.contains(name)) return;
  if (tag.startsWith('</')) {
    // 关闭:弹出最近的同名
    final idx = stack.lastIndexOf(name);
    if (idx >= 0) stack.removeAt(idx);
  } else {
    stack.add(name);
  }
}

/// 在一段纯文本里把 $$..$$ 与 \[..\] 替换成 `<x-tex>`。定界符必须在同一段文本
/// 内闭合(本函数的输入已是单个文本节点,天然不跨标签)。
String _replaceDisplayMath(String text) {
  if (!text.contains(r'$$') && !text.contains(r'\[')) return text;

  final out = StringBuffer();
  var i = 0;
  final n = text.length;
  while (i < n) {
    // $$ ... $$
    if (i + 1 < n && text[i] == r'$' && text[i + 1] == r'$') {
      final close = text.indexOf(r'$$', i + 2);
      if (close > i + 1) {
        final latex = text.substring(i + 2, close).trim();
        if (latex.isNotEmpty) {
          out.write(_texElement(latex));
          i = close + 2;
          continue;
        }
      }
    }
    // \[ ... \]
    if (i + 1 < n && text[i] == r'\' && text[i + 1] == '[') {
      final close = text.indexOf(r'\]', i + 2);
      if (close > i + 1) {
        final latex = text.substring(i + 2, close).trim();
        if (latex.isNotEmpty) {
          out.write(_texElement(latex));
          i = close + 2;
          continue;
        }
      }
    }
    out.write(text[i]);
    i++;
  }
  return out.toString();
}

String _texElement(String rawLatex) {
  final latex = _unescapeHtml(rawLatex);
  final b64 = base64.encode(utf8.encode(latex));
  return '<$kTexTag>$b64</$kTexTag>';
}

/// 解码 `<x-tex>` 元素文本(base64)回 latex 源码。解析失败回退原文本。
String decodeTex(String elementText) {
  try {
    return utf8.decode(base64.decode(elementText.trim()));
  } catch (_) {
    return elementText;
  }
}

/// 文本节点里的公式可能带 HTML 实体(如 a &lt; b),Math.tex 要原始字符。
String _unescapeHtml(String s) {
  if (!s.contains('&')) return s;
  return s
      .replaceAll('&lt;', '<')
      .replaceAll('&gt;', '>')
      .replaceAll('&quot;', '"')
      .replaceAll('&#39;', "'")
      .replaceAll('&apos;', "'")
      .replaceAll('&amp;', '&'); // amp 最后,避免二次解码
}

/// 块级公式渲染:居中 + 宽公式横向滚动 + 出错回退源码。
class RssMathBlock extends StatelessWidget {
  const RssMathBlock({super.key, required this.latex});

  final String latex;

  @override
  Widget build(BuildContext context) {
    final color = RssReaderColors.text(context);
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 8),
      child: SingleChildScrollView(
        scrollDirection: Axis.horizontal,
        child: Math.tex(
          latex,
          mathStyle: MathStyle.display,
          textStyle: TextStyle(color: color, fontSize: 16),
          onErrorFallback: (e) => SelectableText(
            latex,
            style: TextStyle(
              fontFamily: 'monospace',
              fontSize: 13,
              color: BiuTokens.textMuted,
            ),
          ),
        ),
      ),
    );
  }
}

import 'dart:convert';

import 'package:biumind/features/apps/builtin/rss/widgets/latex_html.dart';
import 'package:flutter_test/flutter_test.dart';

// 从 injectDisplayTex 产物里抽出第一个 <x-tex> 的 latex 源码(base64 解码)。
String? firstTex(String html) {
  final m = RegExp('<$kTexTag>([^<]*)</$kTexTag>').firstMatch(html);
  if (m == null) return null;
  return decodeTex(m.group(1)!);
}

int texCount(String html) =>
    RegExp('<$kTexTag>').allMatches(html).length;

void main() {
  group('injectDisplayTex', () {
    test(r'$$..$$ 块级公式被替换为 <x-tex> 且 base64 可解回原 latex', () {
      const html = r'<p>能量质量关系:$$E = mc^2$$ 很经典。</p>';
      final out = injectDisplayTex(html);
      expect(texCount(out), 1);
      expect(firstTex(out), r'E = mc^2');
      // 原 $$ 定界符消失,周边文本保留
      expect(out.contains(r'$$'), isFalse);
      expect(out.contains('很经典'), isTrue);
      expect(out.contains('<p>'), isTrue);
    });

    test(r'\[..\] 块级公式同样被替换', () {
      const html = r'<div>\[\int_0^1 x\,dx\]</div>';
      final out = injectDisplayTex(html);
      expect(texCount(out), 1);
      expect(firstTex(out), r'\int_0^1 x\,dx');
    });

    test(r'一段里多个 $$ 公式各自独立替换', () {
      const html = r'<p>$$a+b$$ 和 $$c-d$$</p>';
      final out = injectDisplayTex(html);
      expect(texCount(out), 2);
    });

    test(r'<code> / <pre> 内的 $$ 是代码字面量,不动', () {
      const html = r'<pre>shell: echo $$PID</pre><code>$$x$$</code>';
      final out = injectDisplayTex(html);
      expect(texCount(out), 0);
      expect(out, html); // 完全不变
    });

    test(r'行内单 $ (货币) 不被误转', () {
      const html = r'<p>定价 $5 起,折后 $3。</p>';
      final out = injectDisplayTex(html);
      expect(texCount(out), 0);
      expect(out, html);
    });

    test(r'未闭合的 $$ 原样保留,不吞后文', () {
      const html = r'<p>开了个 $$E=mc^2 但没闭合</p>';
      final out = injectDisplayTex(html);
      expect(texCount(out), 0);
      expect(out, html);
    });

    test('公式里的 HTML 实体被反转义回原始字符', () {
      const html = r'<p>$$a &lt; b &amp; c$$</p>';
      final out = injectDisplayTex(html);
      expect(firstTex(out), r'a < b & c');
    });

    test('没有任何定界符时快速短路,原样返回', () {
      const html = '<p>普通文章,没有公式。</p>';
      expect(injectDisplayTex(html), html);
    });

    test(r'$$ 定界符跨标签时不识别(只在单文本节点内匹配)', () {
      // $$ 开在 <p>,闭在 <span> 外 —— 不应跨标签吞掉中间的 HTML
      const html = r'<p>$$a</p><p>b$$</p>';
      final out = injectDisplayTex(html);
      expect(texCount(out), 0);
    });

    test(r'空公式 $$$$ 不产生 <x-tex>', () {
      const html = r'<p>$$$$</p>';
      final out = injectDisplayTex(html);
      expect(texCount(out), 0);
    });
  });

  group('decodeTex', () {
    test('round-trip', () {
      const latex = r'\frac{1}{2}\sum_{i=0}^{n} x_i';
      final b64 = base64.encode(utf8.encode(latex));
      expect(decodeTex(b64), latex);
    });

    test('非法 base64 回退原文本', () {
      expect(decodeTex('!!!not-base64!!!'), '!!!not-base64!!!');
    });
  });
}

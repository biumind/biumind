// pre_normalize 5 条规则的单测。Fixture 来自实际 AI 输出。

import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:biumind/features/chat/markdown/pre_normalize.dart';

void main() {
  group('N1 fence normalization', () {
    test('tilde fence → backtick fence', () {
      const input = '~~~python\nprint(1)\n~~~';
      final out = preNormalize(input);
      expect(out, contains('```python'));
      expect(out, isNot(contains('~~~')));
    });

    test('4-backtick fence 不动 (留给 split 处理嵌套)', () {
      const input = '````markdown\n```mermaid\ngraph TD\n```\n````';
      final out = preNormalize(input);
      expect(out, contains('````markdown'));
      expect(out, contains('````'));
      // 内层 ```mermaid 也保留
      expect(out, contains('```mermaid'));
    });

    test('idempotent — 跑两次结果一样', () {
      const input = '~~~python\nprint(1)\n~~~';
      expect(preNormalize(preNormalize(input)), preNormalize(input));
    });
  });

  group('N2 HTML pre/code unwrap', () {
    test('language-mermaid 反包裹', () {
      const input = '<pre><code class="language-mermaid">\n'
          'sequenceDiagram\nA->>B: hi\n</code></pre>';
      final out = preNormalize(input);
      expect(out, contains('```mermaid'));
      expect(out, contains('sequenceDiagram'));
      expect(out, isNot(contains('<pre>')));
    });

    test('language-python 反包裹', () {
      const input =
          '<pre><code class="language-python">print("hi")</code></pre>';
      final out = preNormalize(input);
      expect(out, contains('```python'));
      expect(out, contains('print'));
    });

    test('没 class 的 <pre><code> 不动', () {
      const input = '<pre><code>raw</code></pre>';
      final out = preNormalize(input);
      // 不命中 N2 (没 language-X class)
      expect(out, contains('<pre>'));
    });
  });

  group('N3 fence lang 剥参数', () {
    test('mermaid theme=dark → mermaid', () {
      const input = '```mermaid theme=dark\ngraph TD\nA-->B\n```';
      final out = preNormalize(input);
      expect(out, contains('```mermaid\n'));
      expect(out, isNot(contains('theme=dark')));
    });

    test('python {.numbered} → python', () {
      const input = '```python {.numbered}\nprint(1)\n```';
      final out = preNormalize(input);
      expect(out, contains('```python\n'));
    });

    test('无参的 ```mermaid 不动', () {
      const input = '```mermaid\ngraph TD\n```';
      expect(preNormalize(input), input);
    });
  });

  group('N4 内容首行 promote 成 lang', () {
    test('空 lang + 首行 mermaid → lang=mermaid + 内容剥首行', () {
      const input = '```\nmermaid\nsequenceDiagram\nA->>B\n```';
      final out = preNormalize(input);
      expect(out, contains('```mermaid\n'));
      expect(out, contains('sequenceDiagram'));
      expect(out, isNot(matches(RegExp(r'^mermaid$', multiLine: true))));
    });

    test('空 lang + 首行 python → lang=python', () {
      const input = '```\npython\nprint(1)\n```';
      final out = preNormalize(input);
      expect(out, contains('```python\n'));
    });

    test('空 lang + 首行未知字 → 不动', () {
      const input = '```\nrandomtext\nmore content\n```';
      final out = preNormalize(input);
      // 仍是空 lang fence
      expect(out, startsWith('```\n'));
      expect(out, contains('randomtext'));
    });

    test('已有 lang 的 fence 不动', () {
      const input = '```bash\necho hi\n```';
      expect(preNormalize(input), input);
    });
  });

  group('N5 trim trailing', () {
    test('尾部空白删', () {
      expect(preNormalize('hello\n\n\n'), 'hello');
    });

    test('前导空白保留 (缩进)', () {
      const input = '  indented';
      expect(preNormalize(input), input);
    });
  });

  group('整体串联', () {
    test('空字符串安全', () {
      expect(preNormalize(''), '');
    });

    test('纯文本不动 (除尾部 trim)', () {
      const input = 'hello world';
      expect(preNormalize(input), input);
    });
  });
}

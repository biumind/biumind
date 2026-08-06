// split_segments 测试: R1-R7 + math 块 + markdown 兜底 + 流式 closed=false。

import 'package:flutter_test/flutter_test.dart';
import 'package:biumind/features/chat/markdown/segments.dart';
import 'package:biumind/features/chat/markdown/split_segments.dart';

void main() {
  group('mermaid R1 显式 lang', () {
    test('lang=mermaid → MermaidSegment', () {
      final segs = splitSegments('```mermaid\ngraph TD\nA-->B\n```');
      expect(segs, hasLength(1));
      expect(segs.first, isA<MermaidSegment>());
      expect((segs.first as MermaidSegment).source, contains('graph TD'));
      expect(segs.first.closed, isTrue);
    });

    test('lang=mmd 别名也行', () {
      final segs = splitSegments('```mmd\nflowchart LR\nA-->B\n```');
      expect(segs.first, isA<MermaidSegment>());
    });
  });

  group('mermaid R2 空 lang + 关键字首行', () {
    test('sequenceDiagram', () {
      final segs = splitSegments('```\nsequenceDiagram\nA->>B: hi\n```');
      expect(segs.first, isA<MermaidSegment>());
    });

    test('classDiagram', () {
      final segs = splitSegments('```\nclassDiagram\nclass Animal\n```');
      expect(segs.first, isA<MermaidSegment>());
    });

    test('xychart-beta (v10+)', () {
      final segs = splitSegments('```\nxychart-beta\n  title "demo"\n```');
      expect(segs.first, isA<MermaidSegment>());
    });

    test('block-beta (v10+)', () {
      final segs = splitSegments('```\nblock-beta\n  A B C\n```');
      expect(segs.first, isA<MermaidSegment>());
    });

    test('普通 python 代码 → CodeSegment, 不当 mermaid', () {
      final segs = splitSegments('```\nprint("hello")\n```');
      expect(segs.first, isA<CodeSegment>());
    });
  });

  group('mermaid R3 首行误写 mermaid', () {
    test('strip 首行 mermaid + 关键字第二行', () {
      final segs = splitSegments(
          '```\nmermaid\nsequenceDiagram\nA->>B: hi\n```');
      expect(segs.first, isA<MermaidSegment>());
      final src = (segs.first as MermaidSegment).source;
      expect(src, startsWith('sequenceDiagram'));
      expect(src, isNot(contains('mermaid\n')));
    });

    test('strip 首行 mermaid + 注释行 + 关键字', () {
      final segs = splitSegments(
          '```\nmermaid\n%% comment\ngraph TD\nA-->B\n```');
      expect(segs.first, isA<MermaidSegment>());
    });

    test('首行 mermaid 但后面不是关键字 → 普通 code', () {
      final segs =
          splitSegments('```\nmermaid\nthis is just text\n```');
      expect(segs.first, isA<CodeSegment>());
    });
  });

  group('html / svg 段', () {
    test('lang=html → HtmlSegment', () {
      final segs =
          splitSegments('```html\n<div>Hello</div>\n```');
      expect(segs.first, isA<HtmlSegment>());
    });

    test('lang=svg → SvgSegment', () {
      final segs = splitSegments(
          '```svg\n<svg xmlns="..."><rect/></svg>\n```');
      expect(segs.first, isA<SvgSegment>());
    });

    test('空 lang + <svg 内容 → SvgSegment', () {
      final segs = splitSegments(
          '```\n<svg xmlns="http://www.w3.org/2000/svg"><rect/></svg>\n```');
      expect(segs.first, isA<SvgSegment>());
    });

    test('空 lang + <div 内容 → HtmlSegment', () {
      final segs =
          splitSegments('```\n<div class="x">Hi</div>\n```');
      expect(segs.first, isA<HtmlSegment>());
    });
  });

  group('普通 code', () {
    test('lang=python → CodeSegment lang=python', () {
      final segs =
          splitSegments('```python\nprint("hello")\n```');
      expect(segs.first, isA<CodeSegment>());
      expect((segs.first as CodeSegment).language, 'python');
    });

    test('lang 大小写归一', () {
      final segs = splitSegments('```Python\nprint(1)\n```');
      expect((segs.first as CodeSegment).language, 'python');
    });

    test('空 lang + 普通文本 → CodeSegment lang=空', () {
      final segs = splitSegments('```\nthis is plain text\n```');
      expect(segs.first, isA<CodeSegment>());
      expect((segs.first as CodeSegment).language, '');
    });
  });

  group('math', () {
    test(r'$$..$$ 块 → MathSegment display=true', () {
      final segs = splitSegments('Some text\n\n\$\$\n\\int_0^1 x\\,dx\n\$\$');
      // 期望: 第一段 markdown, 第二段 math
      expect(segs, hasLength(2));
      expect(segs[0], isA<MarkdownSegment>());
      expect(segs[1], isA<MathSegment>());
      expect((segs[1] as MathSegment).latex, contains('int'));
      expect((segs[1] as MathSegment).display, isTrue);
    });

    test(r'inline $$..$$ 一行也认', () {
      final segs = splitSegments(r'$$E = mc^2$$');
      expect(segs, hasLength(1));
      expect(segs.first, isA<MathSegment>());
      expect((segs.first as MathSegment).latex, 'E = mc^2');
    });

    test(r'\[..\] LaTeX 转义形式', () {
      final segs = splitSegments('text\n\n\\[\nx + 1\n\\]\n\nmore');
      expect(segs, hasLength(3));
      expect(segs[0], isA<MarkdownSegment>());
      expect(segs[1], isA<MathSegment>());
      expect(segs[2], isA<MarkdownSegment>());
    });

    test(r'行内 $..$ 不切 (留给 markdown)', () {
      final segs = splitSegments(r'inline math $E=mc^2$ in text');
      expect(segs, hasLength(1));
      expect(segs.first, isA<MarkdownSegment>());
      expect((segs.first as MarkdownSegment).text, contains(r'$E=mc^2$'));
    });
  });

  group('混合输入', () {
    test('markdown + mermaid + code 顺序保留', () {
      final segs = splitSegments(
          '# Title\n\n```mermaid\ngraph TD\nA-->B\n```\n\nSome text\n\n'
          '```python\nprint(1)\n```');
      expect(segs.map((s) => s.runtimeType.toString()).toList(), [
        'MarkdownSegment',
        'MermaidSegment',
        'MarkdownSegment',
        'CodeSegment',
      ]);
      // order 单调递增
      for (var i = 1; i < segs.length; i++) {
        expect(segs[i].order, segs[i - 1].order + 1);
      }
    });
  });

  group('流式 (fence 未闭合)', () {
    test('未闭合 mermaid → MermaidSegment closed=false', () {
      final segs = splitSegments('```mermaid\ngraph TD\nA-->B');
      expect(segs.first, isA<MermaidSegment>());
      expect(segs.first.closed, isFalse);
    });

    test('未闭合 code → CodeSegment closed=false', () {
      final segs = splitSegments('```python\nprint(1)');
      expect(segs.first, isA<CodeSegment>());
      expect(segs.first.closed, isFalse);
    });
  });

  group('多反引号 fence (CommonMark 嵌套)', () {
    test('4-tick 外层包 3-tick 内层 → 单个 CodeSegment lang=markdown', () {
      // AI showcase 经典写法: 外层 4 反引号包 3 反引号内层。
      // 整体应是 1 个 CodeSegment, 内层 ``` 是字面体不被当 fence。
      const src = '````markdown\n```mermaid\ngraph TD\nA-->B\n```\n````';
      final segs = splitSegments(src);
      expect(segs, hasLength(1));
      expect(segs.first, isA<CodeSegment>());
      final code = segs.first as CodeSegment;
      expect(code.language, 'markdown');
      expect(code.code, contains('```mermaid'));
      expect(code.code, contains('graph TD'));
    });

    test('5-tick 外层包 4-tick 包 3-tick', () {
      const src = '`````\n````md\n```mermaid\nA-->B\n```\n````\n`````';
      final segs = splitSegments(src);
      expect(segs, hasLength(1));
      expect(segs.first, isA<CodeSegment>());
    });

    test('4-tick 外层 + 普通文本 (无内嵌 fence) — 仍正常', () {
      const src = '````\nplain text\n````';
      final segs = splitSegments(src);
      expect(segs, hasLength(1));
      expect(segs.first, isA<CodeSegment>());
    });

    test('独立 3-tick 不被 4-tick 闭合误认', () {
      const src = '````python\nprint("```")\n````';
      final segs = splitSegments(src);
      expect(segs, hasLength(1));
      expect((segs.first as CodeSegment).code, contains('print'));
    });
  });

  group('边界', () {
    test('空字符串', () {
      expect(splitSegments(''), isEmpty);
    });

    test('纯文本无 fence 无 math', () {
      final segs = splitSegments('hello world');
      expect(segs, hasLength(1));
      expect(segs.first, isA<MarkdownSegment>());
    });
  });
}

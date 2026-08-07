// markdown_render_test — verifies the chat Markdown integration renders
// KaTeX, Mermaid, and Tables.
//
// The chat renderer lives in features/chat/markdown/pipeline.dart and is
// invoked from message_bubble_v2; since P0 of the text-selection design it
// renders via SelectableMdWidget (markdown/views/selectable_md_widget.dart),
// which reuses this same gpt_markdown parsing pipeline. Rather than pulling
// in its full dependency graph, we re-construct the same config here and
// assert the three behaviors at the library boundary:
//
//   1. `useDollarSignsForLatex: true` → `$x^2$` produces a `Math` widget
//      (from flutter_math_fork) instead of plain text.
//   2. `codeBuilder` recognizing ```mermaid``` swaps to `MermaidPreview`
//      (and falls back to the raw code block while the fence is still
//      streaming-open).
//   3. `tableBuilder` is invoked for GFM tables (a custom Table widget
//      replaces gpt_markdown's default hard-bordered renderer).

import 'package:flutter/material.dart';
import 'package:flutter_math_fork/flutter_math.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:gpt_markdown/gpt_markdown.dart';

import 'package:biumind/features/wiki/presentation/mermaid/mermaid_preview.dart';

/// Builds the same GptMarkdown configuration the chat renderer uses.
Widget _markdownBody(String text) {
  return MaterialApp(
    home: Scaffold(
      body: GptMarkdown(
        text,
        useDollarSignsForLatex: true,
        codeBuilder: (context, name, code, closed) {
          if (name.toLowerCase() == 'mermaid') {
            if (!closed) {
              return _RawCodeStub(code: code, language: name);
            }
            return MermaidPreview(source: code);
          }
          return _RawCodeStub(code: code, language: name);
        },
        tableBuilder: (context, rows, style, cfg) =>
            _StubTable(rows: rows, key: const Key('stub-table')),
      ),
    ),
  );
}

class _RawCodeStub extends StatelessWidget {
  const _RawCodeStub({required this.code, required this.language});
  final String code;
  final String language;
  @override
  Widget build(BuildContext context) =>
      Text('CODE[$language]:$code', key: const Key('raw-code-stub'));
}

class _StubTable extends StatelessWidget {
  const _StubTable({required this.rows, super.key});
  final List<CustomTableRow> rows;
  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        for (final r in rows)
          Text(
            '${r.isHeader ? 'H' : 'R'}|${r.fields.map((f) => f.data.trim()).join('|')}',
          ),
      ],
    );
  }
}

void main() {
  group('chat markdown integration', () {
    testWidgets('renders inline LaTeX via dollar signs', (tester) async {
      await tester.pumpWidget(_markdownBody(r'Energy formula: $E = mc^2$.'));
      await tester.pumpAndSettle();
      // flutter_math_fork's Math widget should appear when LaTeX is parsed.
      expect(find.byType(Math), findsAtLeastNWidgets(1));
    });

    testWidgets(r'renders block LaTeX with $$...$$', (tester) async {
      await tester.pumpWidget(
        _markdownBody('Here it is:\n\n\$\$\\int_0^1 x^2 \\,dx = \\frac{1}{3}\$\$\n'),
      );
      await tester.pumpAndSettle();
      expect(find.byType(Math), findsAtLeastNWidgets(1));
    });

    testWidgets('routes ```mermaid``` to MermaidPreview when fence is closed',
        (tester) async {
      const md = '```mermaid\ngraph LR\nA-->B\n```';
      await tester.pumpWidget(_markdownBody(md));
      await tester.pumpAndSettle(const Duration(milliseconds: 100));
      expect(find.byType(MermaidPreview), findsOneWidget);
      expect(find.byKey(const Key('raw-code-stub')), findsNothing);
    });

    testWidgets('falls back to raw code while mermaid fence is still open',
        (tester) async {
      // No closing ``` — simulate streaming partial output.
      const md = '```mermaid\ngraph LR\nA-->B\n';
      await tester.pumpWidget(_markdownBody(md));
      await tester.pumpAndSettle(const Duration(milliseconds: 50));
      expect(find.byType(MermaidPreview), findsNothing);
      expect(find.byKey(const Key('raw-code-stub')), findsOneWidget);
    });

    testWidgets('non-mermaid code blocks still go through codeBuilder',
        (tester) async {
      const md = '```python\nprint("hi")\n```';
      await tester.pumpWidget(_markdownBody(md));
      await tester.pumpAndSettle();
      expect(find.byType(MermaidPreview), findsNothing);
      expect(find.byKey(const Key('raw-code-stub')), findsOneWidget);
    });

    testWidgets('GFM table invokes the custom tableBuilder', (tester) async {
      const md = '| Name | Score |\n| --- | --- |\n| Alice | 90 |\n| Bob | 75 |\n';
      await tester.pumpWidget(_markdownBody(md));
      await tester.pumpAndSettle();
      expect(find.byKey(const Key('stub-table')), findsOneWidget);
      // Header + 2 data rows = 3 lines from our stub.
      expect(find.text('H|Name|Score'), findsOneWidget);
      expect(find.text('R|Alice|90'), findsOneWidget);
      expect(find.text('R|Bob|75'), findsOneWidget);
    });
  });
}

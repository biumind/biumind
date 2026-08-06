// Markdown 段渲染 — 把 GptMarkdown 用作 markdown 文本块的渲染器。
//
// codeBuilder 不再做特殊分发 (mermaid/html/svg 已经在 split 阶段抽走)
// — 这里走到的 fenced code 都是普通 code 段, 应当不存在 (split 把它们
// 也抽出去了)。但保留 codeBuilder 作 fallback, 防御性的把异常的
// code 渲染兜底。
//
// tableBuilder 用自定义实现 — gpt_markdown 默认 TableBorder.all 太黑。
// LaTeX (行内 $..$ 和块级被这里 _不_ 处理 — block math 已经在 split
// 切走; inline math 留给 useDollarSignsForLatex 的 flutter_math_fork。

import 'package:flutter/material.dart';
import 'package:gpt_markdown/gpt_markdown.dart';

import '../../../../app/theme.dart';
import 'code_segment_view.dart';

class MarkdownSegmentView extends StatelessWidget {
  const MarkdownSegmentView({super.key, required this.text});

  final String text;

  @override
  Widget build(BuildContext context) {
    return GptMarkdown(
      text,
      style: TextStyle(
        color: BiuTokens.text,
        fontSize: 14.5,
        height: 1.7,
      ),
      useDollarSignsForLatex: true,
      // Defensive: 走到这儿的 code 是 split 漏的; 直接退给 CodeSegmentView。
      codeBuilder: (context, name, code, closed) => CodeSegmentView(
        language: name,
        code: code,
        closed: closed,
      ),
      tableBuilder: (context, rows, style, cfg) =>
          _MarkdownTable(rows: rows, baseStyle: style),
    );
  }
}

// 表格: 圆角描边 + 表头浅灰底 + 行间细线 + 横向滚动。
class _MarkdownTable extends StatelessWidget {
  const _MarkdownTable({required this.rows, required this.baseStyle});

  final List<CustomTableRow> rows;
  final TextStyle baseStyle;

  @override
  Widget build(BuildContext context) {
    if (rows.isEmpty) return const SizedBox.shrink();
    final maxCol =
        rows.map((r) => r.fields.length).fold<int>(0, (a, b) => a > b ? a : b);
    final headerStyle = baseStyle.copyWith(
      fontWeight: FontWeight.w600,
      color: BiuTokens.text,
    );
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: BiuTokens.space2),
      child: ClipRRect(
        borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
        child: Container(
          decoration: BoxDecoration(
            border: Border.all(color: BiuTokens.borderSubtle),
            borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
          ),
          child: SingleChildScrollView(
            scrollDirection: Axis.horizontal,
            child: Table(
              defaultColumnWidth: const IntrinsicColumnWidth(),
              defaultVerticalAlignment: TableCellVerticalAlignment.middle,
              border: TableBorder(
                horizontalInside:
                    BorderSide(color: BiuTokens.borderSubtle, width: 0.5),
              ),
              children: [
                for (final row in rows)
                  TableRow(
                    decoration: row.isHeader
                        ? BoxDecoration(color: BiuTokens.surfaceMuted)
                        : null,
                    children: [
                      for (var i = 0; i < maxCol; i++)
                        _MarkdownCell(
                          field: i < row.fields.length
                              ? row.fields[i]
                              : null,
                          style: row.isHeader ? headerStyle : baseStyle,
                        ),
                    ],
                  ),
              ],
            ),
          ),
        ),
      ),
    );
  }
}

class _MarkdownCell extends StatelessWidget {
  const _MarkdownCell({required this.field, required this.style});
  final CustomTableField? field;
  final TextStyle style;

  @override
  Widget build(BuildContext context) {
    final f = field;
    if (f == null) return const SizedBox.shrink();
    Alignment alignment;
    switch (f.alignment) {
      case TextAlign.right:
        alignment = Alignment.centerRight;
        break;
      case TextAlign.center:
        alignment = Alignment.center;
        break;
      default:
        alignment = Alignment.centerLeft;
    }
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 6),
      child: Align(
        alignment: alignment,
        child: Text(f.data.trim(), style: style),
      ),
    );
  }
}

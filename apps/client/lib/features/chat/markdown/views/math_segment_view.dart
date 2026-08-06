// Math 段渲染 — flutter_math_fork 的 Math.tex.
//
// V1 splitter 只产 display=true (块级 $$..$$ / \[..\])。行内 $..$ 让
// MarkdownSegmentView 内部的 GptMarkdown 处理。
//
// 容错: latex 语法错误时 onErrorFallback 回原始字符串, 不抛 exception。
//
// V2 加 hover 复制按钮 — 公式渲染好不直观，复制源码方便贴 Obsidian / Notion /
// Jupyter。鼠标进区域时右上角浮按钮，display=true 才出（行内公式没必要）。

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_math_fork/flutter_math.dart';

import '../../../../app/theme.dart';

class MathSegmentView extends StatefulWidget {
  const MathSegmentView({
    super.key,
    required this.latex,
    required this.display,
  });

  final String latex;
  final bool display;

  @override
  State<MathSegmentView> createState() => _MathSegmentViewState();
}

class _MathSegmentViewState extends State<MathSegmentView> {
  bool _hovered = false;

  Future<void> _copy() async {
    await Clipboard.setData(ClipboardData(text: widget.latex));
    if (!mounted) return;
    ScaffoldMessenger.of(context).showSnackBar(const SnackBar(
      content: Text('已复制 LaTeX 源码'),
      duration: Duration(seconds: 1),
    ));
  }

  @override
  Widget build(BuildContext context) {
    final body = Padding(
      padding: EdgeInsets.symmetric(
        vertical: widget.display ? BiuTokens.space2 : 0,
      ),
      child: Center(
        child: Math.tex(
          widget.latex,
          mathStyle: widget.display ? MathStyle.display : MathStyle.text,
          textStyle: TextStyle(
            color: BiuTokens.text,
            fontSize: 15,
          ),
          onErrorFallback: (e) => SelectableText(
            widget.latex,
            style: const TextStyle(
              fontFamily: 'monospace',
              fontSize: 13,
              color: BiuTokens.error,
            ),
          ),
        ),
      ),
    );
    if (!widget.display) return body;
    return MouseRegion(
      onEnter: (_) => setState(() => _hovered = true),
      onExit: (_) => setState(() => _hovered = false),
      child: Stack(
        children: [
          body,
          Positioned(
            right: 4,
            top: 4,
            child: AnimatedOpacity(
              duration: const Duration(milliseconds: 150),
              opacity: _hovered ? 1.0 : 0.0,
              child: IgnorePointer(
                ignoring: !_hovered,
                child: Material(
                  color: BiuTokens.surfaceMuted,
                  borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
                  child: InkWell(
                    onTap: _copy,
                    borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
                    child: Container(
                      padding: const EdgeInsets.symmetric(
                          horizontal: 6, vertical: 3),
                      decoration: BoxDecoration(
                        border: Border.all(color: BiuTokens.borderSubtle),
                        borderRadius:
                            BorderRadius.circular(BiuTokens.radiusSm),
                      ),
                      child: Row(
                        mainAxisSize: MainAxisSize.min,
                        children: [
                          Icon(Icons.copy_outlined,
                              size: 12, color: BiuTokens.textMuted),
                          const SizedBox(width: 4),
                          Text(
                            'LaTeX',
                            style: TextStyle(
                              fontSize: 11,
                              color: BiuTokens.textMuted,
                              fontFamily: 'monospace',
                            ),
                          ),
                        ],
                      ),
                    ),
                  ),
                ),
              ),
            ),
          ),
        ],
      ),
    );
  }
}

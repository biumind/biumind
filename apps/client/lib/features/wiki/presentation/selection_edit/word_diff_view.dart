// WordDiffView — S3 P1-6 selection-edit preview.
//
// Renders a word-level diff between the original selection and the LLM
// replacement so the user can review before Accept. Uses diff_match_patch
// (pure Dart). Colour palette mirrors git_panel.dart:_DiffText — added spans
// on a translucent green, removed spans on translucent red + strikethrough.

import 'package:diff_match_patch/diff_match_patch.dart';
import 'package:flutter/material.dart';

import '../../../../app/theme.dart';

class WordDiffView extends StatelessWidget {
  const WordDiffView({
    super.key,
    required this.before,
    required this.after,
  });

  /// Original selection text.
  final String before;

  /// LLM replacement text.
  final String after;

  @override
  Widget build(BuildContext context) {
    return Container(
      width: double.infinity,
      padding: const EdgeInsets.all(BiuTokens.space2),
      decoration: BoxDecoration(
        color: BiuTokens.surfaceMuted,
        borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
      ),
      child: Text.rich(
        TextSpan(
          children: _spans(),
          style: const TextStyle(fontSize: 12, height: 1.5),
        ),
      ),
    );
  }

  List<InlineSpan> _spans() {
    final spans = <InlineSpan>[];
    for (final d in diff(before, after)) {
      switch (d.operation) {
        case DIFF_INSERT:
          spans.add(TextSpan(
            text: d.text,
            style: TextStyle(
              color: BiuTokens.green,
              backgroundColor: BiuTokens.green.withValues(alpha: 0.14),
              fontWeight: FontWeight.w600,
            ),
          ));
        case DIFF_DELETE:
          spans.add(TextSpan(
            text: d.text,
            style: TextStyle(
              color: BiuTokens.error,
              backgroundColor: BiuTokens.error.withValues(alpha: 0.14),
              decoration: TextDecoration.lineThrough,
              decorationColor: BiuTokens.error,
            ),
          ));
        default:
          spans.add(TextSpan(
            text: d.text,
            style: TextStyle(color: BiuTokens.text),
          ));
      }
    }
    return spans;
  }
}

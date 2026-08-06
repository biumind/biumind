// OutlinePanel — right-side TOC for the active page.
//
// Lists every heading block in document order, indented by level.
// Hidden when the page has fewer than 2 headings (a single-heading
// page has nothing to navigate to). Tapping a heading invokes the
// supplied [onTap] with the block id; the parent decides how to
// scroll-to-block (in reader mode this drives a Scrollable, in
// editor mode it focuses the matching BlockEditor).
//
// Visual: 12px monospace-ish, level-1 left aligned, deeper levels
// indented 12 px per level. Selected heading (same id as [activeId])
// gets a left bar in BiuTokens.purple.

import 'package:flutter/material.dart';

import '../../../../app/theme.dart';
import 'block_to_markdown.dart';

class OutlinePanel extends StatelessWidget {
  const OutlinePanel({
    super.key,
    required this.headings,
    this.activeId,
    this.onTap,
    this.minHeadings = 2,
  });

  final List<WikiHeading> headings;

  /// When non-null, the matching row gets the active highlight.
  final String? activeId;

  /// Tap callback. Receives the heading's block id.
  final void Function(String blockId)? onTap;

  /// Hide the panel until the page has at least this many headings.
  final int minHeadings;

  @override
  Widget build(BuildContext context) {
    if (headings.length < minHeadings) return SizedBox.shrink();
    return Container(
      width: 220,
      decoration: BoxDecoration(
        border: Border(
          left: BorderSide(color: BiuTokens.borderSubtle),
        ),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Padding(
            padding: const EdgeInsets.fromLTRB(12, 12, 12, 6),
            child: Row(
              children: [
                Icon(Icons.list_alt, size: 14, color: BiuTokens.textMuted),
                const SizedBox(width: 4),
                Text(
                  '大纲',
                  style: TextStyle(
                    fontSize: 11,
                    fontWeight: FontWeight.w600,
                    color: BiuTokens.textMuted,
                    letterSpacing: 0.4,
                  ),
                ),
              ],
            ),
          ),
          const Divider(height: 1),
          Expanded(
            child: ListView.builder(
              padding: const EdgeInsets.symmetric(vertical: 4),
              itemCount: headings.length,
              itemBuilder: (_, i) {
                final h = headings[i];
                final selected = h.blockId == activeId;
                return _OutlineRow(
                  heading: h,
                  selected: selected,
                  onTap: onTap == null ? null : () => onTap!(h.blockId),
                );
              },
            ),
          ),
        ],
      ),
    );
  }
}

class _OutlineRow extends StatelessWidget {
  const _OutlineRow({
    required this.heading,
    required this.selected,
    required this.onTap,
  });

  final WikiHeading heading;
  final bool selected;
  final VoidCallback? onTap;

  @override
  Widget build(BuildContext context) {
    final indent = (heading.level - 1) * 12.0;
    return InkWell(
      onTap: onTap,
      child: Container(
        padding: EdgeInsets.fromLTRB(8 + indent, 4, 8, 4),
        decoration: BoxDecoration(
          border: Border(
            left: BorderSide(
              color: selected ? BiuTokens.purple : Colors.transparent,
              width: 2,
            ),
          ),
        ),
        child: Text(
          heading.text,
          maxLines: 2,
          overflow: TextOverflow.ellipsis,
          style: TextStyle(
            fontSize: heading.level == 1 ? 12.5 : 11.5,
            fontWeight:
                heading.level <= 2 ? FontWeight.w600 : FontWeight.w400,
            color: selected ? BiuTokens.text : BiuTokens.textMuted,
            height: 1.4,
          ),
        ),
      ),
    );
  }
}

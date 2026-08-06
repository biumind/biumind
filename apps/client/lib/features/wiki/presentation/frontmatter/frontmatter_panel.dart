// FrontmatterPanel — read-only metadata strip rendered above page body.
//
// Sections (collapsed if empty):
//   - identity strip: type chip + tag chips + created/updated icon text
//   - description: italic muted paragraph
//   - origin callout: highlighted box for "deep-research"/"web-clip"/etc.
//   - sources / related: chip lists with optional onTap navigation
//   - extras: ExpansionTile for any unrecognised keys
//
// Stays read-only by design — the editor lives in a separate dialog so
// users don't accidentally mutate metadata while reading. Tap the
// pencil icon (handled by the parent) to flip into edit mode.

import 'package:flutter/material.dart';

import '../../../../app/theme.dart';
import 'frontmatter_helpers.dart';

class FrontmatterPanel extends StatelessWidget {
  const FrontmatterPanel({
    super.key,
    required this.frontmatter,
    this.onEdit,
    this.onRelatedTap,
    this.onSourceTap,
  });

  final Map<String, dynamic> frontmatter;

  /// Pencil-icon callback. Hidden when null.
  final VoidCallback? onEdit;

  /// Tap on a related chip — receives the (wrapped or raw) slug.
  /// Caller resolves to a page id and navigates.
  final ValueChanged<String>? onRelatedTap;

  /// Tap on a source chip — receives the source filename.
  final ValueChanged<String>? onSourceTap;

  @override
  Widget build(BuildContext context) {
    final type = stringFieldValue(frontmatter['type']);
    final created = stringFieldValue(frontmatter['created']);
    final updated = stringFieldValue(frontmatter['updated']);
    final description = stringFieldValue(frontmatter['description']);
    final origin = stringFieldValue(frontmatter['origin']);
    final tags = listFieldValue(frontmatter['tags']);
    final sources = listFieldValue(frontmatter['sources']);
    final related = listFieldValue(frontmatter['related']);
    final extras = frontmatter.entries
        .where((e) =>
            !kKnownFrontmatterKeys.contains(e.key) && _hasContent(e.value))
        .toList(growable: false);

    final hasAny = type.isNotEmpty ||
        description.isNotEmpty ||
        origin.isNotEmpty ||
        tags.isNotEmpty ||
        sources.isNotEmpty ||
        related.isNotEmpty ||
        extras.isNotEmpty;

    if (!hasAny && onEdit == null) return const SizedBox.shrink();

    return Container(
      margin: const EdgeInsets.fromLTRB(16, 8, 16, 0),
      padding: const EdgeInsets.all(BiuTokens.space3),
      decoration: BoxDecoration(
        color: BiuTokens.surfaceMuted,
        border: Border.all(color: BiuTokens.borderSubtle),
        borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          // Header row: identity strip + edit button
          Row(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Expanded(
                child: Wrap(
                  spacing: 6,
                  runSpacing: 4,
                  crossAxisAlignment: WrapCrossAlignment.center,
                  children: [
                    if (type.isNotEmpty) _typeChip(type),
                    for (final tag in tags) _tagChip(tag),
                    if (created.isNotEmpty)
                      _metaText(Icons.event_outlined, created),
                    if (updated.isNotEmpty && updated != created)
                      _metaText(Icons.history, '更新 $updated'),
                    if (!hasAny)
                      Text(
                        '没有 frontmatter',
                        style: TextStyle(
                            fontSize: 11, color: BiuTokens.textMuted),
                      ),
                  ],
                ),
              ),
              if (onEdit != null)
                IconButton(
                  tooltip: '编辑 frontmatter',
                  icon: const Icon(Icons.edit_outlined, size: 14),
                  onPressed: onEdit,
                  visualDensity: VisualDensity.compact,
                ),
            ],
          ),
          if (description.isNotEmpty) ...[
            const SizedBox(height: BiuTokens.space2),
            Text(
              description,
              style: TextStyle(
                fontSize: 12,
                fontStyle: FontStyle.italic,
                color: BiuTokens.textSecondary,
                height: 1.4,
              ),
            ),
          ],
          if (origin.isNotEmpty) ...[
            const SizedBox(height: BiuTokens.space2),
            _OriginCallout(origin: origin),
          ],
          if (sources.isNotEmpty) ...[
            const SizedBox(height: BiuTokens.space3),
            _SectionHeader(
                icon: Icons.description_outlined,
                label: 'Sources',
                count: sources.length),
            const SizedBox(height: 4),
            Wrap(
              spacing: 6,
              runSpacing: 4,
              children: [
                for (final s in sources)
                  _ChipBtn(
                    label: s,
                    icon: Icons.article_outlined,
                    onTap: onSourceTap == null ? null : () => onSourceTap!(s),
                  ),
              ],
            ),
          ],
          if (related.isNotEmpty) ...[
            const SizedBox(height: BiuTokens.space3),
            _SectionHeader(
                icon: Icons.north_east, label: 'Related', count: related.length),
            const SizedBox(height: 4),
            Wrap(
              spacing: 6,
              runSpacing: 4,
              children: [
                for (final r in related)
                  _ChipBtn(
                    label: unwrapWikilinkLabel(r),
                    icon: Icons.link,
                    onTap: onRelatedTap == null
                        ? null
                        : () => onRelatedTap!(unwrapWikilinkSlug(r)),
                  ),
              ],
            ),
          ],
          if (extras.isNotEmpty) ...[
            const SizedBox(height: BiuTokens.space2),
            _ExtrasExpansion(extras: extras),
          ],
        ],
      ),
    );
  }

  bool _hasContent(Object? v) {
    if (v == null) return false;
    if (v is String) return v.trim().isNotEmpty;
    if (v is List) return v.isNotEmpty;
    if (v is Map) return v.isNotEmpty;
    return true;
  }

  Widget _typeChip(String type) {
    final color = _typeColor(type);
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.18),
        borderRadius: BorderRadius.circular(4),
      ),
      child: Text(
        type.toUpperCase(),
        style: TextStyle(
          fontSize: 10,
          fontWeight: FontWeight.w700,
          letterSpacing: 0.6,
          color: color,
        ),
      ),
    );
  }

  Color _typeColor(String type) => switch (type) {
        'entity' => BiuTokens.purple,
        'concept' => NamedPaletteStrong.purple,
        'source' => NamedPaletteStrong.emerald,
        'overview' => NamedPaletteStrong.blue,
        'query' => NamedPaletteStrong.red,
        'comparison' => NamedPaletteStrong.amber,
        'synthesis' => NamedPaletteStrong.cyan,
        _ => BiuTokens.textMuted,
      };

  Widget _tagChip(String tag) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 1),
      decoration: BoxDecoration(
        color: BiuTokens.surface,
        border: Border.all(color: BiuTokens.borderSubtle),
        borderRadius: BorderRadius.circular(3),
      ),
      child: Text(
        '#$tag',
        style: TextStyle(fontSize: 11, color: BiuTokens.textSecondary),
      ),
    );
  }

  Widget _metaText(IconData icon, String text) {
    return Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        Icon(icon, size: 11, color: BiuTokens.textMuted),
        const SizedBox(width: 2),
        Text(
          text,
          style: TextStyle(fontSize: 10, color: BiuTokens.textMuted),
        ),
      ],
    );
  }
}

class _OriginCallout extends StatelessWidget {
  const _OriginCallout({required this.origin});
  final String origin;

  @override
  Widget build(BuildContext context) {
    final (label, color) = switch (origin) {
      'deep-research' => ('AI 研究合成', NamedPaletteStrong.red),
      'web-clip' => ('网页剪藏', NamedPaletteStrong.blue),
      'manual' => ('手动创建', BiuTokens.textMuted),
      _ => (origin, BiuTokens.textMuted),
    };
    return Container(
      padding: const EdgeInsets.symmetric(
          horizontal: BiuTokens.space2, vertical: 4),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.1),
        borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
      ),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          Icon(Icons.info_outline, size: 12, color: color),
          const SizedBox(width: 4),
          Text(
            'origin: $label',
            style: TextStyle(
                fontSize: 10, fontWeight: FontWeight.w600, color: color),
          ),
        ],
      ),
    );
  }
}

class _SectionHeader extends StatelessWidget {
  const _SectionHeader({
    required this.icon,
    required this.label,
    required this.count,
  });
  final IconData icon;
  final String label;
  final int count;

  @override
  Widget build(BuildContext context) {
    return Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        Icon(icon, size: 11, color: BiuTokens.textMuted),
        const SizedBox(width: 4),
        Text(
          '$label · $count',
          style: TextStyle(
            fontSize: 10,
            fontWeight: FontWeight.w600,
            color: BiuTokens.textMuted,
            letterSpacing: 0.4,
          ),
        ),
      ],
    );
  }
}

class _ChipBtn extends StatelessWidget {
  const _ChipBtn({required this.label, required this.icon, this.onTap});
  final String label;
  final IconData icon;
  final VoidCallback? onTap;

  @override
  Widget build(BuildContext context) {
    return InkWell(
      onTap: onTap,
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
        decoration: BoxDecoration(
          color: BiuTokens.surface,
          border: Border.all(color: BiuTokens.borderSubtle),
          borderRadius: BorderRadius.circular(3),
        ),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(icon, size: 11, color: BiuTokens.textMuted),
            const SizedBox(width: 3),
            Text(
              label,
              style: TextStyle(
                  fontSize: 11, color: BiuTokens.textSecondary),
            ),
          ],
        ),
      ),
    );
  }
}

class _ExtrasExpansion extends StatelessWidget {
  const _ExtrasExpansion({required this.extras});
  final List<MapEntry<String, dynamic>> extras;

  @override
  Widget build(BuildContext context) {
    return Theme(
      data: Theme.of(context).copyWith(dividerColor: Colors.transparent),
      child: ExpansionTile(
        tilePadding: EdgeInsets.zero,
        childrenPadding: EdgeInsets.zero,
        dense: true,
        title: Text(
          '其他字段 · ${extras.length}',
          style: TextStyle(
              fontSize: 10,
              fontWeight: FontWeight.w600,
              color: BiuTokens.textMuted),
        ),
        children: [
          for (final e in extras)
            Padding(
              padding: const EdgeInsets.symmetric(vertical: 1),
              child: Row(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  SizedBox(
                    width: 90,
                    child: Text(
                      '${e.key}:',
                      style: TextStyle(
                          fontSize: 10,
                          color: BiuTokens.textMuted,
                          fontFamily:
                              'JetBrains Mono, ui-monospace, monospace'),
                    ),
                  ),
                  Expanded(
                    child: Text(
                      _stringify(e.value),
                      style: TextStyle(
                          fontSize: 10,
                          color: BiuTokens.textSecondary,
                          fontFamily:
                              'JetBrains Mono, ui-monospace, monospace'),
                    ),
                  ),
                ],
              ),
            ),
        ],
      ),
    );
  }

  String _stringify(Object? v) {
    if (v is List) return v.join(', ');
    return v?.toString() ?? '';
  }
}

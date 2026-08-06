// Component widgets — atoms that ItemTemplate / Card / list rows
// resolve to. Six v1.5 kinds:
//
//   text       — single string
//   card       — title + subtitle + body + image + actions
//   kv_list    — entries: [{key, value}]
//   markdown   — fenced markdown body (basic — no code highlight in v1.5)
//   progress   — value / max + label
//   chart      — placeholder (real impl lands v2.0 with fl_chart)
//
// Each takes a resolved data map (already interpolated) and renders.
// Action lists route through ActionRunner from action_runner.dart.

import 'package:flutter/material.dart';

import '../../../app/theme.dart';
import '../domain/view_spec.dart';
import 'action_runner.dart';

/// Renders one item by its template spec. The data context (item /
/// data / route / config) is folded into the interpolator inside
/// the layout; this widget receives only the rendered strings.
class ComponentRenderer extends StatelessWidget {
  const ComponentRenderer({
    super.key,
    required this.template,
    required this.runner,
    required this.title,
    required this.subtitle,
    required this.body,
    required this.image,
  });

  final ViewItemTemplate template;
  final ActionRunner runner;
  final String title;
  final String subtitle;
  final String body;
  final String image;

  @override
  Widget build(BuildContext context) {
    switch (template.kind) {
      case 'text':
        return _TextItem(text: title.isNotEmpty ? title : body);
      case 'card':
        return _CardItem(
          title: title,
          subtitle: subtitle,
          body: body,
          image: image,
          actions: template.actions,
          runner: runner,
        );
      case 'kv_list':
        return _KvListItem(rawEntries: template.props['entries']);
      case 'markdown':
        return _MarkdownItem(content: body);
      case 'progress':
        return _ProgressItem(props: template.props, label: title);
      case 'chart':
        return const _ChartPlaceholder();
    }
    // Unknown kind — degrade to plain text so future-version views
    // surface their content somewhere visible.
    return _TextItem(text: title.isNotEmpty ? title : body);
  }
}

class _TextItem extends StatelessWidget {
  const _TextItem({required this.text});
  final String text;
  @override
  Widget build(BuildContext context) {
    if (text.isEmpty) return const SizedBox.shrink();
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 4),
      child: Text(text, style: Theme.of(context).textTheme.bodyMedium),
    );
  }
}

class _CardItem extends StatelessWidget {
  const _CardItem({
    required this.title,
    required this.subtitle,
    required this.body,
    required this.image,
    required this.actions,
    required this.runner,
  });
  final String title;
  final String subtitle;
  final String body;
  final String image;
  final List<ViewActionRef> actions;
  final ActionRunner runner;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Card(
      margin: const EdgeInsets.symmetric(vertical: 4),
      child: Padding(
        padding: const EdgeInsets.all(BiuTokens.space3),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            if (image.isNotEmpty)
              Padding(
                padding: const EdgeInsets.only(bottom: BiuTokens.space2),
                child: ClipRRect(
                  borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
                  child: Image.network(
                    image,
                    height: 120,
                    fit: BoxFit.cover,
                    errorBuilder: (_, _, _) => const SizedBox.shrink(),
                  ),
                ),
              ),
            if (title.isNotEmpty)
              Text(title,
                  style: theme.textTheme.titleSmall?.copyWith(fontWeight: FontWeight.w600)),
            if (subtitle.isNotEmpty)
              Padding(
                padding: const EdgeInsets.only(top: 2),
                child: Text(subtitle,
                    style: theme.textTheme.bodySmall?.copyWith(
                        color: theme.colorScheme.onSurfaceVariant)),
              ),
            if (body.isNotEmpty)
              Padding(
                padding: const EdgeInsets.only(top: BiuTokens.space2),
                child: Text(body, style: theme.textTheme.bodyMedium),
              ),
            if (actions.isNotEmpty)
              Padding(
                padding: const EdgeInsets.only(top: BiuTokens.space2),
                child: Wrap(
                  spacing: BiuTokens.space2,
                  children: actions.map((a) => _actionButton(context, a, runner)).toList(),
                ),
              ),
          ],
        ),
      ),
    );
  }
}

Widget _actionButton(BuildContext context, ViewActionRef a, ActionRunner runner) {
  return TextButton(
    onPressed: () => runner.run(context, a),
    child: Text(a.label),
  );
}

class _KvListItem extends StatelessWidget {
  const _KvListItem({required this.rawEntries});
  final dynamic rawEntries;
  @override
  Widget build(BuildContext context) {
    final entries = (rawEntries is List)
        ? rawEntries.whereType<Map>().toList(growable: false)
        : const <Map>[];
    if (entries.isEmpty) return const SizedBox.shrink();
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: entries.map((e) {
        final k = e['key']?.toString() ?? '';
        final v = e['value']?.toString() ?? '';
        return Padding(
          padding: const EdgeInsets.symmetric(vertical: 2),
          child: Row(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              SizedBox(
                width: 100,
                child: Text(k,
                    style: Theme.of(context).textTheme.bodySmall?.copyWith(
                        color: Theme.of(context).colorScheme.onSurfaceVariant)),
              ),
              Expanded(child: Text(v, style: Theme.of(context).textTheme.bodyMedium)),
            ],
          ),
        );
      }).toList(),
    );
  }
}

class _MarkdownItem extends StatelessWidget {
  const _MarkdownItem({required this.content});
  final String content;
  @override
  Widget build(BuildContext context) {
    // v1.5 ships without a markdown renderer dependency. Plain text
    // with paragraph breaks gives content authors a path that
    // doesn't lose structure; richer rendering (flutter_markdown)
    // lands in v2.0 alongside the chart component.
    if (content.isEmpty) return const SizedBox.shrink();
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 4),
      child: Text(content, style: Theme.of(context).textTheme.bodyMedium),
    );
  }
}

class _ProgressItem extends StatelessWidget {
  const _ProgressItem({required this.props, required this.label});
  final Map<String, dynamic> props;
  final String label;
  @override
  Widget build(BuildContext context) {
    final value = (props['value'] as num?)?.toDouble() ?? 0;
    final max = (props['max'] as num?)?.toDouble() ?? 1;
    final pct = max > 0 ? (value / max).clamp(0.0, 1.0) : 0.0;
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 4),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          if (label.isNotEmpty)
            Padding(
              padding: const EdgeInsets.only(bottom: 4),
              child: Text(label, style: Theme.of(context).textTheme.bodySmall),
            ),
          LinearProgressIndicator(value: pct),
          const SizedBox(height: 2),
          Text('${(pct * 100).toStringAsFixed(0)}%',
              style: Theme.of(context).textTheme.labelSmall),
        ],
      ),
    );
  }
}

class _ChartPlaceholder extends StatelessWidget {
  const _ChartPlaceholder();
  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(BiuTokens.space3),
      decoration: BoxDecoration(
        color: Theme.of(context).colorScheme.surfaceContainerHigh,
        borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
      ),
      child: Text('chart (v2.0)',
          style: Theme.of(context).textTheme.bodySmall?.copyWith(
              color: Theme.of(context).colorScheme.onSurfaceVariant)),
    );
  }
}

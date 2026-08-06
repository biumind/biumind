// A2UIRenderer — recursive widget that maps an A2UINode tree to
// Flutter widgets. Mirrors the kind whitelist in domain/a2ui.dart.
//
// Containers (column / row / stack / grid) recurse over children;
// leaves render directly from props. Buttons / inputs route through
// ActionRunner (manifest action dispatch + on_success effects) so
// custom layouts get the same confirm / risk-warning / refresh
// behaviour as ItemTemplate-based layouts.
//
// We never execute server-pushed code — every interaction expression
// is a manifest action name. Defense in depth against the v3.0 plan
// to allow custom JS in views (which is NOT v2.0 scope).

import 'package:flutter/material.dart';

import '../../../app/theme.dart';
import '../domain/a2ui.dart';
import '../domain/view_spec.dart';
import 'action_runner.dart';

class A2UIRenderer extends StatelessWidget {
  const A2UIRenderer({
    super.key,
    required this.root,
    required this.runner,
    this.validation,
  });

  final A2UINode root;
  final ActionRunner runner;

  /// Validation result computed by the caller (typically the host
  /// layout). When non-null and contains a fatal issue (depth or
  /// node count overflow), we render an inline placeholder so the
  /// user sees something deterministic instead of a partial tree.
  final A2UIValidationResult? validation;

  @override
  Widget build(BuildContext context) {
    if (validation != null && validation!.isFatal) {
      return _FatalPlaceholder(issues: validation!.issues);
    }
    return _renderNode(context, root);
  }

  Widget _renderNode(BuildContext context, A2UINode n) {
    switch (n.kind) {
      case A2UIKind.column:    return _column(context, n);
      case A2UIKind.row:       return _row(context, n);
      case A2UIKind.stack:     return _stack(context, n);
      case A2UIKind.grid:      return _grid(context, n);
      case A2UIKind.text:      return _text(context, n);
      case A2UIKind.image:     return _image(context, n);
      case A2UIKind.card:      return _card(context, n);
      case A2UIKind.button:    return _button(context, n);
      case A2UIKind.input:     return _input(context, n);
      case A2UIKind.chart:     return _chartPlaceholder(context, n);
      case A2UIKind.markdown:  return _markdown(context, n);
      case A2UIKind.progress:  return _progress(context, n);
      case A2UIKind.divider:   return const Divider();
      case A2UIKind.spacer:    return _spacer(n);
      case A2UIKind.unknown:
        return const SizedBox.shrink();
    }
  }

  // ─── Containers ──────────────────────────────────────────

  Widget _column(BuildContext context, A2UINode n) {
    return Padding(
      padding: _padding(n),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: _spaced(n, n.children.map((c) => _renderNode(context, c)).toList()),
      ),
    );
  }

  Widget _row(BuildContext context, A2UINode n) {
    return Padding(
      padding: _padding(n),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: _spaced(n, n.children.map((c) =>
          Flexible(child: _renderNode(context, c)),
        ).toList()),
      ),
    );
  }

  Widget _stack(BuildContext context, A2UINode n) {
    return Padding(
      padding: _padding(n),
      child: Stack(children: n.children.map((c) => _renderNode(context, c)).toList()),
    );
  }

  Widget _grid(BuildContext context, A2UINode n) {
    final cols = (n.props['columns'] as num?)?.toInt() ?? 2;
    return Padding(
      padding: _padding(n),
      child: GridView.count(
        crossAxisCount: cols,
        crossAxisSpacing: BiuTokens.space2,
        mainAxisSpacing: BiuTokens.space2,
        shrinkWrap: true,
        physics: const NeverScrollableScrollPhysics(),
        children: n.children.map((c) => _renderNode(context, c)).toList(),
      ),
    );
  }

  // ─── Leaves ──────────────────────────────────────────────

  Widget _text(BuildContext context, A2UINode n) {
    final text = (n.props['text'] ?? n.props['value'] ?? '') as String;
    if (text.isEmpty) return const SizedBox.shrink();
    final style = (n.props['style'] as String?) ?? 'body';
    final theme = Theme.of(context).textTheme;
    TextStyle? ts;
    switch (style) {
      case 'heading':  ts = theme.titleMedium?.copyWith(fontWeight: FontWeight.w700);
      case 'caption':  ts = theme.bodySmall?.copyWith(
          color: Theme.of(context).colorScheme.onSurfaceVariant);
      case 'mono':     ts = theme.bodyMedium?.copyWith(fontFamily: 'monospace');
      default:         ts = theme.bodyMedium;
    }
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 2),
      child: Text(text, style: ts),
    );
  }

  Widget _image(BuildContext context, A2UINode n) {
    final src = (n.props['src'] ?? n.props['image'] ?? '') as String;
    if (src.isEmpty) return const SizedBox.shrink();
    // For v2.0 we only render https://. cas:// resolution lands
    // alongside the Files CAS HTTP gateway; until then surface the
    // URI as text so authors get visible feedback.
    if (!src.startsWith('https://')) {
      return Padding(
        padding: const EdgeInsets.symmetric(vertical: 2),
        child: Text(src,
            style: Theme.of(context).textTheme.bodySmall?.copyWith(
                color: Theme.of(context).colorScheme.onSurfaceVariant)),
      );
    }
    final width = (n.props['width'] as num?)?.toDouble();
    final height = (n.props['height'] as num?)?.toDouble();
    return ClipRRect(
      borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
      child: Image.network(
        src,
        width: width,
        height: height,
        fit: BoxFit.cover,
        errorBuilder: (_, _, _) => const SizedBox.shrink(),
      ),
    );
  }

  Widget _card(BuildContext context, A2UINode n) {
    final title = (n.props['title'] ?? '') as String;
    final subtitle = (n.props['subtitle'] ?? '') as String;
    final body = (n.props['body'] ?? '') as String;
    return Card(
      margin: const EdgeInsets.symmetric(vertical: 4),
      child: Padding(
        padding: const EdgeInsets.all(BiuTokens.space3),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            if (title.isNotEmpty)
              Text(title,
                  style: Theme.of(context).textTheme.titleSmall?.copyWith(
                      fontWeight: FontWeight.w600)),
            if (subtitle.isNotEmpty)
              Padding(
                padding: const EdgeInsets.only(top: 2),
                child: Text(subtitle,
                    style: Theme.of(context).textTheme.bodySmall?.copyWith(
                        color: Theme.of(context).colorScheme.onSurfaceVariant)),
              ),
            if (body.isNotEmpty)
              Padding(
                padding: const EdgeInsets.only(top: BiuTokens.space2),
                child: Text(body),
              ),
            // Children render below the title block (e.g. action rows).
            ...n.children.map((c) => Padding(
              padding: const EdgeInsets.only(top: BiuTokens.space2),
              child: _renderNode(context, c),
            )),
          ],
        ),
      ),
    );
  }

  Widget _button(BuildContext context, A2UINode n) {
    final label = (n.props['label'] ?? n.props['text'] ?? '') as String;
    final actionRef = _actionRefFromOnClick(n);
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 4),
      child: Align(
        alignment: Alignment.centerLeft,
        child: FilledButton(
          onPressed: actionRef == null ? null : () => runner.run(context, actionRef),
          child: Text(label.isEmpty ? '…' : label),
        ),
      ),
    );
  }

  Widget _input(BuildContext context, A2UINode n) {
    return _A2InputField(
      label: (n.props['label'] ?? '') as String,
      placeholder: (n.props['placeholder'] ?? '') as String,
      field: (n.props['field'] ?? 'value') as String,
      onSubmit: _actionRefFromOnSubmit(n),
      runner: runner,
    );
  }

  Widget _chartPlaceholder(BuildContext context, A2UINode n) {
    return Container(
      padding: const EdgeInsets.all(BiuTokens.space3),
      decoration: BoxDecoration(
        color: Theme.of(context).colorScheme.surfaceContainerHigh,
        borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
      ),
      child: Text('chart (v2.0 placeholder; full impl v2.5)',
          style: Theme.of(context).textTheme.bodySmall?.copyWith(
              color: Theme.of(context).colorScheme.onSurfaceVariant)),
    );
  }

  Widget _markdown(BuildContext context, A2UINode n) {
    final body = (n.props['body'] ?? n.props['text'] ?? '') as String;
    if (body.isEmpty) return const SizedBox.shrink();
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 4),
      child: Text(body, style: Theme.of(context).textTheme.bodyMedium),
    );
  }

  Widget _progress(BuildContext context, A2UINode n) {
    final value = (n.props['value'] as num?)?.toDouble() ?? 0;
    final max = (n.props['max'] as num?)?.toDouble() ?? 1;
    final pct = max > 0 ? (value / max).clamp(0.0, 1.0) : 0.0;
    final label = (n.props['label'] ?? '') as String;
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

  Widget _spacer(A2UINode n) {
    final h = (n.props['height'] as num?)?.toDouble() ?? BiuTokens.space2;
    return SizedBox(height: h);
  }

  // ─── Helpers ─────────────────────────────────────────────

  EdgeInsets _padding(A2UINode n) {
    final p = n.props['padding'];
    if (p is num) return EdgeInsets.all(p.toDouble());
    return EdgeInsets.zero;
  }

  /// _spaced inserts SizedBox separators between children based on
  /// the container's `spacing` prop. Saves authors from spamming
  /// spacer leaves between every row.
  List<Widget> _spaced(A2UINode n, List<Widget> kids) {
    final spacing = (n.props['spacing'] as num?)?.toDouble() ?? 0;
    if (spacing <= 0 || kids.length < 2) return kids;
    final isVertical = n.kind == A2UIKind.column;
    final out = <Widget>[];
    for (var i = 0; i < kids.length; i++) {
      if (i > 0) {
        out.add(isVertical
            ? SizedBox(height: spacing)
            : SizedBox(width: spacing));
      }
      out.add(kids[i]);
    }
    return out;
  }

  ViewActionRef? _actionRefFromOnClick(A2UINode n) {
    final raw = n.props['on_click'];
    if (raw is! Map<String, dynamic>) return null;
    return ViewActionRef(
      label: (n.props['label'] ?? 'click') as String,
      action: raw['action'] as String? ?? '',
      input: (raw['input'] as Map<String, dynamic>?) ?? const {},
      route: raw['route'] as String? ?? '',
    );
  }

  ViewActionRef? _actionRefFromOnSubmit(A2UINode n) {
    final raw = n.props['on_submit'];
    if (raw is! Map<String, dynamic>) return null;
    return ViewActionRef(
      label: (n.props['label'] ?? 'submit') as String,
      action: raw['action'] as String? ?? '',
      input: (raw['input'] as Map<String, dynamic>?) ?? const {},
      route: raw['route'] as String? ?? '',
    );
  }
}

// ─── Input field widget ──────────────────────────────────────────

class _A2InputField extends StatefulWidget {
  const _A2InputField({
    required this.label,
    required this.placeholder,
    required this.field,
    required this.onSubmit,
    required this.runner,
  });
  final String label;
  final String placeholder;
  final String field;
  final ViewActionRef? onSubmit;
  final ActionRunner runner;
  @override
  State<_A2InputField> createState() => _A2InputFieldState();
}

class _A2InputFieldState extends State<_A2InputField> {
  final _ctrl = TextEditingController();
  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 4),
      child: TextField(
        controller: _ctrl,
        decoration: InputDecoration(
          labelText: widget.label.isEmpty ? null : widget.label,
          hintText: widget.placeholder,
          border: const OutlineInputBorder(),
          isDense: true,
        ),
        onSubmitted: widget.onSubmit == null
            ? null
            : (v) {
                final ref = widget.onSubmit!;
                widget.runner.run(context, ViewActionRef(
                  label: ref.label,
                  action: ref.action,
                  input: {...ref.input, widget.field: v},
                  route: ref.route,
                ));
              },
      ),
    );
  }

  @override
  void dispose() {
    _ctrl.dispose();
    super.dispose();
  }
}

// ─── Fatal placeholder ───────────────────────────────────────────

class _FatalPlaceholder extends StatelessWidget {
  const _FatalPlaceholder({required this.issues});
  final List<A2UIIssue> issues;
  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.all(BiuTokens.space4),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            'Custom view too large to render',
            style: Theme.of(context).textTheme.titleSmall?.copyWith(
                color: Theme.of(context).colorScheme.error),
          ),
          const SizedBox(height: BiuTokens.space2),
          for (final i in issues.take(5))
            Text('· ${i.code}: ${i.message}',
                style: Theme.of(context).textTheme.bodySmall),
        ],
      ),
    );
  }
}

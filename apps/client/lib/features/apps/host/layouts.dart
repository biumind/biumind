// Layout widgets — one per ViewLayout enum value.
//
// Each receives:
//   - the parsed ViewSpec
//   - the resolved view-data map (typically {items: [...]}; shape
//     varies per layout — list/grid expect items[], dashboard expects
//     cards[], form is data-less)
//   - a route-context map (from the URL path params)
//   - the ActionRunner for tap / submit
//
// All layouts share the same interpolation context root:
//   context = { route, data, item, config, user, i18n, field }
// Layouts assemble these per-row / per-card and re-instantiate
// Interpolator so item-level templates resolve without leaking
// across iterations.

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../../../app/theme.dart';
import '../domain/a2ui.dart';
import '../domain/interpolator.dart';
import '../domain/view_spec.dart';
import 'a2ui_renderer.dart';
import 'action_runner.dart';
import 'components.dart';
import 'webview_panel.dart';

// ─── ListLayout ─────────────────────────────────────────────

class ListLayout extends StatelessWidget {
  const ListLayout({
    super.key,
    required this.spec,
    required this.data,
    required this.routeParams,
    required this.runner,
  });

  final ViewSpec spec;
  final Map<String, dynamic> data;
  final Map<String, dynamic> routeParams;
  final ActionRunner runner;

  @override
  Widget build(BuildContext context) {
    final items = (data['items'] as List?) ?? const [];
    if (items.isEmpty) {
      return _emptyState(context);
    }
    return ListView.separated(
      itemCount: items.length,
      separatorBuilder: (_, _) => const Divider(height: 1),
      itemBuilder: (context, i) {
        final item = items[i];
        if (item is! Map<String, dynamic>) return const SizedBox.shrink();
        return _renderItem(context, spec, item, data, routeParams, runner);
      },
    );
  }
}

// ─── ListDetailLayout ───────────────────────────────────────
//
// On wide screens: master-detail side-by-side. On mobile: list
// pushes detail on tap. Detail target is the sub-view referenced by
// spec.detailView; resolution lands on AppViewHost (which routes by
// route param).

class ListDetailLayout extends StatefulWidget {
  const ListDetailLayout({
    super.key,
    required this.spec,
    required this.data,
    required this.routeParams,
    required this.runner,
  });

  final ViewSpec spec;
  final Map<String, dynamic> data;
  final Map<String, dynamic> routeParams;
  final ActionRunner runner;

  @override
  State<ListDetailLayout> createState() => _ListDetailLayoutState();
}

class _ListDetailLayoutState extends State<ListDetailLayout> {
  Map<String, dynamic>? _selected;

  @override
  Widget build(BuildContext context) {
    final items = (widget.data['items'] as List?) ?? const [];
    return LayoutBuilder(
      builder: (context, constraints) {
        final wide = constraints.maxWidth > 720;
        final list = ListView.separated(
          itemCount: items.length,
          separatorBuilder: (_, _) => const Divider(height: 1),
          itemBuilder: (context, i) {
            final item = items[i];
            if (item is! Map<String, dynamic>) return const SizedBox.shrink();
            final selected = identical(item, _selected);
            return Container(
              color: selected
                  ? Theme.of(context).colorScheme.surfaceContainerHigh
                  : null,
              child: InkWell(
                onTap: () {
                  if (wide) {
                    setState(() => _selected = item);
                  } else {
                    // Mobile: 用 modal bottom sheet 展示 detail。
                    //
                    // 早期实现是 Navigator.push(MaterialPageRoute(...)) —— 那条
                    // 路径会把临时 detail 页推进 ShellRoute 内层栈, 侧边栏 ctx.go
                    // 会被它"压住"看似无反应（已通过 _Sidebar._navigateTo
                    // popUntil 兜底, 但仍属不规范）。这里改为 modal sheet:
                    //   - 不污染路由栈, 浏览器后退 / 深度链接行为一致
                    //   - 用户切到其他 ShellRoute child 时, sheet 会随侧边栏的
                    //     popUntil 自然消失
                    final rendered = _renderItemString(
                      widget.spec.itemTemplate?.title ?? '',
                      item,
                      widget.data,
                      widget.routeParams,
                    );
                    showModalBottomSheet<void>(
                      context: context,
                      isScrollControlled: true,
                      showDragHandle: true,
                      builder: (sheetCtx) => DraggableScrollableSheet(
                        expand: false,
                        initialChildSize: 0.7,
                        maxChildSize: 0.95,
                        minChildSize: 0.4,
                        builder: (_, scroll) => SingleChildScrollView(
                          controller: scroll,
                          padding: const EdgeInsets.fromLTRB(
                              BiuTokens.space4, 0, BiuTokens.space4, BiuTokens.space4),
                          child: Column(
                            crossAxisAlignment: CrossAxisAlignment.start,
                            children: [
                              if (rendered.isNotEmpty) ...[
                                Text(
                                  rendered,
                                  style: Theme.of(sheetCtx).textTheme.titleMedium,
                                ),
                                const SizedBox(height: BiuTokens.space3),
                              ],
                              _detail(item),
                            ],
                          ),
                        ),
                      ),
                    );
                  }
                },
                child: _renderItem(
                    context, widget.spec, item, widget.data, widget.routeParams, widget.runner),
              ),
            );
          },
        );
        if (!wide) return list;
        return Row(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            SizedBox(width: 320, child: list),
            const VerticalDivider(width: 1),
            Expanded(
              child: _selected == null
                  ? _emptyState(context, message: '请选择列表项')
                  : SingleChildScrollView(
                      padding: const EdgeInsets.all(BiuTokens.space4),
                      child: _detail(_selected!),
                    ),
            ),
          ],
        );
      },
    );
  }

  Widget _detail(Map<String, dynamic> item) {
    // v1.5 detail rendering: dump key→value pairs with the
    // interpolator's _stringify. Real detail-view recursion lands
    // when AppViewHost gains route-param navigation in v2.0.
    //
    // 返回一个 Column（不自带 scroll）—— 调用方负责 SingleChildScrollView
    // 包裹. 这样在 modal sheet 里 DraggableScrollableSheet 的 controller
    // 才能正常驱动滚动, 不会出现嵌套 scroll。
    final rows = <Widget>[];
    item.forEach((k, v) {
      rows.add(Padding(
        padding: const EdgeInsets.symmetric(vertical: 4),
        child: Row(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            SizedBox(
              width: 120,
              child: Text(k.toString(),
                  style: Theme.of(context).textTheme.bodySmall?.copyWith(
                      color: Theme.of(context).colorScheme.onSurfaceVariant)),
            ),
            Expanded(
              child: SelectableText(v.toString()),
            ),
          ],
        ),
      ));
    });
    return Column(crossAxisAlignment: CrossAxisAlignment.start, children: rows);
  }
}

// ─── FormLayout ─────────────────────────────────────────────
//
// Auto-renders a form from manifest.actions[<schema_ref>].input_schema.
// v1.5 supports primitive types only:
//   string  (TextField; format=uri / email get keyboardType hints)
//   integer (TextField with input formatter)
//   boolean (Switch)
//   array of string  (chips with text input)
//   enum     (DropdownButton)
// Required vs optional drives the * indicator + non-empty validator.

class FormLayout extends StatefulWidget {
  const FormLayout({
    super.key,
    required this.spec,
    required this.schema,
    required this.runner,
  });

  final ViewSpec spec;
  /// Resolved input_schema (after schema_ref dereferencing).
  final Map<String, dynamic> schema;
  final ActionRunner runner;

  @override
  State<FormLayout> createState() => _FormLayoutState();
}

class _FormLayoutState extends State<FormLayout> {
  final _formKey = GlobalKey<FormState>();
  final Map<String, dynamic> _values = {};
  bool _submitting = false;

  @override
  Widget build(BuildContext context) {
    final props = (widget.schema['properties'] as Map<String, dynamic>?) ?? const {};
    final required = ((widget.schema['required'] as List?) ?? const [])
        .whereType<String>()
        .toSet();
    if (props.isEmpty) {
      return _emptyState(context, message: 'form schema is empty');
    }
    return Form(
      key: _formKey,
      child: SingleChildScrollView(
        padding: const EdgeInsets.all(BiuTokens.space4),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            ...props.entries.map((e) => _buildField(e.key, e.value as Map<String, dynamic>, required.contains(e.key))),
            const SizedBox(height: BiuTokens.space4),
            FilledButton(
              onPressed: _submitting ? null : _submit,
              child: Text(_submitting ? '提交中…' : '提交'),
            ),
          ],
        ),
      ),
    );
  }

  Widget _buildField(String name, Map<String, dynamic> spec, bool isRequired) {
    final type = spec['type'] as String? ?? 'string';
    final label = spec['title'] as String? ?? name;
    final desc = spec['description'] as String? ?? '';
    final defaultVal = spec['default'];
    final enumVals = (spec['enum'] as List?)?.whereType<String>().toList();
    final format = spec['format'] as String? ?? '';

    Widget field;
    if (enumVals != null && enumVals.isNotEmpty) {
      _values[name] ??= defaultVal ?? enumVals.first;
      field = DropdownButtonFormField<String>(
        initialValue: _values[name] as String?,
        decoration: InputDecoration(labelText: label, helperText: desc),
        items: enumVals
            .map((v) => DropdownMenuItem(value: v, child: Text(v)))
            .toList(),
        onChanged: (v) => setState(() => _values[name] = v),
      );
    } else if (type == 'boolean') {
      _values[name] ??= defaultVal ?? false;
      field = SwitchListTile(
        contentPadding: EdgeInsets.zero,
        title: Text(label + (isRequired ? ' *' : '')),
        subtitle: desc.isEmpty ? null : Text(desc),
        value: _values[name] as bool? ?? false,
        onChanged: (v) => setState(() => _values[name] = v),
      );
    } else if (type == 'integer' || type == 'number') {
      field = TextFormField(
        initialValue: defaultVal?.toString() ?? '',
        decoration: InputDecoration(
            labelText: label + (isRequired ? ' *' : ''), helperText: desc),
        keyboardType: const TextInputType.numberWithOptions(),
        inputFormatters: [FilteringTextInputFormatter.digitsOnly],
        validator: (v) {
          if (isRequired && (v == null || v.isEmpty)) return '必填';
          return null;
        },
        onChanged: (v) => _values[name] = int.tryParse(v),
      );
    } else {
      field = TextFormField(
        initialValue: defaultVal?.toString() ?? '',
        decoration: InputDecoration(
            labelText: label + (isRequired ? ' *' : ''), helperText: desc),
        keyboardType: format == 'uri'
            ? TextInputType.url
            : format == 'email'
                ? TextInputType.emailAddress
                : TextInputType.text,
        validator: (v) {
          if (isRequired && (v == null || v.isEmpty)) return '必填';
          return null;
        },
        onChanged: (v) => _values[name] = v,
      );
    }
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 6),
      child: field,
    );
  }

  Future<void> _submit() async {
    if (!_formKey.currentState!.validate()) return;
    final action = widget.spec.submit?.action ?? '';
    if (action.isEmpty) return;
    setState(() => _submitting = true);
    try {
      await widget.runner.run(
        context,
        ViewActionRef(
          label: '提交',
          action: action,
          input: Map<String, dynamic>.from(_values),
          onSuccess: widget.spec.submit?.onSuccess,
        ),
      );
    } finally {
      if (mounted) setState(() => _submitting = false);
    }
  }
}

// ─── CustomLayout ──────────────────────────────────────────
//
// Renders an A2UI subtree returned by data_source.action. Two
// extra defenses on top of server-side validation:
//   - depth + node-count caps re-asserted (root cause: even with
//     validator, a buggy in-process App could push a tree that the
//     server never saw)
//   - on_click.action whitelist taken from the spec's
//     allowedActions when supplied
//
// allowedActions is sourced from manifest.actions[].name by the
// caller (AppViewHost) and threaded through here.

class CustomLayout extends StatelessWidget {
  const CustomLayout({
    super.key,
    required this.spec,
    required this.data,
    required this.runner,
    this.allowedActions = const {},
  });

  final ViewSpec spec;
  final Map<String, dynamic> data;
  final ActionRunner runner;
  final Set<String> allowedActions;

  @override
  Widget build(BuildContext context) {
    // The action MAY return either { ... node ... } or
    // { "tree": { ... node ... } }; accept both — saves authors
    // from a wrapper-nesting error.
    Object? raw = data;
    if (data['tree'] is Map<String, dynamic>) {
      raw = data['tree'];
    }
    final node = A2UINode.parse(raw);
    if (node == null) {
      return _emptyState(context, message: 'custom view: empty payload');
    }
    final cfg = A2UIValidationConfig(allowedActions: allowedActions);
    final result = validateA2UI(node, cfg);
    return SingleChildScrollView(
      padding: const EdgeInsets.all(BiuTokens.space3),
      child: A2UIRenderer(root: node, runner: runner, validation: result),
    );
  }
}

// ─── WebViewLayout (M12) ────────────────────────────────────
//
// Now wraps the v2.0 webview embed (lib/features/apps/host/webview_panel.dart)
// where supported, falling back to "open externally" on Web (where
// webview_flutter is unavailable). The panel takes care of the
// browser bar, anchor-host nav guard, and JS bridge disablement.

class WebViewLayout extends StatelessWidget {
  const WebViewLayout({super.key, required this.spec, required this.url});
  final ViewSpec spec;
  final String url;
  @override
  Widget build(BuildContext context) {
    if (url.isEmpty) {
      return _emptyState(context, message: 'webview spec missing url');
    }
    return WebViewPanel(initialUrl: url);
  }
}

// ─── Shared helpers ──────────────────────────────────────────

Widget _emptyState(BuildContext context, {String message = '暂无数据'}) {
  return Center(
    child: Padding(
      padding: const EdgeInsets.all(BiuTokens.space5),
      child: Text(message,
          style: Theme.of(context).textTheme.bodyMedium?.copyWith(
              color: Theme.of(context).colorScheme.onSurfaceVariant)),
    ),
  );
}

Widget _renderItem(
  BuildContext context,
  ViewSpec spec,
  Map<String, dynamic> item,
  Map<String, dynamic> data,
  Map<String, dynamic> routeParams,
  ActionRunner runner,
) {
  final tpl = spec.itemTemplate;
  if (tpl == null) {
    // Default render: identifier-equivalent first key.
    final firstVal = item.values.isEmpty ? '' : item.values.first.toString();
    return ListTile(title: Text(firstVal));
  }
  final interp = Interpolator({
    'item': item,
    'data': data,
    'route': routeParams,
  });
  return Padding(
    padding: const EdgeInsets.all(BiuTokens.space2),
    child: ComponentRenderer(
      template: tpl,
      runner: runner,
      title: interp.render(tpl.title),
      subtitle: interp.render(tpl.subtitle),
      body: interp.render(tpl.body),
      image: interp.render(tpl.image),
    ),
  );
}

String _renderItemString(
  String template,
  Map<String, dynamic> item,
  Map<String, dynamic> data,
  Map<String, dynamic> routeParams,
) {
  return Interpolator({
    'item': item,
    'data': data,
    'route': routeParams,
  }).render(template);
}

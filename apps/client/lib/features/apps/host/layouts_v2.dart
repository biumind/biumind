// M16 — v2.0 layout widgets: grid / dashboard / agent_chat.
//
// Kept in a sibling file to layouts.dart so the diff against v1.5
// stays small and import dependencies are explicit.
//
// Each layout receives the parsed ViewSpec plus the per-installation
// resolved data (single map for grid; multi-map for dashboard
// because each card has its own data_source). AgentChat doesn't
// consume data — it's a deep-link to the existing chat surface.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../app/theme.dart';
import '../domain/interpolator.dart';
import '../domain/view_spec.dart';
import 'action_runner.dart';
import 'app_view_host.dart' show ViewDataKey, viewDataFutureProvider;

// ─── GridLayout ─────────────────────────────────────────────
//
// Responsive tile grid. Reuses ViewItemTemplate (same as list) so
// authors who change `layout: list → grid` keep their existing
// rendering. Tap on a tile follows the same detailView semantics
// as ListDetailLayout: push an AppViewHost for spec.detail_view
// (when set) with route param `id` from the tile.

class GridLayout extends StatelessWidget {
  const GridLayout({
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
      return const _Empty('暂无数据');
    }
    final grid = spec.grid ?? const ViewGrid();
    return LayoutBuilder(
      builder: (context, constraints) {
        final cols = grid.columnsForWidth(constraints.maxWidth);
        return GridView.builder(
          padding: const EdgeInsets.all(BiuTokens.space3),
          gridDelegate: SliverGridDelegateWithFixedCrossAxisCount(
            crossAxisCount: cols,
            mainAxisSpacing: grid.spacing.toDouble(),
            crossAxisSpacing: grid.spacing.toDouble(),
            childAspectRatio: grid.aspectRatio <= 0 ? 1.0 : grid.aspectRatio,
          ),
          itemCount: items.length,
          itemBuilder: (context, i) {
            final item = items[i];
            if (item is! Map<String, dynamic>) return const SizedBox.shrink();
            return _GridTile(
              spec: spec,
              item: item,
              data: data,
              routeParams: routeParams,
              runner: runner,
            );
          },
        );
      },
    );
  }
}

class _GridTile extends StatelessWidget {
  const _GridTile({
    required this.spec,
    required this.item,
    required this.data,
    required this.routeParams,
    required this.runner,
  });

  final ViewSpec spec;
  final Map<String, dynamic> item;
  final Map<String, dynamic> data;
  final Map<String, dynamic> routeParams;
  final ActionRunner runner;

  @override
  Widget build(BuildContext context) {
    final tpl = spec.itemTemplate;
    final interp = Interpolator({'item': item, 'data': data, 'route': routeParams});
    final title = tpl == null ? (item.values.isEmpty ? '' : item.values.first.toString()) : interp.render(tpl.title);
    final subtitle = tpl == null ? '' : interp.render(tpl.subtitle);
    final image = tpl == null ? '' : interp.render(tpl.image);
    final body = tpl == null ? '' : interp.render(tpl.body);

    final card = Card(
      clipBehavior: Clip.antiAlias,
      child: InkWell(
        onTap: () => _onTap(context),
        child: Padding(
          padding: const EdgeInsets.all(BiuTokens.space2),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              if (image.isNotEmpty)
                Expanded(
                  child: ClipRRect(
                    borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
                    child: Image.network(image,
                        fit: BoxFit.cover,
                        width: double.infinity,
                        errorBuilder: (_, _, _) => Container(
                              color: Theme.of(context).colorScheme.surfaceContainerHigh,
                            )),
                  ),
                )
              else
                const Spacer(),
              const SizedBox(height: 6),
              if (title.isNotEmpty)
                Text(title,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: Theme.of(context).textTheme.titleSmall),
              if (subtitle.isNotEmpty)
                Text(subtitle,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: Theme.of(context).textTheme.bodySmall?.copyWith(
                          color: Theme.of(context).colorScheme.onSurfaceVariant,
                        )),
              if (body.isNotEmpty && image.isEmpty)
                Padding(
                  padding: const EdgeInsets.only(top: 4),
                  child: Text(body,
                      maxLines: 2,
                      overflow: TextOverflow.ellipsis,
                      style: Theme.of(context).textTheme.bodySmall),
                ),
            ],
          ),
        ),
      ),
    );
    return card;
  }

  void _onTap(BuildContext context) {
    if (spec.detailView.isEmpty) return;
    // Construct an action ref with route /apps/<slug>/<detail>/<id>.
    // Reuses ActionRunner's route handler so the navigation stays
    // identical to list_detail behaviour on mobile.
    final id = item['id']?.toString() ?? '';
    final route = '/apps/${runner.appIdentifier}/${spec.detailView}${id.isEmpty ? '' : '/$id'}';
    runner.run(context, ViewActionRef(label: '', route: route));
  }
}

// ─── DashboardLayout ────────────────────────────────────────
//
// Multi-card overview. Each card carries its own data_source —
// we open a viewDataFutureProvider per (install_id, card.id) tuple
// so failures in one card don't bring down the page, and refresh
// can be card-scoped if needed in v2.5.
//
// Card spans use the standard 12-column layout (Row of Wrap with
// fractional widths). On narrow screens (<600 dp) every card
// becomes full-width regardless of declared span.

class DashboardLayout extends ConsumerWidget {
  const DashboardLayout({
    super.key,
    required this.spec,
    required this.installId,
    required this.appIdentifier,
    required this.routeParams,
    required this.runner,
  });

  final ViewSpec spec;
  final String installId;
  final String appIdentifier;
  final Map<String, dynamic> routeParams;
  final ActionRunner runner;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    if (spec.cards.isEmpty) {
      return const _Empty('dashboard 没有 cards');
    }
    return LayoutBuilder(
      builder: (context, constraints) {
        final narrow = constraints.maxWidth < 600;
        return SingleChildScrollView(
          padding: const EdgeInsets.all(BiuTokens.space3),
          child: Wrap(
            spacing: BiuTokens.space3,
            runSpacing: BiuTokens.space3,
            children: spec.cards.map((card) {
              final span = narrow ? 12 : card.span.clamp(1, 12);
              final available = constraints.maxWidth - 2 * BiuTokens.space3;
              // Wrap uses pixel widths, not fractions.
              final cardWidth = (available * span / 12) - BiuTokens.space3;
              return SizedBox(
                width: cardWidth.clamp(160, available).toDouble(),
                child: _DashboardCard(
                  spec: spec,
                  card: card,
                  installId: installId,
                  appIdentifier: appIdentifier,
                ),
              );
            }).toList(),
          ),
        );
      },
    );
  }
}

class _DashboardCard extends ConsumerWidget {
  const _DashboardCard({
    required this.spec,
    required this.card,
    required this.installId,
    required this.appIdentifier,
  });

  final ViewSpec spec;
  final ViewCard card;
  final String installId;
  final String appIdentifier;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final ds = card.dataSource;
    if (ds == null || ds.action.isEmpty) {
      return _cardShell(
        context,
        title: card.title.isEmpty ? card.id : card.title,
        child: Text('未配置 data_source',
            style: Theme.of(context).textTheme.bodySmall),
      );
    }
    final key = ViewDataKey(
      installId: installId,
      appIdentifier: appIdentifier,
      viewId: '${spec.id}#${card.id}',
      action: ds.action,
      input: ds.input,
    );
    final async = ref.watch(viewDataFutureProvider(key));
    return _cardShell(
      context,
      title: card.title.isEmpty ? card.id : card.title,
      child: async.when(
        loading: () => const SizedBox(
            height: 48, child: Center(child: CircularProgressIndicator())),
        error: (e, _) => Text('加载失败：$e',
            style: Theme.of(context).textTheme.bodySmall?.copyWith(
                  color: Theme.of(context).colorScheme.error,
                )),
        data: (data) => _renderCardBody(context, card, data),
      ),
    );
  }

  Widget _cardShell(BuildContext context,
      {required String title, required Widget child}) {
    return Card(
      child: Padding(
        padding: const EdgeInsets.all(BiuTokens.space3),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(title,
                style: Theme.of(context).textTheme.titleSmall?.copyWith(
                      color: Theme.of(context).colorScheme.onSurfaceVariant,
                    )),
            const SizedBox(height: BiuTokens.space2),
            child,
          ],
        ),
      ),
    );
  }
}

Widget _renderCardBody(
    BuildContext context, ViewCard card, Map<String, dynamic> data) {
  final raw = card.field.isEmpty ? data : _resolvePath(data, card.field);
  switch (card.kind) {
    case 'number':
      final n = (raw is num)
          ? raw
          : num.tryParse(raw?.toString() ?? '') ?? 0;
      return Text(_formatNumber(n, card.format),
          style: Theme.of(context).textTheme.headlineMedium?.copyWith(
                fontWeight: FontWeight.w600,
              ));
    case 'list':
      final list = (raw is List) ? raw : const [];
      if (list.isEmpty) {
        return Text('暂无',
            style: Theme.of(context).textTheme.bodySmall?.copyWith(
                  color: Theme.of(context).colorScheme.onSurfaceVariant,
                ));
      }
      return Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: list.take(5).map<Widget>((e) {
          final s = e is Map ? (e['title']?.toString() ?? e.values.first.toString()) : e.toString();
          return Padding(
            padding: const EdgeInsets.symmetric(vertical: 2),
            child: Text('• $s',
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
                style: Theme.of(context).textTheme.bodyMedium),
          );
        }).toList(),
      );
    case 'chart':
      // v2.0: placeholder — fl_chart integration deferred to v2.5.
      return Container(
        height: 80,
        alignment: Alignment.center,
        decoration: BoxDecoration(
          color: Theme.of(context).colorScheme.surfaceContainerHigh,
          borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
        ),
        child: Text('chart (v2.5)',
            style: Theme.of(context).textTheme.bodySmall?.copyWith(
                  color: Theme.of(context).colorScheme.onSurfaceVariant,
                )),
      );
    case 'text':
    default:
      return Text(raw?.toString() ?? '',
          style: Theme.of(context).textTheme.bodyMedium);
  }
}

dynamic _resolvePath(Map<String, dynamic> root, String dotted) {
  dynamic node = root;
  for (final seg in dotted.split('.')) {
    if (node is Map) {
      node = node[seg];
    } else {
      return null;
    }
  }
  return node;
}

String _formatNumber(num n, String format) {
  switch (format) {
    case 'comma':
      return _comma(n);
    case 'percent':
      return '${(n * 100).toStringAsFixed(1)}%';
  }
  // Drop trailing .0 for whole numbers.
  if (n == n.truncateToDouble()) return n.toInt().toString();
  return n.toString();
}

String _comma(num n) {
  final s = n.toInt().toString();
  final neg = s.startsWith('-');
  final body = neg ? s.substring(1) : s;
  final buf = StringBuffer();
  for (var i = 0; i < body.length; i++) {
    if (i > 0 && (body.length - i) % 3 == 0) buf.write(',');
    buf.write(body[i]);
  }
  return neg ? '-$buf' : buf.toString();
}

// ─── AgentChatLayout ────────────────────────────────────────
//
// Lightweight v2.0 implementation: shows the panel header (title +
// initial prompt + tool-filter chips) and a button that deep-links
// to the existing /chat route with the agent_id pre-selected.
//
// Reasoning: the /chat surface already has streaming, history,
// memory wiring and tool-permission UI; embedding it inside an App
// view would require lifting that state up. Following the same
// pattern as WebViewLayout (placeholder + external open) keeps the
// scope tight; full embedded chat lands together with the Container
// form in M14 where the App can run its own UI tree anyway.

class AgentChatLayout extends StatelessWidget {
  const AgentChatLayout({
    super.key,
    required this.spec,
    required this.routeParams,
  });

  final ViewSpec spec;
  final Map<String, dynamic> routeParams;

  @override
  Widget build(BuildContext context) {
    final cfg = spec.agentChat ?? const ViewAgentChat();
    if (spec.agentId.isEmpty) {
      return const _Empty('agent_chat 缺少 agent_id');
    }
    final interp = Interpolator({'route': routeParams});
    final prompt = interp.render(cfg.initialPrompt);
    final title = cfg.title.isNotEmpty ? cfg.title : spec.title;
    return Padding(
      padding: const EdgeInsets.all(BiuTokens.space4),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          if (title.isNotEmpty)
            Text(title, style: Theme.of(context).textTheme.titleMedium),
          const SizedBox(height: BiuTokens.space2),
          Card(
            child: Padding(
              padding: const EdgeInsets.all(BiuTokens.space3),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Row(
                    children: [
                      const Icon(Icons.smart_toy_outlined, size: 18),
                      const SizedBox(width: 8),
                      Expanded(
                        child: Text('Agent: ${spec.agentId}',
                            style: Theme.of(context).textTheme.bodySmall),
                      ),
                    ],
                  ),
                  if (prompt.isNotEmpty) ...[
                    const SizedBox(height: BiuTokens.space2),
                    Text('初始提示',
                        style: Theme.of(context).textTheme.labelSmall?.copyWith(
                              color:
                                  Theme.of(context).colorScheme.onSurfaceVariant,
                            )),
                    const SizedBox(height: 2),
                    Text(prompt,
                        style: Theme.of(context).textTheme.bodyMedium),
                  ],
                  if (cfg.toolFilter.isNotEmpty) ...[
                    const SizedBox(height: BiuTokens.space2),
                    Text('工具白名单',
                        style: Theme.of(context).textTheme.labelSmall?.copyWith(
                              color:
                                  Theme.of(context).colorScheme.onSurfaceVariant,
                            )),
                    const SizedBox(height: 4),
                    Wrap(
                      spacing: 6,
                      runSpacing: 4,
                      children: cfg.toolFilter
                          .map((t) => Chip(
                                label: Text(t,
                                    style:
                                        Theme.of(context).textTheme.labelSmall),
                                visualDensity: VisualDensity.compact,
                              ))
                          .toList(),
                    ),
                  ],
                ],
              ),
            ),
          ),
          const SizedBox(height: BiuTokens.space3),
          FilledButton.icon(
            onPressed: () => _openChat(context, prompt),
            icon: const Icon(Icons.chat_bubble_outline),
            label: const Text('打开会话'),
          ),
          const SizedBox(height: BiuTokens.space2),
          Text(
            'v2.0 以独立窗口打开 /chat；v2.5 嵌入面板内联。',
            style: Theme.of(context).textTheme.bodySmall?.copyWith(
                  color: Theme.of(context).colorScheme.onSurfaceVariant,
                ),
          ),
        ],
      ),
    );
  }

  void _openChat(BuildContext context, String initialPrompt) {
    // Deep-link via GoRouter. The chat page can parse ?agent=<id>
    // and ?prompt=<text> in v2.5; for now the params are forwarded
    // and the user lands on /chat. Wrapped in try/catch so a missing
    // GoRouter context doesn't crash the App view.
    try {
      final params = <String, String>{};
      if (spec.agentId.isNotEmpty) params['agent'] = spec.agentId;
      if (initialPrompt.isNotEmpty) params['prompt'] = initialPrompt;
      final query = params.isEmpty
          ? ''
          : '?${params.entries.map((e) => '${Uri.encodeQueryComponent(e.key)}=${Uri.encodeQueryComponent(e.value)}').join('&')}';
      GoRouter.of(context).go('/chat$query');
    } catch (_) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('请在「会话」中选择 Agent: ${spec.agentId}')),
      );
    }
  }
}

class _Empty extends StatelessWidget {
  const _Empty(this.msg);
  final String msg;
  @override
  Widget build(BuildContext context) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(BiuTokens.space5),
        child: Text(msg,
            style: Theme.of(context).textTheme.bodyMedium?.copyWith(
                  color: Theme.of(context).colorScheme.onSurfaceVariant,
                )),
      ),
    );
  }
}

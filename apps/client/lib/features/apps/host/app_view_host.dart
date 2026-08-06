// AppViewHost — top-level renderer that turns a (install, view_id)
// pair into a fully-rendered Page.
//
// Flow:
//   1. Resolve the install via installationProvider(installId).
//   2. Resolve the manifest via manifestProvider(identifier); pick
//      the matching view by id.
//   3. Resolve the view's data_source.action by POST /v1/apps/{name}/invoke.
//   4. Hand off to the matching layout widget.
//
// Refresh paths:
//   - User pull-to-refresh → invalidate the data future.
//   - on_success.refresh in an action → same invalidation.
//   - Realtime topic events listed in spec.refresh_on → not yet
//     wired in v1.5 (lands together with the M8 sidebar Realtime
//     bridge); the field is parsed and stored so manifests are
//     forward-compatible.
//
// Stays a ConsumerStatefulWidget so each open carries its own
// route-param map (which can change on push) and its own ActionRunner
// instance (closures over installId).

import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import 'package:http/http.dart' as http;

import '../../../app/theme.dart';
import '../../../core/layout/phone_nav.dart';
import '../../../data/api/apps_client.dart';
import '../../../data/apps_providers.dart';
import '../../../shared/page_scaffold.dart';
import '../../../data/dev_apps_provider.dart';
import '../builtin/rss/rss_app_page.dart';
import '../domain/view_spec.dart';
import 'action_runner.dart';
import 'layouts.dart';
import 'layouts_v2.dart';

/// Family-keyed provider for view data. The key combines install_id
/// + view_id + a ::JSON-encoded route-params hash so two opens of the
/// same view with different route params don't share a cache.
///
/// Dev path: install IDs prefixed with `dev:` route to the local
/// `biu app run --dev` server (127.0.0.1:7099) instead of the cloud
/// app_center, so view iteration with `--mock fixtures/` works
/// without ever touching the real installation pipeline.
final viewDataFutureProvider =
    FutureProvider.family<Map<String, dynamic>, ViewDataKey>(
  (ref, key) async {
    if (key.action.isEmpty) return const {};
    if (key.installId.startsWith('dev:')) {
      return _devInvoke(ref, key);
    }
    // select(baseUrl): token 轮换不重拉 (app view 不闪); token 现读保新鲜.
    ref.watch(appsClientProvider.select((c) => c?.baseUrl));
    final client = ref.read(appsClientProvider);
    final token = ref.read(appsBearerProvider);
    if (client == null || token == null) return const {};
    return client.invoke(
      identifier: key.appIdentifier,
      action: key.action,
      input: key.input,
      token: token,
    );
  },
);

Future<Map<String, dynamic>> _devInvoke(Ref ref, ViewDataKey key) async {
  final endpoint = ref.read(devAppsEndpointProvider);
  if (endpoint == null) return const {};
  final slug = key.installId.substring('dev:'.length);
  final uri = endpoint.replace(path: '/v1/dev/apps/$slug/invoke');
  final body = jsonEncode({'action': key.action, 'input': key.input});
  final resp = await http
      .post(uri, headers: {'content-type': 'application/json'}, body: body)
      .timeout(const Duration(seconds: 5));
  if (resp.statusCode != 200) {
    throw Exception('dev invoke ${key.action}: ${resp.statusCode} ${resp.body}');
  }
  final decoded = jsonDecode(resp.body);
  if (decoded is Map<String, dynamic>) return decoded;
  return {'result': decoded};
}

class ViewDataKey {
  final String installId;
  final String appIdentifier;
  final String viewId;
  final String action;
  final Map<String, dynamic> input;

  const ViewDataKey({
    required this.installId,
    required this.appIdentifier,
    required this.viewId,
    required this.action,
    required this.input,
  });

  @override
  bool operator ==(Object other) {
    return other is ViewDataKey &&
        other.installId == installId &&
        other.appIdentifier == appIdentifier &&
        other.viewId == viewId &&
        other.action == action &&
        _sameMap(other.input, input);
  }

  @override
  int get hashCode => Object.hash(installId, appIdentifier, viewId, action, _mapHash(input));

  static bool _sameMap(Map<String, dynamic> a, Map<String, dynamic> b) {
    if (a.length != b.length) return false;
    for (final entry in a.entries) {
      if (b[entry.key].toString() != entry.value.toString()) return false;
    }
    return true;
  }

  static int _mapHash(Map<String, dynamic> m) {
    var h = 0;
    m.forEach((k, v) => h ^= k.hashCode ^ v.toString().hashCode);
    return h;
  }
}

class AppViewHost extends ConsumerWidget {
  const AppViewHost({
    super.key,
    required this.installId,
    required this.viewId,
    this.routeParams = const {},
  });

  final String installId;
  final String viewId;
  final Map<String, dynamic> routeParams;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    // Dev path: short-circuit the install / manifest lookup. The
    // dev server has the manifest in memory; we shape it into the
    // same Installation contract used by the cloud path so the rest
    // of the renderer doesn't have to branch.
    if (installId.startsWith('dev:')) {
      final slug = installId.substring('dev:'.length);
      final devApps = ref.watch(devAppsProvider);
      return devApps.when(
        loading: () => const _Loading(),
        error: (e, _) => _Error('dev: $e'),
        data: (apps) {
          final dev = apps.where((a) => a.slug == slug).firstOrNull;
          if (dev == null) {
            return const _Error(
                'dev app not loaded (是否在运行 `biu app run --dev`？)');
          }
          // Synthesise an Installation. Only id + identifier are read
          // by _ViewRenderer; the rest can be defaulted.
          final now = DateTime.now();
          final synth = Installation(
            id: installId,
            appId: 'dev:${dev.slug}',
            identifier: dev.identifier,
            version: dev.version,
            scope: 'user',
            scopeId: '',
            enabled: true,
            forced: false,
            installedAt: now,
            updatedAt: now,
          );
          // Resolve viewSpec from the dev manifest directly.
          final views = (dev.manifest['views'] as List?)
                  ?.whereType<Map<String, dynamic>>()
                  .map(ViewSpec.fromJson)
                  .toList(growable: false) ??
              const <ViewSpec>[];
          ViewSpec? spec;
          for (final v in views) {
            if (v.id == viewId) {
              spec = v;
              break;
            }
          }
          if (spec == null) {
            return _Error('view "$viewId" not found in dev manifest');
          }
          return _ViewRenderer(
            installation: synth,
            spec: spec,
            manifest: dev.manifest,
            routeParams: routeParams,
          );
        },
      );
    }

    final installAsync = ref.watch(installationProvider(installId));
    return installAsync.when(
      loading: () => const _Loading(),
      error: (e, _) => _Error('$e'),
      data: (install) {
        if (install == null) return const _Error('install not found');
        // Built-in hand-built UIs short-circuit the manifest-driven
        // renderer. The generic AppViewHost is fine for simple CRUD
        // apps but RSS / radar / boards need polish that doesn't fit
        // the declarative ViewSpec model. Identifier check is the
        // bright line — manifests for these slugs still ship with a
        // legal `views` array so server-side / SDK contracts are
        // unaffected, but we never render that array in the UI.
        if (install.identifier == 'rss') {
          return RssAppPage(
            installation: install,
            viewId: viewId,
            routeParams: routeParams,
          );
        }
        return _Resolved(
          installation: install,
          viewId: viewId,
          routeParams: routeParams,
        );
      },
    );
  }
}

class _Resolved extends ConsumerWidget {
  const _Resolved({
    required this.installation,
    required this.viewId,
    required this.routeParams,
  });

  final Installation installation;
  final String viewId;
  final Map<String, dynamic> routeParams;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final manifestAsync = ref.watch(manifestProvider(installation.identifier));
    return manifestAsync.when(
      loading: () => const _Loading(),
      error: (e, _) => _Error('$e'),
      data: (manifest) {
        final views = (manifest['views'] as List?)
                ?.whereType<Map<String, dynamic>>()
                .map(ViewSpec.fromJson)
                .toList(growable: false) ??
            const <ViewSpec>[];
        ViewSpec? spec;
        for (final v in views) {
          if (v.id == viewId) {
            spec = v;
            break;
          }
        }
        if (spec == null) return _Error('view "$viewId" not found in manifest');
        return _ViewRenderer(
          installation: installation,
          spec: spec,
          manifest: manifest,
          routeParams: routeParams,
        );
      },
    );
  }
}

class _ViewRenderer extends ConsumerWidget {
  const _ViewRenderer({
    required this.installation,
    required this.spec,
    required this.manifest,
    required this.routeParams,
  });

  final Installation installation;
  final ViewSpec spec;
  final Map<String, dynamic> manifest;
  final Map<String, dynamic> routeParams;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    // Build the data-fetch key for layouts that need invocation.
    final dataKey = ViewDataKey(
      installId: installation.id,
      appIdentifier: installation.identifier,
      viewId: spec.id,
      action: spec.dataSource?.action ?? '',
      input: spec.dataSource?.input ?? const {},
    );

    // Dashboard / agent_chat layouts don't use the top-level fetch
    // (dashboard fetches per-card; agent_chat is data-less). Skip the
    // outer provider so we don't fire a useless invoke and then wait
    // on its loading state before painting cards.
    final skipTopFetch =
        spec.layout == ViewLayout.dashboard || spec.layout == ViewLayout.agentChat;
    final dataAsync = (skipTopFetch ||
            spec.dataSource == null ||
            spec.dataSource!.action.isEmpty)
        ? const AsyncValue<Map<String, dynamic>>.data({})
        : ref.watch(viewDataFutureProvider(dataKey));

    final runner = ActionRunner(
      ref: ref,
      appIdentifier: installation.identifier,
      installId: installation.id,
      onRouteNavigate: (ctx, route) {
        // v2.0+ navigation: parse "/apps/<slug>/<view>[/<id>]" 并经由
        // GoRouter push 注册路由 `/apps/host/<installId>/<viewId>?params=<json>`
        // —— 这样浏览器后退按钮 / 深度链接 / 侧边栏 ctx.go 都能一致工作,
        // 也避免了 Navigator.push 把页面塞进 ShellRoute 内层栈卡死侧边栏的
        // 老坑（参见 router.dart _Sidebar._navigateTo 文档）。
        final parsed = _parseAppRoute(route);
        if (parsed == null) {
          ScaffoldMessenger.of(ctx).showSnackBar(
            SnackBar(content: Text('未识别的路由：$route')),
          );
          return;
        }
        final qp = parsed.params.isEmpty
            ? ''
            : '?params=${Uri.encodeComponent(jsonEncode(parsed.params))}';
        ctx.push('/apps/host/${installation.id}/${parsed.viewId}$qp');
      },
      onRefresh: () => ref.invalidate(viewDataFutureProvider(dataKey)),
    );

    return PageScaffold(
      title: spec.title.isEmpty ? installation.identifier : spec.title,
      // 子页头左位 ← (手机形态; 桌面 shrink 不占位, §3.3)。
      leading: const PhoneBackButton(),
      actions: [
        IconButton(
          onPressed: () => ref.invalidate(viewDataFutureProvider(dataKey)),
          icon: const Icon(Icons.refresh),
        ),
        ...spec.toolbar.map((a) => IconButton(
              tooltip: a.label,
              onPressed: () => runner.run(context, a),
              icon: Icon(_iconFor(a.icon)),
            )),
      ],
      child: dataAsync.when(
        loading: () => const _Loading(),
        error: (e, _) => _Error('$e'),
        data: (data) {
          switch (spec.layout) {
            case ViewLayout.list:
              return ListLayout(
                spec: spec, data: data, routeParams: routeParams, runner: runner);
            case ViewLayout.listDetail:
              return ListDetailLayout(
                spec: spec, data: data, routeParams: routeParams, runner: runner);
            case ViewLayout.form:
              return FormLayout(
                spec: spec,
                schema: _resolveSchemaRef(manifest, spec.schemaRef),
                runner: runner,
              );
            case ViewLayout.webView:
              return WebViewLayout(spec: spec, url: spec.url);
            case ViewLayout.custom:
              // M13: Custom layout — App returns an A2UI tree from
              // data_source.action; renderer enforces depth/count
              // caps + on_click action whitelist (from manifest).
              final allowed = <String>{
                for (final a in (manifest['actions'] as List?) ?? const [])
                  if (a is Map<String, dynamic>)
                    if (a['name'] is String) a['name'] as String,
              };
              return CustomLayout(
                spec: spec,
                data: data,
                runner: runner,
                allowedActions: allowed,
              );
            case ViewLayout.grid:
              return GridLayout(
                spec: spec, data: data, routeParams: routeParams, runner: runner);
            case ViewLayout.dashboard:
              // Dashboard ignores the top-level data fetch — each card
              // opens its own provider keyed on (install, view#card).
              return DashboardLayout(
                spec: spec,
                installId: installation.id,
                appIdentifier: installation.identifier,
                routeParams: routeParams,
                runner: runner,
              );
            case ViewLayout.agentChat:
              return AgentChatLayout(spec: spec, routeParams: routeParams);
            default:
              return _UnsupportedLayout(layout: spec.layout);
          }
        },
      ),
    );
  }
}

class _Loading extends StatelessWidget {
  const _Loading();
  @override
  Widget build(BuildContext context) {
    return const Center(child: CircularProgressIndicator());
  }
}

class _Error extends StatelessWidget {
  const _Error(this.msg);
  final String msg;
  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.all(BiuTokens.space5),
      child: Center(child: SelectableText(msg)),
    );
  }
}

class _UnsupportedLayout extends StatelessWidget {
  const _UnsupportedLayout({required this.layout});
  final ViewLayout layout;
  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.all(BiuTokens.space5),
      child: Center(
        child: Text('Layout "${layout.name}" requires a newer client (v2.0+).',
            style: Theme.of(context).textTheme.bodyMedium),
      ),
    );
  }
}

// schema_ref resolution: "actions.<name>.input_schema" path lookup.
Map<String, dynamic> _resolveSchemaRef(Map<String, dynamic> manifest, String ref) {
  if (ref.isEmpty) return const {};
  final parts = ref.split('.');
  dynamic node = manifest;
  if (parts.first == 'actions' && parts.length >= 2) {
    final actions = (manifest['actions'] as List?) ?? const [];
    Map<String, dynamic>? matched;
    for (final a in actions) {
      if (a is Map<String, dynamic> && a['name'] == parts[1]) {
        matched = a;
        break;
      }
    }
    if (matched == null) return const {};
    node = matched;
    for (final seg in parts.skip(2)) {
      if (node is Map) {
        node = node[seg];
      } else {
        return const {};
      }
    }
  } else {
    for (final seg in parts) {
      if (node is Map) {
        node = node[seg];
      } else {
        return const {};
      }
    }
  }
  return (node is Map<String, dynamic>) ? node : const {};
}

class _ParsedAppRoute {
  final String viewId;
  final Map<String, dynamic> params;
  const _ParsedAppRoute(this.viewId, this.params);
}

_ParsedAppRoute? _parseAppRoute(String route) {
  // Expected shape: /apps/<slug>/<view>[/:param=value]*
  // For v1.5 we accept simple routes /apps/<slug>/<view>/<param>
  // and treat <param> as the "id" route param.
  if (!route.startsWith('/apps/')) return null;
  final segments = route.substring('/apps/'.length).split('/').where((s) => s.isNotEmpty).toList();
  if (segments.length < 2) {
    // /apps/<slug> defaults back to home view.
    return const _ParsedAppRoute('home', {});
  }
  final viewId = segments[1];
  final params = <String, dynamic>{};
  if (segments.length >= 3) params['id'] = segments[2];
  return _ParsedAppRoute(viewId, params);
}

IconData _iconFor(String name) {
  switch (name) {
    case 'add':         return Icons.add;
    case 'open':        return Icons.open_in_new;
    case 'trash':       return Icons.delete_outline;
    case 'refresh':     return Icons.refresh;
    case 'edit':        return Icons.edit_outlined;
    case 'download':    return Icons.download_outlined;
    case 'upload':      return Icons.upload_outlined;
    case 'share':       return Icons.share_outlined;
    case 'settings':    return Icons.settings_outlined;
  }
  return Icons.touch_app_outlined;
}

// Dev-loaded Apps provider — pulls from the local `biu app run --dev`
// HTTP server (default 127.0.0.1:7099). Apps surfaced this way are
// NOT in app_center.installations; the App Center page renders them
// in a separate "开发中" section so users (developers) don't confuse
// them with real installations.
//
// Probe strategy: a 250ms HEAD on /v1/dev/health on every refresh.
// If the dev server isn't running, the probe fails fast and the
// provider returns an empty list — no UI noise. If running, we pull
// /v1/dev/apps and surface its DevApp[] payload.

import 'dart:async';
import 'dart:convert';
import 'dart:io' show Platform;

import 'package:flutter/foundation.dart' show kIsWeb;
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:http/http.dart' as http;

/// One App as advertised by the local dev server.
class DevApp {
  final String slug;
  final String identifier;
  final String title;
  final String version;
  final Map<String, dynamic> manifest;
  final String sourcePath;
  final bool mock;

  const DevApp({
    required this.slug,
    required this.identifier,
    required this.title,
    required this.version,
    required this.manifest,
    required this.sourcePath,
    required this.mock,
  });

  factory DevApp.fromJson(Map<String, dynamic> j) => DevApp(
        slug:        j['slug']        as String? ?? '',
        identifier:  j['identifier']  as String? ?? '',
        title:       j['title']       as String? ?? '',
        version:     j['version']     as String? ?? '',
        manifest:    (j['manifest']   as Map<String, dynamic>?) ?? const {},
        sourcePath:  j['source_path'] as String? ?? '',
        mock:        j['mock']        as bool?   ?? false,
      );
}

/// Default endpoint of `biu app run --dev`. Overridable via the
/// BIUMIND_DEV_APPS_URL env var on native (web has no env access).
final devAppsEndpointProvider = Provider<Uri?>((ref) {
  // Web cannot reach localhost CLI; skip entirely.
  if (kIsWeb) return null;
  try {
    final override = Platform.environment['BIUMIND_DEV_APPS_URL'];
    if (override != null && override.isNotEmpty) return Uri.parse(override);
  } catch (_) {
    // Platform.environment throws on unsupported platforms; fall back.
  }
  return Uri.parse('http://127.0.0.1:7099');
});

/// Pulls the current dev-app list. Empty when:
///  - web platform,
///  - dev server not running (connection refused / timeout),
///  - response shape unexpected.
///
/// Auto-refreshes on demand via ref.invalidate; the dev server's SSE
/// /v1/dev/events stream is consumed separately by [devAppsEventsProvider]
/// to push UI refresh on manifest reloads.
final devAppsProvider = FutureProvider<List<DevApp>>((ref) async {
  final endpoint = ref.watch(devAppsEndpointProvider);
  if (endpoint == null) return const [];
  final client = http.Client();
  try {
    // Health probe first — short timeout so a missing dev server
    // doesn't stall the App Center page on every refresh.
    final health = await client
        .get(endpoint.replace(path: '/v1/dev/health'))
        .timeout(const Duration(milliseconds: 250));
    if (health.statusCode != 200) return const [];

    final list = await client
        .get(endpoint.replace(path: '/v1/dev/apps'))
        .timeout(const Duration(seconds: 1));
    if (list.statusCode != 200) return const [];

    final body = jsonDecode(list.body);
    if (body is! Map<String, dynamic>) return const [];
    final apps = body['apps'];
    if (apps is! List) return const [];
    return apps
        .whereType<Map<String, dynamic>>()
        .map(DevApp.fromJson)
        .toList(growable: false);
  } on TimeoutException {
    return const [];
  } catch (_) {
    return const [];
  } finally {
    client.close();
  }
});

/// Subscribes to the dev server's SSE /v1/dev/events feed and emits
/// the kind of every event so the UI can react (typically by
/// invalidating [devAppsProvider]).
///
/// Yields nothing when the endpoint is unreachable; auto-reconnects
/// on stream end with a 2s back-off.
final devAppsEventsProvider = StreamProvider<String>((ref) async* {
  final endpoint = ref.watch(devAppsEndpointProvider);
  if (endpoint == null) return;
  // Cancellation: when the provider is disposed (page navigates away,
  // hot-restart), the loop should exit. Riverpod doesn't expose
  // ref.mounted on StreamProviderRef in this version, so we hook
  // onDispose into a flag instead.
  var disposed = false;
  ref.onDispose(() => disposed = true);
  while (!disposed) {
    final client = http.Client();
    try {
      final req = http.Request('GET', endpoint.replace(path: '/v1/dev/events'));
      final resp = await client.send(req).timeout(const Duration(seconds: 2));
      if (resp.statusCode != 200) {
        client.close();
        await Future.delayed(const Duration(seconds: 2));
        continue;
      }
      await for (final chunk in resp.stream.transform(utf8.decoder)) {
        // SSE frames separated by blank lines; only `data:` lines carry payload.
        for (final line in chunk.split('\n')) {
          if (!line.startsWith('data:')) continue;
          final body = line.substring(5).trim();
          try {
            final m = jsonDecode(body) as Map<String, dynamic>;
            yield (m['kind'] as String?) ?? 'unknown';
          } catch (_) {
            // ignore malformed frames
          }
        }
      }
    } catch (_) {
      // network error — back off then retry
    } finally {
      client.close();
    }
    await Future.delayed(const Duration(seconds: 2));
  }
});

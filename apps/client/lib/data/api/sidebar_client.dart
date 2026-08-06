// SidebarClient — Dart client for the sidebar layout surface.
//
//   GET    /v1/sidebar/layout?scope=desktop|mobile
//   PUT    /v1/sidebar/layout            { scope, items, expected_version, device }
//   POST   /v1/sidebar/reset?scope=...
//
// Returns Layout with the bumped version on PUT; throws
// SidebarConflict on 409 (the caller GETs the latest, surfaces the
// "another device just edited this" UX, then re-PUTs).

import '_http_helpers.dart';

class SidebarItem {
  final String kind; // "system" | "app"
  final String ref;
  final bool hidden;
  final bool badge;

  const SidebarItem({
    required this.kind,
    required this.ref,
    this.hidden = false,
    this.badge = false,
  });

  factory SidebarItem.fromJson(Map<String, dynamic> j) => SidebarItem(
        kind:   j['kind'] as String? ?? 'system',
        ref:    j['ref'] as String? ?? '',
        hidden: j['hidden'] as bool? ?? false,
        badge:  j['badge'] as bool? ?? false,
      );

  Map<String, dynamic> toJson() => {
        'kind': kind,
        'ref':  ref,
        if (hidden) 'hidden': true,
        if (badge)  'badge':  true,
      };
}

class SidebarLayout {
  final String scope;
  final List<SidebarItem> items;
  final int version;
  final DateTime updatedAt;
  final String updatedByDevice;

  const SidebarLayout({
    required this.scope,
    required this.items,
    required this.version,
    required this.updatedAt,
    this.updatedByDevice = '',
  });

  factory SidebarLayout.fromJson(Map<String, dynamic> j) {
    final list = (j['items'] as List?) ?? const [];
    return SidebarLayout(
      scope:           j['scope'] as String? ?? 'desktop',
      items:           list.whereType<Map<String, dynamic>>().map(SidebarItem.fromJson).toList(),
      version:         j['version'] as int? ?? 1,
      updatedAt:       DateTime.tryParse(j['updated_at'] as String? ?? '')?.toLocal()
                       ?? DateTime.fromMillisecondsSinceEpoch(0),
      updatedByDevice: j['updated_by_device'] as String? ?? '',
    );
  }
}

/// 409 raised by Put. Callers GET to refresh + show the
/// "another device just edited" snackbar before retrying.
class SidebarConflict implements Exception {
  final String message;
  const SidebarConflict(this.message);
  @override
  String toString() => 'SidebarConflict: $message';
}

class SidebarClient {
  final Uri baseUrl;
  SidebarClient(this.baseUrl);

  Future<SidebarLayout> get({
    String scope = 'desktop',
    required String token,
  }) async {
    final j = await apiRequest(
      method: 'GET',
      url: baseUrl.replace(
        path: '${baseUrl.path}/v1/sidebar/layout',
        queryParameters: {'scope': scope},
      ),
      bearerToken: token,
    );
    return SidebarLayout.fromJson(j);
  }

  Future<SidebarLayout> put({
    required String scope,
    required List<SidebarItem> items,
    required int expectedVersion,
    String device = '',
    required String token,
  }) async {
    try {
      final j = await apiRequest(
        method: 'PUT',
        url: baseUrl.replace(path: '${baseUrl.path}/v1/sidebar/layout'),
        bearerToken: token,
        body: {
          'scope': scope,
          'items': items.map((e) => e.toJson()).toList(),
          'expected_version': expectedVersion,
          'device': device,
        },
      );
      return SidebarLayout.fromJson(j);
    } on ApiError catch (e) {
      if (e.status == 409) throw SidebarConflict(e.body);
      rethrow;
    }
  }

  Future<SidebarLayout> reset({
    String scope = 'desktop',
    String device = '',
    required String token,
  }) async {
    final j = await apiRequest(
      method: 'POST',
      url: baseUrl.replace(
        path: '${baseUrl.path}/v1/sidebar/reset',
        queryParameters: {'scope': scope, if (device.isNotEmpty) 'device': device},
      ),
      bearerToken: token,
    );
    return SidebarLayout.fromJson(j);
  }
}

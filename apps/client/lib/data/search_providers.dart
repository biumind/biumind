// Search-page Riverpod wiring.
//
// We don't need a query-by-query auto-provider here because the
// search page debounces typing internally and dispatches a single
// imperative `client.search()` per keystroke wave. The provider only
// hands the raw client over to the page; the page owns its own
// AsyncValue state.

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../services/auth_service.dart';
import 'api/search_client.dart';

/// Search REST client — null when no model-relay credentials are configured.
final searchClientProvider = Provider<SearchClient?>((ref) {
  final creds = ref.watch(hubCredentialsProvider);
  if (creds == null) return null;
  return SearchClient(creds.endpoint, creds.bearerToken);
});

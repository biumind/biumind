// Riverpod providers for ReviewsClient.
//
// Mirrors the wiki_providers.dart pattern: client is null when no model-relay
// credentials are configured; recreating happens automatically when
// the user signs in or switches workspace.

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../services/auth_service.dart';
import 'api/reviews_client.dart';

/// Reviews REST client — null when no model-relay credentials are configured.
final reviewsClientProvider = Provider<ReviewsClient?>((ref) {
  final creds = ref.watch(hubCredentialsProvider);
  if (creds == null) return null;
  return ReviewsClient(creds.endpoint, creds.bearerToken);
});

// Riverpod provider for the Memory API client.
//
// The client is null when no model-relay credentials are configured, mirroring
// wikiRepositoryProvider so feature pages can show a "configure
// Settings" hint instead of crashing.

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../services/auth_service.dart';
import 'api/memory_client.dart';

final memoryClientProvider = Provider<MemoryClient?>((ref) {
  final creds = ref.watch(hubCredentialsProvider);
  if (creds == null) return null;
  return MemoryClient(creds.endpoint, creds.bearerToken);
});

// Riverpod providers for chat billing estimate (W1-9).

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../services/auth_service.dart';
import '../data/billing_estimate_client.dart';

final billingEstimateClientProvider =
    Provider<BillingEstimateClient?>((ref) {
  final creds = ref.watch(hubCredentialsProvider);
  if (creds == null) return null;
  return BillingEstimateClient(
    baseUrl: creds.endpoint,
    bearerProvider: () => creds.bearerToken,
  );
});

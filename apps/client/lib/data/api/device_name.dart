// device_name — describe "the device this client runs on" so the server
// can show meaningful entries on the "已登录设备" page (matches the
// device_kind hint backend infers from this string).
//
// Native:  '<OS> · <hostname>' (e.g. 'macOS · MacBook-Pro') — hostname
//          comes from io.Platform.localHostname when reachable.
// Web:     'Web on <OS>' (e.g. 'Web on macOS') — we pick the host OS via
//          defaultTargetPlatform; we don't sniff navigator.userAgent
//          to keep the implementation depend-free (no package:web).
//
// Caller is expected to wrap the io.Platform calls in try/catch and pass
// us the result — this file stays pure to keep tests trivial.

import 'package:flutter/foundation.dart' show kIsWeb, defaultTargetPlatform, TargetPlatform;

/// Build the device_name passed to /v1/auth/login so the server can
/// distinguish this device from others on the user's session list.
///
/// [hostname] is best-effort: pass `io.Platform.localHostname` from
/// the call site (file that already imports dart:io). null/empty falls
/// back to a bare OS name.
String currentDeviceName({String? hostname}) {
  final os = _osLabel();
  if (kIsWeb) {
    // Browser tabs don't have a hostname concept, but the underlying OS
    // is still a useful disambiguator (Web on macOS vs Web on Windows).
    return 'Web on $os';
  }
  final h = (hostname ?? '').trim();
  return h.isEmpty ? os : '$os · $h';
}

String _osLabel() {
  switch (defaultTargetPlatform) {
    case TargetPlatform.macOS:
      return 'macOS';
    case TargetPlatform.iOS:
      return 'iOS';
    case TargetPlatform.android:
      return 'Android';
    case TargetPlatform.linux:
      return 'Linux';
    case TargetPlatform.windows:
      return 'Windows';
    case TargetPlatform.fuchsia:
      return 'Fuchsia';
  }
}

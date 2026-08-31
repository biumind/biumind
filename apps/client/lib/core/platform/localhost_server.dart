/// Shared `InAppLocalhostServer` feeding Flutter assets to embedded
/// webviews (file:// breaks ESM module loading on WKWebView).
///
/// One server serves every embedded bundle — the editor
/// (`assets/editor/index.html`) and the docproc parser
/// (`assets/docproc/index.html`) are just different routes on it.
/// Extracted from editor_native_view.dart when docproc-web (P1) needed
/// the same server; never start a second port.
library;

import 'package:flutter_inappwebview/flutter_inappwebview.dart';

/// Lazily-started localhost server shared across every embedded webview.
/// Closing it would kill any live view, so we never close.
class WebviewLocalhostServer {
  WebviewLocalhostServer._();

  static InAppLocalhostServer? _server;
  static Future<void>? _starting;

  /// Pick a port that's unlikely to collide with our dev tooling
  /// (Vite is 5174, marketing 5173, server 8000, etc).
  static const int port = 9728;

  static Future<int> ensureStarted() async {
    if (_server?.isRunning() ?? false) return port;
    _starting ??= _start();
    await _starting;
    return port;
  }

  static Future<void> _start() async {
    final s = InAppLocalhostServer(port: port, shared: true);
    await s.start();
    _server = s;
  }
}

/// Native (macOS / iOS / Android) implementation of the Milkdown
/// editor host. Loads the bundle from `client/assets/editor/` via a
/// shared `InAppLocalhostServer` (file:// breaks ESM module loading on
/// WKWebView), then bridges to Flutter through:
///   * `addJavaScriptHandler('bridge', ...)` for Editor → Host
///   * `evaluateJavascript('window.__kcEditorReceive(...)')` for Host → Editor
library;

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_inappwebview/flutter_inappwebview.dart';

import 'editor_bridge_controller.dart';
import 'editor_bridge_protocol.dart';

/// Lazily-started localhost server shared across every editor instance.
/// Closing it would kill any other live editor view, so we never close.
class _LocalhostServer {
  _LocalhostServer._();

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

class EditorNativeView extends StatefulWidget {
  const EditorNativeView({
    super.key,
    required this.controller,
    required this.bundlePath,
  });

  final EditorBridgeController controller;

  /// Path under the Flutter assets root, e.g. `assets/editor/index.html`.
  final String bundlePath;

  /// Kick off the shared localhost server ahead of the first editor
  /// (e.g. from the notes home page), so the first webview doesn't pay
  /// the server startup cost. Fire-and-forget; the view itself also
  /// ensures the server in initState.
  static Future<void> warmup() => _LocalhostServer.ensureStarted();

  @override
  State<EditorNativeView> createState() => _EditorNativeViewState();
}

class _EditorNativeViewState extends State<EditorNativeView> {
  Future<Uri>? _bundleUri;
  InAppWebViewController? _webController;

  @override
  void initState() {
    super.initState();
    _bundleUri = _LocalhostServer.ensureStarted().then(
      (port) => Uri.parse('http://127.0.0.1:$port/${widget.bundlePath}'),
    );
  }

  @override
  void dispose() {
    widget.controller.detach();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return FutureBuilder<Uri>(
      future: _bundleUri,
      builder: (context, snap) {
        if (snap.connectionState != ConnectionState.done ||
            snap.data == null) {
          return const SizedBox.shrink();
        }
        return InAppWebView(
          initialUrlRequest: URLRequest(url: WebUri(snap.data.toString())),
          initialSettings: InAppWebViewSettings(
            transparentBackground: true,
            javaScriptEnabled: true,
            useShouldOverrideUrlLoading: true,
            // macOS WKWebView default is the ephemeral data store —
            // not strictly needed but explicit is friendlier.
            iframeAllow: 'clipboard-read; clipboard-write',
            iframeAllowFullscreen: false,
          ),
          onWebViewCreated: _onCreated,
          onLoadStop: (_, _) {
            // The bundle's `bridge.start()` sends `ready` itself; the
            // controller's onIncomingMessage takes care of everything
            // from there.
          },
          shouldOverrideUrlLoading: (_, action) async {
            // Keep all navigation inside the webview except external
            // links — those should bubble up via the bridge `navigate`
            // message, not by following an href.
            final url = action.request.url?.toString() ?? '';
            if (url.startsWith('http://127.0.0.1:${_LocalhostServer.port}')) {
              return NavigationActionPolicy.ALLOW;
            }
            return NavigationActionPolicy.CANCEL;
          },
        );
      },
    );
  }

  void _onCreated(InAppWebViewController c) {
    _webController = c;
    c.addJavaScriptHandler(
      handlerName: 'bridge',
      callback: (args) {
        if (args.isEmpty) return null;
        final raw = args.first;
        if (raw is! Map) return null;
        try {
          final msg = BridgeMessage.fromJson(
            Map<String, dynamic>.from(raw),
          );
          widget.controller.onIncomingMessage(msg);
        } on FormatException {
          // Drop malformed.
        }
        return null;
      },
    );
    widget.controller.attach(_send);
  }

  Future<void> _send(BridgeMessage message) async {
    final c = _webController;
    if (c == null) return;
    final json = jsonEncode(message.toJson());
    // The bundle exposes `window.__kcEditorReceive(data)` — see
    // editor-web/src/bridge/client.ts.
    await c.evaluateJavascript(
      source: 'window.__kcEditorReceive && window.__kcEditorReceive($json);',
    );
  }
}

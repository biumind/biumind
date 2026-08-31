/// Native (macOS / iOS / Android) docproc engine host. Loads the bundle
/// from `client/assets/docproc/` via the shared `WebviewLocalhostServer`
/// （与 editor 同一个 :9728 server，多一个 /assets/docproc/ route，不起
/// 第二个端口）into a *headless* InAppWebView — 无 UI 纯计算 bundle，
/// 不需要可见 widget。桥接方式与 editor 一致：
///   * `addJavaScriptHandler('bridge', ...)` for Bundle → Host
///   * `evaluateJavascript('window.__kcDocprocReceive(...)')` for Host → Bundle
library;

import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_inappwebview/flutter_inappwebview.dart';

import '../platform/localhost_server.dart';
import 'docproc_bridge_controller.dart';
import 'docproc_bridge_protocol.dart';
import 'docproc_bundle.dart';

class DocprocNativeEngineView extends StatefulWidget {
  const DocprocNativeEngineView({super.key, required this.controller});

  final DocprocBridgeController controller;

  /// Kick off the shared localhost server ahead of the first parse
  /// (e.g. when the import dialog opens), so the headless webview
  /// doesn't pay the server startup cost. Fire-and-forget.
  static Future<void> warmup() => WebviewLocalhostServer.ensureStarted();

  @override
  State<DocprocNativeEngineView> createState() =>
      _DocprocNativeEngineViewState();
}

class _DocprocNativeEngineViewState extends State<DocprocNativeEngineView> {
  HeadlessInAppWebView? _headless;
  InAppWebViewController? _webController;

  @override
  void initState() {
    super.initState();
    _start();
  }

  Future<void> _start() async {
    final port = await WebviewLocalhostServer.ensureStarted();
    // ?v= 防 WKWebView 缓存（同 editor_native_view 注释：bundle URL 恒定，
    // sync 追加新 hash 文件后旧入口一旦缓存会完整加载旧 bundle）。
    final url =
        'http://127.0.0.1:$port/$kDocprocNativeBundleAsset'
        '?v=${DateTime.now().millisecondsSinceEpoch}';
    final headless = HeadlessInAppWebView(
      initialUrlRequest: URLRequest(url: WebUri(url)),
      initialSettings: InAppWebViewSettings(javaScriptEnabled: true),
      onWebViewCreated: _onCreated,
    );
    _headless = headless;
    await headless.run();
  }

  @override
  void dispose() {
    widget.controller.detach();
    _headless?.dispose();
    _headless = null;
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    // 无头引擎：不渲染任何可见元素。
    return const SizedBox.shrink();
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
          final msg = DocprocMessage.fromJson(
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

  Future<void> _send(DocprocMessage message) async {
    final c = _webController;
    if (c == null) return;
    final json = jsonEncode(message.toJson());
    // The bundle exposes `window.__kcDocprocReceive(data)` — see
    // docproc-web/src/bridge/client.ts.
    await c.evaluateJavascript(
      source: 'window.__kcDocprocReceive && window.__kcDocprocReceive($json);',
    );
  }
}

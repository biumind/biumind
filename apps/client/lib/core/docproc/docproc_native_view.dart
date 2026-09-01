/// Native (macOS / iOS / Android) docproc engine host. Loads the bundle
/// from `client/assets/docproc/` via the shared `WebviewLocalhostServer`
/// （与 editor 同一个 :9728 server，多一个 /assets/docproc/ route，不起
/// 第二个端口）。桥接方式与 editor 一致：
///   * `addJavaScriptHandler('bridge', ...)` for Bundle → Host
///   * `evaluateJavascript('window.__kcDocprocReceive(...)')` for Host → Bundle
///
/// 宿主方式：树内 1×1 平台视图 InAppWebView（editor 同款），**禁用
/// HeadlessInAppWebView** —— 其 macOS 实现会把 WKWebView 塞进主窗口
/// contentView 且默认整窗大小，挤掉 Flutter 合成导致白屏
/// （2026-09-01 事故，详见设计文档 §2.5）。1×1 视图仍在窗口层级内
/// （避免离屏 WKWebView 的 JS 执行不可靠问题），视觉不可见。
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
  /// (e.g. when the import dialog opens), so the engine webview
  /// doesn't pay the server startup cost. Fire-and-forget.
  static Future<void> warmup() => WebviewLocalhostServer.ensureStarted();

  @override
  State<DocprocNativeEngineView> createState() =>
      _DocprocNativeEngineViewState();
}

class _DocprocNativeEngineViewState extends State<DocprocNativeEngineView> {
  Future<Uri>? _bundleUri;
  InAppWebViewController? _webController;

  @override
  void initState() {
    super.initState();
    // ?v= 防 WKWebView 缓存（同 editor_native_view 注释：bundle URL 恒定，
    // sync 追加新 hash 文件后旧入口一旦缓存会完整加载旧 bundle）。
    _bundleUri = WebviewLocalhostServer.ensureStarted().then(
      (port) => Uri.parse(
        'http://127.0.0.1:$port/$kDocprocNativeBundleAsset'
        '?v=${DateTime.now().millisecondsSinceEpoch}',
      ),
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
        // 无 UI 纯计算 bundle：1×1 + 透明 + 不响应指针。尺寸保持非零 —
        // 0×0 的 WKWebView 会被 WebKit 当离屏视图，JS 执行不可靠。
        return Opacity(
          opacity: 0,
          child: IgnorePointer(
            child: InAppWebView(
              initialUrlRequest: URLRequest(url: WebUri(snap.data.toString())),
              initialSettings: InAppWebViewSettings(
                transparentBackground: true,
                javaScriptEnabled: true,
                useShouldOverrideUrlLoading: true,
              ),
              onWebViewCreated: _onCreated,
              shouldOverrideUrlLoading: (_, action) async {
                final url = action.request.url?.toString() ?? '';
                if (url.startsWith(
                  'http://127.0.0.1:${WebviewLocalhostServer.port}',
                )) {
                  return NavigationActionPolicy.ALLOW;
                }
                return NavigationActionPolicy.CANCEL;
              },
            ),
          ),
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

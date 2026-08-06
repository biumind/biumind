/// Flutter Web implementation of the embedded Milkdown editor.
///
/// We embed the editor bundle as a same-origin iframe and bridge via
/// `window.postMessage`. `client/web/editor/` (populated by the
/// editor-web npm build) is served alongside the Flutter Web bundle.
library;

import 'dart:async';
import 'dart:js_interop';
import 'dart:ui_web' as ui_web;

import 'package:flutter/widgets.dart';
import 'package:web/web.dart' as web;

import 'editor_bridge_controller.dart';
import 'editor_bridge_protocol.dart';

Widget buildEditorWebView({
  required EditorBridgeController controller,
  required String bundleUrl,
}) {
  return _EditorWebView(controller: controller, bundleUrl: bundleUrl);
}

class _EditorWebView extends StatefulWidget {
  const _EditorWebView({required this.controller, required this.bundleUrl});

  final EditorBridgeController controller;
  final String bundleUrl;

  @override
  State<_EditorWebView> createState() => _EditorWebViewState();
}

class _EditorWebViewState extends State<_EditorWebView> {
  static int _seq = 0;
  late final String _viewType;
  late final web.HTMLIFrameElement _iframe;
  StreamSubscription<web.MessageEvent>? _messageSub;

  @override
  void initState() {
    super.initState();
    _seq += 1;
    _viewType = 'kc-editor-$_seq';

    _iframe = web.document.createElement('iframe') as web.HTMLIFrameElement
      ..src = widget.bundleUrl
      ..style.border = '0'
      ..style.width = '100%'
      ..style.height = '100%'
      ..setAttribute('allow', 'clipboard-read; clipboard-write')
      ..setAttribute('title', 'Knowcode editor');

    ui_web.platformViewRegistry.registerViewFactory(
      _viewType,
      (int _) => _iframe,
    );

    _messageSub = web.window.onMessage.listen(_onWindowMessage);

    widget.controller.attach(_send);
  }

  @override
  void dispose() {
    _messageSub?.cancel();
    widget.controller.detach();
    super.dispose();
  }

  Future<void> _send(BridgeMessage message) async {
    final target = _iframe.contentWindow;
    if (target == null) return;
    final json = message.toJson().jsify();
    target.postMessage(json, '*'.toJS);
  }

  void _onWindowMessage(web.MessageEvent ev) {
    // Only listen to messages from our own iframe.
    if (ev.source != _iframe.contentWindow) return;
    final raw = ev.data;
    final dart = raw.dartify();
    if (dart is! Map) return;
    final map = Map<String, dynamic>.from(dart);
    try {
      final msg = BridgeMessage.fromJson(map);
      widget.controller.onIncomingMessage(msg);
    } on FormatException {
      // Drop malformed; bridge contract violation.
    }
  }

  @override
  Widget build(BuildContext context) {
    return HtmlElementView(viewType: _viewType);
  }
}

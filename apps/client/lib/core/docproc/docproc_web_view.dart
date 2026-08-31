/// Flutter Web implementation of the docproc engine: a hidden same-origin
/// iframe (`client/web/docproc/index.html`, populated by the docproc-web
/// npm build) bridged via `window.postMessage`. 无 UI 纯计算 bundle，
/// iframe 渲染为 1x1 不可见。
library;

import 'dart:async';
import 'dart:js_interop';
import 'dart:ui_web' as ui_web;

import 'package:flutter/widgets.dart';
import 'package:web/web.dart' as web;

import 'docproc_bridge_controller.dart';
import 'docproc_bridge_protocol.dart';

Widget buildDocprocWebView({
  required DocprocBridgeController controller,
  required String bundleUrl,
}) {
  return _DocprocWebView(controller: controller, bundleUrl: bundleUrl);
}

class _DocprocWebView extends StatefulWidget {
  const _DocprocWebView({required this.controller, required this.bundleUrl});

  final DocprocBridgeController controller;
  final String bundleUrl;

  @override
  State<_DocprocWebView> createState() => _DocprocWebViewState();
}

class _DocprocWebViewState extends State<_DocprocWebView> {
  static int _seq = 0;
  late final String _viewType;
  late final web.HTMLIFrameElement _iframe;
  StreamSubscription<web.MessageEvent>? _messageSub;

  @override
  void initState() {
    super.initState();
    _seq += 1;
    _viewType = 'kc-docproc-$_seq';

    _iframe = web.document.createElement('iframe') as web.HTMLIFrameElement
      ..src = widget.bundleUrl
      ..style.border = '0'
      ..style.width = '1px'
      ..style.height = '1px'
      ..setAttribute('title', 'BiuMind docproc');

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

  Future<void> _send(DocprocMessage message) async {
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
      final msg = DocprocMessage.fromJson(map);
      widget.controller.onIncomingMessage(msg);
    } on FormatException {
      // Drop malformed; bridge contract violation.
    }
  }

  @override
  Widget build(BuildContext context) {
    return Opacity(
      opacity: 0,
      child: IgnorePointer(
        child: SizedBox(
          width: 1,
          height: 1,
          child: HtmlElementView(viewType: _viewType),
        ),
      ),
    );
  }
}

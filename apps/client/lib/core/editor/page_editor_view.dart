/// Cross-platform entry for the page body editor.
///
/// Web → embedded Milkdown bundle via iframe + postMessage.
/// Native → Phase 2 falls back to a plain markdown TextField; Phase 3
///          swaps in flutter_inappwebview pointing at the same bundle.
library;

import 'package:flutter/foundation.dart' show kIsWeb;
import 'package:flutter/widgets.dart';

import 'editor_bridge_controller.dart';
import 'editor_bridge_protocol.dart';
import 'editor_native_view.dart';
import 'editor_view_stub.dart'
    if (dart.library.html) 'editor_web_view.dart' as platform;

/// Path the Flutter Web shell uses — relative to the app's base href.
/// `client/web/editor/index.html` is populated by `npm run build` in
/// `client/editor-web/`.
const String kEditorWebBundleUrl = 'editor/index.html';

/// Path under the Flutter assets root used by the native localhost
/// server. `client/assets/editor/index.html` is the same bundle synced
/// from the editor-web build.
const String kEditorNativeBundleAsset = 'assets/editor/index.html';

class PageEditorView extends StatefulWidget {
  const PageEditorView({
    super.key,
    required this.initialMarkdown,
    required this.theme,
    required this.onMarkdownChanged,
    this.onWikilinkTap,
    this.resolveWikilinks,
    this.controllerRef,
    this.features = const BridgeFeatures(),
  });

  final String initialMarkdown;
  final BridgeTheme theme;
  final ValueChanged<String> onMarkdownChanged;
  final void Function(String slug)? onWikilinkTap;
  final WikilinkResolver? resolveWikilinks;

  /// Feature toggles sent in the init message (wikilink / mermaid).
  /// Defaults keep the historical behavior; hosts like the note editor
  /// pass `BridgeFeatures(wikilink: false)` to disable `[[wikilink]]`.
  final BridgeFeatures features;

  /// Optional callback that hands the controller back to the caller, so
  /// the host can call `setDoc` after a 409 conflict overwrites the
  /// local buffer.
  final void Function(EditorBridgeController controller)? controllerRef;

  @override
  State<PageEditorView> createState() => _PageEditorViewState();
}

class _PageEditorViewState extends State<PageEditorView> {
  late EditorBridgeController _controller;

  @override
  void initState() {
    super.initState();
    _controller = EditorBridgeController(
      initialMarkdown: widget.initialMarkdown,
      theme: widget.theme,
      features: widget.features,
    )
      ..onMarkdownChanged = widget.onMarkdownChanged
      ..onWikilinkTap = widget.onWikilinkTap
      ..resolveWikilinks = widget.resolveWikilinks;
    widget.controllerRef?.call(_controller);
  }

  @override
  void didUpdateWidget(covariant PageEditorView oldWidget) {
    super.didUpdateWidget(oldWidget);
    _controller
      ..onMarkdownChanged = widget.onMarkdownChanged
      ..onWikilinkTap = widget.onWikilinkTap
      ..resolveWikilinks = widget.resolveWikilinks;
    if (oldWidget.theme != widget.theme) {
      _controller.setOptions(newTheme: widget.theme);
    }
  }

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    if (kIsWeb) {
      return platform.buildEditorWebView(
        controller: _controller,
        bundleUrl: kEditorWebBundleUrl,
      );
    }
    return EditorNativeView(
      controller: _controller,
      bundlePath: kEditorNativeBundleAsset,
    );
  }
}

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
    this.resolvePresignGet,
    this.controllerRef,
    this.features = const BridgeFeatures(),
    this.locale,
  });

  final String initialMarkdown;
  final BridgeTheme theme;
  final ValueChanged<String> onMarkdownChanged;
  final void Function(String slug)? onWikilinkTap;
  final WikilinkResolver? resolveWikilinks;

  /// 编辑器渲染 `biu-file://<uuid>` 图片时向 host 换 presigned URL 的
  /// 回调（笔记附件；wiki 侧暂不接，biu-file 图片在 wiki 里保持裂开）。
  final PresignGetResolver? resolvePresignGet;

  /// Feature toggles sent in the init message (wikilink / mermaid).
  /// Defaults keep the historical behavior; hosts like the note editor
  /// pass `BridgeFeatures(wikilink: false)` to disable `[[wikilink]]`.
  final BridgeFeatures features;

  /// 编辑器 UI 语言（自绘右键菜单 / crepe 文案）。宿主页用
  /// [resolveEditorLocale] 从 localeOverride + 系统 locale 解析传入；
  /// null = 保持 controller 默认 'zh-Hans'。didUpdateWidget 时语言变化
  /// 经 setLocale 推送（菜单现构建即刻生效，crepe 文案维持 init）。
  final String? locale;

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
      locale: widget.locale ?? 'zh-Hans',
    )
      ..onMarkdownChanged = widget.onMarkdownChanged
      ..onWikilinkTap = widget.onWikilinkTap
      ..resolveWikilinks = widget.resolveWikilinks
      ..resolvePresignGet = widget.resolvePresignGet;
    widget.controllerRef?.call(_controller);
  }

  @override
  void didUpdateWidget(covariant PageEditorView oldWidget) {
    super.didUpdateWidget(oldWidget);
    _controller
      ..onMarkdownChanged = widget.onMarkdownChanged
      ..onWikilinkTap = widget.onWikilinkTap
      ..resolveWikilinks = widget.resolveWikilinks
      ..resolvePresignGet = widget.resolvePresignGet;
    if (oldWidget.theme != widget.theme) {
      _controller.setOptions(newTheme: widget.theme);
    }
    final locale = widget.locale;
    if (locale != null && oldWidget.locale != locale) {
      _controller.setLocale(locale);
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

/// Default (non-web) implementation of [buildEditorWebView]. Conditional
/// import in [page_editor_view.dart] swaps this for [editor_web_view.dart]
/// when `dart.library.html` is available.
library;

import 'package:flutter/widgets.dart';

import 'editor_bridge_controller.dart';

Widget buildEditorWebView({
  required EditorBridgeController controller,
  required String bundleUrl,
}) {
  // Should never be called outside Flutter Web (page_editor_view picks
  // the native widget when kIsWeb is false).
  throw UnsupportedError(
    'buildEditorWebView is only available on Flutter Web. '
    'Use buildEditorNativeView on native platforms.',
  );
}

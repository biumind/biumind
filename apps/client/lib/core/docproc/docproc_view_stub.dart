/// Default (non-web) implementation of [buildDocprocWebView]. Conditional
/// import in [docproc_view.dart] swaps this for [docproc_web_view.dart]
/// when `dart.library.html` is available.
library;

import 'package:flutter/widgets.dart';

import 'docproc_bridge_controller.dart';

Widget buildDocprocWebView({
  required DocprocBridgeController controller,
  required String bundleUrl,
}) {
  // Should never be called outside Flutter Web (docproc_view.dart picks
  // the native engine when kIsWeb is false).
  throw UnsupportedError(
    'buildDocprocWebView is only available on Flutter Web. '
    'Use DocprocNativeEngineView on native platforms.',
  );
}

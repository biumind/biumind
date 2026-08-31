/// Cross-platform entry for the docproc (document parsing) engine.
///
/// Web → hidden same-origin iframe + postMessage.
/// Native → headless InAppWebView fed by the shared WebviewLocalhostServer.
///
/// 无 UI 纯计算 bundle：宿主（import_dialog 等）把这个 widget 挂在树里
/// 任意不可见位置即可，解析经 [DocprocBridgeController.parse] 驱动。
library;

import 'package:flutter/foundation.dart' show kIsWeb;
import 'package:flutter/widgets.dart';

import '../platform/platform_caps.dart';
import 'docproc_bridge_controller.dart';
import 'docproc_bundle.dart';
import 'docproc_native_view.dart';
import 'docproc_view_stub.dart'
    if (dart.library.html) 'docproc_web_view.dart' as platform;

class DocprocEngineView extends StatelessWidget {
  const DocprocEngineView({super.key, required this.controller});

  final DocprocBridgeController controller;

  @override
  Widget build(BuildContext context) {
    if (kIsWeb) {
      return platform.buildDocprocWebView(
        controller: controller,
        bundleUrl: kDocprocWebBundleUrl,
      );
    }
    // Windows / Linux 无 WebView（hasLocalDocproc=false）：controller 的
    // 平台守卫会先拦下 parse 调用，这里渲染空避免引擎启动即崩。
    if (!PlatformCaps.detect().hasLocalDocproc) {
      return const SizedBox.shrink();
    }
    return DocprocNativeEngineView(controller: controller);
  }
}

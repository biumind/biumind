/// docproc-web bundle 的两处装载路径（构建产物由
/// `apps/client/docproc-web` 的 `npm run build` 双写）。
library;

/// Path the Flutter Web shell uses — relative to the app's base href.
/// `client/web/docproc/index.html` is populated by `npm run build` in
/// `client/docproc-web/`.
const String kDocprocWebBundleUrl = 'docproc/index.html';

/// Path under the Flutter assets root used by the native localhost
/// server. `client/assets/docproc/index.html` is the same bundle synced
/// from the docproc-web build.
const String kDocprocNativeBundleAsset = 'assets/docproc/index.html';

/// 平台门面 — 编码工作台模块的注册入口。
///
/// 编码模块（`lib/features/code/`）是桌面专属:它通过 `code_bridge_provider`
/// 依赖 `biu_daemon_manager`(本地 daemon / `Process`)、git worktree、PTY
/// 终端等,全部需要 `dart:io`,在 Web 上既跑不起来也用不上。
///
/// 条件 import 在 native(真实实现 `code_module_io.dart`)与 Web(空桩
/// `code_module_web.dart`)之间二选一。router / settings 只 import 本门面,
/// 永不直接 import code 大树 → Web 编译时整棵 code UI + daemon 树不可达 →
/// 被 Dart tree-shaker 剔除出 web bundle(省约 15k LOC + dart:io shim)。
///
/// 约定沿用仓库既有的 `_io` / `_web` + `dart.library.html` 模式:
/// `lib/data/local/db.dart:29`、`lib/core/editor/page_editor_view.dart:15`。
library;

export 'code_module_io.dart'
    if (dart.library.html) 'code_module_web.dart';

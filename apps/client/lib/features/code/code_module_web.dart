/// 编码模块注册 — Web 空桩。
///
/// 被 [code_module.dart] 条件 export(`dart.library.html` 命中时)。本文件
/// **不** import 任何 `features/code` UI / daemon 文件,使整棵 code 树在 Web
/// 编译时不可达,被 tree-shaker 剔除。
///
/// `codeModuleEnabled = false` → router 不注册 `/code` 路由、不显示侧栏入口,
/// settings 不渲染「编码工作台」磁贴。深链 `/code` 由 router redirect 兜底
/// 回 `/chat`。
library;

import 'package:flutter/widgets.dart';

const bool codeModuleEnabled = false;

// 与 code_module_io.dart 同形(DTO 重复定义,沿用 db_open_io/web 先例)。
class CodeRouteSpec {
  const CodeRouteSpec({required this.path, required this.child});
  final String path;
  final Widget child;
}

const List<CodeRouteSpec> codeRouteSpecs = [];

/// 不可达:settings 在 `!codeModuleEnabled` 时不渲染该磁贴。返回占位避免
/// 任何 code 树符号被拖进 web bundle。
Widget buildCodingWorkbenchPane() => const SizedBox.shrink();

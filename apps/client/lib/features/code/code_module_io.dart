/// 编码模块注册 — native(桌面/移动)真实实现。
///
/// 被 [code_module.dart] 条件 export。Web 端对应 `code_module_web.dart`。
/// 本文件引用 `presentation/project_page.dart` 与编码工作台 Settings pane,
/// 因此只有 native build 会触达这些文件;Web build 解析的是空桩,code 大树
/// 整体不可达 → tree-shaking 剔除。
library;

import 'package:flutter/widgets.dart';

import '../settings/presentation/coding_workbench_pane.dart';
import 'presentation/project_page.dart';

/// 编码模块是否可用。native = true;Web = false(空桩)。
const bool codeModuleEnabled = true;

/// 顶层路由规格 — 由 router 转成 [GoRoute] 并套用其 `tabPage` 过渡。
/// 用 DTO 而非直接返回 [GoRoute],避免把 go_router / 过渡细节泄露进 code 树,
/// 也让 Web 空桩能给空列表。
class CodeRouteSpec {
  const CodeRouteSpec({required this.path, required this.child});
  final String path;
  final Widget child;
}

/// 编码工作台的顶层路由(native)。Web 空桩返回 `const []`。
const List<CodeRouteSpec> codeRouteSpecs = [
  CodeRouteSpec(path: '/code', child: CodeProjectPage()),
];

/// 构造编码工作台 Settings 面板。Web 空桩返回占位 widget(不会触达 pane)。
Widget buildCodingWorkbenchPane() => const CodingWorkbenchPane();

// 共享: showMenu 的 RelativeRect 计算 helper.
//
// 为什么需要这个:
//   `showMenu` 的 `position` 参数是 RelativeRect, 它的 inset 是相对
//   **Overlay 的局部坐标系** 的, 不是屏幕绝对坐标。在 GoRouter ShellRoute
//   嵌套 Navigator 的场景下, Overlay 不在屏幕原点 — 直接把
//   `TapDownDetails.globalPosition` 当 inset 用, 菜单会整体平移
//   一个 sidebar 宽度。biumind 的 _AppShell + 各 ShellRoute 都踩过这个雷。
//
// 使用:
//   - 右键 / 长按 (锚鼠标点): popupPositionAt(ctx, details.globalPosition)
//   - 左键 ... 按钮 (锚按钮 RenderBox, lobehub 风, 菜单从按钮下方弹):
//     popupPositionForBox(buttonContext)  // 用 Builder 拿到指向按钮自身的 ctx
//
// 实现参考: Flutter 内置 PopupMenuButton._showButtonMenu。

import 'package:flutter/material.dart';

/// 锚定到屏幕坐标点 (右键 / 长按场景)。
///
/// `globalPos` 是屏幕绝对坐标 (`TapDownDetails.globalPosition` 等)。
/// helper 内部用 `overlay.globalToLocal(globalPos)` 转到 overlay 局部坐标,
/// 再生成零尺寸的 RelativeRect — 菜单从该点的右下方展开。
RelativeRect popupPositionAt(BuildContext context, Offset globalPos) {
  final overlay =
      Overlay.of(context).context.findRenderObject() as RenderBox;
  final localPos = overlay.globalToLocal(globalPos);
  return RelativeRect.fromRect(
    Rect.fromPoints(localPos, localPos),
    Offset.zero & overlay.size,
  );
}

/// 锚定到按钮 RenderBox 的下沿 (lobehub 风 — 菜单贴按钮往下弹)。
///
/// `anchorContext` 必须是按钮 widget 自己的 BuildContext —— 通常用
/// `Builder(builder: (iconContext) => InkWell(onTap: () => ...))` 拿到。
/// 直接传外层 build 的 context 会拿到整个 tile 的 RenderBox, 锚点跑偏。
RelativeRect popupPositionForBox(BuildContext anchorContext) {
  final overlay =
      Overlay.of(anchorContext).context.findRenderObject() as RenderBox;
  final box = anchorContext.findRenderObject() as RenderBox;
  final topLeft = box.localToGlobal(
    box.size.bottomLeft(Offset.zero),
    ancestor: overlay,
  );
  final bottomRight = box.localToGlobal(
    box.size.bottomRight(Offset.zero),
    ancestor: overlay,
  );
  return RelativeRect.fromRect(
    Rect.fromPoints(topLeft, bottomRight),
    Offset.zero & overlay.size,
  );
}

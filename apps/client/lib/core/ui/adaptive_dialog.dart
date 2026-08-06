// AdaptiveDialog —— 弹层形态收口（移动端适配 P1, 方案 docs/BiuMind-Mobile-Adaptation-Plan.md §4.5）。
//
// 全 app 唯一的「宽屏 dialog / 手机 bottom sheet」分支点。业务弹窗只调
// showAdaptiveDialog + AdaptiveDialogFrame，不得再自行 isPhoneLayout 分支。
//
// 桌面 / Web: 与原先 showDialog + Dialog + ConstrainedBox 完全一致（尺寸、
//   insetPadding、barrierColor 均不变，零回归）。
// 手机: showModalBottomSheet(isScrollControlled + showDragHandle + useSafeArea
//   + 键盘顶起 padding)，写法与 thread_settings_sheet.dart 一致；内容高度上限
//   屏高 85%。

import 'package:flutter/material.dart';

import '../layout/form_factor.dart';

/// 宽屏退化为 showDialog(barrierColor: black54)，手机为 modal bottom sheet。
///
/// [barrierDismissible] 宽屏透传 showDialog；手机映射 sheet 的
/// isDismissible + enableDrag —— false 时「必须点按钮才能关」的语义在
/// 两种形态下一致（手机同样不可滑关 / 点遮罩关）。
/// [transparentBackground] 用于分享卡片这类自带视觉的弹层（桌面端
/// Dialog(backgroundColor: transparent) 的等价物）：手机端 sheet 背景透明。
/// [showDragHandle] 仅在手机端生效（桌面 dialog 无把手概念）。
Future<T?> showAdaptiveDialog<T>({
  required BuildContext context,
  required WidgetBuilder builder,
  bool barrierDismissible = true,
  bool transparentBackground = false,
  bool showDragHandle = true,
}) {
  if (isPhoneLayout(context)) {
    return showModalBottomSheet<T>(
      context: context,
      isScrollControlled: true,
      useSafeArea: true,
      isDismissible: barrierDismissible,
      enableDrag: barrierDismissible,
      showDragHandle: showDragHandle,
      backgroundColor: transparentBackground ? Colors.transparent : null,
      builder: (ctx) => Padding(
        padding: EdgeInsets.only(bottom: MediaQuery.viewInsetsOf(ctx).bottom),
        child: builder(ctx),
      ),
    );
  }
  return showDialog<T>(
    context: context,
    barrierDismissible: barrierDismissible,
    barrierColor: Colors.black54,
    builder: builder,
  );
}

/// 弹窗内容框架。宽屏 = Dialog(backgroundColor: surface) + insetPadding +
/// ConstrainedBox(maxWidth/maxHeight)；手机 = 全宽内容，高度上限屏高 85%。
///
/// child 约定为 Column(mainAxisSize: MainAxisSize.min, [header, Expanded(列表)]),
/// 与现有各 dialog 的结构一致；两种形态下布局行为相同。
class AdaptiveDialogFrame extends StatelessWidget {
  const AdaptiveDialogFrame({
    super.key,
    required this.child,
    this.maxWidth = 720,
    this.maxHeight = 600,
    this.insetPadding = const EdgeInsets.symmetric(
      horizontal: 64,
      vertical: 80,
    ),
    this.shape,
    this.backgroundColor,
    this.phonePadding = EdgeInsets.zero,
  });

  final Widget child;
  final double maxWidth;
  final double maxHeight;

  /// 桌面 Dialog 的 insetPadding；默认 64/80（多数 dialog 现状）。
  /// 注意 Flutter Dialog 默认是 40/24 —— 原 dialog 未显式指定 insetPadding 时,
  /// 迁移必须显式传 40/24 保持桌面像素级不变。
  final EdgeInsets insetPadding;

  /// 桌面 Dialog 的 shape；null = 默认（多数 dialog 现状未指定）。
  final ShapeBorder? shape;

  /// 桌面 Dialog 背景色；null = colorScheme.surface（多数 dialog 现状）。
  /// 分享卡片类自带视觉的弹层传 Colors.transparent（手机端需同时在
  /// showAdaptiveDialog 传 transparentBackground: true）。
  final Color? backgroundColor;

  /// 手机端内容外侧 padding（桌面 insetPadding 不适用手机, 不生效）。
  final EdgeInsets phonePadding;

  @override
  Widget build(BuildContext context) {
    if (isPhoneLayout(context)) {
      final screenH = MediaQuery.sizeOf(context).height;
      return Padding(
        padding: phonePadding,
        child: ConstrainedBox(
          constraints: BoxConstraints(maxHeight: screenH * 0.85),
          child: child,
        ),
      );
    }
    return Dialog(
      backgroundColor: backgroundColor ?? Theme.of(context).colorScheme.surface,
      insetPadding: insetPadding,
      shape: shape,
      child: ConstrainedBox(
        constraints: BoxConstraints(maxWidth: maxWidth, maxHeight: maxHeight),
        child: child,
      ),
    );
  }
}

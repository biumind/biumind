// BiuScrollbar — 显式滚动条的唯一收口组件 (C12)。
//
// 全 app 滚动条策略见 biu_scroll_behavior.dart 顶部规约 (macOS overlay 风:
// 静止隐藏 / 滚动或悬停出现 / 无轨道 / 4px 细滑块)。分工:
//   * 桌面 (macOS/windows/linux): 全局 BiuScrollBehavior 已给每个
//     Scrollable 挂了同款 Scrollbar — 这里直接返回 child, 避免双挂。
//   * 移动 (iOS/android): behavior 不挂, 这里补上同款参数的 Scrollbar,
//     滚动时给出细滑块指示。
//
// 业务代码需要显式滚动条时一律用这个, 禁止再写裸 Scrollbar/
// CupertinoScrollbar/RawScrollbar (check_invariants.sh C12 静态拦截)。
// 视觉 (粗细/颜色/轨道) 永远走 ThemeData.scrollbarTheme, 不在局部调。

import 'package:flutter/material.dart';

class BiuScrollbar extends StatelessWidget {
  const BiuScrollbar({super.key, this.controller, required this.child});

  final ScrollController? controller;
  final Widget child;

  @override
  Widget build(BuildContext context) {
    switch (Theme.of(context).platform) {
      case TargetPlatform.macOS:
      case TargetPlatform.windows:
      case TargetPlatform.linux:
        return child;
      case TargetPlatform.iOS:
      case TargetPlatform.android:
      case TargetPlatform.fuchsia:
        return Scrollbar(
          controller: controller,
          thumbVisibility: false, // 不常显 — 滚动时出现
          trackVisibility: false, // 无轨道 — overlay 风
          interactive: true,
          child: child,
        );
    }
  }
}

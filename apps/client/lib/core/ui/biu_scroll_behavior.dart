// BiuScrollBehavior — 全 app 滚动行为的唯一挂载点
// (main.dart MaterialApp.router scrollBehavior)。
//
// 设计规约 (对标 macOS 原生 overlay 滚动条 / Linear / Notion):
//   * 滚动条是覆盖式反馈元素, 不是布局元素 — 静止隐藏, 滚动/悬停时出现,
//     永不在「内容刚好放得下」的页面出现。
//   * 桌面 (macOS/windows/linux): 给每个 Scrollable 挂 Scrollbar,
//     thumbVisibility/trackVisibility 双 false = 不常显、无轨道;
//     interactive 保留长列表拖拽滑块能力。视觉 (4px 细滑块/颜色) 全部走
//     ThemeData.scrollbarTheme (app/theme/theme_builder.dart)。
//   * 移动 (iOS/android/fuchsia): 不包 Scrollbar, 沿用平台原生行为。
//
// 「滚动条可见 ⟺ 内容真的比视口长」是不变量 — 固定表单页 (登录/设置) 应当
// 在最小窗口尺寸下零溢出 (见 login_page.dart 的 LayoutBuilder 布局),
// 而不是靠滚动兜底。

import 'package:flutter/material.dart';

class BiuScrollBehavior extends MaterialScrollBehavior {
  const BiuScrollBehavior();

  @override
  Widget buildScrollbar(
    BuildContext context,
    Widget child,
    ScrollableDetails details,
  ) {
    switch (getPlatform(context)) {
      case TargetPlatform.macOS:
      case TargetPlatform.windows:
      case TargetPlatform.linux:
        return Scrollbar(
          controller: details.controller,
          thumbVisibility: false, // 不常显 — 滚动/悬停时自动出现
          trackVisibility: false, // 无轨道 — overlay 风
          interactive: true, // 悬停显形后可拖拽 (长列表)
          child: child,
        );
      case TargetPlatform.iOS:
      case TargetPlatform.android:
      case TargetPlatform.fuchsia:
        return child;
    }
  }
}

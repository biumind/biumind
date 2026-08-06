// 统一形态因子 / 断点判定 — 手机适配 P0 基础设施。
// 方案: docs/BiuMind-Mobile-Adaptation-Plan.md §4.1
//
// 全 app 唯一的布局断点: 屏宽 < 600 → phone 形态 (Drawer / 列表↔详情两级 /
// 长按替代 hover), 否则 desktop 形态 (现状不变)。历史上各页自定义阈值
// (600/700/720/768/1280) 各自为政, 新代码一律用这里的判定。

import 'package:flutter/material.dart';

/// 布局形态。平板 / 折叠屏中间态 (600–1024) 暂归 desktop, 后续再细分。
enum AppFormFactor { phone, desktop }

/// 统一断点: 屏宽 < 600 logical px 视为手机。
const double kPhoneBreakpoint = 600;

AppFormFactor formFactorOf(BuildContext context) =>
    MediaQuery.sizeOf(context).width < kPhoneBreakpoint
        ? AppFormFactor.phone
        : AppFormFactor.desktop;

/// 手机形态 = Drawer 导航 / 列表↔详情两级 / 长按替代 hover / 大触摸目标。
bool isPhoneLayout(BuildContext context) =>
    formFactorOf(context) == AppFormFactor.phone;

/// 当前 platform 是否有 hover 概念 (桌面 + Web 有, iOS/Android 触屏无)。
/// 与 BiuHoverable 内部判定同源; hover-only UI (hover 才显示的按钮等)
/// 用它降级为常驻 / 长按入口。
bool platformHasHover(BuildContext context) {
  switch (Theme.of(context).platform) {
    case TargetPlatform.iOS:
    case TargetPlatform.android:
      return false;
    case TargetPlatform.macOS:
    case TargetPlatform.linux:
    case TargetPlatform.windows:
    case TargetPlatform.fuchsia:
      return true;
  }
}

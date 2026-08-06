// BiuBrand — 永久品牌色常量,跨色板/跨用户**恒定**。
//
// 跟 BiuColors 的关键区别:
//   * BiuColors.brand   = 用户当前选的色板的主色 (跟着切)
//   * BiuBrand.primary  = BiuMind 永久品牌色 #7C3AED (永远是它)
//
// 仅用于"BiuMind 不是你的 BiuMind"的场景:
//   * 分享卡片 (用户 A 分享出去给用户 B 看,色调要一致 → 跨用户品牌识别)
//   * 营销素材 (登录页 logo / App icon / favicon / 公开 web 页面)
//   * 系统通知 / push (跨设备一致)
//
// 业务 UI(Chat / Hero / Banner / Settings 等)用 `BiuColors.brand` —
// 跟用户色板走才有"我的 BiuMind"感受。
//
// **不要**新增颜色到这里,除非真是"永久品牌资产"级别的决策。

import 'package:flutter/material.dart';

class BiuBrand {
  BiuBrand._();

  // ── 永久品牌色 (默认色板 purple-orange 的 light 值) ──────────
  static const Color primary = Color(0xFF7C3AED);
  static const Color accent  = Color(0xFFFB923C);

  // ── Logo 渐变 (用于 export/marketing,业务 logo 用 c.brandGradient) ──
  static const List<Color> logoGradient = [primary, accent];

  // ── 分享卡片用色 (固定,不跟主题切) ───────────────────────────
  // 这些是"在导出 PNG 上看"的色,要跟系统暗色模式无关。
  static const Color shareSurface     = Color(0xFFFFFFFF);
  static const Color shareTextStrong  = Color(0xFF1A1A1A);
  static const Color shareTextMuted   = Color(0xFF6B6B70);
  static const Color shareTextHint    = Color(0xFF9B9BA0);
  static const Color shareSurfaceMid  = Color(0xFFF2F2F5);
  static const Color shareDivider     = Color(0xFFEDEDF0);
  static const Color shareShadow      = Color(0x18000000);
}

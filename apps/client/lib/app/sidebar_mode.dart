// SidebarMode —— 桌面侧边栏显示模式（用户可切换，持久化）。
//
// 三态（参考 WorkBuddy / VSCode）：
//   - expanded  : 图标 + 文字（232px，默认）
//   - iconsOnly : 只显示图标（48px 图标栏）
//   - hidden    : 完全收起（0px，内容全宽）
//
// 切换入口：桌面顶栏的 sidebar toggle（单击三态循环 hidden → iconsOnly
// → expanded → hidden；右键/长按 弹三态菜单直选）+ Cmd/Ctrl+B（同循环）。
//
// 持久化：SharedPreferences 单一 JSON 在 key `biu.app.sidebar`，
// 跟 chat_preferences 同模式。
//
// 注意：/code、/wiki、/creation 路由的"强制 iconsOnly"(_AppShell
// ._shouldCompact) 是布局级的临时收窄，不写进这里、不覆盖用户选择。

import 'dart:convert';

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:shared_preferences/shared_preferences.dart';

const _kKey = 'biu.app.sidebar';

enum SidebarMode { expanded, iconsOnly, hidden }

class SidebarModeNotifier extends StateNotifier<SidebarMode> {
  SidebarModeNotifier() : super(SidebarMode.expanded) {
    _load();
  }

  Future<void> _load() async {
    try {
      final prefs = await SharedPreferences.getInstance();
      final raw = prefs.getString(_kKey);
      if (raw == null) return;
      final j = jsonDecode(raw);
      if (j is! Map<String, dynamic>) return;
      final mode = SidebarMode.values.asNameMap()[j['mode'] as String?];
      if (mode != null) state = mode;
    } catch (_) {/* fail silent —— 起默认 */}
  }

  Future<void> _persist() async {
    try {
      final prefs = await SharedPreferences.getInstance();
      await prefs.setString(_kKey, jsonEncode({'mode': state.name}));
    } catch (_) {}
  }

  /// 单击 toggle / Cmd·Ctrl+B: 三态循环
  /// hidden → iconsOnly → expanded → hidden。
  Future<void> toggle() => setMode(switch (state) {
        SidebarMode.hidden => SidebarMode.iconsOnly,
        SidebarMode.iconsOnly => SidebarMode.expanded,
        SidebarMode.expanded => SidebarMode.hidden,
      });

  Future<void> setMode(SidebarMode mode) async {
    state = mode;
    await _persist();
  }
}

final sidebarModeProvider =
    StateNotifierProvider<SidebarModeNotifier, SidebarMode>(
  (ref) => SidebarModeNotifier(),
);

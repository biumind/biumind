// 移动端底部主导航 (R1.1)。
//
// 6 个高频 tab: 对话 / 知识库 / 笔记 / 创作 / 应用 / 我的。点击 ctx.go (复用
// _AppShell 现有导航模型), 选中态按当前路由前缀判定。
//
// 设计: docs/BiuMind-Mobile-Redesign-Design.md §4.1
// 范围 (R1.1 骨架): 5 tab 指向现有页面; 各 tab 内容优化分属 R1.2 (对话
// 首页化) / R1.3 (我的收口)。低频入口 (搜索 / 技能 / 会员 / 设备 /
// pinned app / 侧边栏定制) 仍走顶部 ☰ Drawer, R1.6 收口语义。
//
// 平台分流: 仅手机形态渲染 (_AppShell phone 分支挂载); 桌面 / Web 零回归。
// 毛玻璃视觉与触摸目标打磨分属 R3 (§6.2)。
//
// l10n: tab 5 走 t.navProfile (l10n 现代化完成: of() nullable + arb 补全)。

import 'package:flutter/material.dart';
import 'package:go_router/go_router.dart';

import '../../app/theme.dart';
import '../../l10n/app_localizations.dart';

/// 底部 tab 目的地定义。
class _PhoneTabDest {
  final IconData icon;
  final IconData selectedIcon;
  /// systemId, 用于 build 内的 labelFor 查文案。
  final String key;
  /// 点击此 tab 导航到的顶层路由。
  final String path;
  /// 路由前缀: 当前路径命中任一 (精确或前缀) → 此 tab 高亮。
  final List<String> matches;
  const _PhoneTabDest(this.icon, this.selectedIcon, this.key, this.path, this.matches);
}

/// 6 个底部 tab (顺序 = 视觉从左到右)。
///
/// "我的" (末位 tab) 伞下覆盖 settings / membership / skills 三类账户性低频
/// 入口 —— 它们在 R1.3 才统一收口为「我的」页, R1.1 先保证三处路由点亮
/// 同一 tab, 切换时不闪。
const List<_PhoneTabDest> _kPhoneTabs = [
  _PhoneTabDest(Icons.chat_bubble_outline_rounded, Icons.chat_bubble_rounded,
      'chat', '/chat', ['/chat']),
  _PhoneTabDest(Icons.menu_book_outlined, Icons.menu_book_rounded,
      'wiki', '/wiki', ['/wiki']),
  _PhoneTabDest(Icons.note_outlined, Icons.note_rounded,
      'notes', '/notes', ['/notes']),
  _PhoneTabDest(Icons.auto_awesome_outlined, Icons.auto_awesome_rounded,
      'creation', '/creation', ['/creation']),
  _PhoneTabDest(Icons.apps_outlined, Icons.apps_rounded,
      'apps', '/apps', ['/apps']),
  _PhoneTabDest(Icons.person_outline_rounded, Icons.person_rounded,
      'profile', '/profile', ['/profile', '/settings', '/membership', '/skills']),
];

/// 当前路由落在哪个 tab; 都不匹配 (如 /search, 或深链进 app host 不属任一
/// tab) 返回 0 (对话)。NavigationBar 的 selectedIndex 必须有效, 故用对话
/// 兜底 —— /search 是横切能力 (R1.5 做顶部入口), 兜底高亮不影响功能。
int phoneTabIndexFor(String path) {
  for (var i = 0; i < _kPhoneTabs.length; i++) {
    for (final prefix in _kPhoneTabs[i].matches) {
      if (path == prefix || path.startsWith('$prefix/')) return i;
    }
  }
  return 0;
}

/// 手机形态底部主导航。挂载点: `_AppShell` phone 分支 body Column 末尾。
///
/// 用 Material 3 [NavigationBar] (a11y / ripple / label 行为自带), 选中态
/// 经局部 [NavigationBarTheme] 套品牌色 (跟桌面 `_NavRow` selected =
/// brand / brandSoft 视觉一致), 不污染全局主题。
class PhoneTabBar extends StatelessWidget {
  const PhoneTabBar({super.key});

  @override
  Widget build(BuildContext context) {
    final t = AppLocalizations.of(context)!;
    final c = Theme.of(context).extension<BiuColors>()!;
    final loc = GoRouterState.of(context).uri.path;
    final selectedIndex = phoneTabIndexFor(loc);

    String labelFor(String key) => switch (key) {
          'chat' => t.navChat,
          'wiki' => t.navWiki,
          'notes' => '笔记',
          'creation' => t.navCreation,
          'apps' => t.appsTitle,
          'profile' => t.navProfile,
          _ => key,
        };

    return SafeArea(
      // top:false — 主壳外层 SafeArea(top:phone) 已吃顶部 inset; 这里只
      // 让出底部 home indicator 区。放 Column 末尾 (非 Scaffold 的
      // bottomNavigationBar 槽), 必须自己处理 bottom inset。
      top: false,
      child: Theme(
        data: Theme.of(context).copyWith(
          navigationBarTheme: NavigationBarThemeData(
            backgroundColor: c.surface0,
            surfaceTintColor: Colors.transparent,
            indicatorColor: c.brandSoft,
            height: 60,
            iconTheme: WidgetStateProperty.resolveWith((states) {
              final selected = states.contains(WidgetState.selected);
              return IconThemeData(
                  color: selected ? c.brand : c.text3, size: 22);
            }),
            labelTextStyle: WidgetStateProperty.resolveWith((states) {
              final selected = states.contains(WidgetState.selected);
              return TextStyle(
                color: selected ? c.brand : c.text3,
                fontSize: 11,
                fontWeight: selected ? FontWeight.w600 : FontWeight.w500,
              );
            }),
          ),
        ),
        child: NavigationBar(
          selectedIndex: selectedIndex,
          onDestinationSelected: (i) => context.go(_kPhoneTabs[i].path),
          labelBehavior: NavigationDestinationLabelBehavior.alwaysShow,
          destinations: [
            for (final dest in _kPhoneTabs)
              NavigationDestination(
                icon: Icon(dest.icon),
                selectedIcon: Icon(dest.selectedIcon),
                label: labelFor(dest.key),
              ),
          ],
        ),
      ),
    );
  }
}

// Wiki 模块顶层壳 —— 在 biumind 顶层 _AppShell 内部再嵌套一层。
//
// 布局（桌面）：
//   ┌──────────────┬──────────────────────────────┐
//   │ WikiNavRail  │  ShellRoute child（路由切换）  │
//   │ 220px        │                              │
//   ├──────────────┴──────────────────────────────┤
//   │ WikiStatusBar (32px)                         │
//   └─────────────────────────────────────────────┘
//
// 手机形态 (<600px)：220px rail 收进 WikiShell 自己的 Drawer，页头加 ☰，
// 内容全宽，状态栏保留（方案 docs/BiuMind-Mobile-Adaptation-Plan.md §4.6）。
//
// projectId 由当前 GoRouter location 解析：/wiki/p/:pid/* 时非空，
// /wiki 时为空（NavRail 切换为工作区模式）。
//
// ⌘K / Ctrl+K 全局监听打开 WikiCommandPalette（命令模式）；
// ⌘P / Ctrl+P 打开面板的页面跳转模式（按页面名跳页）。

import 'dart:math' as math;

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:go_router/go_router.dart';

import '../../../app/theme.dart';
import '../../../core/layout/form_factor.dart';
import 'wiki_command_palette.dart';
import 'wiki_nav_rail.dart';
import 'wiki_status_bar.dart';

class WikiShell extends StatelessWidget {
  const WikiShell({super.key, required this.child});
  final Widget child;

  /// 从 /wiki/p/:pid/* 解析出 projectId；其余情形返回空串。
  static String _projectIdFromLocation(String loc) {
    final segs = Uri.parse(loc).pathSegments;
    if (segs.length >= 3 && segs[0] == 'wiki' && segs[1] == 'p') {
      return segs[2];
    }
    return '';
  }

  @override
  Widget build(BuildContext context) {
    final loc = GoRouterState.of(context).uri.path;
    final projectId = _projectIdFromLocation(loc);

    // 手机形态：rail 抽屉化，走独立子壳；桌面路径保持现状。
    if (isPhoneLayout(context)) {
      return _PhoneWikiShell(projectId: projectId, child: child);
    }

    return Scaffold(
      backgroundColor: BiuTokens.bg,
      body: CallbackShortcuts(
        bindings: <ShortcutActivator, VoidCallback>{
          const SingleActivator(LogicalKeyboardKey.keyK, meta: true): () =>
              WikiCommandPalette.show(context, projectId: projectId),
          const SingleActivator(LogicalKeyboardKey.keyK, control: true): () =>
              WikiCommandPalette.show(context, projectId: projectId),
          const SingleActivator(LogicalKeyboardKey.keyP, meta: true): () =>
              WikiCommandPalette.show(context,
                  projectId: projectId, jumpToPage: true),
          const SingleActivator(LogicalKeyboardKey.keyP, control: true): () =>
              WikiCommandPalette.show(context,
                  projectId: projectId, jumpToPage: true),
        },
        child: Focus(
          autofocus: true,
          child: Column(
            children: <Widget>[
              Expanded(
                child: Row(
                  crossAxisAlignment: CrossAxisAlignment.stretch,
                  children: <Widget>[
                    WikiNavRail(projectId: projectId),
                    Container(width: 1, color: BiuTokens.borderSubtle),
                    Expanded(child: child),
                  ],
                ),
              ),
              Container(height: 1, color: BiuTokens.borderSubtle),
              WikiStatusBar(projectId: projectId),
            ],
          ),
        ),
      ),
    );
  }
}

/// WikiShell 手机形态 —— rail 收进 Drawer + 全宽内容 + 状态栏保留。
///
/// 这是 app shell 顶层导航 Drawer 之外的**第二个独立 drawer**（装 wiki
/// 树），由 WikiShell 自己的 Scaffold 承载：菜单按钮用 `Scaffold.of`
/// 命中本层（按钮是 body 的直接后代），不走 appShellScaffoldKey ——
/// 那个开的是顶层导航抽屉。
class _PhoneWikiShell extends StatelessWidget {
  const _PhoneWikiShell({required this.projectId, required this.child});

  final String projectId;
  final Widget child;

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: BiuTokens.bg,
      // drawer 宽度与 app shell 一致：min(304, 屏宽×0.85)。
      drawer: Drawer(
        width: math.min(304, MediaQuery.sizeOf(context).width * 0.85),
        child: SafeArea(
          child: WikiNavRail(projectId: projectId, inDrawer: true),
        ),
      ),
      body: CallbackShortcuts(
        bindings: <ShortcutActivator, VoidCallback>{
          const SingleActivator(LogicalKeyboardKey.keyK, meta: true): () =>
              WikiCommandPalette.show(context, projectId: projectId),
          const SingleActivator(LogicalKeyboardKey.keyK, control: true): () =>
              WikiCommandPalette.show(context, projectId: projectId),
          const SingleActivator(LogicalKeyboardKey.keyP, meta: true): () =>
              WikiCommandPalette.show(context,
                  projectId: projectId, jumpToPage: true),
          const SingleActivator(LogicalKeyboardKey.keyP, control: true): () =>
              WikiCommandPalette.show(context,
                  projectId: projectId, jumpToPage: true),
        },
        child: Focus(
          autofocus: true,
          child: Column(
            children: <Widget>[
              const _PhoneWikiHeader(),
              Expanded(child: child),
              Container(height: 1, color: BiuTokens.borderSubtle),
              // 32px 状态栏手机上保留 —— 高度可接受，sync 状态点在弱网/
              // 上传等待场景仍有信息量。
              WikiStatusBar(projectId: projectId),
            ],
          ),
        ),
      ),
    );
  }
}

/// 手机形态的壳内页头：☰（开 wiki drawer）+ 「知识库」。
/// 样式与设置的 _PhoneListHeader / PhoneSubpageHeader 一致（48px + 底部线）。
class _PhoneWikiHeader extends StatelessWidget {
  const _PhoneWikiHeader();

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Container(
      height: 48,
      decoration: BoxDecoration(
        border: Border(
          bottom: BorderSide(color: theme.colorScheme.outlineVariant),
        ),
      ),
      child: Row(
        children: <Widget>[
          IconButton(
            icon: const Icon(Icons.menu),
            tooltip: MaterialLocalizations.of(context).openAppDrawerTooltip,
            onPressed: () => Scaffold.of(context).openDrawer(),
          ),
          Text(
            '知识库',
            style: theme.textTheme.titleMedium
                ?.copyWith(fontWeight: FontWeight.w600),
          ),
        ],
      ),
    );
  }
}

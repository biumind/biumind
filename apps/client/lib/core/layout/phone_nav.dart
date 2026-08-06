// 手机形态导航辅助 — App shell Scaffold key + ☰ 按钮。
// 方案: docs/BiuMind-Mobile-Adaptation-Plan.md §4.2

import 'package:flutter/material.dart';
import 'package:go_router/go_router.dart';

import 'form_factor.dart';

/// 手机形态顶部「更多」按钮 (R1.6 起语义变更): 原开主导航 Drawer, 现底部
/// 5 tab 是唯一主导航, ☰ 改为通用「更多」popup (搜索 / 帮助反馈)。桌面
/// 形态 shrink 不占位。
///
/// pinned app 从「应用」tab 进; 侧边栏定制是桌面功能, 移动端不暴露。
class PhoneMenuButton extends StatelessWidget {
  const PhoneMenuButton({super.key});

  @override
  Widget build(BuildContext context) {
    if (!isPhoneLayout(context)) return const SizedBox.shrink();
    return PopupMenuButton<String>(
      icon: const Icon(Icons.more_vert),
      tooltip: '更多',
      onSelected: (v) {
        switch (v) {
          case 'search':
            context.go('/search');
          case 'help':
            context.go('/suggestions');
        }
      },
      itemBuilder: (_) => const [
        PopupMenuItem(
          value: 'search',
          child: ListTile(
            leading: Icon(Icons.search_outlined),
            title: Text('搜索'),
            dense: true,
            contentPadding: EdgeInsets.zero,
          ),
        ),
        PopupMenuItem(
          value: 'help',
          child: ListTile(
            leading: Icon(Icons.feedback_outlined),
            title: Text('帮助与反馈'),
            dense: true,
            contentPadding: EdgeInsets.zero,
          ),
        ),
      ],
    );
  }
}

/// 手机形态「列表 → 详情」两级导航的详情页头: ← 返回 + 标题。
/// 仅手机两级容器 (设置详情 / AI 服务商详情等) 使用; 桌面壳不经过这些容器。
class PhoneSubpageHeader extends StatelessWidget {
  const PhoneSubpageHeader({
    super.key,
    required this.title,
    required this.onBack,
  });

  final String title;
  final VoidCallback onBack;

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
        children: [
          IconButton(
            icon: const Icon(Icons.arrow_back),
            tooltip: MaterialLocalizations.of(context).backButtonTooltip,
            onPressed: onBack,
          ),
          Expanded(
            child: Text(
              title,
              style: theme.textTheme.titleMedium
                  ?.copyWith(fontWeight: FontWeight.w600),
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
            ),
          ),
        ],
      ),
    );
  }
}

/// 子页头部左位的 ← (手机形态): maybePop 返回上一层; 桌面 shrink 不占位。
/// 与 [PhoneMenuButton] 互补: 顶层页头部给 ☰ (Drawer), 子页头部给 ← (返回)。
/// 导航设计 docs/BiuMind-Mobile-Navigation-Design.md §3.3。
class PhoneBackButton extends StatelessWidget {
  const PhoneBackButton({super.key});

  @override
  Widget build(BuildContext context) {
    if (!isPhoneLayout(context)) return const SizedBox.shrink();
    return IconButton(
      icon: const Icon(Icons.arrow_back),
      tooltip: MaterialLocalizations.of(context).backButtonTooltip,
      // maybePop: 深链直达等无栈场景静默无操作 (不会出现黑屏)。
      onPressed: () => Navigator.of(context).maybePop(),
    );
  }
}

/// AppBar.leading 位的 ← (导航设计 §3.3)。不能直接
/// `leading: const PhoneBackButton()`: AppBar 对任何非 null leading 恒占
/// _kLeadingWidth (56px) — shrink 的按钮也会让桌面标题右移 56px。
/// 因此桌面必须传 null; 手机恒给 ← (深链无栈时 maybePop 静默无操作)。
Widget? phoneBackLeading(BuildContext context) =>
    isPhoneLayout(context) ? const PhoneBackButton() : null;

/// 内容驱动进入子页的统一入口 (导航设计 §3.3):
/// 手机形态 context.push (产生可 pop 的栈 — 可视 ← / 右滑 / 系统返回三通道);
/// 桌面 / Web context.go (tab 模型替换, 无栈, 零回归)。
///
/// 顶层目的地切换 (Drawer / rail / 导航菜单) 不要用本函数 — 永远 ctx.go。
void enterSubPage(BuildContext context, String location, {Object? extra}) {
  if (isPhoneLayout(context)) {
    context.push<void>(location, extra: extra);
  } else {
    context.go(location, extra: extra);
  }
}

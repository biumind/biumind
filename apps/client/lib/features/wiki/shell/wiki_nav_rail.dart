// Wiki 内部 NavRail —— 220px 文本 + 图标列表。
//
// 借鉴 knowcode nav_rail 的两种形态：
//
// * 工作区模式（projectId 为空）：品牌 + 工作区 + 邮箱 + 全局设置 + 退出登录
// * 项目模式（projectId 非空）：品牌 + 10 个功能入口 + 工作区/LLM 设置/全局设置
//
// Phase 0：除"页面"外的子 tab 都点进占位 TODO 页面，后续按批迁移。

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../app/theme.dart';
import '../../settings/application/settings_controller.dart';
import 'wiki_tokens.dart';

class WikiNavRail extends ConsumerWidget {
  const WikiNavRail({super.key, required this.projectId, this.inDrawer = false});

  final String projectId;

  /// 手机形态：作为 WikiShell Drawer 内容渲染 —— 宽度撑满 drawer，
  /// 点条目先关抽屉再跳转，条目触摸目标加大到 ≥40px。桌面恒为 false。
  final bool inDrawer;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final loc = GoRouterState.of(context).uri.path;
    return projectId.isEmpty
        ? _WorkspaceMode(loc: loc, inDrawer: inDrawer)
        : _ProjectMode(loc: loc, projectId: projectId, inDrawer: inDrawer);
  }
}

class _ProjectMode extends StatelessWidget {
  const _ProjectMode({
    required this.loc,
    required this.projectId,
    this.inDrawer = false,
  });
  final String loc;
  final String projectId;
  final bool inDrawer;

  @override
  Widget build(BuildContext context) {
    final base = '/wiki/p/$projectId';
    bool active(String suffix) {
      if (suffix.isEmpty) {
        return loc == base || loc.startsWith('$base/pages/');
      }
      return loc.startsWith('$base$suffix');
    }

    final items = <_NavItem>[
      _NavItem(
        icon: Icons.description_outlined,
        label: '页面',
        // ⌘P = 按页面名跳页（WikiShell 绑定打开命令面板的页面跳转模式）。
        trailing: '⌘P',
        selected: active(''),
        onTap: () => context.go(base),
      ),
      _NavItem(
        icon: Icons.upload_file_outlined,
        label: '源文件',
        selected: active('/sources'),
        onTap: () => context.go('$base/sources'),
      ),
      _NavItem(
        icon: Icons.search,
        label: '搜索',
        selected: active('/search'),
        onTap: () => context.go('$base/search'),
      ),
      _NavItem(
        icon: Icons.hub_outlined,
        label: '图谱',
        selected: active('/graph'),
        onTap: () => context.go('$base/graph'),
      ),
      _NavItem(
        icon: Icons.chat_bubble_outline,
        label: '对话',
        selected: active('/chat'),
        onTap: () => context.go('$base/chat'),
      ),
      _NavItem(
        icon: Icons.travel_explore_outlined,
        label: '研究',
        selected: active('/research'),
        onTap: () => context.go('$base/research'),
      ),
      _NavItem(
        icon: Icons.fact_check_outlined,
        label: '审查',
        selected: active('/reviews'),
        onTap: () => context.go('$base/reviews'),
      ),
      _NavItem(
        icon: Icons.folder_copy_outlined,
        label: '镜像',
        selected: active('/mirror'),
        onTap: () => context.go('$base/mirror'),
      ),
    ];

    return _RailFrame(
      inDrawer: inDrawer,
      children: <Widget>[
        _BrandRow(inDrawer: inDrawer),
        const SizedBox(height: WikiTokens.space5),
        ...items.map((it) => _NavTile(item: it, inDrawer: inDrawer)),
        const Spacer(),
        _NavTile(
          inDrawer: inDrawer,
          item: _NavItem(
            icon: Icons.workspaces_outlined,
            label: '工作区',
            selected: false,
            onTap: () => context.go('/wiki'),
          ),
        ),
        _NavTile(
          inDrawer: inDrawer,
          item: _NavItem(
            icon: Icons.tune,
            label: 'LLM 设置',
            // biumind 单作者项目顶层 /settings 已含完整 providers/models
            // 体系，项目级 LLM 设置不再单独做一份；本入口直接跳顶层。
            selected: false,
            onTap: () => context.go('/settings'),
          ),
        ),
        _NavTile(
          inDrawer: inDrawer,
          item: _NavItem(
            icon: Icons.feedback_outlined,
            label: '反馈',
            selected: false,
            onTap: () => context.go('/suggestions'),
          ),
        ),
        _NavTile(
          inDrawer: inDrawer,
          item: _NavItem(
            icon: Icons.settings_outlined,
            label: '全局设置',
            selected: false,
            onTap: () => context.go('/settings'),
          ),
        ),
      ],
    );
  }
}

class _WorkspaceMode extends ConsumerWidget {
  const _WorkspaceMode({required this.loc, this.inDrawer = false});
  final String loc;
  final bool inDrawer;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final atHome = loc == '/wiki';
    final settingsAsync = ref.watch(settingsControllerProvider);
    final email = settingsAsync.valueOrNull?.userEmail;

    return _RailFrame(
      inDrawer: inDrawer,
      children: <Widget>[
        _BrandRow(inDrawer: inDrawer),
        const SizedBox(height: WikiTokens.space5),
        _NavTile(
          inDrawer: inDrawer,
          item: _NavItem(
            icon: Icons.home_outlined,
            label: '工作区',
            selected: atHome,
            onTap: () => context.go('/wiki'),
          ),
        ),
        const Spacer(),
        if (email != null && email.isNotEmpty)
          Padding(
            padding: const EdgeInsets.symmetric(
              horizontal: WikiTokens.space2 + 2,
              vertical: WikiTokens.space2,
            ),
            child: Text(
              email,
              overflow: TextOverflow.ellipsis,
              style: TextStyle(
                color: BiuTokens.textMuted,
                fontSize: WikiTokens.fontXs,
              ),
            ),
          ),
        _NavTile(
          inDrawer: inDrawer,
          item: _NavItem(
            icon: Icons.feedback_outlined,
            label: '反馈',
            selected: false,
            onTap: () => context.go('/suggestions'),
          ),
        ),
        _NavTile(
          inDrawer: inDrawer,
          item: _NavItem(
            icon: Icons.settings_outlined,
            label: '全局设置',
            selected: false,
            onTap: () => context.go('/settings'),
          ),
        ),
        _NavTile(
          inDrawer: inDrawer,
          item: _NavItem(
            icon: Icons.logout,
            label: '退出登录',
            selected: false,
            onTap: () => _signOut(context, ref),
          ),
        ),
      ],
    );
  }

  void _signOut(BuildContext context, WidgetRef ref) async {
    await ref.read(settingsControllerProvider.notifier).signOut();
    if (context.mounted) context.go('/login');
  }
}

class _RailFrame extends StatelessWidget {
  const _RailFrame({required this.children, this.inDrawer = false});
  final List<Widget> children;
  final bool inDrawer;

  @override
  Widget build(BuildContext context) {
    return Container(
      // 桌面固定 220px 二级 rail；drawer 形态撑满 drawer 宽。
      width: inDrawer ? double.infinity : WikiTokens.sidebarWidth,
      color: BiuTokens.bg,
      padding: const EdgeInsets.fromLTRB(
        WikiTokens.space3,
        WikiTokens.space5,
        WikiTokens.space3,
        WikiTokens.space3,
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: children,
      ),
    );
  }
}

class _BrandRow extends StatelessWidget {
  const _BrandRow({this.inDrawer = false});

  final bool inDrawer;

  @override
  Widget build(BuildContext context) {
    final brand = Theme.of(context).colorScheme.primary;
    return InkWell(
      onTap: () {
        // drawer 形态：先关抽屉再跳转（drawer 是 push 上来的 route）。
        if (inDrawer) Navigator.of(context).pop();
        context.go('/wiki');
      },
      borderRadius: BorderRadius.circular(WikiTokens.radiusButton),
      child: Padding(
        // drawer 形态加大纵向 padding —— 触摸目标从 ~34px 提到 ~42px。
        padding: EdgeInsets.symmetric(
          horizontal: WikiTokens.space2,
          vertical: inDrawer ? WikiTokens.space2 : WikiTokens.space1,
        ),
        child: Row(
          children: <Widget>[
            Container(
              width: 26,
              height: 26,
              decoration: BoxDecoration(
                color: brand,
                borderRadius: BorderRadius.circular(WikiTokens.radiusButton),
              ),
              alignment: Alignment.center,
              child: const Text(
                'K',
                style: TextStyle(
                  color: Colors.white,
                  fontSize: 14,
                  fontWeight: FontWeight.w700,
                ),
              ),
            ),
            const SizedBox(width: WikiTokens.space2 + 2),
            Text(
              '知识库',
              style: TextStyle(
                color: BiuTokens.text,
                fontSize: WikiTokens.fontLg,
                fontWeight: FontWeight.w700,
                letterSpacing: -0.2,
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _NavItem {
  _NavItem({
    required this.icon,
    required this.label,
    required this.selected,
    required this.onTap,
    this.trailing,
  });

  final IconData icon;
  final String label;
  final bool selected;
  final VoidCallback onTap;
  final String? trailing;
}

class _NavTile extends StatefulWidget {
  const _NavTile({required this.item, this.inDrawer = false});
  final _NavItem item;
  final bool inDrawer;

  @override
  State<_NavTile> createState() => _NavTileState();
}

class _NavTileState extends State<_NavTile> {
  bool _hover = false;

  @override
  Widget build(BuildContext context) {
    final selected = widget.item.selected;
    final fg = selected
        ? BiuTokens.text
        : (_hover ? BiuTokens.text : BiuTokens.textSecondary);
    final bg = selected
        ? SemanticTokens.successSoft // 绿底 pill,与品牌绿一致
        : (_hover ? BiuTokens.surfaceMuted : Colors.transparent);

    return MouseRegion(
      cursor: SystemMouseCursors.click,
      onEnter: (_) => setState(() => _hover = true),
      onExit: (_) => setState(() => _hover = false),
      child: GestureDetector(
        onTap: () {
          // drawer 形态（手机）：先关抽屉再执行跳转 —— 与 _AppShell
          // ._navigateTo 的 popUntil(isFirst) → go 同序。
          if (widget.inDrawer) Navigator.of(context).pop();
          widget.item.onTap();
        },
        // hover bg 即时切换 — 旧 AnimatedContainer 120ms 在快速划过 wiki 二级
        // nav rail 时多项同时淡出会有残影。
        child: Container(
          margin: const EdgeInsets.symmetric(vertical: 1),
          // drawer 形态加大纵向 padding：触摸目标从 ~34px 提到 ~42px
          // (12+~18+12+2×1 margin)；桌面密度不变。
          padding: EdgeInsets.symmetric(
            horizontal: WikiTokens.space2 + 2,
            vertical: widget.inDrawer ? WikiTokens.space3 : WikiTokens.space2,
          ),
          decoration: BoxDecoration(
            color: bg,
            borderRadius: BorderRadius.circular(WikiTokens.radiusButton),
          ),
          child: Row(
            children: <Widget>[
              Icon(widget.item.icon, size: 16, color: fg),
              const SizedBox(width: WikiTokens.space2 + 2),
              Expanded(
                child: Text(
                  widget.item.label,
                  overflow: TextOverflow.ellipsis,
                  style: TextStyle(
                    color: fg,
                    fontSize: WikiTokens.fontMd,
                    fontWeight: selected ? FontWeight.w600 : FontWeight.w500,
                  ),
                ),
              ),
              if (widget.item.trailing != null)
                Text(
                  widget.item.trailing!,
                  style: TextStyle(
                    color: BiuTokens.textMuted,
                    fontSize: WikiTokens.fontXs,
                    fontFeatures: <FontFeature>[
                      FontFeature.tabularFigures(),
                    ],
                  ),
                ),
            ],
          ),
        ),
      ),
    );
  }
}

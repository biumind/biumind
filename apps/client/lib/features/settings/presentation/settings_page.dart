// Settings page — three-column LobeChat-style layout.
//
// Left column   : nav menu, three groups (通用 / 智能体 / 系统). Most
//                  items are placeholders ("Coming soon", greyed) —
//                  five are wired up:
//                    通用 > 外观
//                    智能体 > AI 服务商   ★ (the main page)
//                    智能体 > 服务模型
//                    智能体 > 凭证管理
//                    系统 > 关于
//
// Middle column : depends on the active nav item. For "AI 服务商"
//                  it's the provider list (search + 已启用/未启用 +
//                  添加自定义). For other items, the column collapses.
//
// Right column  : provider detail when one is selected, or an overview
//                  with provider cards when nothing is.
//
// All other widgets live in providers_settings.dart so this file
// stays a thin shell.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../app/theme.dart';
import '../../../core/layout/form_factor.dart';
import '../../../core/layout/phone_nav.dart';
import '../../../l10n/app_localizations.dart';
import 'activity_pane.dart';
import 'api_keys_page.dart';
import 'appearance_pane.dart';
import 'chat_settings_pane.dart';
import '../../code/code_module.dart';
import 'data_statistics_pane.dart';
import 'docproc_pane.dart';
import 'my_shares_pane.dart';
import 'search_settings_pane.dart';
import 'security_pane.dart';
import 'simple_settings_panes.dart' hide AppearancePane;
import 'sign_out_dialog.dart';
import 'tokens_pane.dart';

enum SettingsTab {
  statistics,
  appearance,
  docproc,
  chat,
  credentials,
  apiKeys,
  security,
  tokens,
  activity,
  codingWorkbench,
  search,
  myShares,
  about,
}

final activeSettingsTabProvider =
    StateProvider<SettingsTab>((_) => SettingsTab.apiKeys);

class SettingsPage extends ConsumerWidget {
  const SettingsPage({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    // 手机形态: 分类列表 → 详情 两级壳
    // (方案 docs/BiuMind-Mobile-Adaptation-Plan.md §4.6); 桌面三栏不变。
    if (isPhoneLayout(context)) return const _PhoneSettingsShell();
    final tab = ref.watch(activeSettingsTabProvider);
    return ColoredBox(
      color: BiuTokens.bg,
      child: Row(
        children: [
          const _NavColumn(),
          VerticalDivider(width: 1, color: BiuTokens.borderSubtle),
          Expanded(child: _Body(tab: tab)),
        ],
      ),
    );
  }
}

class _Body extends StatelessWidget {
  const _Body({required this.tab});
  final SettingsTab tab;

  @override
  Widget build(BuildContext context) {
    switch (tab) {
      case SettingsTab.statistics:
        return const DataStatisticsPane();
      case SettingsTab.appearance:
        return const AppearancePane();
      case SettingsTab.docproc:
        return const DocprocPane();
      case SettingsTab.chat:
        return const ChatSettingsPane();
      case SettingsTab.credentials:
        return const CredentialsPane();
      case SettingsTab.apiKeys:
        return const ApiKeysPage();
      case SettingsTab.security:
        return const SecurityPane();
      case SettingsTab.tokens:
        return const TokensPane();
      case SettingsTab.activity:
        return const ActivityPane();
      case SettingsTab.codingWorkbench:
        return buildCodingWorkbenchPane();
      case SettingsTab.search:
        return const SearchSettingsPane();
      case SettingsTab.myShares:
        return const MySharesPane();
      case SettingsTab.about:
        return const AboutPane();
    }
  }
}

// ─── Left: navigation ──────────────────────────────────

class _NavColumn extends ConsumerWidget {
  const _NavColumn({this.width = 220, this.onTabSelected});

  /// 桌面固定 220; 手机列表态传 double.infinity。
  final double width;

  /// 手机两级壳用来打开详情; 桌面 null (只切 provider)。
  final void Function(SettingsTab)? onTabSelected;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final t = AppLocalizations.of(context)!;
    final active = ref.watch(activeSettingsTabProvider);
    void go(SettingsTab tab) {
      ref.read(activeSettingsTabProvider.notifier).state = tab;
      onTabSelected?.call(tab);
    }

    return SizedBox(
      width: width,
      child: ListView(
        padding: const EdgeInsets.symmetric(
            horizontal: BiuTokens.space2, vertical: BiuTokens.space4),
        children: [
          // ─── 通用 ─────────────────────────────────
          _GroupHeader(label: t.settingsGroupGeneral),
          _NavItem(
            label: t.settingsNavStatistics,
            icon: Icons.bar_chart_rounded,
            selected: active == SettingsTab.statistics,
            onTap: () => go(SettingsTab.statistics),
          ),
          _NavItem(
            label: t.settingsNavAppearance,
            icon: Icons.palette_outlined,
            selected: active == SettingsTab.appearance,
            onTap: () => go(SettingsTab.appearance),
          ),
          _NavItem(
            label: t.settingsNavShortcuts,
            icon: Icons.keyboard_outlined,
            comingSoon: true,
          ),
          _NavItem(
            label: '搜索',
            icon: Icons.search_outlined,
            selected: active == SettingsTab.search,
            onTap: () => go(SettingsTab.search),
          ),
          _NavItem(
            label: '我的分享',
            icon: Icons.share_outlined,
            selected: active == SettingsTab.myShares,
            onTap: () => go(SettingsTab.myShares),
          ),
          _NavItem(
            label: t.settingsNavDocproc,
            icon: Icons.article_outlined,
            selected: active == SettingsTab.docproc,
            onTap: () => go(SettingsTab.docproc),
          ),

          // ─── 智能体 ───────────────────────────────
          _GroupHeader(label: t.settingsGroupAgent),
          // 技能 has its own top-level rail (lib/features/skills/) so
          // the duplicate Settings entry was just user confusion. The
          // top-level page already covers list / install / approve /
          // realtime sync. If per-user skill prefs ever land (e.g.
          // marketplace adapter on/off, ed25519 trust store), add a
          // dedicated SkillsSettingsPane here.
          _NavItem(
            label: t.settingsNavMemory,
            icon: Icons.memory_outlined,
            comingSoon: true,
          ),
          _NavItem(
            label: t.settingsNavCredentials,
            icon: Icons.key_outlined,
            selected: active == SettingsTab.credentials,
            onTap: () => go(SettingsTab.credentials),
          ),
          _NavItem(
            label: '模型服务',
            icon: Icons.vpn_key_outlined,
            selected: active == SettingsTab.apiKeys,
            onTap: () => go(SettingsTab.apiKeys),
          ),
          _NavItem(
            label: '已登录设备',
            icon: Icons.devices_other_outlined,
            selected: active == SettingsTab.security,
            onTap: () => go(SettingsTab.security),
          ),
          _NavItem(
            label: 'API Tokens',
            icon: Icons.vpn_key_outlined,
            selected: active == SettingsTab.tokens,
            onTap: () => go(SettingsTab.tokens),
          ),
          _NavItem(
            label: '活动',
            icon: Icons.history_outlined,
            selected: active == SettingsTab.activity,
            onTap: () => go(SettingsTab.activity),
          ),
          if (codeModuleEnabled)
            _NavItem(
              label: t.settingsNavCodingWorkbench,
              icon: Icons.terminal_rounded,
              selected: active == SettingsTab.codingWorkbench,
              onTap: () => go(SettingsTab.codingWorkbench),
            ),
          _NavItem(
            label: t.settingsNavChat,
            icon: Icons.chat_bubble_outline,
            selected: active == SettingsTab.chat,
            onTap: () => go(SettingsTab.chat),
          ),

          // ─── 订阅与计费 ────────────────────────────
          _GroupHeader(label: '订阅与计费'),
          _NavItem(
            label: '会员中心',
            icon: Icons.workspace_premium_outlined,
            onTap: () => enterSubPage(context, '/membership'),
          ),
          _NavItem(
            label: '订单历史',
            icon: Icons.receipt_long_outlined,
            onTap: () => enterSubPage(context, '/membership/orders'),
          ),
          _NavItem(
            label: '兑换码',
            icon: Icons.card_giftcard_outlined,
            onTap: () => enterSubPage(context, '/membership/coupons'),
          ),
          _NavItem(
            label: '邀请奖励',
            icon: Icons.group_add_outlined,
            onTap: () => enterSubPage(context, '/membership/referrals'),
          ),

          // ─── 设备 ─────────────────────────────────
          _GroupHeader(label: '设备'),
          _NavItem(
            label: '我的设备',
            icon: Icons.devices_outlined,
            onTap: () => enterSubPage(context, '/settings/devices'),
          ),

          // ─── 系统 ─────────────────────────────────
          _GroupHeader(label: t.settingsGroupSystem),
          _NavItem(
            label: t.settingsNavProxy,
            icon: Icons.dns_outlined,
            comingSoon: true,
          ),
          _NavItem(
            label: t.settingsNavStorage,
            icon: Icons.storage_outlined,
            comingSoon: true,
          ),
          _NavItem(
            label: t.settingsNavApiKey,
            icon: Icons.vpn_key_outlined,
            comingSoon: true,
          ),
          _NavItem(
            label: t.settingsNavAdvanced,
            icon: Icons.tune_outlined,
            comingSoon: true,
          ),
          _NavItem(
            label: t.settingsNavAbout,
            icon: Icons.info_outline,
            selected: active == SettingsTab.about,
            onTap: () => go(SettingsTab.about),
          ),

          // ─── 账户 ─────────────────────────────────
          _GroupHeader(label: '账户'),
          _NavItem(
            label: '退出登录',
            icon: Icons.logout,
            onTap: () => confirmAndSignOut(context, ref),
          ),
        ],
      ),
    );
  }
}

// ─── 手机: 分类列表 → 详情 两级壳 (§4.6) ────────────────────

class _PhoneSettingsShell extends ConsumerStatefulWidget {
  const _PhoneSettingsShell();

  @override
  ConsumerState<_PhoneSettingsShell> createState() =>
      _PhoneSettingsShellState();
}

class _PhoneSettingsShellState extends ConsumerState<_PhoneSettingsShell> {
  SettingsTab? _detail;

  @override
  Widget build(BuildContext context) {
    final t = AppLocalizations.of(context)!;
    // 外部深链 (命令面板等先设 activeSettingsTabProvider 再 go /settings):
    // provider 变化时直接落进对应详情。
    ref.listen<SettingsTab>(activeSettingsTabProvider, (_, next) {
      if (next != _detail) setState(() => _detail = next);
    });
    final detail = _detail;
    // PopScope: Android 系统返回 / iOS 右滑在详情态映射为「返回分类列表」
    // (导航设计 §3.4); 列表态放行。
    return PopScope(
      canPop: detail == null,
      onPopInvokedWithResult: (didPop, _) {
        if (!didPop) setState(() => _detail = null);
      },
      child: ColoredBox(
        color: BiuTokens.bg,
        child: detail == null
            ? Column(
                children: [
                  _PhoneListHeader(title: t.settingsTitle),
                  Expanded(
                    child: _NavColumn(
                      width: double.infinity,
                      onTabSelected: (tab) =>
                          setState(() => _detail = tab),
                    ),
                  ),
                ],
              )
            : Column(
                children: [
                  PhoneSubpageHeader(
                    title: _tabLabel(t, detail),
                    onBack: () => setState(() => _detail = null),
                  ),
                  Expanded(child: _Body(tab: detail)),
                ],
              ),
      ),
    );
  }
}

class _PhoneListHeader extends StatelessWidget {
  const _PhoneListHeader({required this.title});
  final String title;

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
          const PhoneMenuButton(),
          Text(
            title,
            style: theme.textTheme.titleMedium
                ?.copyWith(fontWeight: FontWeight.w600),
          ),
        ],
      ),
    );
  }
}

String _tabLabel(AppLocalizations t, SettingsTab tab) {
  switch (tab) {
    case SettingsTab.statistics:
      return t.settingsNavStatistics;
    case SettingsTab.appearance:
      return t.settingsNavAppearance;
    case SettingsTab.docproc:
      return t.settingsNavDocproc;
    case SettingsTab.chat:
      return t.settingsNavChat;
    case SettingsTab.credentials:
      return t.settingsNavCredentials;
    case SettingsTab.apiKeys:
      return '模型服务';
    case SettingsTab.security:
      return '已登录设备';
    case SettingsTab.tokens:
      return 'API Tokens';
    case SettingsTab.activity:
      return '活动';
    case SettingsTab.codingWorkbench:
      return t.settingsNavCodingWorkbench;
    case SettingsTab.search:
      return '搜索';
    case SettingsTab.myShares:
      return '我的分享';
    case SettingsTab.about:
      return t.settingsNavAbout;
  }
}

class _GroupHeader extends StatelessWidget {
  const _GroupHeader({required this.label});
  final String label;
  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(BiuTokens.space3, BiuTokens.space4,
          BiuTokens.space3, BiuTokens.space1),
      child: Text(
        label,
        style: TextStyle(
          fontSize: 11,
          fontWeight: FontWeight.w600,
          color: BiuTokens.textMuted,
          letterSpacing: 0.5,
        ),
      ),
    );
  }
}

class _NavItem extends StatelessWidget {
  const _NavItem({
    required this.label,
    required this.icon,
    this.selected = false,
    this.onTap,
    this.comingSoon = false,
  });
  final String label;
  final IconData icon;
  final bool selected;
  final VoidCallback? onTap;
  final bool comingSoon;

  @override
  Widget build(BuildContext context) {
    final disabled = comingSoon || onTap == null;
    return MouseRegion(
      cursor: disabled
          ? SystemMouseCursors.basic
          : SystemMouseCursors.click,
      child: GestureDetector(
        onTap: disabled ? null : onTap,
        behavior: HitTestBehavior.opaque,
        child: Container(
          margin: const EdgeInsets.only(bottom: 1),
          padding: const EdgeInsets.symmetric(
              horizontal: BiuTokens.space3, vertical: BiuTokens.space2),
          decoration: BoxDecoration(
            color: selected ? BiuTokens.purpleSoft : Colors.transparent,
            borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
          ),
          child: Row(
            children: [
              Icon(
                icon,
                size: 16,
                color: disabled
                    ? BiuTokens.textDisabled
                    : (selected ? BiuTokens.purple : BiuTokens.textSecondary),
              ),
              const SizedBox(width: BiuTokens.space2),
              Expanded(
                child: Text(
                  label,
                  style: TextStyle(
                    fontSize: 13,
                    fontWeight: selected ? FontWeight.w600 : FontWeight.w500,
                    color: disabled
                        ? BiuTokens.textDisabled
                        : (selected ? BiuTokens.purple : BiuTokens.text),
                  ),
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                ),
              ),
              if (comingSoon)
                Padding(
                  padding: EdgeInsets.only(left: 4),
                  child: Text(
                    'soon',
                    style: TextStyle(
                      fontSize: 10,
                      color: BiuTokens.textDisabled,
                    ),
                  ),
                ),
            ],
          ),
        ),
      ),
    );
  }
}

// ProfilePage — 移动端底部 tab 5「我的」落地页 (R1.3)。
//
// 账户中心: 顶部账户卡 (头像 / 邮箱 / 积分) + 功能入口列表 (会员 / 订单 /
// 兑换码 / 邀请 / 设备 / 技能 / 设置 / 反馈)。把散落的账户性低频功能收口
// 到一个入口, 设置降为子项 (不再独占 tab 5)。
//
// 设计: docs/BiuMind-Mobile-Redesign-Design.md §4.3
//
// 仅手机形态 (tab 5 = /profile); 桌面 sidebar 无此入口, build 开头 fallback
// 到 SettingsPage 防御手动深链 (Web URL bar)。
//
// l10n: 同 R1.1 决策, 本轮不碰 l10n 系统 (of() nullable 迁移 + arb 补 key
// 是独立债), 入口 label 暂硬编码中文。

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../settings/presentation/sign_out_dialog.dart';

import '../../../app/theme.dart';
import '../../../core/layout/form_factor.dart';
import '../../../core/layout/phone_nav.dart';
import '../../creation/application/credits_controller.dart';
import '../../creation/data/credits_client.dart';
import '../../settings/application/settings_controller.dart';
import '../../settings/presentation/settings_page.dart';

class ProfilePage extends ConsumerWidget {
  const ProfilePage({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    // 桌面防御: sidebar 不会到 /profile, 但 Web URL bar 可能手动深链;
    // fallback 到设置页, 避免桌面渲染一个手机形态的「我的」页。
    if (!isPhoneLayout(context)) return const SettingsPage();

    final email =
        ref.watch(settingsControllerProvider).valueOrNull?.userEmail ?? '';
    final credits = ref.watch(creditsBalanceProvider).valueOrNull;

    return Scaffold(
      backgroundColor: BiuTokens.bg,
      body: SafeArea(
        // bottom:false — 主壳外层 SafeArea(top:phone) + 底部 PhoneTabBar 已
        // 处理 inset; 这里不再重复包底部。
        bottom: false,
        child: Column(
          children: [
            const _ProfileHeader(),
            Expanded(
              child: ListView(
                padding: const EdgeInsets.symmetric(vertical: 4),
                children: [
                  _AccountCard(email: email, credits: credits),
                  const _SectionHeader('账户'),
                  _Entry(
                    icon: Icons.workspace_premium_outlined,
                    label: '会员中心',
                    onTap: () => enterSubPage(context, '/membership'),
                  ),
                  _Entry(
                    icon: Icons.receipt_long_outlined,
                    label: '订单历史',
                    onTap: () => enterSubPage(context, '/membership/orders'),
                  ),
                  _Entry(
                    icon: Icons.card_giftcard_outlined,
                    label: '兑换码',
                    onTap: () => enterSubPage(context, '/membership/coupons'),
                  ),
                  _Entry(
                    icon: Icons.group_add_outlined,
                    label: '邀请奖励',
                    onTap: () => enterSubPage(context, '/membership/referrals'),
                  ),
                  const _SectionHeader('设备与技能'),
                  _Entry(
                    icon: Icons.devices_outlined,
                    label: '我的设备',
                    onTap: () => enterSubPage(context, '/settings/devices'),
                  ),
                  _Entry(
                    icon: Icons.extension_outlined,
                    label: '技能管理',
                    onTap: () => enterSubPage(context, '/skills'),
                  ),
                  const _SectionHeader('设置与帮助'),
                  _Entry(
                    icon: Icons.settings_outlined,
                    label: '设置',
                    onTap: () => enterSubPage(context, '/settings'),
                  ),
                  _Entry(
                    icon: Icons.feedback_outlined,
                    label: '帮助与反馈',
                    onTap: () => enterSubPage(context, '/suggestions'),
                  ),
                  const SizedBox(height: 24),
                  _Entry(
                    icon: Icons.logout,
                    label: '退出登录',
                    destructive: true,
                    onTap: () => confirmAndSignOut(context, ref),
                  ),
                  const SizedBox(height: 16),
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }
}

/// 顶部 header: ☰ (开 Drawer, R1.6 前语义) + 「我的」标题。
/// 仿 settings _PhoneListHeader, 左对齐。
class _ProfileHeader extends StatelessWidget {
  const _ProfileHeader();

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
            '我的',
            style: theme.textTheme.titleMedium
                ?.copyWith(fontWeight: FontWeight.w600),
          ),
        ],
      ),
    );
  }
}

/// 账户卡: 头像 (邮箱首字母) + 邮箱 + 积分。会员 badge 待 R1.x 接
/// mySubscriptionProvider 精化 (status == active → Pro), 本轮克制省略。
class _AccountCard extends StatelessWidget {
  const _AccountCard({required this.email, required this.credits});
  final String email;
  final CreditsBalance? credits;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final c = theme.extension<BiuColors>()!;
    final initial = email.isNotEmpty ? email[0].toUpperCase() : '?';
    return Container(
      margin: const EdgeInsets.fromLTRB(16, 12, 16, 4),
      padding: const EdgeInsets.all(16),
      decoration: BoxDecoration(
        color: c.surface0,
        borderRadius: BorderRadius.circular(RadiusTokens.lg),
        border: Border.all(color: c.borderSoft),
      ),
      child: Row(
        children: [
          CircleAvatar(
            radius: 24,
            backgroundColor: c.brandSoft,
            child: Text(
              initial,
              style: TextStyle(
                fontSize: 20,
                fontWeight: FontWeight.w600,
                color: c.brand,
              ),
            ),
          ),
          const SizedBox(width: SpacingTokens.s3),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  email.isNotEmpty ? email : '已登录',
                  style: theme.textTheme.titleSmall
                      ?.copyWith(fontWeight: FontWeight.w600),
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                ),
                const SizedBox(height: 4),
                Text(
                  credits != null ? '${credits!.total} 积分' : '积分加载中…',
                  style:
                      theme.textTheme.bodySmall?.copyWith(color: c.text3),
                ),
              ],
            ),
          ),
        ],
      ),
    );
  }
}

class _SectionHeader extends StatelessWidget {
  const _SectionHeader(this.text);
  final String text;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final c = theme.extension<BiuColors>()!;
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 20, 16, 4),
      child: Text(
        text,
        style: theme.textTheme.labelMedium?.copyWith(
          color: c.text3,
          fontWeight: FontWeight.w600,
        ),
      ),
    );
  }
}

class _Entry extends StatelessWidget {
  const _Entry({
    required this.icon,
    required this.label,
    required this.onTap,
    this.destructive = false,
  });
  final IconData icon;
  final String label;
  final VoidCallback onTap;
  final bool destructive;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final c = theme.extension<BiuColors>()!;
    return Material(
      color: Colors.transparent,
      child: InkWell(
        onTap: onTap,
        child: Padding(
          padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 14),
          child: Row(
            children: [
              Icon(icon, size: 20, color: destructive ? BiuTokens.error : c.text2),
              const SizedBox(width: SpacingTokens.s3),
              Expanded(
                child: Text(
                  label,
                  style: theme.textTheme.bodyMedium
                      ?.copyWith(color: destructive ? BiuTokens.error : c.text1),
                ),
              ),
              Icon(Icons.chevron_right, size: 18, color: c.textMuted),
            ],
          ),
        ),
      ),
    );
  }
}

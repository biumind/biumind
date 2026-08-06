// ReferralPage — W6-13 邀请奖励主页.
//
// 显示:
//   - 用户自己的 8 位邀请码
//   - 邀请统计 (total / pending / rewarded / reverted)
//   - 分享按钮 (打开 ReferralShareSheet)
//   - 兑换他人邀请码入口 (claim — 注册后第一次进来时才用)

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../../core/layout/phone_nav.dart';
import '../../application/membership_providers.dart';
import '../../domain/referral.dart';
import '../widgets/referral_share_sheet.dart';

class ReferralPage extends ConsumerWidget {
  /// 已登录 user 的 user_id (用于嵌邀请链接). 调用方传入.
  final String currentUserID;
  const ReferralPage({super.key, required this.currentUserID});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final statsAsync = ref.watch(referralStatsProvider);
    return Scaffold(
      appBar: AppBar(
        // 子页头左位 ← (手机形态; 桌面必须为 null, 见 phone_nav.dart)。
        leading: phoneBackLeading(context),
        title: const Text('邀请奖励'),
      ),
      body: statsAsync.when(
        data: (stats) => _Body(stats: stats, currentUserID: currentUserID),
        loading: () => const Center(child: CircularProgressIndicator()),
        error: (e, _) => Center(child: Text('$e')),
      ),
    );
  }
}

class _Body extends StatelessWidget {
  final ReferralStats stats;
  final String currentUserID;
  const _Body({required this.stats, required this.currentUserID});

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return SingleChildScrollView(
      padding: const EdgeInsets.all(20),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Card(
            elevation: 2,
            shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(16)),
            child: Padding(
              padding: const EdgeInsets.all(24),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    '你的邀请码',
                    style: theme.textTheme.bodySmall?.copyWith(color: theme.hintColor),
                  ),
                  const SizedBox(height: 8),
                  Row(
                    children: [
                      Expanded(
                        child: SelectableText(
                          stats.inviteCode.isEmpty ? '——' : stats.inviteCode,
                          style: theme.textTheme.displaySmall?.copyWith(
                            fontFamily: 'monospace',
                            fontWeight: FontWeight.w800,
                            letterSpacing: 4,
                            color: theme.colorScheme.primary,
                          ),
                        ),
                      ),
                      FilledButton.icon(
                        icon: const Icon(Icons.share),
                        label: const Text('分享'),
                        onPressed: stats.inviteCode.isEmpty
                            ? null
                            : () => _openShare(context),
                      ),
                    ],
                  ),
                ],
              ),
            ),
          ),
          const SizedBox(height: 16),
          Text('邀请统计', style: theme.textTheme.titleMedium),
          const SizedBox(height: 12),
          Row(
            children: [
              _StatTile(label: '总邀请', value: stats.total),
              _StatTile(label: '已奖励', value: stats.rewarded, color: Colors.green),
              _StatTile(label: '待生效', value: stats.pending, color: Colors.orange),
              _StatTile(label: '已撤销', value: stats.reverted, color: Colors.red),
            ],
          ),
          const SizedBox(height: 24),
          Text('奖励规则', style: theme.textTheme.titleMedium),
          const SizedBox(height: 8),
          Card(
            child: Padding(
              padding: const EdgeInsets.all(16),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: const [
                  _RewardLine(text: '被邀请人首次完成 1 笔有效订阅 → 双方各得 500 积分'),
                  SizedBox(height: 6),
                  _RewardLine(text: '同 IP / 设备指纹 24h 内邀请超阈值视为刷量, 不发奖励'),
                  SizedBox(height: 6),
                  _RewardLine(text: '被邀请人退款 / 异常 → 已发奖励将回收'),
                ],
              ),
            ),
          ),
        ],
      ),
    );
  }

  void _openShare(BuildContext context) {
    showModalBottomSheet<void>(
      context: context,
      isScrollControlled: true,
      shape: const RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(top: Radius.circular(16)),
      ),
      builder: (_) => ReferralShareSheet(
        inviteCode: stats.inviteCode,
        inviterUserID: currentUserID,
      ),
    );
  }
}

class _StatTile extends StatelessWidget {
  final String label;
  final int value;
  final Color? color;
  const _StatTile({required this.label, required this.value, this.color});

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Expanded(
      child: Card(
        margin: const EdgeInsets.symmetric(horizontal: 4),
        child: Padding(
          padding: const EdgeInsets.symmetric(vertical: 16, horizontal: 8),
          child: Column(
            children: [
              Text(
                '$value',
                style: theme.textTheme.headlineMedium?.copyWith(
                  fontWeight: FontWeight.w700,
                  color: color ?? theme.colorScheme.primary,
                ),
              ),
              const SizedBox(height: 2),
              Text(
                label,
                style: theme.textTheme.bodySmall?.copyWith(color: theme.hintColor),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

class _RewardLine extends StatelessWidget {
  final String text;
  const _RewardLine({required this.text});

  @override
  Widget build(BuildContext context) {
    return Row(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Icon(Icons.bolt, size: 16, color: Theme.of(context).colorScheme.primary),
        const SizedBox(width: 6),
        Expanded(child: Text(text, style: Theme.of(context).textTheme.bodySmall)),
      ],
    );
  }
}

// MembershipCenterPage — W7 IA 重设计.
//
// 「定价中心」一站式: 当前订阅 + 余额 hero / 月-年付切换 / 套餐对比 /
// 单次充值积分包 / 兑换码 / 邀请奖励.
//
// 入口:
//   - 主侧栏底部 CreditIndicator 点击 (替代旧 /creation/recharge)
//   - 设置页「会员中心」
//   - 用户头像下拉「会员中心」

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../../app/theme/effects.dart';
import '../../../../app/theme/extensions.dart' show BiuColors;
import '../../../../app/theme/palettes.dart';
import '../../../../core/layout/phone_nav.dart';
import '../../../../core/ui/biu_glass.dart';
import '../../../creation/application/credits_controller.dart';
import '../../../settings/application/settings_controller.dart';
import '../../application/membership_providers.dart';
import '../../domain/plan.dart';
import '../../domain/subscription.dart';
import '../widgets/billing_cycle_toggle.dart';
import '../widgets/cancel_confirm_dialog.dart';
import '../widgets/plan_card.dart';
import '../widgets/recharge_packs_section.dart';
import '../widgets/upgrade_modal.dart';

class MembershipCenterPage extends ConsumerStatefulWidget {
  const MembershipCenterPage({super.key});

  @override
  ConsumerState<MembershipCenterPage> createState() => _MembershipCenterPageState();
}

class _MembershipCenterPageState extends ConsumerState<MembershipCenterPage> {
  BillingCycle _cycle = BillingCycle.monthly;

  @override
  void initState() {
    super.initState();
    // 进会员中心自动刷新余额. 用户可能刚从 checkout 回来 (支付完成,
    // 后端 webhook 已经发积分到账),或刚发完 chat. 5min 缓存可能 stale,
    // 这里强制走最新数据.
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!mounted) return;
      ref.invalidate(creditsBalanceProvider);
    });
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final subAsync = ref.watch(mySubscriptionProvider);
    final plansAsync = ref.watch(plansListProvider);

    return Scaffold(
      appBar: AppBar(
        // 子页头左位 ← (手机形态; 桌面必须为 null — AppBar 对非空 leading
        // 恒占 56px, shrink 也会让标题右移, §3.3)。
        leading: phoneBackLeading(context),
        title: const Text('会员中心'),
        actions: [
          IconButton(
            icon: const Icon(Icons.card_giftcard_outlined),
            tooltip: '兑换码',
            onPressed: () => context.push('/membership/coupons'),
          ),
          IconButton(
            icon: const Icon(Icons.group_add_outlined),
            tooltip: '邀请奖励',
            onPressed: () {
              final uid = subAsync.valueOrNull?.userId ?? '';
              context.push('/membership/referrals', extra: <String, dynamic>{'user_id': uid});
            },
          ),
          IconButton(
            icon: const Icon(Icons.history),
            tooltip: '订单历史',
            onPressed: () => context.push('/membership/orders'),
          ),
        ],
      ),
      body: SingleChildScrollView(
        padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 20),
        child: Center(
          child: ConstrainedBox(
            constraints: const BoxConstraints(maxWidth: 1200),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                // ─── Hero: 当前订阅 + 余额合并 ──
                subAsync.when(
                  data: (sub) => sub != null
                      ? _HeroCard(sub: sub)
                      : const _NotLoggedInBanner(),
                  loading: () => const Padding(
                    padding: EdgeInsets.all(24),
                    child: Center(child: CircularProgressIndicator()),
                  ),
                  error: (e, _) => _ErrorBanner(message: '$e'),
                ),
                const SizedBox(height: 32),

                // ─── 月/年付切换 ──
                Center(
                  child: BillingCycleToggle(
                    value: _cycle,
                    onChanged: (v) => setState(() => _cycle = v),
                  ),
                ),
                const SizedBox(height: 24),

                // ─── 套餐对比 ──
                Text('选择套餐', style: theme.textTheme.titleLarge),
                const SizedBox(height: 12),
                plansAsync.when(
                  data: (plans) => _PlanGrid(
                    plans: plans,
                    currentSub: subAsync.valueOrNull,
                    cycle: _cycle,
                  ),
                  loading: () => const Padding(
                    padding: EdgeInsets.all(24),
                    child: Center(child: CircularProgressIndicator()),
                  ),
                  error: (e, _) => _ErrorBanner(message: '$e'),
                ),
                const SizedBox(height: 36),

                // ─── 单次充值积分包 ──
                const RechargePacksSection(),
                const SizedBox(height: 36),

                // ─── 底部: 兑换码 + 邀请 入口卡 ──
                _ExtraActionsRow(
                  onCoupon: () => context.push('/membership/coupons'),
                  onReferral: () {
                    final uid = subAsync.valueOrNull?.userId ?? '';
                    context.push('/membership/referrals',
                        extra: <String, dynamic>{'user_id': uid});
                  },
                ),
              ],
            ),
          ),
        ),
      ),
    );
  }
}

// ─── Hero (当前订阅 + 余额合并) ───────────────────

class _HeroCard extends ConsumerWidget {
  final Subscription sub;
  const _HeroCard({required this.sub});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final theme = Theme.of(context);
    final balance = ref.watch(creditsBalanceProvider);
    final actions = ref.watch(membershipActionsProvider);
    final canCancel =
        sub.status == SubStatus.active || sub.status == SubStatus.trialing;
    final canResume = sub.status == SubStatus.canceled;

    final c = theme.extension<BiuColors>()!;
    final palette = ref.watch(settingsControllerProvider).valueOrNull?.palette
        ?? PaletteId.inkblueOrange;
    final spec = paletteSpecOf(palette);
    final banner = spec.bannerFor(theme.brightness);
    final radius = BorderRadius.circular(16);
    final layers = bannerLayers(banner, borderRadius: radius);

    // banner = Stack 五层(从底到顶):① 主渐变 ② scrim 暗化 ③ 顶 1px 白色
    // inset 高光 ④ 右上角 radial 微光 ⑤ 内容(文字 + CTA glass)
    // 跟 prototype `.banner` 完整 box-shadow + ::before scrim + ::after radial
    // 一致,深色 banner 显得更有"玻璃质感"。
    return Stack(
      children: [
        // ① 主渐变
        Positioned.fill(child: DecoratedBox(decoration: layers.main)),
        // ② scrim 暗化(顶 2% black → 底 16% black,提文字对比度)
        Positioned.fill(child: DecoratedBox(decoration: layers.scrim)),
        // ③ 顶 1px 白色 inset 高光 (prototype `inset 0 1px 0 rgba(255,255,255,0.12)`)
        Positioned.fill(
          child: IgnorePointer(
            child: DecoratedBox(
              decoration: bannerTopEdgeHighlight(borderRadius: radius),
            ),
          ),
        ),
        // ④ 右上角 radial 微光 (prototype `::after radial-gradient circle at 100% 0%`)
        Positioned.fill(
          child: IgnorePointer(
            child: DecoratedBox(
              decoration: bannerHighlightOverlay(borderRadius: radius),
            ),
          ),
        ),
        // ③ 内容
        Padding(
      padding: const EdgeInsets.all(24),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          // Top row: 方案 + 状态 chip
          Row(
            children: [
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      '当前方案',
                      style: TextStyle(
                        color: c.bannerFgDim,
                        fontSize: 12,
                        shadows: bannerTextShadow,
                      ),
                    ),
                    const SizedBox(height: 4),
                    Text(
                      sub.plan.name,
                      style: TextStyle(
                        color: c.bannerFg,
                        fontSize: 28,
                        fontWeight: FontWeight.w800,
                        letterSpacing: -0.7,
                        shadows: bannerTextShadow,
                      ),
                    ),
                    if (sub.currentPeriodEnd != null)
                      Padding(
                        padding: const EdgeInsets.only(top: 4),
                        child: Text(
                          '周期至 ${sub.currentPeriodEnd!.toLocal().toString().split(' ').first}',
                          style: TextStyle(
                            color: c.bannerFgDim,
                            fontSize: 12,
                            shadows: bannerTextShadow,
                          ),
                        ),
                      ),
                  ],
                ),
              ),
              // 状态 chip 用 BiuGlass 玻璃磨砂(对应 prototype --banner-cta-bg
              // + backdrop-filter saturate(180%) blur(20px))
              BiuGlass(
                borderRadius: BorderRadius.circular(999),
                tintColor: Colors.white,
                tintAlpha: 0.18,
                border: Border.all(color: c.bannerCtaBorder),
                child: Padding(
                  padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 4),
                  child: Text(
                    sub.status.label,
                    style: TextStyle(
                      color: c.bannerCtaFg,
                      fontSize: 12,
                      fontWeight: FontWeight.w600,
                      shadows: bannerTextShadow,
                    ),
                  ),
                ),
              ),
            ],
          ),
          const SizedBox(height: 20),
          Container(height: 1, color: Colors.white.withValues(alpha: 0.18)),
          const SizedBox(height: 20),

          // Mid row: 余额 + 配额并排
          balance.when(
            // 单位换算: 用 *Credits getter 转整数积分 (毫分 ~/ 1000),否则
            // UI 直接显示 millicents 当积分,数字虚高 1000 倍.
            data: (b) => _BalanceQuotaRow(
              total: b.totalCredits,
              permanent: b.permanentCredits,
              timeLimited: b.timeLimitedCredits,
              chatQuota: sub.chatQuota,
              aigcQuota: sub.aigcQuota,
            ),
            loading: () => const SizedBox(
              height: 40,
              child: Center(
                child: CircularProgressIndicator(color: Colors.white),
              ),
            ),
            error: (e, _) => Text(
              '$e',
              style: const TextStyle(color: Colors.white),
            ),
          ),
          if (canCancel || canResume) ...[
            const SizedBox(height: 16),
            Row(
              children: [
                if (canCancel)
                  TextButton.icon(
                    icon: const Icon(Icons.cancel_outlined,
                        size: 18, color: Colors.white),
                    label: const Text('取消订阅',
                        style: TextStyle(color: Colors.white)),
                    onPressed: actions == null
                        ? null
                        : () => _showCancelDialog(context, ref, sub),
                  ),
                if (canResume)
                  TextButton.icon(
                    icon: const Icon(Icons.refresh,
                        size: 18, color: Colors.white),
                    label: const Text('恢复订阅',
                        style: TextStyle(color: Colors.white)),
                    onPressed: actions == null
                        ? null
                        : () async {
                            try {
                              await actions.resume();
                              if (context.mounted) {
                                ScaffoldMessenger.of(context).showSnackBar(
                                  const SnackBar(content: Text('已恢复订阅')),
                                );
                              }
                            } catch (e) {
                              if (context.mounted) {
                                ScaffoldMessenger.of(context).showSnackBar(
                                  SnackBar(content: Text('恢复失败: $e')),
                                );
                              }
                            }
                          },
                  ),
              ],
            ),
          ],
        ],
      ),
    ), // 闭 Padding
      ], // 闭 Stack.children
    );  // 闭 Stack
  }
}

class _BalanceQuotaRow extends StatelessWidget {
  final int total;
  final int permanent;
  final int timeLimited;
  final QuotaUsage chatQuota;
  final QuotaUsage aigcQuota;
  const _BalanceQuotaRow({
    required this.total,
    required this.permanent,
    required this.timeLimited,
    required this.chatQuota,
    required this.aigcQuota,
  });

  @override
  Widget build(BuildContext context) {
    final isWide = MediaQuery.of(context).size.width >= 720;
    final balanceCol = Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      mainAxisSize: MainAxisSize.min,
      children: [
        Text(
          '积分余额',
          style: TextStyle(
            color: Colors.white.withValues(alpha: 0.85),
            fontSize: 12,
          ),
        ),
        const SizedBox(height: 4),
        Row(
          crossAxisAlignment: CrossAxisAlignment.baseline,
          textBaseline: TextBaseline.alphabetic,
          children: [
            Text(
              '$total',
              style: const TextStyle(
                color: Colors.white,
                fontSize: 28,
                fontWeight: FontWeight.w700,
              ),
            ),
            const SizedBox(width: 6),
            const Text('积分',
                style: TextStyle(color: Colors.white, fontSize: 13)),
          ],
        ),
        Text(
          '永久 $permanent · 时效 $timeLimited',
          style: TextStyle(
            color: Colors.white.withValues(alpha: 0.85),
            fontSize: 12,
          ),
        ),
      ],
    );

    final quotaCol = Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      mainAxisSize: MainAxisSize.min,
      children: [
        _QuotaInline(label: 'Chat 月度配额', usage: chatQuota),
        const SizedBox(height: 8),
        _QuotaInline(label: 'AIGC 月度配额', usage: aigcQuota),
      ],
    );

    if (isWide) {
      return Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Expanded(child: balanceCol),
          Expanded(child: quotaCol),
        ],
      );
    }
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        balanceCol,
        const SizedBox(height: 16),
        quotaCol,
      ],
    );
  }
}

class _QuotaInline extends StatelessWidget {
  final String label;
  final QuotaUsage usage;
  const _QuotaInline({required this.label, required this.usage});

  @override
  Widget build(BuildContext context) {
    if (usage.monthly <= 0) {
      return Text(
        '$label · 不在套餐配额内',
        style: TextStyle(
            color: Colors.white.withValues(alpha: 0.7), fontSize: 12),
      );
    }
    final pct = (usage.progress * 100).clamp(0, 100).toStringAsFixed(0);
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Row(
          children: [
            Expanded(
              child: Text(
                label,
                style: const TextStyle(color: Colors.white, fontSize: 12),
              ),
            ),
            Text(
              '${usage.used}/${usage.monthly} ($pct%)',
              style: TextStyle(
                color: Colors.white.withValues(alpha: 0.85),
                fontSize: 11,
              ),
            ),
          ],
        ),
        const SizedBox(height: 4),
        ClipRRect(
          borderRadius: BorderRadius.circular(4),
          child: LinearProgressIndicator(
            value: usage.progress,
            minHeight: 5,
            backgroundColor: Colors.white.withValues(alpha: 0.18),
            valueColor: AlwaysStoppedAnimation(
              usage.exhausted ? Colors.red.shade300 : Colors.white,
            ),
          ),
        ),
      ],
    );
  }
}

void _showCancelDialog(BuildContext context, WidgetRef ref, Subscription sub) {
  showDialog<void>(
    context: context,
    builder: (ctx) => _CancelDialogWrapper(sub: sub),
  );
}

class _CancelDialogWrapper extends ConsumerStatefulWidget {
  final Subscription sub;
  const _CancelDialogWrapper({required this.sub});

  @override
  ConsumerState<_CancelDialogWrapper> createState() =>
      _CancelDialogWrapperState();
}

class _CancelDialogWrapperState extends ConsumerState<_CancelDialogWrapper> {
  bool _busy = false;

  @override
  Widget build(BuildContext context) {
    return CancelConfirmDialog(
      subscription: widget.sub,
      busy: _busy,
      onConfirm: (immediate) async {
        final actions = ref.read(membershipActionsProvider);
        if (actions == null) return;
        setState(() => _busy = true);
        try {
          await actions.cancel(immediate: immediate);
          if (context.mounted) {
            Navigator.of(context).pop();
            ScaffoldMessenger.of(context).showSnackBar(
              SnackBar(content: Text(immediate ? '订阅已立即取消' : '订阅将于周期末取消')),
            );
          }
        } catch (e) {
          if (context.mounted) {
            ScaffoldMessenger.of(context).showSnackBar(
              SnackBar(content: Text('取消失败: $e')),
            );
          }
        } finally {
          if (mounted) setState(() => _busy = false);
        }
      },
    );
  }
}

// ─── Plan grid ──────────────────────────────────

class _PlanGrid extends ConsumerWidget {
  final List<Plan> plans;
  final Subscription? currentSub;
  final BillingCycle cycle;
  const _PlanGrid({
    required this.plans,
    required this.currentSub,
    required this.cycle,
  });

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    if (plans.isEmpty) {
      return const Padding(
        padding: EdgeInsets.all(24),
        child: Center(child: Text('暂无可选套餐')),
      );
    }
    final width = MediaQuery.of(context).size.width;
    final crossAxisCount = width >= 1100 ? 4 : (width >= 720 ? 2 : 1);
    return GridView.builder(
      shrinkWrap: true,
      physics: const NeverScrollableScrollPhysics(),
      gridDelegate: SliverGridDelegateWithFixedCrossAxisCount(
        crossAxisCount: crossAxisCount,
        crossAxisSpacing: 14,
        mainAxisSpacing: 14,
        childAspectRatio: 0.82,
      ),
      itemCount: plans.length,
      itemBuilder: (ctx, i) {
        final p = plans[i];
        final isCurrent = currentSub?.plan.code == p.code;
        return PlanCard(
          plan: p,
          cycle: cycle,
          isCurrent: isCurrent,
          onSelect: isCurrent ? null : () => _onSelect(ctx, ref, p),
          ctaLabel: _ctaLabel(p, currentSub?.plan),
        );
      },
    );
  }

  String _ctaLabel(Plan p, Plan? current) {
    if (current == null) return '选择';
    if (current.code == p.code) return '当前方案';
    return p.sortOrder > current.sortOrder ? '升级' : '降级';
  }

  Future<void> _onSelect(BuildContext context, WidgetRef ref, Plan target) async {
    final actions = ref.read(membershipActionsProvider);
    if (actions == null) return;
    if (currentSub == null || currentSub!.isVirtual) {
      context.push('/membership/checkout', extra: <String, dynamic>{
        'plan_code': target.code.wireValue,
        'billing_cycle':
            cycle == BillingCycle.yearly ? 'yearly' : 'monthly',
      });
      return;
    }
    try {
      final resp = await actions.changePlan(target.code.wireValue);
      if (!context.mounted) return;
      await showDialog<void>(
        context: context,
        builder: (_) => UpgradeModal(
          oldPlan: currentSub!.plan,
          newPlan: target,
          response: resp,
          onClose: () => Navigator.of(context).pop(),
          onProceed: () {
            Navigator.of(context).pop();
            if (resp.isImmediate) {
              context.push('/membership/checkout', extra: <String, dynamic>{
                'plan_code': target.code.wireValue,
                'net_charge_cents': resp.proration?.netChargeCents,
                'billing_cycle':
                    cycle == BillingCycle.yearly ? 'yearly' : 'monthly',
              });
            }
          },
        ),
      );
    } catch (e) {
      if (!context.mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('切换失败: $e')),
      );
    }
  }
}

// ─── Extra actions row ─────────────────────

class _ExtraActionsRow extends StatelessWidget {
  final VoidCallback onCoupon;
  final VoidCallback onReferral;
  const _ExtraActionsRow({required this.onCoupon, required this.onReferral});

  @override
  Widget build(BuildContext context) {
    return Row(
      children: [
        Expanded(
          child: _ExtraTile(
            icon: Icons.card_giftcard,
            title: '兑换码',
            subtitle: '输入券码立即领取奖励',
            onTap: onCoupon,
          ),
        ),
        const SizedBox(width: 12),
        Expanded(
          child: _ExtraTile(
            icon: Icons.group_add,
            title: '邀请奖励',
            subtitle: '邀请好友双方各得 500 积分',
            onTap: onReferral,
          ),
        ),
      ],
    );
  }
}

class _ExtraTile extends StatelessWidget {
  final IconData icon;
  final String title;
  final String subtitle;
  final VoidCallback onTap;
  const _ExtraTile({
    required this.icon,
    required this.title,
    required this.subtitle,
    required this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Card(
      elevation: 0,
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(12),
        side: BorderSide(color: theme.dividerColor),
      ),
      child: InkWell(
        borderRadius: BorderRadius.circular(12),
        onTap: onTap,
        child: Padding(
          padding: const EdgeInsets.all(16),
          child: Row(
            children: [
              Icon(icon, color: theme.colorScheme.primary),
              const SizedBox(width: 12),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(title, style: theme.textTheme.titleSmall),
                    Text(
                      subtitle,
                      style: theme.textTheme.bodySmall?.copyWith(color: theme.hintColor),
                    ),
                  ],
                ),
              ),
              Icon(Icons.arrow_forward, size: 16, color: theme.hintColor),
            ],
          ),
        ),
      ),
    );
  }
}

class _NotLoggedInBanner extends StatelessWidget {
  const _NotLoggedInBanner();
  @override
  Widget build(BuildContext context) {
    return const Card(
      child: Padding(
        padding: EdgeInsets.all(20),
        child: Text('请先登录后查看会员状态'),
      ),
    );
  }
}

class _ErrorBanner extends StatelessWidget {
  final String message;
  const _ErrorBanner({required this.message});
  @override
  Widget build(BuildContext context) {
    return Card(
      color: Theme.of(context).colorScheme.errorContainer,
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Text(message),
      ),
    );
  }
}

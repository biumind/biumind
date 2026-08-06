// CreditIndicator — 主 sidebar 底部全局可见的积分 chip.
//
// 显示 ⚡ N 积分, 点击跳 /membership (会员中心 = 定价中心 = 充值入口).
// 未登录或加载中显示 -- 占位.
//
// 设计参考: docs/BiuMind-AIGC-Migration-Plan.md §3.5
//   "顶部「积分 / 限时 9 折」从顶栏挪到主 sidebar 底部"

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../../app/theme.dart';
import '../../../../core/ui/biu_hoverable.dart';
import '../../../../features/settings/application/settings_controller.dart';
import '../../../membership/application/membership_providers.dart';
import '../../../membership/domain/subscription.dart';
import '../../application/credits_controller.dart';

class CreditIndicator extends ConsumerWidget {
  const CreditIndicator({super.key, this.compact = false});

  /// compact=true 时只显示图标 + 数字 (主 sidebar 收缩成 48px 时使用).
  final bool compact;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final balance = ref.watch(creditsBalanceProvider);
    // 单位换算: total 字段是 millicents (1 积分 = 1000 millicents);
    // UI 显示用 totalCredits getter 转整数积分,避免数字虚高 1000 倍.
    final total = balance.maybeWhen(
      data: (b) => b.totalCredits,
      orElse: () => null,
    );
    // W4-7: 在 tooltip 里附带本月 chat quota 进度. 加载失败时 fallback 不显示.
    final sub = ref.watch(mySubscriptionProvider);
    final chatQuota = sub.maybeWhen(
      data: (s) => s?.chatQuota ?? QuotaUsage.empty,
      orElse: () => QuotaUsage.empty,
    );
    final tooltip = _buildTooltip(total, chatQuota);

    final theme = Theme.of(context);
    final c = theme.extension<BiuColors>();
    final brightness = theme.brightness;
    final st = ShadowTokens.forBrightness(brightness);
    // bolt mini-box 用当前色板的 hero-grad(brandGradient),跟 sidebar 顶 BiuMark
    // 同色族 — prototype `.credit-chip .bolt { background: hero-grad }` 一致。
    final palette = ref.watch(settingsControllerProvider).valueOrNull?.palette ??
        PaletteId.inkblueOrange;
    final spec = paletteSpecOf(palette);

    final radius = BorderRadius.circular(BiuTokens.radiusMd);
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 6),
      child: Tooltip(
        message: tooltip,
        // prototype `.credit-chip:hover { transform: translateY(-1px); shadow-md }`
        // 等价 — surface-2 + hairline border + 1px lift。这里用 BiuHoverable
        // 自定义 builder,因为 BiuLift 强制 child 不带阴影,跟我们 chip 自带 sm
        // shadow 起冲突。
        child: BiuHoverable(
          onTap: () => context.go('/membership'),
          builder: (ctx, hovered, _) {
            return AnimatedContainer(
              duration: const Duration(milliseconds: 200),
              curve: Curves.easeOutCubic,
              transform: Matrix4.translationValues(0, hovered ? -1 : 0, 0),
              padding: EdgeInsets.symmetric(
                horizontal: compact ? 6 : 10,
                vertical: 8,
              ),
              decoration: BoxDecoration(
                color: c?.surface2 ?? theme.colorScheme.surfaceContainer,
                borderRadius: radius,
                border: Border.all(
                  color: c?.borderHairline ?? theme.colorScheme.outlineVariant,
                ),
                boxShadow: hovered ? st.md : st.sm,
              ),
              child: compact
                  ? _CompactContent(total: total, spec: spec, brightness: brightness)
                  : _FullContent(total: total, spec: spec, brightness: brightness),
            );
          },
        ),
      ),
    );
  }
}

class _CompactContent extends StatelessWidget {
  const _CompactContent({
    required this.total,
    required this.spec,
    required this.brightness,
  });
  final int? total;
  final PaletteSpec spec;
  final Brightness brightness;

  @override
  Widget build(BuildContext context) {
    return Column(
      mainAxisSize: MainAxisSize.min,
      children: [
        _BoltBox(spec: spec, brightness: brightness, size: 18),
        const SizedBox(height: 2),
        Text(
          total == null ? '--' : _abbreviate(total!),
          style: TextStyle(
            fontSize: 10,
            fontWeight: FontWeight.w600,
            color: BiuTokens.text,
          ),
        ),
      ],
    );
  }
}

class _FullContent extends StatelessWidget {
  const _FullContent({
    required this.total,
    required this.spec,
    required this.brightness,
  });
  final int? total;
  final PaletteSpec spec;
  final Brightness brightness;

  @override
  Widget build(BuildContext context) {
    return Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        _BoltBox(spec: spec, brightness: brightness, size: 24),
        const SizedBox(width: 8),
        Text(
          total == null ? '--' : '$total',
          style: TextStyle(
            fontSize: 13,
            fontWeight: FontWeight.w600,
            color: BiuTokens.text,
          ),
        ),
        const SizedBox(width: 4),
        Text(
          '积分',
          style: TextStyle(fontSize: 11, color: BiuTokens.textMuted),
        ),
      ],
    );
  }
}

/// 24x24 brand-gradient mini-box 包白色 ⚡ icon — 跟 prototype
/// `.credit-chip .bolt { 24x24; background: hero-grad; color: white }` 对齐,
/// 跟 sidebar 顶 BiuMark 同色族,品牌曝光更强。
class _BoltBox extends StatelessWidget {
  const _BoltBox({
    required this.spec,
    required this.brightness,
    required this.size,
  });
  final PaletteSpec spec;
  final Brightness brightness;
  final double size;

  @override
  Widget build(BuildContext context) {
    return Container(
      width: size,
      height: size,
      alignment: Alignment.center,
      decoration: BoxDecoration(
        gradient: heroBaseLinear(spec, brightness),
        borderRadius: BorderRadius.circular(8),
      ),
      child: Icon(
        Icons.bolt,
        size: size * 0.65,
        color: Colors.white,
      ),
    );
  }
}

/// _buildTooltip: 拼接 tooltip 文案. 含余额 + 本月 chat quota 进度 (有数据时).
String _buildTooltip(int? total, QuotaUsage chatQuota) {
  final sb = StringBuffer();
  sb.write(total == null ? '积分' : '$total 积分 · 点击充值');
  if (chatQuota.monthly > 0) {
    final pct = (chatQuota.progress * 100).clamp(0, 100).toStringAsFixed(0);
    sb.write('\n本月 chat: ${chatQuota.used}/${chatQuota.monthly} ($pct%)');
  }
  return sb.toString();
}

/// _abbreviate: 大数字给 compact 显示用. 1234 → 1.2k, 12345 → 12k, 123456 → 123k.
String _abbreviate(int n) {
  if (n < 1000) return '$n';
  if (n < 10000) return '${(n / 1000).toStringAsFixed(1)}k';
  return '${(n / 1000).round()}k';
}

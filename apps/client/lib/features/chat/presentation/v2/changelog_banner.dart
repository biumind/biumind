// ChangelogBannerV2 —— Hero 顶部展示最新版本更新概述。
// 设计文档 docs/BiuMind-Chat-UI-Benchmark-Optimization.md（v2 changelog）。
//
// 行为：
//   * 当前版本号 [_kVersion] 写死在文件里，每次大更新手动 bump
//   * SharedPreferences 记 dismissed 的版本号；当前版本被 dismissed 后不再
//     显示，直到下一版上线
//   * banner 渲染半透明 primary 色，点 "知道了" 收起，点"详情"展示完整
//     changelog dialog

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:shared_preferences/shared_preferences.dart';

import '../../../../app/theme/effects.dart';
import '../../../../app/theme/palettes.dart';
import '../../../../core/ui/biu_glass.dart';
import '../../../../features/settings/application/settings_controller.dart';
import '../../../../l10n/app_localizations.dart';

const _kVersion = 'v2-2026-06';
const _kDismissedKey = 'biu.chat.hero.changelog.dismissed';

class ChangelogBannerV2 extends ConsumerStatefulWidget {
  const ChangelogBannerV2({super.key});

  @override
  ConsumerState<ChangelogBannerV2> createState() => _ChangelogBannerV2State();
}

class _ChangelogBannerV2State extends ConsumerState<ChangelogBannerV2>
    with SingleTickerProviderStateMixin {
  bool? _show;
  late final AnimationController _entryCtl;
  late final Animation<double> _fade;
  late final Animation<Offset> _slide;

  @override
  void initState() {
    super.initState();
    _entryCtl = AnimationController(
      vsync: this,
      duration: const Duration(milliseconds: 280),
    );
    _fade = CurvedAnimation(parent: _entryCtl, curve: Curves.easeOutCubic);
    _slide = Tween<Offset>(
      begin: const Offset(0, -0.3), // 起点上方 30% 高度,8px 视觉等价
      end: Offset.zero,
    ).animate(_fade);
    _checkDismissed();
  }

  @override
  void dispose() {
    _entryCtl.dispose();
    super.dispose();
  }

  Future<void> _checkDismissed() async {
    try {
      final prefs = await SharedPreferences.getInstance();
      final dismissed = prefs.getString(_kDismissedKey);
      if (!mounted) return;
      setState(() => _show = dismissed != _kVersion);
      if (_show == true) _entryCtl.forward();
    } catch (_) {
      if (mounted) {
        setState(() => _show = true);
        _entryCtl.forward();
      }
    }
  }

  Future<void> _dismiss() async {
    setState(() => _show = false);
    try {
      final prefs = await SharedPreferences.getInstance();
      await prefs.setString(_kDismissedKey, _kVersion);
    } catch (_) {}
  }

  void _openDetails() {
    final l = AppLocalizations.of(context)!;
    final bullets = <String>[
      l.chatV2ChangelogBullet1,
      l.chatV2ChangelogBullet2,
      l.chatV2ChangelogBullet3,
      l.chatV2ChangelogBullet4,
      l.chatV2ChangelogBullet5,
    ];
    showDialog<void>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: Row(
          children: [
            const Icon(Icons.celebration_outlined, size: 18),
            const SizedBox(width: 8),
            Text(l.chatV2ChangelogHeadline),
          ],
        ),
        content: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            for (final b in bullets)
              Padding(
                padding: const EdgeInsets.symmetric(vertical: 3),
                child: Text(b),
              ),
            const SizedBox(height: 8),
            Text(
              _kVersion,
              style: Theme.of(ctx).textTheme.labelSmall?.copyWith(
                    color: Theme.of(ctx).colorScheme.onSurfaceVariant,
                  ),
            ),
          ],
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(ctx).pop(),
            child: Text(l.chatV2DialogOk),
          ),
        ],
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    if (_show != true) return const SizedBox.shrink();
    final theme = Theme.of(context);
    final l = AppLocalizations.of(context)!;
    final disableAnims = MediaQuery.maybeOf(context)?.disableAnimations ?? false;

    // prototype 同款 vibrant banner — 5 层 Stack: 主渐变 + scrim + 顶 inset
    // 高光 + 右上 radial 高光 + 内容(白字 + 玻璃 CTA)。整套跟 membership banner
    // 视觉级别一致,跟 prototype "BiuMind Chat 又添 5 件趁手装备" banner 同款。
    final palette = ref.watch(settingsControllerProvider).valueOrNull?.palette
        ?? PaletteId.inkblueOrange;
    final spec = paletteSpecOf(palette);
    final banner = spec.bannerFor(theme.brightness);
    final radius = BorderRadius.circular(12);
    final layers = bannerLayers(banner, borderRadius: radius);

    final body = Padding(
      padding: const EdgeInsets.only(bottom: 16),
      child: ClipRRect(
        borderRadius: radius,
        child: Stack(
          children: [
            Positioned.fill(child: DecoratedBox(decoration: layers.main)),
            Positioned.fill(child: DecoratedBox(decoration: layers.scrim)),
            Positioned.fill(
              child: IgnorePointer(
                child: DecoratedBox(
                  decoration: bannerTopEdgeHighlight(borderRadius: radius),
                ),
              ),
            ),
            Positioned.fill(
              child: IgnorePointer(
                child: DecoratedBox(
                  decoration: bannerHighlightOverlay(borderRadius: radius),
                ),
              ),
            ),
            Padding(
              padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
              child: Row(
                children: [
                  Text(
                    '✨',
                    style: TextStyle(
                      // prototype `.banner .emoji { font-size: 22px;
                      // filter: drop-shadow(0 2px 6px rgba(0,0,0,.28)) }`
                      fontSize: 22,
                      shadows: [
                        Shadow(
                          color: Colors.black.withValues(alpha: 0.28),
                          offset: const Offset(0, 2),
                          blurRadius: 6,
                        ),
                      ],
                    ),
                  ),
                  const SizedBox(width: 14), // prototype `gap: 14px`
                  Expanded(
                    // prototype `.banner .text { strong + small }` —
                    // 两行文案,strong 700 主标题,small 副标题 12px 500
                    // banner-fg-dim 色,跟 prototype 1:1。
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      mainAxisSize: MainAxisSize.min,
                      children: [
                        Text(
                          l.chatV2ChangelogHeadline,
                          style: TextStyle(
                            color: banner.fg,
                            fontSize: 14,
                            fontWeight: FontWeight.w700,
                            letterSpacing: -0.07, // -0.005em × 14
                            shadows: bannerTextShadow,
                          ),
                        ),
                        const SizedBox(height: 2),
                        Text(
                          l.chatV2ChangelogSubtitle,
                          style: TextStyle(
                            color: banner.fgDim,
                            fontSize: 12,
                            fontWeight: FontWeight.w500,
                            shadows: bannerTextShadow,
                          ),
                        ),
                      ],
                    ),
                  ),
                  // CTA 玻璃按钮 — prototype `.banner .cta`,白半透明 + backdrop-blur
                  BiuGlass(
                    borderRadius: BorderRadius.circular(999),
                    tintColor: Colors.white,
                    tintAlpha: 0.18,
                    border: Border.all(color: banner.ctaBorder),
                    child: InkWell(
                      onTap: _openDetails,
                      borderRadius: BorderRadius.circular(999),
                      child: Padding(
                        // prototype `.banner .cta { padding: 6px 14px }`
                        padding: const EdgeInsets.symmetric(
                            horizontal: 14, vertical: 6),
                        child: Text(
                          l.chatV2ChangelogDetails,
                          style: TextStyle(
                            color: banner.ctaFg,
                            fontSize: 12,
                            fontWeight: FontWeight.w600,
                            shadows: bannerTextShadow,
                          ),
                        ),
                      ),
                    ),
                  ),
                  const SizedBox(width: 6),
                  // close 按钮:白色透明 hover bg
                  InkWell(
                    onTap: _dismiss,
                    borderRadius: BorderRadius.circular(999),
                    child: Padding(
                      padding: const EdgeInsets.all(4),
                      child: Icon(Icons.close,
                          size: 14, color: banner.fgDim),
                    ),
                  ),
                ],
              ),
            ),
          ],
        ),
      ),
    );

    if (disableAnims) return body;
    return FadeTransition(
      opacity: _fade,
      child: SlideTransition(position: _slide, child: body),
    );
  }
}

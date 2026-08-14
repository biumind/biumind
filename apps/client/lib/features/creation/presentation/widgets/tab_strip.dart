// TabStrip — 创作类型 4 段切.
//
// 视频 / 图片 / 数字人 / 爆款解析 切 GenerationType.
//
// 视觉: zhiying-portal 风格 — 浅底 + 当前段背景 surface + 强调色文字.
// 未选 chip 透明背景 + 次级文字色; 选中 chip 实心 (绿色 / 紫色按主题).

import 'package:flutter/material.dart';

import '../../../../app/theme.dart';
import '../../../../l10n/app_localizations.dart';
import '../../application/generation_form_controller.dart';

class TabStrip extends StatelessWidget {
  const TabStrip({
    super.key,
    required this.current,
    required this.onSelect,
  });

  final GenerationType current;
  final ValueChanged<GenerationType> onSelect;

  @override
  Widget build(BuildContext context) {
    final t = AppLocalizations.of(context)!;
    final entries = <_TabEntry>[
      _TabEntry(GenerationType.video, Icons.movie_filter_outlined, t.creationTabVideo),
      _TabEntry(GenerationType.image, Icons.image_outlined, t.creationTabImage),
      // 数字人暂无生成链路, 标「即将上线」(详见 generation_panel.dart)。
      _TabEntry(GenerationType.digitalHuman, Icons.person_outline,
          t.creationTabDigitalHuman, soon: true),
      _TabEntry(GenerationType.hotparse, Icons.local_fire_department_outlined,
          t.creationTabHotparse),
    ];
    return Container(
      padding: const EdgeInsets.all(4),
      decoration: BoxDecoration(
        color: BiuTokens.surfaceMuted,
        borderRadius: BorderRadius.circular(BiuTokens.radiusMd),
      ),
      child: Row(
        children: [
          for (final e in entries)
            Expanded(
              child: _TabSegment(
                icon: e.icon,
                label: e.label,
                active: e.type == current,
                soon: e.soon,
                onTap: () => onSelect(e.type),
              ),
            ),
        ],
      ),
    );
  }
}

class _TabEntry {
  final GenerationType type;
  final IconData icon;
  final String label;
  final bool soon; // 「即将上线」标记
  const _TabEntry(this.type, this.icon, this.label, {this.soon = false});
}

class _TabSegment extends StatelessWidget {
  const _TabSegment({
    required this.icon,
    required this.label,
    required this.active,
    required this.onTap,
    this.soon = false,
  });

  final IconData icon;
  final String label;
  final bool active;
  final bool soon;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final fg = active ? BiuTokens.green : BiuTokens.textSecondary;
    return Material(
      color: active ? BiuTokens.surface : Colors.transparent,
      borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
      child: InkWell(
        borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
        onTap: onTap,
        child: Padding(
          padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 8),
          child: Row(
            mainAxisAlignment: MainAxisAlignment.center,
            mainAxisSize: MainAxisSize.min,
            children: [
              Icon(icon, size: 16, color: fg),
              const SizedBox(width: 6),
              Flexible(
                child: Text(
                  label,
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                  style: TextStyle(
                    fontSize: 13,
                    fontWeight: active ? FontWeight.w600 : FontWeight.w500,
                    color: fg,
                  ),
                ),
              ),
              if (soon) ...[
                const SizedBox(width: 4),
                Container(
                  width: 5,
                  height: 5,
                  decoration: BoxDecoration(
                    color: BiuTokens.purple,
                    shape: BoxShape.circle,
                  ),
                ),
              ],
            ],
          ),
        ),
      ),
    );
  }
}

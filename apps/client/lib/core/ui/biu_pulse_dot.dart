// BiuPulseDot — 呼吸光圈点(在线 / 录音 / 思考中状态指示)。
//
// 对应 prototype:
//   .dot-live {
//     width: 8px; height: 8px;
//     background: var(--sem-success);
//     box-shadow: 0 0 0 3px color-mix(success 25%, transparent);
//     animation: pulse 2.4s ease infinite;
//   }
//   @keyframes pulse {
//     0%, 100% { box-shadow: 0 0 0 0 color-mix(success 30%, transparent); }
//     50%      { box-shadow: 0 0 0 6px color-mix(success 8%, transparent); }
//   }
//
// 实现:
//   * 内圈实色点 (color, size)
//   * 外圈光晕 0..maxRingRadius px,alpha 0.30..0.08 反相(扩散稀释)
//   * AnimationController 2.4s loop,sin 平滑
//   * MediaQuery.disableAnimations 时静态 fallback (中间帧)

import 'dart:math' as math;

import 'package:flutter/material.dart';

class BiuPulseDot extends StatefulWidget {
  const BiuPulseDot({
    super.key,
    required this.color,
    this.size = 8,
    this.maxRingRadius = 6,
    this.duration = const Duration(milliseconds: 2400),
  });

  final Color color;
  final double size;

  /// 光圈最大半径 px(prototype 6px)
  final double maxRingRadius;

  final Duration duration;

  @override
  State<BiuPulseDot> createState() => _BiuPulseDotState();
}

class _BiuPulseDotState extends State<BiuPulseDot>
    with SingleTickerProviderStateMixin {
  late final AnimationController _ctl;

  @override
  void initState() {
    super.initState();
    _ctl = AnimationController(vsync: this, duration: widget.duration)
      ..repeat();
  }

  @override
  void dispose() {
    _ctl.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final disableAnims = MediaQuery.maybeOf(context)?.disableAnimations ?? false;
    if (disableAnims) {
      return _build(widget.maxRingRadius / 2, 0.18);
    }
    return AnimatedBuilder(
      animation: _ctl,
      builder: (ctx, _) {
        // 0..1 三角波 (1 - cos(2πt)) / 2
        final t = (1 - math.cos(2 * math.pi * _ctl.value)) / 2;
        final ringRadius = widget.maxRingRadius * t;
        // alpha 0.30 → 0.08 (扩散最大时反而最淡)
        final ringAlpha = 0.30 - (0.30 - 0.08) * t;
        return _build(ringRadius, ringAlpha);
      },
    );
  }

  Widget _build(double ringRadius, double ringAlpha) {
    final size = widget.size;
    final outerSize = size + ringRadius * 2;
    return SizedBox(
      width: outerSize,
      height: outerSize,
      child: Stack(
        alignment: Alignment.center,
        children: [
          if (ringRadius > 0)
            Container(
              width: outerSize,
              height: outerSize,
              decoration: BoxDecoration(
                shape: BoxShape.circle,
                color: widget.color.withValues(alpha: ringAlpha),
              ),
            ),
          Container(
            width: size,
            height: size,
            decoration: BoxDecoration(
              shape: BoxShape.circle,
              color: widget.color,
            ),
          ),
        ],
      ),
    );
  }
}

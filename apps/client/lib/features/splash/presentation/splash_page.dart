// 启动 splash 页 —— GoRouter 的 initial route。
//
// 进场动画约 1.6s：头淡入 → 电路从下揭开 → 手 slide-in → 文字渐显。
// 完成后根据 hubCredentialsProvider 决定 next:
//   有 creds  → /chat
//   无 creds  → /login
//
// 中央 mark 用 Hero(tag: 'biu-mark')，目标页 LoginPage 的 mark 同 tag → Flutter
// 在 pushReplacement 时自动播 mark 从中心 96px 飞到 login brand 区 72px 的过渡。
//
// 进程内 [_shown] static flag 防止 hot-reload 反复触发。

import 'dart:math' as math;

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../app/theme/brand.dart';
import '../../../services/auth_service.dart';
import '../../../shared/brand/biu_mark.dart';

// Splash 在 theme 加载前显示,只能用 BiuBrand 永久品牌色 + 静态 ink/bg。
// theme-ignore: legacy — 这两个值是 splash 专属,暂保留。
const _kInk = Color(0xFF18181B); // theme-ignore: legacy
const _kBg = Color(0xFFFAFAFA);  // theme-ignore: legacy
const _kPurple = BiuBrand.primary;

/// Hero tag — splash 中央 mark 与 LoginPage 的 brand mark 共用。
const String biuMarkHeroTag = 'biu-mark';

class SplashPage extends ConsumerStatefulWidget {
  const SplashPage({super.key});

  /// 进程内只播一次。
  static bool _shown = false;

  @override
  ConsumerState<SplashPage> createState() => _SplashPageState();
}

class _SplashPageState extends ConsumerState<SplashPage> with SingleTickerProviderStateMixin {
  late final AnimationController _controller;

  @override
  void initState() {
    super.initState();
    _controller = AnimationController(
      vsync: this,
      duration: const Duration(milliseconds: 1600),
    );

    if (SplashPage._shown) {
      // 进程内已播过：不显示 splash，下一帧立即跳走
      WidgetsBinding.instance.addPostFrameCallback((_) => _proceed());
      return;
    }
    _controller.forward().whenComplete(_proceed);
  }

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    // reduced-motion：跳过动画立即过场
    if (!SplashPage._shown && MediaQuery.of(context).disableAnimations) {
      _controller.stop();
      WidgetsBinding.instance.addPostFrameCallback((_) => _proceed());
    }
  }

  void _proceed() {
    SplashPage._shown = true;
    if (!mounted) return;
    final creds = ref.read(hubCredentialsProvider);
    context.pushReplacement(creds != null ? '/chat' : '/login');
  }

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: _kBg,
      body: AnimatedBuilder(
        animation: _controller,
        builder: (context, _) => _SplashContent(progress: _controller.value),
      ),
    );
  }
}

// ─────────────────────────────────────────────────────
// 进场动画内容
// ─────────────────────────────────────────────────────

class _SplashContent extends StatelessWidget {
  const _SplashContent({required this.progress});
  final double progress;

  // 各阶段时间窗（splash 总时长 1.6s）
  static const _headWin = (0.00, 0.32);
  static const _circuitWin = (0.22, 0.60);
  static const _handWin = (0.50, 0.82);
  static const _textWin = (0.60, 0.92);

  static double _phase((double, double) w, double t) =>
      ((t - w.$1) / (w.$2 - w.$1)).clamp(0.0, 1.0);

  static double _easeOutCubic(double t) => 1.0 - math.pow(1.0 - t, 3).toDouble();

  /// easeOutBack — 终值有轻微 overshoot，模拟"指过去"的拟人手势。
  static double _easeOutBack(double t) {
    const c1 = 1.70158;
    const c3 = c1 + 1;
    final x = t - 1;
    return 1 + c3 * x * x * x + c1 * x * x;
  }

  @override
  Widget build(BuildContext context) {
    final headT = _easeOutCubic(_phase(_headWin, progress));
    final circuitT = _easeOutCubic(_phase(_circuitWin, progress));
    final handT = _easeOutBack(_phase(_handWin, progress));
    final textT = _easeOutCubic(_phase(_textWin, progress));

    // 内容整体进场前的微下沉
    final entryRise = (1.0 - headT) * 8.0;

    return Center(
      child: Transform.translate(
        offset: Offset(0, entryRise),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            SizedBox(
              width: 220,
              height: 220,
              child: Stack(
                alignment: Alignment.center,
                children: [
                  // 思考波纹：3 圈错相位扩散
                  _ThinkRing(progress: progress, start: 0.30, end: 0.92),
                  _ThinkRing(progress: progress, start: 0.45, end: 1.00),
                  _ThinkRing(progress: progress, start: 0.60, end: 1.00),
                  // 紫色光晕呼吸
                  _Halo(progress: progress),
                  // Mark 主体（Hero — 飞向 LoginPage）
                  Hero(
                    tag: biuMarkHeroTag,
                    // flightShuttleBuilder：飞行途中渲染完整 mark（不要分层进场状态）
                    flightShuttleBuilder: (
                      flightContext,
                      animation,
                      flightDirection,
                      fromHeroContext,
                      toHeroContext,
                    ) =>
                        const BiuMark(
                      size: 96,
                      inkColor: _kInk,
                      circuitColor: _kPurple,
                    ),
                    child: _StagedMark(headT: headT, circuitT: circuitT, handT: handT),
                  ),
                ],
              ),
            ),
            const SizedBox(height: 18),
            Opacity(
              opacity: textT,
              child: Transform.translate(
                offset: Offset(0, (1.0 - textT) * 8),
                child: const _BiuMindText(),
              ),
            ),
          ],
        ),
      ),
    );
  }
}

// ─────────────────────────────────────────────────────
// Mark 分层进场（96px 容器，叠 3 个 BiuMark 实例分别处理 头/电路/手）
// ─────────────────────────────────────────────────────

class _StagedMark extends StatelessWidget {
  const _StagedMark({required this.headT, required this.circuitT, required this.handT});
  final double headT;
  final double circuitT;
  final double handT;

  static const _markSize = 96.0;

  @override
  Widget build(BuildContext context) {
    return SizedBox.square(
      dimension: _markSize,
      child: Stack(
        fit: StackFit.expand,
        children: [
          // 头：fade + scale
          Opacity(
            opacity: headT,
            child: Transform.scale(
              scale: 0.92 + 0.08 * headT,
              child: const BiuMark(
                size: _markSize,
                inkColor: _kInk,
                circuitColor: _kPurple,
                circuitOpacity: 0,
                handOpacity: 0,
              ),
            ),
          ),
          // 电路：ClipRect 从下往上揭开
          ClipRect(
            clipper: _BottomUpClipper(circuitT),
            child: const BiuMark(
              size: _markSize,
              inkColor: _kInk,
              circuitColor: _kPurple,
              headOpacity: 0,
              handOpacity: 0,
            ),
          ),
          // 手：fade + slide (overshoot)
          Opacity(
            opacity: handT.clamp(0.0, 1.0),
            child: Transform.translate(
              offset: Offset(10 * (1.0 - handT), 8 * (1.0 - handT)),
              child: const BiuMark(
                size: _markSize,
                inkColor: _kInk,
                circuitColor: _kPurple,
                headOpacity: 0,
                circuitOpacity: 0,
              ),
            ),
          ),
        ],
      ),
    );
  }
}

class _BottomUpClipper extends CustomClipper<Rect> {
  _BottomUpClipper(this.t);
  final double t;

  @override
  Rect getClip(Size size) {
    final visibleH = size.height * t.clamp(0.0, 1.0);
    return Rect.fromLTWH(0, size.height - visibleH, size.width, visibleH);
  }

  @override
  bool shouldReclip(covariant _BottomUpClipper old) => old.t != t;
}

// ─────────────────────────────────────────────────────
// 装饰：光晕呼吸 + 思考波纹
// ─────────────────────────────────────────────────────

class _Halo extends StatelessWidget {
  const _Halo({required this.progress});
  final double progress;

  @override
  Widget build(BuildContext context) {
    final phase = progress * math.pi * 3;
    final scale = 0.85 + 0.15 * (math.sin(phase) * 0.5 + 0.5);
    final opacity = 0.40 + 0.25 * (math.sin(phase + math.pi / 3) * 0.5 + 0.5);
    return Opacity(
      opacity: opacity,
      child: Transform.scale(
        scale: scale,
        child: Container(
          width: 160,
          height: 160,
          decoration: const BoxDecoration(
            shape: BoxShape.circle,
            gradient: RadialGradient(
              colors: [Color(0xFFEEEDFB), Color(0x00FAFAFA)], // theme-ignore: legacy splash 光晕
              stops: [0.0, 0.85],
            ),
          ),
        ),
      ),
    );
  }
}

class _ThinkRing extends StatelessWidget {
  const _ThinkRing({required this.progress, required this.start, required this.end});
  final double progress;
  final double start;
  final double end;

  @override
  Widget build(BuildContext context) {
    final t = ((progress - start) / (end - start)).clamp(0.0, 1.0);
    if (t <= 0 || t >= 1) return const SizedBox.shrink();
    final scale = 0.5 + 1.45 * t;
    final opacity = 0.45 * (1.0 - t);
    return IgnorePointer(
      child: Opacity(
        opacity: opacity,
        child: Transform.scale(
          scale: scale,
          child: Container(
            width: 110,
            height: 110,
            decoration: BoxDecoration(
              shape: BoxShape.circle,
              border: Border.all(color: _kPurple, width: 1.4),
            ),
          ),
        ),
      ),
    );
  }
}

// ─────────────────────────────────────────────────────
// 文字 "Biu" 黑 + "Mind" 紫
// ─────────────────────────────────────────────────────

class _BiuMindText extends StatelessWidget {
  const _BiuMindText();

  @override
  Widget build(BuildContext context) {
    return const Text.rich(
      TextSpan(
        style: TextStyle(
          fontSize: 22,
          fontWeight: FontWeight.w700,
          letterSpacing: -0.6,
          height: 1.0,
        ),
        children: [
          TextSpan(text: 'Biu', style: TextStyle(color: _kInk)),
          TextSpan(text: 'Mind', style: TextStyle(color: _kPurple)),
        ],
      ),
    );
  }
}

// WindowDragArea —— macOS 全尺寸内容窗口的拖拽区。
//
// MainFlutterWindow (macOS Runner) 开了 fullSizeContentView + 透明隐藏
// 标题栏，内容延伸进原标题栏区域；原生标题栏不再提供拖拽，故顶部窗口条
// 需要自绘拖拽行为：
//   - 按住拖动 → 移动窗口（channel `biumind/window` -> NSWindow.performDrag）
//   - 双击     → 缩放窗口（等价点绿色红绿灯）
//
// 非 macOS 平台透传 child —— Windows/Linux 用原生标题栏，不需要自绘拖拽。
//
// 注意：内部 GestureDetector 只注册 pan / doubleTap，不注册 tap，
// 所以 child 里的 IconButton 等点击不受影响。

import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

class WindowDragArea extends StatelessWidget {
  const WindowDragArea({required this.child, super.key});

  final Widget child;

  static const _channel = MethodChannel('biumind/window');

  static bool get _enabled =>
      !kIsWeb && defaultTargetPlatform == TargetPlatform.macOS;

  @override
  Widget build(BuildContext context) {
    if (!_enabled) return child;
    return GestureDetector(
      behavior: HitTestBehavior.translucent,
      onPanStart: (_) => _channel.invokeMethod<void>('drag'),
      onDoubleTap: () => _channel.invokeMethod<void>('zoom'),
      child: child,
    );
  }
}

/// macOS 红绿灯在窗口顶部区域的几何 (左上原点, logical px)。
///
/// 由 MainFlutterWindow 的 `biumind/window` channel `trafficLights` 方法
/// 实测返回 — 不同 macOS 版本 / 窗口配置下按钮位置会变, 不要在 Flutter
/// 侧写死。非 macOS / 查询失败返回 null, 调用方走默认值。
class TrafficLightMetrics {
  const TrafficLightMetrics({required this.centerY, required this.right});

  /// 红绿灯垂直中心距窗口顶 (实测 macOS 15 上 ≈16)。
  final double centerY;

  /// 绿灯 (最右一颗) 右缘距窗口左缘。
  final double right;

  static Future<TrafficLightMetrics?> query() async {
    if (kIsWeb || defaultTargetPlatform != TargetPlatform.macOS) return null;
    try {
      final r = await WindowDragArea._channel
          .invokeMethod<Map<dynamic, dynamic>>('trafficLights');
      if (r == null) return null;
      final cy = (r['centerY'] as num?)?.toDouble();
      final right = (r['right'] as num?)?.toDouble();
      if (cy == null || right == null) return null;
      return TrafficLightMetrics(centerY: cy, right: right);
    } catch (_) {
      return null; // channel 未就绪 (热重载 / 非 Runner 环境) — 走默认
    }
  }
}

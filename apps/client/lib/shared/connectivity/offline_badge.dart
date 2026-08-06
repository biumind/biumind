// OfflineBadge — 在 AppShell 顶层悬浮显示连通状态。
//
// 状态 (token_manager.dart connectivityStateProvider):
//   online           — 不显示 (零 noise)
//   reconnecting     — 黄色 chip "重连中..." + 旋转图标
//   offlineWithCache — 灰色 chip "离线 — 历史可读"
//
// 设计:
//   - 用 Material chip 风格,圆角 + 浅色背景, 不夺主页面注意力
//   - 鼠标 hover 显示 tooltip 解释为什么 + 怎么办
//   - Positioned 浮在右上角,不挤压正文布局
//   - online 时返 SizedBox.shrink() 完全消失
//
// 详见 BiuMind-Identity-Session-Design §3.5 离线 grace。

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../app/theme.dart';
import '../../services/token_manager.dart';

class OfflineBadge extends ConsumerWidget {
  const OfflineBadge({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final state = ref.watch(connectivityStateProvider);
    if (state == ConnectivityState.online) return const SizedBox.shrink();

    final theme = Theme.of(context);
    final isReconnecting = state == ConnectivityState.reconnecting;
    final bg = isReconnecting
        ? WarningCallout.bg
        : theme.colorScheme.surfaceContainerHighest;
    final fg = isReconnecting
        ? WarningCallout.textFg
        : theme.colorScheme.onSurfaceVariant;
    final label = isReconnecting ? '重连中...' : '离线 — 历史可读';
    final tooltip = isReconnecting
        ? '与服务器的连接暂时中断,后台正在重试。当前会话仍可正常使用。'
        : '与服务器断开连接超过 access token 有效期。'
            '历史内容仍可查看,发送/上传等写操作暂不可用,'
            '网络恢复后会自动重连。';

    return Tooltip(
      message: tooltip,
      waitDuration: const Duration(milliseconds: 300),
      child: Container(
        padding: const EdgeInsets.symmetric(
            horizontal: BiuTokens.space3, vertical: BiuTokens.space2),
        decoration: BoxDecoration(
          color: bg,
          borderRadius: BorderRadius.circular(999),
          border: Border.all(
            color: theme.colorScheme.outlineVariant.withValues(alpha: 0.5),
          ),
        ),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            if (isReconnecting)
              SizedBox(
                width: 12,
                height: 12,
                child: CircularProgressIndicator(
                  strokeWidth: 1.6,
                  color: fg,
                ),
              )
            else
              Icon(Icons.cloud_off_outlined, size: 14, color: fg),
            const SizedBox(width: BiuTokens.space2),
            Text(
              label,
              style: theme.textTheme.labelSmall?.copyWith(
                color: fg,
                fontWeight: FontWeight.w600,
              ),
            ),
          ],
        ),
      ),
    );
  }
}

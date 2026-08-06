// ConnectionBanner — 创作模块顶端「连接状态」横条.
//
// 状态:
//   sseLive          → 不渲染 (高度 0)
//   pollingFallback  → 黄色 "实时通道断开, 30s 轮询兜底中"
//   offline          → 红色 "网络异常, 作品列表可能不是最新"
//   aigcUri 未配     → 紫色 "未配置 AIGC 服务地址, 请到设置 → 服务" (高优先级)

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../../app/theme.dart';
import '../../../../services/auth_service.dart';
import '../../../settings/application/settings_controller.dart';
import '../../application/tasks_controller.dart' as tc;
import '../../application/tasks_controller.dart' show tasksControllerProvider;

class ConnectionBanner extends ConsumerWidget {
  const ConnectionBanner({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final settings = ref.watch(settingsControllerProvider).valueOrNull;
    final creds = ref.watch(hubCredentialsProvider);

    // 未登录 / 未配 aigcUri (登录后但 settings 没填) → 配置提示优先.
    if (creds != null && settings != null && settings.aigcUri == null) {
      return _BannerBar(
        icon: Icons.settings_outlined,
        message: '未配置 AIGC 服务地址 — 创作功能不可用',
        actionLabel: '去设置',
        onAction: () => context.go('/settings'),
        bg: BiuTokens.purpleSoft,
        fg: BiuTokens.purple,
      );
    }

    // 未登录: 提示登录入口
    if (creds == null) {
      return _BannerBar(
        icon: Icons.login,
        message: '登录后即可创作 / 查看作品',
        actionLabel: '登录',
        onAction: () => context.go('/settings'),
        bg: BiuTokens.purpleSoft,
        fg: BiuTokens.purple,
      );
    }

    final state = ref.watch(tasksControllerProvider);
    switch (state.connection) {
      case tc.ConnectionState.offline:
        return _BannerBar(
          icon: Icons.cloud_off,
          message: '网络异常 — 作品列表可能不是最新',
          bg: BiuTokens.errorSoft,
          fg: BiuTokens.error,
        );
      case tc.ConnectionState.pollingFallback:
        return const _BannerBar(
          icon: Icons.sync,
          message: '实时通道断开, 30s 轮询兜底中 — 进度可能略有延迟',
          bg: WarningCallout.bg,
          fg: WarningCallout.textFg,
        );
      case tc.ConnectionState.sseLive:
      case tc.ConnectionState.idle:
        return const SizedBox.shrink();
    }
  }
}

class _BannerBar extends StatelessWidget {
  const _BannerBar({
    required this.icon,
    required this.message,
    required this.bg,
    required this.fg,
    this.actionLabel,
    this.onAction,
  });

  final IconData icon;
  final String message;
  final Color bg;
  final Color fg;
  final String? actionLabel;
  final VoidCallback? onAction;

  @override
  Widget build(BuildContext context) {
    return Container(
      width: double.infinity,
      color: bg,
      padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
      child: Row(
        children: [
          Icon(icon, size: 14, color: fg),
          const SizedBox(width: 8),
          Expanded(
            child: Text(
              message,
              style: TextStyle(
                fontSize: 12,
                color: fg,
                fontWeight: FontWeight.w500,
              ),
            ),
          ),
          if (actionLabel != null && onAction != null)
            TextButton(
              onPressed: onAction,
              style: TextButton.styleFrom(
                foregroundColor: fg,
                padding: const EdgeInsets.symmetric(horizontal: 8),
                minimumSize: const Size(0, 28),
                tapTargetSize: MaterialTapTargetSize.shrinkWrap,
              ),
              child: Text(
                actionLabel!,
                style: const TextStyle(
                  fontWeight: FontWeight.w600,
                  fontSize: 12,
                ),
              ),
            ),
        ],
      ),
    );
  }
}

// sign_out_dialog — 退出登录二次确认 (移动「我的」+ 桌面 sidebar footer +
// 设置页 三处入口共用).
//
// 确认 → settingsController.signOut (内含 purgeUserData: 清本地缓存 DAO +
// 断连接 + invalidate provider; 编码项目保留) → go /login. 取消无操作.
//
// 抽公共: 三处入口文案 / 行为统一, 改一处即三处同步; 避免 dialog 模板散落.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../app/theme.dart';
import '../application/settings_controller.dart';

/// 退出登录二次确认 dialog. 确认后 signOut (清本地缓存, 编码项目保留) +
/// 跳 /login; 取消无操作.
Future<void> confirmAndSignOut(BuildContext context, WidgetRef ref) async {
  final ok = await showDialog<bool>(
    context: context,
    builder: (ctx) => AlertDialog(
      title: const Text('退出登录'),
      content: const Text('确定退出当前账号?本地缓存(知识库 / 创作 / 订阅)会清除,编码项目保留。'),
      actions: [
        TextButton(
          onPressed: () => Navigator.of(ctx).pop(false),
          child: const Text('取消'),
        ),
        FilledButton(
          onPressed: () => Navigator.of(ctx).pop(true),
          style: FilledButton.styleFrom(backgroundColor: BiuTokens.error),
          child: const Text('退出'),
        ),
      ],
    ),
  );
  if (ok != true) return;
  await ref.read(settingsControllerProvider.notifier).signOut();
  if (context.mounted) context.go('/login');
}

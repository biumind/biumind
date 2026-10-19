// auto 权限模式首次启用 opt-in 确认（设计
// docs/BiuMind-Auto-Permission-Mode-Design.md §6 opt-in 段、§7-3）。
//
// 标记存 ChatPreferences.autoModeOptedIn（SharedPreferences，客户端本地
// 用户级）——与服务端 Go 侧 user 层 settings.json 的
// permissions.autoModeOptIn 语义对齐；**不写 Drift、不写项目级配置**
// （防恶意仓库经 .biu/config.toml 偷开 auto）。
//
// 入口统一走 [confirmAutoModeOptIn]：chat composer 权限 chip、code 工作
// 台新任务下拉（项目默认权限档在保存时过同一 guard）、设置页「权限模式
// 默认值」。plan / manual 不弹。

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../l10n/app_localizations.dart';
import 'chat_preferences.dart';

/// 切到 auto 模式前的首次启用确认。
///
/// 已确认过 → 直接 true（不弹窗）；未确认 → 弹确认对话框，用户点「启用
/// 自动执行」则写标记并返回 true，取消 /  dismiss 返回 false（调用方放弃
/// 切换，chip / 下拉保持原模式显示）。
Future<bool> confirmAutoModeOptIn(BuildContext context, WidgetRef ref) async {
  if (ref.read(chatPreferencesProvider).autoModeOptedIn) return true;
  final l = AppLocalizations.of(context)!;
  final ok = await showDialog<bool>(
    context: context,
    builder: (ctx) => AlertDialog(
      title: Text(l.permissionAutoOptInTitle),
      content: Text(l.permissionAutoOptInBody),
      actions: [
        TextButton(
          onPressed: () => Navigator.of(ctx).pop(false),
          child: Text(l.commonCancel),
        ),
        FilledButton(
          onPressed: () => Navigator.of(ctx).pop(true),
          child: Text(l.permissionAutoOptInConfirm),
        ),
      ],
    ),
  );
  if (ok != true) return false;
  await ref.read(chatPreferencesProvider.notifier).setAutoModeOptedIn(true);
  return true;
}

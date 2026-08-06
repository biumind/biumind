// UpdateBanner — 检测到客户端有新版本时, 主 shell 顶部显示紫色提示 banner,
// 引导用户前往官网下载页。
//
// 设计 (见 docs/BiuMind-Client-Release-Manifest.md):
//   - 数据源: GET <origin>/downloads/releases.json (单 origin, 经 site nginx)
//   - 触发: manifest.version > PackageInfo.version 且 channel=stable
//   - dismissable 但不持久化: 重启 app 重新评估 (避免用户 dismiss 后长期错过更新)
//   - 网络错: 静默 — 不能因为拉失败吓用户
//   - 本轮只做"更新检测"提示, 不做静默自动更新 (Sparkle/appcast 后续阶段)

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:url_launcher/url_launcher.dart';

import '../../../app/theme.dart';
import '../../../features/update/application/update_check_controller.dart';

/// 用户点 dismiss 后的内存 flag — 仅当前会话有效, 重启 app 重新评估。
final _dismissedProvider = StateProvider<bool>((_) => false);

class UpdateBanner extends ConsumerWidget {
  const UpdateBanner({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final dismissed = ref.watch(_dismissedProvider);
    if (dismissed) return const SizedBox.shrink();
    final asyncUpdate = ref.watch(updateAvailableProvider);
    final update = asyncUpdate.valueOrNull;
    if (update == null) return const SizedBox.shrink();

    final theme = Theme.of(context);
    return Material(
      color: BiuTokens.purpleSoft,
      child: Container(
        padding: const EdgeInsets.symmetric(
            horizontal: BiuTokens.space5, vertical: BiuTokens.space3),
        decoration: BoxDecoration(
          border: Border(
            bottom: BorderSide(color: BiuTokens.purple, width: 1),
          ),
        ),
        child: Row(
          children: [
            Icon(Icons.system_update,
                color: BiuTokens.purple, size: 20),
            const SizedBox(width: BiuTokens.space3),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    '发现新版本 v${update.targetVersion}',
                    style: theme.textTheme.titleSmall?.copyWith(
                      color: BiuTokens.purple,
                      fontWeight: FontWeight.w700,
                    ),
                  ),
                  if (update.notes.isNotEmpty) ...[
                    const SizedBox(height: 2),
                    Text(
                      update.notes,
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                      style: theme.textTheme.bodySmall?.copyWith(
                        color: BiuTokens.textSecondary,
                      ),
                    ),
                  ],
                ],
              ),
            ),
            const SizedBox(width: BiuTokens.space3),
            TextButton(
              onPressed: () => _openDownloadPage(update.downloadPageUrl),
              style: TextButton.styleFrom(
                foregroundColor: BiuTokens.purple,
              ),
              child: const Text('前往下载'),
            ),
            IconButton(
              tooltip: '本次会话内不再提醒',
              icon: const Icon(Icons.close, size: 18),
              color: BiuTokens.textSecondary,
              onPressed: () =>
                  ref.read(_dismissedProvider.notifier).state = true,
            ),
          ],
        ),
      ),
    );
  }

  Future<void> _openDownloadPage(String url) async {
    final uri = Uri.tryParse(url);
    if (uri == null) return;
    await launchUrl(uri, mode: LaunchMode.externalApplication);
  }
}

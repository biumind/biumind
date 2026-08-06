// SecurityAlertBanner — 检测最近 24h 内是否有 refresh_token_reuse
// 安全事件,有则在主 shell 顶部显示红色警告 banner,引导用户改密。
//
// 决策点 (BiuMind-Identity-Session-Design B2-c):
//   - 数据源: GET /v1/identity/me/security-events 服务端已提供
//   - 触发窗口: 24h (reuse 事件最有价值的 actionable 窗口)
//   - dismissable 但**不持久化**: 每次 app 启动 / 顶层重建都重新拉一次,
//     避免用户 dismiss 后继续被攻击的盲区。如果用户改了密,A3 整族
//     已经撤,新 session 不会再触发新事件,banner 自然消失。
//   - 网络错: 静默 — 不能因为拉失败也吓用户

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../app/theme.dart';
import '../../data/api/identity_client.dart';
import '../../features/settings/application/settings_controller.dart';

/// 最近 24h 内有 refresh_token_reuse 事件 → true。
/// 网络错 / 未登录 / 无 events → false (静默)。
final recentReuseEventProvider = FutureProvider.autoDispose<bool>((ref) async {
  final settings = ref.watch(settingsControllerProvider).valueOrNull;
  if (settings == null) return false;
  final url = (settings.identityUrl ?? '').trim();
  final token = (settings.accessToken ?? '').trim();
  if (url.isEmpty || token.isEmpty) return false;

  final client = IdentityClient(Uri.parse(url));
  try {
    final events = await client.listSecurityEvents(token, limit: 20);
    final cutoff =
        DateTime.now().toUtc().subtract(const Duration(hours: 24));
    for (final e in events) {
      if (e['kind'] != 'refresh_token_reuse') continue;
      final raw = e['created_at'];
      if (raw is! String) continue;
      final ts = DateTime.tryParse(raw);
      if (ts == null) continue;
      if (ts.toUtc().isAfter(cutoff)) return true;
    }
    return false;
  } catch (_) {
    return false; // 网络错不吓用户
  }
});

/// 用户点 dismiss 后的内存 flag — 仅当前会话有效, 重启 app 重新评估。
final _dismissedProvider = StateProvider<bool>((_) => false);

class SecurityAlertBanner extends ConsumerWidget {
  const SecurityAlertBanner({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final dismissed = ref.watch(_dismissedProvider);
    if (dismissed) return const SizedBox.shrink();
    final asyncHas = ref.watch(recentReuseEventProvider);
    final hasReuse = asyncHas.valueOrNull ?? false;
    if (!hasReuse) return const SizedBox.shrink();

    final theme = Theme.of(context);
    return Material(
      color: BiuTokens.errorSoft,
      child: InkWell(
        onTap: () => _showDetailSheet(context, ref),
        child: Container(
          padding: const EdgeInsets.symmetric(
              horizontal: BiuTokens.space5, vertical: BiuTokens.space3),
          decoration: const BoxDecoration(
            border: Border(
              bottom: BorderSide(color: BiuTokens.error, width: 1),
            ),
          ),
          child: Row(
            children: [
              const Icon(Icons.warning_amber_rounded,
                  color: BiuTokens.error, size: 20),
              const SizedBox(width: BiuTokens.space3),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      '检测到您账号有可疑访问',
                      style: theme.textTheme.titleSmall?.copyWith(
                        color: BiuTokens.error,
                        fontWeight: FontWeight.w700,
                      ),
                    ),
                    const SizedBox(height: 2),
                    Text(
                      '最近 24 小时内有失效凭证被重新使用 — 可能 token 已泄漏。'
                      '建议立即修改密码,所有其他设备会自动登出。',
                      style: theme.textTheme.bodySmall?.copyWith(
                        color: BiuTokens.textSecondary,
                      ),
                    ),
                  ],
                ),
              ),
              const SizedBox(width: BiuTokens.space3),
              TextButton(
                onPressed: () => _showDetailSheet(context, ref),
                style: TextButton.styleFrom(
                  foregroundColor: BiuTokens.error,
                ),
                child: const Text('查看详情'),
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
      ),
    );
  }

  void _showDetailSheet(BuildContext ctx, WidgetRef ref) {
    showModalBottomSheet<void>(
      context: ctx,
      builder: (sheetCtx) => Padding(
        padding: const EdgeInsets.all(BiuTokens.space5),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: const [
                Icon(Icons.warning_amber_rounded, color: BiuTokens.error),
                SizedBox(width: BiuTokens.space2),
                Text('账号安全建议',
                    style: TextStyle(
                        fontSize: 18, fontWeight: FontWeight.w700)),
              ],
            ),
            const SizedBox(height: BiuTokens.space4),
            const Text(
              '我们检测到您的账号在最近 24 小时内有失效的登录凭证被重新使用,'
              '这通常意味着该凭证可能曾被复制或泄漏。\n\n'
              '为保险起见,我们已经撤销所有相关会话。建议您:',
              style: TextStyle(fontSize: 14, height: 1.6),
            ),
            const SizedBox(height: BiuTokens.space3),
            const Text('1. 立即修改密码(Settings → 登录 / 安全)\n'
                '2. 检查 Settings → 已登录设备,撤销不熟悉的设备\n'
                '3. 如频繁出现,联系平台管理员',
                style: TextStyle(fontSize: 13, height: 1.7)),
            const SizedBox(height: BiuTokens.space5),
            Align(
              alignment: Alignment.centerRight,
              child: FilledButton(
                onPressed: () => Navigator.of(sheetCtx).pop(),
                child: const Text('我知道了'),
              ),
            ),
          ],
        ),
      ),
    );
  }
}

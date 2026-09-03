// DocprocPane — 全局设置页「通用 > 文档处理」tab（P2 W3，设计文档
// BiuMind-Client-Docproc-Design §3.4）。
//
// 承载文档处理位置三态（SharedPreferences `biu.wiki.docproc` 经
// docprocPreferencesProvider）：
//   * 自动（默认）—— §3.4 矩阵：小文件本机（免费），大文件云端
//   * 优先本机 —— ≤ 队列字节上限都本机（免费）
//   * 优先云端 —— 全部云端解析（按页花积分）
//
// 判定本身收敛在 docproc_preferences.dart 的 docprocShouldParseLocally
// （纯函数），队列 enqueue 时快照到 item；本 pane 只负责读写设置。
// hasLocalDocproc=false 的平台（Windows/Linux）禁用「优先本机」并提示。
//
// 末尾另挂「Wiki 生成模型」区块（B2）：用户级服务端偏好（identity
// /v1/identity/me/settings/ingest-model，ingestModelProvider），云端 worker
// 生成 Wiki 页时读取，跨端同步。

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../app/theme.dart';
import '../../../core/platform/platform_caps.dart';
import '../../../l10n/app_localizations.dart';
import '../../chat/data/chat_model_groups.dart';
import '../../wiki/application/docproc_preferences.dart';
import '../application/wiki_settings_providers.dart';

class DocprocPane extends ConsumerWidget {
  const DocprocPane({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final theme = Theme.of(context);
    final t = AppLocalizations.of(context)!;
    final caps = ref.watch(platformCapsProvider);
    final prefs = ref.watch(docprocPreferencesProvider);
    final notifier = ref.read(docprocPreferencesProvider.notifier);

    Widget option(
      DocprocProcessLocation value,
      String label,
      String desc, {
      bool enabled = true,
    }) {
      return RadioListTile<DocprocProcessLocation>(
        value: value,
        enabled: enabled,
        title: Text(label, style: theme.textTheme.bodyMedium),
        subtitle: Text(desc, style: theme.textTheme.bodySmall),
        dense: true,
        contentPadding: EdgeInsets.zero,
      );
    }

    return Center(
      child: ConstrainedBox(
        constraints: const BoxConstraints(maxWidth: 720),
        child: ListView(
          padding: const EdgeInsets.symmetric(
              horizontal: BiuTokens.space5, vertical: BiuTokens.space6),
          children: [
            Text(t.settingsNavDocproc, style: theme.textTheme.headlineLarge),
            const SizedBox(height: BiuTokens.space1),
            Text(t.settingsDocprocSubtitle, style: theme.textTheme.bodySmall),
            const SizedBox(height: BiuTokens.space5),

            RadioGroup<DocprocProcessLocation>(
              groupValue: prefs.location,
              onChanged: (v) {
                if (v != null) notifier.setLocation(v);
              },
              child: Column(
                children: [
                  option(
                    DocprocProcessLocation.auto,
                    t.settingsDocprocAuto,
                    t.settingsDocprocAutoDesc,
                  ),
                  option(
                    DocprocProcessLocation.preferLocal,
                    t.settingsDocprocPreferLocal,
                    t.settingsDocprocPreferLocalDesc,
                    enabled: caps.hasLocalDocproc,
                  ),
                  option(
                    DocprocProcessLocation.preferCloud,
                    t.settingsDocprocPreferCloud,
                    t.settingsDocprocPreferCloudDesc,
                  ),
                ],
              ),
            ),
            if (!caps.hasLocalDocproc) ...[
              const SizedBox(height: BiuTokens.space2),
              Text(
                t.settingsDocprocUnsupported,
                style: theme.textTheme.bodySmall
                    ?.copyWith(color: theme.colorScheme.error),
              ),
            ],
            const SizedBox(height: BiuTokens.space4),
            Text(
              t.settingsDocprocNote,
              style: theme.textTheme.bodySmall
                  ?.copyWith(color: BiuTokens.textMuted),
            ),

            // ── Wiki 生成模型（B2，服务端偏好，跨端同步）─────────
            const SizedBox(height: BiuTokens.space5),
            Divider(height: 1, color: BiuTokens.borderSubtle),
            const SizedBox(height: BiuTokens.space4),
            const _WikiIngestModelSection(),
          ],
        ),
      ),
    );
  }
}

/// _WikiIngestModelSection —— 「Wiki 生成模型」服务端偏好（B2）。
///
/// 偏好存 identity（ingestModelProvider → WikiSettingsClient），云端 worker
/// 生成 Wiki 页时读取，跨端同步；不能用 SharedPreferences。下拉选项 =
/// chatModelGroupsProvider 的 official 组（平台官方 chat 模型）+ 一项
/// 「跟随平台默认」（null = 不设置偏好，语义同 chat 设置的"BiuMind 默认"）。
/// 切换即 PUT；失败 SnackBar 反馈，状态回滚由 notifier 保证（PUT 失败不
/// 改本地状态）。
class _WikiIngestModelSection extends ConsumerWidget {
  const _WikiIngestModelSection();

  Future<void> _save(BuildContext context, WidgetRef ref, String? model) async {
    final t = AppLocalizations.of(context)!;
    final messenger = ScaffoldMessenger.of(context);
    try {
      await ref.read(ingestModelProvider.notifier).setModel(model);
    } catch (_) {
      messenger.showSnackBar(
        SnackBar(content: Text(t.settingsDocprocIngestModelSaveFailed)),
      );
    }
  }

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final theme = Theme.of(context);
    final t = AppLocalizations.of(context)!;
    final setting = ref.watch(ingestModelProvider);
    final groupsAsync = ref.watch(chatModelGroupsProvider);

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(t.settingsDocprocIngestModelTitle,
            style: theme.textTheme.titleSmall),
        const SizedBox(height: BiuTokens.space1),
        Text(
          t.settingsDocprocIngestModelDesc,
          style: theme.textTheme.bodySmall
              ?.copyWith(color: BiuTokens.textMuted),
        ),
        const SizedBox(height: BiuTokens.space3),
        if (setting.hasError)
          Text(
            '—',
            style: theme.textTheme.bodySmall
                ?.copyWith(color: theme.colorScheme.error),
          )
        else
          groupsAsync.when(
            loading: () => const LinearProgressIndicator(minHeight: 2),
            error: (_, _) => Text(
              '—',
              style: theme.textTheme.bodySmall
                  ?.copyWith(color: theme.colorScheme.error),
            ),
            data: (groups) {
              // 仅平台官方 chat 模型（official 组）。
              final models = [
                for (final g in groups.where((g) => g.isOfficial))
                  ...g.models,
              ];
              final codes = {for (final m in models) m.code};
              // 当前值不在 official 清单（如下线）时回退 null 显示「跟随平台
              // 默认」，避免 DropdownButton 值不匹配断言 —— 同
              // chat_settings_pane 的默认模型处理。
              final current = setting.valueOrNull;
              final value =
                  (current != null && codes.contains(current)) ? current : null;
              return DropdownButton<String?>(
                value: value,
                isExpanded: true,
                hint: Text(t.settingsDocprocIngestModelDefault),
                items: <DropdownMenuItem<String?>>[
                  DropdownMenuItem(
                    value: null,
                    child: Text(t.settingsDocprocIngestModelDefault),
                  ),
                  for (final m in models)
                    DropdownMenuItem(
                      value: m.code,
                      child: Text(m.displayName,
                          overflow: TextOverflow.ellipsis),
                    ),
                ],
                onChanged: setting.isLoading
                    ? null
                    : (v) => _save(context, ref, v),
              );
            },
          ),
      ],
    );
  }
}

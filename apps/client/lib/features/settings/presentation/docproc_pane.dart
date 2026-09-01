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

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../app/theme.dart';
import '../../../core/platform/platform_caps.dart';
import '../../../l10n/app_localizations.dart';
import '../../wiki/application/docproc_preferences.dart';

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
          ],
        ),
      ),
    );
  }
}

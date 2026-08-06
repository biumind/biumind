// SearchSettingsPane — 搜索相关设置（N3）。
//
// 目前只有一项:「在统一搜索中包含笔记」。开关存 AppSettings
// .searchIncludeNotes（默认关，本地持久化）；开启后搜索页
// （features/search）的 POST /v1/search 请求带 include_notes=true，
// 响应附带的 notes 分组渲染在结果里。
//
// 与笔记模块中栏的笔记内搜索（GET /v1/notes/search，N2）是两回事，
// 这里不影响它。

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../app/theme.dart';
import '../application/settings_controller.dart';

class SearchSettingsPane extends ConsumerWidget {
  const SearchSettingsPane({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final settings = ref.watch(settingsControllerProvider).valueOrNull;
    final includeNotes = settings?.searchIncludeNotes ?? false;
    return Center(
      child: ConstrainedBox(
        constraints: const BoxConstraints(maxWidth: 720),
        child: ListView(
          padding: const EdgeInsets.symmetric(
              horizontal: BiuTokens.space5, vertical: BiuTokens.space6),
          children: [
            Text('搜索', style: Theme.of(context).textTheme.headlineLarge),
            const SizedBox(height: BiuTokens.space1),
            Text('统一搜索的范围与结果',
                style: Theme.of(context).textTheme.bodySmall),
            const SizedBox(height: BiuTokens.space5),
            SwitchListTile(
              contentPadding: EdgeInsets.zero,
              title: const Text('在统一搜索中包含笔记',
                  style: TextStyle(fontSize: 14)),
              subtitle: Text(
                '开启后，搜索页结果会附带匹配的笔记（标题 + 摘要）',
                style: TextStyle(fontSize: 12, color: BiuTokens.textMuted),
              ),
              value: includeNotes,
              onChanged: (v) => ref
                  .read(settingsControllerProvider.notifier)
                  .updateSearchIncludeNotes(v),
            ),
          ],
        ),
      ),
    );
  }
}

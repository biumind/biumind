// ModelDefaultBadge —— "默认"徽标 (平台默认 chat 模型, admin 经
// model-relay is_default_chat 设置, 全局唯一)。
//
// 三个消费点共用: model_picker_dialog 行内徽标 / chat_settings_pane
// 默认模型下拉 / new_thread_dialog 模型下拉。颜色走 theme.colorScheme,
// 不引入自定义 Color 字面量 (T1)。

import 'package:flutter/material.dart';

import '../../../../l10n/app_localizations.dart';

class ModelDefaultBadge extends StatelessWidget {
  const ModelDefaultBadge({super.key});

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 5, vertical: 1),
      decoration: BoxDecoration(
        borderRadius: BorderRadius.circular(4),
        border: Border.all(
          color: theme.colorScheme.primary.withValues(alpha: 0.5),
        ),
      ),
      child: Text(
        AppLocalizations.of(context)!.chatV2ModelPickerDefaultBadge,
        style: theme.textTheme.labelSmall?.copyWith(
          color: theme.colorScheme.primary,
          fontSize: 10,
        ),
      ),
    );
  }
}

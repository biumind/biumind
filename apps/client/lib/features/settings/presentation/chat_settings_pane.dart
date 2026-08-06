// ChatSettingsPane — 全局设置页「智能体 > 聊天」tab。
//
// 承载聊天专属偏好(SharedPreferences `biu.chat.prefs` 经 chatPreferencesProvider):
//   * 默认模式 chat / agent / task —— 新对话出厂选中哪个(出厂 = agent)
//   * 默认模型 —— 新会话默认走的模型(空 = BiuMind 官方默认);新会话创建
//     (createDefaultThread)读的就是这一处,是默认模型的单一真相源
//   * 自动改名 —— 首条 user 消息后自动用 prompt 推标题
//
// 原本这些散落在 palette-only 的 ChatSettingsDialogV2(没有可见入口);v? 收口到
// 全局设置页,语言/字号等 app 级项另归「通用 > 外观」。

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../app/theme.dart';
import '../../../l10n/app_localizations.dart';
import '../../chat/application/chat_controller.dart';
import '../../chat/application/chat_preferences.dart';
import '../../chat/domain/chat_models.dart';

class ChatSettingsPane extends ConsumerWidget {
  const ChatSettingsPane({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final theme = Theme.of(context);
    final t = AppLocalizations.of(context)!;
    final prefs = ref.watch(chatPreferencesProvider);
    final notifier = ref.read(chatPreferencesProvider.notifier);
    final modelsAsync = ref.watch(availableChatModelsProvider);

    return Center(
      child: ConstrainedBox(
        constraints: const BoxConstraints(maxWidth: 720),
        child: ListView(
          padding: const EdgeInsets.symmetric(
              horizontal: BiuTokens.space5, vertical: BiuTokens.space6),
          children: [
            Text(t.settingsNavChat, style: theme.textTheme.headlineLarge),
            const SizedBox(height: BiuTokens.space1),
            Text(t.settingsChatDefaultsSectionSubtitle,
                style: theme.textTheme.bodySmall),
            const SizedBox(height: BiuTokens.space5),

            // ── 默认模式 ──────────────────────────────────────
            _SectionCard(
              title: t.chatV2SettingsDefaultMode,
              child: SegmentedButton<ThreadMode>(
                segments: const [
                  ButtonSegment(
                    value: ThreadMode.chat,
                    label: Text('Chat'),
                    icon: Icon(Icons.chat_bubble_outline, size: 14),
                  ),
                  ButtonSegment(
                    value: ThreadMode.agent,
                    label: Text('Agent'),
                    icon: Icon(Icons.smart_toy_outlined, size: 14),
                  ),
                  ButtonSegment(
                    value: ThreadMode.task,
                    label: Text('Task'),
                    icon: Icon(Icons.task_outlined, size: 14),
                  ),
                ],
                selected: {prefs.defaultMode},
                onSelectionChanged: (s) => notifier.setDefaultMode(s.first),
              ),
            ),
            const SizedBox(height: BiuTokens.space4),

            // ── 默认模型 ──────────────────────────────────────
            _SectionCard(
              title: t.chatV2SettingsDefaultModel,
              child: modelsAsync.when(
                loading: () => const LinearProgressIndicator(minHeight: 2),
                error: (_, _) => Text(
                  '—',
                  style: theme.textTheme.bodySmall
                      ?.copyWith(color: theme.colorScheme.error),
                ),
                data: (models) {
                  final byKey = {for (final m in models) m.routeKey: m};
                  // 当前默认 → routeKey(null = BiuMind 默认)。优先精确
                  // (code,providerId) 匹配;老 prefs 无 providerId 时退而匹配同
                  // code 第一项;找不到回退 null,避免 dropdown 值不匹配抛异常。
                  String? currentKey;
                  if (prefs.defaultModel != null) {
                    for (final m in models) {
                      if (m.code == prefs.defaultModel) {
                        currentKey = m.routeKey;
                        if (m.providerId == prefs.defaultProviderId) break;
                      }
                    }
                  }
                  return DropdownButton<String?>(
                    value: currentKey,
                    isExpanded: true,
                    hint: Text(t.chatV2SettingsDefaultModelDefault),
                    items: <DropdownMenuItem<String?>>[
                      DropdownMenuItem(
                        value: null,
                        child: Text(t.chatV2SettingsDefaultModelDefault),
                      ),
                      for (final m in models)
                        DropdownMenuItem(
                          value: m.routeKey,
                          child: Text(m.label, overflow: TextOverflow.ellipsis),
                        ),
                    ],
                    onChanged: (v) {
                      if (v == null) {
                        notifier.setDefaultModel(null);
                        return;
                      }
                      final m = byKey[v];
                      if (m != null) {
                        notifier.setDefaultModel(m.code, providerId: m.providerId);
                      }
                    },
                  );
                },
              ),
            ),
            const SizedBox(height: BiuTokens.space4),

            // ── 语音朗读 (TTS) ────────────────────────────────
            const _TtsSection(),
            const SizedBox(height: BiuTokens.space4),

            // ── 自动改名 ──────────────────────────────────────
            _SectionCard(
              title: t.chatV2SettingsAutoRenameTitle,
              child: SwitchListTile(
                title: Text(
                  t.chatV2SettingsAutoRenameSubtitle,
                  style: theme.textTheme.bodySmall?.copyWith(
                    color: theme.colorScheme.onSurfaceVariant,
                  ),
                ),
                value: prefs.autoRenameEnabled,
                contentPadding: EdgeInsets.zero,
                onChanged: notifier.setAutoRenameEnabled,
              ),
            ),
          ],
        ),
      ),
    );
  }
}

/// _TtsSection —— 消息「朗读」的云端 TTS 配置。
///
/// 模型下拉(audio_speech 模型,空 = 设备本地)+ 音色输入框。配齐模型 + 音色
/// 后 TtsController 优先走 model-relay cosyvoice;否则/失败回落 flutter_tts。
class _TtsSection extends ConsumerStatefulWidget {
  const _TtsSection();
  @override
  ConsumerState<_TtsSection> createState() => _TtsSectionState();
}

class _TtsSectionState extends ConsumerState<_TtsSection> {
  late final TextEditingController _voiceCtl;

  @override
  void initState() {
    super.initState();
    final prefs = ref.read(chatPreferencesProvider);
    _voiceCtl = TextEditingController(text: prefs.ttsVoice ?? '');
  }

  @override
  void dispose() {
    _voiceCtl.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final t = AppLocalizations.of(context)!;
    final prefs = ref.watch(chatPreferencesProvider);
    final notifier = ref.read(chatPreferencesProvider.notifier);
    final modelsAsync = ref.watch(availableTtsModelsProvider);

    return _SectionCard(
      title: t.chatV2SettingsTtsTitle,
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            t.chatV2SettingsTtsHint,
            style: theme.textTheme.bodySmall?.copyWith(
              color: theme.colorScheme.onSurfaceVariant,
            ),
          ),
          const SizedBox(height: BiuTokens.space3),
          modelsAsync.when(
            loading: () => const LinearProgressIndicator(minHeight: 2),
            error: (_, _) => Text(
              '—',
              style: theme.textTheme.bodySmall
                  ?.copyWith(color: theme.colorScheme.error),
            ),
            data: (models) {
              // 同 code 跨 provider 去重(TTS 不做路由消歧,按 code 即可),
              // 避免 DropdownButton "exactly one item with value" 断言。
              final byCode = <String, AvailableChatModel>{};
              for (final m in models) {
                byCode.putIfAbsent(m.code, () => m);
              }
              final codes = byCode.keys.toList();
              final current =
                  codes.contains(prefs.ttsModel) ? prefs.ttsModel : null;
              return Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  DropdownButton<String?>(
                    value: current,
                    isExpanded: true,
                    hint: Text(t.chatV2SettingsTtsModelLocal),
                    items: <DropdownMenuItem<String?>>[
                      DropdownMenuItem(
                        value: null,
                        child: Text(t.chatV2SettingsTtsModelLocal),
                      ),
                      for (final code in codes)
                        DropdownMenuItem(
                          value: code,
                          child: Text(byCode[code]!.label,
                              overflow: TextOverflow.ellipsis),
                        ),
                    ],
                    onChanged: (v) => notifier.setTtsModel(v),
                  ),
                  if (models.isEmpty)
                    Padding(
                      padding: const EdgeInsets.only(top: BiuTokens.space2),
                      child: Text(
                        t.chatV2SettingsTtsNoModels,
                        style: theme.textTheme.bodySmall?.copyWith(
                          color: theme.colorScheme.onSurfaceVariant,
                        ),
                      ),
                    ),
                  // 音色输入框 —— 仅在选了云端模型时显示。
                  if (current != null) ...[
                    const SizedBox(height: BiuTokens.space3),
                    TextField(
                      controller: _voiceCtl,
                      decoration: InputDecoration(
                        labelText: t.chatV2SettingsTtsVoice,
                        hintText: t.chatV2SettingsTtsVoiceHint,
                        isDense: true,
                        border: const OutlineInputBorder(),
                      ),
                      onChanged: (v) => notifier.setTtsVoice(v.trim()),
                    ),
                  ],
                ],
              );
            },
          ),
        ],
      ),
    );
  }
}

/// 设置页统一的 section 卡:标题 + 内容。与 appearance_pane 的同名私有组件视觉
/// 对齐(brand 微染 + hairline 边框由 BiuCard 提供这里简化为 surface 卡)。
class _SectionCard extends StatelessWidget {
  const _SectionCard({required this.title, required this.child});
  final String title;
  final Widget child;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Container(
      padding: const EdgeInsets.all(BiuTokens.space4),
      decoration: BoxDecoration(
        color: theme.colorScheme.surface,
        borderRadius: BorderRadius.circular(BiuTokens.radiusMd),
        border: Border.all(color: BiuTokens.borderSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            title,
            style: const TextStyle(
              fontSize: 13,
              fontWeight: FontWeight.w600,
              letterSpacing: 0.2,
            ),
          ),
          const SizedBox(height: BiuTokens.space3),
          child,
        ],
      ),
    );
  }
}

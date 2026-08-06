// MessageActionsV2 —— assistant message 下方的 footer + 鼠标悬停 mini hover bar。
// 设计文档 docs/BiuMind-Chat-UI-Benchmark-Optimization.md（v2 一次成型）。
//
// 包含的 action（assistant 消息底部 footer）：
//   * 复制 / 重新生成 / 引用回复 / 翻译 / TTS / 分享 / 😊 emoji 反应 / ⭐ 收藏 /
//     Token usage chip
// 鼠标悬停 hover bar（仅 desktop / web）：
//   * 复制 / 重新生成 / 分享 / 😊 emoji 反应
//
// 引用回复通过 composerInjectProvider 把 markdown blockquote 塞到 composer。
// reaction 状态走 ChatRepo.watchReactionsForMessage。
//
// emoji 反应替代了原 👍/👎 点赞点踩（无消费者、纯本地占位）。设计参考 lobehub
// MessageContent.Thinking 同级的 ReactionPicker / ReactionDisplay（思路,未 fork）:
//   - 两段式选择:MenuAnchor 弹常用 emoji 网格 + 「更多」→ 全量 EmojiPicker
//   - 已选 emoji 以 chip 形式内联展示,点 chip 再点一次取消(toggle)
//   - 单用户本地场景,不做 count / users[] 多人聚合

import 'package:emoji_picker_flutter/emoji_picker_flutter.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:url_launcher/url_launcher.dart';

import '../../../../app/theme.dart';
import '../../../../core/layout/form_factor.dart';
import '../../../../data/local/db.dart';
import '../../application/chat_controller.dart';
import '../../application/draft_history_controller.dart';
import '../../application/tts_controller.dart';
import '../../domain/chat_models.dart';
import 'share_message_modal.dart';

/// Assistant message footer —— 一排小图标。仅 status==completed 显示。
class AssistantFooterV2 extends ConsumerWidget {
  const AssistantFooterV2({
    super.key,
    required this.message,
    required this.threadId,
  });

  final Message message;
  final String threadId;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final ctlState = ref.watch(chatControllerProvider(threadId));
    final inFlight = ctlState.value?.isStreaming ?? false;
    final ctl = ref.read(chatControllerProvider(threadId).notifier);
    final content = message.assembledText;

    // 手机形态: footer 精简为 复制 / 重生成 / 朗读 / reaction + ⋮(低频项进
    // 菜单) — 一整排 10+ 小图标在窄屏过密 (方案 §4.4); 桌面维持现状。
    final phone = isPhoneLayout(context);

    return Padding(
      padding: const EdgeInsets.only(top: 4, left: 4),
      child: Wrap(
        spacing: 0,
        runSpacing: 0,
        crossAxisAlignment: WrapCrossAlignment.center,
        children: [
          // emoji 反应置首位（高频 + 视觉锚点），其后才是复制 / 重生成等。
          _ReactionRow(threadId: threadId, messageId: message.id),
          _IconBtn(
            icon: Icons.copy_outlined,
            tooltip: '复制',
            onPressed: content.trim().isEmpty
                ? null
                : () => _copy(context, content),
          ),
          if (!phone)
            _IconBtn(
              icon: Icons.format_indent_increase,
              tooltip: '复制为 markdown 引用',
              onPressed: content.trim().isEmpty
                  ? null
                  : () => _copy(context, _quote(content), label: '已复制为引用'),
            ),
          _IconBtn(
            icon: Icons.refresh,
            tooltip: '重新生成',
            onPressed: inFlight ? null : () => ctl.regenerate(message.id),
          ),
          if (!phone)
            _IconBtn(
              icon: Icons.format_quote_outlined,
              tooltip: '引用回复（注入到输入框）',
              onPressed: content.trim().isEmpty
                  ? null
                  : () => ref
                        .read(composerInjectProvider.notifier)
                        .inject('${_quote(content)}\n\n'),
            ),
          if (!phone)
            _IconBtn(
              icon: Icons.translate,
              tooltip: '翻译（在 Google Translate 打开）',
              onPressed: content.trim().isEmpty
                  ? null
                  : () => _openTranslate(content),
            ),
          _SpeakBtn(messageId: message.id, text: content),
          if (!phone)
            _IconBtn(
              icon: Icons.ios_share,
              tooltip: '分享为图片',
              onPressed: content.trim().isEmpty
                  ? null
                  : () => showShareMessageDialog(
                      context,
                      content: content,
                      model: message.model,
                      createdAt: message.createdAt,
                    ),
            ),
          if (phone)
            PopupMenuButton<String>(
              icon: Icon(
                Icons.more_horiz,
                size: 18,
                color: BiuTokens.textSecondary,
              ),
              tooltip: '更多',
              enabled: content.trim().isNotEmpty,
              onSelected: (v) {
                switch (v) {
                  case 'quote-copy':
                    _copy(context, _quote(content), label: '已复制为引用');
                  case 'quote-inject':
                    ref
                        .read(composerInjectProvider.notifier)
                        .inject('${_quote(content)}\n\n');
                  case 'translate':
                    _openTranslate(content);
                  case 'share':
                    showShareMessageDialog(
                      context,
                      content: content,
                      model: message.model,
                      createdAt: message.createdAt,
                    );
                }
              },
              itemBuilder: (_) => const [
                PopupMenuItem(
                  value: 'quote-copy',
                  child: Text('复制为 markdown 引用'),
                ),
                PopupMenuItem(
                  value: 'quote-inject',
                  child: Text('引用回复（注入到输入框）'),
                ),
                PopupMenuItem(
                  value: 'translate',
                  child: Text('翻译（在 Google Translate 打开）'),
                ),
                PopupMenuItem(value: 'share', child: Text('分享为图片')),
              ],
            ),
          _DeleteMessageBtn(message: message),
          _TokenChip(input: message.inputTokens, output: message.outputTokens),
        ],
      ),
    );
  }

  Future<void> _copy(
    BuildContext context,
    String text, {
    String label = '已复制',
  }) async {
    await Clipboard.setData(ClipboardData(text: text));
    if (context.mounted) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text(label), duration: const Duration(seconds: 1)),
      );
    }
  }

  Future<void> _openTranslate(String text) async {
    final encoded = Uri.encodeComponent(text);
    final url = Uri.parse(
      'https://translate.google.com/?sl=auto&tl=zh-CN&text=$encoded&op=translate',
    );
    try {
      await launchUrl(url, mode: LaunchMode.externalApplication);
    } catch (_) {
      /* fail silent */
    }
  }
}

/// 鼠标悬停时右上角浮出的 mini bar（3 个高频 action）。
/// MessageBubbleV2 通过 MouseRegion 控制 opacity。
class HoverActionBarV2 extends ConsumerWidget {
  const HoverActionBarV2({
    super.key,
    required this.message,
    required this.threadId,
  });

  final Message message;
  final String threadId;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final theme = Theme.of(context);
    final ctlState = ref.watch(chatControllerProvider(threadId));
    final inFlight = ctlState.value?.isStreaming ?? false;
    final ctl = ref.read(chatControllerProvider(threadId).notifier);
    final content = message.assembledText;

    return Material(
      color: Colors.transparent,
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 4, vertical: 2),
        decoration: BoxDecoration(
          color: theme.colorScheme.surface,
          borderRadius: BorderRadius.circular(6),
          border: Border.all(color: theme.colorScheme.outlineVariant),
          boxShadow: ShadowTokens.forBrightness(theme.brightness).md,
        ),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            // emoji 反应置首位 —— 跟 footer 对齐。
            _EmojiReactionTrigger(
              onPick: (emoji) => ref.read(chatControllerDepsProvider).repo
                  .toggleReaction(
                    messageId: message.id,
                    threadId: threadId,
                    kind: emoji,
                  ),
            ),
            _IconBtn(
              icon: Icons.copy_outlined,
              tooltip: '复制',
              onPressed: content.trim().isEmpty
                  ? null
                  : () async {
                      await Clipboard.setData(ClipboardData(text: content));
                      if (context.mounted) {
                        ScaffoldMessenger.of(context).showSnackBar(
                          const SnackBar(
                            content: Text('已复制'),
                            duration: Duration(seconds: 1),
                          ),
                        );
                      }
                    },
            ),
            _IconBtn(
              icon: Icons.format_indent_increase,
              tooltip: '复制为引用',
              onPressed: content.trim().isEmpty
                  ? null
                  : () async {
                      await Clipboard.setData(
                        ClipboardData(text: _quote(content)),
                      );
                      if (context.mounted) {
                        ScaffoldMessenger.of(context).showSnackBar(
                          const SnackBar(
                            content: Text('已复制为引用'),
                            duration: Duration(seconds: 1),
                          ),
                        );
                      }
                    },
            ),
            _IconBtn(
              icon: Icons.refresh,
              tooltip: '重新生成',
              onPressed: inFlight ? null : () => ctl.regenerate(message.id),
            ),
            _IconBtn(
              icon: Icons.translate,
              tooltip: '翻译（Google Translate）',
              onPressed: content.trim().isEmpty
                  ? null
                  : () => _openTranslateUrl(content),
            ),
            _SpeakBtn(messageId: message.id, text: content),
            _HoverStarBtn(threadId: threadId, messageId: message.id),
            _IconBtn(
              icon: Icons.ios_share,
              tooltip: '分享为图片',
              onPressed: content.trim().isEmpty
                  ? null
                  : () => showShareMessageDialog(
                      context,
                      content: content,
                      model: message.model,
                      createdAt: message.createdAt,
                    ),
            ),
            _DeleteMessageBtn(message: message),
          ],
        ),
      ),
    );
  }
}

Future<void> _openTranslateUrl(String text) async {
  final encoded = Uri.encodeComponent(text);
  final url = Uri.parse(
    'https://translate.google.com/?sl=auto&tl=zh-CN&text=$encoded&op=translate',
  );
  try {
    await launchUrl(url, mode: LaunchMode.externalApplication);
  } catch (_) {
    /* fail silent */
  }
}

/// HoverBar 上的 ⭐ 收藏切换按钮 —— 跟 footer 的 _ReactionRow 走同一 repo
/// 路径，区别只是只展示 star（不暴露 like/dislike）。
class _HoverStarBtn extends ConsumerWidget {
  const _HoverStarBtn({required this.threadId, required this.messageId});
  final String threadId;
  final String messageId;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final reactionsAsync = ref.watch(_reactionsForMessageProvider(messageId));
    final set =
        reactionsAsync.valueOrNull?.map((r) => r.kind).toSet() ?? const {};
    final starred = set.contains('star');
    return _IconBtn(
      icon: starred ? Icons.star : Icons.star_outline,
      tooltip: starred ? '取消收藏' : '收藏',
      color: starred ? StarredColors.icon : null,
      onPressed: () => ref
          .read(chatControllerDepsProvider)
          .repo
          .toggleReaction(
            messageId: messageId,
            threadId: threadId,
            kind: 'star',
          ),
    );
  }
}

/// 删除消息按钮（assistant footer / hover / user hover 共用）—— 本地
/// [ChatRepo.deleteMessages] 不可恢复，故点完弹二次确认 dialog。
class _DeleteMessageBtn extends ConsumerWidget {
  const _DeleteMessageBtn({required this.message});
  final Message message;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    return _IconBtn(
      icon: Icons.delete_outline,
      tooltip: '删除',
      color: Theme.of(context).colorScheme.error,
      onPressed: () => _confirmAndDelete(context, ref),
    );
  }

  Future<void> _confirmAndDelete(BuildContext context, WidgetRef ref) async {
    final ok = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('删除消息'),
        content: const Text('删除该条消息？该操作不可恢复。'),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(ctx).pop(false),
            child: const Text('取消'),
          ),
          TextButton(
            style: TextButton.styleFrom(
              foregroundColor: Theme.of(ctx).colorScheme.error,
            ),
            onPressed: () => Navigator.of(ctx).pop(true),
            child: const Text('删除'),
          ),
        ],
      ),
    );
    if (ok != true) return;
    await ref
        .read(chatControllerProvider(message.threadId).notifier)
        .deleteMessage(message.id);
  }
}

/// 用户消息鼠标悬停时浮出的 mini bar —— 与 assistant 的 [HoverActionBarV2]
/// 对称。动作：复制 / 编辑 / 重新生成 / 删除。
///
/// 编辑走 [onEdit] 回调（bubble 层切到内联编辑态）；重新生成 = 以该 user
/// 文本为 prompt 截断其后并重发（[ChatController.regenerateFromUserMessage]）。
class UserMessageHoverBar extends ConsumerWidget {
  const UserMessageHoverBar({
    super.key,
    required this.message,
    required this.threadId,
    required this.onEdit,
  });

  final Message message;
  final String threadId;
  /// 进入内联编辑态（bubble 持有 _editing 状态）。
  final VoidCallback onEdit;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final theme = Theme.of(context);
    final ctlState = ref.watch(chatControllerProvider(threadId));
    final inFlight = ctlState.value?.isStreaming ?? false;
    final ctl = ref.read(chatControllerProvider(threadId).notifier);
    final content = message.assembledText;

    return Material(
      color: Colors.transparent,
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 4, vertical: 2),
        decoration: BoxDecoration(
          color: theme.colorScheme.surface,
          borderRadius: BorderRadius.circular(6),
          border: Border.all(color: theme.colorScheme.outlineVariant),
          boxShadow: ShadowTokens.forBrightness(theme.brightness).md,
        ),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            _IconBtn(
              icon: Icons.copy_outlined,
              tooltip: '复制',
              onPressed: content.trim().isEmpty
                  ? null
                  : () async {
                      await Clipboard.setData(ClipboardData(text: content));
                      if (context.mounted) {
                        ScaffoldMessenger.of(context).showSnackBar(
                          const SnackBar(
                            content: Text('已复制'),
                            duration: Duration(seconds: 1),
                          ),
                        );
                      }
                    },
            ),
            _IconBtn(
              icon: Icons.edit_outlined,
              tooltip: '编辑',
              onPressed: content.trim().isEmpty ? null : onEdit,
            ),
            _IconBtn(
              icon: Icons.refresh,
              tooltip: '重新生成',
              onPressed: inFlight
                  ? null
                  : () => ctl.regenerateFromUserMessage(message.id),
            ),
            _DeleteMessageBtn(message: message),
          ],
        ),
      ),
    );
  }
}

class _IconBtn extends StatelessWidget {
  const _IconBtn({
    required this.icon,
    required this.tooltip,
    required this.onPressed,
    this.color,
  });
  final IconData icon;
  final String tooltip;
  final VoidCallback? onPressed;
  final Color? color;

  @override
  Widget build(BuildContext context) {
    // 手机端触摸目标加大到 40 (桌面紧凑密度 28 不变 — 方案 §4.4)。
    final phone = isPhoneLayout(context);
    return IconButton(
      icon: Icon(icon, size: phone ? 18 : 14),
      tooltip: tooltip,
      visualDensity: VisualDensity.compact,
      padding: EdgeInsets.zero,
      constraints: BoxConstraints(
        minWidth: phone ? 40 : 28,
        minHeight: phone ? 40 : 28,
      ),
      color: color ?? BiuTokens.textSecondary,
      onPressed: onPressed,
    );
  }
}

class _SpeakBtn extends ConsumerWidget {
  const _SpeakBtn({required this.messageId, required this.text});
  final String messageId;
  final String text;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final activeId = ref.watch(ttsControllerProvider);
    final active = activeId == messageId;
    return _IconBtn(
      icon: active ? Icons.stop_circle_outlined : Icons.volume_up_outlined,
      tooltip: active ? '停止朗读' : '朗读',
      color: active ? BiuTokens.purple : BiuTokens.textSecondary,
      onPressed: text.trim().isEmpty
          ? null
          : () async {
              final c = ref.read(ttsControllerProvider.notifier);
              if (active) {
                await c.stop();
                return;
              }
              await c.speak(
                messageId: messageId,
                text: text,
                localeTag: detectTtsLocale(text),
              );
            },
    );
  }
}

class _ReactionRow extends ConsumerWidget {
  const _ReactionRow({required this.threadId, required this.messageId});
  final String threadId;
  final String messageId;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final reactionsAsync = ref.watch(_reactionsForMessageProvider(messageId));
    final all = reactionsAsync.valueOrNull ?? const <LocalMessageReactionV2>[];
    final starred = all.any((r) => r.kind == 'star');
    // emoji 反应:排除 'star' (收藏单独成按钮) 与历史 'like'/'dislike' 残留。
    final emojis = all
        .map((r) => r.kind)
        .where((k) => k != 'star' && k != 'like' && k != 'dislike')
        .toList();
    final deps = ref.read(chatControllerDepsProvider);
    void toggle(String kind) => deps.repo.toggleReaction(
          messageId: messageId,
          threadId: threadId,
          kind: kind,
        );
    return Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        _EmojiReactionTrigger(onPick: toggle),
        for (final e in emojis) _EmojiChip(emoji: e, onTap: () => toggle(e)),
        _IconBtn(
          icon: starred ? Icons.star : Icons.star_outline,
          tooltip: starred ? '取消收藏' : '收藏',
          color: starred ? StarredColors.icon : BiuTokens.textSecondary,
          onPressed: () => toggle('star'),
        ),
      ],
    );
  }
}

/// 😊 emoji 反应触发器 —— 点开弹常用 emoji 网格,「更多」展开全量 picker。
/// 两段式参考 lobehub ReactionPicker (QUICK_REACTIONS + emoji-mart)。
class _EmojiReactionTrigger extends StatefulWidget {
  const _EmojiReactionTrigger({required this.onPick});
  final ValueChanged<String> onPick;

  @override
  State<_EmojiReactionTrigger> createState() => _EmojiReactionTriggerState();
}

class _EmojiReactionTriggerState extends State<_EmojiReactionTrigger> {
  // 自持 MenuController —— menuChildren 在 overlay 里渲染,拿不到 builder 的
  // controller,故外部 new 一个传给 MenuAnchor,回调里直接 close。
  final MenuController _controller = MenuController();

  Future<void> _showFull() async {
    final picked = await showDialog<String>(
      context: context,
      builder: (ctx) => const _FullEmojiPickerDialog(),
    );
    if (picked != null && mounted) widget.onPick(picked);
  }

  @override
  Widget build(BuildContext context) {
    return MenuAnchor(
      controller: _controller,
      menuChildren: [
        _QuickEmojiGrid(
          onPick: (emoji) {
            _controller.close();
            widget.onPick(emoji);
          },
          onMore: () {
            _controller.close();
            _showFull();
          },
        ),
      ],
      builder: (context, controller, child) {
        final phone = isPhoneLayout(context);
        return IconButton(
          icon: Icon(Icons.add_reaction_outlined, size: phone ? 18 : 14),
          tooltip: '添加表情',
          visualDensity: VisualDensity.compact,
          padding: EdgeInsets.zero,
          constraints: BoxConstraints(
            minWidth: phone ? 40 : 28,
            minHeight: phone ? 40 : 28,
          ),
          color: BiuTokens.textSecondary,
          onPressed: () =>
              controller.isOpen ? controller.close() : controller.open(),
        );
      },
    );
  }
}

/// 常用 emoji 网格 —— 固定精选集 + 「更多」入口。
/// 精选集覆盖 99% 反应场景 (对标 lobehub QUICK_REACTIONS 扩充);「更多」
/// 才进全量 picker,避免每次都扛 1500+ emoji 的重面板。
class _QuickEmojiGrid extends StatelessWidget {
  const _QuickEmojiGrid({required this.onPick, required this.onMore});
  final ValueChanged<String> onPick;
  final VoidCallback onMore;

  static const _quick = <String>[
    '👍', '👎', '❤️', '🔥', '👏', '🙏', '💯', '🎉',
    '😂', '😅', '😄', '😎', '🤔', '😮', '😢', '😡',
    '💪', '✨', '🚀', '👀', '🙌', '💜', '🤝', '💡',
  ];

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return SizedBox(
      width: 232, // 8 列 × ~29 → 紧凑网格
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Padding(
            padding: const EdgeInsets.all(6),
            child: Wrap(
              spacing: 2,
              runSpacing: 2,
              children: [
                for (final e in _quick)
                  InkWell(
                    onTap: () => onPick(e),
                    borderRadius: BorderRadius.circular(6),
                    child: Padding(
                      padding: const EdgeInsets.all(4),
                      child: Text(e, style: const TextStyle(fontSize: 18)),
                    ),
                  ),
              ],
            ),
          ),
          Divider(
            height: 1,
            thickness: 1,
            color: theme.colorScheme.outlineVariant,
          ),
          TextButton.icon(
            onPressed: onMore,
            icon: const Icon(Icons.grid_view_outlined, size: 14),
            label: const Text('更多表情'),
            style: TextButton.styleFrom(
              visualDensity: VisualDensity.compact,
              minimumSize: const Size(0, 32),
              tapTargetSize: MaterialTapTargetSize.shrinkWrap,
              alignment: Alignment.centerLeft,
            ),
          ),
        ],
      ),
    );
  }
}

/// 已选 emoji 反应 chip —— 单用户本地场景,存在即当前用户所选,点一下取消。
class _EmojiChip extends StatelessWidget {
  const _EmojiChip({required this.emoji, required this.onTap});
  final String emoji;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Padding(
      padding: const EdgeInsets.only(left: 2),
      child: InkWell(
        onTap: onTap,
        borderRadius: BorderRadius.circular(12),
        child: Tooltip(
          message: '取消表情',
          child: Container(
            padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
            decoration: BoxDecoration(
              color: theme.colorScheme.primary.withValues(alpha: 0.12),
              borderRadius: BorderRadius.circular(12),
              border: Border.all(
                color: theme.colorScheme.primary.withValues(alpha: 0.4),
              ),
            ),
            child: Text(emoji, style: const TextStyle(fontSize: 14)),
          ),
        ),
      ),
    );
  }
}

/// 全量 emoji picker dialog —— 8 分类 + 搜索,选一个返回 emoji 字符串。
class _FullEmojiPickerDialog extends StatelessWidget {
  const _FullEmojiPickerDialog();

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return AlertDialog(
      contentPadding: EdgeInsets.zero,
      titlePadding: EdgeInsets.zero,
      content: SizedBox(
        width: 328,
        child: EmojiPicker(
          onEmojiSelected: (category, emoji) =>
              Navigator.of(context).pop(emoji.emoji),
          config: Config(
            height: 300,
            checkPlatformCompatibility: true,
            emojiViewConfig: EmojiViewConfig(
              emojiSizeMax: 28 *
                  (defaultTargetPlatform == TargetPlatform.iOS ? 1.2 : 1.0),
            ),
            viewOrderConfig: const ViewOrderConfig(
              top: EmojiPickerItem.categoryBar,
              middle: EmojiPickerItem.emojiView,
              bottom: EmojiPickerItem.searchBar,
            ),
            skinToneConfig: const SkinToneConfig(),
            categoryViewConfig: CategoryViewConfig(
              backgroundColor: theme.colorScheme.surface,
            ),
            bottomActionBarConfig: const BottomActionBarConfig(),
            searchViewConfig: SearchViewConfig(
              backgroundColor: theme.colorScheme.surface,
            ),
          ),
        ),
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.of(context).pop(),
          child: const Text('取消'),
        ),
      ],
    );
  }
}

class _TokenChip extends StatelessWidget {
  const _TokenChip({this.input, this.output});
  final int? input;
  final int? output;

  @override
  Widget build(BuildContext context) {
    if (input == null && output == null) return const SizedBox.shrink();
    final parts = <String>[];
    if (input != null) parts.add('↑${_fmt(input!)}');
    if (output != null) parts.add('↓${_fmt(output!)}');
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 6),
      child: Text(
        parts.join(' '),
        style: TextStyle(
          fontSize: 11,
          color: BiuTokens.textMuted,
          fontFeatures: const [FontFeature.tabularFigures()],
        ),
      ),
    );
  }

  static String _fmt(int n) {
    if (n < 1000) return '$n';
    if (n < 10000) return '${(n / 1000).toStringAsFixed(1)}k';
    return '${(n / 1000).round()}k';
  }
}

final _reactionsForMessageProvider = StreamProvider.autoDispose
    .family<List<LocalMessageReactionV2>, String>((ref, messageId) {
      final deps = ref.watch(chatControllerDepsProvider);
      return deps.repo.watchReactionsForMessage(messageId);
    });

/// 把消息文本转 markdown blockquote。长引用截断成前 6 行 + 省略号。
String _quote(String content) {
  final trimmed = content.trim();
  final lines = trimmed.split('\n');
  const maxLines = 6;
  final kept = lines.length <= maxLines
      ? lines
      : [...lines.take(maxLines), '...'];
  return kept.map((l) => '> $l').join('\n');
}

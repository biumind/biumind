// ChatPageV2 —— Chat 重构 R4 主页面。
//
// 顶 AppBar（thread 标题 + 错误提示） + MessageListV2 + ComposerV2。
// 不存任何本地 state —— 都来自 chatControllerProvider + threadProvider +
// messagesProvider。
//
// 进入路径：
//   - 先 ref.read(chatRepoProvider).createThread(...) 拿 threadId
//   - push ChatPageV2(threadId: ...)
//
// R4 不做 thread switching / 侧边栏 / NewThreadDialog —— 那些 R6 来。

import 'package:collection/collection.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../../core/layout/form_factor.dart';
import '../../../../data/notes_providers.dart';
import '../../../../l10n/app_localizations.dart';
import '../../application/chat_controller.dart';
import '../../application/in_thread_search_controller.dart';
import '../../application/selection_mode_controller.dart';
import '../../domain/chat_models.dart';
import '../../domain/slash_commands.dart';
import 'approval_card.dart';
import 'composer_v2.dart';
import 'context_window_bar.dart';
import 'in_thread_search_bar.dart';
import 'keyboard_shortcuts_dialog.dart';
import 'message_list_v2.dart';
import 'model_picker_dialog.dart';
import 'new_thread_dialog.dart';
import 'selection_action_bar.dart';
import 'thread_settings_sheet.dart';

class ChatPageV2 extends ConsumerWidget {
  const ChatPageV2({
    super.key,
    required this.threadId,
    this.userName,
    this.onBack,
  });

  final String threadId;
  final String? userName;

  /// 手机形态 (列表↔会话两级): 返回会话列表。桌面 null = 无返回按钮。
  final VoidCallback? onBack;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final threadAsync = ref.watch(threadProvider(threadId));
    final controllerAsync = ref.watch(chatControllerProvider(threadId));
    final notifier = ref.read(chatControllerProvider(threadId).notifier);

    final isStreaming = controllerAsync.value?.isStreaming ?? false;
    final isCancelling = controllerAsync.value?.isCancelling ?? false;
    final lastError = controllerAsync.value?.lastError;
    final lastErrorAction =
        controllerAsync.value?.lastErrorAction ?? ChatErrorAction.none;
    final modelHint = threadAsync.value?.model;

    // P0-3: 切 thread 时退出多选模式。selectionMode 锚定 threadId，与
    // 当前 page 不一致时 reset。schedulePostFrame 避免 build 中 setState。
    final selMode = ref.watch(selectionModeProvider);
    if (selMode.active && selMode.threadId != threadId) {
      WidgetsBinding.instance.addPostFrameCallback((_) {
        ref.read(selectionModeProvider.notifier).onThreadChanged(threadId);
      });
    }

    final searchOpen = ref.watch(inThreadSearchProvider(threadId)).open;
    final l = AppLocalizations.of(context)!;
    // 手机形态 (<600px): AppBar 收敛为 ← + 标题 + [stop] + search (pin/
    // 多选/设置/快捷键面板在手机上不提供); 桌面右上角极简 (WorkBuddy 风):
    // [stop] + search + ⋮(pin/多选/设置/快捷键)。mode 只在 composer chip
    // 行展示。
    final phone = isPhoneLayout(context);

    return Scaffold(
      appBar: AppBar(
        // 手机两级导航: ← 返回会话列表 (非路由 pop, 清 shell 的 _selectedId)。
        leading: onBack != null ? BackButton(onPressed: onBack) : null,
        title: _ThreadTitle(
          thread: threadAsync.value,
          threadId: threadId,
          isStreaming: isStreaming,
          isCancelling: isCancelling,
        ),
        actions: [
          if (threadAsync.value != null) ...[
            // 流式中显示停止按钮 —— 跟 composer 的 stop 一致，但放 AppBar 上
            // 让用户翻看历史时也能立刻 stop（不必滚到底）。
            if (isStreaming || isCancelling)
              IconButton(
                icon: Icon(
                  isCancelling ? Icons.hourglass_top : Icons.stop_circle,
                  size: 20,
                ),
                tooltip: isCancelling
                    ? l.chatV2AppBarStoppingTooltip
                    : l.chatV2AppBarStopTooltip,
                color: isCancelling
                    ? null
                    : Theme.of(context).colorScheme.error,
                onPressed: isCancelling ? null : () => notifier.cancel(),
              ),
            IconButton(
              icon: const Icon(Icons.search, size: 20),
              tooltip: l.chatV2AppBarSearchTooltip,
              onPressed: () =>
                  ref.read(inThreadSearchProvider(threadId).notifier).open(),
            ),
            // 低频动作收进 ⋮ (右上角极简): pin / 多选 / 会话设置 / 快捷键面板。
            // pin 功能跟 sidebar 右键 pin 同源。手机端本就不提供后三项。
            if (!phone)
              PopupMenuButton<String>(
                icon: const Icon(Icons.more_horiz, size: 20),
                tooltip: l.chatV2AppBarMore,
                onSelected: (v) {
                  switch (v) {
                    case 'pin':
                      ref
                          .read(chatControllerDepsProvider)
                          .repo
                          .setPinned(threadId, !threadAsync.value!.pinned);
                    case 'select':
                      ref
                          .read(selectionModeProvider.notifier)
                          .enter(threadId);
                    case 'settings':
                      showThreadSettingsSheet(context, threadId: threadId);
                    case 'shortcuts':
                      showKeyboardShortcutsDialog(context);
                  }
                },
                itemBuilder: (menuCtx) => [
                  _appBarMenuItem(
                    menuCtx,
                    value: 'pin',
                    icon: threadAsync.value!.pinned
                        ? Icons.push_pin
                        : Icons.push_pin_outlined,
                    label: threadAsync.value!.pinned
                        ? l.chatV2AppBarUnpinTooltip
                        : l.chatV2AppBarPinTooltip,
                  ),
                  _appBarMenuItem(
                    menuCtx,
                    value: 'select',
                    icon: Icons.checklist_rtl,
                    label: l.chatV2AppBarMultiSelectTooltip,
                  ),
                  _appBarMenuItem(
                    menuCtx,
                    value: 'settings',
                    icon: Icons.tune,
                    label: l.chatV2AppBarSettingsTooltip,
                  ),
                  _appBarMenuItem(
                    menuCtx,
                    value: 'shortcuts',
                    icon: Icons.keyboard_outlined,
                    label: l.chatV2AppBarShortcutsTooltip,
                  ),
                ],
              ),
          ],
        ],
      ),
      body: CallbackShortcuts(
        bindings: {
          const SingleActivator(LogicalKeyboardKey.keyF, meta: true): () =>
              ref.read(inThreadSearchProvider(threadId).notifier).toggle(),
          const SingleActivator(LogicalKeyboardKey.keyF, control: true): () =>
              ref.read(inThreadSearchProvider(threadId).notifier).toggle(),
          const SingleActivator(LogicalKeyboardKey.escape): () {
            if (ref.read(inThreadSearchProvider(threadId)).open) {
              ref.read(inThreadSearchProvider(threadId).notifier).close();
            }
          },
          // Shift+? 弹快捷键面板。Composer 聚焦时此 shortcut 被 TextField
          // 吃掉（输入 ?），离开输入框时才生效，跟用户预期一致。
          const SingleActivator(LogicalKeyboardKey.slash, shift: true): () =>
              showKeyboardShortcutsDialog(context),
          // Cmd/Ctrl+Shift+M 弹模型选择 dialog —— 比 AppBar PopupMenu 还
          // 直达，不必抬手点。
          const SingleActivator(
            LogicalKeyboardKey.keyM,
            meta: true,
            shift: true,
          ): () =>
              _showModelPickerDialog(context, ref, threadId),
          const SingleActivator(
            LogicalKeyboardKey.keyM,
            control: true,
            shift: true,
          ): () =>
              _showModelPickerDialog(context, ref, threadId),
        },
        child: Focus(
          autofocus: true,
          child: _buildBody(
            context: context,
            ref: ref,
            threadId: threadId,
            lastError: lastError,
            lastErrorAction: lastErrorAction,
            modelHint: modelHint,
            isStreaming: isStreaming,
            isCancelling: isCancelling,
            controllerAsync: controllerAsync,
            notifier: notifier,
            searchOpen: searchOpen,
          ),
        ),
      ),
    );
  }

  Widget _buildBody({
    required BuildContext context,
    required WidgetRef ref,
    required String threadId,
    required String? lastError,
    required ChatErrorAction lastErrorAction,
    required String? modelHint,
    required bool isStreaming,
    required bool isCancelling,
    required AsyncValue controllerAsync,
    required ChatController notifier,
    required bool searchOpen,
  }) {
    return Column(
      children: [
        if (lastError != null)
          _ErrorBanner(
            error: lastError,
            action: lastErrorAction,
            onAction: () =>
                _onErrorAction(context, ref, threadId, lastErrorAction),
            onDismiss: () => ref
                .read(chatControllerProvider(threadId).notifier)
                .clearError(),
          ),
        if (searchOpen) InThreadSearchBarV2(threadId: threadId),
        ContextWindowBarV2(threadId: threadId, model: modelHint),
        Expanded(
          child: Stack(
            children: [
              MessageListV2(
                threadId: threadId,
                modelHint: modelHint,
                userName: userName,
              ),
              const Positioned(
                left: 0,
                right: 0,
                bottom: 0,
                child: SelectionActionBarV2(),
              ),
            ],
          ),
        ),
        // Inline tool-call approval card —— 仅 thread.autoApprove != 'auto'
        // 时才会有 pending 请求,有就在 composer 上方浮一张 confirm 卡。
        ApprovalCardV2(threadId: threadId),
        ComposerV2(
          threadId: threadId,
          streaming: isStreaming,
          cancelling: isCancelling,
          enabled: !controllerAsync.isLoading,
          onSend: (text, attachments) =>
              notifier.sendMessage(text, attachments: attachments),
          onCancel: () => notifier.cancel(),
          onSlashCommand: (cmd) => _onSlash(context, ref, cmd, threadId),
        ),
      ],
    );
  }
}

class _ThreadTitle extends StatelessWidget {
  const _ThreadTitle({
    this.thread,
    this.threadId,
    this.isStreaming = false,
    this.isCancelling = false,
  });
  final Thread? thread;
  final String? threadId;
  final bool isStreaming;
  final bool isCancelling;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final l = AppLocalizations.of(context)!;
    final t = thread;
    final title = t == null
        ? l.chatV2SidebarTitle
        : (t.title.isEmpty ? l.chatV2NewThreadFallback : t.title);
    return Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        Flexible(child: Text(title, overflow: TextOverflow.ellipsis)),
        if (isStreaming || isCancelling) ...[
          const SizedBox(width: 8),
          SizedBox(
            width: 12,
            height: 12,
            child: CircularProgressIndicator(
              strokeWidth: 1.6,
              valueColor: AlwaysStoppedAnimation<Color>(
                isCancelling
                    ? theme.colorScheme.error
                    : theme.colorScheme.primary,
              ),
            ),
          ),
          const SizedBox(width: 6),
          Text(
            isCancelling
                ? l.chatV2ThreadStatusStopping
                : l.chatV2ThreadStatusGenerating,
            style: theme.textTheme.labelSmall?.copyWith(
              color: isCancelling
                  ? theme.colorScheme.error
                  : theme.colorScheme.primary,
            ),
          ),
          if (isStreaming && !isCancelling && threadId != null)
            _AppBarStreamRate(threadId: threadId!),
        ],
      ],
    );
  }
}

/// AppBar 上的 token/s 速率小标签 —— 跟 block 末尾的 _StreamRateMeter
/// 同算法（chars/sec → /4 ≈ tokens/sec EMA），但跟踪的是当前 active
/// assistant message 的 assembledText。streaming 期间 messagesProvider
/// 持续 emit 触发 build，自然取到最新长度。
class _AppBarStreamRate extends ConsumerStatefulWidget {
  const _AppBarStreamRate({required this.threadId});
  final String threadId;

  @override
  ConsumerState<_AppBarStreamRate> createState() => _AppBarStreamRateState();
}

class _AppBarStreamRateState extends ConsumerState<_AppBarStreamRate> {
  DateTime? _firstAt;
  DateTime? _lastAt;
  int _lastLen = 0;
  double _ema = 0.0;
  bool _have = false;
  String? _trackedMessageId;

  @override
  Widget build(BuildContext context) {
    final ctlAsync = ref.watch(chatControllerProvider(widget.threadId));
    final activeId = ctlAsync.value?.activeAssistantMessageId;
    final msgsAsync = ref.watch(messagesProvider(widget.threadId));
    if (activeId == null || msgsAsync.valueOrNull == null) {
      return const SizedBox.shrink();
    }
    final msgs = msgsAsync.value!;
    final m = msgs.firstWhere((x) => x.id == activeId, orElse: () => msgs.last);
    final text = m.assembledText;
    final now = DateTime.now();

    // 切到新一条 active message 时重置统计。
    if (_trackedMessageId != activeId) {
      _trackedMessageId = activeId;
      _firstAt = now;
      _lastAt = now;
      _lastLen = text.length;
      _ema = 0.0;
      _have = false;
    }
    // 长度变化 → 累计速率。
    final dt = now.difference(_lastAt ?? now).inMilliseconds;
    if (dt >= 50 && text.length > _lastLen) {
      final inst = (text.length - _lastLen) * 1000.0 / dt;
      _ema = _have ? 0.3 * inst + 0.7 * _ema : inst;
      _have = true;
      _lastAt = now;
      _lastLen = text.length;
    }
    if (!_have) return const SizedBox.shrink();
    final since = now.difference(_firstAt ?? now);
    if (since.inMilliseconds < 800) return const SizedBox.shrink();
    final tps = (_ema / 4).round();
    if (tps <= 0) return const SizedBox.shrink();
    final theme = Theme.of(context);
    return Padding(
      padding: const EdgeInsets.only(left: 6),
      child: Text(
        '· $tps t/s',
        style: theme.textTheme.labelSmall?.copyWith(
          color: theme.colorScheme.onSurfaceVariant,
          fontFeatures: const [FontFeature.tabularFigures()],
        ),
      ),
    );
  }
}

class _ErrorBanner extends StatelessWidget {
  const _ErrorBanner({
    required this.error,
    required this.onDismiss,
    this.action = ChatErrorAction.none,
    this.onAction,
  });
  final String error;
  final ChatErrorAction action;
  final VoidCallback? onAction;
  final VoidCallback onDismiss;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Material(
      color: theme.colorScheme.errorContainer,
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
        child: Row(
          children: [
            Icon(
              Icons.error_outline,
              size: 18,
              color: theme.colorScheme.onErrorContainer,
            ),
            const SizedBox(width: 8),
            Expanded(
              child: Text(
                error,
                style: theme.textTheme.bodySmall?.copyWith(
                  color: theme.colorScheme.onErrorContainer,
                ),
                maxLines: 2,
                overflow: TextOverflow.ellipsis,
              ),
            ),
            // 一键修复入口 —— 模型停用/不存在/无渠道给「重新选择模型」,
            // plan 门禁给「升级会员」。none 时不渲染。
            if (action != ChatErrorAction.none && onAction != null) ...[
              const SizedBox(width: 8),
              TextButton(
                onPressed: onAction,
                style: TextButton.styleFrom(
                  foregroundColor: theme.colorScheme.onErrorContainer,
                  padding: const EdgeInsets.symmetric(horizontal: 8),
                  minimumSize: const Size(0, 32),
                  textStyle: theme.textTheme.labelMedium?.copyWith(
                    fontWeight: FontWeight.w600,
                  ),
                ),
                child: Text(
                  action == ChatErrorAction.upgradePlan ? '升级会员' : '重新选择模型',
                ),
              ),
            ],
            IconButton(
              icon: const Icon(Icons.close, size: 16),
              onPressed: onDismiss,
              padding: EdgeInsets.zero,
              constraints: const BoxConstraints(minWidth: 24, minHeight: 24),
              color: theme.colorScheme.onErrorContainer,
            ),
          ],
        ),
      ),
    );
  }
}

/// 错误 banner 动作分发:reselectModel → 打开模型选择器(复用 thread 改模型
/// 流程);upgradePlan → 跳会员中心。动作完成后清掉错误 banner。
Future<void> _onErrorAction(
  BuildContext context,
  WidgetRef ref,
  String threadId,
  ChatErrorAction action,
) async {
  switch (action) {
    case ChatErrorAction.reselectModel:
      await _showModelPickerDialog(context, ref, threadId);
    case ChatErrorAction.upgradePlan:
      if (context.mounted) context.go('/membership');
    case ChatErrorAction.none:
      return;
  }
  // 用户已采取修复动作(换模型/去升级)→ 清掉旧错误,避免 banner 滞留。
  ref.read(chatControllerProvider(threadId).notifier).clearError();
}

/// AppBar ⋮ 菜单项 (icon + label 横排)。
PopupMenuEntry<String> _appBarMenuItem(
  BuildContext context, {
  required String value,
  required IconData icon,
  required String label,
}) {
  return PopupMenuItem<String>(
    value: value,
    child: Row(
      children: [
        Icon(
          icon,
          size: 18,
          color: Theme.of(context).colorScheme.onSurfaceVariant,
        ),
        const SizedBox(width: 10),
        Text(label),
      ],
    ),
  );
}

/// 把 ComposerV2 抛上来的斜杠命令派发到当前 page 上下文。
/// /clear 和 /help 在 composer 内部已处理；这里对接 /new 和 /note。
Future<void> _onSlash(
  BuildContext context,
  WidgetRef ref,
  SlashCommand cmd,
  String threadId,
) async {
  switch (cmd.id) {
    case 'new':
      // 直接按默认偏好建会话(不弹对话框)——与 ThreadsShellPage sidebar 的
      // "+" 按钮一致。
      await createDefaultThread(ref);
      break;
    case 'note':
      await _saveLastReplyAsNote(context, ref, threadId);
      break;
    default:
      break;
  }
}

/// /note —— 把当前 thread 最近一条已完成的 assistant 回复存成笔记
/// （笔记功能，独立于 Wiki；docs/BiuMind-Notes-Design-Draft.md D6：
/// 聊天 → 笔记单向互通）。纯本地乐观写 + outbox 上行，离线可用。
Future<void> _saveLastReplyAsNote(
  BuildContext context,
  WidgetRef ref,
  String threadId,
) async {
  final notesRepo = ref.read(notesRepositoryProvider);
  if (notesRepo == null) return;
  final chatRepo = ref.read(chatControllerDepsProvider).repo;
  final messages = await chatRepo.watchMessages(threadId).first;
  final reply = messages.lastWhereOrNull(
    (m) =>
        m.role == MessageRole.assistant &&
        m.status == MessageStatus.completed &&
        m.assembledText.trim().isNotEmpty,
  );
  if (!context.mounted) return;
  if (reply == null) {
    ScaffoldMessenger.of(context).showSnackBar(
      const SnackBar(content: Text('还没有可保存的回复')),
    );
    return;
  }
  final content = reply.assembledText.trim();
  // 标题取首行截断；空行退化为默认名。
  final firstLine =
      content.split('\n').firstWhere((l) => l.trim().isNotEmpty).trim();
  final title =
      firstLine.length > 50 ? '${firstLine.substring(0, 50)}…' : firstLine;
  await notesRepo.createNote(title: title, contentMd: content);
  if (!context.mounted) return;
  ScaffoldMessenger.of(context).showSnackBar(
    SnackBar(
      content: const Text('已存为笔记'),
      action: SnackBarAction(
        label: '查看',
        onPressed: () => context.go('/notes'),
      ),
    ),
  );
}

/// Cmd/Ctrl+Shift+M 触发的模型快速选择 —— 走统一的 ModelPickerDialogV2,
/// 跟 composer 里的模型选择入口同款,免得用户看到两套体验。
Future<void> _showModelPickerDialog(
  BuildContext context,
  WidgetRef ref,
  String threadId,
) async {
  final repo = ref.read(chatControllerDepsProvider).repo;
  final currentThread = await repo.getThread(threadId);
  if (!context.mounted) return;
  final picked = await showModelPickerDialog(
    context,
    currentModel: currentThread?.model,
    currentProviderId: currentThread?.providerId,
  );
  if (picked == null) return;
  await repo.setThreadModel(
    threadId,
    picked.modelCode,
    providerId: picked.providerId,
  );
}

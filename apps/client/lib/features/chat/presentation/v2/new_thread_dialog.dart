// NewThreadDialog —— Chat 重构 R6。
//
// 让用户决定：
//   1. mode：chat / agent / task
//   2. chat → 选 model（chatModelGroupsProvider 下拉：official=relay / BYOK=brain）
//   3. agent → 选 environment（agentEnvironmentsProvider 拉 online worker）
//   4. task → 输 poolTag（runtime 池标签；空字符串 = 用默认池）
//
// 创建后调 ChatRepo.createThread 拿 thread 实例 + 通过 onCreated 回调。
//
// UI：modal dialog，左边垂直 segmented mode 选择，右边随 mode 变化的
// 配置面板。提交按钮在右下。

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:logging/logging.dart';
import 'package:uuid/uuid.dart';

import '../../../../core/ui/biu_text_field.dart';
import '../../../../data/agent_plane/environment.dart';
import '../../../../data/providers_providers.dart' show providersListProvider;
import '../../../../l10n/app_localizations.dart';
import '../../application/chat_controller.dart';
import '../../application/chat_preferences.dart';
import '../../application/new_thread_memory.dart';
import '../../domain/chat_models.dart';
import '../../domain/thread_title.dart';

/// helper —— 在 [ctx] 上弹 NewThreadDialog；用户取消返 null。
/// 创建成功返新 thread 的 id。[projectId] 非 null 时新建的 thread 自动
/// 落到该 project（wiki 项目内嵌面板用）。
Future<String?> showNewThreadDialog(BuildContext ctx, {String? projectId}) {
  return showDialog<String>(
    context: ctx,
    builder: (_) => NewThreadDialog(projectId: projectId),
  );
}

/// 直接按用户默认偏好新建会话(不弹 NewThreadDialog)。"+"/「新建空白对话」/
/// `/new` 都走这里——一键建会话,无需任何选择。返回新 thread id;失败返 null。
///
/// 默认:mode = prefs.defaultMode(出厂 = agent/智能)、model = prefs.defaultModel
/// (空 = BiuMind 官方默认)、runtimeEnvMode 按 mode 推导(chat=none / agent=local /
/// task=cloud)。agent 模式自动绑定首台在线设备(biu_daemon/biu_cli);无在线设备时
/// env 留空照常建会话(createThread 仅本地 Drift 插入、不碰 brain,设备只在发消息时
/// 才需要),用户可后续在 composer 模式切换里绑定。需要标题/系统提示/指定 worker/
/// Task 池等高级配置时仍可调 [showNewThreadDialog]。
Future<String?> createDefaultThread(WidgetRef ref, {String? projectId}) async {
  final prefs = ref.read(chatPreferencesProvider);
  final repo = ref.read(chatControllerDepsProvider).repo;
  final id = const Uuid().v4();
  final mode = prefs.defaultMode;
  final model = (prefs.defaultModel != null && prefs.defaultModel!.isNotEmpty)
      ? prefs.defaultModel
      : null;
  final providerId = model == null ? null : prefs.defaultProviderId;
  final runtimeEnvMode = switch (mode) {
    ThreadMode.chat => 'none',
    ThreadMode.agent => 'local',
    ThreadMode.task => 'cloud',
  };
  String? envId;
  if (mode == ThreadMode.agent) {
    // 强制 refresh 一次,避免 autoDispose 缓存里已被 brain GC 的过期 daemon
    // 导致后续 createSession 404(与 composer 模式切换同款防御)。
    ref.invalidate(agentEnvironmentsProvider);
    try {
      final envs = await ref.read(agentEnvironmentsProvider.future);
      final online = envs
          .where((e) =>
              e.isOnline &&
              (e.workerKind == 'biu_daemon' || e.workerKind == 'biu_cli'))
          .toList();
      if (online.isNotEmpty) envId = online.first.environmentId;
    } catch (_) {/* 拉设备失败照常建会话,env 留空 */}
  }
  try {
    await repo.createThread(
      id: id,
      mode: mode,
      environmentId: envId,
      model: model,
      providerId: providerId,
      runtimeEnvMode: runtimeEnvMode,
      projectId: projectId,
    );
    return id;
  } catch (_) {
    return null;
  }
}

class NewThreadDialog extends ConsumerStatefulWidget {
  const NewThreadDialog({super.key, this.projectId});
  final String? projectId;

  @override
  ConsumerState<NewThreadDialog> createState() => _NewThreadDialogState();
}

class _NewThreadDialogState extends ConsumerState<NewThreadDialog> {
  static const _uuid = Uuid();

  ThreadMode _mode = ThreadMode.chat;
  String _chatModel = 'biumind-default';
  // 与 _chatModel 配对的 provider slug —— 路由消歧(同 code 可能在官方 +
  // BYOK provider 下都有)。'biumind-default' 时为 null。
  String? _chatProviderId;
  String? _agentEnvId;
  // Agent loop backend (Runtime v3 R3/Q3): 'biumindkit'(默认) | 'claude-cli'。
  // 仅 agent 模式可选。claude-cli=外部 Claude Code(用你的订阅,不计 biumind 额度)。
  String _agentBackend = 'biumindkit';
  // Agent 工具执行环境 (Runtime v3 轴 B): 'local'(本机 daemon,默认) |
  // 'cloud'(云容器沙箱)。chat 恒 none / task 恒 cloud,只有 agent 可由用户
  // 在 local↔cloud 间切。cloud 后端(services/sandbox,R5)尚未就绪,选择器里
  // cloud 档置灰("即将上线"),所以当前实际值恒 local —— UI 先行,R5 落地后
  // 解开置灰即可用,不必再动这里的状态线。
  String _agentRuntimeEnv = 'local';
  final _poolTagCtrl = TextEditingController();
  final _titleCtrl = TextEditingController();
  final _systemPromptCtrl = TextEditingController();

  bool _submitting = false;
  String? _submitError;

  @override
  void initState() {
    super.initState();
    // 应用全局偏好：默认 mode + 默认模型（chat 模式下）。
    final prefs = ref.read(chatPreferencesProvider);
    _mode = prefs.defaultMode;
    if (prefs.defaultModel != null && prefs.defaultModel!.isNotEmpty) {
      _chatModel = prefs.defaultModel!;
      _chatProviderId = prefs.defaultProviderId;
    }
    // (诊断阶段) 暂不在 initState 主动 invalidate — 先看 _AgentModePanel
    // 的 ref.watch 能不能正常拉数据。如果第一次 watch 就卡 loading 说明
    // 是 listEnvironments 在调用层 hang;如果拿到 stale data 才需要刷新。
    // systemPrompt / title 都 listen 一下让 hint 能联动重算。
    _systemPromptCtrl.addListener(_onAnyChange);
    _titleCtrl.addListener(_onAnyChange);
    // 异步取上次记忆字段（systemPrompt + poolTag）预填。
    NewThreadMemoryStore.load().then((m) {
      if (!mounted) return;
      setState(() {
        if (_systemPromptCtrl.text.isEmpty && m.systemPrompt.isNotEmpty) {
          _systemPromptCtrl.text = m.systemPrompt;
        }
        if (_poolTagCtrl.text.isEmpty && m.poolTag.isNotEmpty) {
          _poolTagCtrl.text = m.poolTag;
        }
      });
    });
  }

  void _onAnyChange() {
    if (mounted) setState(() {});
  }

  /// 工具执行环境（Runtime v3 轴 B）：chat 恒无外设（none）、task 恒云容器
  /// （cloud）；agent 由 _RuntimeEnvSelector 在 local↔cloud 间选（_agentRuntimeEnv，
  /// 默认 local）。cloud 后端（R5）就绪前选择器里 cloud 置灰,故此值实际恒 local。
  String _runtimeEnvForMode() => switch (_mode) {
        ThreadMode.chat => 'none',
        ThreadMode.agent => _agentRuntimeEnv,
        ThreadMode.task => 'cloud',
      };

  /// 用 systemPrompt 推荐一个标题；空时返空。已经手动输入 title 不抢。
  String _suggestedTitle() {
    if (_titleCtrl.text.trim().isNotEmpty) return '';
    return titleFromPrompt(_systemPromptCtrl.text);
  }

  @override
  void dispose() {
    _systemPromptCtrl.removeListener(_onAnyChange);
    _titleCtrl.removeListener(_onAnyChange);
    _poolTagCtrl.dispose();
    _titleCtrl.dispose();
    _systemPromptCtrl.dispose();
    super.dispose();
  }

  bool get _canSubmit {
    if (_submitting) return false;
    return switch (_mode) {
      ThreadMode.chat => true,
      ThreadMode.agent => _agentEnvId != null,
      ThreadMode.task => true, // poolTag 空也算合法（用默认池）
    };
  }

  /// 模型选择回调 —— code='biumind-default' 时 providerId 必为 null。
  void _onModelChanged(String code, String? providerId) {
    setState(() {
      _chatModel = code;
      _chatProviderId = code == 'biumind-default' ? null : providerId;
    });
  }

  Future<void> _submit() async {
    if (!_canSubmit) return;
    setState(() {
      _submitting = true;
      _submitError = null;
    });
    try {
      final repo = ref.read(chatControllerDepsProvider).repo;
      final id = _uuid.v4();
      // 标题为空 + systemPrompt 有内容时自动用 _suggestedTitle 兜底，避免
       // sidebar 一长串"新对话"。
      final effectiveTitle = _titleCtrl.text.trim().isEmpty
          ? titleFromPrompt(_systemPromptCtrl.text)
          : _titleCtrl.text.trim();
      await repo.createThread(
        id: id,
        title: effectiveTitle,
        mode: _mode,
        environmentId: _mode == ThreadMode.agent ? _agentEnvId : null,
        poolTag: _mode == ThreadMode.task
            ? (_poolTagCtrl.text.trim().isEmpty
                ? null
                : _poolTagCtrl.text.trim())
            : null,
        // 三种模式都可指定 model（agent / task brain ChatRunner 也用同一个
        // 字段路由）。biumind-default 留空让 brain fallback。providerId 与
        // model 配对消歧(BYOK 路由),默认时同样留空。
        model: _chatModel == 'biumind-default' ? null : _chatModel,
        providerId: _chatModel == 'biumind-default' ? null : _chatProviderId,
        systemPrompt: _systemPromptCtrl.text.trim().isEmpty
            ? null
            : _systemPromptCtrl.text.trim(),
        projectId: widget.projectId,
        runtimeEnvMode: _runtimeEnvForMode(),
        backend: _mode == ThreadMode.agent ? _agentBackend : 'biumindkit',
      );
      // 记忆字段：systemPrompt + poolTag —— 下次打开 dialog 自动预填。
      // unawaited，不阻塞 pop。
      NewThreadMemoryStore.save(NewThreadMemory(
        systemPrompt: _systemPromptCtrl.text.trim(),
        poolTag: _poolTagCtrl.text.trim(),
      ));
      if (mounted) Navigator.of(context).pop(id);
    } catch (e) {
      if (mounted) {
        setState(() {
          _submitting = false;
          _submitError = e.toString();
        });
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    final l = AppLocalizations.of(context)!;
    return Dialog(
      child: ConstrainedBox(
        constraints: const BoxConstraints(maxWidth: 640, maxHeight: 520),
        child: Padding(
          padding: const EdgeInsets.all(20),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Row(
                children: [
                  Text(
                    l.chatV2NewDialogTitle,
                    style: const TextStyle(
                        fontSize: 18, fontWeight: FontWeight.w600),
                  ),
                  const Spacer(),
                  IconButton(
                    icon: const Icon(Icons.close, size: 18),
                    onPressed: () => Navigator.of(context).pop(),
                  ),
                ],
              ),
              const SizedBox(height: 12),
              Expanded(
                child: Row(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    SizedBox(
                      width: 140,
                      child: _ModeSelector(
                        current: _mode,
                        onChanged: (m) => setState(() => _mode = m),
                      ),
                    ),
                    const VerticalDivider(width: 24),
                    Expanded(child: _buildModePanel()),
                  ],
                ),
              ),
              if (_submitError != null) ...[
                const SizedBox(height: 8),
                Text(
                  _submitError!,
                  style: TextStyle(
                    color: Theme.of(context).colorScheme.error,
                    fontSize: 12,
                  ),
                ),
              ],
              const SizedBox(height: 12),
              Row(
                mainAxisAlignment: MainAxisAlignment.end,
                children: [
                  TextButton(
                    onPressed: _submitting ? null : () => Navigator.of(context).pop(),
                    child: Text(l.chatV2DialogCancel),
                  ),
                  const SizedBox(width: 8),
                  FilledButton(
                    onPressed: _canSubmit ? _submit : null,
                    child: _submitting
                        ? const SizedBox(
                            width: 14, height: 14,
                            child: CircularProgressIndicator(strokeWidth: 2),
                          )
                        : Text(l.chatV2NewDialogCreate),
                  ),
                ],
              ),
            ],
          ),
        ),
      ),
    );
  }

  Widget _buildModePanel() {
    final l = AppLocalizations.of(context)!;
    return SingleChildScrollView(
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          BiuTextField(
            controller: _titleCtrl,
            labelText: l.chatV2NewDialogTitleField,
            hintText: _suggestedTitle().isEmpty
                ? null
                : l.chatV2NewDialogTitleSuggested(_suggestedTitle()),
          ),
          const SizedBox(height: 12),
          switch (_mode) {
            ThreadMode.chat => _ChatModePanel(
                selectedModel: _chatModel,
                selectedProviderId: _chatProviderId,
                onModelChanged: _onModelChanged,
                systemPromptCtrl: _systemPromptCtrl,
              ),
            ThreadMode.agent => _AgentModePanel(
                selectedEnvId: _agentEnvId,
                onEnvChanged: (id) => setState(() => _agentEnvId = id),
                selectedModel: _chatModel,
                selectedProviderId: _chatProviderId,
                onModelChanged: _onModelChanged,
                systemPromptCtrl: _systemPromptCtrl,
                selectedBackend: _agentBackend,
                onBackendChanged: (b) => setState(() => _agentBackend = b),
                selectedRuntimeEnv: _agentRuntimeEnv,
                onRuntimeEnvChanged: (v) =>
                    setState(() => _agentRuntimeEnv = v),
              ),
            ThreadMode.task => _TaskModePanel(
                poolTagCtrl: _poolTagCtrl,
                selectedModel: _chatModel,
                selectedProviderId: _chatProviderId,
                onModelChanged: _onModelChanged,
                systemPromptCtrl: _systemPromptCtrl,
              ),
          },
        ],
      ),
    );
  }
}

class _ModeSelector extends StatelessWidget {
  const _ModeSelector({required this.current, required this.onChanged});
  final ThreadMode current;
  final ValueChanged<ThreadMode> onChanged;

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        for (final m in ThreadMode.values)
          _ModeTile(
            mode: m,
            selected: m == current,
            onTap: () => onChanged(m),
          ),
      ],
    );
  }
}

class _ModeTile extends StatelessWidget {
  const _ModeTile({required this.mode, required this.selected, required this.onTap});
  final ThreadMode mode;
  final bool selected;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final l = AppLocalizations.of(context)!;
    final (icon, label, hint) = switch (mode) {
      ThreadMode.chat => (
          Icons.chat_bubble_outline,
          l.chatV2NewDialogModeChat,
          l.chatV2NewDialogModeChatHint
        ),
      ThreadMode.agent => (
          Icons.auto_awesome,
          l.chatV2NewDialogModeAgent,
          l.chatV2NewDialogModeAgentHint
        ),
      ThreadMode.task => (
          Icons.bolt_outlined,
          l.chatV2NewDialogModeTask,
          l.chatV2NewDialogModeTaskHint
        ),
    };
    return InkWell(
      onTap: onTap,
      borderRadius: BorderRadius.circular(6),
      child: Container(
        margin: const EdgeInsets.only(bottom: 6),
        padding: const EdgeInsets.all(10),
        decoration: BoxDecoration(
          color: selected ? theme.colorScheme.primaryContainer : null,
          borderRadius: BorderRadius.circular(6),
          border: Border.all(
            color: selected ? theme.colorScheme.primary : theme.colorScheme.outlineVariant,
          ),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                Icon(icon, size: 14),
                const SizedBox(width: 6),
                Text(label, style: const TextStyle(fontWeight: FontWeight.w600)),
              ],
            ),
            const SizedBox(height: 2),
            Text(
              hint,
              style: theme.textTheme.bodySmall?.copyWith(
                color: theme.colorScheme.onSurfaceVariant,
              ),
            ),
          ],
        ),
      ),
    );
  }
}

/// 模型选择回调 —— code='biumind-default' 时 providerId 为 null。
typedef _ModelPicked = void Function(String code, String? providerId);

/// 三种模式共用：模型 dropdown + 系统提示。Agent / Task 模式发 createSession
/// 时 brain ChatRunner / runtime worker 都用 model 字段决定 LLM。
///
/// 模型列表走**用户面** availableChatModelsProvider(providersList + 每
/// provider 的 /v1/providers/{id}/models,已过滤 type=chat)。改自原先的
/// admin-only /v1/admin/models —— 那条路非 admin 用户拿到空列表,且混入
/// embedding / tts / 图像 / 视频等非对话模型。每项带 providerId 供路由消歧。
class _ModelAndSystemFields extends ConsumerWidget {
  const _ModelAndSystemFields({
    required this.selectedModel,
    required this.selectedProviderId,
    required this.onModelChanged,
    required this.systemPromptCtrl,
  });
  final String selectedModel; // code 或 'biumind-default'
  final String? selectedProviderId;
  final _ModelPicked onModelChanged;
  final TextEditingController systemPromptCtrl;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final l = AppLocalizations.of(context)!;
    final modelsAsync = ref.watch(availableChatModelsProvider);
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Row(
          children: [
            _Label(l.chatV2NewDialogModelLabel),
            const Spacer(),
            IconButton(
              icon: const Icon(Icons.refresh, size: 16),
              tooltip: l.chatV2NewDialogRefreshTooltip,
              onPressed: () {
                // 重拉 provider 列表 + 派生的 chat 模型列表。
                ref.invalidate(providersListProvider);
                ref.invalidate(availableChatModelsProvider);
              },
              padding: EdgeInsets.zero,
              constraints: const BoxConstraints(minWidth: 24, minHeight: 24),
            ),
          ],
        ),
        modelsAsync.when(
          data: (models) {
            final byKey = {for (final m in models) m.routeKey: m};
            // 当前选中 → routeKey。优先精确 (code, providerId) 匹配;老 prefs
            // 只有 code(无 providerId)时退而匹配同 code 第一项;都找不到回退
            // 默认,避免 dropdown "exactly one item with value" 异常。
            String currentKey = 'biumind-default';
            if (selectedModel != 'biumind-default') {
              AvailableChatModel? match;
              for (final m in models) {
                if (m.code == selectedModel) {
                  match = m;
                  if (m.providerId == selectedProviderId) break;
                }
              }
              if (match != null) currentKey = match.routeKey;
            }
            return DropdownButtonFormField<String>(
              initialValue: currentKey,
              isDense: true,
              decoration: const InputDecoration(
                isDense: true,
                border: OutlineInputBorder(),
              ),
              items: [
                DropdownMenuItem(
                  value: 'biumind-default',
                  child: Text(l.chatV2NewDialogModelOfficial),
                ),
                for (final m in models)
                  DropdownMenuItem(
                    value: m.routeKey,
                    child: Text(m.label, overflow: TextOverflow.ellipsis),
                  ),
                if (models.isEmpty)
                  DropdownMenuItem(
                    value: '__empty__',
                    enabled: false,
                    child: Text(l.chatV2NewDialogModelEmpty),
                  ),
              ],
              onChanged: (v) {
                if (v == null || v == '__empty__') return;
                if (v == 'biumind-default') {
                  onModelChanged('biumind-default', null);
                  return;
                }
                final m = byKey[v];
                if (m != null) onModelChanged(m.code, m.providerId);
              },
            );
          },
          loading: () => const Padding(
            padding: EdgeInsets.symmetric(vertical: 8),
            child: LinearProgressIndicator(),
          ),
          error: (e, _) => Container(
            padding: const EdgeInsets.all(8),
            decoration: BoxDecoration(
              color: Theme.of(context).colorScheme.errorContainer,
              borderRadius: BorderRadius.circular(4),
            ),
            child: Text(
              l.chatV2NewDialogModelLoadFailed('$e'),
              style: Theme.of(context).textTheme.bodySmall,
            ),
          ),
        ),
        const SizedBox(height: 12),
        _Label(l.chatV2NewDialogSystemPromptLabel),
        BiuTextField(
          controller: systemPromptCtrl,
          minLines: 3,
          maxLines: 6,
          hintText: l.chatV2NewDialogSystemPromptHint,
        ),
      ],
    );
  }
}

class _ChatModePanel extends StatelessWidget {
  const _ChatModePanel({
    required this.selectedModel,
    required this.selectedProviderId,
    required this.onModelChanged,
    required this.systemPromptCtrl,
  });
  final String selectedModel;
  final String? selectedProviderId;
  final _ModelPicked onModelChanged;
  final TextEditingController systemPromptCtrl;

  @override
  Widget build(BuildContext context) {
    return _ModelAndSystemFields(
      selectedModel: selectedModel,
      selectedProviderId: selectedProviderId,
      onModelChanged: onModelChanged,
      systemPromptCtrl: systemPromptCtrl,
    );
  }
}

/// 范围决策（2026-06，见 docs/BiuMind-Runtime-v3-Design.md §8.2）:聊天区当前
/// 只支持 biu cli（biumindkit），Agent 模式不开放 Claude Code / Codex 外部 CLI
/// backend（它们仍属独立「编码」模块）。故 backend 选择器隐藏（backend 恒
/// biumindkit）。真要在聊天区重启外部 backend 时把此开关置 true 即可恢复。
const bool kChatAllowExternalBackends = false;

/// Agent loop backend 选择器（Runtime v3 R3/Q3 + R8）。biumindkit（内建,默认）
/// / Claude Code / Codex（外部 CLI 当 backend）。选外部 CLI 时明示「用你的订阅、
/// 不计 biumind 额度」(A1 透明度)。当前由 kChatAllowExternalBackends 关闭。
class _BackendSelector extends StatelessWidget {
  const _BackendSelector({required this.selected, required this.onChanged});
  final String selected;
  final ValueChanged<String> onChanged;

  @override
  Widget build(BuildContext context) {
    final cs = Theme.of(context).colorScheme;
    Widget tile(String value, String label, IconData icon) {
      final on = selected == value;
      return Expanded(
        child: InkWell(
          onTap: () => onChanged(value),
          borderRadius: BorderRadius.circular(8),
          child: Container(
            padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
            decoration: BoxDecoration(
              color: on ? cs.primaryContainer : cs.surfaceContainerHighest,
              borderRadius: BorderRadius.circular(8),
              border: Border.all(color: on ? cs.primary : cs.outlineVariant),
            ),
            child: Row(
              children: [
                Icon(icon, size: 16,
                    color: on ? cs.primary : cs.onSurfaceVariant),
                const SizedBox(width: 8),
                Text(label,
                    style: TextStyle(
                        fontSize: 13,
                        fontWeight: on ? FontWeight.w600 : FontWeight.w400)),
              ],
            ),
          ),
        ),
      );
    }

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        const Text('Agent backend',
            style: TextStyle(fontSize: 12, fontWeight: FontWeight.w500)),
        const SizedBox(height: 6),
        Row(children: [
          tile('biumindkit', 'BiuMind 内建', Icons.hub_outlined),
          const SizedBox(width: 8),
          tile('claude-cli', 'Claude Code', Icons.terminal),
          const SizedBox(width: 8),
          tile('codex-cli', 'Codex', Icons.code_rounded),
        ]),
        if (selected == 'claude-cli') ...[
          const SizedBox(height: 6),
          Text(
            '用你本机的 Claude Code 订阅(~/.claude)运行,工具由 Claude Code 自己执行;'
            '此对话不计入 BiuMind 额度。需目标设备已装 claude CLI 并登录。',
            style: TextStyle(fontSize: 11.5, color: cs.onSurfaceVariant),
          ),
        ],
        if (selected == 'codex-cli') ...[
          const SizedBox(height: 6),
          Text(
            '用你本机的 Codex CLI 订阅运行,工具由 Codex 自己执行;此对话不计入 '
            'BiuMind 额度。需目标设备已装 codex CLI 并登录。',
            style: TextStyle(fontSize: 11.5, color: cs.onSurfaceVariant),
          ),
        ],
      ],
    );
  }
}

/// Runtime v3 轴 B 工具执行环境选择器(仅 agent 模式)。
///
/// 两档:
///   * local —— 工具在你选定的本机/在线 daemon 上执行(下方 worker 列表选具体设备)。
///   * cloud —— 工具在 BiuMind 云端容器沙箱里执行。后端(services/sandbox,R5)
///     尚未就绪,此档置灰 + "即将上线"角标,不可选;解开置灰只需删掉下面的
///     _cloudReady=false 判断(届时还要在 _AgentModePanel 里按 cloud 隐藏 worker
///     列表 + 显示池/容器配置)。
class _RuntimeEnvSelector extends StatelessWidget {
  const _RuntimeEnvSelector({required this.selected, required this.onChanged});
  final String selected;
  final ValueChanged<String> onChanged;

  // R5 云沙箱就绪后置 true(或接真实能力探测)解开 cloud 档。
  static const bool _cloudReady = false;

  @override
  Widget build(BuildContext context) {
    final cs = Theme.of(context).colorScheme;
    Widget tile(String value, String label, IconData icon,
        {required bool enabled, String? badge}) {
      final on = selected == value;
      final bg = !enabled
          ? cs.surfaceContainerHighest.withValues(alpha: 0.4)
          : (on ? cs.primaryContainer : cs.surfaceContainerHighest);
      final fg = !enabled
          ? cs.onSurfaceVariant.withValues(alpha: 0.5)
          : (on ? cs.primary : cs.onSurfaceVariant);
      return Expanded(
        child: InkWell(
          onTap: enabled ? () => onChanged(value) : null,
          borderRadius: BorderRadius.circular(8),
          child: Container(
            padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
            decoration: BoxDecoration(
              color: bg,
              borderRadius: BorderRadius.circular(8),
              border: Border.all(
                  color: enabled && on ? cs.primary : cs.outlineVariant),
            ),
            child: Row(
              children: [
                Icon(icon, size: 16, color: fg),
                const SizedBox(width: 8),
                Text(label,
                    style: TextStyle(
                        fontSize: 13,
                        color: fg,
                        fontWeight:
                            enabled && on ? FontWeight.w600 : FontWeight.w400)),
                if (badge != null) ...[
                  const SizedBox(width: 6),
                  Container(
                    padding:
                        const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
                    decoration: BoxDecoration(
                      color: cs.surfaceContainerHigh,
                      borderRadius: BorderRadius.circular(4),
                    ),
                    child: Text(badge,
                        style:
                            TextStyle(fontSize: 10, color: cs.onSurfaceVariant)),
                  ),
                ],
              ],
            ),
          ),
        ),
      );
    }

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        const Text('执行环境',
            style: TextStyle(fontSize: 12, fontWeight: FontWeight.w500)),
        const SizedBox(height: 6),
        Row(children: [
          tile('local', '本机', Icons.computer_outlined, enabled: true),
          const SizedBox(width: 8),
          tile('cloud', '云端沙箱', Icons.cloud_outlined,
              enabled: _cloudReady, badge: _cloudReady ? null : '即将上线'),
        ]),
        const SizedBox(height: 6),
        Text(
          selected == 'cloud'
              ? '工具在 BiuMind 云端容器沙箱里执行,无需本机在线。'
              : '工具在你选定的在线设备(下方)上执行。',
          style: TextStyle(fontSize: 11.5, color: cs.onSurfaceVariant),
        ),
      ],
    );
  }
}

class _AgentModePanel extends ConsumerWidget {
  const _AgentModePanel({
    required this.selectedEnvId,
    required this.onEnvChanged,
    required this.selectedModel,
    required this.selectedProviderId,
    required this.onModelChanged,
    required this.systemPromptCtrl,
    required this.selectedBackend,
    required this.onBackendChanged,
    required this.selectedRuntimeEnv,
    required this.onRuntimeEnvChanged,
  });
  final String? selectedEnvId;
  final ValueChanged<String> onEnvChanged;
  final String selectedModel;
  final String? selectedProviderId;
  final _ModelPicked onModelChanged;
  final TextEditingController systemPromptCtrl;
  final String selectedBackend;
  final ValueChanged<String> onBackendChanged;
  final String selectedRuntimeEnv;
  final ValueChanged<String> onRuntimeEnvChanged;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final l = AppLocalizations.of(context)!;
    final async = ref.watch(agentEnvironmentsProvider);
    Logger('biumind.new_thread_dialog').info(
        '_AgentModePanel.build: envs state=${async.runtimeType} hasValue=${async.hasValue} hasError=${async.hasError}');
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        _ModelAndSystemFields(
          selectedModel: selectedModel,
          selectedProviderId: selectedProviderId,
          onModelChanged: onModelChanged,
          systemPromptCtrl: systemPromptCtrl,
        ),
        const SizedBox(height: 12),
        // 工具执行环境(Runtime v3 轴 B)—— 本机 daemon / 云端沙箱。
        // cloud 后端(R5)未就绪前置灰。
        _RuntimeEnvSelector(
          selected: selectedRuntimeEnv,
          onChanged: onRuntimeEnvChanged,
        ),
        const SizedBox(height: 12),
        // 聊天区只支持 biu cli(biumindkit):隐藏外部 backend 选择器,backend
        // 恒默认 biumindkit。恢复见 kChatAllowExternalBackends。
        if (kChatAllowExternalBackends) ...[
          _BackendSelector(
            selected: selectedBackend,
            onChanged: onBackendChanged,
          ),
          const SizedBox(height: 12),
        ],
        Row(
          children: [
            _Label(l.chatV2NewDialogPickWorker),
            const Spacer(),
            IconButton(
              icon: const Icon(Icons.refresh, size: 16),
              tooltip: l.chatV2NewDialogRefreshTooltip,
              onPressed: () => ref.invalidate(agentEnvironmentsProvider),
            ),
          ],
        ),
        async.when(
          data: (envs) {
            // Agent 模式 brain 端只接受 biu_daemon / biu_cli。runtime
            // worker 走 Task 模式（共享池），列出来选了也是 400
            // wrong_worker_kind，这里直接过滤掉。
            final agentCapable = envs
                .where((e) => e.isOnline)
                .where((e) =>
                    e.workerKind == 'biu_daemon' || e.workerKind == 'biu_cli')
                .toList();
            if (agentCapable.isEmpty) {
              return _EmptyEnvHint(allEnvs: envs);
            }
            // 默认 agent 模式下用户「不进行选择」即可创建:首个在线设备到位后
            // 自动选中(与 composer 模式切换的自动绑定行为一致)。仅在尚未选时
            // 兜底,用户手动改过则不抢。post-frame 调用避免 build 中 setState。
            if (selectedEnvId == null) {
              final auto = agentCapable.first.environmentId;
              WidgetsBinding.instance.addPostFrameCallback((_) {
                onEnvChanged(auto);
              });
            }
            return Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                for (final e in agentCapable)
                  _EnvTile(
                    env: e,
                    selected: e.environmentId == selectedEnvId,
                    onTap: () => onEnvChanged(e.environmentId),
                  ),
              ],
            );
          },
          loading: () => const Padding(
            padding: EdgeInsets.symmetric(vertical: 16),
            child: Center(child: CircularProgressIndicator()),
          ),
          error: (e, _) => Padding(
            padding: const EdgeInsets.all(8),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  l.chatV2NewDialogEnvLoadFailed('$e'),
                  style: TextStyle(color: Theme.of(context).colorScheme.error),
                ),
                const SizedBox(height: 8),
                // brain transient 重启 / 网络抖动后,provider 已经走过
                // 3 次 retry 还是挂的话才走到这里。给一个显式重试按钮
                // 让用户不用关弹窗就能再来一次 — 比"加载失败"裸文字
                // 友好得多。
                Align(
                  alignment: Alignment.centerLeft,
                  child: OutlinedButton.icon(
                    icon: const Icon(Icons.refresh, size: 16),
                    label: Text(l.chatV2NewDialogRefreshTooltip),
                    onPressed: () =>
                        ref.invalidate(agentEnvironmentsProvider),
                  ),
                ),
              ],
            ),
          ),
        ),
      ],
    );
  }
}

class _EmptyEnvHint extends StatelessWidget {
  const _EmptyEnvHint({required this.allEnvs});
  final List<AgentEnvironment> allEnvs;
  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final l = AppLocalizations.of(context)!;
    return Container(
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: theme.colorScheme.surfaceContainerHighest,
        borderRadius: BorderRadius.circular(6),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(l.chatV2NewDialogNoOnlineDaemon),
          const SizedBox(height: 4),
          Text(
            allEnvs.isEmpty
                ? l.chatV2NewDialogEmptyEnvAuto
                : l.chatV2NewDialogEmptyEnvHistory(allEnvs.length),
            style: theme.textTheme.bodySmall?.copyWith(
              color: theme.colorScheme.onSurfaceVariant,
            ),
          ),
        ],
      ),
    );
  }
}

class _EnvTile extends StatelessWidget {
  const _EnvTile({required this.env, required this.selected, required this.onTap});
  final AgentEnvironment env;
  final bool selected;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return InkWell(
      onTap: onTap,
      child: Container(
        margin: const EdgeInsets.only(bottom: 6),
        padding: const EdgeInsets.all(10),
        decoration: BoxDecoration(
          color: selected ? theme.colorScheme.primaryContainer : null,
          borderRadius: BorderRadius.circular(6),
          border: Border.all(
            color: selected ? theme.colorScheme.primary : theme.colorScheme.outlineVariant,
          ),
        ),
        child: Row(
          children: [
            Container(
              width: 8, height: 8,
              decoration: const BoxDecoration(
                color: Colors.green,
                shape: BoxShape.circle,
              ),
            ),
            const SizedBox(width: 8),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(env.machineName.isEmpty ? env.environmentId : env.machineName,
                      style: const TextStyle(fontWeight: FontWeight.w600)),
                  if (env.osArch != null)
                    Text(
                      '${env.workerKind} · ${env.osArch}',
                      style: theme.textTheme.bodySmall?.copyWith(
                        color: theme.colorScheme.onSurfaceVariant,
                      ),
                    ),
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _TaskModePanel extends StatelessWidget {
  const _TaskModePanel({
    required this.poolTagCtrl,
    required this.selectedModel,
    required this.selectedProviderId,
    required this.onModelChanged,
    required this.systemPromptCtrl,
  });
  final TextEditingController poolTagCtrl;
  final String selectedModel;
  final String? selectedProviderId;
  final _ModelPicked onModelChanged;
  final TextEditingController systemPromptCtrl;

  @override
  Widget build(BuildContext context) {
    final l = AppLocalizations.of(context)!;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        _ModelAndSystemFields(
          selectedModel: selectedModel,
          selectedProviderId: selectedProviderId,
          onModelChanged: onModelChanged,
          systemPromptCtrl: systemPromptCtrl,
        ),
        const SizedBox(height: 12),
        _Label(l.chatV2NewDialogPoolTagLabel),
        BiuTextField(
          controller: poolTagCtrl,
          hintText: l.chatV2NewDialogPoolTagHint,
        ),
        const SizedBox(height: 12),
        Text(
          l.chatV2NewDialogTaskModeHint,
          style: Theme.of(context).textTheme.bodySmall?.copyWith(
                color: Theme.of(context).colorScheme.onSurfaceVariant,
              ),
        ),
      ],
    );
  }
}

class _Label extends StatelessWidget {
  const _Label(this.text);
  final String text;
  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.only(bottom: 4),
      child: Text(text, style: Theme.of(context).textTheme.labelMedium),
    );
  }
}

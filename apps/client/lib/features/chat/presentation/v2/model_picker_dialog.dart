// ModelPickerDialogV2 —— lobehub 同款 chat 模型选择器。
//
// 数据源: chatModelGroupsProvider —— official (brain 平台池) + identity
// BYOK providers (用户在「API Keys」配的 key)。模型只在 official 或用户持
// 有效 identity key 时列出, 故不会显"选了用不了"的模型。P3: brain 不再存
// key, picker 不再依赖 brain 的 per-user provider 行作"已配置"信号。
//
// 形态:
//   ┌─────────────────────────────────────────────────┐
//   │ 🔍 搜索模型…                                ✕ │
//   ├─────────────────────────────────────────────────┤
//   │  BiuMind Cloud                              ⚙ │ official → /membership
//   │      Claude Sonnet 4.6                  200K    │
//   ├─────────────────────────────────────────────────┤
//   │  Anthropic                                  ⚙ │ BYOK → /settings API Keys
//   │      Claude Opus 4.7                    200K    │
//   └─────────────────────────────────────────────────┘
//
// 选中后,通过 onPicked 回调返回 (modelCode, providerSlug)。调用方调
// chat_repo.setThreadModel(threadId, modelCode, providerId: providerSlug)。

import 'package:flutter/material.dart' hide showAdaptiveDialog;
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../../core/ui/adaptive_dialog.dart';
import '../../../../core/ui/biu_text_field.dart';
import '../../../../l10n/app_localizations.dart';
import '../../../settings/presentation/settings_page.dart'
    show SettingsTab, activeSettingsTabProvider;
import '../../data/chat_model_groups.dart';

/// picker 选中结果 —— 同时给 modelCode + providerSlug, 调用方据此 set
/// thread.model + thread.providerId。
class ModelPickerResult {
  final String modelCode;
  final String providerId;
  const ModelPickerResult({required this.modelCode, required this.providerId});
}

/// 弹出搜索式 model picker。返回 [ModelPickerResult];取消返 null。
///
/// [currentModel] / [currentProviderId]:用于在列表中标 ✓ 选中态。
Future<ModelPickerResult?> showModelPickerDialog(
  BuildContext context, {
  required String? currentModel,
  required String? currentProviderId,
}) {
  return showAdaptiveDialog<ModelPickerResult?>(
    context: context,
    builder: (ctx) => _ModelPickerDialog(
      currentModel: currentModel,
      currentProviderId: currentProviderId,
    ),
  );
}

class _ModelPickerDialog extends ConsumerStatefulWidget {
  const _ModelPickerDialog({
    required this.currentModel,
    required this.currentProviderId,
  });
  final String? currentModel;
  final String? currentProviderId;

  @override
  ConsumerState<_ModelPickerDialog> createState() => _ModelPickerDialogState();
}

class _ModelPickerDialogState extends ConsumerState<_ModelPickerDialog> {
  String _query = '';
  final _searchCtrl = TextEditingController();
  final _searchFocus = FocusNode();

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (mounted) _searchFocus.requestFocus();
    });
  }

  @override
  void dispose() {
    _searchCtrl.dispose();
    _searchFocus.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final l = AppLocalizations.of(context)!;
    final groupsAsync = ref.watch(chatModelGroupsProvider);

    return AdaptiveDialogFrame(
      maxWidth: 520,
      maxHeight: 560,
      insetPadding: const EdgeInsets.symmetric(horizontal: 40, vertical: 24),
      shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(12)),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          // ── 搜索条 ─────────────────────────────────────────────
          Padding(
            padding: const EdgeInsets.fromLTRB(12, 10, 8, 8),
            child: Row(
              children: [
                Expanded(
                  child: BiuTextField(
                    controller: _searchCtrl,
                    focusNode: _searchFocus,
                    onChanged: (v) => setState(() => _query = v),
                    hintText: l.chatV2ModelPickerSearchHint,
                    prefixIcon: const Icon(Icons.search, size: 18),
                  ),
                ),
                IconButton(
                  icon: const Icon(Icons.close, size: 18),
                  tooltip: l.chatV2DialogCancel,
                  onPressed: () => Navigator.of(context).pop(),
                ),
              ],
            ),
          ),
          const Divider(height: 1),
          // ── 模型列表(provider 分组) ──────────────────────────
          Expanded(
            child: groupsAsync.isLoading
                ? const Center(
                    child: SizedBox(
                      width: 24,
                      height: 24,
                      child: CircularProgressIndicator(strokeWidth: 2),
                    ),
                  )
                : (groupsAsync.valueOrNull ?? const []).isEmpty
                    ? _EmptyState(onTap: () => _goApiKeys(context))
                    : _GroupList(
                        groups: groupsAsync.valueOrNull ?? const [],
                        query: _query,
                        currentModel: widget.currentModel,
                        currentProviderId: widget.currentProviderId,
                        onPick: (r) => Navigator.of(context).pop(r),
                        onSettings: (g) => _goSettings(context, g),
                      ),
          ),
        ],
      ),
    );
  }

  /// official provider → 会员中心(管理订阅);BYOK → API Keys 页管 key。
  void _goSettings(BuildContext ctx, ChatModelGroup g) {
    Navigator.of(ctx).pop();
    if (g.isOfficial) {
      ctx.go('/membership');
      return;
    }
    ref.read(activeSettingsTabProvider.notifier).state = SettingsTab.apiKeys;
    ctx.go('/settings');
  }

  /// 空态唯一动作:跳 API Keys 让用户配一把 BYOK key。
  void _goApiKeys(BuildContext ctx) {
    Navigator.of(ctx).pop();
    ref.read(activeSettingsTabProvider.notifier).state = SettingsTab.apiKeys;
    ctx.go('/settings');
  }
}

class _EmptyState extends StatelessWidget {
  const _EmptyState({required this.onTap});
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final l = AppLocalizations.of(context)!;
    final theme = Theme.of(context);
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(24),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(
              Icons.cloud_off_outlined,
              size: 36,
              color: theme.colorScheme.onSurfaceVariant,
            ),
            const SizedBox(height: 10),
            Text(
              l.chatV2ModelPickerEmpty,
              style: theme.textTheme.bodyMedium?.copyWith(
                color: theme.colorScheme.onSurfaceVariant,
              ),
            ),
            const SizedBox(height: 14),
            FilledButton.icon(
              onPressed: onTap,
              icon: const Icon(Icons.tune, size: 16),
              label: Text(l.chatV2ModelPickerEmptyAction),
            ),
          ],
        ),
      ),
    );
  }
}

class _GroupList extends StatelessWidget {
  const _GroupList({
    required this.groups,
    required this.query,
    required this.currentModel,
    required this.currentProviderId,
    required this.onPick,
    required this.onSettings,
  });
  final List<ChatModelGroup> groups;
  final String query;
  final String? currentModel;
  final String? currentProviderId;
  final ValueChanged<ModelPickerResult> onPick;
  final ValueChanged<ChatModelGroup> onSettings;

  @override
  Widget build(BuildContext context) {
    final l = AppLocalizations.of(context)!;
    final theme = Theme.of(context);
    final q = query.trim().toLowerCase();
    if (groups.isEmpty) {
      return _EmptyState(onTap: () => onSettings(groups.first));
    }
    return ListView.builder(
      padding: const EdgeInsets.symmetric(vertical: 4),
      itemCount: groups.length,
      itemBuilder: (_, i) {
        final g = groups[i];
        final matched = q.isEmpty
            ? g.models
            : g.models.where((m) {
                final dn = m.displayName.toLowerCase();
                final mid = m.code.toLowerCase();
                return dn.contains(q) || mid.contains(q);
              }).toList();
        // 搜索模式下空匹配 → 整个 provider 隐藏(避免空组占地方)
        if (q.isNotEmpty && matched.isEmpty) {
          return const SizedBox.shrink();
        }
        return Padding(
          padding: const EdgeInsets.symmetric(vertical: 4),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              // provider header: displayName + 齿轮
              Padding(
                padding: const EdgeInsets.fromLTRB(14, 8, 6, 4),
                child: Row(
                  children: [
                    Expanded(
                      child: Text(
                        g.displayName,
                        style: theme.textTheme.labelSmall?.copyWith(
                          color: theme.colorScheme.onSurfaceVariant,
                          fontWeight: FontWeight.w600,
                          letterSpacing: 0.4,
                        ),
                      ),
                    ),
                    Tooltip(
                      message: l.chatV2ModelPickerSettings,
                      child: InkWell(
                        onTap: () => onSettings(g),
                        borderRadius: BorderRadius.circular(4),
                        child: Padding(
                          padding: const EdgeInsets.all(4),
                          child: Icon(
                            Icons.settings_outlined,
                            size: 14,
                            color: theme.colorScheme.onSurfaceVariant,
                          ),
                        ),
                      ),
                    ),
                  ],
                ),
              ),
              for (final m in matched)
                _ModelRow(
                  label: m.displayName,
                  modelCode: m.code,
                  contextWindow: m.contextWindow,
                  priceLabel: m.priceLabel,
                  selected: currentModel == m.code &&
                      currentProviderId == g.providerId,
                  onTap: () => onPick(
                    ModelPickerResult(
                      modelCode: m.code,
                      providerId: g.providerId,
                    ),
                  ),
                ),
            ],
          ),
        );
      },
    );
  }
}

class _ModelRow extends StatelessWidget {
  const _ModelRow({
    required this.label,
    required this.modelCode,
    required this.contextWindow,
    required this.priceLabel,
    required this.selected,
    required this.onTap,
  });
  final String label;
  final String modelCode;
  final int? contextWindow;
  final String? priceLabel; // P6: official markup 后实际计费价 chip
  final bool selected;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return InkWell(
      onTap: onTap,
      child: Padding(
        padding: const EdgeInsets.fromLTRB(14, 6, 14, 6),
        child: Row(
          children: [
            SizedBox(
              width: 16,
              child: selected
                  ? Icon(
                      Icons.check,
                      size: 14,
                      color: theme.colorScheme.primary,
                    )
                  : const SizedBox.shrink(),
            ),
            const SizedBox(width: 6),
            Expanded(
              child: Text(
                label,
                style: theme.textTheme.bodyMedium?.copyWith(
                  fontWeight: selected ? FontWeight.w600 : FontWeight.w400,
                ),
                overflow: TextOverflow.ellipsis,
              ),
            ),
            if (priceLabel != null) ...[
              const SizedBox(width: 8),
              Text(
                priceLabel!,
                style: theme.textTheme.labelSmall?.copyWith(
                  color: theme.colorScheme.primary,
                  fontFeatures: const [FontFeature.tabularFigures()],
                ),
              ),
            ],
            if ((contextWindow ?? 0) > 0) ...[
              const SizedBox(width: 8),
              Text(
                _fmtCtx(contextWindow!),
                style: theme.textTheme.labelSmall?.copyWith(
                  color: theme.colorScheme.onSurfaceVariant,
                  fontFeatures: const [FontFeature.tabularFigures()],
                ),
              ),
            ],
          ],
        ),
      ),
    );
  }

  static String _fmtCtx(int tokens) {
    if (tokens >= 1000000) {
      final m = tokens / 1000000;
      return m == m.truncate() ? '${m.toInt()}M' : '${m.toStringAsFixed(1)}M';
    }
    if (tokens >= 1000) return '${(tokens / 1000).round()}K';
    return '$tokens';
  }
}

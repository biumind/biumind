// Radar tab — 2-pane (rules | hits) layout. Left pane lists rules
// with on/off toggles + a "+ 新建规则" button. Right pane shows hits
// for the currently-selected rule, or all rules when "全部" is picked.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../../../app/theme.dart';
import '../models.dart';
import '../providers.dart';
import 'hits_pane.dart';
import 'rule_editor_sheet.dart';

class RadarTab extends ConsumerWidget {
  const RadarTab({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    return LayoutBuilder(
      builder: (ctx, constraints) {
        final isNarrow = constraints.maxWidth < 800;
        if (isNarrow) {
          return const _NarrowRadar();
        }
        return Container(
          color: BiuTokens.bg,
          child: Row(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: const [
              _RulesPane(),
              Expanded(child: HitsPane()),
            ],
          ),
        );
      },
    );
  }
}

class _NarrowRadar extends ConsumerStatefulWidget {
  const _NarrowRadar();
  @override
  ConsumerState<_NarrowRadar> createState() => _NarrowRadarState();
}

class _NarrowRadarState extends ConsumerState<_NarrowRadar> {
  bool _rulesOpen = false;

  @override
  Widget build(BuildContext context) {
    return Stack(
      children: [
        Column(
          children: [
            Container(
              padding: const EdgeInsets.symmetric(
                  horizontal: BiuTokens.space3, vertical: BiuTokens.space2),
              decoration: BoxDecoration(
                border: Border(
                    bottom: BorderSide(color: BiuTokens.borderSubtle)),
              ),
              child: Row(
                children: [
                  IconButton(
                    icon: const Icon(Icons.menu, size: 20),
                    onPressed: () => setState(() => _rulesOpen = true),
                  ),
                  const SizedBox(width: BiuTokens.space2),
                  const Text('雷达命中',
                      style: TextStyle(
                          fontSize: 14, fontWeight: FontWeight.w600)),
                ],
              ),
            ),
            const Expanded(child: HitsPane()),
          ],
        ),
        if (_rulesOpen)
          Positioned.fill(
            child: GestureDetector(
              onTap: () => setState(() => _rulesOpen = false),
              child: Container(color: Colors.black.withValues(alpha: 0.4)),
            ),
          ),
        if (_rulesOpen)
          Positioned(
            left: 0,
            top: 0,
            bottom: 0,
            child: SizedBox(
              width: 300,
              child: Material(
                color: BiuTokens.surface,
                child: const _RulesPane(),
              ),
            ),
          ),
      ],
    );
  }
}

class _RulesPane extends ConsumerWidget {
  const _RulesPane();

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final rulesAsync = ref.watch(rulesProvider);
    final selection = ref.watch(rssSelectionProvider);
    final controller = ref.read(rssSelectionProvider.notifier);

    return Container(
      width: 280,
      decoration: BoxDecoration(
        color: BiuTokens.surface,
        border: Border(right: BorderSide(color: BiuTokens.borderSubtle)),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Expanded(
            child: rulesAsync.when(
              loading: () => const Center(child: CircularProgressIndicator()),
              error: (e, _) => Padding(
                padding: const EdgeInsets.all(BiuTokens.space4),
                child: SelectableText('$e',
                    style: const TextStyle(fontSize: 12)),
              ),
              data: (rules) {
                final children = <Widget>[
                  _RuleTile(
                    selected: selection.selectedRuleId == null,
                    title: '全部',
                    severity: 'info',
                    keywords: const [],
                    enabled: true,
                    showToggle: false,
                    onTap: () => controller.selectRule(null),
                  ),
                  if (rules.isEmpty)
                    const _EmptyRules()
                  else
                    ...rules.map((r) => _RuleTile(
                          selected: selection.selectedRuleId == r.id,
                          title: r.name.isEmpty ? '未命名规则' : r.name,
                          severity: r.onHitBadge,
                          keywords: r.matchAny.isNotEmpty ? r.matchAny : r.matchAll,
                          enabled: r.enabled,
                          showToggle: true,
                          onTap: () => controller.selectRule(r.id),
                          onToggle: (v) => _toggle(context, ref, r, v),
                          onDelete: () => _delete(context, ref, r),
                        )),
                ];
                return ListView(
                  padding: const EdgeInsets.symmetric(
                      vertical: BiuTokens.space2),
                  children: children,
                );
              },
            ),
          ),
          Container(
            padding: const EdgeInsets.all(BiuTokens.space3),
            decoration: BoxDecoration(
              border: Border(top: BorderSide(color: BiuTokens.borderSubtle)),
            ),
            child: SizedBox(
              width: double.infinity,
              child: OutlinedButton.icon(
                icon: const Icon(Icons.add, size: 16),
                label: const Text('新建规则'),
                onPressed: () => showRuleEditorSheet(context, ref),
              ),
            ),
          ),
        ],
      ),
    );
  }

  Future<void> _toggle(
      BuildContext context, WidgetRef ref, Rule rule, bool enabled) async {
    final actions = ref.read(rssActionsProvider);
    if (actions == null) return;
    try {
      await actions.rulesUpdate(id: rule.id, enabled: enabled);
      ref.refreshRules();
    } catch (e) {
      if (!context.mounted) return;
      ScaffoldMessenger.of(context)
          .showSnackBar(SnackBar(content: Text('切换失败: $e')));
    }
  }

  Future<void> _delete(
      BuildContext context, WidgetRef ref, Rule rule) async {
    final ok = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('删除规则'),
        content: Text('确认删除规则 “${rule.name}” 吗？相关命中也会一并删除。'),
        actions: [
          TextButton(
              onPressed: () => Navigator.pop(ctx, false),
              child: const Text('取消')),
          FilledButton(
              onPressed: () => Navigator.pop(ctx, true),
              child: const Text('删除')),
        ],
      ),
    );
    if (ok != true) return;
    final actions = ref.read(rssActionsProvider);
    if (actions == null) return;
    try {
      await actions.rulesDelete(rule.id);
      ref.read(rssSelectionProvider.notifier).selectRule(null);
      ref.refreshRules();
      ref.refreshHits();
    } catch (e) {
      if (!context.mounted) return;
      ScaffoldMessenger.of(context)
          .showSnackBar(SnackBar(content: Text('删除失败: $e')));
    }
  }
}

class _RuleTile extends StatelessWidget {
  const _RuleTile({
    required this.selected,
    required this.title,
    required this.severity,
    required this.keywords,
    required this.enabled,
    required this.showToggle,
    required this.onTap,
    this.onToggle,
    this.onDelete,
  });

  final bool selected;
  final String title;
  final String severity;
  final List<String> keywords;
  final bool enabled;
  final bool showToggle;
  final VoidCallback onTap;
  final ValueChanged<bool>? onToggle;
  final VoidCallback? onDelete;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.symmetric(
          horizontal: BiuTokens.space2, vertical: 1),
      child: Material(
        color: selected ? BiuTokens.purpleSoft : Colors.transparent,
        borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
        child: InkWell(
          borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
          onTap: onTap,
          onLongPress: onDelete,
          child: Padding(
            padding: const EdgeInsets.symmetric(
                horizontal: BiuTokens.space3, vertical: BiuTokens.space2),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Row(
                  children: [
                    Container(
                      width: 8,
                      height: 8,
                      decoration: BoxDecoration(
                        color: severityColor(severity),
                        shape: BoxShape.circle,
                      ),
                    ),
                    const SizedBox(width: BiuTokens.space2),
                    Expanded(
                      child: Text(
                        title,
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                        style: TextStyle(
                          fontSize: 13,
                          fontWeight: FontWeight.w600,
                          color: selected ? BiuTokens.purple : BiuTokens.text,
                        ),
                      ),
                    ),
                    if (showToggle)
                      Transform.scale(
                        scale: 0.7,
                        child: Switch(
                          value: enabled,
                          onChanged: onToggle,
                        ),
                      ),
                  ],
                ),
                if (keywords.isNotEmpty) ...[
                  const SizedBox(height: 6),
                  Wrap(
                    spacing: 4,
                    runSpacing: 4,
                    children: keywords
                        .take(4)
                        .map((kw) => Container(
                              padding: const EdgeInsets.symmetric(
                                  horizontal: 6, vertical: 1),
                              decoration: BoxDecoration(
                                color: BiuTokens.surfaceMuted,
                                borderRadius: BorderRadius.circular(
                                    BiuTokens.radiusFull),
                              ),
                              child: Text(
                                kw,
                                style: TextStyle(
                                    fontSize: 10,
                                    color: BiuTokens.textSecondary),
                              ),
                            ))
                        .toList(),
                  ),
                ],
              ],
            ),
          ),
        ),
      ),
    );
  }
}

class _EmptyRules extends StatelessWidget {
  const _EmptyRules();
  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.all(BiuTokens.space5),
      child: Column(
        children: [
          Icon(Icons.radar_outlined, size: 32, color: BiuTokens.textMuted),
          const SizedBox(height: BiuTokens.space3),
          Text('还没有规则',
              style: TextStyle(
                  fontSize: 13,
                  fontWeight: FontWeight.w500,
                  color: BiuTokens.textSecondary)),
          const SizedBox(height: BiuTokens.space1),
          Text('点击下方“新建规则”，让雷达替你盯关键词',
              textAlign: TextAlign.center,
              style: TextStyle(fontSize: 12, color: BiuTokens.textMuted)),
        ],
      ),
    );
  }
}

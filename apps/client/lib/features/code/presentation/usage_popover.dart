// UsagePopover —— 状态栏用量入口(M5)。点开拉一次 usage.read 快照,展示
// Claude(订阅 5h/7d)+ Codex(主/次额度)的剩余百分比 + 重置时刻。
//
// loopback daemon 未就绪时入口禁用;任一源 unavailable 时该卡片显示
// reason(降级,不报错)。颜色阈值:剩余 >70 绿 / ≥20 橙 / 否则红。

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../app/theme.dart';
import '../../../l10n/app_localizations.dart';
import '../data/code_bridge_provider.dart';
import '../domain/usage_models.dart';

class UsagePopover extends ConsumerStatefulWidget {
  const UsagePopover({super.key});

  @override
  ConsumerState<UsagePopover> createState() => _UsagePopoverState();
}

class _UsagePopoverState extends ConsumerState<UsagePopover> {
  final _menu = MenuController();
  Future<UsageSnapshot>? _future;

  void _load() {
    final client = ref.read(codeBridgeClientProvider);
    setState(() {
      _future = client?.readUsageSnapshot() ??
          Future<UsageSnapshot>.error('daemon not ready');
    });
  }

  @override
  Widget build(BuildContext context) {
    final t = AppLocalizations.of(context)!;
    final connected = ref.watch(codeBridgeClientProvider) != null;

    return MenuAnchor(
      controller: _menu,
      style: MenuStyle(
        backgroundColor: WidgetStatePropertyAll(BiuTokens.surface),
        padding: const WidgetStatePropertyAll(EdgeInsets.zero),
        shape: WidgetStatePropertyAll(
          RoundedRectangleBorder(
            borderRadius: BorderRadius.circular(10),
            side: BorderSide(color: BiuTokens.borderSubtle),
          ),
        ),
      ),
      menuChildren: [
        SizedBox(width: 300, child: _content(t)),
      ],
      builder: (context, controller, _) => IconButton(
        iconSize: 15,
        padding: EdgeInsets.zero,
        constraints: const BoxConstraints(minWidth: 26, minHeight: 26),
        visualDensity: VisualDensity.compact,
        tooltip: t.codeUsageTooltip,
        color: BiuTokens.textMuted,
        icon: const Icon(Icons.insights_outlined),
        onPressed: connected
            ? () {
                if (controller.isOpen) {
                  controller.close();
                } else {
                  _load();
                  controller.open();
                }
              }
            : null,
      ),
    );
  }

  Widget _content(AppLocalizations t) {
    return Padding(
      padding: const EdgeInsets.all(BiuTokens.space3),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Row(
            children: [
              Text(
                t.codeUsageTooltip,
                style: TextStyle(
                  fontSize: 12,
                  fontWeight: FontWeight.w700,
                  color: BiuTokens.textSecondary,
                  letterSpacing: 0.4,
                ),
              ),
              const Spacer(),
              IconButton(
                iconSize: 14,
                padding: EdgeInsets.zero,
                constraints:
                    const BoxConstraints(minWidth: 22, minHeight: 22),
                tooltip: t.codeUsageRefresh,
                icon: const Icon(Icons.refresh_rounded),
                color: BiuTokens.textMuted,
                onPressed: _load,
              ),
            ],
          ),
          const SizedBox(height: BiuTokens.space2),
          FutureBuilder<UsageSnapshot>(
            future: _future,
            builder: (context, snap) {
              if (snap.connectionState == ConnectionState.waiting) {
                return _statusText(t.codeUsageLoading);
              }
              if (snap.hasError || !snap.hasData) {
                return _statusText(t.codeUsageFailed);
              }
              final s = snap.data!;
              return Column(
                mainAxisSize: MainAxisSize.min,
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  _sourceCard(
                    t,
                    title: 'Claude Code',
                    available: s.claude.available,
                    reason: s.claude.reason,
                    metrics: [
                      (t.codeUsageFiveHour, s.claude.data?.fiveHour),
                      (t.codeUsageSevenDay, s.claude.data?.sevenDay),
                    ],
                  ),
                  const SizedBox(height: BiuTokens.space3),
                  _sourceCard(
                    t,
                    title: 'Codex',
                    subtitle: _codexSubtitle(s.codex),
                    available: s.codex.available,
                    reason: s.codex.reason,
                    metrics: [
                      (t.codeUsagePrimary, s.codex.data?.primary),
                      (t.codeUsageSecondary, s.codex.data?.secondary),
                    ],
                  ),
                ],
              );
            },
          ),
        ],
      ),
    );
  }

  Widget _sourceCard(
    AppLocalizations t, {
    required String title,
    String? subtitle,
    required bool available,
    String? reason,
    required List<(String, UsageWindow?)> metrics,
  }) {
    final hasWindow = metrics.any((m) => m.$2 != null);
    return Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Row(
          children: [
            Text(
              title,
              style: TextStyle(
                fontSize: 11.5,
                fontWeight: FontWeight.w600,
                color: BiuTokens.textSecondary,
              ),
            ),
            if (subtitle != null && subtitle.isNotEmpty) ...[
              const SizedBox(width: 6),
              Expanded(
                child: Text(
                  subtitle,
                  overflow: TextOverflow.ellipsis,
                  style: TextStyle(fontSize: 10.5, color: BiuTokens.textMuted),
                ),
              ),
            ],
          ],
        ),
        const SizedBox(height: 6),
        if (!available)
          _statusText(reason ?? t.codeUsageFailed)
        else if (!hasWindow)
          _statusText(t.codeUsageNoWindows)
        else
          ...metrics
              .where((m) => m.$2 != null)
              .map((m) => _metricRow(t, m.$1, m.$2!)),
      ],
    );
  }

  Widget _metricRow(AppLocalizations t, String label, UsageWindow w) {
    final color = _usageColor(w.remainingPercent);
    final reset = _formatReset(w.resetAtTime);
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 3),
      child: Row(
        children: [
          SizedBox(
            width: 56,
            child: Text(
              label,
              style: TextStyle(fontSize: 11, color: BiuTokens.textMuted),
            ),
          ),
          Text(
            '${w.remainingPercent}% ${t.codeUsageLeft}',
            style: TextStyle(
              fontSize: 11.5,
              fontFamily: 'SF Mono',
              fontWeight: FontWeight.w600,
              color: color,
            ),
          ),
          const Spacer(),
          if (reset != null)
            Text(
              reset,
              style: TextStyle(fontSize: 10.5, color: BiuTokens.textMuted),
            ),
        ],
      ),
    );
  }

  Widget _statusText(String text) => Padding(
        padding: const EdgeInsets.symmetric(vertical: 4),
        child: Text(
          text,
          style: TextStyle(fontSize: 11, color: BiuTokens.textMuted, height: 1.4),
        ),
      );

  static String? _codexSubtitle(UsageSource<CodexUsage> codex) {
    if (!codex.available || codex.data == null) return null;
    final parts = [codex.data!.planType, codex.data!.email]
        .where((e) => e != null && e.isNotEmpty)
        .cast<String>()
        .toList();
    return parts.isEmpty ? null : parts.join(' · ');
  }

  // 剩余百分比着色:>70 绿 / ≥20 橙 / 否则红。
  static Color _usageColor(int remaining) {
    if (remaining > 70) return BiuTokens.green;
    if (remaining >= 20) return SemanticTokens.warning;
    return BiuTokens.error;
  }

  // 重置时刻格式 "MM-dd HH:mm"(本地时区);无则 null。手动拼避免引 intl。
  static String? _formatReset(DateTime? dt) {
    if (dt == null) return null;
    final l = dt.toLocal();
    String two(int v) => v.toString().padLeft(2, '0');
    return '${two(l.month)}-${two(l.day)} ${two(l.hour)}:${two(l.minute)}';
  }
}

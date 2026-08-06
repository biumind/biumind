// HeroShortcutHintV2 —— 首次进入 Hero 时显示一行"试试 Cmd+K"小提示。
// 设计文档 docs/BiuMind-Chat-UI-Benchmark-Optimization.md（v2 onboarding）。
//
// dismiss 后写 SharedPreferences `biu.chat.hero.shortcut_hint.dismissed=true`，
// 不再显示（不像 changelog banner 跟版本号绑定 —— 这是 once-and-done）。

import 'package:flutter/material.dart';
import 'package:shared_preferences/shared_preferences.dart';

import '../../../../core/ui/biu_kbd.dart';
import '../../../../l10n/app_localizations.dart';

const _kKey = 'biu.chat.hero.shortcut_hint.dismissed';

class HeroShortcutHintV2 extends StatefulWidget {
  const HeroShortcutHintV2({super.key});

  @override
  State<HeroShortcutHintV2> createState() => _HeroShortcutHintV2State();
}

class _HeroShortcutHintV2State extends State<HeroShortcutHintV2> {
  bool? _show;

  @override
  void initState() {
    super.initState();
    _check();
  }

  Future<void> _check() async {
    try {
      final prefs = await SharedPreferences.getInstance();
      final dismissed = prefs.getBool(_kKey) ?? false;
      if (mounted) setState(() => _show = !dismissed);
    } catch (_) {
      if (mounted) setState(() => _show = true);
    }
  }

  Future<void> _dismiss() async {
    setState(() => _show = false);
    try {
      final prefs = await SharedPreferences.getInstance();
      await prefs.setBool(_kKey, true);
    } catch (_) {}
  }

  @override
  Widget build(BuildContext context) {
    if (_show != true) return const SizedBox.shrink();
    final theme = Theme.of(context);
    final l = AppLocalizations.of(context)!;
    final isMac = theme.platform == TargetPlatform.macOS;
    final mod = isMac ? '⌘' : 'Ctrl';
    return Padding(
      padding: const EdgeInsets.only(bottom: 12),
      child: Container(
        // prototype `.tip-bar { padding: 10px 14px; radius: var(--radius-md)=10;
        // background: surf-2; 无 border }` — 之前的 6px radius + outlineVariant
        // 边框是早期实现,跟 prototype 不一致。
        padding: const EdgeInsets.fromLTRB(14, 10, 14, 10),
        decoration: BoxDecoration(
          color: theme.colorScheme.surfaceContainerLow,
          borderRadius: BorderRadius.circular(10),
        ),
        child: Row(
          children: [
            Icon(Icons.tips_and_updates_outlined,
                size: 14, color: theme.colorScheme.onSurfaceVariant),
            const SizedBox(width: 8),
            Expanded(
              child: RichText(
                text: TextSpan(
                  style: theme.textTheme.labelMedium?.copyWith(
                    color: theme.colorScheme.onSurfaceVariant,
                  ),
                  children: [
                    TextSpan(text: l.chatV2HintIntro),
                    // prototype `.tip-bar kbd` 风格 — surface-0 + hairline 边框 +
                    // 4px 圆角的小 pill,真正"按键感"。WidgetSpan 让 inline 排版
                    // 跟文字一起换行/对齐。
                    BiuKbd.span('$mod K'),
                    TextSpan(text: l.chatV2HintBeforeCrossSearch),
                    BiuKbd.span('$mod ⇧ F'),
                    TextSpan(text: l.chatV2HintAfterCrossSearch),
                    BiuKbd.span('?'),
                    TextSpan(text: l.chatV2HintAfterHelp),
                  ],
                ),
              ),
            ),
            IconButton(
              icon: const Icon(Icons.close, size: 12),
              tooltip: l.chatV2HintDismiss,
              visualDensity: VisualDensity.compact,
              onPressed: _dismiss,
            ),
          ],
        ),
      ),
    );
  }
}

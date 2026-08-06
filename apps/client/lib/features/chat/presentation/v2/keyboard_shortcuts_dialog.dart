// KeyboardShortcutsDialog —— 按 ? 弹出当前所有快捷键 cheatsheet。
// 设计文档 docs/BiuMind-Chat-UI-Benchmark-Optimization.md P2-23。
//
// 不分组太多，按场景两栏：消息流 / 输入框。每行 [按键] [说明]。

import 'package:flutter/material.dart';

import '../../../../l10n/app_localizations.dart';

Future<void> showKeyboardShortcutsDialog(BuildContext ctx) {
  return showDialog<void>(
    context: ctx,
    builder: (_) => const _KeyboardShortcutsDialog(),
  );
}

class _KeyboardShortcutsDialog extends StatelessWidget {
  const _KeyboardShortcutsDialog();

  @override
  Widget build(BuildContext context) {
    final l = AppLocalizations.of(context)!;
    final isMac = Theme.of(context).platform == TargetPlatform.macOS;
    final mod = isMac ? '⌘' : 'Ctrl';
    return AlertDialog(
      title: Row(
        children: [
          const Icon(Icons.keyboard_outlined, size: 20),
          const SizedBox(width: 8),
          Text(l.chatV2ShortcutsTitle),
          const Spacer(),
          IconButton(
            icon: const Icon(Icons.close, size: 18),
            onPressed: () => Navigator.of(context).pop(),
          ),
        ],
      ),
      contentPadding: const EdgeInsets.symmetric(horizontal: 24, vertical: 12),
      content: SizedBox(
        width: 520,
        child: SingleChildScrollView(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              _Section(title: l.chatV2ShortcutsSectionInput, rows: [
                _Row(keys: const ['Enter'], desc: l.chatV2ShortcutsSend),
                _Row(
                    keys: const ['Shift', 'Enter'],
                    desc: l.chatV2ShortcutsNewline),
                _Row(
                    keys: const ['↑'], desc: l.chatV2ShortcutsHistoryUp),
                _Row(
                    keys: const ['↓'],
                    desc: l.chatV2ShortcutsHistoryDown),
                _Row(keys: const ['/'], desc: l.chatV2ShortcutsSlash),
                _Row(keys: const ['Esc'], desc: l.chatV2ShortcutsEsc),
              ]),
              const SizedBox(height: 16),
              _Section(title: l.chatV2ShortcutsSectionMessages, rows: [
                _Row(
                    keys: [mod, 'F'],
                    desc: l.chatV2ShortcutsInThreadSearch),
                _Row(
                    keys: const ['Enter'],
                    desc: l.chatV2ShortcutsSearchNext),
                _Row(
                    keys: const ['Shift', 'Enter'],
                    desc: l.chatV2ShortcutsSearchPrev),
              ]),
              const SizedBox(height: 16),
              _Section(title: l.chatV2ShortcutsSectionGlobal, rows: [
                _Row(keys: [mod, 'K'], desc: l.chatV2ShortcutsPalette),
                _Row(keys: [mod, 'N'], desc: l.chatV2ShortcutsNewThread),
                _Row(
                    keys: [mod, 'Shift', 'F'],
                    desc: l.chatV2ShortcutsCrossSearch),
                _Row(
                    keys: [mod, 'Shift', 'M'],
                    desc: l.chatV2ShortcutsModelPicker),
                _Row(keys: const ['?'], desc: l.chatV2ShortcutsHelp),
              ]),
            ],
          ),
        ),
      ),
    );
  }
}

class _Section extends StatelessWidget {
  const _Section({required this.title, required this.rows});
  final String title;
  final List<_Row> rows;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Padding(
          padding: const EdgeInsets.only(bottom: 6),
          child: Text(
            title,
            style: theme.textTheme.labelLarge?.copyWith(
              color: theme.colorScheme.primary,
              fontWeight: FontWeight.w600,
            ),
          ),
        ),
        ...rows,
      ],
    );
  }
}

class _Row extends StatelessWidget {
  const _Row({required this.keys, required this.desc});
  final List<String> keys;
  final String desc;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 4),
      child: Row(
        children: [
          SizedBox(
            width: 140,
            child: Wrap(
              spacing: 4,
              children: [
                for (var i = 0; i < keys.length; i++) ...[
                  _Key(label: keys[i]),
                  if (i < keys.length - 1)
                    Text(
                      '+',
                      style: theme.textTheme.bodySmall?.copyWith(
                        color: theme.colorScheme.onSurfaceVariant,
                      ),
                    ),
                ],
              ],
            ),
          ),
          const SizedBox(width: 12),
          Expanded(
            child: Text(
              desc,
              style: theme.textTheme.bodyMedium,
            ),
          ),
        ],
      ),
    );
  }
}

class _Key extends StatelessWidget {
  const _Key({required this.label});
  final String label;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
      decoration: BoxDecoration(
        color: theme.colorScheme.surfaceContainerHighest,
        borderRadius: BorderRadius.circular(4),
        border: Border.all(color: theme.colorScheme.outlineVariant),
      ),
      child: Text(
        label,
        style: theme.textTheme.labelSmall?.copyWith(
          fontFamily: 'monospace',
          fontWeight: FontWeight.w500,
        ),
      ),
    );
  }
}

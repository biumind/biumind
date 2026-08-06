// Shared page scaffold — every top-level tab wraps in this so they
// all get the same:
//   * No AppBar (per UI-Design-System §3.3 / §3.4)
//   * Bg color BiuTokens.bg
//   * Optional inline headline title + actions row
//   * Optional max-width content container (default 1100 — wider than
//     chat's 720, since list/grid pages benefit from breathing room
//     but shouldn't span 2000px on a 4K monitor)
//
// Pages that need fully custom layouts (chat — center 720, settings —
// center 720) can opt out of the constraint by passing `maxWidth: null`.

import 'package:flutter/material.dart';

import '../app/theme.dart';

class PageScaffold extends StatelessWidget {
  const PageScaffold({
    super.key,
    required this.title,
    required this.child,
    this.actions,
    this.subtitle,
    this.leading,
    this.maxWidth = 1100,
    this.padding = const EdgeInsets.symmetric(
      horizontal: BiuTokens.space5,
      vertical: BiuTokens.space6,
    ),
  });

  /// Inline page title (rendered as headlineLarge). Empty string skips
  /// the header bar entirely — useful for pages with their own
  /// custom header layout (chat).
  final String title;
  final String? subtitle;
  final List<Widget>? actions;

  /// Optional widget prepended to the header row (left of the title).
  /// Subpages pass `const PhoneBackButton()` here (导航设计 §3.3) — it
  /// shrinks on desktop so the header layout is unchanged there.
  final Widget? leading;
  final Widget child;
  final double? maxWidth;
  final EdgeInsets padding;

  @override
  Widget build(BuildContext context) {
    final contentColumn = Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        if (title.isNotEmpty) _Header(
          title: title,
          subtitle: subtitle,
          actions: actions,
          leading: leading,
        ),
        Expanded(child: child),
      ],
    );

    final body = maxWidth == null
        ? contentColumn
        : Center(
            child: ConstrainedBox(
              constraints: BoxConstraints(maxWidth: maxWidth!),
              child: contentColumn,
            ),
          );

    return ColoredBox(
      color: BiuTokens.bg,
      child: Padding(padding: padding, child: body),
    );
  }
}

class _Header extends StatelessWidget {
  const _Header({
    required this.title,
    required this.subtitle,
    required this.actions,
    this.leading,
  });
  final String title;
  final String? subtitle;
  final List<Widget>? actions;
  final Widget? leading;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.only(bottom: BiuTokens.space5),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.center,
        children: [
          ?leading,
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  title,
                  style: Theme.of(context).textTheme.headlineLarge,
                ),
                if (subtitle != null && subtitle!.isNotEmpty) ...[
                  const SizedBox(height: BiuTokens.space1),
                  Text(
                    subtitle!,
                    style: Theme.of(context).textTheme.bodySmall,
                  ),
                ],
              ],
            ),
          ),
          if (actions != null) ...[
            const SizedBox(width: BiuTokens.space3),
            Row(mainAxisSize: MainAxisSize.min, children: actions!),
          ],
        ],
      ),
    );
  }
}

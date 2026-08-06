// SkillTile widget tests — pin the row layout and menu behaviour
// against design-review changes. Layout regressions (avatar size,
// badge order, status text colour) bleed into UX confusion fast,
// so the asserts here are deliberately strict on the visible
// strings + finder counts.

import 'package:biumind/data/api/skill_client.dart';
import 'package:biumind/features/skills/presentation/skill_tile.dart';
import 'package:flutter/material.dart';
import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:biumind/l10n/app_localizations.dart';
import 'package:flutter_test/flutter_test.dart';

Skill _skill({
  String id = 'skill_1',
  String identifier = 'wiki',
  String name = '知识库',
  String description = 'BiuMind 知识库工具',
  String source = 'bundled',
  String status = 'active',
  String icon = '📚',
  List<String> permissions = const [],
}) {
  return Skill(
    id: id,
    orgId: 'org_x',
    identifier: identifier,
    name: name,
    description: description,
    source: source,
    status: status,
    manifest: SkillManifest(icon: icon),
    content: '',
    contentHash: '',
    paths: const [],
    permissions: permissions,
    zipFileSha256: '',
    createdAt: DateTime(2026, 5, 28),
    updatedAt: DateTime(2026, 5, 28),
  );
}

Widget _wrap(Skill s, {
  void Function()? onTap,
  void Function(SkillTileAction)? onMenuAction,
}) {
  // AppLocalizations.of falls back to English when no delegate is
  // wired (see lib/l10n/app_localizations.dart). The widget itself
  // hard-codes Chinese strings for status / menu items, so we pin
  // those directly and let the source-badge labels (which DO route
  // through l10n) come back in English — the tile under test still
  // renders predictably either way.
  return MaterialApp(
    localizationsDelegates: const [
      AppLocalizations.delegate,
      GlobalMaterialLocalizations.delegate,
      GlobalWidgetsLocalizations.delegate,
      GlobalCupertinoLocalizations.delegate,
    ],
    supportedLocales: AppLocalizations.supportedLocales,
    home: Scaffold(
      body: Material(
        child: SkillTile(
          skill: s,
          onTap: onTap ?? () {},
          onMenuAction: onMenuAction ?? (_) {},
        ),
      ),
    ),
  );
}

void main() {
  testWidgets('renders name + description + emoji avatar', (tester) async {
    await tester.pumpWidget(_wrap(_skill()));
    await tester.pumpAndSettle();

    expect(find.text('知识库'), findsOneWidget);
    expect(find.text('BiuMind 知识库工具'), findsOneWidget);
    // Emoji rendered as text inside avatar.
    expect(find.text('📚'), findsOneWidget);
  });

  testWidgets('falls back to identifier when name empty', (tester) async {
    await tester.pumpWidget(_wrap(_skill(name: '', identifier: 'fallback-id')));
    await tester.pumpAndSettle();

    expect(find.text('fallback-id'), findsOneWidget);
  });

  testWidgets('letter avatar when icon empty', (tester) async {
    await tester.pumpWidget(_wrap(_skill(icon: '', identifier: 'wiki')));
    await tester.pumpAndSettle();

    // No emoji glyph — instead a single uppercase letter.
    expect(find.text('📚'), findsNothing);
    expect(find.text('W'), findsOneWidget);
  });

  testWidgets('status text matches skill status', (tester) async {
    await tester.pumpWidget(_wrap(_skill(status: 'active')));
    await tester.pumpAndSettle();
    expect(find.text('已启用'), findsOneWidget);

    await tester.pumpWidget(_wrap(_skill(status: 'staged')));
    await tester.pumpAndSettle();
    expect(find.text('待审核'), findsOneWidget);

    await tester.pumpWidget(_wrap(_skill(status: 'disabled')));
    await tester.pumpAndSettle();
    expect(find.text('已停用'), findsOneWidget);

    await tester.pumpWidget(_wrap(_skill(status: 'suspended')));
    await tester.pumpAndSettle();
    expect(find.text('已暂停'), findsOneWidget);
  });

  testWidgets('tap row fires onTap', (tester) async {
    var tapped = false;
    await tester.pumpWidget(_wrap(_skill(), onTap: () => tapped = true));
    await tester.pumpAndSettle();
    // Tap on the name (deep inside the InkWell) — using widget finder
    // not the tile area to avoid hitting the kebab button.
    await tester.tap(find.text('知识库'));
    await tester.pumpAndSettle();
    expect(tapped, isTrue);
  });

  testWidgets('menu shows approve/reject for staged skill', (tester) async {
    SkillTileAction? captured;
    await tester.pumpWidget(_wrap(
      _skill(status: 'staged', source: 'user'),
      onMenuAction: (a) => captured = a,
    ));
    await tester.pumpAndSettle();

    // Open the kebab menu.
    await tester.tap(find.byIcon(Icons.more_horiz));
    await tester.pumpAndSettle();

    expect(find.text('批准'), findsOneWidget);
    expect(find.text('驳回'), findsOneWidget);
    // Active-only items shouldn't appear under staged.
    expect(find.text('置顶到默认助手'), findsNothing);
    expect(find.text('停用'), findsNothing);

    // Tap 批准 → action fires.
    await tester.tap(find.text('批准'));
    await tester.pumpAndSettle();
    expect(captured?.isApprove, isTrue);
  });

  testWidgets('menu shows pin + disable for active skill', (tester) async {
    await tester.pumpWidget(_wrap(_skill(status: 'active', source: 'user')));
    await tester.pumpAndSettle();
    await tester.tap(find.byIcon(Icons.more_horiz));
    await tester.pumpAndSettle();

    expect(find.text('置顶到默认助手'), findsOneWidget);
    expect(find.text('停用'), findsOneWidget);
    expect(find.text('删除'), findsOneWidget);
    expect(find.text('批准'), findsNothing);
  });

  testWidgets('menu hides delete for bundled skill', (tester) async {
    await tester.pumpWidget(_wrap(_skill(status: 'active', source: 'bundled')));
    await tester.pumpAndSettle();
    await tester.tap(find.byIcon(Icons.more_horiz));
    await tester.pumpAndSettle();

    // Bundled rows are platform-shipped — no delete option.
    expect(find.text('删除'), findsNothing);
  });

  testWidgets('menu shows enable for disabled skill', (tester) async {
    await tester.pumpWidget(_wrap(_skill(status: 'disabled', source: 'user')));
    await tester.pumpAndSettle();
    await tester.tap(find.byIcon(Icons.more_horiz));
    await tester.pumpAndSettle();

    expect(find.text('启用'), findsOneWidget);
    expect(find.text('停用'), findsNothing);
    expect(find.text('置顶到默认助手'), findsNothing);
  });

  testWidgets('source badge text varies by source', (tester) async {
    // Source badge text comes from app_en.arb / app_zh.arb via the
    // l10n delegate. This test runs without a delegate so the english
    // fallback applies — that's still enough to confirm the badge
    // changes per source value (the failure mode we care about is
    // "all sources rendered the same string").
    for (final entry in const <(String, String)>[
      ('bundled', 'Bundled'),
      ('user', 'My'),
      ('marketplace', 'Marketplace'),
    ]) {
      await tester.pumpWidget(_wrap(_skill(source: entry.$1)));
      await tester.pumpAndSettle();
      expect(find.text(entry.$2), findsOneWidget,
          reason: 'source=${entry.$1} should render badge ${entry.$2}');
    }
  });
}

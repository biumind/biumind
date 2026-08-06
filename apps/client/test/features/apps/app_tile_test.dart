// AppTile widget tests — pin the catalog tile layout against
// regressions. Visual regressions (badge placement, icon fallback,
// description clamp) make App Center confusing fast, so the asserts
// here are deliberately strict on the visible strings + finder
// counts.

import 'package:biumind/data/api/apps_client.dart';
import 'package:biumind/features/apps/presentation/app_tile.dart';
import 'package:flutter/material.dart';
import 'package:biumind/l10n/app_localizations.dart';
import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_test/flutter_test.dart';

AppCatalogEntry _entry({
  String identifier = 'rss',
  String name = 'RSS',
  String description = 'Subscribe to feeds',
  String version = '0.1.0',
  String icon = '',
  String category = 'content',
}) {
  return AppCatalogEntry(
    identifier:  identifier,
    name:        name,
    description: description,
    version:     version,
    icon:        icon,
    category:    category,
  );
}

Widget _wrap(AppCatalogEntry e, {bool installed = false, void Function()? onTap}) {
  return MaterialApp(
    localizationsDelegates: const [
      AppLocalizations.delegate,
      GlobalMaterialLocalizations.delegate,
      GlobalWidgetsLocalizations.delegate,
      GlobalCupertinoLocalizations.delegate,
    ],
    supportedLocales: AppLocalizations.supportedLocales,
    home: Scaffold(
      body: Center(
        child: SizedBox(
          width: 200,
          height: 200,
          child: AppTile(entry: e, installed: installed, onTap: onTap ?? () {}),
        ),
      ),
    ),
  );
}

void main() {
  testWidgets('renders display name + description + identifier', (tester) async {
    await tester.pumpWidget(_wrap(_entry(
      identifier: 'rss', name: 'RSS 订阅', description: 'AI 摘要',
    )));
    expect(find.text('RSS 订阅'), findsOneWidget);
    expect(find.text('AI 摘要'), findsOneWidget);
    expect(find.text('rss'), findsOneWidget);
  });

  testWidgets('shows installed badge when installed=true', (tester) async {
    await tester.pumpWidget(_wrap(_entry(), installed: true));
    // The English fallback label "Installed" comes from the l10n map.
    expect(find.text('Installed'), findsOneWidget);
  });

  testWidgets('hides installed badge when installed=false', (tester) async {
    await tester.pumpWidget(_wrap(_entry(), installed: false));
    expect(find.text('Installed'), findsNothing);
  });

  testWidgets('renders emoji icon when manifest icon is non-URL', (tester) async {
    await tester.pumpWidget(_wrap(_entry(name: 'RSS', icon: '📰')));
    expect(find.text('📰'), findsOneWidget);
  });

  testWidgets('falls back to first-letter avatar when icon is HTTPS URL', (tester) async {
    // We don't load remote images in widget tests; the tile renders
    // the letter avatar instead. Assert by absence of the URL string.
    await tester.pumpWidget(_wrap(_entry(name: 'RSS', icon: 'https://example.com/icon.png')));
    expect(find.text('https://example.com/icon.png'), findsNothing);
    // First letter avatar uppercases to 'R'.
    expect(find.text('R'), findsOneWidget);
  });

  testWidgets('falls back to "?" when name is empty', (tester) async {
    await tester.pumpWidget(_wrap(_entry(name: '', identifier: 'x')));
    expect(find.text('?'), findsOneWidget);
  });

  testWidgets('tap fires onTap', (tester) async {
    var taps = 0;
    await tester.pumpWidget(_wrap(_entry(), onTap: () => taps++));
    await tester.tap(find.byType(AppTile));
    await tester.pump();
    expect(taps, 1);
  });
}

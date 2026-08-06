// InstallDialog widget tests — pin the permission-confirm flow.
//
// Critical UX assertions:
//   - All declared permissions render as a row each
//   - Risky permissions (net.outbound / sandbox.exec / secrets.read)
//     get the warning icon
//   - Cancel returns null
//   - Install with everything checked returns the full set
//   - Install with one unchecked returns the subset
//   - Empty permission list shows the "no permissions requested" text

import 'package:biumind/features/apps/presentation/install_dialog.dart';
import 'package:flutter/material.dart';
import 'package:biumind/l10n/app_localizations.dart';
import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_test/flutter_test.dart';

Future<InstallChoice?> _open(WidgetTester tester, {
  required List<String> permissions,
  required Future<void> Function(BuildContext) afterShow,
}) async {
  InstallChoice? result;
  await tester.pumpWidget(MaterialApp(
    localizationsDelegates: const [
      AppLocalizations.delegate,
      GlobalMaterialLocalizations.delegate,
      GlobalWidgetsLocalizations.delegate,
      GlobalCupertinoLocalizations.delegate,
    ],
    supportedLocales: AppLocalizations.supportedLocales,
    home: Builder(builder: (ctx) => Scaffold(
      body: Center(
        child: ElevatedButton(
          onPressed: () async {
            result = await InstallDialog.show(ctx,
                appName: 'Test',
                version: '0.1.0',
                permissions: permissions);
          },
          child: const Text('open'),
        ),
      ),
    )),
  ));
  await tester.tap(find.text('open'));
  await tester.pumpAndSettle();
  await afterShow(tester.element(find.byType(AlertDialog)));
  await tester.pumpAndSettle();
  return result;
}

void main() {
  testWidgets('renders all permission rows', (tester) async {
    await _open(tester, permissions: [
      'hub.invoke',
      'wiki.write',
      'cron.register',
    ], afterShow: (ctx) async {});
    expect(find.text('hub.invoke'), findsOneWidget);
    expect(find.text('wiki.write'), findsOneWidget);
    expect(find.text('cron.register'), findsOneWidget);
  });

  testWidgets('shows warning icon for risky permissions', (tester) async {
    await _open(tester, permissions: [
      'hub.invoke',
      'net.outbound:*.example.com',
      'sandbox.exec',
    ], afterShow: (ctx) async {});
    // Two risky entries → exactly two warning icons.
    expect(find.byIcon(Icons.warning_amber_rounded), findsNWidgets(2));
  });

  testWidgets('cancel returns null', (tester) async {
    InstallChoice? result;
    await tester.pumpWidget(MaterialApp(
      localizationsDelegates: const [
        AppLocalizations.delegate,
        GlobalMaterialLocalizations.delegate,
        GlobalWidgetsLocalizations.delegate,
        GlobalCupertinoLocalizations.delegate,
      ],
      supportedLocales: AppLocalizations.supportedLocales,
      home: Builder(builder: (ctx) => Scaffold(
        body: ElevatedButton(
          onPressed: () async {
            result = await InstallDialog.show(ctx,
                appName: 'X', version: '0.1.0',
                permissions: ['hub.invoke']);
          },
          child: const Text('open'),
        ),
      )),
    ));
    await tester.tap(find.text('open'));
    await tester.pumpAndSettle();
    // Find the dialog's cancel button (text comes from l10n fallback).
    await tester.tap(find.widgetWithText(TextButton, 'Cancel'));
    await tester.pumpAndSettle();
    expect(result, isNull);
  });

  testWidgets('install with all checked returns full set', (tester) async {
    InstallChoice? result;
    await tester.pumpWidget(MaterialApp(
      localizationsDelegates: const [
        AppLocalizations.delegate,
        GlobalMaterialLocalizations.delegate,
        GlobalWidgetsLocalizations.delegate,
        GlobalCupertinoLocalizations.delegate,
      ],
      supportedLocales: AppLocalizations.supportedLocales,
      home: Builder(builder: (ctx) => Scaffold(
        body: ElevatedButton(
          onPressed: () async {
            result = await InstallDialog.show(ctx,
                appName: 'X', version: '0.1.0',
                permissions: ['hub.invoke', 'wiki.write']);
          },
          child: const Text('open'),
        ),
      )),
    ));
    await tester.tap(find.text('open'));
    await tester.pumpAndSettle();
    await tester.tap(find.widgetWithText(FilledButton, 'Install'));
    await tester.pumpAndSettle();
    expect(result, isNotNull);
    expect(result!.grantedPermissions.toSet(), {'hub.invoke', 'wiki.write'});
  });

  testWidgets('unchecking a permission excludes it', (tester) async {
    InstallChoice? result;
    await tester.pumpWidget(MaterialApp(
      localizationsDelegates: const [
        AppLocalizations.delegate,
        GlobalMaterialLocalizations.delegate,
        GlobalWidgetsLocalizations.delegate,
        GlobalCupertinoLocalizations.delegate,
      ],
      supportedLocales: AppLocalizations.supportedLocales,
      home: Builder(builder: (ctx) => Scaffold(
        body: ElevatedButton(
          onPressed: () async {
            result = await InstallDialog.show(ctx,
                appName: 'X', version: '0.1.0',
                permissions: ['hub.invoke', 'notify.send']);
          },
          child: const Text('open'),
        ),
      )),
    ));
    await tester.tap(find.text('open'));
    await tester.pumpAndSettle();
    // Tap the notify.send row's checkbox (label is unique).
    await tester.tap(find.widgetWithText(CheckboxListTile, 'notify.send'));
    await tester.pumpAndSettle();
    await tester.tap(find.widgetWithText(FilledButton, 'Install'));
    await tester.pumpAndSettle();
    expect(result!.grantedPermissions, ['hub.invoke']);
  });

  testWidgets('renders fallback message when no permissions requested', (tester) async {
    await _open(tester, permissions: const [], afterShow: (ctx) async {});
    expect(
      find.text('This app requests no permissions.'),
      findsOneWidget,
    );
  });
}

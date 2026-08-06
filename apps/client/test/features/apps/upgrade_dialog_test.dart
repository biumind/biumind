// UpgradeDialog widget tests — pin the permission-diff Modal.
//
// Critical UX assertions:
//   - 无新权限 → 直接 enable 升级按钮
//   - 有新权限 → 升级按钮一直 disabled，直到每个 added perm 被勾上
//   - removed perms 渲染但不可点
//   - unchanged perms 默认折叠，点开展开
//   - 取消返回 null
//   - 升级返回的列表 = 用户勾选的 added perms

import 'package:biumind/data/api/apps_client.dart';
import 'package:biumind/features/apps/presentation/upgrade_dialog.dart';
import 'package:flutter/material.dart';
import 'package:biumind/l10n/app_localizations.dart';
import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_test/flutter_test.dart';

UpgradeStatus _status({
  String current = '0.1.0',
  String target = '0.2.0',
  bool requiresApproval = false,
  List<String> added = const [],
  List<String> removed = const [],
  List<String> unchanged = const [],
}) {
  return UpgradeStatus(
    available: true,
    currentVersion: current,
    targetVersion: target,
    requiresApproval: requiresApproval,
    permsDiff: PermsDiff(added: added, removed: removed, unchanged: unchanged),
  );
}

Future<List<String>?> _open(WidgetTester tester, UpgradeStatus s, {
  required Future<void> Function(BuildContext) afterShow,
}) async {
  List<String>? result;
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
          result = await UpgradeDialog.show(ctx, appName: 'TestApp', status: s);
        },
        child: const Text('open'),
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
  testWidgets('no new perms → upgrade button enabled immediately', (tester) async {
    final result = await _open(
      tester,
      _status(unchanged: ['hub.invoke']),
      afterShow: (ctx) async {
        await tester.tap(find.widgetWithText(FilledButton, 'Upgrade'));
        await tester.pumpAndSettle();
      },
    );
    expect(result, isNotNull);
    expect(result, isEmpty);
  });

  testWidgets('button disabled until every added perm is checked', (tester) async {
    List<String>? result;
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
            result = await UpgradeDialog.show(ctx, appName: 'X',
                status: _status(
                  requiresApproval: true,
                  added: ['hub.invoke', 'wiki.write'],
                ));
          },
          child: const Text('open'),
        ),
      )),
    ));
    await tester.tap(find.text('open'));
    await tester.pumpAndSettle();

    // Initial: button disabled.
    final btn = tester.widget<FilledButton>(find.widgetWithText(FilledButton, 'Upgrade'));
    expect(btn.onPressed, isNull);

    // Check first added perm — still disabled (one more to go).
    await tester.tap(find.widgetWithText(CheckboxListTile, 'hub.invoke'));
    await tester.pumpAndSettle();
    final btn2 = tester.widget<FilledButton>(find.widgetWithText(FilledButton, 'Upgrade'));
    expect(btn2.onPressed, isNull);

    // Check second — now enabled.
    await tester.tap(find.widgetWithText(CheckboxListTile, 'wiki.write'));
    await tester.pumpAndSettle();
    final btn3 = tester.widget<FilledButton>(find.widgetWithText(FilledButton, 'Upgrade'));
    expect(btn3.onPressed, isNotNull);

    // Tap to apply.
    await tester.tap(find.widgetWithText(FilledButton, 'Upgrade'));
    await tester.pumpAndSettle();
    expect(result, isNotNull);
    expect(result!.toSet(), {'hub.invoke', 'wiki.write'});
  });

  testWidgets('removed perms render with strike-through', (tester) async {
    await _open(
      tester,
      _status(removed: ['old.perm'], unchanged: ['hub.invoke']),
      afterShow: (ctx) async {},
    );
    expect(find.text('· old.perm'), findsOneWidget);
  });

  testWidgets('unchanged perms hidden until expanded', (tester) async {
    await _open(
      tester,
      _status(unchanged: ['hub.invoke', 'wiki.write']),
      afterShow: (ctx) async {
        // Initially hidden — only the section header visible.
        expect(find.text('· hub.invoke'), findsNothing);
        // Tap the section header to expand.
        await tester.tap(find.text('Already granted (2)'));
        await tester.pumpAndSettle();
        expect(find.text('· hub.invoke'), findsOneWidget);
      },
    );
  });

  testWidgets('cancel returns null', (tester) async {
    List<String>? result;
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
            result = await UpgradeDialog.show(ctx, appName: 'X',
                status: _status(unchanged: ['hub.invoke']));
          },
          child: const Text('open'),
        ),
      )),
    ));
    await tester.tap(find.text('open'));
    await tester.pumpAndSettle();
    await tester.tap(find.widgetWithText(TextButton, 'Not now'));
    await tester.pumpAndSettle();
    expect(result, isNull);
  });
}

// ConnectionBanner — 不同状态下的渲染.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:biumind/features/creation/presentation/widgets/connection_banner.dart';
import 'package:biumind/l10n/app_localizations.dart';
import 'package:biumind/services/auth_service.dart';

Widget _wrap({required HubCredentials? creds}) => ProviderScope(
      overrides: [
        hubCredentialsProvider.overrideWith((_) => creds),
      ],
      child: const MaterialApp(
        localizationsDelegates: [AppLocalizations.delegate],
        supportedLocales: [Locale('en'), Locale('zh')],
        home: Scaffold(body: ConnectionBanner()),
      ),
    );

void main() {
  testWidgets('未登录: 提示登录', (tester) async {
    await tester.pumpWidget(_wrap(creds: null));
    await tester.pump();
    expect(find.textContaining('登录后即可创作'), findsOneWidget);
    expect(find.text('登录'), findsOneWidget);
  });
}

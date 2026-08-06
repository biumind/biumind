// CharacterDialog + VoiceSheet 烟雾测 — 验证 sheet 打开 + 列出 + 取消.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:biumind/features/creation/application/aigc_providers.dart';
import 'package:biumind/features/creation/domain/character.dart';
import 'package:biumind/features/creation/presentation/widgets/character_dialog.dart';
import 'package:biumind/l10n/app_localizations.dart';

const _systemChar = CharacterEntry(
  id: 'sys-1',
  userId: '',
  name: '系统主播',
  avatarUrl: '',
  voiceDefault: 'BV001_streaming',
  isPublic: false,
  isSystem: true,
);

const _voice = VoiceEntry(
  id: 'BV001_streaming',
  name: '灿灿',
  provider: 'volcengine',
  language: 'zh-CN',
  gender: 'female',
  style: '温柔',
);

Widget _wrap(Widget child) => ProviderScope(
      overrides: [
        aigcCharactersProvider
            .overrideWith((ref) async => const [_systemChar]),
        aigcVoicesProvider.overrideWith((ref, _) async => const [_voice]),
      ],
      child: MaterialApp(
        localizationsDelegates: const [AppLocalizations.delegate],
        supportedLocales: const [Locale('en'), Locale('zh')],
        home: Scaffold(body: child),
      ),
    );

void main() {
  testWidgets('pickCharacter 打开 sheet, 显示系统内置标记 + 选择回填', (tester) async {
    CharacterEntry? picked;

    await tester.pumpWidget(_wrap(
      Builder(builder: (ctx) {
        return Center(
          child: ElevatedButton(
            onPressed: () async => picked = await pickCharacter(ctx),
            child: const Text('open'),
          ),
        );
      }),
    ));

    await tester.tap(find.text('open'));
    await tester.pumpAndSettle();

    expect(find.text('选择数字人'), findsOneWidget);
    expect(find.text('系统主播'), findsOneWidget);
    expect(find.text('内置'), findsOneWidget);

    await tester.tap(find.text('系统主播'));
    await tester.pumpAndSettle();

    expect(picked, isNotNull);
    expect(picked!.id, 'sys-1');
  });

  testWidgets('pickVoice 打开 sheet, 列出音色', (tester) async {
    VoiceEntry? picked;

    await tester.pumpWidget(_wrap(
      Builder(builder: (ctx) {
        return Center(
          child: ElevatedButton(
            onPressed: () async => picked = await pickVoice(ctx),
            child: const Text('open'),
          ),
        );
      }),
    ));

    await tester.tap(find.text('open'));
    await tester.pumpAndSettle();

    expect(find.text('选择音色'), findsOneWidget);
    expect(find.text('灿灿'), findsOneWidget);

    await tester.tap(find.text('灿灿'));
    await tester.pumpAndSettle();

    expect(picked, isNotNull);
    expect(picked!.id, 'BV001_streaming');
  });
}

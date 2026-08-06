// CreationCard — 三态渲染 smoke test.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:biumind/features/creation/domain/creation_task.dart';
import 'package:biumind/features/creation/presentation/widgets/creation_card.dart';
import 'package:biumind/l10n/app_localizations.dart';

CreationTask _task({
  required TaskStatus status,
  int progress = 0,
  List<TaskOutput> outputs = const [],
  String? errorMessage,
}) {
  final now = DateTime.utc(2026, 6, 6);
  return CreationTask(
    id: 't-${status.wire}',
    userId: 'u1',
    type: 'image',
    modelCode: 'wanx-2.6-t2i',
    prompt: 'a cute cat',
    status: status,
    progress: progress,
    outputs: outputs,
    errorMessage: errorMessage,
    createdAt: now,
    updatedAt: now,
  );
}

Future<void> _pumpCard(WidgetTester tester, CreationTask t,
    {VoidCallback? onRetry}) async {
  await tester.pumpWidget(
    ProviderScope(
      child: MaterialApp(
        localizationsDelegates: const [AppLocalizations.delegate],
        supportedLocales: const [Locale('en'), Locale('zh')],
        home: Scaffold(
          body: SizedBox(
            width: 240,
            height: 240,
            child: CreationCard(task: t, onRetry: onRetry),
          ),
        ),
      ),
    ),
  );
  await tester.pump();
}

void main() {
  group('CreationCard', () {
    testWidgets('active 态渲染进度条 + status', (tester) async {
      await _pumpCard(
        tester,
        _task(status: TaskStatus.running, progress: 42),
      );
      expect(find.byType(LinearProgressIndicator), findsOneWidget);
      expect(find.textContaining('42'), findsOneWidget);
    });

    testWidgets('queued 态有取消按钮', (tester) async {
      await _pumpCard(tester, _task(status: TaskStatus.queued));
      expect(find.byIcon(Icons.close), findsOneWidget);
    });

    testWidgets('completed 态: hover overlay 默认隐藏 (opacity 0), 输出有 1 张时无 +N 角标',
        (tester) async {
      await _pumpCard(
        tester,
        _task(
          status: TaskStatus.completed,
          progress: 100,
          outputs: [
            TaskOutput(idx: 0, kind: 'image', sha256: 'a' * 64, url: 'cas:${'a' * 64}'),
          ],
        ),
      );
      // 角标只有 outputs.length>1 才显示
      expect(find.text('+1'), findsNothing);
    });

    testWidgets('completed 态多输出: 显示 +N 角标', (tester) async {
      await _pumpCard(
        tester,
        _task(
          status: TaskStatus.completed,
          outputs: [
            TaskOutput(idx: 0, kind: 'image', sha256: 'a' * 64, url: 'cas:${'a' * 64}'),
            TaskOutput(idx: 1, kind: 'image', sha256: 'b' * 64, url: 'cas:${'b' * 64}'),
          ],
        ),
      );
      expect(find.text('+2'), findsOneWidget);
    });

    testWidgets('failed 态: error icon + 重试按钮 (有 onRetry)', (tester) async {
      await _pumpCard(
        tester,
        _task(status: TaskStatus.failed, errorMessage: 'rate limit'),
        onRetry: () {},
      );
      expect(find.byIcon(Icons.error_outline), findsOneWidget);
      expect(find.byIcon(Icons.refresh), findsOneWidget);
    });

    testWidgets('blocked 态: 盾牌 icon + 黄色 tone, 无重试', (tester) async {
      await _pumpCard(
        tester,
        _task(
          status: TaskStatus.blocked,
          errorMessage: 'content moderation: nsfw',
        ),
        onRetry: () {},
      );
      expect(find.byIcon(Icons.shield_outlined), findsOneWidget);
      expect(find.byIcon(Icons.refresh), findsNothing);
    });

    testWidgets('cancelled 态: block icon, 无重试', (tester) async {
      await _pumpCard(tester, _task(status: TaskStatus.cancelled));
      expect(find.byIcon(Icons.block), findsOneWidget);
    });
  });
}

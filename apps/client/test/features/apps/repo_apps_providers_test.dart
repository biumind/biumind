// Repo Apps providers 失效测试（M1.14）。
//
// 不走真实 HTTP（TestWidgetsFlutterBinding 会拦截 HttpClient）——
// repoAppBuildsProvider 直接 overrideWith 计数工厂，pin：
//   1. family 缓存语义（同 installId 只拉一次）
//   2. AppsRefresh.invalidateRepoBuilds(installId) 精准失效 —— 只清该
//      installId 的缓存，不动其他 key。
// client → HTTP 的契约由 apps_client_test.dart 覆盖。

import 'package:biumind/data/api/apps_client.dart';
import 'package:biumind/data/apps_providers.dart';
import 'package:biumind/services/auth_service.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  testWidgets('invalidateRepoBuilds 精准失效单个 installId 的缓存',
      (tester) async {
    final callCounts = <String, int>{};
    final container = ProviderContainer(overrides: [
      repoAppBuildsProvider.overrideWith((ref, installId) async {
        callCounts[installId] = (callCounts[installId] ?? 0) + 1;
        return [RepoBuild(id: 'b1', createdAt: DateTime(2026, 8, 23))];
      }),
    ]);
    addTearDown(container.dispose);

    late WidgetRef capturedRef;
    await tester.pumpWidget(UncontrolledProviderScope(
      container: container,
      child: MaterialApp(
        home: Consumer(builder: (context, ref, _) {
          capturedRef = ref;
          // watch 两个 key，验证失效粒度。
          final a = ref.watch(repoAppBuildsProvider('i1'));
          final b = ref.watch(repoAppBuildsProvider('i2'));
          return Text(
              'a=${a.valueOrNull?.length ?? -1} b=${b.valueOrNull?.length ?? -1}');
        }),
      ),
    ));

    // 等两个 family 都 resolve。
    for (var i = 0; i < 50; i++) {
      await tester.pump(const Duration(milliseconds: 20));
      if (find.text('a=1 b=1').evaluate().isNotEmpty) break;
    }
    expect(find.text('a=1 b=1'), findsOneWidget);
    expect(callCounts, {'i1': 1, 'i2': 1});

    // 失效 i1 → 只有 i1 重新拉取。
    capturedRef.invalidateRepoBuilds('i1');
    for (var i = 0; i < 50; i++) {
      await tester.pump(const Duration(milliseconds: 20));
      if ((callCounts['i1'] ?? 0) >= 2) break;
    }
    expect(callCounts['i1'], 2);
    expect(callCounts['i2'], 1);
  });

  test('repoAnalyzeProvider 未配置凭据 → null 降级不炸', () async {
    final container = ProviderContainer(overrides: [
      hubCredentialsProvider.overrideWithValue(null),
    ]);
    addTearDown(container.dispose);
    final result = await container
        .read(repoAnalyzeProvider('https://github.com/x/y').future);
    expect(result, isNull);
  });

  test('repoAppBuildsProvider 未配置凭据 → 空列表降级', () async {
    final container = ProviderContainer(overrides: [
      hubCredentialsProvider.overrideWithValue(null),
    ]);
    addTearDown(container.dispose);
    final result = await container.read(repoAppBuildsProvider('i1').future);
    expect(result, isEmpty);
  });
}

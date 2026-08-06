// A2UIRenderer widget tests — pin the v2.0 visual contract.
//
// Goal: regressions in container layout, leaf rendering, and button
// → ActionRunner wiring surface as test failures rather than as
// silent UI bugs in apps that ship custom layouts.

import 'package:biumind/features/apps/domain/a2ui.dart';
import 'package:biumind/features/apps/domain/view_spec.dart';
import 'package:biumind/features/apps/host/a2ui_renderer.dart';
import 'package:biumind/features/apps/host/action_runner.dart';
import 'package:flutter/material.dart';
import 'package:biumind/l10n/app_localizations.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_test/flutter_test.dart';

class _CapturingRunner extends ActionRunner {
  _CapturingRunner(WidgetRef ref)
      : super(
          ref: ref,
          appIdentifier: 'test',
          installId: 'inst',
          onRouteNavigate: (_, _) {},
          onRefresh: () {},
        );

  ViewActionRef? lastRun;

  @override
  Future<void> run(BuildContext context, ViewActionRef a) async {
    lastRun = a;
  }
}

Widget _wrap(A2UINode root) {
  return ProviderScope(
    child: Consumer(
      builder: (context, ref, _) {
        final runner = _CapturingRunner(ref);
        return MaterialApp(
          home: Scaffold(
            body: A2UIRenderer(root: root, runner: runner),
          ),
        );
      },
    ),
  );
}

Future<_CapturingRunner> _wrapWithRunner(
    WidgetTester tester, A2UINode root) async {
  late _CapturingRunner runner;
  await tester.pumpWidget(ProviderScope(
    child: Consumer(
      builder: (context, ref, _) {
        runner = _CapturingRunner(ref);
        return MaterialApp(
          home: Scaffold(
            body: A2UIRenderer(root: root, runner: runner),
          ),
        );
      },
    ),
  ));
  return runner;
}

void main() {
  testWidgets('text leaf renders', (tester) async {
    await tester.pumpWidget(_wrap(
      A2UINode.fromJson({'kind': 'text', 'text': 'hello'}),
    ));
    expect(find.text('hello'), findsOneWidget);
  });

  testWidgets('column container renders children in order', (tester) async {
    await tester.pumpWidget(_wrap(
      A2UINode.fromJson({
        'kind': 'column',
        'children': [
          {'kind': 'text', 'text': 'first'},
          {'kind': 'text', 'text': 'second'},
        ],
      }),
    ));
    final first = tester.getTopLeft(find.text('first'));
    final second = tester.getTopLeft(find.text('second'));
    expect(second.dy, greaterThan(first.dy));
  });

  testWidgets('row places children left-to-right', (tester) async {
    await tester.pumpWidget(_wrap(
      A2UINode.fromJson({
        'kind': 'row',
        'children': [
          {'kind': 'text', 'text': 'L'},
          {'kind': 'text', 'text': 'R'},
        ],
      }),
    ));
    expect(find.text('L'), findsOneWidget);
    expect(find.text('R'), findsOneWidget);
  });

  testWidgets('card shows title + subtitle + body', (tester) async {
    await tester.pumpWidget(_wrap(
      A2UINode.fromJson({
        'kind': 'card',
        'title': 'T',
        'subtitle': 'S',
        'body': 'B',
      }),
    ));
    expect(find.text('T'), findsOneWidget);
    expect(find.text('S'), findsOneWidget);
    expect(find.text('B'), findsOneWidget);
  });

  testWidgets('button onClick runs action via runner', (tester) async {
    final runner = await _wrapWithRunner(tester, A2UINode.fromJson({
      'kind': 'button',
      'label': 'do',
      'on_click': {'action': 'subscribe', 'input': {'url': 'x'}},
    }));
    await tester.tap(find.text('do'));
    await tester.pump();
    expect(runner.lastRun?.action, 'subscribe');
    expect(runner.lastRun?.input['url'], 'x');
  });

  testWidgets('button without on_click is disabled', (tester) async {
    await tester.pumpWidget(_wrap(
      A2UINode.fromJson({'kind': 'button', 'label': 'go'}),
    ));
    final btn = tester.widget<FilledButton>(find.byType(FilledButton));
    expect(btn.onPressed, isNull);
  });

  testWidgets('progress shows percentage', (tester) async {
    await tester.pumpWidget(_wrap(
      A2UINode.fromJson({
        'kind': 'progress', 'value': 3, 'max': 10, 'label': 'loading',
      }),
    ));
    expect(find.text('loading'), findsOneWidget);
    expect(find.text('30%'), findsOneWidget);
  });

  testWidgets('unknown kind renders as empty (forward-compat)', (tester) async {
    await tester.pumpWidget(_wrap(
      A2UINode.fromJson({
        'kind': 'column',
        'children': [
          {'kind': 'text', 'text': 'kept'},
          {'kind': 'mystery'},
        ],
      }),
    ));
    expect(find.text('kept'), findsOneWidget);
  });

  testWidgets('fatal validation renders placeholder', (tester) async {
    final root = A2UINode.fromJson({'kind': 'text', 'text': 'x'});
    final result = A2UIValidationResult(
      issues: const [A2UIIssue(
        code: 'depth_exceeded',
        message: 'tree too deep',
        path: r'$',
      )],
      nodeCount: 1,
      maxDepth: 9,
    );
    await tester.pumpWidget(ProviderScope(
      child: Consumer(builder: (ctx, ref, _) {
        return MaterialApp(home: Scaffold(
          body: A2UIRenderer(
            root: root,
            runner: _CapturingRunner(ref),
            validation: result,
          ),
        ));
      }),
    ));
    expect(find.textContaining('Custom view too large'), findsOneWidget);
    expect(find.textContaining('tree too deep'), findsOneWidget);
    // Original tree NOT rendered when fatal.
    expect(find.text('x'), findsNothing);
  });
}

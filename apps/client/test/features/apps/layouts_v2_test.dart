// Widget-level smoke tests for M16 layouts.
//
// We don't go deep on golden pixel diffs — just verify each layout
// hydrates without throwing on representative manifests, picks the
// expected widget kinds, and respects the user-facing knobs that
// users will reach for first (column counts, card spans, empty
// state, agent_chat deep-link).

import 'package:biumind/features/apps/domain/view_spec.dart';
import 'package:biumind/features/apps/host/action_runner.dart';
import 'package:biumind/features/apps/host/layouts_v2.dart';
import 'package:flutter/material.dart';
import 'package:biumind/l10n/app_localizations.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_test/flutter_test.dart';

ActionRunner _stubRunner(WidgetRef ref) => ActionRunner(
      ref: ref,
      appIdentifier: 'test',
      installId: 'install-1',
      onRouteNavigate: (_, _) {},
    );

Widget _wrap(Widget child) {
  return ProviderScope(
    child: MaterialApp(
      home: Consumer(
        builder: (context, ref, _) => Scaffold(body: child),
      ),
    ),
  );
}

void main() {
  testWidgets('GridLayout shows empty state when items missing',
      (tester) async {
    late ActionRunner runner;
    await tester.pumpWidget(_wrap(
      Consumer(builder: (context, ref, _) {
        runner = _stubRunner(ref);
        return GridLayout(
          spec: const ViewSpec(
            id: 'g',
            route: '/apps/g/home',
            title: 'Grid',
            layout: ViewLayout.grid,
          ),
          data: const {},
          routeParams: const {},
          runner: runner,
        );
      }),
    ));
    await tester.pumpAndSettle();
    expect(find.text('暂无数据'), findsOneWidget);
  });

  testWidgets('GridLayout renders one tile per item with title',
      (tester) async {
    await tester.binding.setSurfaceSize(const Size(800, 600));
    addTearDown(() => tester.binding.setSurfaceSize(null));
    await tester.pumpWidget(_wrap(
      Consumer(builder: (context, ref, _) {
        return GridLayout(
          spec: const ViewSpec(
            id: 'g',
            route: '/apps/g/home',
            title: 'Grid',
            layout: ViewLayout.grid,
            itemTemplate: ViewItemTemplate(
              kind: 'card',
              title: r'${item.title}',
            ),
            grid: ViewGrid(columns: [1, 2, 3]),
          ),
          data: const {
            'items': [
              {'id': '1', 'title': 'Alpha'},
              {'id': '2', 'title': 'Bravo'},
              {'id': '3', 'title': 'Charlie'},
            ],
          },
          routeParams: const {},
          runner: _stubRunner(ref),
        );
      }),
    ));
    await tester.pumpAndSettle();
    expect(find.text('Alpha'), findsOneWidget);
    expect(find.text('Bravo'), findsOneWidget);
    expect(find.text('Charlie'), findsOneWidget);
  });

  testWidgets('DashboardLayout falls back to placeholder when no cards',
      (tester) async {
    await tester.pumpWidget(_wrap(
      Consumer(builder: (context, ref, _) {
        return DashboardLayout(
          spec: const ViewSpec(
            id: 'd',
            route: '/apps/d/home',
            title: 'Dashboard',
            layout: ViewLayout.dashboard,
          ),
          installId: 'install-1',
          appIdentifier: 'test',
          routeParams: const {},
          runner: _stubRunner(ref),
        );
      }),
    ));
    await tester.pumpAndSettle();
    expect(find.text('dashboard 没有 cards'), findsOneWidget);
  });

  testWidgets('AgentChatLayout shows agent id + open button', (tester) async {
    await tester.pumpWidget(_wrap(
      const AgentChatLayout(
        spec: ViewSpec(
          id: 'c',
          route: '/apps/c/chat',
          title: 'Chat',
          layout: ViewLayout.agentChat,
          agentId: 'agent-xyz',
          agentChat: ViewAgentChat(
            initialPrompt: 'Help me',
            toolFilter: ['email.', 'memory.'],
          ),
        ),
        routeParams: {},
      ),
    ));
    await tester.pumpAndSettle();
    expect(find.textContaining('Agent: agent-xyz'), findsOneWidget);
    expect(find.text('Help me'), findsOneWidget);
    expect(find.text('email.'), findsOneWidget);
    expect(find.text('memory.'), findsOneWidget);
    expect(find.text('打开会话'), findsOneWidget);
  });

  testWidgets('AgentChatLayout shows empty state without agent_id',
      (tester) async {
    await tester.pumpWidget(_wrap(
      const AgentChatLayout(
        spec: ViewSpec(
          id: 'c',
          route: '/apps/c/chat',
          title: '',
          layout: ViewLayout.agentChat,
        ),
        routeParams: {},
      ),
    ));
    await tester.pumpAndSettle();
    expect(find.text('agent_chat 缺少 agent_id'), findsOneWidget);
  });
}

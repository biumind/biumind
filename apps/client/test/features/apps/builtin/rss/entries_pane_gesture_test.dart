// M12.1.4 — entries_pane swipe gestures (phone-width).
//
// Verifies that on a narrow viewport a left swipe (endToStart) on an entry
// row invokes entries_mark_read, and a right swipe (startToEnd) invokes
// entries_star. We fake RssApi (override invoke to record calls) and stub
// the entries list provider.

import 'package:biumind/data/api/apps_client.dart';
import 'package:biumind/features/apps/builtin/rss/models.dart';
import 'package:biumind/features/apps/builtin/rss/providers.dart';
import 'package:biumind/features/apps/builtin/rss/widgets/entries_pane.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

class _FakeRssApi extends RssApi {
  _FakeRssApi() : super(AppsClient(Uri.parse('http://test.invalid')), 'tok');
  final calls = <String>[];
  final inputs = <Map<String, dynamic>>[];

  @override
  Future<Map<String, dynamic>> invoke(String action,
      [Map<String, dynamic> input = const {}]) async {
    calls.add(action);
    inputs.add(input);
    return <String, dynamic>{};
  }
}

const _entry = Entry(
  id: 'e1',
  feedId: 'f1',
  title: 'Swipe me',
  unread: true,
);

Future<_FakeRssApi> _pump(WidgetTester tester) async {
  final api = _FakeRssApi();
  await tester.pumpWidget(
    ProviderScope(
      overrides: [
        rssApiProvider.overrideWithValue(api),
        entriesProvider(const EntriesQuery(feedId: 'all'))
            .overrideWith((ref) => Stream.value(const [_entry])),
      ],
      child: const MaterialApp(
        home: MediaQuery(
          // < kRssNarrowWidth → swipe gestures enabled.
          data: MediaQueryData(size: Size(400, 800)),
          child: Scaffold(body: EntriesPane()),
        ),
      ),
    ),
  );
  await tester.pumpAndSettle();
  return api;
}

void main() {
  testWidgets('left swipe marks the entry read', (tester) async {
    final api = await _pump(tester);
    expect(find.text('Swipe me'), findsOneWidget);

    await tester.drag(find.text('Swipe me'), const Offset(-500, 0));
    await tester.pumpAndSettle();

    expect(api.calls, contains('entries_mark_read'));
    final markCall = api.inputs[api.calls.indexOf('entries_mark_read')];
    expect(markCall['id'], 'e1');
    expect(markCall['read'], true);
  });

  testWidgets('right swipe stars the entry', (tester) async {
    final api = await _pump(tester);
    await tester.drag(find.text('Swipe me'), const Offset(500, 0));
    await tester.pumpAndSettle();

    expect(api.calls, contains('entries_star'));
    final starCall = api.inputs[api.calls.indexOf('entries_star')];
    expect(starCall['id'], 'e1');
    expect(starCall['starred'], true);
  });

  testWidgets('wide viewport disables swipe (no Dismissible)', (tester) async {
    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          rssApiProvider.overrideWithValue(_FakeRssApi()),
          entriesProvider(const EntriesQuery(feedId: 'all'))
              .overrideWith((ref) => Stream.value(const [_entry])),
        ],
        child: const MaterialApp(
          home: MediaQuery(
            data: MediaQueryData(size: Size(1200, 800)),
            child: Scaffold(body: EntriesPane()),
          ),
        ),
      ),
    );
    await tester.pumpAndSettle();
    expect(find.text('Swipe me'), findsOneWidget);
    expect(find.byType(Dismissible), findsNothing);
  });
}

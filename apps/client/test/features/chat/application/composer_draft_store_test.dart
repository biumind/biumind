// ComposerDraftStore —— per-thread 草稿单测。

import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'package:biumind/features/chat/application/composer_draft_store.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  setUp(() {
    SharedPreferences.setMockInitialValues({});
  });

  test('load empty when no draft saved', () async {
    expect(await ComposerDraftStore.load('t1'), '');
  });

  test('save then load roundtrip', () async {
    await ComposerDraftStore.save('t1', 'half-typed prompt');
    expect(await ComposerDraftStore.load('t1'), 'half-typed prompt');
  });

  test('drafts isolated per thread', () async {
    await ComposerDraftStore.save('t1', 'aaa');
    await ComposerDraftStore.save('t2', 'bbb');
    expect(await ComposerDraftStore.load('t1'), 'aaa');
    expect(await ComposerDraftStore.load('t2'), 'bbb');
  });

  test('save empty string clears the key', () async {
    await ComposerDraftStore.save('t1', 'something');
    await ComposerDraftStore.save('t1', '');
    expect(await ComposerDraftStore.load('t1'), '');
    final prefs = await SharedPreferences.getInstance();
    expect(prefs.containsKey('biu.chat.draft.t1'), false);
  });

  test('clear removes the key', () async {
    await ComposerDraftStore.save('t1', 'x');
    await ComposerDraftStore.clear('t1');
    expect(await ComposerDraftStore.load('t1'), '');
  });

  test('listAll returns all non-empty drafts keyed by threadId', () async {
    await ComposerDraftStore.save('t1', 'aaa');
    await ComposerDraftStore.save('t2', 'bbb');
    await ComposerDraftStore.save('t3', '');
    final all = await ComposerDraftStore.listAll();
    expect(all.length, 2);
    expect(all['t1'], 'aaa');
    expect(all['t2'], 'bbb');
    expect(all.containsKey('t3'), false);
  });

  test('listAll returns empty when no drafts saved', () async {
    final all = await ComposerDraftStore.listAll();
    expect(all, isEmpty);
  });
}

// NewThreadMemory —— NewThreadDialog 字段记忆单测。

import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'package:biumind/features/chat/application/new_thread_memory.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  setUp(() {
    SharedPreferences.setMockInitialValues({});
  });

  test('load empty when nothing saved', () async {
    final m = await NewThreadMemoryStore.load();
    expect(m.systemPrompt, '');
    expect(m.poolTag, '');
  });

  test('save then load roundtrip', () async {
    await NewThreadMemoryStore.save(const NewThreadMemory(
      systemPrompt: '你是 Flutter 架构师',
      poolTag: 'pool-a',
    ));
    final m = await NewThreadMemoryStore.load();
    expect(m.systemPrompt, '你是 Flutter 架构师');
    expect(m.poolTag, 'pool-a');
  });

  test('save overwrites previous', () async {
    await NewThreadMemoryStore.save(
      const NewThreadMemory(systemPrompt: 'a'),
    );
    await NewThreadMemoryStore.save(
      const NewThreadMemory(systemPrompt: 'b', poolTag: 'p'),
    );
    final m = await NewThreadMemoryStore.load();
    expect(m.systemPrompt, 'b');
    expect(m.poolTag, 'p');
  });

  test('JSON round-trip preserves fields', () {
    const m = NewThreadMemory(systemPrompt: 'sp', poolTag: 'pt');
    final j = m.toJson();
    final back = NewThreadMemory.fromJson(j);
    expect(back.systemPrompt, 'sp');
    expect(back.poolTag, 'pt');
  });

  test('fromJson missing fields → defaults', () {
    final m = NewThreadMemory.fromJson(const {});
    expect(m.systemPrompt, '');
    expect(m.poolTag, '');
  });
}

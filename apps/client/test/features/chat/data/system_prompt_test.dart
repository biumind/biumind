// ChatRepo.setSystemPrompt —— P0-补 Thread 设置 sheet 后端单测。

import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:biumind/data/local/db.dart';
import 'package:biumind/features/chat/data/chat_repo.dart';
import 'package:biumind/features/chat/domain/chat_models.dart';

void main() {
  late AppDb db;
  late ChatRepo repo;

  setUp(() async {
    db = AppDb.memory();
    repo = ChatRepo(db);
    await repo.createThread(
        id: 't1', mode: ThreadMode.chat, title: 'sample');
  });
  tearDown(() async {
    await db.close();
  });

  test('initially null', () async {
    final t = await repo.getThread('t1');
    expect(t!.systemPrompt, isNull);
  });

  test('setSystemPrompt persists value', () async {
    await repo.setSystemPrompt('t1', '你是一个 Flutter 架构师');
    final t = await repo.getThread('t1');
    expect(t!.systemPrompt, '你是一个 Flutter 架构师');
  });

  test('setSystemPrompt to null clears it', () async {
    await repo.setSystemPrompt('t1', 'hello');
    await repo.setSystemPrompt('t1', null);
    final t = await repo.getThread('t1');
    expect(t!.systemPrompt, isNull);
  });

  test('updates updatedAt timestamp', () async {
    final before = await repo.getThread('t1');
    // Drift 默认 1s 粒度的 unix timestamp 存储 → sleep 至少 1.1s 让 tick 走过去。
    await Future.delayed(const Duration(milliseconds: 1100));
    await repo.setSystemPrompt('t1', 'x');
    final after = await repo.getThread('t1');
    expect(after!.updatedAt.isAfter(before!.updatedAt), true);
  });
}

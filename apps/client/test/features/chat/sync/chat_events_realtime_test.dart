// ChatEventsListener._onFrame 分支逻辑单测 —— AppDb.memory() + 直接投递
// RealtimeFrame（经 debugHandleFrame 测试钩子，不拉 SSE）。
// 覆盖:
//   1. chat.thread_deleted → 本地级联删（跨设备删除传播）
//   2. thread_deleted 缺 thread_id → no-op
//   3. 未知 kind → 静默忽略（前向兼容）

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:biumind/data/local/db.dart';
import 'package:biumind/data/sse/realtime_hub.dart';
import 'package:biumind/data/wiki_providers.dart' show appDbProvider;
import 'package:biumind/features/chat/data/chat_repo.dart';
import 'package:biumind/features/chat/domain/chat_models.dart';
import 'package:biumind/features/chat/sync/chat_events_realtime.dart';

// 从 container 拿一个 Ref —— ChatEventsListener 构造吃 Ref 不吃 container。
final _refProbe = Provider<Ref>((ref) => ref);

RealtimeFrame _frame(String kind, Map<String, dynamic> payload) {
  return RealtimeFrame(
    id: 'e1',
    topic: 'chat:user:u1',
    kind: kind,
    payload: payload,
  );
}

void main() {
  late AppDb db;
  late ChatRepo repo;
  late ProviderContainer container;
  late ChatEventsListener listener;

  setUp(() {
    db = AppDb.memory();
    repo = ChatRepo(db);
    container = ProviderContainer(overrides: [
      appDbProvider.overrideWithValue(db),
    ]);
    // resolveService 返 null → thread_deleted 分支走 appDb 兜底 repo。
    listener = ChatEventsListener(
      container.read(_refProbe),
      resolveService: () => null,
    );
  });

  tearDown(() async {
    container.dispose();
    await db.close();
  });

  Future<void> flush() async {
    // deleteThreads 是 unawaited future —— 让事件队列跑完。
    for (var i = 0; i < 10; i++) {
      await Future<void>.delayed(Duration.zero);
    }
  }

  test('chat.thread_deleted deletes the thread locally', () async {
    await repo.createThread(id: 't1', mode: ThreadMode.chat, title: 'gone');
    expect(await repo.getThread('t1'), isNotNull);

    listener.debugHandleFrame(_frame('chat.thread_deleted', {
      'event_id': 'e1',
      'event_type': 'chat.thread_deleted',
      'data': {'thread_id': 't1'},
    }));
    await flush();

    expect(await repo.getThread('t1'), isNull);
  });

  test('chat.thread_deleted without thread_id is a no-op', () async {
    await repo.createThread(id: 't1', mode: ThreadMode.chat);

    listener.debugHandleFrame(_frame('chat.thread_deleted', {
      'event_id': 'e1',
      'event_type': 'chat.thread_deleted',
      'data': <String, dynamic>{},
    }));
    await flush();

    expect(await repo.getThread('t1'), isNotNull);
  });

  test('chat.message_deleted deletes the message locally', () async {
    await repo.createThread(id: 't1', mode: ThreadMode.chat);
    await repo.appendMessage(
      id: 'm1',
      threadId: 't1',
      role: MessageRole.user,
      status: MessageStatus.completed,
    );
    expect(await repo.getMessage('m1'), isNotNull);

    listener.debugHandleFrame(_frame('chat.message_deleted', {
      'event_id': 'e1',
      'event_type': 'chat.message_deleted',
      'data': {'message_id': 'm1'},
    }));
    await flush();

    expect(await repo.getMessage('m1'), isNull);
  });

  test('chat.message_deleted without message_id is a no-op', () async {
    await repo.createThread(id: 't1', mode: ThreadMode.chat);
    await repo.appendMessage(
      id: 'm1',
      threadId: 't1',
      role: MessageRole.user,
      status: MessageStatus.completed,
    );

    listener.debugHandleFrame(_frame('chat.message_deleted', {
      'event_id': 'e1',
      'event_type': 'chat.message_deleted',
      'data': <String, dynamic>{},
    }));
    await flush();

    expect(await repo.getMessage('m1'), isNotNull);
  });

  test('unknown kind is silently ignored', () async {
    await repo.createThread(id: 't1', mode: ThreadMode.chat);

    listener.debugHandleFrame(_frame('chat.some_future_event', {
      'data': {'thread_id': 't1'},
    }));
    await flush();

    expect(await repo.getThread('t1'), isNotNull);
  });
}

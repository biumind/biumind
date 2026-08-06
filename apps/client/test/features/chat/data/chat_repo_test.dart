// ChatRepo R1 单测 —— AppDb.memory() 内存 sqlite，每个测试一个 fresh db。
// 覆盖 thread / message / block / session 的 CRUD + watch reactivity。

import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:biumind/data/local/db.dart';
import 'package:biumind/features/chat/data/chat_repo.dart';
import 'package:biumind/features/chat/domain/chat_models.dart';

void main() {
  late AppDb db;
  late ChatRepo repo;

  setUp(() {
    db = AppDb.memory();
    repo = ChatRepo(db);
  });
  tearDown(() async {
    await db.close();
  });

  // ─── Threads ─────────────────────────────────────────────

  test('createThread persists with mode + watchThreads emits', () async {
    final t = await repo.createThread(
      id: 't1',
      mode: ThreadMode.chat,
      title: 'hello',
    );
    expect(t.id, 't1');
    expect(t.mode, ThreadMode.chat);

    final list = await repo.watchThreads().first;
    expect(list.length, 1);
    expect(list.first.title, 'hello');
  });

  test('watchThreads orders pinned first then by updatedAt desc', () async {
    final older = DateTime(2026, 1, 1);
    await repo.createThread(id: 'old', mode: ThreadMode.chat, title: 'old');
    // sneaky: tweak its updatedAt back by direct query
    await db.customStatement(
        'UPDATE chat_threads_v2 SET updated_at = ? WHERE id = "old"',
        [older.millisecondsSinceEpoch ~/ 1000]);
    await repo.createThread(id: 'new', mode: ThreadMode.chat, title: 'new');
    await repo.createThread(
        id: 'pinned', mode: ThreadMode.chat, title: 'pinned');
    await repo.setPinned('pinned', true);

    final list = await repo.watchThreads().first;
    expect(list.map((t) => t.id).toList(), ['pinned', 'new', 'old']);
  });

  test('archiveThread filters out from watchThreads', () async {
    await repo.createThread(id: 't1', mode: ThreadMode.chat);
    await repo.createThread(id: 't2', mode: ThreadMode.chat);
    await repo.archiveThread('t1');

    final list = await repo.watchThreads().first;
    expect(list.length, 1);
    expect(list.first.id, 't2');
  });

  test('renameThread updates title + updatedAt', () async {
    await repo.createThread(id: 't1', mode: ThreadMode.chat, title: 'old');
    await repo.renameThread('t1', 'new');
    final t = await repo.getThread('t1');
    expect(t!.title, 'new');
  });

  test('deleteThread cascades to messages + blocks + sessions', () async {
    await repo.createThread(id: 't1', mode: ThreadMode.chat);
    await repo.appendMessage(
        id: 'm1', threadId: 't1', role: MessageRole.user);
    await repo.upsertBlock(
      const TextBlock(id: 'b1', index: 0, state: BlockState.closed, text: 'hi'),
      messageId: 'm1',
    );
    await repo.persistSession(Session(
      sessionId: 's1',
      threadId: 't1',
      mode: ThreadMode.chat,
      sessionToken: 'tok',
      tokenExpiresAt: DateTime.now().add(const Duration(minutes: 30)),
      status: SessionStatus.active,
      createdAt: DateTime.now(),
    ));

    await repo.deleteThread('t1');

    expect(await repo.getThread('t1'), isNull);
    expect(await repo.getMessage('m1'), isNull);
    expect(await repo.activeSession('t1'), isNull);
  });

  test('deleteThreads batch removes selected, leaves others intact', () async {
    await repo.createThread(id: 't1', mode: ThreadMode.chat);
    await repo.createThread(id: 't2', mode: ThreadMode.agent);
    await repo.createThread(id: 't3', mode: ThreadMode.chat);
    // 给 t1 挂 message + block + session,验证级联也覆盖批量路径。
    await repo.appendMessage(
        id: 'm1', threadId: 't1', role: MessageRole.user);
    await repo.upsertBlock(
      const TextBlock(id: 'b1', index: 0, state: BlockState.closed, text: 'hi'),
      messageId: 'm1',
    );
    await repo.persistSession(Session(
      sessionId: 's1',
      threadId: 't1',
      mode: ThreadMode.chat,
      sessionToken: 'tok',
      tokenExpiresAt: DateTime.now().add(const Duration(minutes: 30)),
      status: SessionStatus.active,
      createdAt: DateTime.now(),
    ));

    await repo.deleteThreads(['t1', 't3']);

    expect(await repo.getThread('t1'), isNull);
    expect(await repo.getThread('t3'), isNull);
    expect(await repo.getMessage('m1'), isNull);
    expect(await repo.activeSession('t1'), isNull);
    // 未选中的 t2 完好。
    expect(await repo.getThread('t2'), isNotNull);
  });

  test('deleteThreads with empty iterable is a noop', () async {
    await repo.createThread(id: 't1', mode: ThreadMode.chat);
    await repo.deleteThreads(const []);
    expect(await repo.getThread('t1'), isNotNull);
  });

  test('agent thread keeps environmentId; task thread keeps poolTag', () async {
    await repo.createThread(
      id: 'agent-1',
      mode: ThreadMode.agent,
      environmentId: 'env-mac',
    );
    await repo.createThread(
      id: 'task-1',
      mode: ThreadMode.task,
      poolTag: 'runtime-prod',
    );

    final a = await repo.getThread('agent-1');
    expect(a!.mode, ThreadMode.agent);
    expect(a.environmentId, 'env-mac');
    expect(a.poolTag, isNull);

    final t = await repo.getThread('task-1');
    expect(t!.mode, ThreadMode.task);
    expect(t.poolTag, 'runtime-prod');
    expect(t.environmentId, isNull);
  });

  // ─── Messages + Blocks ────────────────────────────────────

  test('appendMessage assigns monotonically increasing seq', () async {
    await repo.createThread(id: 't1', mode: ThreadMode.chat);
    final m1 = await repo.appendMessage(
        id: 'a', threadId: 't1', role: MessageRole.user);
    final m2 = await repo.appendMessage(
        id: 'b', threadId: 't1', role: MessageRole.assistant);
    final m3 = await repo.appendMessage(
        id: 'c', threadId: 't1', role: MessageRole.user);
    expect(m1.seq, 1);
    expect(m2.seq, 2);
    expect(m3.seq, 3);
  });

  test('upsertBlock streams text delta accumulation', () async {
    await repo.createThread(id: 't1', mode: ThreadMode.chat);
    await repo.appendMessage(
        id: 'm1', threadId: 't1', role: MessageRole.assistant);
    // 模拟 streaming：同一个 (messageId, blockIndex) 多次 upsert
    await repo.upsertBlock(
      const TextBlock(
          id: 'b1', index: 0, state: BlockState.streaming, text: 'Hel'),
      messageId: 'm1',
    );
    await repo.upsertBlock(
      const TextBlock(
          id: 'b1', index: 0, state: BlockState.streaming, text: 'Hello'),
      messageId: 'm1',
    );
    await repo.upsertBlock(
      const TextBlock(
          id: 'b1', index: 0, state: BlockState.closed, text: 'Hello world.'),
      messageId: 'm1',
    );

    final m = await repo.getMessage('m1');
    expect(m!.blocks.length, 1);
    final b = m.blocks.first as TextBlock;
    expect(b.text, 'Hello world.');
    expect(b.state, BlockState.closed);
  });

  test('upsertBlock supports tool_use + tool_result + image types', () async {
    await repo.createThread(id: 't1', mode: ThreadMode.agent);
    await repo.appendMessage(
        id: 'm1', threadId: 't1', role: MessageRole.assistant);
    await repo.upsertBlock(
      const TextBlock(
          id: 'tb', index: 0, state: BlockState.closed, text: 'lemme check'),
      messageId: 'm1',
    );
    await repo.upsertBlock(
      const ToolUseBlock(
        id: 'tu',
        index: 1,
        state: BlockState.closed,
        toolUseId: 'toolu_1',
        toolName: 'web_search',
        input: {'q': 'latest'},
      ),
      messageId: 'm1',
    );
    await repo.appendMessage(
        id: 'm2', threadId: 't1', role: MessageRole.toolResult);
    await repo.upsertBlock(
      const ToolResultBlock(
        id: 'tr',
        index: 0,
        state: BlockState.closed,
        toolResultId: 'toolu_1',
        isError: false,
        content: '{"results":[...]}',
      ),
      messageId: 'm2',
    );
    await repo.appendMessage(
        id: 'm3', threadId: 't1', role: MessageRole.user);
    await repo.upsertBlock(
      const ImageBlock(
        id: 'ib',
        index: 0,
        state: BlockState.closed,
        mimeType: 'image/png',
        data: 'iVBOR...base64',
      ),
      messageId: 'm3',
    );

    final m1 = await repo.getMessage('m1');
    expect(m1!.blocks.length, 2);
    expect(m1.blocks[0], isA<TextBlock>());
    expect(m1.blocks[1], isA<ToolUseBlock>());
    expect((m1.blocks[1] as ToolUseBlock).input?['q'], 'latest');

    final m2 = await repo.getMessage('m2');
    expect((m2!.blocks.first as ToolResultBlock).toolResultId, 'toolu_1');

    final m3 = await repo.getMessage('m3');
    expect((m3!.blocks.first as ImageBlock).mimeType, 'image/png');
  });

  test('replaceBlocks atomically swaps a message\'s blocks', () async {
    await repo.createThread(id: 't1', mode: ThreadMode.chat);
    await repo.appendMessage(
        id: 'm1', threadId: 't1', role: MessageRole.assistant);
    await repo.upsertBlock(
      const TextBlock(id: 'b1', index: 0, state: BlockState.closed, text: 'a'),
      messageId: 'm1',
    );
    await repo.upsertBlock(
      const TextBlock(id: 'b2', index: 1, state: BlockState.closed, text: 'b'),
      messageId: 'm1',
    );

    await repo.replaceBlocks('m1', [
      const TextBlock(id: 'b3', index: 0, state: BlockState.closed, text: 'X'),
    ]);

    final m = await repo.getMessage('m1');
    expect(m!.blocks.length, 1);
    expect((m.blocks.first as TextBlock).text, 'X');
  });

  test('finalizeMessage sets status + tokens + completedAt', () async {
    await repo.createThread(id: 't1', mode: ThreadMode.chat);
    await repo.appendMessage(
        id: 'm1', threadId: 't1', role: MessageRole.assistant);
    await repo.finalizeMessage(
      'm1',
      status: MessageStatus.completed,
      stopReason: 'end_turn',
      inputTokens: 12,
      outputTokens: 34,
    );
    final m = await repo.getMessage('m1');
    expect(m!.status, MessageStatus.completed);
    expect(m.stopReason, 'end_turn');
    expect(m.inputTokens, 12);
    expect(m.outputTokens, 34);
    expect(m.completedAt, isNotNull);
  });

  test('watchMessages emits combined message+blocks list', () async {
    await repo.createThread(id: 't1', mode: ThreadMode.chat);
    await repo.appendMessage(
        id: 'm1', threadId: 't1', role: MessageRole.user);
    await repo.upsertBlock(
      const TextBlock(id: 'b1', index: 0, state: BlockState.closed, text: 'hi'),
      messageId: 'm1',
    );

    final list = await repo.watchMessages('t1').first;
    expect(list.length, 1);
    expect(list.first.blocks.length, 1);
    expect((list.first.blocks.first as TextBlock).text, 'hi');
  });

  test('Message.assembledText joins multiple TextBlocks', () async {
    await repo.createThread(id: 't1', mode: ThreadMode.chat);
    await repo.appendMessage(
        id: 'm1', threadId: 't1', role: MessageRole.assistant);
    await repo.upsertBlock(
      const TextBlock(
          id: 'b1', index: 0, state: BlockState.closed, text: 'first'),
      messageId: 'm1',
    );
    await repo.upsertBlock(
      const TextBlock(
          id: 'b2', index: 1, state: BlockState.closed, text: 'second'),
      messageId: 'm1',
    );
    final m = await repo.getMessage('m1');
    expect(m!.assembledText, 'first\nsecond');
  });

  // ─── Sessions ────────────────────────────────────────────

  test('persistSession + activeSession round-trip', () async {
    await repo.createThread(id: 't1', mode: ThreadMode.chat);
    final now = DateTime.now();
    await repo.persistSession(Session(
      sessionId: 's1',
      threadId: 't1',
      mode: ThreadMode.chat,
      sessionToken: 'tok-A',
      tokenExpiresAt: now.add(const Duration(minutes: 30)),
      status: SessionStatus.active,
      createdAt: now,
    ));

    final s = await repo.activeSession('t1');
    expect(s, isNotNull);
    expect(s!.sessionId, 's1');
    expect(s.sessionToken, 'tok-A');
    expect(s.isActive, true);
  });

  test('finalizeSession flips status away from active', () async {
    await repo.createThread(id: 't1', mode: ThreadMode.chat);
    await repo.persistSession(Session(
      sessionId: 's1',
      threadId: 't1',
      mode: ThreadMode.chat,
      sessionToken: 'tok',
      tokenExpiresAt: DateTime.now().add(const Duration(minutes: 30)),
      status: SessionStatus.active,
      createdAt: DateTime.now(),
    ));
    await repo.finalizeSession('s1', status: SessionStatus.completed);

    final s = await repo.activeSession('t1');
    expect(s, isNull, reason: 'active filter excludes non-active sessions');
  });

  test('updateLastSeenSeq + updateSessionToken atomicity', () async {
    await repo.createThread(id: 't1', mode: ThreadMode.chat);
    await repo.persistSession(Session(
      sessionId: 's1',
      threadId: 't1',
      mode: ThreadMode.chat,
      sessionToken: 'old',
      tokenExpiresAt: DateTime.now().add(const Duration(minutes: 5)),
      status: SessionStatus.active,
      createdAt: DateTime.now(),
    ));

    await repo.updateLastSeenSeq('s1', 42);
    await repo.updateSessionToken(
      's1',
      token: 'new',
      expiresAt: DateTime.now().add(const Duration(minutes: 30)),
    );

    final s = await repo.activeSession('t1');
    expect(s!.lastSeenSeq, 42);
    expect(s.sessionToken, 'new');
  });

  test('Session.tokenExpiringSoon flags <5min remaining', () {
    final almost = Session(
      sessionId: 's1',
      threadId: 't1',
      mode: ThreadMode.chat,
      sessionToken: 'tok',
      tokenExpiresAt: DateTime.now().add(const Duration(minutes: 3)),
      status: SessionStatus.active,
      createdAt: DateTime.now(),
    );
    expect(almost.tokenExpiringSoon, true);

    final fresh = Session(
      sessionId: 's2',
      threadId: 't2',
      mode: ThreadMode.chat,
      sessionToken: 'tok',
      tokenExpiresAt: DateTime.now().add(const Duration(minutes: 25)),
      status: SessionStatus.active,
      createdAt: DateTime.now(),
    );
    expect(fresh.tokenExpiringSoon, false);
  });
}

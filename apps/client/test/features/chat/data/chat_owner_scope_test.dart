// P0 本地数据隔离单测 —— ownerKey scope 跨账号互不可见。
//
// 设计文档 docs/BiuMind-Local-Data-Isolation-Design.md §7（硬性要求）：
//   - DAO 单测：跨 scope 数据互不可见；
//   - 回归场景（2026-08 事故）：scope A 有数据 → 用 scope B 查 threads → 空。
//
// ChatRepo 构造绑定 scope（编译期必填，不存在「不过滤」的调用路径），
// 本测试在同一内存库上开两个 scope 的 repo 实例验证隔离不变量。

import 'dart:convert';

import 'package:biumind/data/local/db.dart';
import 'package:biumind/features/chat/data/chat_repo.dart';
import 'package:biumind/features/chat/data/chat_scope.dart';
import 'package:biumind/features/chat/domain/chat_models.dart';
import 'package:biumind/services/auth_service.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  late AppDb db;
  late ChatRepo repoA;
  late ChatRepo repoB;

  setUp(() {
    db = AppDb.memory();
    repoA = ChatRepo(db, scope: 'env-a:user-a');
    repoB = ChatRepo(db, scope: 'env-a:user-b');
  });
  tearDown(() async {
    await db.close();
  });

  /// scope A 灌一套完整数据：thread + message + text block + star reaction
  /// + session（覆盖五张表）。
  Future<void> seedA() async {
    await repoA.createThread(id: 't1', mode: ThreadMode.chat, title: 'A 的会话');
    await repoA.appendMessage(
      id: 'm1',
      threadId: 't1',
      role: MessageRole.user,
      status: MessageStatus.completed,
    );
    await repoA.upsertBlock(
      TextBlock(
        id: 'm1_b0',
        index: 0,
        state: BlockState.closed,
        text: 'hello from A',
      ),
      messageId: 'm1',
    );
    await repoA.toggleReaction(messageId: 'm1', threadId: 't1', kind: 'star');
    await repoA.persistSession(Session(
      sessionId: 's1',
      threadId: 't1',
      mode: ThreadMode.chat,
      sessionToken: 'tok',
      tokenExpiresAt: DateTime(2027),
      status: SessionStatus.active,
      createdAt: DateTime(2026, 8, 1),
    ));
  }

  test('回归：scope A 有数据 → scope B 查 threads 为空（本次事故场景）', () async {
    await seedA();

    // A 自己能看到（防测试空转）。
    expect(await repoA.watchThreads().first, hasLength(1));

    // B 的全部读取路径都不可见。
    expect(await repoB.watchThreads().first, isEmpty);
    expect(await repoB.watchThread('t1').first, isNull);
    expect(await repoB.getThread('t1'), isNull);
    expect(await repoB.watchArchivedThreads().first, isEmpty);
    expect(await repoB.listAllThreads(), isEmpty);
  });

  test('跨 scope：messages / blocks / sessions / reactions 互不可见', () async {
    await seedA();

    expect(await repoB.watchMessages('t1').first, isEmpty);
    expect(await repoB.listMessagesOnce('t1'), isEmpty);
    expect(await repoB.getMessage('m1'), isNull);
    expect(await repoB.activeSession('t1'), isNull);
    expect(await repoB.watchReactionsForMessage('m1').first, isEmpty);
    expect(await repoB.watchStarredMessages().first, isEmpty);
    expect(await repoB.watchStarredMessageHits().first, isEmpty);

    // A 侧 sanity：message + block + session + reaction 都在。
    final msgsA = await repoA.watchMessages('t1').first;
    expect(msgsA, hasLength(1));
    expect(msgsA.first.blocks, hasLength(1));
    expect(await repoA.activeSession('t1'), isNotNull);
    expect(await repoA.watchStarredMessages().first, hasLength(1));
  });

  test('跨 scope：搜索 / 统计 / 同步对照查询互不可见', () async {
    await seedA();

    expect(await repoB.searchMessages(query: 'hello'), isEmpty);
    expect((await repoB.threadStats()).threadCount, 0);
    expect((await repoB.threadStats()).messageCount, 0);
    expect((await repoB.recentStats()).messages, 0);
    expect(await repoB.dailyStreak(), 0);
    expect(await repoB.recentModels(), isEmpty);
    expect(await repoB.messageCountsByThread(), isEmpty);
    expect(await repoB.remoteUpdatedMarkers(), isEmpty);

    // A 侧 sanity。
    expect(await repoA.searchMessages(query: 'hello'), hasLength(1));
    expect((await repoA.threadStats()).threadCount, 1);
  });

  test('跨 scope：B 的写/删不伤 A 的行', () async {
    await seedA();

    // B 对 A 的 id 做更新/删除 —— 全部因 ownerKey 条件落空而成为 noop。
    await repoB.renameThread('t1', '被 B 改名');
    await repoB.setPinned('t1', true);
    await repoB.archiveThread('t1');
    await repoB.finalizeMessage('m1', status: MessageStatus.failed);
    await repoB.deleteMessages(['m1']);
    await repoB.deleteThreads(['t1']);
    // (messageId, kind) toggle：B 的 toggle 不应删掉 A 的 star。
    await repoB.toggleReaction(messageId: 'm1', threadId: 't1', kind: 'star');
    await repoB.clearReactionsForMessage('m1');

    final t = await repoA.getThread('t1');
    expect(t, isNotNull);
    expect(t!.title, 'A 的会话');
    expect(t.pinned, isFalse);
    expect(t.archived, isFalse);
    final m = await repoA.getMessage('m1');
    expect(m, isNotNull);
    expect(m!.status, MessageStatus.completed);
    expect(await repoA.watchReactionsForMessage('m1').first, hasLength(1));
  });

  // ── scope 派生（chat_scope.dart）────────────────────────────

  String fakeJwt(String sub) {
    String b64(Map<String, dynamic> m) =>
        base64Url.encode(utf8.encode(jsonEncode(m))).replaceAll('=', '');
    return '${b64({'alg': 'none'})}.${b64({'sub': sub})}.sig';
  }

  test('ownerKey 派生：同环境地址写法差异 → 同 scope；不同环境/账号 → 不同', () {
    HubCredentials creds(String url, String sub) =>
        HubCredentials(endpoint: Uri.parse(url), bearerToken: fakeJwt(sub));

    final base = chatOwnerKeyFromCredentials(creds('https://hub.example.com', 'u1'));
    // 尾部 `/`、默认端口、大小写差异不产生新 scope。
    expect(
      chatOwnerKeyFromCredentials(creds('https://hub.example.com/', 'u1')),
      base,
    );
    expect(
      chatOwnerKeyFromCredentials(creds('https://hub.example.com:443', 'u1')),
      base,
    );
    expect(
      chatOwnerKeyFromCredentials(creds('https://HUB.example.COM', 'u1')),
      base,
    );
    // 换账号 / 换环境 / 非默认端口 → 不同 scope。
    expect(
      chatOwnerKeyFromCredentials(creds('https://hub.example.com', 'u2')),
      isNot(base),
    );
    expect(
      chatOwnerKeyFromCredentials(creds('https://staging.example.com', 'u1')),
      isNot(base),
    );
    expect(
      chatOwnerKeyFromCredentials(creds('http://localhost:7001', 'u1')),
      isNot(base),
    );
    // token 非 JWT / 无 sub → null（调用方退化空流/不写库）。
    expect(
      chatOwnerKeyFromCredentials(
        HubCredentials(endpoint: Uri.parse('https://hub.example.com'), bearerToken: 'opaque-token'),
      ),
      isNull,
    );
  });
}

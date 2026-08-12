// NoteOutboxFlusher 单测 —— fake brain (in-process HttpServer) + AppDb.memory() +
// 注入时钟。覆盖自伤 409 修复（compact 合并 + cascade 级联抬 baseVersion）+
// 真 409 冲突流仍弹。
//
//   1. compact 合并同笔记堆积的 update_note（payload 并集、baseVersion 取最老、删余）
//   2. compact 不跨笔记、不跨 op（update_note + trash_note 共存；不同 entity 各留）
//   3. bumpOutboxBaseVersion 直接抬剩余同笔记行的 baseVersion
//   4. flusher 集成：堆积 update_note 单次 PUT 成功，无自伤 409
//   5. 真他端冲突（baseVersion 落后服务端）→ 409 → conflicts 流 + 行删
//   6. during-flush race：PUT 往返期间新插的 update_note 被 cascade 抬 baseVersion

import 'dart:convert';
import 'dart:io';

import 'package:drift/drift.dart' show Value;
import 'package:flutter_test/flutter_test.dart';

import 'package:biumind/data/local/db.dart';
import 'package:biumind/data/local/notes_dao.dart';
import 'package:biumind/data/note_merge.dart';
import 'package:biumind/data/api/notes_client.dart' as api;
import 'package:biumind/data/outbox/note_outbox_flusher.dart';

/// 假 brain —— 只实现 PUT /v1/notes/{id} 的 If-Match 乐观锁语义。
/// `onBeforeRespond` 在版本判定前触发，供 race 测试注入「PUT 往返期间的新
/// autosave 入队」。
class _FakeBrain {
  final Map<String, int> versionByNote = {};
  /// 409 时 current.content_md 的覆盖值（模拟「他端已改服务端正文」）。
  /// 未设置则回显请求 body 的 content_md（旧行为）。
  final Map<String, String> remoteContentByConflict = {};
  Future<void> Function(String noteId)? onBeforeRespond;
  final List<String> requests = [];
  HttpServer? _server;

  String get baseUrl => 'http://127.0.0.1:${_server!.port}';

  Future<void> start() async {
    _server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    _server!.listen(_handle);
  }

  Future<void> stop() async => _server?.close(force: true);

  Future<void> _handle(HttpRequest req) async {
    final body = await utf8.decoder.bind(req).join();
    final ifMatch = int.tryParse(
            req.headers.value('If-Match') ?? '') ??
        0;
    final noteId = req.uri.pathSegments.last;
    if (req.method == 'PUT' && req.uri.path.startsWith('/v1/notes/')) {
      requests.add('PUT ${req.uri.path} If-Match=$ifMatch $body');
      await onBeforeRespond?.call(noteId);
      final cur = versionByNote[noteId] ?? 1;
      final j = jsonDecode(body) as Map<String, dynamic>;
      if (ifMatch != cur) {
        // 409 —— 对齐服务端 api.go 的 version_conflict 响应形状。
        req.response.statusCode = 409;
        req.response.headers.contentType = ContentType.json;
        req.response.write(jsonEncode({
          'error': {'code': 'version_conflict', 'message': 'If-Match mismatch'},
          'current_version': cur,
          'current': {
            'id': noteId,
            'title': j['title'] ?? '',
            'content_md':
                remoteContentByConflict[noteId] ?? j['content_md'] ?? '',
            'version': cur,
            'updated_at': DateTime.utc(2100).toIso8601String(),
          },
        }));
        await req.response.close();
        return;
      }
      final newVersion = cur + 1;
      versionByNote[noteId] = newVersion;
      req.response.statusCode = 200;
      req.response.headers.contentType = ContentType.json;
      req.response.write(jsonEncode({
        'id': noteId,
        'notebook_id': j['notebook_id'],
        'title': j['title'] ?? '',
        'content_md': j['content_md'] ?? '',
        'is_todo': j['is_todo'] ?? false,
        'position': j['position'] ?? 0,
        'version': newVersion,
        'updated_at': DateTime.utc(2100).toIso8601String(),
      }));
      await req.response.close();
      return;
    }
    req.response.statusCode = 404;
    await req.response.close();
  }
}

void main() {
  late AppDb db;
  late NotesDao dao;
  late api.NotesClient client;
  late _FakeBrain brain;

  setUp(() async {
    db = AppDb.memory();
    dao = NotesDao(db, scope: 'test');
    brain = _FakeBrain();
    await brain.start();
    client = api.NotesClient(Uri.parse(brain.baseUrl), 'tok');
  });

  tearDown(() async {
    await brain.stop();
    await db.close();
  });

  /// 构造并落一条本地笔记行（flusher._upsertFromDto 需它存在才回写 version）。
  Future<void> seedNote(String id, {int version = 1}) async {
    await dao.upsertNote(LocalNote(
      id: id,
      title: 't',
      contentMd: '',
      isTodo: false,
      position: 0,
      version: version,
      trashed: false,
      updatedAt: DateTime.utc(2100),
      ownerKey: 'test',
    ));
    brain.versionByNote[id] = version;
  }

  /// 落一条带 base 的笔记（content=本地草稿，baseContent=上次服务端确认态，
  /// baseVersion=对应版本）。用于 3-way merge 测试。brain 端 versionByNote
  /// 设为 [serverVersion]（若 > baseVersion 表示他端已推高 → 触发 409）。
  Future<void> seedNoteWithBase(
    String id, {
    required String content,
    required String baseContent,
    required int baseVersion,
    int? serverVersion,
  }) async {
    await dao.upsertNote(LocalNote(
      id: id,
      title: 't',
      contentMd: content,
      isTodo: false,
      position: 0,
      version: baseVersion,
      trashed: false,
      updatedAt: DateTime.utc(2100),
      ownerKey: 'test',
      baseContentMd: baseContent,
      baseVersion: baseVersion,
    ));
    brain.versionByNote[id] = serverVersion ?? baseVersion;
  }

  Future<int> enqueueUpdate(
    String entityId, {
    required int baseVersion,
    Map<String, dynamic> payload = const {},
  }) =>
      dao.enqueueOutbox(NoteOutboxCompanion.insert(
        op: 'update_note',
        entityId: entityId,
        payloadJson: jsonEncode(payload),
        baseVersion: Value(baseVersion),
        createdAt: DateTime.utc(2000),
        nextAttemptAt: DateTime.utc(2000), // 必过期，否则 dueOutbox 跳过
      ));

  NoteOutboxFlusher makeFlusher() => NoteOutboxFlusher(
        dao: dao,
        client: client,
        interval: const Duration(seconds: 1),
      );

  /// 模拟 providers 注入的 onAutoMergeResolved = repository.updateNote：
  /// 把合并文写回本地行（保留 base）+ 入队新 update_note（baseVersion=当前
  /// 本地 version，flusher 已把本地行写回 remote 快照故 = remoteVersion）。
  Future<void> applyMerged(String id, String md) async {
    final ex = await dao.noteById(id);
    if (ex == null) return;
    await dao.upsertNote(ex.copyWith(contentMd: md));
    await dao.enqueueOutbox(NoteOutboxCompanion.insert(
      op: 'update_note',
      entityId: id,
      payloadJson: jsonEncode({'content_md': md}),
      baseVersion: Value(ex.version),
      createdAt: DateTime.utc(2000),
      nextAttemptAt: DateTime.utc(2000),
    ));
  }

  group('compact', () {
    test('同笔记多条 update_note 合并成一条，payload 字段并集', () async {
      await enqueueUpdate('n1', baseVersion: 10, payload: {'title': 'T'});
      await enqueueUpdate('n1', baseVersion: 10, payload: {'content_md': 'C'});
      await dao.compactUpdateNotes();

      final pending = await dao.allOutbox();
      expect(pending.length, 1, reason: '两条折叠成一条');
      final p = jsonDecode(pending.single.payloadJson) as Map<String, dynamic>;
      expect(p, {'title': 'T', 'content_md': 'C'}, reason: '字段并集');
      expect(pending.single.baseVersion, 10, reason: 'baseVersion 取最老行');
    });

    test('新值盖旧值（同字段后者胜）', () async {
      await enqueueUpdate('n1', baseVersion: 1, payload: {'content_md': 'A'});
      await enqueueUpdate('n1', baseVersion: 1, payload: {'content_md': 'AB'});
      await dao.compactUpdateNotes();
      final p = jsonDecode((await dao.allOutbox()).single.payloadJson);
      expect(p['content_md'], 'AB', reason: 'id 大的盖 id 小的');
    });

    test('不跨笔记、不跨 op 合并', () async {
      await enqueueUpdate('n1', baseVersion: 1, payload: {'title': 'a'});
      await enqueueUpdate('n2', baseVersion: 1, payload: {'title': 'b'});
      await dao.enqueueOutbox(NoteOutboxCompanion.insert(
        op: 'trash_note',
        entityId: 'n1',
        payloadJson: '{}',
        createdAt: DateTime.utc(2100),
        nextAttemptAt: DateTime.utc(2100),
      ));
      await dao.compactUpdateNotes();
      expect((await dao.allOutbox()).length, 3, reason: '2 不同 entity + trash 各留');
    });
  });

  test('bumpOutboxBaseVersion 抬同笔记剩余 update_note 行', () async {
    await enqueueUpdate('n1', baseVersion: 10, payload: {'title': 'a'});
    await dao.bumpOutboxBaseVersion('n1', 11);
    final row = (await dao.allOutbox()).single;
    expect(row.baseVersion, 11);
  });

  group('flusher 集成', () {
    test('堆积 update_note 单次 PUT，无自伤 409', () async {
      await seedNote('n1', version: 10);
      await enqueueUpdate('n1', baseVersion: 10, payload: {'content_md': 'A'});
      await enqueueUpdate('n1', baseVersion: 10, payload: {'content_md': 'AB'});

      await makeFlusher().flushOnce();

      // compact 折叠后只发一次 PUT（baseVersion=10 命中服务端 version=10）。
      expect(brain.requests, hasLength(1));
      expect(brain.requests.single, contains('If-Match=10'));
      expect(brain.requests.single, contains('"content_md":"AB"'),
          reason: '合并后最新快照上送');
      expect(await dao.allOutbox(), isEmpty, reason: '成功行已删');
      expect((await dao.noteById('n1'))!.version, 11,
          reason: '_upsertFromDto 回填服务端新 version');
    });

    test('真他端冲突（baseVersion 落后服务端）→ conflicts 流 + 行删', () async {
      await seedNote('n1', version: 11); // 服务端已被他端推到 11
      await enqueueUpdate('n1', baseVersion: 10, payload: {'content_md': 'X'});

      final flusher = makeFlusher();
      final conflicts = <NoteOutboxConflict>[];
      final sub = flusher.conflicts.listen(conflicts.add);
      await flusher.flushOnce();

      // broadcast StreamController 异步投递，pump 一下让 conflict 事件落地。
      await Future<void>.delayed(Duration.zero);
      await sub.cancel();

      expect(conflicts, hasLength(1));
      expect(conflicts.single.entityId, 'n1');
      expect(conflicts.single.baseVersion, 10);
      expect(await dao.allOutbox(), isEmpty, reason: '冲突 op 已丢');
    });

    test('during-flush race：PUT 往返期间新插的行被 cascade 抬 baseVersion',
        () async {
      await seedNote('n1', version: 10);
      await enqueueUpdate('n1', baseVersion: 10, payload: {'content_md': 'A'});

      // 模拟 PUT 往返期间的 autosave：brain 在版本判定前插一条 baseVersion=10
      // 的新 update_note（本地 note.version 此刻还没回填，所以仍是旧值 10）。
      brain.onBeforeRespond = (noteId) async {
        await enqueueUpdate(noteId, baseVersion: 10, payload: {'content_md': 'AB'});
      };

      final flusher = makeFlusher();
      await flusher.flushOnce();

      // 第一条 PUT 成功（10→11），cascade 把 race 插入的行抬到 11。
      final pending = await dao.allOutbox();
      expect(pending, hasLength(1), reason: 'race 行存活（未被第一条的 delete 带走）');
      expect(pending.single.baseVersion, 11,
          reason: 'cascade 抬到服务端新 version，下轮 PUT 不再 409');

      // 第二轮 flush：race 行 baseVersion=11 命中服务端 11，成功。
      brain.onBeforeRespond = null;
      await flusher.flushOnce();
      expect(await dao.allOutbox(), isEmpty, reason: 'race 行已成功 flush');
      expect(brain.requests, hasLength(2));
      expect(brain.requests.last, contains('If-Match=11'));
    });
  });

  group('3-way merge (409)', () {
    test('非重叠段 → 静默自动合并，无 conflict，新 op baseVersion=remote', () async {
      // base v10 = P1/P2/P3。本地改 P2→P2L（草稿）。他端改 P3→P3R 推到 v11。
      await seedNoteWithBase('n1',
          content: 'P1\n\nP2L\n\nP3',
          baseContent: 'P1\n\nP2\n\nP3',
          baseVersion: 10,
          serverVersion: 11);
      brain.remoteContentByConflict['n1'] = 'P1\n\nP2\n\nP3R';
      await enqueueUpdate('n1',
          baseVersion: 10, payload: {'content_md': 'P1\n\nP2L\n\nP3'});

      final flusher = makeFlusher();
      final conflicts = <NoteOutboxConflict>[];
      final sub = flusher.conflicts.listen(conflicts.add);
      String? autoMerged;
      flusher.onAutoMergeResolved = (id, md) async {
        autoMerged = md;
        await applyMerged(id, md);
      };

      await flusher.flushOnce();
      await Future<void>.delayed(Duration.zero);
      await sub.cancel();

      expect(conflicts, isEmpty, reason: '非重叠段静默自动合并，不弹冲突');
      expect(autoMerged, 'P1\n\nP2L\n\nP3R');
      // 本地行：merge 后 applyMerged 覆盖为合并文。
      expect((await dao.noteById('n1'))!.contentMd, 'P1\n\nP2L\n\nP3R');
      // 新 op 入队 baseVersion=11（remote），原 op 已删。
      final pending = await dao.allOutbox();
      expect(pending, hasLength(1));
      expect(pending.single.op, 'update_note');
      expect(pending.single.baseVersion, 11);
    });

    test('同段冲突 → emit merge bundle（带 segments），op 删，本地行=remote',
        () async {
      await seedNoteWithBase('n1',
          content: 'A\n\nBX\n\nC',
          baseContent: 'A\n\nB\n\nC',
          baseVersion: 10,
          serverVersion: 11);
      brain.remoteContentByConflict['n1'] = 'A\n\nBY\n\nC';
      await enqueueUpdate('n1',
          baseVersion: 10, payload: {'content_md': 'A\n\nBX\n\nC'});

      final flusher = makeFlusher();
      final conflicts = <NoteOutboxConflict>[];
      final sub = flusher.conflicts.listen(conflicts.add);
      flusher.onAutoMergeResolved = applyMerged; // 同段冲突不应触发自动合并

      await flusher.flushOnce();
      await Future<void>.delayed(Duration.zero);
      await sub.cancel();

      expect(conflicts, hasLength(1));
      final c = conflicts.single;
      expect(c.hasMergeBundle, isTrue);
      expect(c.baseContentMd, 'A\n\nB\n\nC');
      expect(c.localContentMd, 'A\n\nBX\n\nC');
      expect(c.remoteContentMd, 'A\n\nBY\n\nC');
      expect(c.remoteVersion, 11);
      expect(c.segments!.whereType<ConflictMergeSegment>().length, 1);
      expect(await dao.allOutbox(), isEmpty, reason: '冲突 op 已丢');
      expect((await dao.noteById('n1'))!.contentMd, 'A\n\nBY\n\nC',
          reason: '本地行写成服务端快照');
    });

    test('base==null（离线建未同步）→ legacy conflict，无 bundle', () async {
      await seedNote('n1', version: 10); // 无 base 列
      brain.versionByNote['n1'] = 11;
      brain.remoteContentByConflict['n1'] = 'REMOTE';
      await enqueueUpdate('n1', baseVersion: 10, payload: {'content_md': 'LOCAL'});

      final flusher = makeFlusher();
      final conflicts = <NoteOutboxConflict>[];
      final sub = flusher.conflicts.listen(conflicts.add);
      await flusher.flushOnce();
      await Future<void>.delayed(Duration.zero);
      await sub.cancel();

      expect(conflicts, hasLength(1));
      expect(conflicts.single.hasMergeBundle, isFalse, reason: '无 base → legacy');
      expect(await dao.allOutbox(), isEmpty, reason: 'op 已丢');
    });
  });
}

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
import 'package:biumind/data/api/notes_client.dart' as api;
import 'package:biumind/data/outbox/note_outbox_flusher.dart';

/// 假 brain —— 只实现 PUT /v1/notes/{id} 的 If-Match 乐观锁语义。
/// `onBeforeRespond` 在版本判定前触发，供 race 测试注入「PUT 往返期间的新
/// autosave 入队」。
class _FakeBrain {
  final Map<String, int> versionByNote = {};
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
            'content_md': j['content_md'] ?? '',
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
}

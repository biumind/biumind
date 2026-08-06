// Drift DAO + NotesRepository + NoteOutboxFlusher + NotesSyncPoller
// behavioural tests.
//
// 镜像 wiki_local_test.dart 的形态：全部跑在 in-memory NativeDatabase 上，
// fake API server 用真实 HttpServer（真 header、真路径、真 409 冲突），
// 保证 HTTP 面不撒谎。

import 'dart:convert';
import 'dart:io';

import 'package:biumind/data/api/notes_client.dart' as api;
import 'package:biumind/data/local/db.dart';
import 'package:biumind/data/local/notes_dao.dart';
import 'package:biumind/data/notes_repository.dart';
import 'package:biumind/data/notes_sync.dart';
import 'package:biumind/data/outbox/note_outbox_flusher.dart';
import 'package:drift/drift.dart' show Value;
import 'package:flutter_test/flutter_test.dart';

class _FakeNotes {
  _FakeNotes._(this.server);
  final HttpServer server;
  int _nextId = 0;
  int _nextEventId = 0;
  final Map<String, Map<String, dynamic>> notebooks = {};
  final Map<String, Map<String, dynamic>> notes = {};
  final Map<String, Map<String, dynamic>> tags = {};
  final List<Map<String, dynamic>> events = [];
  bool simulate409 = false;
  bool simulate500 = false;
  String? lastIfMatch;
  List<String>? lastSetTagIds;

  static Future<_FakeNotes> start() async {
    final s = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    final fake = _FakeNotes._(s);
    s.listen(fake._handle);
    return fake;
  }

  Uri get url => Uri.parse('http://${server.address.host}:${server.port}');
  Future<void> stop() => server.close(force: true);

  void addEvent(String type, Map<String, dynamic> payload) {
    events.add({
      'id': ++_nextEventId,
      'event_type': type,
      'actor_id': 'tester',
      'payload': payload,
      'created_at': DateTime.now().toUtc().toIso8601String(),
    });
  }

  Future<void> _handle(HttpRequest req) async {
    final body = await utf8.decoder.bind(req).join();
    final path = req.uri.path;
    final parts = path.split('/').where((s) => s.isNotEmpty).toList();
    Map<String, dynamic>? response;
    var status = 200;

    if (simulate500) {
      status = 500;
      response = {'error': 'fake_500'};
    } else if (req.method == 'GET' && path == '/v1/notebooks') {
      response = {'notebooks': notebooks.values.toList()};
    } else if (req.method == 'POST' && path == '/v1/notebooks') {
      final j = jsonDecode(body) as Map<String, dynamic>;
      final nb = {
        'id': 'srv-nb${++_nextId}',
        'name': j['name'],
        'position': j['position'] ?? 0.0,
        'created_at': DateTime.now().toUtc().toIso8601String(),
        'updated_at': DateTime.now().toUtc().toIso8601String(),
      };
      notebooks[nb['id'] as String] = nb;
      response = nb;
    } else if (req.method == 'DELETE' && parts.length == 3 && parts[1] == 'notebooks') {
      notebooks.remove(parts[2]);
      response = {'deleted': parts[2]};
    } else if (req.method == 'GET' && path == '/v1/notes') {
      response = {
        'notes': notes.values.where((n) => n['deleted_at'] == null).toList()
      };
    } else if (req.method == 'POST' && path == '/v1/notes') {
      final j = jsonDecode(body) as Map<String, dynamic>;
      // 与 brain 一致：客户端带 id（uuid）时直接使用（幂等重放返回既有
      // 记录），否则服务端分配。
      final id = j['id'] as String? ?? 'srv-n${++_nextId}';
      final existing = notes[id];
      if (existing != null) {
        response = existing;
      } else {
        final note = {
          'id': id,
          'notebook_id': j['notebook_id'],
          'title': j['title'],
          'content_md': j['content_md'] ?? '',
          'is_todo': j['is_todo'] ?? false,
          'position': j['position'] ?? 0.0,
          'version': 1,
          'created_at': DateTime.now().toUtc().toIso8601String(),
          'updated_at': DateTime.now().toUtc().toIso8601String(),
        };
        notes[id] = note;
        response = note;
      }
    } else if (req.method == 'GET' && path == '/v1/notes/trash') {
      response = {
        'notes': notes.values.where((n) => n['deleted_at'] != null).toList()
      };
    } else if (req.method == 'GET' && path == '/v1/notes/changes') {
      final since = int.tryParse(req.uri.queryParameters['since'] ?? '') ?? 0;
      final evs = events.where((e) => (e['id'] as int) > since).toList();
      final latest =
          events.isEmpty ? 0 : (events.last['id'] as int);
      response = {'events': evs, 'latest': latest};
    } else if (req.method == 'PUT' &&
        parts.length == 3 &&
        parts[1] == 'notes' &&
        parts[2] != 'trash' &&
        parts[2] != 'changes') {
      final note = notes[parts[2]];
      lastIfMatch = req.headers.value('If-Match');
      if (note == null) {
        status = 404;
        response = {'error': 'not_found'};
      } else if (simulate409) {
        status = 409;
        response = {
          'error': {'code': 'version_conflict'},
          'current_version': note['version'],
        };
      } else {
        final j = jsonDecode(body) as Map<String, dynamic>;
        if (j.containsKey('title')) note['title'] = j['title'];
        if (j.containsKey('content_md')) note['content_md'] = j['content_md'];
        if (j.containsKey('notebook_id')) {
          note['notebook_id'] =
              (j['notebook_id'] as String).isEmpty ? null : j['notebook_id'];
        }
        if (j.containsKey('is_todo')) note['is_todo'] = j['is_todo'];
        if (j.containsKey('position')) note['position'] = j['position'];
        note['version'] = (note['version'] as int) + 1;
        note['updated_at'] = DateTime.now().toUtc().toIso8601String();
        response = note;
      }
    } else if (req.method == 'DELETE' && parts.length == 3 && parts[1] == 'notes') {
      final note = notes[parts[2]];
      if (note == null) {
        status = 404;
        response = {'error': 'not_found'};
      } else {
        note['deleted_at'] = DateTime.now().toUtc().toIso8601String();
        response = {'deleted': parts[2]};
      }
    } else if (req.method == 'POST' &&
        parts.length == 4 &&
        parts[1] == 'notes' &&
        parts[3] == 'restore') {
      final note = notes[parts[2]];
      if (note == null) {
        status = 404;
        response = {'error': 'not_found'};
      } else {
        note['deleted_at'] = null;
        note['version'] = (note['version'] as int) + 1;
        response = note;
      }
    } else if (req.method == 'DELETE' &&
        parts.length == 4 &&
        parts[1] == 'notes' &&
        parts[3] == 'purge') {
      notes.remove(parts[2]);
      response = {'purged': parts[2]};
    } else if (req.method == 'GET' && path == '/v1/note-tags') {
      response = {'tags': tags.values.toList()};
    } else if (req.method == 'POST' && path == '/v1/note-tags') {
      final j = jsonDecode(body) as Map<String, dynamic>;
      final tag = {'id': 'srv-t${++_nextId}', 'name': j['name']};
      tags[tag['id'] as String] = tag;
      response = tag;
    } else if (req.method == 'PUT' &&
        parts.length == 4 &&
        parts[1] == 'notes' &&
        parts[3] == 'tags') {
      lastSetTagIds = ((jsonDecode(body) as Map)['tag_ids'] as List)
          .map((t) => t.toString())
          .toList();
      response = {'note_id': parts[2], 'tag_ids': lastSetTagIds};
    } else {
      status = 404;
      response = {
        'error': 'unknown_route',
        'path': path,
        'method': req.method
      };
    }

    req.response.statusCode = status;
    req.response.headers.contentType = ContentType.json;
    req.response.write(jsonEncode(response));
    await req.response.close();
  }
}

void main() {
  late AppDb db;
  late NotesDao dao;

  setUp(() {
    db = AppDb.memory();
    dao = NotesDao(db);
  });

  tearDown(() => db.close());

  group('NotesDao', () {
    test('upserts and watches notebooks', () async {
      await dao.upsertNotebook(LocalNoteNotebook(
        id: 'nb1',
        name: '收件箱',
        position: 0.0,
        updatedAt: DateTime.now().toUtc(),
      ));
      final list = await dao.listNotebooks();
      expect(list.length, 1);
      expect(list.first.name, '收件箱');
    });

    test('renameNoteId migrates tag links', () async {
      final now = DateTime.now().toUtc();
      await dao.upsertNote(LocalNote(
        id: 'local-n-1',
        notebookId: null,
        title: 't',
        contentMd: '',
        isTodo: false,
        todoCompletedAt: null,
        position: 0.0,
        version: 1,
        trashed: false,
        trashedAt: null,
        updatedAt: now,
      ));
      await dao.upsertTag(const LocalNoteTag(id: 'tag1', name: 'x'));
      await dao.setNoteTags('local-n-1', ['tag1']);
      await dao.renameNoteId('local-n-1', 'srv-n1');
      expect(await dao.listTagIdsForNote('srv-n1'), ['tag1']);
      expect(await dao.listTagIdsForNote('local-n-1'), isEmpty);
    });

    test('renameNotebookId migrates notes', () async {
      final now = DateTime.now().toUtc();
      await dao.upsertNotebook(LocalNoteNotebook(
          id: 'local-nb-1', name: 'X', position: 0.0, updatedAt: now));
      await dao.upsertNote(LocalNote(
        id: 'n1',
        notebookId: 'local-nb-1',
        title: 't',
        contentMd: '',
        isTodo: false,
        todoCompletedAt: null,
        position: 0.0,
        version: 1,
        trashed: false,
        trashedAt: null,
        updatedAt: now,
      ));
      await dao.renameNotebookId('local-nb-1', 'srv-nb1');
      final note = await dao.noteById('n1');
      expect(note!.notebookId, 'srv-nb1');
    });

    test('outbox enqueue + due + delete', () async {
      final now = DateTime.now().toUtc();
      final id = await dao.enqueueOutbox(NoteOutboxCompanion.insert(
        op: 'create_note',
        entityId: 'local-n-1',
        payloadJson: '{}',
        createdAt: now,
        nextAttemptAt: now,
      ));
      final due =
          await dao.dueOutbox(now: now.add(const Duration(seconds: 1)));
      expect(due.length, 1);
      await dao.deleteOutbox(id);
      expect((await dao.allOutbox()).length, 0);
    });

    test('bumpOutboxFailure increments attempts and reschedules', () async {
      final now = DateTime.now().toUtc();
      final id = await dao.enqueueOutbox(NoteOutboxCompanion.insert(
        op: 'create_note',
        entityId: 'local-n-1',
        payloadJson: '{}',
        createdAt: now,
        nextAttemptAt: now,
      ));
      await dao.bumpOutboxFailure(
          id, 'boom', now.add(const Duration(seconds: 60)));
      final entry = (await dao.allOutbox()).first;
      expect(entry.attempts, 1);
      expect(entry.lastError, 'boom');
    });

    test('rekeyOutbox 重写 payloadJson 里的引用（标量 + 列表），不动正文',
        () async {
      final now = DateTime.now().toUtc();
      // create_note：notebook_id 标量引用 local- 占位笔记本；content_md
      // 里恰好出现该 id 的文本（JSON 内已转义）不应被误改。
      await dao.enqueueOutbox(NoteOutboxCompanion.insert(
        op: 'create_note',
        entityId: 'n1',
        notebookId: const Value('local-nb-1'),
        payloadJson: jsonEncode({
          'title': 't',
          'content_md': '引用 "local-nb-1" 这个字符串',
          'notebook_id': 'local-nb-1',
        }),
        createdAt: now,
        nextAttemptAt: now,
      ));
      // set_note_tags：tag_ids 列表引用 local- 占位标签。
      await dao.enqueueOutbox(NoteOutboxCompanion.insert(
        op: 'set_note_tags',
        entityId: 'n1',
        payloadJson: jsonEncode({
          'tag_ids': ['local-tag-1', 'srv-t9'],
        }),
        createdAt: now,
        nextAttemptAt: now,
      ));

      await dao.rekeyOutbox(
          oldEntityId: 'local-nb-1', newEntityId: 'srv-nb1');
      await dao.rekeyOutbox(
          oldEntityId: 'local-tag-1', newEntityId: 'srv-t1');

      final rows = await dao.allOutbox();
      final create =
          jsonDecode(rows[0].payloadJson) as Map<String, dynamic>;
      expect(create['notebook_id'], 'srv-nb1');
      expect(create['content_md'], '引用 "local-nb-1" 这个字符串',
          reason: '正文里转义过的同名文本不能被改写');
      expect(rows[0].notebookId, 'srv-nb1');
      final setTags =
          jsonDecode(rows[1].payloadJson) as Map<String, dynamic>;
      expect(setTags['tag_ids'], ['srv-t1', 'srv-t9']);
    });
  });

  group('NotesRepository', () {
    test('createNote is optimistic and queues an outbox entry', () async {
      final fake = await _FakeNotes.start();
      addTearDown(fake.stop);
      final repo = NotesRepository(
        dao: dao,
        client: api.NotesClient(fake.url, 'tok'),
      );

      final n = await repo.createNote(title: '速记', contentMd: '# hi');
      expect(n.pendingCreate, isTrue);
      // 真 uuid（不带 'local-' 前缀），flush 前后 id 不变。
      expect(n.id, isNot(startsWith('local-')));
      expect(
        RegExp(r'^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-'
                r'[0-9a-f]{12}$')
            .hasMatch(n.id),
        isTrue,
        reason: 'createNote 应生成服务端可用的 uuid',
      );

      final local = await dao.noteById(n.id);
      expect(local!.title, '速记');

      final outbox = await dao.allOutbox();
      expect(outbox.length, 1);
      expect(outbox.first.op, 'create_note');
      final payload =
          jsonDecode(outbox.first.payloadJson) as Map<String, dynamic>;
      expect(payload['id'], n.id,
          reason: 'create_note payload 应带客户端 uuid 供服务端幂等使用');
    });

    test('updateNote uses cached version as If-Match base', () async {
      final fake = await _FakeNotes.start();
      addTearDown(fake.stop);
      final repo = NotesRepository(
        dao: dao,
        client: api.NotesClient(fake.url, 'tok'),
      );

      // Seed a note as if the server had returned it on refresh.
      await dao.upsertNote(LocalNote(
        id: 'srv-n1',
        notebookId: null,
        title: 'old',
        contentMd: 'v3',
        isTodo: false,
        todoCompletedAt: null,
        position: 1.0,
        version: 3,
        trashed: false,
        trashedAt: null,
        updatedAt: DateTime.now().toUtc(),
      ));

      await repo.updateNote('srv-n1', contentMd: 'edited');
      final outbox = await dao.allOutbox();
      expect(outbox.length, 1);
      expect(outbox.first.baseVersion, 3);
      // 乐观落库：本地内容已更新，version 保持基线等 flush 回填。
      final local = await dao.noteById('srv-n1');
      expect(local!.contentMd, 'edited');
      expect(local.version, 3);
    });

    test('trashNote marks trashed locally and enqueues trash op', () async {
      final fake = await _FakeNotes.start();
      addTearDown(fake.stop);
      final repo = NotesRepository(
        dao: dao,
        client: api.NotesClient(fake.url, 'tok'),
      );

      final n = await repo.createNote(title: 'to-trash');
      await repo.trashNote(n.id);
      final local = await dao.noteById(n.id);
      expect(local!.trashed, isTrue);
      expect(local.trashedAt, isNotNull);
      final outbox = await dao.allOutbox();
      expect(outbox.map((e) => e.op), contains('trash_note'));
    });

    test('saveAsCopy clones the local row with conflict-copy suffix',
        () async {
      final fake = await _FakeNotes.start();
      addTearDown(fake.stop);
      final repo = NotesRepository(
        dao: dao,
        client: api.NotesClient(fake.url, 'tok'),
      );

      await dao.upsertNote(LocalNote(
        id: 'srv-n1',
        notebookId: null,
        title: '菜谱',
        contentMd: 'local draft',
        isTodo: false,
        todoCompletedAt: null,
        position: 1.0,
        version: 5,
        trashed: false,
        trashedAt: null,
        updatedAt: DateTime.now().toUtc(),
      ));

      final copy = await repo.saveAsCopy('srv-n1');
      expect(copy.id, isNot(startsWith('local-')));
      expect(copy.title, '菜谱$kNoteConflictCopySuffix');
      expect(copy.contentMd, 'local draft');
      expect(copy.pendingCreate, isTrue);
      // 原行不动。
      final original = await dao.noteById('srv-n1');
      expect(original!.title, '菜谱');
    });
  });

  group('NoteOutboxFlusher', () {
    test('drains create ops; note keeps client uuid, notebook rekeys',
        () async {
      final fake = await _FakeNotes.start();
      addTearDown(fake.stop);
      final client = api.NotesClient(fake.url, 'tok');
      final repo = NotesRepository(dao: dao, client: client);
      final flusher = NoteOutboxFlusher(dao: dao, client: client);
      addTearDown(flusher.dispose);

      final nb = await repo.createNotebook('工作');
      final note = await repo.createNote(notebookId: nb.id, title: 'Hello');
      await flusher.flushOnce();

      final outbox = await dao.allOutbox();
      expect(outbox, isEmpty,
          reason: 'flusher should leave the outbox empty on success');

      // Notebook 仍是 local- 占位 → rekey 成服务端 id；note 用客户端
      // uuid 创建，flush 前后 id 不变。
      final nbs = await dao.listNotebooks();
      expect(nbs.first.id, startsWith('srv-'));
      final localNote = await dao.noteById(note.id);
      expect(localNote, isNotNull,
          reason: 'note id 不应在 flush 后被 rekey，UI 持有的引用保持有效');
      expect(localNote!.notebookId, nbs.first.id);
      expect(fake.notes.keys, contains(note.id),
          reason: '服务端应以客户端 uuid 落记录');
      expect(fake.notes[note.id]!['notebook_id'], nbs.first.id,
          reason: 'create_note payload 里的 local- 占位 notebook_id 必须随 '
              'rekey 重写，否则服务端拿到不存在的 id（4xx 丢 op，笔记永远 '
              '同步不上）');
    });

    test('离线建笔记本→移入笔记 回归：update_note payload 随 rekey 重写',
        () async {
      final fake = await _FakeNotes.start();
      addTearDown(fake.stop);
      final client = api.NotesClient(fake.url, 'tok');
      final repo = NotesRepository(dao: dao, client: client);
      final flusher = NoteOutboxFlusher(dao: dao, client: client);
      addTearDown(flusher.dispose);

      // 先同步建一篇根目录笔记，再离线建笔记本并移入 —— 三条 op 同批
      // 冲刷，create_notebook 成功后 update_note 的 payload 必须换成
      // 服务端 id。
      final note = await repo.createNote(title: 't');
      await flusher.flushOnce();
      final nb = await repo.createNotebook('收件箱');
      await repo.updateNote(note.id, notebookId: nb.id);
      await flusher.flushOnce();

      expect(await dao.allOutbox(), isEmpty);
      final srvNb = (await dao.listNotebooks()).single.id;
      expect(srvNb, startsWith('srv-'));
      expect(fake.notes[note.id]!['notebook_id'], srvNb,
          reason: '服务端收到的 notebook_id 必须是 rekey 后的 id，不是 '
              'local- 占位');
    });

    test('离线建标签→打标 回归：set_note_tags payload 随 rekey 重写',
        () async {
      final fake = await _FakeNotes.start();
      addTearDown(fake.stop);
      final client = api.NotesClient(fake.url, 'tok');
      final repo = NotesRepository(dao: dao, client: client);
      final flusher = NoteOutboxFlusher(dao: dao, client: client);
      addTearDown(flusher.dispose);

      final note = await repo.createNote(title: 't');
      await flusher.flushOnce();
      final tag = await repo.createTag('重要');
      await repo.setNoteTags(note.id, [tag.id]);
      await flusher.flushOnce();

      expect(await dao.allOutbox(), isEmpty);
      final srvTag = (await dao.listTags()).single.id;
      expect(srvTag, startsWith('srv-'));
      expect(fake.lastSetTagIds, [srvTag],
          reason: 'tag_ids 列表里的 local- 占位必须随 rekey 重写');
    });

    test('创建→flush→立即编辑 回归：updateNote 同一 id 正常落库不抛错',
        () async {
      final fake = await _FakeNotes.start();
      addTearDown(fake.stop);
      final client = api.NotesClient(fake.url, 'tok');
      final repo = NotesRepository(dao: dao, client: client);
      final flusher = NoteOutboxFlusher(dao: dao, client: client);
      addTearDown(flusher.dispose);

      final note = await repo.createNote(title: 't', contentMd: 'v1');
      await flusher.flushOnce();
      expect(await dao.allOutbox(), isEmpty);

      // P0 回归：flush 前 UI 持有的 id 在 flush 后必须仍然有效。
      await repo.updateNote(note.id, contentMd: 'v2');
      final local = await dao.noteById(note.id);
      expect(local!.contentMd, 'v2');
      final outbox = await dao.allOutbox();
      expect(outbox.single.op, 'update_note');
      expect(outbox.single.entityId, note.id);

      // 编辑也能顺利冲刷到服务端（版本回填）。
      await flusher.flushOnce();
      expect(await dao.allOutbox(), isEmpty);
      expect(fake.notes[note.id]!['content_md'], 'v2');
    });

    test('recoverOrphanedLocalNotes 补 create op 并经 rekey 同步上服务端',
        () async {
      final fake = await _FakeNotes.start();
      addTearDown(fake.stop);
      final client = api.NotesClient(fake.url, 'tok');
      final repo = NotesRepository(dao: dao, client: client);
      final flusher = NoteOutboxFlusher(dao: dao, client: client);
      addTearDown(flusher.dispose);

      // 模拟历史版本留下的孤儿行：local- 占位 id，无 create_note op。
      final now = DateTime.now().toUtc();
      await dao.upsertNote(LocalNote(
        id: 'local-orphan-1',
        notebookId: null,
        title: 'orphan',
        contentMd: 'local only',
        isTodo: false,
        todoCompletedAt: null,
        position: 0.0,
        version: 1,
        trashed: false,
        trashedAt: null,
        updatedAt: now,
      ));

      final recovered = await repo.recoverOrphanedLocalNotes();
      expect(recovered, 1);
      final outbox = await dao.allOutbox();
      expect(outbox.single.op, 'create_note');
      expect(outbox.single.entityId, 'local-orphan-1');
      final payload =
          jsonDecode(outbox.single.payloadJson) as Map<String, dynamic>;
      expect(payload.containsKey('id'), isFalse,
          reason: 'local- 不是合法 uuid，恢复 op 不带 id，走服务端分配 + rekey');
      expect(payload['content_md'], 'local only');

      // 幂等：再跑一次不重复入队。
      expect(await repo.recoverOrphanedLocalNotes(), 0);
      expect((await dao.allOutbox()).length, 1);

      // 冲刷后行走既有 rekey 路径换成服务端 id。
      await flusher.flushOnce();
      expect(await dao.noteById('local-orphan-1'), isNull);
      final rows = await db.select(db.noteNotes).get();
      expect(rows.single.id, startsWith('srv-'));
      expect(rows.single.contentMd, 'local only');
    });

    test('409 conflict surfaces on the conflicts stream and drops the op',
        () async {
      final fake = await _FakeNotes.start();
      addTearDown(fake.stop);
      fake.notes['srv-n1'] = {
        'id': 'srv-n1',
        'notebook_id': null,
        'title': 'server',
        'content_md': 'server content',
        'is_todo': false,
        'position': 1.0,
        'version': 7,
        'updated_at': DateTime.now().toUtc().toIso8601String(),
      };
      fake.simulate409 = true;

      final client = api.NotesClient(fake.url, 'tok');
      final repo = NotesRepository(dao: dao, client: client);
      final flusher = NoteOutboxFlusher(dao: dao, client: client);
      addTearDown(flusher.dispose);

      // Mirror the server note locally first, at a stale version.
      await dao.upsertNote(LocalNote(
        id: 'srv-n1',
        notebookId: null,
        title: 'server',
        contentMd: 'server content',
        isTodo: false,
        todoCompletedAt: null,
        position: 1.0,
        version: 1, // stale on purpose
        trashed: false,
        trashedAt: null,
        updatedAt: DateTime.now().toUtc(),
      ));
      await repo.updateNote('srv-n1', contentMd: 'edited offline');

      final conflictFuture = flusher.conflicts.first;
      await flusher.flushOnce();
      final conflict =
          await conflictFuture.timeout(const Duration(seconds: 2));

      expect(conflict.entityId, 'srv-n1');
      expect(conflict.baseVersion, 1);
      expect(await dao.allOutbox(), isEmpty,
          reason: '409 should drop the conflicting op');
    });

    test('update_note success backfills the server-bumped version', () async {
      final fake = await _FakeNotes.start();
      addTearDown(fake.stop);
      fake.notes['srv-n1'] = {
        'id': 'srv-n1',
        'notebook_id': null,
        'title': 'server',
        'content_md': 'server content',
        'is_todo': false,
        'position': 1.0,
        'version': 3,
        'updated_at': DateTime.now().toUtc().toIso8601String(),
      };

      final client = api.NotesClient(fake.url, 'tok');
      final repo = NotesRepository(dao: dao, client: client);
      final flusher = NoteOutboxFlusher(dao: dao, client: client);
      addTearDown(flusher.dispose);

      await dao.upsertNote(LocalNote(
        id: 'srv-n1',
        notebookId: null,
        title: 'server',
        contentMd: 'server content',
        isTodo: false,
        todoCompletedAt: null,
        position: 1.0,
        version: 3,
        trashed: false,
        trashedAt: null,
        updatedAt: DateTime.now().toUtc(),
      ));
      await repo.updateNote('srv-n1', contentMd: 'edited');
      await flusher.flushOnce();

      expect(await dao.allOutbox(), isEmpty);
      final local = await dao.noteById('srv-n1');
      expect(local!.version, 4, reason: 'server bump should be backfilled');
      expect(local.contentMd, 'edited');
      expect(fake.lastIfMatch, '3');
    });

    test('5xx triggers backoff (op stays queued, attempts increments)',
        () async {
      final fake = await _FakeNotes.start();
      addTearDown(fake.stop);
      fake.simulate500 = true;
      final client = api.NotesClient(fake.url, 'tok');
      final repo = NotesRepository(dao: dao, client: client);
      final flusher = NoteOutboxFlusher(dao: dao, client: client);
      addTearDown(flusher.dispose);

      await repo.createNote(title: 'Inbox');
      await flusher.flushOnce();

      final outbox = await dao.allOutbox();
      expect(outbox.length, 1);
      expect(outbox.first.attempts, 1);
      expect(outbox.first.lastError, isNotNull);
    });
  });

  group('NotesSyncPoller', () {
    test('pullOnce applies events (incl. tombstone) and advances the cursor',
        () async {
      final fake = await _FakeNotes.start();
      addTearDown(fake.stop);
      fake.addEvent('note.created', {
        'note_id': 'srv-n1',
        'title': 'from-other-device',
        'content_md': 'synced',
        'is_todo': false,
        'position': 1.0,
        'version': 1,
        'notebook_id': null,
        'updated_at': DateTime.now().toUtc().toIso8601String(),
      });
      fake.addEvent('note.created', {
        'note_id': 'srv-n2',
        'title': 'doomed',
        'content_md': '',
        'is_todo': false,
        'position': 2.0,
        'version': 1,
        'notebook_id': null,
        'updated_at': DateTime.now().toUtc().toIso8601String(),
      });
      fake.addEvent('note.deleted', {'note_id': 'srv-n2'});

      final repo = NotesRepository(
        dao: dao,
        client: api.NotesClient(fake.url, 'tok'),
      );
      final poller = NotesSyncPoller(db: db, repository: repo);
      addTearDown(poller.dispose);

      await poller.pullOnce();

      final n1 = await dao.noteById('srv-n1');
      expect(n1, isNotNull);
      expect(n1!.title, 'from-other-device');
      expect(n1.trashed, isFalse);

      final n2 = await dao.noteById('srv-n2');
      expect(n2, isNotNull);
      expect(n2!.trashed, isTrue, reason: 'tombstone event marks trashed');

      // Cursor persisted to SseCursors.
      final cursor = await (db.select(db.sseCursors)
            ..where((t) => t.scope.equals(NotesSyncPoller.cursorScope)))
          .getSingle();
      expect(cursor.lastEventId, '3');

      // 第二轮从 cursor 续拉：没有新事件，本地行不被重复应用（幂等）。
      await poller.pullOnce();
      expect((await dao.noteById('srv-n1'))!.title, 'from-other-device');
    });

    test('note.purged hard-deletes the local row', () async {
      final fake = await _FakeNotes.start();
      addTearDown(fake.stop);
      final now = DateTime.now().toUtc();
      await dao.upsertNote(LocalNote(
        id: 'srv-n9',
        notebookId: null,
        title: 'gone',
        contentMd: '',
        isTodo: false,
        todoCompletedAt: null,
        position: 0.0,
        version: 2,
        trashed: true,
        trashedAt: now,
        updatedAt: now,
      ));
      fake.addEvent('note.purged', {'note_id': 'srv-n9'});

      final repo = NotesRepository(
        dao: dao,
        client: api.NotesClient(fake.url, 'tok'),
      );
      final poller = NotesSyncPoller(db: db, repository: repo);
      addTearDown(poller.dispose);

      await poller.pullOnce();
      expect(await dao.noteById('srv-n9'), isNull);
    });
  });
}

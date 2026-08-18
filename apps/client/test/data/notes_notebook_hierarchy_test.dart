// 笔记本多级目录（parent_id）数据层行为测试 —— repository 乐观写 +
// outbox payload、flusher 父子 rekey/延后、deleted 事件子本上移、DTO 解析。
//
// 跑在 in-memory NativeDatabase + 假 brain（真 HttpServer）上，形态对齐
// notes_local_test.dart / note_outbox_flusher_test.dart。

import 'dart:convert';
import 'dart:io';

import 'package:biumind/data/api/notes_client.dart' as api;
import 'package:biumind/data/local/db.dart';
import 'package:biumind/data/local/notes_dao.dart';
import 'package:biumind/data/notes_repository.dart';
import 'package:biumind/data/outbox/note_outbox_flusher.dart';
import 'package:flutter_test/flutter_test.dart';

/// 假 brain —— 只实现 notebook 三个端点；记录请求体供断言。
/// parent_id 语义对齐真服务端（PR2）：POST 缺省 = 根级；PUT 字段缺省 =
/// 不动，'' = 升根，uuid = 移到该父本（'' 在响应里归一为 null）。
class _FakeBrain {
  final Map<String, Map<String, dynamic>> notebooks = {};
  final List<Map<String, dynamic>> requests = [];
  int _nextId = 0;
  HttpServer? _server;

  String get baseUrl => 'http://127.0.0.1:${_server!.port}';

  Future<void> start() async {
    _server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    _server!.listen(_handle);
  }

  Future<void> stop() async => _server?.close(force: true);

  Map<String, dynamic> _out(Map<String, dynamic> nb) => {
        'id': nb['id'],
        'name': nb['name'],
        'parent_id': nb['parent_id'],
        'position': nb['position'] ?? 0.0,
        'created_at': DateTime.utc(2100).toIso8601String(),
        'updated_at': DateTime.utc(2100).toIso8601String(),
      };

  Future<void> _handle(HttpRequest req) async {
    final body = await utf8.decoder.bind(req).join();
    final parts =
        req.uri.path.split('/').where((s) => s.isNotEmpty).toList();
    final j = body.isEmpty
        ? <String, dynamic>{}
        : jsonDecode(body) as Map<String, dynamic>;

    if (req.method == 'POST' && req.uri.path == '/v1/notebooks') {
      requests.add({'method': 'POST', 'body': j});
      final nb = {
        'id': 'srv-nb${++_nextId}',
        'name': j['name'],
        'parent_id': j['parent_id'],
        'position': j['position'] ?? 0.0,
      };
      notebooks[nb['id'] as String] = nb;
      req.response.headers.contentType = ContentType.json;
      req.response.write(jsonEncode(_out(nb)));
      await req.response.close();
      return;
    }
    if (req.method == 'PUT' &&
        parts.length == 3 &&
        parts[1] == 'notebooks') {
      requests.add({'method': 'PUT', 'id': parts[2], 'body': j});
      final nb = notebooks[parts[2]];
      if (nb == null) {
        req.response.statusCode = 404;
        req.response.write(jsonEncode({'error': {'code': 'not_found'}}));
        await req.response.close();
        return;
      }
      if (j.containsKey('name')) nb['name'] = j['name'];
      if (j.containsKey('position')) nb['position'] = j['position'];
      if (j.containsKey('parent_id')) {
        final p = j['parent_id'] as String?;
        nb['parent_id'] = (p == null || p.isEmpty) ? null : p;
      }
      req.response.headers.contentType = ContentType.json;
      req.response.write(jsonEncode(_out(nb)));
      await req.response.close();
      return;
    }
    req.response.statusCode = 404;
    await req.response.close();
  }
}

api.NoteChangeEvent _event(String type, Map<String, dynamic> payload) =>
    api.NoteChangeEvent(
      id: 1,
      eventType: type,
      actorId: 'tester',
      payload: payload,
      createdAt: DateTime.utc(2100),
    );

void main() {
  late AppDb db;
  late NotesDao dao;
  late api.NotesClient client;
  late NotesRepository repo;
  late _FakeBrain brain;

  setUp(() async {
    db = AppDb.memory();
    dao = NotesDao(db, scope: 'test');
    brain = _FakeBrain();
    await brain.start();
    client = api.NotesClient(Uri.parse(brain.baseUrl), 'tok');
    repo = NotesRepository(dao: dao, client: client);
  });

  tearDown(() async {
    await brain.stop();
    await db.close();
  });

  test('DTO: parent_id 解析（有值 / null / 缺省）', () {
    Map<String, dynamic> j(dynamic parent) => {
          'id': 'nb1',
          'name': 'n',
          'position': 0,
          'updated_at': '2100-01-01T00:00:00Z',
          if (!identical(parent, _absent)) 'parent_id': parent,
        };
    expect(api.NoteNotebook.fromJson(j('p1')).parentId, 'p1');
    expect(api.NoteNotebook.fromJson(j(null)).parentId, isNull);
    expect(api.NoteNotebook.fromJson(j(_absent)).parentId, isNull);
  });

  test('createNotebook 带 parentId：本地行 + outbox payload 都带上', () async {
    final parent = await repo.createNotebook('父');
    final child = await repo.createNotebook('子', parentId: parent.id);

    expect(child.parentId, parent.id);
    final row = await dao.notebookById(child.id);
    expect(row?.parentId, parent.id);

    final outbox = await dao.allOutbox();
    expect(outbox, hasLength(2));
    final childOp = outbox.firstWhere((e) => e.entityId == child.id);
    expect(childOp.op, NoteOutboxOp.createNotebook);
    final payload = jsonDecode(childOp.payloadJson) as Map<String, dynamic>;
    expect(payload['parent_id'], parent.id);
    // 父本的 op 不带 parent_id key（根级，presence = 不设）。
    final parentPayload =
        jsonDecode(outbox.first.payloadJson) as Map<String, dynamic>;
    expect(parentPayload.containsKey('parent_id'), isFalse);
  });

  test('updateNotebook 三态：不动 / 移到父本 / 升根', () async {
    final a = await repo.createNotebook('A');
    final b = await repo.createNotebook('B');
    final c = await repo.createNotebook('C', parentId: b.id);
    await dao.wipe();
    // wipe 清掉了行，重新种（上面只验证 create 路径；这里直接走 DAO 造初态）。
    Future<void> seed(String id, String? parent) => dao.upsertNotebook(
        LocalNoteNotebook(
            id: id,
            name: id,
            parentId: parent,
            position: 0,
            ownerKey: 'test',
            updatedAt: DateTime.utc(2100)));
    await seed(a.id, null);
    await seed(b.id, null);
    await seed(c.id, b.id);

    // 1) 只改名 → parent 不动，payload 无 parent_id。
    await repo.updateNotebook(c.id, name: 'C2');
    var row = await dao.notebookById(c.id);
    expect(row?.name, 'C2');
    expect(row?.parentId, b.id);
    var ops = await dao.allOutbox();
    var payload = jsonDecode(ops.last.payloadJson) as Map<String, dynamic>;
    expect(payload.containsKey('parent_id'), isFalse);

    // 2) 移到 A 下 → 本地 + payload parent_id = A。
    await repo.updateNotebook(c.id, parentId: a.id);
    row = await dao.notebookById(c.id);
    expect(row?.parentId, a.id);
    ops = await dao.allOutbox();
    payload = jsonDecode(ops.last.payloadJson) as Map<String, dynamic>;
    expect(payload['parent_id'], a.id);

    // 3) 升根 → 本地 parentId = null，payload parent_id = ''（服务端约定）。
    await repo.updateNotebook(c.id, moveToRoot: true);
    row = await dao.notebookById(c.id);
    expect(row?.parentId, isNull);
    ops = await dao.allOutbox();
    payload = jsonDecode(ops.last.payloadJson) as Map<String, dynamic>;
    expect(payload['parent_id'], '');
  });

  test('flusher 父子 rekey：父 create 成功后子 op 的 parent_id 被改写', () async {
    // 离线连建：父（local- 占位）→ 子（parent_id = 父的 local- id）。
    final parent = await repo.createNotebook('父');
    await repo.createNotebook('子', parentId: parent.id);

    final flusher = NoteOutboxFlusher(dao: dao, client: client);
    await flusher.flushOnce();

    // 两条 op 都冲掉了。
    expect(await dao.allOutbox(), isEmpty);
    // 两个 POST：子的 body.parent_id 已是父的服务端 uuid（rekey 生效）。
    expect(brain.requests, hasLength(2));
    final childReq = brain.requests[1]['body'] as Map<String, dynamic>;
    final parentSrvId = brain.notebooks.keys.first;
    expect(childReq['parent_id'], parentSrvId,
        reason: '子 op 上送前 parent_id 应被 rekey 成服务端 uuid');
    // 本地行：父 rekey 成 srv id，子 parentId 同步改写。
    final parentRow = await dao.notebookById(parentSrvId);
    expect(parentRow, isNotNull);
    final childSrvId =
        brain.notebooks.keys.firstWhere((id) => id != parentSrvId);
    final childRow = await dao.notebookById(childSrvId);
    expect(childRow?.parentId, parentSrvId);
  });

  test('flusher 延后：parent_id 还是 local- 占位时 op 不被 4xx 丢掉', () async {
    // 直接入队一条父本不存在的子 op（模拟父 op 被 backoff 到未来）。
    await dao.upsertNotebook(LocalNoteNotebook(
        id: 'local-child',
        name: '子',
        parentId: 'local-missing-parent',
        position: 0,
        ownerKey: 'test',
        updatedAt: DateTime.utc(2100)));
    await dao.enqueueOutbox(NoteOutboxCompanion.insert(
      op: NoteOutboxOp.createNotebook,
      entityId: 'local-child',
      payloadJson: jsonEncode({'name': '子', 'parent_id': 'local-missing-parent'}),
      createdAt: DateTime.utc(2000),
      nextAttemptAt: DateTime.utc(2000),
    ));

    final flusher = NoteOutboxFlusher(dao: dao, client: client);
    await flusher.flushOnce();

    // op 仍在（backoff 延后），没有上送（上送会 400 → 被永久 drop）。
    final outbox = await dao.allOutbox();
    expect(outbox, hasLength(1));
    expect(outbox.first.attempts, 1);
    expect(brain.requests, isEmpty);
  });

  test('flusher update_notebook 透传 parent_id（含升根空串）', () async {
    // 先建父/子并冲掉 create op（拿到 srv id）。
    final parent = await repo.createNotebook('父');
    await repo.createNotebook('子', parentId: parent.id);
    final flusher = NoteOutboxFlusher(dao: dao, client: client);
    await flusher.flushOnce();
    brain.requests.clear();
    final childSrvId = (await dao.listNotebooks())
        .firstWhere((r) => r.name == '子')
        .id;

    // reparent 升根 → flush → PUT body parent_id = ''。
    await repo.updateNotebook(childSrvId, moveToRoot: true);
    await flusher.flushOnce();
    final put = brain.requests.singleWhere((r) => r['method'] == 'PUT');
    expect((put['body'] as Map)['parent_id'], '');
    // 服务端（假 brain）归一为 null，本地行也已清。
    final row = await dao.notebookById(childSrvId);
    expect(row?.parentId, isNull);
  });

  test('notebook.deleted 事件：子本上移到祖父 / 根', () async {
    Future<void> seed(String id, String? parent) => dao.upsertNotebook(
        LocalNoteNotebook(
            id: id,
            name: id,
            parentId: parent,
            position: 0,
            ownerKey: 'test',
            updatedAt: DateTime.utc(2100)));
    await seed('G', null);
    await seed('P', 'G');
    await seed('C', 'P');
    await seed('R', null);
    await seed('C2', 'R');

    // 删 P → C 上移到 G 下（服务端子本上移不发事件，客户端本地重放）。
    await repo.applyChanges([
      _event('notebook.deleted', {'notebook_id': 'P'}),
    ]);
    expect(await dao.notebookById('P'), isNull);
    expect((await dao.notebookById('C'))?.parentId, 'G');

    // 删根 R → C2 变根。
    await repo.applyChanges([
      _event('notebook.deleted', {'notebook_id': 'R'}),
    ]);
    expect((await dao.notebookById('C2'))?.parentId, isNull);
  });

  test('notebook.created/updated 事件 upsert 带 parent_id（含升根清空）', () async {
    await repo.applyChanges([
      _event('notebook.created', {
        'notebook_id': 'P',
        'name': '父',
        'parent_id': null,
      }),
      _event('notebook.created', {
        'notebook_id': 'C',
        'name': '子',
        'parent_id': 'P',
      }),
    ]);
    expect((await dao.notebookById('C'))?.parentId, 'P');

    // updated 事件 parent_id=null → 本地必须真清空（replace 语义，不是
    // insertOnConflictUpdate 的 null-as-absent）。
    await repo.applyChanges([
      _event('notebook.updated', {
        'notebook_id': 'C',
        'name': '子',
        'parent_id': null,
        'position': 0.0,
      }),
    ]);
    expect((await dao.notebookById('C'))?.parentId, isNull);
  });
}

const _absent = Object();

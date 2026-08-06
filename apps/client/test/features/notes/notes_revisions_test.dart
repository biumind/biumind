// N3 版本历史 / 转知识库 / 归档提示条测试。
//
// 形态对齐 notes_search_ui_test.dart：真实 loopback HttpServer 充当
// brain + AppDb.memory 真 NotesRepository。provider / repository 接线
// 与归档后的列表语义（watchNotes 排除归档）在这里锁；
// NoteArchivedBanner 是纯 widget，直接 pump 测显隐。

import 'dart:convert';
import 'dart:io';

import 'package:biumind/data/api/notes_client.dart' as api;
import 'package:biumind/data/local/db.dart';
import 'package:biumind/data/local/notes_dao.dart';
import 'package:biumind/data/notes_providers.dart';
import 'package:biumind/data/notes_repository.dart';
import 'package:biumind/features/notes/application/notes_ui_providers.dart';
import 'package:biumind/features/notes/presentation/note_revisions_dialog.dart'
    show revisionChangeTypeLabel;
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

class _FakeBrain {
  _FakeBrain._(this.server);
  final HttpServer server;
  String lastBody = '';

  static Future<_FakeBrain> start() async {
    final s = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    final fake = _FakeBrain._(s);
    s.listen(fake._handle);
    return fake;
  }

  Uri get url => Uri.parse('http://${server.address.host}:${server.port}');
  Future<void> stop() => server.close(force: true);

  static Map<String, dynamic> _noteJson(String id, {bool archived = false}) =>
      {
        'id': id,
        'notebook_id': null,
        'title': 't-$id',
        'content_md': 'body-$id',
        'is_todo': false,
        'position': 0.0,
        'version': 2,
        'created_at': '2026-07-29T00:00:00Z',
        'updated_at': '2026-07-29T02:00:00Z',
        'archived_at': archived ? '2026-07-29T04:00:00Z' : null,
        'promoted_page_id': archived ? 'p1' : null,
      };

  Future<void> _handle(HttpRequest req) async {
    lastBody = await utf8.decoder.bind(req).join();
    final path = req.uri.path;
    Map<String, dynamic> response;
    var status = 200;

    if (req.method == 'GET' && path == '/v1/notes/n1/revisions') {
      response = {
        'revisions': [
          {
            'id': 'r1',
            'note_id': 'n1',
            'title': 'hello',
            'change_type': 'edit',
            'change_summary': '编辑',
            'created_at': '2026-07-29T01:00:00Z',
          }
        ]
      };
    } else if (req.method == 'POST' &&
        path == '/v1/notes/n1/revisions/r1/restore') {
      response = _noteJson('n1');
    } else if (req.method == 'POST' &&
        path == '/v1/notes/n1/revisions/r1/save-as-copy') {
      response = _noteJson('n-copy');
    } else if (req.method == 'POST' && path == '/v1/notes/n1/promote') {
      response = {
        'page': {'id': 'p1'},
        'note': _noteJson('n1', archived: true),
      };
    } else if (req.method == 'POST' && path == '/v1/notes/n1/unarchive') {
      response = _noteJson('n1');
    } else {
      status = 404;
      response = {'error': 'unknown_route', 'path': path};
    }

    req.response.statusCode = status;
    req.response.headers.contentType = ContentType.json;
    req.response.write(jsonEncode(response));
    await req.response.close();
  }
}

void main() {
  late _FakeBrain fake;
  late AppDb db;
  late NotesRepository repo;
  late ProviderContainer container;

  setUp(() async {
    fake = await _FakeBrain.start();
    db = AppDb.memory();
    repo = NotesRepository(
      dao: NotesDao(db),
      client: api.NotesClient(fake.url, 'tok'),
    );
    container = ProviderContainer(overrides: <Override>[
      notesRepositoryProvider.overrideWithValue(repo),
    ]);
  });

  tearDown(() async {
    container.dispose();
    await db.close();
    await fake.stop();
  });

  group('revisionChangeTypeLabel', () {
    test('edit/restore 映射中文，未知值原样', () {
      expect(revisionChangeTypeLabel('edit'), '编辑');
      expect(revisionChangeTypeLabel('restore'), '恢复点');
      expect(revisionChangeTypeLabel('snapshot'), 'snapshot');
    });
  });

  group('noteRevisionsProvider', () {
    test('拉服务端版本列表（不含 content_md）', () async {
      final revs = await container.read(noteRevisionsProvider('n1').future);
      expect(revs.single.id, 'r1');
      expect(revs.single.changeType, 'edit');
      expect(revs.single.contentMd, isNull);
    });
  });

  group('repository 版本动作落库', () {
    test('restoreRevision 把返回的 note 落 Drift', () async {
      final note = await repo.restoreRevision('n1', 'r1');
      expect(note.id, 'n1');
      final listed = await repo.watchNotes().first;
      expect(listed.map((n) => n.id), contains('n1'));
    });

    test('saveRevisionAsCopy 新笔记落库出现在列表', () async {
      final copy = await repo.saveRevisionAsCopy('n1', 'r1');
      expect(copy.id, 'n-copy');
      final listed = await repo.watchNotes().first;
      expect(listed.map((n) => n.id), contains('n-copy'));
    });
  });

  group('归档 / 转知识库', () {
    test('promoteNote 后归档笔记从默认列表消失，unarchive 后回来', () async {
      final result = await repo.promoteNote('n1', 'proj-1');
      final sent = jsonDecode(fake.lastBody) as Map<String, dynamic>;
      expect(sent, {'project_id': 'proj-1'});
      expect(result.note.promotedPageId, 'p1');
      expect(result.note.archivedAt, isNotNull);

      // 归档后默认列表（watchNotes 排除 archivedAt 非空）看不到；
      // getNote 按 id 直读仍拿得到（编辑器打开归档笔记显示提示条用）。
      expect(await repo.watchNotes().first, isEmpty);
      final byId = await repo.getNote('n1');
      expect(byId, isNotNull);
      expect(byId!.promotedPageId, 'p1');

      await repo.unarchiveNote('n1');
      final listed = await repo.watchNotes().first;
      expect(listed.single.id, 'n1');
      expect(listed.single.archivedAt, isNull);
      expect(listed.single.promotedPageId, isNull);
    });
  });
}

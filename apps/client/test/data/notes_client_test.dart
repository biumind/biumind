// NotesClient HTTP-layer tests — mirror wiki_client_test.dart: a real
// loopback HttpServer stands in for brain, so paths / query params /
// If-Match headers / DTO parsing are all exercised for real.

import 'dart:convert';
import 'dart:io';

import 'package:biumind/data/api/notes_client.dart';
import 'package:flutter_test/flutter_test.dart';

class _FakeNotes {
  _FakeNotes._(this.server);
  final HttpServer server;
  final List<String> seenAuth = [];
  String? lastIfMatch;
  Map<String, String> lastQuery = {};
  String lastBody = '';

  /// 服务端 note JSON（含 N3 新字段 source_url/author/archived_at/
  /// promoted_page_id，均可空）。
  static Map<String, dynamic> _noteJson(
    String id, {
    String title = 't',
    bool archived = false,
  }) =>
      {
        'id': id,
        'notebook_id': null,
        'title': title,
        'content_md': '',
        'is_todo': false,
        'position': 0.0,
        'version': 3,
        'created_at': '2026-07-29T00:00:00Z',
        'updated_at': '2026-07-29T02:00:00Z',
        'source_url': 'https://example.com/a',
        'author': 'someone',
        'archived_at': archived ? '2026-07-29T04:00:00Z' : null,
        'promoted_page_id': archived ? 'p1' : null,
      };

  static Future<_FakeNotes> start() async {
    final s = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    final fake = _FakeNotes._(s);
    s.listen(fake._handle);
    return fake;
  }

  Uri get url => Uri.parse('http://${server.address.host}:${server.port}');
  Future<void> stop() => server.close(force: true);

  Future<void> _handle(HttpRequest req) async {
    seenAuth.add(req.headers.value(HttpHeaders.authorizationHeader) ?? '');
    lastIfMatch = req.headers.value('If-Match');
    lastQuery = req.uri.queryParameters;
    lastBody = await utf8.decoder.bind(req).join();
    final path = req.uri.path;
    Map<String, dynamic> response;
    var status = 200;

    if (req.method == 'GET' && path == '/v1/notes') {
      response = {
        'notes': [
          {
            'id': 'n1',
            'notebook_id': null,
            'title': 'hello',
            'content_md': '# hi',
            'is_todo': true,
            'todo_completed_at': null,
            'position': 1.5,
            'version': 2,
            'created_at': '2026-07-29T00:00:00Z',
            'updated_at': '2026-07-29T01:00:00Z',
          }
        ]
      };
    } else if (req.method == 'POST' && path == '/v1/notes') {
      response = {
        'id': 'srv-n1',
        'notebook_id': null,
        'title': 't',
        'content_md': '',
        'is_todo': false,
        'position': 0.0,
        'version': 1,
        'created_at': '2026-07-29T00:00:00Z',
        'updated_at': '2026-07-29T00:00:00Z',
      };
    } else if (req.method == 'PUT' && path == '/v1/notes/n1') {
      if (req.headers.value('If-Match') != '2') {
        status = 409;
        response = {
          'error': {'code': 'version_conflict'},
          'current_version': 3,
        };
      } else {
        response = {
          'id': 'n1',
          'notebook_id': null,
          'title': 't',
          'content_md': '',
          'is_todo': false,
          'position': 0.0,
          'version': 3,
          'created_at': '2026-07-29T00:00:00Z',
          'updated_at': '2026-07-29T02:00:00Z',
        };
      }
    } else if (req.method == 'GET' && path == '/v1/notes/changes') {
      response = {
        'events': [
          {
            'id': 11,
            'event_type': 'note.deleted',
            'actor_id': 'u1',
            'payload': {'note_id': 'n9'},
            'created_at': '2026-07-29T03:00:00Z',
          }
        ],
        'latest': 11,
      };
    } else if (req.method == 'GET' && path == '/v1/notes/n1/revisions') {
      // 列表响应不含 content_md（只有元信息）。
      response = {
        'revisions': [
          {
            'id': 'r2',
            'note_id': 'n1',
            'title': 'hello v2',
            'change_type': 'restore',
            'change_summary': '恢复前自动备份',
            'created_at': '2026-07-29T02:00:00Z',
          },
          {
            'id': 'r1',
            'note_id': 'n1',
            'title': 'hello',
            'change_type': 'edit',
            'change_summary': '编辑',
            'created_at': '2026-07-29T01:00:00Z',
          },
        ]
      };
    } else if (req.method == 'GET' && path == '/v1/notes/n1/revisions/r1') {
      response = {
        'id': 'r1',
        'note_id': 'n1',
        'title': 'hello',
        'change_type': 'edit',
        'change_summary': '编辑',
        'created_at': '2026-07-29T01:00:00Z',
        'content_md': '# 旧内容',
      };
    } else if (req.method == 'POST' &&
        path == '/v1/notes/n1/revisions/r1/restore') {
      response = _noteJson('n1', archived: false);
    } else if (req.method == 'POST' &&
        path == '/v1/notes/n1/revisions/r1/save-as-copy') {
      response = _noteJson('n-copy', title: 'hello（历史副本）');
    } else if (req.method == 'POST' && path == '/v1/notes/n1/promote') {
      response = {
        'page': {'id': 'p1', 'title': 'hello'},
        'note': _noteJson('n1', archived: true),
      };
    } else if (req.method == 'POST' && path == '/v1/notes/n1/unarchive') {
      response = _noteJson('n1', archived: false);
    } else if (req.method == 'GET' && path == '/v1/notes/missing') {
      status = 404;
      response = {
        'error': {'code': 'not_found'}
      };
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
  late _FakeNotes fake;
  late NotesClient client;

  setUp(() async {
    fake = await _FakeNotes.start();
    client = NotesClient(fake.url, 'tok-123');
  });

  tearDown(() => fake.stop());

  test('listNotes sends bearer + filters and parses DTOs', () async {
    final notes = await client.listNotes(
        notebookId: 'nb1', todoOnly: true, limit: 50);
    expect(fake.seenAuth.last, 'Bearer tok-123');
    expect(fake.lastQuery['notebook_id'], 'nb1');
    expect(fake.lastQuery['todo'], 'true');
    expect(fake.lastQuery['limit'], '50');
    expect(notes.single.id, 'n1');
    expect(notes.single.isTodo, isTrue);
    expect(notes.single.version, 2);
    expect(notes.single.position, 1.5);
  });

  test('listNotes rootOnly maps to notebook_id=root', () async {
    await client.listNotes(rootOnly: true);
    expect(fake.lastQuery['notebook_id'], 'root');
  });

  test('createNote posts the server-shaped body', () async {
    final n = await client.createNote(
      title: 't',
      contentMd: 'body',
      isTodo: true,
      position: 2.0,
    );
    final sent = jsonDecode(fake.lastBody) as Map<String, dynamic>;
    expect(sent['title'], 't');
    expect(sent['content_md'], 'body');
    expect(sent['is_todo'], true);
    expect(sent['position'], 2.0);
    expect(sent.containsKey('notebook_id'), isFalse,
        reason: 'null notebookId 不应出现在 body 里（presence 语义）');
    expect(n.id, 'srv-n1');
  });

  test('createNote 带 id 时请求体包含该 id（服务端幂等键）', () async {
    const clientId = '3f6b8c3e-1234-4abc-8def-0123456789ab';
    await client.createNote(id: clientId, title: 't');
    final sent = jsonDecode(fake.lastBody) as Map<String, dynamic>;
    expect(sent['id'], clientId);
  });

  test('createNote 不带 id 时请求体不含 id 字段（服务端分配）', () async {
    await client.createNote(title: 't');
    final sent = jsonDecode(fake.lastBody) as Map<String, dynamic>;
    expect(sent.containsKey('id'), isFalse);
  });

  test('updateNote sends If-Match and parses the bumped version', () async {
    final n = await client.updateNote('n1',
        ifMatchVersion: 2, title: 'new', notebookId: '');
    expect(fake.lastIfMatch, '2');
    final sent = jsonDecode(fake.lastBody) as Map<String, dynamic>;
    expect(sent['notebook_id'], '', reason: '空串 = 移回根');
    expect(sent['title'], 'new');
    expect(n.version, 3);
  });

  test('updateNote 409 surfaces NotesApiError.isVersionConflict', () async {
    try {
      await client.updateNote('n1', ifMatchVersion: 1, title: 'x');
      fail('should have thrown');
    } on NotesApiError catch (e) {
      expect(e.isVersionConflict, isTrue);
      expect(e.status, 409);
      expect(e.body, contains('version_conflict'));
    }
  });

  test('changes parses events + latest cursor', () async {
    final page = await client.changes(10);
    expect(fake.lastQuery['since'], '10');
    expect(page.latest, 11);
    expect(page.events.single.eventType, 'note.deleted');
    expect(page.events.single.payload['note_id'], 'n9');
  });

  test('404 surfaces NotesApiError.isNotFound', () async {
    try {
      await client.getNote('missing');
      fail('should have thrown');
    } on NotesApiError catch (e) {
      expect(e.isNotFound, isTrue);
    }
  });

  // ─── N3：版本历史 / 归档 / 转知识库 ─────────────────────────

  test('listNotes archivedOnly sends archived=only', () async {
    await client.listNotes(archivedOnly: true);
    expect(fake.lastQuery['archived'], 'only');
  });

  test('listRevisions sends paging and parses metadata-only rows', () async {
    final revs = await client.listRevisions('n1', limit: 50, offset: 10);
    expect(fake.lastQuery['limit'], '50');
    expect(fake.lastQuery['offset'], '10');
    expect(revs, hasLength(2));
    expect(revs.first.id, 'r2');
    expect(revs.first.changeType, 'restore');
    expect(revs.first.changeSummary, '恢复前自动备份');
    expect(revs.first.contentMd, isNull, reason: '列表响应不含 content_md');
    expect(revs.last.changeType, 'edit');
  });

  test('getRevision parses full detail incl. content_md', () async {
    final rev = await client.getRevision('n1', 'r1');
    expect(rev.title, 'hello');
    expect(rev.contentMd, '# 旧内容');
    expect(rev.createdAt, DateTime.utc(2026, 7, 29, 1));
  });

  test('restoreRevision posts and parses the updated note', () async {
    final note = await client.restoreRevision('n1', 'r1');
    expect(note.id, 'n1');
    expect(note.archivedAt, isNull);
    expect(note.promotedPageId, isNull);
  });

  test('saveRevisionAsCopy parses the new note', () async {
    final note = await client.saveRevisionAsCopy('n1', 'r1');
    expect(note.id, 'n-copy');
    expect(note.title, 'hello（历史副本）');
  });

  test('promoteNote posts project_id and parses page + archived note',
      () async {
    final result = await client.promoteNote('n1', 'proj-1');
    final sent = jsonDecode(fake.lastBody) as Map<String, dynamic>;
    expect(sent, {'project_id': 'proj-1'});
    expect(result.page['id'], 'p1');
    expect(result.note.promotedPageId, 'p1');
    expect(result.note.archivedAt, DateTime.utc(2026, 7, 29, 4));
    expect(result.note.sourceUrl, 'https://example.com/a');
    expect(result.note.author, 'someone');
  });

  test('unarchiveNote parses the cleared archive fields', () async {
    final note = await client.unarchiveNote('n1');
    expect(note.id, 'n1');
    expect(note.archivedAt, isNull);
    expect(note.promotedPageId, isNull);
  });
}

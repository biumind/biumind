// Drift DAO + WikiRepository + OutboxFlusher behavioural tests.
//
// All tests run against an in-memory NativeDatabase so they're hermetic.
// The fake API server is reused from `wiki_client_test.dart` to keep the
// HTTP surface honest (real headers, real paths, real 409 conflicts).

import 'dart:convert';
import 'dart:io';

import 'package:biumind/data/api/wiki_client.dart' as api;
import 'package:biumind/data/local/db.dart';
import 'package:biumind/data/local/wiki_dao.dart';
import 'package:biumind/data/outbox/wiki_outbox_flusher.dart';
import 'package:biumind/data/wiki_repository.dart';
import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_test/flutter_test.dart';

class _FakeWiki {
  _FakeWiki._(this.server);
  final HttpServer server;
  int _nextId = 0;
  final Map<String, Map<String, dynamic>> projects = {};
  final Map<String, List<Map<String, dynamic>>> pagesByProject = {};
  final Map<String, List<Map<String, dynamic>>> blocksByPage = {};
  bool simulate409 = false;
  bool simulate500 = false;

  static Future<_FakeWiki> start() async {
    final s = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    final fake = _FakeWiki._(s);
    s.listen(fake._handle);
    return fake;
  }

  Uri get url => Uri.parse('http://${server.address.host}:${server.port}');
  Future<void> stop() => server.close(force: true);

  Future<void> _handle(HttpRequest req) async {
    final body = await utf8.decoder.bind(req).join();
    final parts = req.uri.path.split('/').where((s) => s.isNotEmpty).toList();
    Map<String, dynamic>? response;
    int status = 200;

    if (simulate500) {
      status = 500;
      response = {'error': 'fake_500'};
    } else if (req.method == 'GET' && req.uri.path == '/v1/wiki/projects') {
      response = {'projects': projects.values.toList()};
    } else if (req.method == 'POST' && req.uri.path == '/v1/wiki/projects') {
      final j = jsonDecode(body) as Map<String, dynamic>;
      final id = 'srv-p${++_nextId}';
      final p = {'id': id, 'name': j['name']};
      projects[id] = p;
      response = p;
    } else if (req.method == 'GET' &&
        parts.length == 5 &&
        parts[4] == 'pages') {
      response = {'pages': pagesByProject[parts[3]] ?? []};
    } else if (req.method == 'POST' &&
        parts.length == 5 &&
        parts[4] == 'pages') {
      final pid = parts[3];
      final j = jsonDecode(body) as Map<String, dynamic>;
      final page = {
        'id': 'srv-pg${++_nextId}',
        'project_id': pid,
        'title': j['title'],
        'version': 1,
        'updated_at': DateTime.now().toUtc().toIso8601String(),
      };
      pagesByProject.putIfAbsent(pid, () => []).add(page);
      response = page;
    } else if (req.method == 'GET' &&
        parts.length == 7 &&
        parts[6] == 'blocks') {
      response = {'blocks': blocksByPage[parts[5]] ?? []};
    } else if (req.method == 'POST' &&
        parts.length == 7 &&
        parts[6] == 'blocks') {
      final pageId = parts[5];
      final j = jsonDecode(body) as Map<String, dynamic>;
      final blk = {
        'id': 'srv-b${++_nextId}',
        'page_id': pageId,
        'position': j['position'],
        'type': j['type'],
        'content': j['content'],
        'version': 1,
      };
      blocksByPage.putIfAbsent(pageId, () => []).add(blk);
      response = blk;
    } else if (req.method == 'PUT' &&
        parts.length == 6 &&
        parts[4] == 'blocks') {
      final blockId = parts[5];
      Map<String, dynamic>? blk;
      for (final list in blocksByPage.values) {
        for (final b in list) {
          if (b['id'] == blockId) {
            blk = b;
            break;
          }
        }
        if (blk != null) break;
      }
      if (blk == null) {
        status = 404;
        response = {'error': 'not_found'};
      } else if (simulate409) {
        status = 409;
        response = {'error': 'version_conflict', 'server_version': blk['version']};
      } else {
        final j = jsonDecode(body) as Map<String, dynamic>;
        blk['content'] = j['content'];
        blk['version'] = (blk['version'] as int) + 1;
        response = blk;
      }
    } else if (req.method == 'DELETE' &&
        parts.length == 6 &&
        parts[4] == 'blocks') {
      final blockId = parts[5];
      for (final list in blocksByPage.values) {
        list.removeWhere((b) => b['id'] == blockId);
      }
      response = {'ok': true};
    } else {
      status = 404;
      response = {'error': 'unknown_route', 'path': req.uri.path, 'method': req.method};
    }

    req.response.statusCode = status;
    req.response.headers.contentType = ContentType.json;
    req.response.write(jsonEncode(response));
    await req.response.close();
  }
}

void main() {
  late AppDb db;
  late WikiDao dao;

  setUp(() {
    db = AppDb.memory();
    dao = WikiDao(db);
  });

  tearDown(() => db.close());

  group('WikiDao', () {
    test('upserts and watches projects', () async {
      await dao.upsertProject(LocalWikiProject(
        id: 'p1',
        name: 'Notes',
        updatedAt: DateTime.now().toUtc(),
      ));
      final list = await dao.listProjects();
      expect(list.length, 1);
      expect(list.first.name, 'Notes');
    });

    test('renameProjectId migrates pages', () async {
      final now = DateTime.now().toUtc();
      await dao.upsertProject(LocalWikiProject(id: 'local-1', name: 'X', updatedAt: now));
      await dao.upsertPage(LocalWikiPage(
        id: 'local-pg-1',
        projectId: 'local-1',
        title: 'page',
        version: 1,
        parentId: null,
        updatedAt: now,
      ));
      await dao.renameProjectId('local-1', 'srv-1');
      final pages = await db.select(db.wikiPages).get();
      expect(pages.first.projectId, 'srv-1');
    });

    test('outbox enqueue + due + delete', () async {
      final now = DateTime.now().toUtc();
      final id = await dao.enqueueOutbox(WikiOutboxCompanion.insert(
        op: 'create_block',
        entityId: 'local-b-1',
        payloadJson: '{}',
        createdAt: now,
        nextAttemptAt: now,
      ));
      final due = await dao.dueOutbox(now: now.add(const Duration(seconds: 1)));
      expect(due.length, 1);
      await dao.deleteOutbox(id);
      expect((await dao.allOutbox()).length, 0);
    });

    test('bumpOutboxFailure increments attempts and reschedules', () async {
      final now = DateTime.now().toUtc();
      final id = await dao.enqueueOutbox(WikiOutboxCompanion.insert(
        op: 'create_block',
        entityId: 'local-b-1',
        payloadJson: '{}',
        createdAt: now,
        nextAttemptAt: now,
      ));
      await dao.bumpOutboxFailure(id, 'boom', now.add(const Duration(seconds: 60)));
      final entry = (await dao.allOutbox()).first;
      expect(entry.attempts, 1);
      expect(entry.lastError, 'boom');
    });
  });

  group('WikiRepository', () {
    test('createProject is optimistic and queues an outbox entry', () async {
      final fake = await _FakeWiki.start();
      addTearDown(fake.stop);
      final repo = WikiRepository(
        dao: dao,
        client: api.WikiClient(fake.url, 'tok'),
      );

      final p = await repo.createProject('Inbox');
      expect(p.pendingCreate, isTrue);
      expect(p.id, startsWith('local-'));

      final outbox = await dao.allOutbox();
      expect(outbox.length, 1);
      expect(outbox.first.op, 'create_project');
    });

    test('updateBlock uses cached version as If-Match base', () async {
      final fake = await _FakeWiki.start();
      addTearDown(fake.stop);
      final repo = WikiRepository(
        dao: dao,
        client: api.WikiClient(fake.url, 'tok'),
      );

      // Seed a block as if the server had returned it on refresh.
      await dao.upsertBlock(LocalWikiBlock(
        id: 'srv-b1',
        pageId: 'srv-pg1',
        position: 1.0,
        type: 'text',
        contentJson: '{"text":"hi"}',
        version: 3,
        deleted: false,
        updatedAt: DateTime.now().toUtc(),
      ));

      await repo.updateBlock('srv-p1', 'srv-b1', content: {'text': 'edited'});
      final outbox = await dao.allOutbox();
      expect(outbox.length, 1);
      expect(outbox.first.baseVersion, 3);
    });
  });

  group('WikiOutboxFlusher', () {
    test('drains create_project op and rekeys pending children', () async {
      final fake = await _FakeWiki.start();
      addTearDown(fake.stop);
      final client = api.WikiClient(fake.url, 'tok');
      final repo = WikiRepository(dao: dao, client: client);
      final flusher = WikiOutboxFlusher(dao: dao, client: client);
      addTearDown(flusher.dispose);

      final project = await repo.createProject('Inbox');
      await repo.createPage(project.id, title: 'Hello');
      await flusher.flushOnce();

      // Both ops should have drained successfully.
      final outbox = await dao.allOutbox();
      expect(outbox, isEmpty,
          reason: 'flusher should leave the outbox empty on success');
      // The project was rekeyed to a server id.
      final rows = await dao.listProjects();
      expect(rows.first.id, startsWith('srv-'));
      expect(fake.pagesByProject.keys.single, rows.first.id,
          reason: 'create_page 必须打到 rekey 后的项目 id 上 —— flushOnce '
              '开头的 due 快照是旧值，apply 前重读才能拿到新 id');
    });

    test('409 conflict surfaces on the conflicts stream and drops the op',
        () async {
      final fake = await _FakeWiki.start();
      addTearDown(fake.stop);
      // Pre-populate a server-owned block.
      fake.blocksByPage['srv-pg1'] = [
        {
          'id': 'srv-b1',
          'page_id': 'srv-pg1',
          'position': 1.0,
          'type': 'text',
          'content': {'text': 'hi'},
          'version': 7,
        }
      ];
      fake.simulate409 = true;

      final client = api.WikiClient(fake.url, 'tok');
      final repo = WikiRepository(dao: dao, client: client);
      final flusher = WikiOutboxFlusher(dao: dao, client: client);
      addTearDown(flusher.dispose);

      // Mirror the server block locally first.
      await dao.upsertBlock(LocalWikiBlock(
        id: 'srv-b1',
        pageId: 'srv-pg1',
        position: 1.0,
        type: 'text',
        contentJson: '{"text":"hi"}',
        version: 1, // stale on purpose
        deleted: false,
        updatedAt: DateTime.now().toUtc(),
      ));
      await repo.updateBlock('srv-p1', 'srv-b1', content: {'text': 'edited'});

      final conflictFuture = flusher.conflicts.first;
      await flusher.flushOnce();
      final conflict = await conflictFuture.timeout(const Duration(seconds: 2));

      expect(conflict.entityId, 'srv-b1');
      expect(await dao.allOutbox(), isEmpty,
          reason: '409 should drop the conflicting op');
    });

    test('5xx triggers backoff (op stays queued, attempts increments)',
        () async {
      final fake = await _FakeWiki.start();
      addTearDown(fake.stop);
      fake.simulate500 = true;
      final client = api.WikiClient(fake.url, 'tok');
      final repo = WikiRepository(dao: dao, client: client);
      final flusher = WikiOutboxFlusher(dao: dao, client: client);
      addTearDown(flusher.dispose);

      await repo.createProject('Inbox');
      await flusher.flushOnce();

      final outbox = await dao.allOutbox();
      expect(outbox.length, 1);
      expect(outbox.first.attempts, 1);
      expect(outbox.first.lastError, isNotNull);
    });
  });
}

import 'dart:convert';
import 'dart:io';

import 'package:biumind/data/api/wiki_client.dart';
import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_test/flutter_test.dart';

class _FakeWiki {
  _FakeWiki._(this.server);
  final HttpServer server;
  final List<Map<String, dynamic>> projects = [
    {'id': 'p1', 'name': 'Notes'},
  ];
  final Map<String, List<Map<String, dynamic>>> pagesByProject = {};
  final Map<String, List<Map<String, dynamic>>> blocksByPage = {};
  int _nextId = 0;
  final List<String> seenAuth = [];

  static Future<_FakeWiki> start() async {
    final s = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    final fake = _FakeWiki._(s);
    s.listen(fake._handle);
    return fake;
  }

  Uri get url => Uri.parse('http://${server.address.host}:${server.port}');

  Future<void> stop() => server.close(force: true);

  Future<void> _handle(HttpRequest req) async {
    seenAuth.add(req.headers.value(HttpHeaders.authorizationHeader) ?? '');
    final body = await utf8.decoder.bind(req).join();
    final path = req.uri.path;
    final parts = path.split('/').where((s) => s.isNotEmpty).toList();
    Map<String, dynamic>? response;
    int status = 200;

    if (req.method == 'GET' && path == '/v1/wiki/projects') {
      response = {'projects': projects};
    } else if (req.method == 'POST' && path == '/v1/wiki/projects') {
      final j = jsonDecode(body) as Map<String, dynamic>;
      final p = {'id': 'p${++_nextId}', 'name': j['name']};
      projects.add(p);
      response = p;
    } else if (req.method == 'GET' &&
        parts.length == 5 &&
        parts[3] == parts[3] /* projects/{pid}/pages */) {
      final pid = parts[3];
      response = {'pages': pagesByProject[pid] ?? []};
    } else if (req.method == 'POST' && parts.length == 5 && parts[4] == 'pages') {
      final pid = parts[3];
      final j = jsonDecode(body) as Map<String, dynamic>;
      final page = {
        'id': 'pg${++_nextId}',
        'project_id': pid,
        'title': j['title'],
        'version': 1,
        'updated_at': DateTime.now().toUtc().toIso8601String(),
      };
      pagesByProject.putIfAbsent(pid, () => []).add(page);
      response = page;
    } else if (req.method == 'GET' && parts.length == 7 && parts[6] == 'blocks') {
      final pageId = parts[5];
      response = {'blocks': blocksByPage[pageId] ?? []};
    } else if (req.method == 'POST' && parts.length == 7 && parts[6] == 'blocks') {
      final pageId = parts[5];
      final j = jsonDecode(body) as Map<String, dynamic>;
      final blk = {
        'id': 'b${++_nextId}',
        'page_id': pageId,
        'position': j['position'],
        'type': j['type'],
        'content': j['content'],
        'version': 1,
      };
      blocksByPage.putIfAbsent(pageId, () => []).add(blk);
      response = blk;
    } else if (req.method == 'PUT' && parts.length == 5 && parts[4] == 'blocks') {
      // not exercised in this test (path pattern actually has 6 parts);
      // real one is /v1/wiki/projects/{pid}/blocks/{id}
    } else if (req.method == 'PUT' && parts.length == 6 && parts[4] == 'blocks') {
      final blockId = parts[5];
      final ifMatch = req.headers.value('If-Match');
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
      } else if (ifMatch != null && ifMatch != blk['version'].toString()) {
        status = 409;
        response = {'error': 'version_conflict', 'server_version': blk['version']};
      } else {
        final j = jsonDecode(body) as Map<String, dynamic>;
        blk['content'] = j['content'];
        blk['version'] = (blk['version'] as int) + 1;
        response = blk;
      }
    } else {
      status = 404;
      response = {'error': 'unknown_route', 'path': path, 'method': req.method};
    }

    req.response.statusCode = status;
    req.response.headers.contentType = ContentType.json;
    req.response.write(jsonEncode(response));
    await req.response.close();
  }

}

void main() {
  test('list / create / page / blocks happy path', () async {
    final fake = await _FakeWiki.start();
    addTearDown(fake.stop);

    final c = WikiClient(fake.url, 'tok');

    final projects = await c.listProjects();
    expect(projects.length, 1);
    expect(projects.first.name, 'Notes');

    final page = await c.createPage(projects.first.id, title: 'Quantum');
    expect(page.title, 'Quantum');
    expect(page.version, 1);

    final pages = await c.listPages(projects.first.id);
    expect(pages.length, 1);
    expect(pages.first.id, page.id);

    final block = await c.createBlock(
      projects.first.id,
      page.id,
      type: 'heading',
      position: 1.0,
      content: {'text': 'Overview', 'level': 2},
    );
    expect(block.type, 'heading');
    expect(block.content['text'], 'Overview');

    final blocks = await c.listBlocks(projects.first.id, page.id);
    expect(blocks.length, 1);

    expect(fake.seenAuth.first, 'Bearer tok');
  });

  test('updateBlock with If-Match version conflict surfaces 409', () async {
    final fake = await _FakeWiki.start();
    addTearDown(fake.stop);
    final c = WikiClient(fake.url, 'tok');

    final page = await c.createPage('p1', title: 'X');
    final block = await c.createBlock('p1', page.id,
        type: 'text', position: 1.0, content: {'text': 'hi'});

    // Wrong version → 409
    expect(
      () => c.updateBlock(
        'p1',
        block.id,
        content: {'text': 'updated'},
        ifMatchVersion: 99,
      ),
      throwsA(isA<WikiApiError>().having((e) => e.status, 'status', 409)),
    );

    // Right version → success, version bumps to 2.
    final updated = await c.updateBlock(
      'p1',
      block.id,
      content: {'text': 'updated'},
      ifMatchVersion: 1,
    );
    expect(updated.version, 2);
    expect(updated.content['text'], 'updated');
  });
}

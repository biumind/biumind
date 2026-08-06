// Unit + integration tests for SkillClient.
//
// Pattern mirrors memory_client_test.dart: pure-function fromJson
// tests + an in-process HttpServer to exercise the wire.

import 'dart:convert';
import 'dart:io';

import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:biumind/data/api/skill_client.dart';

void main() {
  group('Skill.fromJson', () {
    test('full payload', () {
      final s = Skill.fromJson({
        'id': 'skill_abc',
        'org_id': '11111111-1111-1111-1111-111111111111',
        'owner_id': '22222222-2222-2222-2222-222222222222',
        'identifier': 'code-review',
        'name': 'Code Review',
        'description': 'PR auto-review',
        'source': 'imported',
        'status': 'active',
        'manifest': {
          'version': '1.0.0',
          'license': 'MIT',
          'author': {'name': 'Alice', 'url': 'https://a.dev'},
          'source_url': 'https://example.org/skill.md',
        },
        'content': 'Body content',
        'content_hash': 'abc',
        'paths': ['**/*.go', 'src/**'],
        'permissions': ['sandbox.exec'],
        'zip_file_sha256': 'def',
        'created_at': '2026-05-28T10:00:00Z',
        'updated_at': '2026-05-28T11:00:00Z',
      });
      expect(s.id, 'skill_abc');
      expect(s.identifier, 'code-review');
      expect(s.source, 'imported');
      expect(s.manifest.version, '1.0.0');
      expect(s.manifest.license, 'MIT');
      expect(s.manifest.authorName, 'Alice');
      expect(s.paths, ['**/*.go', 'src/**']);
      expect(s.permissions, ['sandbox.exec']);
      expect(s.zipFileSha256, 'def');
    });

    test('missing optional fields fall back gracefully', () {
      final s = Skill.fromJson({
        'id': 'skill_x',
        'identifier': 'x',
        'name': 'X',
      });
      expect(s.description, '');
      expect(s.source, 'user'); // default
      expect(s.status, 'active');
      expect(s.paths, isEmpty);
      expect(s.permissions, isEmpty);
      expect(s.zipFileSha256, '');
      expect(s.ownerId, isNull);
    });

    test('manifest absent → empty manifest', () {
      final s = Skill.fromJson({'id': 'x', 'identifier': 'y', 'name': 'Y'});
      expect(s.manifest.version, '');
      expect(s.manifest.authorName, '');
    });
  });

  group('SkillResource.fromJson', () {
    test('inline shape', () {
      final r = SkillResource.fromJson(<String, dynamic>{
        'sha256': 'abc',
        'size_bytes': 17,
        'mime_type': 'text/markdown',
        'inline': '- item 1\n- item 2\n',
      });
      expect(r.isInline, isTrue);
      expect(r.isCAS, isFalse);
      expect(r.inline, '- item 1\n- item 2\n');
      expect(r.sizeBytes, 17);
      expect(r.mimeType, 'text/markdown');
    });

    test('CAS shape (no inline body)', () {
      final r = SkillResource.fromJson(<String, dynamic>{
        'sha256': 'deadbeef',
        'size_bytes': 1048576,
        'mime_type': 'application/octet-stream',
      });
      expect(r.isInline, isFalse);
      expect(r.isCAS, isTrue);
      expect(r.sha256, 'deadbeef');
    });

    test('empty shape', () {
      final r = SkillResource.fromJson(<String, dynamic>{});
      expect(r.isInline, isFalse);
      expect(r.isCAS, isFalse);
      expect(r.sizeBytes, 0);
    });
  });

  group('Skill.resources parsing', () {
    test('decodes object map into typed entries', () {
      final s = Skill.fromJson(<String, dynamic>{
        'id': 'skill_x',
        'org_id': 'o1',
        'identifier': 'x',
        'name': 'X',
        'description': 'd',
        'source': 'user',
        'status': 'active',
        'content': 'body',
        'content_hash': 'h',
        'created_at': '2026-05-28T00:00:00Z',
        'updated_at': '2026-05-28T00:00:00Z',
        'resources': {
          'references/notes.md': {
            'sha256': 'a',
            'size_bytes': 12,
            'mime_type': 'text/markdown',
            'inline': '# notes\n',
          },
          'scripts/run.sh': {
            'sha256': 'b',
            'size_bytes': 7,
            'mime_type': 'text/x-shellscript',
            'inline': 'echo x\n',
          },
        },
      });
      expect(s.resources.length, 2);
      expect(s.resources['references/notes.md']!.inline, '# notes\n');
      expect(s.resources['scripts/run.sh']!.mimeType, 'text/x-shellscript');
    });

    test('missing resources field defaults to empty map', () {
      final s = Skill.fromJson(<String, dynamic>{
        'id': 's', 'org_id': 'o', 'identifier': 'x', 'name': 'X',
        'description': '', 'source': 'bundled', 'status': 'active',
        'content': '', 'content_hash': '',
        'created_at': '2026-05-28T00:00:00Z',
        'updated_at': '2026-05-28T00:00:00Z',
      });
      expect(s.resources, isEmpty);
    });

    test('non-object value rejected gracefully', () {
      // Forward-compat — server sends a string instead of an object;
      // we drop the entry rather than crash the page render.
      final s = Skill.fromJson(<String, dynamic>{
        'id': 's', 'org_id': 'o', 'identifier': 'x', 'name': 'X',
        'description': '', 'source': 'user', 'status': 'active',
        'content': '', 'content_hash': '',
        'created_at': '2026-05-28T00:00:00Z',
        'updated_at': '2026-05-28T00:00:00Z',
        'resources': {
          'good.md': {'inline': 'ok'},
          'bad.md': 'unexpected string',
        },
      });
      expect(s.resources.keys, contains('good.md'));
      expect(s.resources.keys, isNot(contains('bad.md')));
    });
  });

  group('AgentSkill.fromJson', () {
    test('roundtrips', () {
      final as = AgentSkill.fromJson({
        'agent_id': 'a-uuid', 'skill_id': 'skill_x',
        'is_enabled': true, 'pinned': true,
        'added_at': '2026-05-28T10:00:00Z',
      });
      expect(as.agentId, 'a-uuid');
      expect(as.isEnabled, isTrue);
      expect(as.pinned, isTrue);
    });

    test('defaults when fields absent', () {
      final as = AgentSkill.fromJson({});
      expect(as.isEnabled, isFalse);
      expect(as.pinned, isFalse);
    });
  });

  group('SkillClient end-to-end against in-process HttpServer', () {
    late HttpServer server;
    late Uri base;
    final received = <HttpRequest>[];

    setUp(() async {
      received.clear();
      server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
      base = Uri.parse('http://localhost:${server.port}');
      _serve(server, received, _scriptedResponses);
    });

    tearDown(() async {
      _scriptedResponses.clear();
      await server.close(force: true);
    });

    test('list passes filters as query params', () async {
      _scriptedResponses.add((r) async {
        r.response.headers.contentType = ContentType.json;
        r.response.write(jsonEncode({'skills': const []}));
        await r.response.close();
      });
      final c = SkillClient(base, 'tok');
      await c.list(source: 'imported', status: 'active');
      final got = received.single.uri;
      expect(got.path, '/v1/skills');
      expect(got.queryParameters['source'], 'imported');
      expect(got.queryParameters['status'], 'active');
      expect(received.single.headers.value('Authorization'), 'Bearer tok');
    });

    test('list parses array of skills', () async {
      _scriptedResponses.add((r) async {
        r.response.write(jsonEncode({
          'skills': [
            {'id': 'skill_a', 'identifier': 'alpha', 'name': 'Alpha', 'status': 'active'},
            {'id': 'skill_b', 'identifier': 'beta', 'name': 'Beta', 'status': 'active'},
          ]
        }));
        await r.response.close();
      });
      final c = SkillClient(base, 'tok');
      final rows = await c.list();
      expect(rows, hasLength(2));
      expect(rows[0].identifier, 'alpha');
    });

    test('installInline POSTs JSON body with required fields', () async {
      String? capturedBody;
      _scriptedResponses.add((r) async {
        capturedBody = await utf8.decoder.bind(r).join();
        r.response.write(jsonEncode({
          'id': 'skill_x', 'identifier': 'hello', 'name': 'Hello',
          'status': 'active', 'content_hash': 'h'
        }));
        await r.response.close();
      });
      final c = SkillClient(base, 'tok');
      final s = await c.installInline(
        identifier: 'hello', name: 'Hello',
        description: 'greet', body: r'Hi $ARGS',
      );
      expect(s.id, 'skill_x');
      final body = jsonDecode(capturedBody!) as Map<String, dynamic>;
      expect(body['identifier'], 'hello');
      expect(body['name'], 'Hello');
      expect(received.single.method, 'POST');
    });

    test('installFromUrl includes url + omits inline fields', () async {
      String? capturedBody;
      _scriptedResponses.add((r) async {
        capturedBody = await utf8.decoder.bind(r).join();
        r.response.write(jsonEncode({
          'id': 'skill_x', 'identifier': 'remote', 'name': 'Remote',
          'status': 'active', 'content_hash': 'h'
        }));
        await r.response.close();
      });
      final c = SkillClient(base, 'tok');
      await c.installFromUrl('https://example.org/skill.md');
      final body = jsonDecode(capturedBody!) as Map<String, dynamic>;
      expect(body['url'], 'https://example.org/skill.md');
      expect(body.containsKey('identifier'), isFalse);
      expect(body.containsKey('body'), isFalse);
    });

    test('toggle PATCH-style POST hits correct path', () async {
      _scriptedResponses.add((r) async {
        r.response.write(jsonEncode({
          'agent_id': 'a-uuid', 'skill_id': 'skill_x',
          'is_enabled': true, 'pinned': false,
          'added_at': '2026-05-28T10:00:00Z',
        }));
        await r.response.close();
      });
      final c = SkillClient(base, 'tok');
      await c.toggle('skill_x', agentId: 'a-uuid', isEnabled: true);
      expect(received.single.uri.path, '/v1/skills/skill_x/toggle');
      expect(received.single.method, 'POST');
    });

    test('delete uses DELETE method', () async {
      _scriptedResponses.add((r) async {
        r.response.write(jsonEncode({'id': 'skill_x'}));
        await r.response.close();
      });
      final c = SkillClient(base, 'tok');
      await c.delete('skill_x');
      expect(received.single.method, 'DELETE');
      expect(received.single.uri.path, '/v1/skills/skill_x');
    });

    test('non-2xx → SkillApiError with status', () async {
      _scriptedResponses.add((r) async {
        r.response.statusCode = 409;
        r.response.write(jsonEncode({
          'error': {'code': 'identifier_taken', 'message': 'already in use'}
        }));
        await r.response.close();
      });
      final c = SkillClient(base, 'tok');
      try {
        await c.installInline(identifier: 'x', name: 'X', description: 'd', body: 'b');
        fail('expected SkillApiError');
      } on SkillApiError catch (e) {
        expect(e.status, 409);
        expect(e.isConflict, isTrue);
        expect(e.body, contains('identifier_taken'));
      }
    });

    test('413 → isTooLarge', () async {
      _scriptedResponses.add((r) async {
        r.response.statusCode = 413;
        r.response.write('too big');
        await r.response.close();
      });
      final c = SkillClient(base, 'tok');
      try {
        await c.installFromUrl('https://x');
        fail('expected SkillApiError');
      } on SkillApiError catch (e) {
        expect(e.isTooLarge, isTrue);
      }
    });
  });

  group('ProposeResult', () {
    test('parses skill + previous version when update_of present', () {
      final j = <String, dynamic>{
        'id': 'skill_new',
        'org_id': 'o1',
        'identifier': 'foo',
        'name': 'Foo',
        'description': 'd',
        'source': 'user',
        'status': 'staged',
        'content': 'NEW BODY',
        'content_hash': 'hash_new_v2',
        'created_at': '2026-05-28T00:00:00Z',
        'updated_at': '2026-05-28T00:00:00Z',
        'update_of': {
          'id': 'skill_old',
          'identifier': 'foo',
          'content': 'OLD BODY',
          'content_hash': 'hash_old_v1',
        },
      };
      final r = ProposeResult.fromJson(j);
      expect(r.skill.id, 'skill_new');
      expect(r.skill.contentHash, 'hash_new_v2');
      expect(r.previous, isNotNull);
      expect(r.previous!.contentHash, 'hash_old_v1');
      expect(r.previous!.content, 'OLD BODY');
      // Diff summary one-liner — what the page can show without
      // building a full diff UI.
      final summary = r.previous!.diffSummary(r.skill.contentHash);
      expect(summary, contains('hash_old_v1'));
      expect(summary, contains('hash_new_v2'));
      expect(summary, contains('→'));
    });

    test('previous is null on a fresh propose', () {
      final j = <String, dynamic>{
        'id': 'skill_x',
        'org_id': 'o1',
        'identifier': 'x',
        'name': 'X',
        'description': '',
        'source': 'user',
        'status': 'staged',
        'content': 'b',
        'content_hash': 'h',
        'created_at': '2026-05-28T00:00:00Z',
        'updated_at': '2026-05-28T00:00:00Z',
      };
      final r = ProposeResult.fromJson(j);
      expect(r.skill.id, 'skill_x');
      expect(r.previous, isNull);
    });

    test('diffSummary returns empty when either hash missing', () {
      final p = PreviousSkillVersion(
          id: 'a', identifier: 'a', content: '', contentHash: '');
      expect(p.diffSummary('newhash'), '');
      final p2 = PreviousSkillVersion(
          id: 'a', identifier: 'a', content: '', contentHash: 'oldhash');
      expect(p2.diffSummary(''), '');
    });
  });
}

// ─── Tiny scripted-response harness ─────────────────────

final _scriptedResponses = <Future<void> Function(HttpRequest)>[];

void _serve(
  HttpServer s,
  List<HttpRequest> received,
  List<Future<void> Function(HttpRequest)> script,
) {
  s.listen((req) async {
    received.add(req);
    if (script.isEmpty) {
      req.response.statusCode = 500;
      await req.response.close();
      return;
    }
    final h = script.removeAt(0);
    await h(req);
  });
}

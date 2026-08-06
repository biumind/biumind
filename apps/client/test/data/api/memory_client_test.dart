// Unit tests for MemoryClient JSON parsing & request building.
//
// We don't make real HTTP calls; the parsing is the meat of the
// client surface and the rest is `dart:io` plumbing already covered
// by graph_client / wiki_client tests. We DO end-to-end the encoder
// path via a mock HttpServer for one happy-path flow so query
// parameters and headers are exercised.

import 'dart:convert';
import 'dart:io';

import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:biumind/data/api/memory_client.dart';

void main() {
  group('Memory.fromJson', () {
    test('full payload roundtrips', () {
      final m = Memory.fromJson({
        'id': '11111111-1111-1111-1111-111111111111',
        'project_id': '22222222-2222-2222-2222-222222222222',
        'kind': 'preference',
        'content': 'prefers concise replies',
        'salience': 0.7,
        'last_accessed_at': '2026-05-24T10:00:00Z',
        'created_at': '2026-05-24T09:00:00Z',
        'score': 1.5,
      });
      expect(m.id, '11111111-1111-1111-1111-111111111111');
      expect(m.kind, 'preference');
      expect(m.content, 'prefers concise replies');
      expect(m.salience, closeTo(0.7, 1e-6));
      expect(m.score, closeTo(1.5, 1e-6));
      expect(m.lastAccessedAt.toUtc().hour, 10);
    });

    test('missing optional fields fall back gracefully', () {
      final m = Memory.fromJson({
        'id': 'a',
        'project_id': 'b',
      });
      expect(m.kind, 'recall'); // default
      expect(m.content, '');
      expect(m.salience, closeTo(0.5, 1e-6));
      expect(m.score, isNull);
    });
  });

  group('RecallResult.fromJson', () {
    test('parses hybrid mode', () {
      final r = RecallResult.fromJson({
        'memories': [
          {'id': '1', 'project_id': 'p', 'content': 'a', 'score': 2.0},
        ],
        'mode': 'hybrid',
        'query': 'vim',
      });
      expect(r.memories.single.content, 'a');
      expect(r.mode, RecallMode.hybrid);
      expect(r.query, 'vim');
    });

    test('parses lexical mode', () {
      final r = RecallResult.fromJson({
        'memories': const [],
        'mode': 'lexical',
        'query': 'x',
      });
      expect(r.mode, RecallMode.lexical);
      expect(r.memories, isEmpty);
    });

    test('unknown mode falls back to .unknown', () {
      final r = RecallResult.fromJson({'mode': 'martian', 'query': 'q'});
      expect(r.mode, RecallMode.unknown);
    });
  });

  group('MemoryClient end-to-end against in-process HttpServer', () {
    late HttpServer server;
    late Uri base;
    final List<HttpRequest> received = [];

    setUp(() async {
      received.clear();
      server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
      base = Uri.parse('http://localhost:${server.port}');
      // Spin up handler in background; each test plants the response.
      _serve(server, received, _scriptedResponses);
    });

    tearDown(() async {
      _scriptedResponses.clear();
      await server.close(force: true);
    });

    test('store sends JSON body and parses response', () async {
      _scriptedResponses.add((HttpRequest r) async {
        r.response.headers.contentType = ContentType.json;
        r.response.write(jsonEncode({
          'id': 'mid',
          'project_id': 'pid',
          'kind': 'recall',
          'content': 'hi',
          'salience': 0.5,
        }));
        await r.response.close();
      });

      final c = MemoryClient(base, 'tok');
      final m = await c.store(projectId: 'pid', content: 'hi');
      expect(m.id, 'mid');
      expect(received.single.method, 'POST');
      expect(received.single.uri.path, '/v1/memory');
      expect(received.single.headers.value('Authorization'), 'Bearer tok');
    });

    test('list passes project_id + kind + limit as query params', () async {
      _scriptedResponses.add((HttpRequest r) async {
        r.response.headers.contentType = ContentType.json;
        r.response.write(jsonEncode({'memories': const []}));
        await r.response.close();
      });

      final c = MemoryClient(base, 'tok');
      await c.list(projectId: 'pid-1', kind: 'preference', limit: 25);
      final got = received.single.uri;
      expect(got.path, '/v1/memory');
      expect(got.queryParameters['project_id'], 'pid-1');
      expect(got.queryParameters['kind'], 'preference');
      expect(got.queryParameters['limit'], '25');
    });

    test('recall returns RecallResult with mode chip metadata', () async {
      _scriptedResponses.add((HttpRequest r) async {
        r.response.write(jsonEncode({
          'memories': [{'id': 'm', 'project_id': 'p', 'content': 'vim ftw', 'score': 2.39}],
          'mode': 'hybrid',
          'query': 'vim',
        }));
        await r.response.close();
      });

      final c = MemoryClient(base, 'tok');
      final res = await c.recall(projectId: 'p', query: 'vim');
      expect(res.mode, RecallMode.hybrid);
      expect(res.memories.single.score, closeTo(2.39, 1e-3));
      expect(received.single.uri.queryParameters['q'], 'vim');
    });

    test('recall rejects empty query before hitting the wire', () async {
      final c = MemoryClient(base, 'tok');
      expect(() => c.recall(projectId: 'p', query: '   '),
          throwsA(isA<ArgumentError>()));
      expect(received, isEmpty);
    });

    test('store rejects invalid kind before hitting the wire', () async {
      final c = MemoryClient(base, 'tok');
      expect(
        () => c.store(projectId: 'p', content: 'x', kind: 'martian'),
        throwsA(isA<ArgumentError>()),
      );
      expect(received, isEmpty);
    });

    test('store rewrites deprecated "skill" alias to "habit" on the wire',
        () async {
      String? capturedBody;
      _scriptedResponses.add((HttpRequest r) async {
        capturedBody = await utf8.decoder.bind(r).join();
        r.response.statusCode = 200;
        r.response.write(jsonEncode({
          'id': 'm1',
          'project_id': 'p',
          'kind': 'habit',
          'content': 'x',
          'salience': 0.5,
          'last_accessed_at': '2026-05-27T00:00:00Z',
          'created_at': '2026-05-27T00:00:00Z',
        }));
        await r.response.close();
      });
      final c = MemoryClient(base, 'tok');
      // Caller still passes 'skill' for back-compat; client must
      // canonicalise to 'habit' before sending so the server's CHECK
      // (which no longer accepts 'skill') doesn't reject it.
      await c.store(projectId: 'p', content: 'x', kind: 'skill');
      final body = jsonDecode(capturedBody!) as Map<String, dynamic>;
      expect(body['kind'], 'habit');
    });

    test('list canonicalises deprecated "skill" filter to "habit"', () async {
      _scriptedResponses.add((HttpRequest r) async {
        r.response.statusCode = 200;
        r.response.write(jsonEncode({'memories': []}));
        await r.response.close();
      });
      final c = MemoryClient(base, 'tok');
      await c.list(projectId: 'p', kind: 'skill');
      expect(received.single.uri.queryParameters['kind'], 'habit');
    });

    test('isAcceptedMemoryKind admits canonical + deprecated alias', () {
      expect(isAcceptedMemoryKind('recall'), isTrue);
      expect(isAcceptedMemoryKind('preference'), isTrue);
      expect(isAcceptedMemoryKind('habit'), isTrue);
      expect(isAcceptedMemoryKind('skill'), isTrue); // 90-day alias
      expect(isAcceptedMemoryKind('martian'), isFalse);
    });

    test('normalizeMemoryKind rewrites only the deprecated alias', () {
      expect(normalizeMemoryKind('habit'), 'habit');
      expect(normalizeMemoryKind('skill'), 'habit');
      expect(normalizeMemoryKind('recall'), 'recall');
      expect(normalizeMemoryKind('preference'), 'preference');
    });

    test('non-2xx surfaces as MemoryApiError', () async {
      _scriptedResponses.add((HttpRequest r) async {
        r.response.statusCode = 403;
        r.response.write(jsonEncode({
          'error': {'code': 'forbidden', 'message': 'no'},
        }));
        await r.response.close();
      });

      final c = MemoryClient(base, 'tok');
      try {
        await c.list(projectId: 'p');
        fail('expected error');
      } on MemoryApiError catch (e) {
        expect(e.status, 403);
        expect(e.isForbidden, isTrue);
      }
    });

    test('delete sends DELETE + memory id in path', () async {
      _scriptedResponses.add((HttpRequest r) async {
        r.response.write(jsonEncode({'deleted': 'mid'}));
        await r.response.close();
      });

      final c = MemoryClient(base, 'tok');
      await c.delete('mid');
      expect(received.single.method, 'DELETE');
      expect(received.single.uri.path, '/v1/memory/mid');
    });
  });
}

// ─── tiny scripted-response harness ─────────────────────

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

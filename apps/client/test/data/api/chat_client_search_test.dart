// ChatClient.searchMessages — wire shape + DTO parsing.
// Uses an in-process HttpServer to capture the outgoing request and
// confirm field placement (POST body, not query params; ISO8601 dates
// in UTC; thread_id only sent when scoped).

import 'dart:convert';
import 'dart:io';

import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:biumind/data/api/chat_client.dart';

class _Capture {
  String? body;
  String? authHeader;
  String? path;
}

Future<HttpServer> _spinFakeBrain(_Capture cap, Map<String, dynamic> response,
    {int status = 200}) async {
  final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
  server.listen((req) async {
    cap.path = req.uri.path;
    cap.authHeader = req.headers.value('authorization');
    cap.body = await utf8.decoder.bind(req).join();
    req.response.statusCode = status;
    req.response.headers.contentType = ContentType.json;
    req.response.write(jsonEncode(response));
    await req.response.close();
  });
  return server;
}

void main() {
  group('ChatClient.searchMessages', () {
    test('builds correct POST body and parses hits', () async {
      final cap = _Capture();
      final server = await _spinFakeBrain(cap, {
        'hits': [
          {
            'message_id': 'm1',
            'thread_id': 't1',
            'thread_title': 'Project alpha',
            'role': 'assistant',
            'snippet': '...<mark>vector</mark> index...',
            'score': 0.42,
            'created_at': '2026-04-01T12:00:00Z',
          }
        ],
        'total': 1,
        'took_ms': 17,
        'query': 'vector',
      });
      final url = Uri.parse('http://${server.address.host}:${server.port}');
      final client = ChatClient(url, 'tok-xyz');

      final result = await client.searchMessages(
        query: 'vector',
        threadId: 't1',
        role: 'assistant',
        fromTime: DateTime.utc(2026, 1, 1),
        toTime: DateTime.utc(2026, 12, 31),
        limit: 10,
        offset: 0,
        highlight: true,
      );

      // Wire shape
      expect(cap.path, '/v1/chat/search');
      expect(cap.authHeader, 'Bearer tok-xyz');
      final sent = jsonDecode(cap.body!) as Map<String, dynamic>;
      expect(sent['query'], 'vector');
      expect(sent['thread_id'], 't1');
      expect(sent['role'], 'assistant');
      expect(sent['from'], '2026-01-01T00:00:00.000Z');
      expect(sent['to'], '2026-12-31T00:00:00.000Z');
      expect(sent['limit'], 10);
      expect(sent['offset'], 0);
      expect(sent['highlight'], true);

      // DTO parsing
      expect(result.total, 1);
      expect(result.tookMs, 17);
      expect(result.query, 'vector');
      expect(result.hits, hasLength(1));
      final h = result.hits.first;
      expect(h.messageId, 'm1');
      expect(h.threadId, 't1');
      expect(h.threadTitle, 'Project alpha');
      expect(h.role, 'assistant');
      expect(h.snippet, contains('<mark>'));
      expect(h.score, closeTo(0.42, 1e-9));
      expect(h.createdAt.year, 2026);

      await server.close(force: true);
    });

    test('thread_id absent when not scoped (cross-thread search)', () async {
      final cap = _Capture();
      final server = await _spinFakeBrain(cap,
          {'hits': [], 'total': 0, 'took_ms': 1, 'query': 'x'});
      final url = Uri.parse('http://${server.address.host}:${server.port}');
      final client = ChatClient(url, 'tok');

      await client.searchMessages(query: 'x');
      final sent = jsonDecode(cap.body!) as Map<String, dynamic>;
      expect(sent.containsKey('thread_id'), isFalse,
          reason: 'thread_id must be omitted when not scoping to one thread');
      expect(sent.containsKey('role'), isFalse);
      expect(sent.containsKey('from'), isFalse);
      expect(sent.containsKey('to'), isFalse);

      await server.close(force: true);
    });

    test('empty hits list parses to empty list, not null', () async {
      final cap = _Capture();
      final server = await _spinFakeBrain(cap,
          {'hits': [], 'total': 0, 'took_ms': 0, 'query': 'nope'});
      final url = Uri.parse('http://${server.address.host}:${server.port}');
      final client = ChatClient(url, 'tok');

      final r = await client.searchMessages(query: 'nope');
      expect(r.hits, isEmpty);
      expect(r.total, 0);

      await server.close(force: true);
    });

    test('missing optional fields default safely', () async {
      final cap = _Capture();
      final server = await _spinFakeBrain(cap, {
        'hits': [
          {
            // No thread_title / no score / no created_at — all should default
            'message_id': 'm-bare',
            'thread_id': 't-bare',
            'role': 'user',
            'snippet': 'plain',
          }
        ],
        'total': 1,
        'took_ms': 0,
        'query': 'q',
      });
      final url = Uri.parse('http://${server.address.host}:${server.port}');
      final client = ChatClient(url, 'tok');
      final r = await client.searchMessages(query: 'q');
      final h = r.hits.single;
      expect(h.threadTitle, '');
      expect(h.score, 0);
      expect(h.createdAt.millisecondsSinceEpoch, 0);

      await server.close(force: true);
    });
  });
}

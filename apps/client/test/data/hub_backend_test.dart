import 'dart:convert';
import 'dart:io';

import 'package:biumind/core/ai/ai_surface.dart';
import 'package:biumind/data/api/hub_backend.dart';
import 'package:flutter_test/flutter_test.dart';

/// Spawns a local HttpServer that emits a scripted SSE response when
/// /v1/messages is hit. Caller passes the script lines (already formatted SSE).
class _FakeHub {
  final HttpServer server;
  final List<String> seenAuthHeaders = [];
  final List<Map<String, dynamic>> seenBodies = [];
  _FakeHub._(this.server);

  static Future<_FakeHub> start({required List<String> sseScript}) async {
    final s = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    final fake = _FakeHub._(s);
    s.listen((req) async {
      if (req.uri.path != '/v1/messages') {
        req.response.statusCode = 404;
        await req.response.close();
        return;
      }
      fake.seenAuthHeaders.add(req.headers.value(HttpHeaders.authorizationHeader) ?? '');
      final raw = await utf8.decoder.bind(req).join();
      try {
        fake.seenBodies.add(jsonDecode(raw) as Map<String, dynamic>);
      } catch (_) {/* tolerate parse errors */}
      req.response.statusCode = 200;
      req.response.headers.contentType = ContentType('text', 'event-stream');
      for (final line in sseScript) {
        req.response.write(line);
      }
      await req.response.close();
    });
    return fake;
  }

  Uri get url => Uri.parse('http://${server.address.host}:${server.port}');

  Future<void> stop() async {
    await server.close(force: true);
  }
}

String _sse(String event, Object data) {
  final body = data is String ? data : jsonEncode(data);
  return 'event: $event\ndata: $body\n\n';
}

void main() {
  test('streams text deltas as AiChunks', () async {
    final fake = await _FakeHub.start(sseScript: [
      _sse('delta', {'text': 'Hello'}),
      _sse('delta', {'text': ', world'}),
      _sse('stop', {'reason': 'end_turn'}),
      _sse('end', {}),
    ]);
    addTearDown(fake.stop);

    final backend = RelayBackend(HubConfig(endpoint: fake.url, bearerToken: 'tok'));
    final chunks = <AiChunk>[];
    await for (final c in backend.invoke(const AiInvocation(
      intent: 'chat', input: 'hi', surface: AiSurfaceKind.chat,
    ))) {
      chunks.add(c);
    }
    final text = chunks
        .where((c) => c.kind == AiChunkKind.text)
        .map((c) => c.text)
        .join();
    expect(text, 'Hello, world');
    expect(chunks.last.kind, AiChunkKind.done);
    expect(fake.seenAuthHeaders.first, 'Bearer tok');
    final body = fake.seenBodies.first;
    expect(body['model'], 'claude-sonnet-4-6');
    expect(body['stream'], true);
    expect(body['system'], contains('BiuMind'));
    expect((body['messages'] as List).first['content'], 'hi');
  });

  test('emits AiChunk.error on 4xx', () async {
    final s = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    s.listen((req) async {
      req.response.statusCode = 401;
      req.response.write('not allowed');
      await req.response.close();
    });
    addTearDown(() async => s.close(force: true));

    final backend = RelayBackend(HubConfig(
      endpoint: Uri.parse('http://${s.address.host}:${s.port}'),
      bearerToken: 'tok',
    ));
    final chunks = <AiChunk>[];
    await for (final c in backend.invoke(const AiInvocation(
      intent: 'chat', input: 'x', surface: AiSurfaceKind.chat,
    ))) {
      chunks.add(c);
    }
    expect(chunks.length, 1);
    expect(chunks.first.kind, AiChunkKind.error);
    expect(chunks.first.error, contains('401'));
  });

  test('translates tool_call_* events', () async {
    final fake = await _FakeHub.start(sseScript: [
      _sse('delta', {'text': 'Reading...'}),
      _sse('tool_call_start', {'id': 'tc1', 'name': 'read'}),
      _sse('tool_call_args', {'id': 'tc1', 'delta': '{"path":"x"}'}),
      _sse('tool_call_end', {'id': 'tc1'}),
      _sse('end', {}),
    ]);
    addTearDown(fake.stop);

    final backend = RelayBackend(HubConfig(endpoint: fake.url, bearerToken: 'tok'));
    final chunks = <AiChunk>[];
    await for (final c in backend.invoke(const AiInvocation(
      intent: 'chat', input: 'use read', surface: AiSurfaceKind.chat,
    ))) {
      chunks.add(c);
    }
    final kinds = chunks.map((c) => c.kind).toList();
    expect(kinds, [
      AiChunkKind.text,
      AiChunkKind.toolCallStart,
      AiChunkKind.toolCallArgs,
      AiChunkKind.toolCallEnd,
      AiChunkKind.done,
    ]);
    expect(chunks[1].toolName, 'read');
    expect(chunks[2].text, '{"path":"x"}');
  });

  test('uses surface-specific model + system override', () async {
    final fake = await _FakeHub.start(sseScript: [_sse('end', {})]);
    addTearDown(fake.stop);

    final backend = RelayBackend(HubConfig(
      endpoint: fake.url,
      bearerToken: 'tok',
      modelOverrides: {AiSurfaceKind.translate: 'gpt-4o-mini'},
      systemOverrides: {AiSurfaceKind.translate: 'You translate.'},
    ));
    await backend
        .invoke(const AiInvocation(
            intent: 'translate', input: '你好', surface: AiSurfaceKind.translate))
        .drain<void>();

    final body = fake.seenBodies.first;
    expect(body['model'], 'gpt-4o-mini');
    expect(body['system'], 'You translate.');
  });

  test('respects AiPolicy.preferModel', () async {
    final fake = await _FakeHub.start(sseScript: [_sse('end', {})]);
    addTearDown(fake.stop);

    final backend = RelayBackend(HubConfig(endpoint: fake.url, bearerToken: 'tok'));
    await backend
        .invoke(const AiInvocation(
          intent: 'chat',
          input: 'hi',
          surface: AiSurfaceKind.chat,
          policy: AiPolicy(preferModel: 'claude-haiku-4-5'),
        ))
        .drain<void>();
    final body = fake.seenBodies.first;
    expect(body['model'], 'claude-haiku-4-5');
  });
}

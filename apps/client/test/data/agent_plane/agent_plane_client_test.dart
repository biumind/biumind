// AgentPlaneClient 端到端 HTTP 测试 —— in-process HttpServer，跟 brain
// agentplane API 形状对齐。覆盖 list / create session / refresh token /
// 错误响应。

import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:biumind/data/agent_plane/agent_plane_client.dart';
import 'package:biumind/data/api/_http_helpers.dart';

void main() {
  late HttpServer server;
  late Uri base;
  late List<HttpRequest> received;
  late List<Future<void> Function(HttpRequest)> scripted;

  setUp(() async {
    received = [];
    scripted = [];
    server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    server.listen((req) async {
      received.add(req);
      if (scripted.isEmpty) {
        req.response.statusCode = 500;
        await req.response.close();
        return;
      }
      await scripted.removeAt(0)(req);
    });
    base = Uri.parse('http://127.0.0.1:${server.port}');
  });

  tearDown(() async {
    await server.close(force: true);
  });

  AgentPlaneClient newClient() {
    return AgentPlaneClient(
      baseUrl: base.toString(),
      tokenProvider: () async => 'test-tok',
    );
  }

  test('listEnvironments parses response + sends Bearer', () async {
    scripted.add((req) async {
      expect(req.method, 'GET');
      expect(req.uri.path, '/v1/agent/environments');
      expect(req.headers.value('authorization'), 'Bearer test-tok');
      req.response.statusCode = 200;
      req.response.headers.contentType = ContentType.json;
      req.response.write(jsonEncode({
        'environments': [
          {
            'environment_id': 'e-1',
            'worker_kind': 'biu_daemon',
            'machine_name': 'mbp',
            'os_arch': 'darwin/arm64',
            'capabilities': ['sandbox'],
            'state': 'online',
            'last_seen_at': '2026-05-30T10:00:00Z',
          },
          {
            'environment_id': 'e-2',
            'worker_kind': 'runtime',
            'machine_name': 'pool-runtime-1',
            'pool_tag': 'runtime-prod',
            'state': 'offline',
          },
        ],
      }));
      await req.response.close();
    });

    final c = newClient();
    final envs = await c.listEnvironments();
    expect(envs.length, 2);
    expect(envs[0].environmentId, 'e-1');
    expect(envs[0].isOnline, true);
    expect(envs[0].capabilities, ['sandbox']);
    expect(envs[1].state, 'offline');
    expect(envs[1].poolTag, 'runtime-prod');
  });

  test('listEnvironments(state=online) passes query param', () async {
    scripted.add((req) async {
      expect(req.uri.query, 'state=online');
      req.response.statusCode = 200;
      req.response.write(jsonEncode({'environments': []}));
      await req.response.close();
    });

    final c = newClient();
    final envs = await c.listEnvironments(state: 'online');
    expect(envs, isEmpty);
  });

  test('createSession returns session_id + session_token', () async {
    scripted.add((req) async {
      expect(req.uri.path, '/v1/agent/sessions');
      final body = jsonDecode(await utf8.decoder.bind(req).join());
      expect(body['mode'], 'agent');
      expect(body['environment_id'], 'e-1');
      expect(body['prompt'], 'hello');
      req.response.statusCode = 201;
      req.response.headers.contentType = ContentType.json;
      req.response.write(jsonEncode({
        'session_id': 's-1',
        'session_token': 'sess-tok',
        'expires_at': '2026-05-30T11:00:00Z',
        'mode': 'agent',
        'environment_id': 'e-1',
        'jetstream_subject_in': 'biu.session.s-1.in',
        'jetstream_subject_out': 'biu.session.s-1.out',
      }));
      await req.response.close();
    });

    final c = newClient();
    final resp = await c.createSession(
      mode: 'agent',
      environmentId: 'e-1',
      prompt: 'hello',
    );
    expect(resp.sessionId, 's-1');
    expect(resp.sessionToken, 'sess-tok');
    expect(resp.mode, 'agent');
    expect(resp.environmentId, 'e-1');
    expect(resp.jetstreamSubjectOut, 'biu.session.s-1.out');
  });

  test('createSession(mode=task pool) sends pool_tag', () async {
    scripted.add((req) async {
      final body = jsonDecode(await utf8.decoder.bind(req).join())
          as Map<String, dynamic>;
      expect(body['mode'], 'task');
      expect(body['pool_tag'], 'runtime-prod');
      req.response.statusCode = 201;
      req.response.write(jsonEncode({
        'session_id': 's-2',
        'session_token': 'tok',
        'mode': 'task',
        'jetstream_subject_in': '',
        'jetstream_subject_out': '',
      }));
      await req.response.close();
    });

    final c = newClient();
    final resp = await c.createSession(
      mode: 'task',
      poolTag: 'runtime-prod',
      prompt: 'task prompt',
    );
    expect(resp.sessionId, 's-2');
  });

  test('refreshSessionToken returns new token', () async {
    scripted.add((req) async {
      expect(req.uri.path, '/v1/agent/sessions/s-1/refresh-token');
      req.response.statusCode = 200;
      req.response.write(jsonEncode({
        'session_token': 'fresh-tok',
        'expires_at': '2026-05-30T12:00:00Z',
      }));
      await req.response.close();
    });

    final c = newClient();
    final resp = await c.refreshSessionToken('s-1');
    expect(resp.sessionToken, 'fresh-tok');
  });

  test('createSession surfaces 503 no_runtime_available as ApiError', () async {
    scripted.add((req) async {
      req.response.statusCode = 503;
      req.response.write(jsonEncode({
        'error': {'code': 'no_runtime_available', 'message': 'pool empty'},
      }));
      await req.response.close();
    });

    final c = newClient();
    try {
      await c.createSession(mode: 'task', prompt: 'x');
      fail('expected ApiError');
    } on ApiError catch (e) {
      expect(e.status, 503);
      expect(e.body, contains('no_runtime_available'));
    }
  });

  test('listEnvironments propagates 401 ApiError', () async {
    scripted.add((req) async {
      req.response.statusCode = 401;
      req.response.write('unauthorized');
      await req.response.close();
    });

    final c = newClient();
    try {
      await c.listEnvironments();
      fail('expected ApiError');
    } on ApiError catch (e) {
      expect(e.status, 401);
    }
  });

  test('resumeSession refreshes token and packs into ResumeResp (S9-1)', () async {
    // resumeSession 内部调 refreshSessionToken；mock 该端点
    scripted.add((req) async {
      expect(req.uri.path, '/v1/agent/sessions/s-existing/refresh-token');
      req.response.statusCode = 200;
      req.response.write(jsonEncode({
        'session_token': 'fresh-after-resume',
        'expires_at': '2026-12-31T00:00:00Z',
      }));
      await req.response.close();
    });

    final c = newClient();
    final resume = await c.resumeSession('s-existing');
    expect(resume.sessionId, 's-existing');
    expect(resume.sessionToken, 'fresh-after-resume');
    expect(resume.sinceSeq, 0, reason: 'default to full replay');
  });

  test('resumeSession honors explicit sinceSeq', () async {
    scripted.add((req) async {
      req.response.statusCode = 200;
      req.response.write(jsonEncode({
        'session_token': 'tok',
      }));
      await req.response.close();
    });

    final c = newClient();
    final resume = await c.resumeSession('s-existing', sinceSeq: 1234);
    expect(resume.sinceSeq, 1234);
  });
}

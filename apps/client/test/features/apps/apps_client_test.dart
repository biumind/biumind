// apps_client 契约测试（Repo Apps M1.14）—— 补建：apps_client.dart 文件
// 头注释一直声称本文件存在，实际缺失。
//
// 用 in-process HttpServer 当 mock HTTP 层（模式照
// test/data/api/billing_error_hook_test.dart），pin 住：
//   - 请求路径 / 方法 / body shape（与服务端契约 1:1）
//   - 响应 snake_case 解析（服务端新 struct 带 json tag，客户端无双 key
//     fallback 债）
//   - 错误统一 {"error":{"code","message"}} → ApiError

import 'dart:convert';
import 'dart:io';

import 'package:biumind/data/api/_http_helpers.dart';
import 'package:biumind/data/api/apps_client.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  late HttpServer server;
  late AppsClient client;

  /// 每次请求记录 (method, path, rawBody)。
  final hits = <(String, String, String)>[];

  /// path → (status, body)。测试在发出请求前注册。
  final routes = <String, (int, String)>{};

  setUp(() async {
    hits.clear();
    routes.clear();
    server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    final base = Uri.parse('http://${server.address.host}:${server.port}');
    client = AppsClient(base);
    server.listen((req) async {
      final body = await utf8.decoder.bind(req).join();
      hits.add((req.method, req.uri.path, body));
      final route = routes[req.uri.path];
      req.response.statusCode = route?.$1 ?? 404;
      req.response.headers.contentType = ContentType.json;
      req.response.write(route?.$2 ?? '{"error":{"code":"not_found","message":"no route"}}');
      await req.response.close();
    });
  });

  tearDown(() async {
    await server.close(force: true);
  });

  const analysisJson = '''
{
  "manifest_draft": {
    "identifier": "openmontage",
    "title": "OpenMontage",
    "description": "video montage service",
    "icon": "🎬",
    "version": "1.2.0",
    "category": "content"
  },
  "stack": {
    "kind": "python-fastapi",
    "install_cmd": "uv pip install -r requirements.txt",
    "start_cmd": "uvicorn app:app --port 8800",
    "port": 8800,
    "health_path": "/healthz",
    "runtime_reqs": [
      {"name": "python3", "version": ">=3.10", "auto_install": true}
    ]
  },
  "env_schema": [
    {"name": "API_KEY", "label": "API Key", "secret": true, "default": "", "optional": false, "system": false},
    {"name": "PORT", "label": "端口", "secret": false, "default": "8800", "optional": true, "system": false},
    {"name": "BIU_INSTALL_ID", "label": "", "secret": false, "default": "", "optional": true, "system": true}
  ],
  "repo_meta": {
    "url": "https://github.com/acme/openmontage",
    "default_branch": "main",
    "latest_ref": "v1.2.0",
    "latest_sha": "deadbeef",
    "stars": 1234,
    "license": "MIT"
  },
  "warnings": ["未发现 README"]
}
''';

  test('analyzeRepo: POST /v1/apps/repo/analyze + 完整解析', () async {
    routes['/v1/apps/repo/analyze'] = (200, analysisJson);

    final a = await client.analyzeRepo(
      repoUrl: 'https://github.com/acme/openmontage',
      token: 't',
    );

    expect(hits.single.$1, 'POST');
    expect(hits.single.$2, '/v1/apps/repo/analyze');
    expect(jsonDecode(hits.single.$3),
        {'repo_url': 'https://github.com/acme/openmontage'});

    expect(a.manifestDraft.identifier, 'openmontage');
    expect(a.manifestDraft.title, 'OpenMontage');
    expect(a.manifestDraft.version, '1.2.0');
    expect(a.stack.kind, 'python-fastapi');
    expect(a.stack.installCmd, contains('uv pip'));
    expect(a.stack.startCmd, contains('uvicorn'));
    expect(a.stack.port, 8800);
    expect(a.stack.healthPath, '/healthz');
    expect(a.stack.runtimeReqs.single.name, 'python3');
    expect(a.stack.runtimeReqs.single.version, '>=3.10');
    expect(a.stack.runtimeReqs.single.autoInstall, isTrue);
    expect(a.envSchema.length, 3);
    expect(a.envSchema[0].name, 'API_KEY');
    expect(a.envSchema[0].secret, isTrue);
    expect(a.envSchema[2].system, isTrue);
    expect(a.repoMeta.stars, 1234);
    expect(a.repoMeta.license, 'MIT');
    expect(a.repoMeta.latestRef, 'v1.2.0');
    expect(a.repoMeta.defaultBranch, 'main');
    expect(a.warnings, ['未发现 README']);
  });

  test('analyzeRepo: 空响应防御性解析给默认值', () async {
    routes['/v1/apps/repo/analyze'] = (200, '{}');
    final a = await client.analyzeRepo(repoUrl: 'https://github.com/x/y', token: 't');
    expect(a.manifestDraft.identifier, '');
    expect(a.stack.port, 0);
    expect(a.envSchema, isEmpty);
    expect(a.repoMeta.stars, 0);
    expect(a.warnings, isEmpty);
  });

  test('analyzeRepo: 服务端"不支持"错误 → ApiError 带 error.message', () async {
    routes['/v1/apps/repo/analyze'] = (
      400,
      '{"error":{"code":"unsupported_repo","message":"不支持的项目类型：未发现可运行的 Web 服务"}}'
    );
    try {
      await client.analyzeRepo(repoUrl: 'https://github.com/x/y', token: 't');
      fail('expected ApiError');
    } on ApiError catch (e) {
      expect(e.status, 400);
      expect(e.body, contains('unsupported_repo'));
      expect(e.body, contains('不支持的项目类型'));
    }
  });

  test('installRepo: POST /v1/apps/repo/installs，ref_type + config 上送',
      () async {
    routes['/v1/apps/repo/installs'] = (200, '''
{
  "id": "ins-1",
  "scope": "user",
  "scope_id": "u1",
  "app_id": "app-1",
  "identifier": "openmontage",
  "version": "1.2.0",
  "enabled": true,
  "config": {"PORT": "8800"},
  "installed_at": "2026-08-23T08:00:00Z",
  "updated_at": "2026-08-23T08:00:00Z"
}
''');
    final install = await client.installRepo(
      repoUrl: 'https://github.com/acme/openmontage',
      refType: 'release',
      config: const {'PORT': '8800'},
      token: 't',
    );
    expect(hits.single.$1, 'POST');
    expect(hits.single.$2, '/v1/apps/repo/installs');
    final sent = jsonDecode(hits.single.$3) as Map<String, dynamic>;
    expect(sent['repo_url'], 'https://github.com/acme/openmontage');
    expect(sent['ref_type'], 'release');
    expect(sent['config'], {'PORT': '8800'});

    expect(install.id, 'ins-1');
    expect(install.identifier, 'openmontage');
    expect(install.version, '1.2.0');
    expect(install.config, {'PORT': '8800'});
  });

  test('getRepoRuntime: GET /v1/apps/installs/{id}/runtime', () async {
    routes['/v1/apps/installs/ins-1/runtime'] =
        (200, '{"mode":"local","status":"running","url":"http://127.0.0.1:8800"}');
    final rt = await client.getRepoRuntime(installId: 'ins-1', token: 't');
    expect(hits.single.$1, 'GET');
    expect(hits.single.$2, '/v1/apps/installs/ins-1/runtime');
    expect(rt.mode, 'local');
    expect(rt.status, 'running');
    expect(rt.url, 'http://127.0.0.1:8800');
    expect(rt.isRunning, isTrue);
  });

  test('listRepoBuilds: GET /v1/apps/installs/{id}/builds', () async {
    routes['/v1/apps/installs/ins-1/builds'] = (200, '''
{
  "builds": [
    {"id": "b1", "ref": "v1.2.0", "sha": "deadbeef", "status": "live",
     "duration_ms": 42000, "created_at": "2026-08-23T08:00:00Z"},
    {"id": "b0", "ref": "v1.1.0", "sha": "cafe", "status": "failed",
     "duration_ms": 5000, "created_at": "2026-08-22T08:00:00Z"}
  ]
}
''');
    final builds = await client.listRepoBuilds(installId: 'ins-1', token: 't');
    expect(hits.single.$1, 'GET');
    expect(hits.single.$2, '/v1/apps/installs/ins-1/builds');
    expect(builds.length, 2);
    expect(builds.first.id, 'b1');
    expect(builds.first.status, 'live');
    expect(builds.first.durationMs, 42000);
    expect(builds.first.createdAt.year, 2026);
    expect(builds.last.status, 'failed');
  });

  test('redeployRepo: POST /v1/apps/installs/{id}/redeploy → build_id + ref/sha',
      () async {
    // M2 服务端：build_id 之外带 ref/sha。
    routes['/v1/apps/installs/ins-1/redeploy'] =
        (200, '{"build_id":"b2","ref":"v1.3.0","sha":"cafe0123"}');
    final r = await client.redeployRepo(installId: 'ins-1', token: 't');
    expect(hits.single.$1, 'POST');
    expect(hits.single.$2, '/v1/apps/installs/ins-1/redeploy');
    expect(r.buildId, 'b2');
    expect(r.ref, 'v1.3.0');
    expect(r.sha, 'cafe0123');
  });

  test('redeployRepo: 老服务端只有 build_id → ref/sha 空串（向后兼容）',
      () async {
    routes['/v1/apps/installs/ins-1/redeploy'] =
        (200, '{"build_id":"b2"}');
    final r = await client.redeployRepo(installId: 'ins-1', token: 't');
    expect(r.buildId, 'b2');
    expect(r.ref, '');
    expect(r.sha, '');
  });

  test('AppCatalogEntry: 可选 repo_meta / tier 防御性解析', () {
    final withMeta = AppCatalogEntry.fromJson({
      'identifier': 'openmontage',
      'title': 'OpenMontage',
      'version': '1.2.0',
      'tier': 'repo',
      'repo_meta': {'url': 'https://github.com/acme/openmontage', 'stars': 7},
    });
    expect(withMeta.tier, 'repo');
    expect(withMeta.repoMeta, isNotNull);
    expect(withMeta.repoMeta!.stars, 7);

    final plain = AppCatalogEntry.fromJson({'identifier': 'rss'});
    expect(plain.tier, '');
    expect(plain.repoMeta, isNull);

    // 类型不符（服务端 bug）也不炸。
    final weird = AppCatalogEntry.fromJson(
        {'identifier': 'x', 'repo_meta': 'not-a-map'});
    expect(weird.repoMeta, isNull);
  });
}

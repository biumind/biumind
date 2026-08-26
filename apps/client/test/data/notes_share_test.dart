// 笔记分享（S1）数据层测试 —— 镜像 notes_client_test.dart：真实 loopback
// HttpServer 冒充 brain，5 个管理端接口的路径 / 方法 / body presence 语义 /
// DTO 解析全部真打一遍；另有状态机推导与有效期归桶的纯函数测试。
//
// 契约基准：docs/BiuMind-Technical-Architecture.md §7.6「API 契约（S1 冻结）」。

import 'dart:convert';
import 'dart:io';

import 'package:biumind/data/api/notes_client.dart';
import 'package:biumind/features/notes/application/note_share_providers.dart';
import 'package:flutter_test/flutter_test.dart';

class _FakeShareApi {
  _FakeShareApi._(this.server);
  final HttpServer server;

  /// 最近一次请求（method + path + 解析后的 body）。
  String lastMethod = '';
  String lastPath = '';
  Map<String, dynamic> lastBody = {};

  static Map<String, dynamic> _shareJson({
    String token = 'tok-abc',
    bool passwordSet = false,
    String? expiresAt = '2026-09-02T00:00:00Z',
    int credentialVersion = 1,
    int viewCount = 7,
    int? maxViews,
    String? disabledAt,
  }) => {
    'token': token,
    'password_set': passwordSet,
    'expires_at': expiresAt,
    'credential_version': credentialVersion,
    'view_count': viewCount,
    'max_views': maxViews,
    'disabled_at': disabledAt,
    'created_at': '2026-08-26T00:00:00Z',
    'updated_at': '2026-08-26T01:00:00Z',
  };

  static Future<_FakeShareApi> start() async {
    final s = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    final fake = _FakeShareApi._(s);
    s.listen(fake._handle);
    return fake;
  }

  Uri get url => Uri.parse('http://${server.address.host}:${server.port}');
  Future<void> stop() => server.close(force: true);

  Future<void> _handle(HttpRequest req) async {
    lastMethod = req.method;
    lastPath = req.uri.path;
    final rawBody = await utf8.decoder.bind(req).join();
    lastBody = rawBody.isEmpty
        ? {}
        : (jsonDecode(rawBody) as Map).cast<String, dynamic>();
    final path = req.uri.path;

    var status = 200;
    Map<String, dynamic>? response;

    if (req.method == 'PUT' && path == '/v1/notes/n1/share') {
      response = _shareJson();
    } else if (req.method == 'GET' && path == '/v1/notes/n1/share') {
      response = _shareJson(passwordSet: true);
    } else if (req.method == 'GET' && path == '/v1/notes/none/share') {
      status = 404;
      response = {'error': 'not_found'};
    } else if (req.method == 'DELETE' && path == '/v1/notes/n1/share') {
      status = 204;
      response = null;
    } else if (req.method == 'POST' && path == '/v1/notes/n1/share/rotate') {
      response = _shareJson(token: 'tok-new', credentialVersion: 2);
    } else if (req.method == 'GET' && path == '/v1/notes/shares') {
      response = {
        'shares': [
          {
            ..._shareJson(viewCount: 3),
            'note_id': 'n1',
            'note_title': '会议纪要',
            'status': 'active',
          },
          {
            ..._shareJson(
              token: 'tok-x',
              expiresAt: null,
              disabledAt: '2026-08-27T00:00:00Z',
            ),
            'note_id': 'n2',
            'note_title': '',
            'status': 'disabled',
          },
          {
            ..._shareJson(token: 'tok-y', expiresAt: '2026-08-01T00:00:00Z'),
            'note_id': 'n3',
            'note_title': '旧笔记',
            'status': 'expired',
          },
          {
            ..._shareJson(token: 'tok-z', viewCount: 100, maxViews: 100),
            'note_id': 'n4',
            'note_title': '热门笔记',
            'status': 'exhausted',
          },
        ],
      };
    } else {
      status = 500;
      response = {'error': 'unexpected ${req.method} $path'};
    }

    req.response.statusCode = status;
    if (response != null) {
      req.response.headers.contentType = ContentType.json;
      req.response.write(jsonEncode(response));
    }
    await req.response.close();
  }
}

void main() {
  late _FakeShareApi fake;
  late NotesClient client;

  setUp(() async {
    fake = await _FakeShareApi.start();
    client = NotesClient(fake.url, 'token');
  });

  tearDown(() => fake.stop());

  group('管理端接口', () {
    test('PUT 创建/更新：传 expires_in 则上送，password 缺省不出现在 body', () async {
      final share = await client.putShare('n1', expiresIn: '7d');
      expect(fake.lastMethod, 'PUT');
      expect(fake.lastPath, '/v1/notes/n1/share');
      expect(fake.lastBody['expires_in'], '7d');
      expect(fake.lastBody.containsKey('password'), isFalse);
      expect(share.token, 'tok-abc');
      expect(share.passwordSet, isFalse);
      expect(share.expiresAt, DateTime.utc(2026, 9, 2));
      expect(share.credentialVersion, 1);
      expect(share.viewCount, 7);
      expect(share.disabledAt, isNull);
    });

    test('PUT expires_in 缺省 = 不上送（契约修订：保持现有 expires_at）', () async {
      await client.putShare('n1');
      expect(fake.lastBody.containsKey('expires_in'), isFalse);
      expect(fake.lastBody.containsKey('password'), isFalse);
    });

    test('PUT password：空串 = 移除密码（字段必须上送），expires_in 缺省', () async {
      await client.putShare('n1', password: '');
      expect(fake.lastBody['password'], '');
      expect(fake.lastBody.containsKey('expires_in'), isFalse);
    });

    test('PUT password：有值 = 重设密码，expires_in 缺省', () async {
      await client.putShare('n1', password: '1234');
      expect(fake.lastBody['password'], '1234');
      expect(fake.lastBody.containsKey('expires_in'), isFalse);
    });

    test('GET 单篇分享状态', () async {
      final share = await client.getShare('n1');
      expect(fake.lastMethod, 'GET');
      expect(share.passwordSet, isTrue);
    });

    test('GET 无分享 → 404 NotesApiError.isNotFound', () async {
      expect(
        () => client.getShare('none'),
        throwsA(
          isA<NotesApiError>().having(
            (e) => e.isNotFound,
            'isNotFound',
            isTrue,
          ),
        ),
      );
    });

    test('DELETE 停用 → 204 无响应体', () async {
      await client.deleteShare('n1');
      expect(fake.lastMethod, 'DELETE');
      expect(fake.lastPath, '/v1/notes/n1/share');
    });

    test('rotate → 新 token + credential_version+1', () async {
      final share = await client.rotateShare('n1');
      expect(fake.lastMethod, 'POST');
      expect(fake.lastPath, '/v1/notes/n1/share/rotate');
      expect(share.token, 'tok-new');
      expect(share.credentialVersion, 2);
    });

    test('GET 我的分享列表：note_id/note_title/status/max_views 解析', () async {
      final items = await client.listShares();
      expect(items, hasLength(4));
      expect(items[0].noteId, 'n1');
      expect(items[0].noteTitle, '会议纪要');
      expect(items[0].status, NoteShareStatus.active);
      expect(items[0].share.maxViews, isNull);
      expect(items[1].status, NoteShareStatus.disabled);
      expect(items[1].share.disabledAt, isNotNull);
      expect(items[1].share.expiresAt, isNull);
      expect(items[2].status, NoteShareStatus.expired);
      // S2：exhausted 状态值 + max_views 解析。
      expect(items[3].status, NoteShareStatus.exhausted);
      expect(items[3].share.maxViews, 100);
      expect(items[3].share.viewCount, 100);
    });

    test('PUT max_views 三态：正整数设置 / 0 移除 / 缺省不上送（S2）', () async {
      await client.putShare('n1', maxViews: 500);
      expect(fake.lastBody['max_views'], 500);

      await client.putShare('n1', maxViews: 0);
      expect(fake.lastBody['max_views'], 0);

      await client.putShare('n1');
      expect(fake.lastBody.containsKey('max_views'), isFalse);
    });
  });

  group('status 推导（与服务端同一状态机）', () {
    final now = DateTime.utc(2026, 8, 26, 12);

    test('disabled_at 非空 → disabled（优先于过期判定）', () {
      expect(
        noteShareStatusOf(
          disabledAt: now.subtract(const Duration(days: 1)),
          expiresAt: now.subtract(const Duration(days: 2)),
          now: now,
        ),
        NoteShareStatus.disabled,
      );
    });

    test('expires_at 已过 → expired（恰到期时刻也算过期）', () {
      expect(
        noteShareStatusOf(
          disabledAt: null,
          expiresAt: now.subtract(const Duration(seconds: 1)),
          now: now,
        ),
        NoteShareStatus.expired,
      );
      expect(
        noteShareStatusOf(disabledAt: null, expiresAt: now, now: now),
        NoteShareStatus.expired,
      );
    });

    test('未停用未过期 → active；expires_at null = 永久有效', () {
      expect(
        noteShareStatusOf(
          disabledAt: null,
          expiresAt: now.add(const Duration(days: 1)),
          now: now,
        ),
        NoteShareStatus.active,
      );
      expect(
        noteShareStatusOf(disabledAt: null, expiresAt: null, now: now),
        NoteShareStatus.active,
      );
    });

    test('max_views 触顶 → exhausted（优先级低于 disabled、高于 expired）', () {
      // view_count >= max_views（恰等也算触顶）→ exhausted
      expect(
        noteShareStatusOf(
          disabledAt: null,
          expiresAt: now.add(const Duration(days: 1)),
          viewCount: 100,
          maxViews: 100,
          now: now,
        ),
        NoteShareStatus.exhausted,
      );
      // 未触顶 → active
      expect(
        noteShareStatusOf(
          disabledAt: null,
          expiresAt: null,
          viewCount: 99,
          maxViews: 100,
          now: now,
        ),
        NoteShareStatus.active,
      );
      // disabled 优先于 exhausted
      expect(
        noteShareStatusOf(
          disabledAt: now.subtract(const Duration(days: 1)),
          expiresAt: null,
          viewCount: 100,
          maxViews: 100,
          now: now,
        ),
        NoteShareStatus.disabled,
      );
      // exhausted 优先于 expired
      expect(
        noteShareStatusOf(
          disabledAt: null,
          expiresAt: now.subtract(const Duration(days: 1)),
          viewCount: 100,
          maxViews: 100,
          now: now,
        ),
        NoteShareStatus.exhausted,
      );
      // max_views null = 不限，永不 exhausted
      expect(
        noteShareStatusOf(
          disabledAt: null,
          expiresAt: null,
          viewCount: 99999,
          maxViews: null,
          now: now,
        ),
        NoteShareStatus.active,
      );
    });

    test('NoteShare.status 方法走同一推导', () {
      final share = NoteShare(
        token: 't',
        passwordSet: false,
        expiresAt: now.subtract(const Duration(hours: 1)),
        credentialVersion: 1,
        viewCount: 0,
        createdAt: now,
        updatedAt: now,
      );
      expect(share.status(now), NoteShareStatus.expired);
    });
  });

  group('有效期归桶（仅用于有效期选择器的选中态回显，不再上送）', () {
    final now = DateTime.utc(2026, 8, 26, 12);

    test('null / 已过期 → never', () {
      expect(noteShareExpiresInOf(null, now), 'never');
      expect(
        noteShareExpiresInOf(now.subtract(const Duration(hours: 1)), now),
        'never',
      );
    });

    test('按剩余时间归桶', () {
      expect(
        noteShareExpiresInOf(now.add(const Duration(hours: 20)), now),
        '1d',
      );
      expect(noteShareExpiresInOf(now.add(const Duration(days: 5)), now), '7d');
      expect(
        noteShareExpiresInOf(now.add(const Duration(days: 20)), now),
        '30d',
      );
      expect(
        noteShareExpiresInOf(now.add(const Duration(days: 60)), now),
        'never',
      );
    });
  });

  group('分享 URL 拼接（契约：客户端拼 origin/s/n/token）', () {
    test('origin 无 path 时直接拼', () {
      expect(
        noteShareUrl(Uri.parse('https://biumind.ai'), 'tok-abc'),
        'https://biumind.ai/s/n/tok-abc',
      );
    });

    test('自托管带端口 origin', () {
      expect(
        noteShareUrl(Uri.parse('http://localhost:8088'), 'tok-abc'),
        'http://localhost:8088/s/n/tok-abc',
      );
    });
  });
}

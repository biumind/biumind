// 验证 humanizeAppsError() 把 ApiError + 网络异常映射成稳定的用户文案。
//
// 走 Builder pattern 拿一个真 BuildContext —— l10n.appsErr* 是 lookup-
// based, 没 context 就拿不到 zh/en strings.

import 'package:biumind/data/api/_http_helpers.dart';
import 'package:biumind/features/apps/presentation/apps_error.dart';
import 'package:biumind/l10n/app_localizations.dart';
import 'package:flutter/widgets.dart';
import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_test/flutter_test.dart';

Future<String> _humanize(WidgetTester tester, Object e, {Locale loc = const Locale('zh')}) async {
  String? out;
  await tester.pumpWidget(Localizations(
    locale: loc,
    delegates: const [
      AppLocalizations.delegate,
      GlobalMaterialLocalizations.delegate,
      GlobalWidgetsLocalizations.delegate,
    ],
    child: Directionality(
      textDirection: TextDirection.ltr,
      child: Builder(builder: (ctx) {
        out = humanizeAppsError(ctx, e);
        return const SizedBox.shrink();
      }),
    ),
  ));
  return out!;
}

void main() {
  test('apps_error_test smoke', () {});

  testWidgets('400 → 校验错误带 server detail', (tester) async {
    final err = ApiError(path: '/v1/apps/installs', status: 400, body: '{"error":"bad slug"}');
    final out = await _humanize(tester, err);
    expect(out.contains('bad slug'), isTrue, reason: '应该带上 server detail');
    expect(out.contains('参数有误') || out.contains('Invalid request'), isTrue);
  });

  testWidgets('401 → 登录过期 (zh)', (tester) async {
    final err = ApiError(path: '/v1/apps', status: 401, body: '');
    final out = await _humanize(tester, err);
    expect(out, contains('登录'));
  });

  testWidgets('403 → 没有权限', (tester) async {
    final err = ApiError(path: '/v1/apps', status: 403, body: '');
    final out = await _humanize(tester, err);
    expect(out, contains('没有权限'));
  });

  testWidgets('404 → 对象不存在', (tester) async {
    final err = ApiError(path: '/v1/apps', status: 404, body: '');
    final out = await _humanize(tester, err);
    expect(out, contains('不存在'));
  });

  testWidgets('409 → 冲突', (tester) async {
    final err = ApiError(path: '/v1/sidebar/layout', status: 409, body: '{"error":"version_conflict"}');
    final out = await _humanize(tester, err);
    expect(out, contains('冲突'));
  });

  testWidgets('429 → 限流', (tester) async {
    final err = ApiError(path: '/v1/apps', status: 429, body: '');
    final out = await _humanize(tester, err);
    expect(out, contains('频繁'));
  });

  testWidgets('5xx → 服务暂时不可用 (带 status)', (tester) async {
    final err = ApiError(path: '/v1/apps', status: 503, body: 'upstream timeout');
    final out = await _humanize(tester, err);
    expect(out, contains('503'));
    expect(out, contains('服务'));
  });

  testWidgets('socket 异常 → 网络异常文案', (tester) async {
    final err = Exception('SocketException: Connection refused');
    final out = await _humanize(tester, err);
    expect(out, contains('网络'));
  });

  testWidgets('英文 locale 走英文文案', (tester) async {
    final err = ApiError(path: '/v1/apps', status: 401, body: '');
    final out = await _humanize(tester, err, loc: const Locale('en'));
    expect(out, contains('Session expired'));
  });

  testWidgets('400 不带 JSON body 也能工作', (tester) async {
    final err = ApiError(path: '/v1/apps', status: 400, body: 'plain text problem');
    final out = await _humanize(tester, err);
    expect(out, contains('plain text problem'));
  });

  // ─── nested {error: {code, message}} 解析 (#7) ──────────────

  testWidgets('400 nested error.message 抽出', (tester) async {
    // app_center writeErr 风格 — invoke 路径常见返回。
    final err = ApiError(
      path: '/v1/apps/rss/invoke',
      status: 400,
      body: '{"error":{"code":"missing_action","message":"action 字段不能为空"}}',
    );
    final out = await _humanize(tester, err);
    expect(out, contains('action 字段不能为空'));
  });

  testWidgets('403 not_installed 走专属文案', (tester) async {
    final err = ApiError(
      path: '/v1/apps/rss/invoke',
      status: 403,
      body: '{"error":{"code":"not_installed","message":"install required before invoking actions"}}',
    );
    final out = await _humanize(tester, err);
    expect(out, contains('请先安装'));
    expect(out.contains('没有权限'), isFalse, reason: '不应再用通用 forbidden');
  });

  testWidgets('403 install_disabled 走专属文案', (tester) async {
    final err = ApiError(
      path: '/v1/apps/rss/invoke',
      status: 403,
      body: '{"error":{"code":"install_disabled","message":"this installation is currently disabled"}}',
    );
    final out = await _humanize(tester, err);
    expect(out, contains('已停用'));
  });

  testWidgets('403 generic forbidden 不命中专属 code 仍用通用文案', (tester) async {
    final err = ApiError(
      path: '/v1/apps/rss/invoke',
      status: 403,
      body: '{"error":{"code":"permission_denied","message":"cedar deny"}}',
    );
    final out = await _humanize(tester, err);
    expect(out, contains('没有权限'));
  });

  testWidgets('500 invoke_failed 把 app 业务消息露出 + strip 前缀', (tester) async {
    final err = ApiError(
      path: '/v1/apps/tasks/invoke',
      status: 500,
      body: '{"error":{"code":"invoke_failed","message":"tasks: tsk-abc not found"}}',
    );
    final out = await _humanize(tester, err);
    expect(out, contains('tsk-abc not found'));
    expect(out.contains('tasks:'), isFalse, reason: 'app 前缀应该被剥掉');
  });

  testWidgets('500 非 invoke_failed 仍用通用服务不可用文案', (tester) async {
    final err = ApiError(
      path: '/v1/apps',
      status: 500,
      body: '{"error":{"code":"db_unavailable","message":"connection refused"}}',
    );
    final out = await _humanize(tester, err);
    expect(out, contains('500'));
    expect(out.contains('db_unavailable'), isFalse, reason: '非业务错误的 detail 不该露出');
  });

  testWidgets('strip 前缀只对 lowercase 前缀生效, "Failed: ..." 保持原样', (tester) async {
    // 模拟非 app-prefix 的 message; 不应被砍头。
    final err = ApiError(
      path: '/v1/apps/x/invoke',
      status: 500,
      body: '{"error":{"code":"invoke_failed","message":"Failed to reach upstream"}}',
    );
    final out = await _humanize(tester, err);
    expect(out, contains('Failed to reach upstream'));
  });
}

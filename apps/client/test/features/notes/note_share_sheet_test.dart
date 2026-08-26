// 笔记分享弹层（NoteShareSheet）widget 测试。
//
// TestWidgetsFlutterBinding 会拦截真实 HTTP（一律 400），不能用
// notes_client_test.dart 的 loopback HttpServer 模式 —— 改用
// `_FakeShareClient extends NotesClient`（内存态 + 请求日志）override
// noteShareClientProvider；hubCredentialsProvider 用固定 origin（分享 URL
// 客户端拼接 `${origin}/s/${token}`）。noteShareProvider /
// myNoteSharesProvider 的 invalidate 刷新链路走真实实现，「点击 → 调用 →
// invalidate → 重拉 → UI 更新」整回路被覆盖。
//
// 加载态有 CircularProgressIndicator（无限动画），不能用 pumpAndSettle ——
// 统一用 _pumpUntil 小步推进。

import 'package:biumind/app/theme/theme.dart';
import 'package:biumind/data/api/notes_client.dart';
import 'package:biumind/features/notes/application/note_share_providers.dart';
import 'package:biumind/features/notes/presentation/note_share_sheet.dart';
import 'package:biumind/services/auth_service.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

/// 内存态假 client：5 个管理端方法全真实现（含契约 presence 语义），
/// 记录请求日志供断言。
class _FakeShareClient extends NotesClient {
  _FakeShareClient() : super(Uri.parse('http://fake'), 'tok');

  NoteShare? share;

  /// 请求日志（'PUT' / 'DELETE' / 'ROTATE' / 'GET' / 'LIST'）。
  final log = <String>[];

  /// 最近一次 PUT 上送的 body（password 缺省 = key 不存在）。
  Map<String, dynamic> lastPutBody = {};

  /// 置 true 后下一次 PUT 抛 500（验证失败路径不落本地态）。
  bool failNextPut = false;

  static DateTime? _expiresAtFor(String expiresIn) {
    final days = switch (expiresIn) { '1d' => 1, '7d' => 7, '30d' => 30, _ => null };
    if (days == null) return null;
    return DateTime.now().toUtc().add(Duration(days: days));
  }

  /// 预置一条活跃分享。
  void seed({bool passwordSet = false, DateTime? expiresAt}) {
    share = NoteShare(
      token: 'tok-1',
      passwordSet: passwordSet,
      expiresAt: expiresAt,
      credentialVersion: 1,
      viewCount: 3,
      createdAt: DateTime.utc(2026, 8, 26),
      updatedAt: DateTime.utc(2026, 8, 26),
    );
  }

  @override
  Future<NoteShare> putShare(
    String noteId, {
    String? password,
    String? expiresIn,
  }) async {
    log.add('PUT');
    lastPutBody = {'expires_in': ?expiresIn, 'password': ?password};
    if (failNextPut) {
      failNextPut = false;
      throw const NotesApiError(
          method: 'PUT', path: '/x', status: 500, body: 'boom');
    }
    final cur = share;
    var passwordSet = cur?.passwordSet ?? false;
    var credentialVersion = cur?.credentialVersion ?? 1;
    if (password != null) {
      if (password.isEmpty) {
        passwordSet = false;
      } else {
        passwordSet = true;
        credentialVersion += 1;
      }
    }
    share = NoteShare(
      token: cur?.token ?? 'tok-1',
      passwordSet: passwordSet,
      // 契约修订：expires_in 缺省 = 保持现有 expires_at 不变。
      expiresAt:
          expiresIn == null ? cur?.expiresAt : _expiresAtFor(expiresIn),
      credentialVersion: credentialVersion,
      viewCount: cur?.viewCount ?? 0,
      // PUT 对已停用分享 = 恢复（契约）。
      createdAt: cur?.createdAt ?? DateTime.utc(2026, 8, 26),
      updatedAt: DateTime.now().toUtc(),
    );
    return share!;
  }

  @override
  Future<NoteShare> getShare(String noteId) async {
    log.add('GET');
    final s = share;
    if (s == null) {
      throw const NotesApiError(
          method: 'GET', path: '/x', status: 404, body: '{"error":"not_found"}');
    }
    return s;
  }

  @override
  Future<void> deleteShare(String noteId) async {
    log.add('DELETE');
    final s = share;
    if (s == null) return;
    share = NoteShare(
      token: s.token,
      passwordSet: s.passwordSet,
      expiresAt: s.expiresAt,
      credentialVersion: s.credentialVersion,
      viewCount: s.viewCount,
      disabledAt: DateTime.now().toUtc(),
      createdAt: s.createdAt,
      updatedAt: DateTime.now().toUtc(),
    );
  }

  @override
  Future<NoteShare> rotateShare(String noteId) async {
    log.add('ROTATE');
    final s = share;
    share = NoteShare(
      token: 'tok-rotated',
      passwordSet: s?.passwordSet ?? false,
      expiresAt: s?.expiresAt,
      credentialVersion: (s?.credentialVersion ?? 1) + 1,
      viewCount: s?.viewCount ?? 0,
      createdAt: s?.createdAt ?? DateTime.utc(2026, 8, 26),
      updatedAt: DateTime.now().toUtc(),
    );
    return share!;
  }

  @override
  Future<List<NoteShareListItem>> listShares() async =>
      const <NoteShareListItem>[];
}

void main() {
  late _FakeShareClient client;
  String? clipboardText;

  const origin = 'https://share.example';

  setUp(() {
    client = _FakeShareClient();
    clipboardText = null;
  });

  Future<void> pumpSheet(WidgetTester tester) async {
    tester.binding.defaultBinaryMessenger.setMockMethodCallHandler(
      SystemChannels.platform,
      (call) async {
        if (call.method == 'Clipboard.setData') {
          clipboardText = (call.arguments as Map)['text'] as String?;
        }
        return null;
      },
    );
    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          noteShareClientProvider.overrideWithValue(client),
          hubCredentialsProvider.overrideWithValue(
            HubCredentials(
                endpoint: Uri.parse(origin), bearerToken: 'tok'),
          ),
        ],
        child: MaterialApp(
          theme: buildTheme(
            palette: PaletteId.inkblueOrange,
            mode: Brightness.light,
            fontSize: FontSize.small,
          ),
          home: const Scaffold(body: NoteShareSheet(noteId: 'n1')),
        ),
      ),
    );
  }

  String expectedUrl(String token) => '$origin/s/$token';

  testWidgets('未分享 → 「创建分享链接」→ 创建后出现链接与复制按钮', (tester) async {
    await pumpSheet(tester);
    await pumpUntilFound(tester, find.text('创建分享链接'));

    await tester.tap(find.text('创建分享链接'));
    await pumpUntilFound(tester, find.text(expectedUrl('tok-1')));

    // PUT body：创建显式传默认档位 never，password 字段缺省。
    expect(client.lastPutBody['expires_in'], 'never');
    expect(client.lastPutBody.containsKey('password'), isFalse);
    expect(find.byTooltip('复制链接'), findsOneWidget);
    expect(find.textContaining('累计访问'), findsOneWidget);
  });

  testWidgets('复制按钮 → 剪贴板拿到 origin 拼接的分享链接', (tester) async {
    client.seed();
    await pumpSheet(tester);
    await pumpUntilFound(tester, find.byTooltip('复制链接'));

    await tester.tap(find.byTooltip('复制链接'));
    await tester.pump();
    expect(clipboardText, expectedUrl('tok-1'));
  });

  testWidgets('密码：开关展开输入 → 3 位按钮禁用 → 4–8 位提交成功 → 合并复制',
      (tester) async {
    client.seed();
    await pumpSheet(tester);
    await pumpUntilFound(tester, find.byTooltip('复制链接'));

    // 开密码开关 → 输入区出现。
    await tester.tap(find.byType(Switch));
    await pumpUntilFound(tester, find.byType(TextField));

    // 3 位：「设置」按钮禁用。
    await tester.enterText(find.byType(TextField), '123');
    await tester.pump();
    var button = tester.widget<FilledButton>(
      find.widgetWithText(FilledButton, '设置'),
    );
    expect(button.onPressed, isNull);

    // 4–8 位：可提交。
    await tester.enterText(find.byType(TextField), '1234');
    await tester.pump();
    button = tester.widget<FilledButton>(
      find.widgetWithText(FilledButton, '设置'),
    );
    expect(button.onPressed, isNotNull);
    await tester.tap(find.widgetWithText(FilledButton, '设置'));
    // 提交成功 → 按钮文案随 password_set 刷新成「更新」。
    await pumpUntilFound(tester, find.widgetWithText(FilledButton, '更新'));

    expect(client.lastPutBody['password'], '1234');
    // 契约修订：改密码不再上送 expires_in（缺省 = 保持现有有效期）。
    expect(client.lastPutBody.containsKey('expires_in'), isFalse);
    expect(client.share!.passwordSet, isTrue);
    expect(client.share!.credentialVersion, 2);

    // 本次会话设过密码 → 复制为「链接 + 密码」合并文案。
    await tester.tap(find.byTooltip('复制链接'));
    await tester.pump();
    expect(clipboardText, contains(expectedUrl('tok-1')));
    expect(clipboardText, contains('访问密码：1234'));
  });

  testWidgets('密码设置失败：不落本地态，输入区保留待重试', (tester) async {
    client.seed();
    await pumpSheet(tester);
    await pumpUntilFound(tester, find.byTooltip('复制链接'));

    await tester.tap(find.byType(Switch));
    await pumpUntilFound(tester, find.byType(TextField));
    await tester.enterText(find.byType(TextField), '1234');
    await tester.pump();

    client.failNextPut = true;
    await tester.tap(find.widgetWithText(FilledButton, '设置'));
    await pumpUntilFound(tester, find.textContaining('操作失败'));

    // 失败：password_set 未变、输入区不收起（可修正重试），
    // 复制仍是纯链接（没有「链接+密码」合并文案）。
    expect(client.share!.passwordSet, isFalse);
    expect(find.byType(TextField), findsOneWidget);
    await tester.tap(find.byTooltip('复制链接'));
    await tester.pump();
    expect(clipboardText, expectedUrl('tok-1'));
  });

  testWidgets('有效期切换 → PUT body 的 expires_in 正确', (tester) async {
    client.seed();
    await pumpSheet(tester);
    await pumpUntilFound(tester, find.byTooltip('复制链接'));

    for (final (label, expected) in [
      ('1 天', '1d'),
      ('7 天', '7d'),
      ('30 天', '30d'),
      ('永久', 'never'),
    ]) {
      final putsBefore = client.log.where((e) => e == 'PUT').length;
      final getsBefore = client.log.where((e) => e == 'GET').length;
      await tester.tap(find.text(label));
      // 等本次 PUT 发出 + invalidate 触发的重拉（GET）完成，再点下一个
      // —— 否则 tap 可能落在刷新中的旧树上被吞掉（tooltip「复制链接」
      // 动作前后都存在，不能当完成信号）。
      for (var i = 0; i < 100; i++) {
        final puts = client.log.where((e) => e == 'PUT').length;
        final gets = client.log.where((e) => e == 'GET').length;
        if (puts > putsBefore && gets > getsBefore) break;
        await tester.pump(const Duration(milliseconds: 50));
      }
      expect(
        client.lastPutBody['expires_in'],
        expected,
        reason: '点「$label」应上送 expires_in=$expected',
      );
      // password 字段始终缺省（保持不变）。
      expect(client.lastPutBody.containsKey('password'), isFalse);
    }
    expect(client.log.where((e) => e == 'PUT').length, 4);
  });

  testWidgets('重置链接 → 确认对话框 → rotate → 展示新 token', (tester) async {
    client.seed();
    await pumpSheet(tester);
    await pumpUntilFound(tester, find.text(expectedUrl('tok-1')));

    await tester.tap(find.text('重置链接'));
    await pumpUntilFound(
      tester,
      find.text('重置后旧链接立即作废，之前发出去的链接将无法访问。确定重置？'),
    );
    await tester.tap(find.widgetWithText(FilledButton, '重置'));
    await pumpUntilFound(tester, find.text(expectedUrl('tok-rotated')));

    expect(client.log, contains('ROTATE'));
    expect(find.text(expectedUrl('tok-1')), findsNothing);
  });

  testWidgets('停止分享 → 确认 → DELETE → 已停用横幅 → 恢复分享 → PUT',
      (tester) async {
    client.seed();
    await pumpSheet(tester);
    await pumpUntilFound(tester, find.byTooltip('复制链接'));

    // 停止分享（弹层上是 TextButton，确认对话框里是 FilledButton）。
    await tester.tap(find.widgetWithText(TextButton, '停止分享'));
    await pumpUntilFound(
        tester, find.widgetWithText(FilledButton, '停止分享'));
    await tester.tap(find.widgetWithText(FilledButton, '停止分享'));
    await pumpUntilFound(tester, find.text('已停止分享，链接当前无法访问'));

    expect(client.log, contains('DELETE'));
    expect(client.share!.disabledAt, isNotNull);
    // 已停用：动作行换成「恢复分享」。
    expect(find.text('恢复分享'), findsOneWidget);

    await tester.tap(find.text('恢复分享'));
    // 动作行从「恢复分享」换回「重置链接」才是恢复完成（tooltip「复制链接」
    // 在停用态也存在，不能当完成信号）。
    await pumpUntilFound(tester, find.text('重置链接'));
    expect(client.log.where((e) => e == 'PUT').length, 1);
    // 恢复的 PUT：两个字段全缺省（原 token 恢复、有效期保持不变）。
    expect(client.lastPutBody.containsKey('expires_in'), isFalse);
    expect(client.lastPutBody.containsKey('password'), isFalse);
    expect(client.share!.disabledAt, isNull);
    expect(find.text('已停止分享，链接当前无法访问'), findsNothing);
  });
}

/// 小步 pump 直到 finder 命中（加载态 spinner 是无限动画，不能
/// pumpAndSettle）。
Future<void> pumpUntilFound(
  WidgetTester tester,
  Finder finder, {
  int maxPumps = 100,
}) async {
  for (var i = 0; i < maxPumps; i++) {
    if (finder.evaluate().isNotEmpty) return;
    await tester.pump(const Duration(milliseconds: 50));
  }
  fail('pumpUntilFound 超时：$finder 未出现');
}

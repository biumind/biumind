// 我的分享管理页（MySharesPane）widget 测试。
//
// TestWidgetsFlutterBinding 拦截真实 HTTP（一律 400），改用
// `_FakeSharesClient extends NotesClient`（内存列表 + 请求日志）override
// noteShareClientProvider，myNoteSharesProvider 的拉取与 invalidate 刷新
// 走真实 provider 链路（模式同 note_share_sheet_test.dart）。
//
// 覆盖：三态 chip（生效中/已停用/已过期）、空态、停用 / 恢复 / 重置链接
// 动作触发的请求与行内状态刷新。

import 'package:biumind/app/theme/theme.dart';
import 'package:biumind/data/api/notes_client.dart';
import 'package:biumind/features/notes/application/note_share_providers.dart';
import 'package:biumind/features/settings/presentation/my_shares_pane.dart';
import 'package:biumind/services/auth_service.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

class _FakeSharesClient extends NotesClient {
  _FakeSharesClient() : super(Uri.parse('http://fake'), 'tok');

  /// GET /v1/notes/shares 返回的列表（动作方法会同步改状态）。
  List<NoteShareListItem> items = [];

  /// 请求日志（'LIST' / 'PUT:n1' / 'DELETE:n1' / 'ROTATE:n1'）。
  final log = <String>[];

  Map<String, dynamic> lastPutBody = {};

  static NoteShareListItem item({
    required String noteId,
    required String title,
    required NoteShareStatus status,
    int viewCount = 0,
    DateTime? expiresAt,
  }) =>
      NoteShareListItem(
        share: NoteShare(
          token: 'tok-$noteId',
          passwordSet: false,
          expiresAt: expiresAt,
          credentialVersion: 1,
          viewCount: viewCount,
          disabledAt: status == NoteShareStatus.disabled
              ? DateTime.utc(2026, 8, 27)
              : null,
          createdAt: DateTime.utc(2026, 8, 26),
          updatedAt: DateTime.utc(2026, 8, 26),
        ),
        noteId: noteId,
        noteTitle: title,
        status: status,
      );

  NoteShareListItem _replace(String noteId, NoteShareStatus status) {
    final i = items.indexWhere((it) => it.noteId == noteId);
    final old = items[i];
    final next = NoteShareListItem(
      share: NoteShare(
        token: old.share.token,
        passwordSet: old.share.passwordSet,
        expiresAt: old.share.expiresAt,
        credentialVersion: old.share.credentialVersion,
        viewCount: old.share.viewCount,
        disabledAt: status == NoteShareStatus.disabled
            ? DateTime.now().toUtc()
            : null,
        createdAt: old.share.createdAt,
        updatedAt: DateTime.now().toUtc(),
      ),
      noteId: old.noteId,
      noteTitle: old.noteTitle,
      status: status,
    );
    items[i] = next;
    return next;
  }

  @override
  Future<List<NoteShareListItem>> listShares() async {
    log.add('LIST');
    return items;
  }

  @override
  Future<void> deleteShare(String noteId) async {
    log.add('DELETE:$noteId');
    _replace(noteId, NoteShareStatus.disabled);
  }

  @override
  Future<NoteShare> putShare(
    String noteId, {
    String? password,
    String? expiresIn,
  }) async {
    log.add('PUT:$noteId');
    lastPutBody = {'expires_in': ?expiresIn, 'password': ?password};
    // PUT 对已停用分享 = 以原 token 恢复（契约）。
    return _replace(noteId, NoteShareStatus.active).share;
  }

  @override
  Future<NoteShare> rotateShare(String noteId) async {
    log.add('ROTATE:$noteId');
    final i = items.indexWhere((it) => it.noteId == noteId);
    final old = items[i];
    final next = NoteShareListItem(
      share: NoteShare(
        token: 'tok-rotated',
        passwordSet: old.share.passwordSet,
        expiresAt: old.share.expiresAt,
        credentialVersion: old.share.credentialVersion + 1,
        viewCount: old.share.viewCount,
        createdAt: old.share.createdAt,
        updatedAt: DateTime.now().toUtc(),
      ),
      noteId: old.noteId,
      noteTitle: old.noteTitle,
      status: old.status,
    );
    items[i] = next;
    return next.share;
  }
}

void main() {
  late _FakeSharesClient client;

  setUp(() {
    client = _FakeSharesClient();
  });

  Future<void> pumpPane(WidgetTester tester) async {
    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          noteShareClientProvider.overrideWithValue(client),
          hubCredentialsProvider.overrideWithValue(
            HubCredentials(
                endpoint: Uri.parse('https://share.example'),
                bearerToken: 'tok'),
          ),
        ],
        child: MaterialApp(
          theme: buildTheme(
            palette: PaletteId.inkblueOrange,
            mode: Brightness.light,
            fontSize: FontSize.small,
          ),
          home: const Scaffold(body: MySharesPane()),
        ),
      ),
    );
  }

  testWidgets('列表渲染：三态 chip + 标题 + 有效期/访问次数', (tester) async {
    client.items = [
      _FakeSharesClient.item(
          noteId: 'n1',
          title: '会议纪要',
          status: NoteShareStatus.active,
          viewCount: 5),
      _FakeSharesClient.item(
          noteId: 'n2', title: '', status: NoteShareStatus.disabled),
      _FakeSharesClient.item(
          noteId: 'n3',
          title: '旧笔记',
          status: NoteShareStatus.expired,
          expiresAt: DateTime.utc(2026, 8, 1)),
    ];
    await pumpPane(tester);
    await pumpUntilFound(tester, find.text('生效中'));

    expect(find.text('会议纪要'), findsOneWidget);
    expect(find.text('无标题笔记'), findsOneWidget); // n2 标题空 → 兜底
    expect(find.text('旧笔记'), findsOneWidget);
    expect(find.text('生效中'), findsOneWidget);
    expect(find.text('已停用'), findsOneWidget);
    expect(find.text('已过期'), findsOneWidget);
    expect(find.textContaining('累计访问 5 次'), findsOneWidget);
    // 只有生效中的行有「复制链接」；已停用行有「恢复分享」。
    expect(find.byTooltip('复制链接'), findsOneWidget);
    expect(find.byTooltip('恢复分享'), findsOneWidget);
  });

  testWidgets('空态：还没有分享过笔记', (tester) async {
    client.items = [];
    await pumpPane(tester);
    await pumpUntilFound(tester, find.text('还没有分享过笔记'));
    expect(find.text('在笔记编辑器工具栏或列表右键菜单中发起分享'), findsOneWidget);
  });

  testWidgets('停用：确认对话框 → DELETE → 行刷成已停用', (tester) async {
    client.items = [
      _FakeSharesClient.item(
          noteId: 'n1', title: '会议纪要', status: NoteShareStatus.active),
    ];
    await pumpPane(tester);
    await pumpUntilFound(tester, find.byTooltip('停止分享'));

    await tester.tap(find.byTooltip('停止分享'));
    await pumpUntilFound(
        tester, find.widgetWithText(FilledButton, '停止分享'));
    await tester.tap(find.widgetWithText(FilledButton, '停止分享'));
    await pumpUntilFound(tester, find.text('已停用'));

    expect(client.log, contains('DELETE:n1'));
    // 行内状态随 invalidate 重拉刷新：停用按钮换成恢复按钮。
    expect(find.byTooltip('恢复分享'), findsOneWidget);
    expect(find.byTooltip('停止分享'), findsNothing);
  });

  testWidgets('恢复：直接 PUT（契约：原 token 恢复）→ 行刷回生效中',
      (tester) async {
    client.items = [
      _FakeSharesClient.item(
          noteId: 'n1', title: '会议纪要', status: NoteShareStatus.disabled),
    ];
    await pumpPane(tester);
    await pumpUntilFound(tester, find.byTooltip('恢复分享'));

    await tester.tap(find.byTooltip('恢复分享'));
    await pumpUntilFound(tester, find.text('生效中'));

    expect(client.log, contains('PUT:n1'));
    // 契约修订：恢复的 PUT 两个字段全缺省（有效期保持现有 expires_at）。
    expect(client.lastPutBody.containsKey('expires_in'), isFalse);
    expect(client.lastPutBody.containsKey('password'), isFalse); // 缺省 = 保持
    expect(find.byTooltip('停止分享'), findsOneWidget);
  });

  testWidgets('重置链接：确认对话框 → POST rotate', (tester) async {
    client.items = [
      _FakeSharesClient.item(
          noteId: 'n1', title: '会议纪要', status: NoteShareStatus.active),
    ];
    await pumpPane(tester);
    await pumpUntilFound(tester, find.byTooltip('重置链接'));

    await tester.tap(find.byTooltip('重置链接'));
    await pumpUntilFound(tester, find.widgetWithText(FilledButton, '重置'));
    await tester.tap(find.widgetWithText(FilledButton, '重置'));
    await pumpUntilFound(tester, find.text('生效中'));

    expect(client.log, contains('ROTATE:n1'));
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

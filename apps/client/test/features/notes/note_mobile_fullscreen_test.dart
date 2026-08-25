// F1 移动端笔记全屏编辑模式 widget 测试：
//   * NoteMobileTitleRow —— 返回键、操作图标收进 ⋯ 菜单、菜单项回调；
//   * NoteSaveIndicator —— 保存中→已保存 2s 淡出、失败常驻红色；
//   * NoteEditFallbackPage —— 深链兜底页渲染 + 返回列表导航。

import 'package:biumind/core/editor/page_autosave.dart';
import 'package:biumind/data/notes_repository.dart';
import 'package:biumind/features/notes/presentation/note_editor_view.dart'
    show NoteMobileTitleRow, NoteSaveIndicator;
import 'package:biumind/features/notes/presentation/notes_home_page.dart'
    show NoteEditFallbackPage;
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';

RepoNote _repoNote(String id) => RepoNote(
      id: id,
      title: 't-$id',
      contentMd: '',
      isTodo: false,
      position: 0.0,
      version: 1,
      updatedAt: DateTime.utc(2026, 8, 25),
    );

void main() {
  group('NoteMobileTitleRow（手机全屏标题行）', () {
    late List<String> called;

    Widget buildRow() {
      called = <String>[];
      final router = GoRouter(
        initialLocation: '/edit',
        routes: [
          GoRoute(
            path: '/notes',
            pageBuilder: (_, _) =>
                const NoTransitionPage<void>(child: Text('LIST')),
          ),
          GoRoute(
            path: '/edit',
            pageBuilder: (_, _) => NoTransitionPage<void>(
              child: Scaffold(
                body: NoteMobileTitleRow(
                  controller: TextEditingController(),
                  onChanged: (_) {},
                  onTrash: () => called.add('trash'),
                  note: _repoNote('n1'),
                  onToggleTodo: () => called.add('todo'),
                  onInsertImage: () => called.add('image'),
                  onInsertAttachment: () => called.add('attachment'),
                  onShowHistory: () => called.add('history'),
                  onPromoteToWiki: () => called.add('promote'),
                  onEditTags: () => called.add('tags'),
                ),
              ),
            ),
          ),
        ],
      );
      return MaterialApp.router(routerConfig: router);
    }

    testWidgets('返回键在最左，操作图标全部收进 ⋯ 菜单', (tester) async {
      await tester.pumpWidget(buildRow());
      // 返回键存在
      expect(find.byIcon(Icons.arrow_back), findsOneWidget);
      // 平铺的操作图标不存在（全部进菜单）
      expect(find.byIcon(Icons.image_outlined), findsNothing);
      expect(find.byIcon(Icons.attach_file), findsNothing);
      expect(find.byIcon(Icons.delete_outline), findsNothing);
      expect(find.byIcon(Icons.more_vert), findsOneWidget);

      // ⋯ 菜单包含全部 7 项
      await tester.tap(find.byIcon(Icons.more_vert));
      await tester.pumpAndSettle();
      expect(find.text('插入图片'), findsOneWidget);
      expect(find.text('插入附件'), findsOneWidget);
      expect(find.text('转为待办'), findsOneWidget);
      expect(find.text('编辑标签'), findsOneWidget);
      expect(find.text('历史版本'), findsOneWidget);
      expect(find.text('转入知识库'), findsOneWidget);
      expect(find.text('移入回收站'), findsOneWidget);
    });

    testWidgets('菜单项触发对应回调', (tester) async {
      await tester.pumpWidget(buildRow());
      Future<void> tapItem(String label, String expectCalled) async {
        await tester.tap(find.byIcon(Icons.more_vert));
        await tester.pumpAndSettle();
        await tester.tap(find.text(label));
        await tester.pumpAndSettle();
        expect(called, contains(expectCalled));
      }

      await tapItem('插入图片', 'image');
      await tapItem('编辑标签', 'tags');
      await tapItem('移入回收站', 'trash');
    });

    testWidgets('返回键 pop 回上一页（路由栈内有页时）', (tester) async {
      final router = GoRouter(
        initialLocation: '/notes',
        routes: [
          GoRoute(
            path: '/notes',
            pageBuilder: (_, _) => const NoTransitionPage<void>(
              child: Text('LIST'),
            ),
            routes: [
              GoRoute(
                path: 'edit',
                pageBuilder: (_, _) => NoTransitionPage<void>(
                  child: Scaffold(
                    body: NoteMobileTitleRow(
                      controller: TextEditingController(),
                      onChanged: (_) {},
                      onTrash: () {},
                      note: _repoNote('n1'),
                      onToggleTodo: () {},
                      onInsertImage: () {},
                      onInsertAttachment: () {},
                      onShowHistory: () {},
                      onPromoteToWiki: null,
                    ),
                  ),
                ),
              ),
            ],
          ),
        ],
      );
      await tester.pumpWidget(MaterialApp.router(routerConfig: router));
      router.push('/notes/edit');
      await tester.pumpAndSettle();
      expect(find.byIcon(Icons.arrow_back), findsOneWidget);
      await tester.tap(find.byIcon(Icons.arrow_back));
      await tester.pumpAndSettle();
      expect(find.text('LIST'), findsOneWidget);
    });
  });

  group('NoteSaveIndicator（瞬时保存指示）', () {
    Widget buildIndicator(AutoSaveStatus status) => MaterialApp(
          home: Scaffold(
            body: NoteSaveIndicator(
              contentStatus: status,
              titleStatus: AutoSaveStatus.idle,
              errorMessage: status == AutoSaveStatus.error ? '网络断开' : null,
            ),
          ),
        );

    testWidgets('保存中 → 已保存 2s 后淡出', (tester) async {
      await tester.pumpWidget(buildIndicator(AutoSaveStatus.saving));
      expect(find.text('保存中…'), findsOneWidget);

      await tester.pumpWidget(buildIndicator(AutoSaveStatus.saved));
      expect(find.text('已保存'), findsOneWidget);
      expect(
        tester.widget<AnimatedOpacity>(find.byType(AnimatedOpacity)).opacity,
        1,
      );

      // 2s 停留 + 300ms 淡出后不可见
      await tester.pump(const Duration(milliseconds: 2000));
      await tester.pump(const Duration(milliseconds: 300));
      expect(
        tester.widget<AnimatedOpacity>(find.byType(AnimatedOpacity)).opacity,
        0,
      );
    });

    testWidgets('失败常驻（错误文案可见，不随时间消失）', (tester) async {
      await tester.pumpWidget(buildIndicator(AutoSaveStatus.error));
      expect(find.text('保存失败：网络断开'), findsOneWidget);
      await tester.pump(const Duration(seconds: 5));
      await tester.pump(const Duration(milliseconds: 300));
      expect(
        tester.widget<AnimatedOpacity>(find.byType(AnimatedOpacity)).opacity,
        1,
      );
      expect(find.text('保存失败：网络断开'), findsOneWidget);
    });
  });

  group('NoteEditFallbackPage（深链兜底）', () {
    testWidgets('提示 + 返回列表入口，点击导航到 /notes', (tester) async {
      final router = GoRouter(
        initialLocation: '/fallback',
        routes: [
          GoRoute(
            path: '/notes',
            pageBuilder: (_, _) =>
                const NoTransitionPage<void>(child: Text('LIST')),
          ),
          GoRoute(
            path: '/fallback',
            pageBuilder: (_, _) =>
                const NoTransitionPage<void>(child: NoteEditFallbackPage()),
          ),
        ],
      );
      await tester.pumpWidget(MaterialApp.router(routerConfig: router));
      expect(find.text('笔记不存在或已删除'), findsOneWidget);
      await tester.tap(find.text('返回列表'));
      await tester.pumpAndSettle();
      expect(find.text('LIST'), findsOneWidget);
    });
  });
}

// WikiShell / WikiPage / ProjectBrowserPage 手机形态测试 — 手机适配 P1 (§4.6)。
//
// 覆盖:
//   * 桌面 (1200×800): 220px rail 常驻、无 ☰、内容宽 = 屏宽-221、⌘K 提示在;
//   * 手机 (390×844): rail 收进 WikiShell 自己的 Drawer (宽 304)、☰ 可开、
//     内容全宽、⌘K 提示隐藏、点条目先关抽屉再跳转;
//   * WikiPage: 手机 280px 左栏收进 bottom sheet、大纲进 bottom sheet;
//     桌面双栏 + 内联 OutlinePanel 原样。
//   * ProjectBrowserPage: 手机 320px master 收进 bottom sheet (顶行列表
//     按钮, 选中自动关); 桌面 master/detail 双栏原样。
//
// 后端依赖全部用 fake/override 隔掉 (wikiController / activity 计数 /
// pending 写入数 / settings repo), 无网络。

import 'package:biumind/app/theme/theme.dart';
import 'package:biumind/data/wiki_providers.dart';
import 'package:biumind/data/wiki_repository.dart';
import 'package:biumind/features/wiki/application/wiki_controller.dart';
import 'package:biumind/features/wiki/presentation/activity/activity_provider.dart';
import 'package:biumind/features/wiki/presentation/reviews_page.dart';
import 'package:biumind/features/wiki/presentation/project_browser/project_browser_page.dart';
import 'package:biumind/features/wiki/presentation/sources/sources_page.dart';
import 'package:biumind/features/wiki/presentation/wiki_page.dart';
import 'package:biumind/features/wiki/shell/wiki_nav_rail.dart';
import 'package:biumind/features/wiki/shell/wiki_shell.dart';
import 'package:biumind/services/settings_repo.dart';
import 'package:flutter/material.dart';
import 'package:biumind/l10n/app_localizations.dart';
import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';
import 'package:shared_preferences/shared_preferences.dart';

class _FakeWikiController extends WikiController {
  static const _project = RepoProject(id: 'p1', name: 'Demo');
  static final _page = RepoPage(
    id: 'pg1',
    projectId: 'p1',
    title: '第一页',
    version: 1,
    updatedAt: DateTime(2026),
    // project_browser 的分组树按 frontmatter.type 聚合且默认只展开
    // kDefaultExpandedPageTypes — 给 'entity' 让测试行默认可见。
    frontmatter: const {'type': 'entity'},
  );
  static final _blocks = <RepoBlock>[
    RepoBlock(
      id: 'b1',
      pageId: 'pg1',
      position: 1,
      type: 'heading',
      content: const {'text': '第一章', 'level': 1},
      version: 1,
    ),
    RepoBlock(
      id: 'b2',
      pageId: 'pg1',
      position: 2,
      type: 'heading',
      content: const {'text': '第二章', 'level': 2},
      version: 1,
    ),
  ];

  WikiState _state() => WikiState(
        projects: const [_project],
        activeProject: _project,
        pages: [_page],
        activePage: _page,
        blocks: _blocks,
      );

  @override
  Future<WikiState> build() async => _state();

  // 避开真实 selectPage 的 repo 依赖 — 测试里 repo 为 null。
  @override
  Future<void> selectPage(RepoPage page) async {
    state = AsyncData(_state());
  }
}

/// 公共 overrides: 无凭据 (repo null → related/backlinks 静默空)、
/// activity 计数 0、pending 写入 0、settings 走内存 repo。
List<Override> _overrides() => [
      wikiControllerProvider.overrideWith(() => _FakeWikiController()),
      activityFeedCountProvider.overrideWith((ref, _) => 0),
      pendingWriteCountProvider.overrideWith((ref) => Stream.value(0)),
      settingsRepoProvider.overrideWithValue(InMemorySettingsRepo()),
    ];

Widget _wrap(GoRouter router) {
  return ProviderScope(
    overrides: _overrides(),
    // 真实主题 (注册 BiuColors extension) — wiki 组件依赖它。
    child: MaterialApp.router(
      localizationsDelegates: const [
        AppLocalizations.delegate,
        GlobalMaterialLocalizations.delegate,
        GlobalWidgetsLocalizations.delegate,
        GlobalCupertinoLocalizations.delegate,
      ],
      supportedLocales: AppLocalizations.supportedLocales,
      theme: buildTheme(
        palette: PaletteId.inkblueOrange,
        mode: Brightness.light,
        fontSize: FontSize.small,
      ),
      routerConfig: router,
    ),
  );
}

/// WikiShell 专用路由: stub child 带 key, 便于量内容区宽度。
GoRouter _shellRouter() => GoRouter(
      initialLocation: '/wiki/p/p1',
      routes: [
        ShellRoute(
          builder: (ctx, state, child) => WikiShell(child: child),
          routes: [
            GoRoute(
              path: '/wiki',
              builder: (_, _) => Container(key: const Key('stub-home')),
            ),
            GoRoute(
              path: '/wiki/p/:pid',
              builder: (_, _) => Container(key: const Key('stub-project')),
            ),
          ],
        ),
      ],
    );

void _setView(WidgetTester tester, Size size) {
  tester.view.physicalSize = size;
  tester.view.devicePixelRatio = 1.0;
  addTearDown(tester.view.resetPhysicalSize);
  addTearDown(tester.view.resetDevicePixelRatio);
}

/// WikiStatusBar 的 BiuPulseDot 是 2.4s 无限循环呼吸动画，pumpAndSettle
/// 永不 settle —— 改用手动帧（时长覆盖 route / drawer / bottom-sheet 动画）。
Future<void> _pumpFrames(WidgetTester tester) async {
  await tester.pump();
  await tester.pump(const Duration(milliseconds: 400));
  await tester.pump(const Duration(milliseconds: 400));
}

void main() {
  group('WikiShell', () {
    testWidgets('桌面: rail 常驻 + 无 ☰ + 内容宽 979 + ⌘K 提示在', (tester) async {
      _setView(tester, const Size(1200, 800));
      await tester.pumpWidget(_wrap(_shellRouter()));
      await _pumpFrames(tester);

      expect(find.byType(WikiNavRail), findsOneWidget);
      expect(find.byIcon(Icons.menu), findsNothing);
      // 1200 - 220 rail - 1 divider = 979。
      expect(tester.getSize(find.byKey(const Key('stub-project'))).width, 979);
      expect(find.text('已同步'), findsOneWidget);
      expect(find.text('命令面板'), findsOneWidget);
    });

    testWidgets('手机: rail 进 drawer, ☰ 可开, 内容全宽, 点条目关抽屉跳转',
        (tester) async {
      _setView(tester, const Size(390, 844));
      await tester.pumpWidget(_wrap(_shellRouter()));
      await _pumpFrames(tester);

      // rail 不常驻; 内容全宽; 状态栏保留但 ⌘K 提示隐藏。
      expect(find.byType(WikiNavRail), findsNothing);
      expect(find.byIcon(Icons.menu), findsOneWidget);
      expect(tester.getSize(find.byKey(const Key('stub-project'))).width, 390);
      expect(find.text('已同步'), findsOneWidget);
      expect(find.text('命令面板'), findsNothing);

      // ☰ 开 WikiShell 自己的 drawer (宽 min(304, 390×0.85) = 304)。
      await tester.tap(find.byIcon(Icons.menu));
      await _pumpFrames(tester);
      expect(find.byType(WikiNavRail), findsOneWidget);
      expect(tester.getSize(find.byType(Drawer)).width, 304);

      // 点「工作区」: 先关抽屉再 go /wiki。
      await tester.tap(find.text('工作区'));
      await _pumpFrames(tester);
      expect(find.byType(WikiNavRail), findsNothing);
      expect(find.byKey(const Key('stub-home')), findsOneWidget);

      // 工作区模式 drawer 也能开 (含「退出登录」)。
      await tester.tap(find.byIcon(Icons.menu));
      await _pumpFrames(tester);
      expect(find.text('退出登录'), findsOneWidget);
    });
  });

  group('WikiPage', () {
    GoRouter pageRouter() => GoRouter(
          initialLocation: '/wiki',
          routes: [
            // Scaffold 提供 Material 祖先 (_LeftPane 的 ListTile 需要) ——
            // 生产上由 WikiShell 的 Scaffold 提供。
            GoRoute(
              path: '/wiki',
              builder: (_, _) => const Scaffold(body: WikiPage()),
            ),
          ],
        );

    testWidgets('桌面: 280 左栏常驻 + 内联大纲, 无列表按钮', (tester) async {
      _setView(tester, const Size(1200, 800));
      await tester.pumpWidget(_wrap(pageRouter()));
      await _pumpFrames(tester);

      // 左栏项目切换器 + 面包屑各出现一次项目名 → 左栏在。
      expect(find.text('Demo'), findsNWidgets(2));
      expect(find.byIcon(Icons.format_list_bulleted), findsNothing);
      // 2 个 heading → 内联 OutlinePanel (标题「大纲」)。
      expect(find.text('大纲'), findsOneWidget);
    });

    testWidgets('手机: 左栏收进 bottom sheet, 大纲进 bottom sheet',
        (tester) async {
      _setView(tester, const Size(390, 844));
      await tester.pumpWidget(_wrap(pageRouter()));
      await _pumpFrames(tester);

      // 内容单栏: 项目名只剩面包屑一处; 内联大纲不在, 入口按钮在。
      expect(find.text('Demo'), findsOneWidget);
      expect(find.text('大纲'), findsNothing);
      expect(find.byIcon(Icons.format_list_bulleted), findsOneWidget);
      expect(find.byIcon(Icons.list_alt), findsOneWidget);

      // 页面列表 sheet: 打开 → 选中 → 自动关。
      await tester.tap(find.byIcon(Icons.format_list_bulleted));
      await _pumpFrames(tester);
      expect(find.byType(BottomSheet), findsOneWidget);
      // 面包屑 + 标题 + sheet 列表项（阅读视图渲的是 block 内容, 不含页名）。
      expect(find.text('第一页'), findsNWidgets(3));
      await tester.tap(find.text('第一页').last);
      await _pumpFrames(tester);
      expect(find.byType(BottomSheet), findsNothing);

      // 大纲 sheet: 两级标题在 sheet 内可见 (阅读模式纯查看, 不可点)。
      // 注意阅读视图本身也渲「第一章/第二章」, 断言必须限定在 sheet 内。
      await tester.tap(find.byIcon(Icons.list_alt));
      await _pumpFrames(tester);
      expect(find.byType(BottomSheet), findsOneWidget);
      expect(
        find.descendant(
          of: find.byType(BottomSheet),
          matching: find.text('第一章'),
        ),
        findsOneWidget,
      );
      expect(
        find.descendant(
          of: find.byType(BottomSheet),
          matching: find.text('第二章'),
        ),
        findsOneWidget,
      );
      await tester.tapAt(const Offset(195, 50)); // 点遮罩关掉
      await _pumpFrames(tester);
      expect(find.byType(BottomSheet), findsNothing);
    });
  });

  group('ProjectBrowserPage', () {
    // 与生产一致挂 Scaffold 祖先 (InkWell/IconButton 需要 Material);
    // pages/:pageId 子路由供 sheet 里点行后 context.go 落位。
    GoRouter browserRouter() => GoRouter(
          initialLocation: '/wiki/p/p1',
          routes: [
            GoRoute(
              path: '/wiki/p/:pid',
              builder: (_, state) => Scaffold(
                body: ProjectBrowserPage(
                  projectId: state.pathParameters['pid'] ?? '',
                ),
              ),
              routes: [
                GoRoute(
                  path: 'pages/:pageId',
                  builder: (_, state) => Scaffold(
                    body: ProjectBrowserPage(
                      projectId: state.pathParameters['pid'] ?? '',
                      pageId: state.pathParameters['pageId'],
                    ),
                  ),
                ),
              ],
            ),
          ],
        );

    testWidgets('桌面: 320 master 常驻 + detail 并排, 无列表按钮', (tester) async {
      SharedPreferences.setMockInitialValues(<String, Object>{});
      _setView(tester, const Size(1200, 800));
      await tester.pumpWidget(_wrap(browserRouter()));
      await _pumpFrames(tester);

      // master 在: 项目名头 + 过滤框; 页面同时出现在 master 行和 detail 标题。
      expect(find.text('Demo'), findsOneWidget);
      expect(find.text('过滤页面…'), findsOneWidget);
      expect(find.text('第一页'), findsNWidgets(2));
      expect(find.byIcon(Icons.format_list_bulleted), findsNothing);
      // PhoneBackButton 桌面 shrink 不占位。
      expect(find.byIcon(Icons.arrow_back), findsNothing);
    });

    testWidgets('手机: master 收进 bottom sheet, 选中自动关', (tester) async {
      SharedPreferences.setMockInitialValues(<String, Object>{});
      _setView(tester, const Size(390, 844));
      await tester.pumpWidget(_wrap(browserRouter()));
      await _pumpFrames(tester);

      // detail 全宽: 顶行有 ←(PhoneBackButton) + 项目名 + 列表入口; master 不在树。
      expect(find.byIcon(Icons.arrow_back), findsOneWidget);
      expect(find.text('Demo'), findsOneWidget);
      expect(find.byIcon(Icons.format_list_bulleted), findsOneWidget);
      expect(find.text('过滤页面…'), findsNothing);
      expect(find.text('第一页'), findsOneWidget); // 仅 detail 标题

      // 列表 sheet: 打开 → master (过滤框 + 分组页面树) 在内。
      await tester.tap(find.byIcon(Icons.format_list_bulleted));
      await _pumpFrames(tester);
      expect(find.byType(BottomSheet), findsOneWidget);
      expect(
        find.descendant(
          of: find.byType(BottomSheet),
          matching: find.text('过滤页面…'),
        ),
        findsOneWidget,
      );
      final rowInSheet = find.descendant(
        of: find.byType(BottomSheet),
        matching: find.text('第一页'),
      );
      expect(rowInSheet, findsOneWidget);

      // 选中 → 先关 sheet 再 go pages/:pageId; detail 仍是该页。
      await tester.tap(rowInSheet);
      await _pumpFrames(tester);
      expect(find.byType(BottomSheet), findsNothing);
      expect(find.text('第一页'), findsOneWidget);
    });
  });

  group('子页头部 PhoneBackButton (§3.3)', () {
    // 子页 build 路径不触 GoRouter / 网络 (repo null → 空列表态),
    // 直接 MaterialApp + Scaffold 祖先即可。
    Widget plain(Widget page, Key key) {
      return ProviderScope(
        overrides: _overrides(),
        child: MaterialApp(
    localizationsDelegates: const [
      AppLocalizations.delegate,
      GlobalMaterialLocalizations.delegate,
      GlobalWidgetsLocalizations.delegate,
      GlobalCupertinoLocalizations.delegate,
    ],
    supportedLocales: AppLocalizations.supportedLocales,
          key: key,
          theme: buildTheme(
            palette: PaletteId.inkblueOrange,
            mode: Brightness.light,
            fontSize: FontSize.small,
          ),
          home: Scaffold(body: page),
        ),
      );
    }

    testWidgets('手机: sources / reviews 子页头部出现 ←', (tester) async {
      _setView(tester, const Size(390, 844));

      await tester.pumpWidget(
        plain(const SourcesPage(projectId: 'p1'), const ValueKey('src')),
      );
      await _pumpFrames(tester);
      expect(find.byIcon(Icons.arrow_back), findsOneWidget);

      // 换根 key 强制重建 (同类型根 widget 复用元素的问题见
      // form_factor_test 注释)。
      await tester.pumpWidget(
        plain(const ReviewsPage(), const ValueKey('reviews')),
      );
      await _pumpFrames(tester);
      expect(find.byIcon(Icons.arrow_back), findsOneWidget);
    });

    testWidgets('桌面: sources / reviews 子页头部 ← shrink 不出现', (tester) async {
      _setView(tester, const Size(1200, 800));

      await tester.pumpWidget(
        plain(const SourcesPage(projectId: 'p1'), const ValueKey('src')),
      );
      await _pumpFrames(tester);
      expect(find.byIcon(Icons.arrow_back), findsNothing);

      await tester.pumpWidget(
        plain(const ReviewsPage(), const ValueKey('reviews')),
      );
      await _pumpFrames(tester);
      expect(find.byIcon(Icons.arrow_back), findsNothing);
    });
  });
}

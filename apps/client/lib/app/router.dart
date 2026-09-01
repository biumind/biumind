// App shell — left sidebar (compact, custom) + content area.
//
// Replaces NavigationRail with a hand-rolled list because the default
// rail can't hit the spacing / look-and-feel we want (per the
// UI-Design-System spec):
//   * Icons + labels stacked horizontally (not below the icon)
//   * Active item: soft purple pill (BiuTokens.purpleSoft)
//   * 32px row height, 13px label
//   * Bottom-pinned connection status + settings shortcut

import 'dart:convert';

import 'package:flutter/foundation.dart'
    show defaultTargetPlatform, kIsWeb, TargetPlatform;
import 'package:flutter/material.dart';
import 'package:flutter/services.dart' show LogicalKeyboardKey;
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../core/layout/form_factor.dart';
import '../core/layout/phone_tab_bar.dart';
import '../core/docproc/docproc_view.dart';
import '../core/platform/platform_caps.dart';
import '../core/platform/window_drag.dart';
import '../core/ui/biu_pulse_dot.dart';
import '../core/ui/popup_position.dart';
import 'sidebar_mode.dart';
import '../features/auth/presentation/login_page.dart';
import '../features/chat/presentation/v2/threads_shell_page.dart';
import '../features/code/code_module.dart';
import '../features/creation/presentation/creation_shell.dart';
import '../features/creation/presentation/widgets/credit_indicator.dart';
import '../features/creation/presentation/pages/gallery_page.dart' as creation;
import '../features/creation/presentation/pages/inspiration_page.dart' as creation;
import '../features/creation/presentation/pages/my_works_page.dart' as creation;
import '../features/creation/presentation/pages/studio_page.dart' as creation;
// TODO(memory): 后端 services/brain/internal/memory 未完全实现, 先下线客户端全部入口.
// 恢复时取消 4 处注释: 本 import + /memory 路由 + _systemItems 的 memory NavItem
// + sidebar_customize_page._systemDefaults 的 ('memory','Memory').
// import '../features/memory/presentation/memory_page.dart';
import '../features/profile/presentation/profile_page.dart';
import '../features/settings/presentation/settings_page.dart';
import '../features/settings/presentation/sign_out_dialog.dart';
import '../features/settings/presentation/device_management_page.dart';
import '../features/search/search_page.dart';
import '../features/notes/presentation/notes_home_page.dart';
import '../features/notes/presentation/notes_trash_page.dart';
import '../data/api/apps_client.dart';
import '../data/api/sidebar_client.dart';
import '../data/apps_providers.dart';
import '../data/sidebar_badges.dart';
import '../data/sidebar_providers.dart';
import '../features/apps/presentation/app_icon_resolver.dart';
import '../features/apps/host/app_view_host.dart';
import '../features/apps/presentation/app_detail_page.dart';
import '../features/apps/presentation/app_settings_page.dart';
import '../features/apps/presentation/apps_page.dart';
import '../features/apps/presentation/repo_install_page.dart';
import '../features/apps/presentation/repo_window_page.dart';
import '../features/apps/presentation/sidebar_customize_page.dart';
import '../features/membership/presentation/pages/checkout_page.dart';
import '../features/membership/presentation/pages/coupon_redeem_page.dart';
import '../features/membership/presentation/pages/membership_center_page.dart';
import '../features/membership/presentation/pages/order_history_page.dart';
import '../features/membership/presentation/pages/plan_compare_page.dart';
import '../features/membership/presentation/pages/referral_page.dart';
import '../features/skills/presentation/skills_page.dart';
import '../features/splash/presentation/splash_page.dart';
import '../features/wiki/presentation/reviews_page.dart';
import '../features/wiki/data/docproc_queue_controller.dart';
import '../features/wiki/presentation/chat/project_chat_page.dart';
import '../features/wiki/presentation/graph/project_graph_page.dart';
import '../features/wiki/presentation/mirror/mirror_page.dart' as wikimirror;
import '../features/wiki/presentation/oauth_connect/oauth_connect_page.dart';
import '../features/wiki/presentation/research/research_page.dart' as wikiresearch;
import '../features/wiki/presentation/suggestions/suggestions_page.dart';
import '../features/wiki/presentation/projects/projects_page.dart';
import '../features/wiki/presentation/project_browser/project_browser_page.dart';
import '../features/wiki/presentation/search/project_search_page.dart';
import '../features/wiki/presentation/sources/ingest_stream_page.dart';
import '../features/wiki/presentation/sources/sources_page.dart';
import '../features/wiki/shell/wiki_shell.dart';
import '../l10n/app_localizations.dart';
import '../services/auth_service.dart';
import '../core/ui/biu_section_label.dart';
import '../shared/brand/biu_logo.dart';
import '../shared/connectivity/offline_badge.dart';
import '../shared/connectivity/security_alert_banner.dart';
import '../features/update/presentation/update_banner.dart';
import 'theme.dart';

/// Root navigator key — used when something outside the widget tree needs
/// a Material context (e.g. token_manager bumps sessionExpiredCount and
/// main.dart listens to show a "session expired" dialog).
final rootNavigatorKey = GlobalKey<NavigatorState>();

/// 全局 ScaffoldMessenger key — 让非 widget 代码(如 _http_helpers 的计费
/// 拦截 hook)能从任意位置弹 SnackBar(配额/限流轻提示)。
final rootScaffoldMessengerKey = GlobalKey<ScaffoldMessengerState>();

/// 没登录也允许浏览的 route 前缀。出现在 redirect() 第二阶段 (auth check)
/// 决策中: matchedLocation 命中任意一条 → 跳过 /login 重定向。
///
/// 当前白名单:
///   * `/skills`   — 技能广场: 用户可以看清楚有哪些 skill 再决定登录
///   * `/settings` — 设置: 外观 / 语言 / 端点等本地偏好不依赖登录
///
/// 顶层放 const 函数, 测试可直接 import 验证 (避免 router 整体真跑)。
bool isPublicRoute(String location) {
  return location.startsWith('/skills') ||
      location.startsWith('/settings') ||
      // /connect 是外部 AI 客户端 OAuth 入口，可能在用户没登录时被深链
      // 进入；页面内部会引导用户先登录（authorize/info 401 时显示 hint）。
      location.startsWith('/connect') ||
      // /suggestions 公开列表，未登录也允许浏览；提交反馈时再要求登录。
      location.startsWith('/suggestions');
}

/// 顶层目的地 (shell tab) 的 Page: 永远无转场 — tab 切换应当即时,
/// 桌面侧边栏 / VS Code 心智模型; 手机端 Drawer 切换同样不播转场。
Page<void> tabPage(Widget child) => NoTransitionPage<void>(child: child);

/// 子页 (非顶层目的地) 的 Page: 移动平台 MaterialPage —— 转场动画 +
/// iOS 右滑返回 + Android 系统返回栈感知; 桌面/Web 与 tabPage() 一致
/// (NoTransitionPage, tab 模型不变)。导航设计 §3.2。
Page<void> subPage(Widget child) {
  switch (defaultTargetPlatform) {
    case TargetPlatform.iOS:
    case TargetPlatform.android:
      return MaterialPage<void>(child: child);
    default:
      return NoTransitionPage<void>(child: child);
  }
}

GoRouter buildRouter(ProviderContainer container) {
  return GoRouter(
    navigatorKey: rootNavigatorKey,
    initialLocation: '/splash',
    // 重定向顺序: splash 不动 → 没 creds 时除白名单外跳 /login →
    // 已登录还在 /login 跳 /chat。
    //
    // 启动即登录: 未登录用户的首屏就是 /login (splash 动画后),
    // 不再插引导向导。白名单 (isPublicRoute) 内的页面例外。
    // /chat 不在白名单 — 真要聊天必须登录, 维持产品价值边界。
    redirect: (context, state) {
      final loc = state.matchedLocation;
      // Splash 不 redirect — 等动画完成后自己 pushReplacement
      if (loc == '/splash') return null;
      // Web 无编码模块(code UI/daemon 树已条件编译剔除) — /code 深链兜底到
      // /chat, 避免 web 上命中不存在的路由掉进错误页。
      if (!codeModuleEnabled && loc.startsWith('/code')) return '/chat';
      final creds = container.read(hubCredentialsProvider);
      final loggingIn = loc == '/login';
      if (creds == null && !loggingIn && !isPublicRoute(loc)) return '/login';
      if (creds != null && loggingIn) return '/chat';
      return null;
    },
    // Subscribe to creds changes so the redirect runs on sign-in / sign-out.
    refreshListenable: _RouterListenable(container),
    routes: [
      GoRoute(
        path: '/splash',
        // MaterialPage 才能触发 Hero transition (NoTransitionPage 跳过 Hero)
        pageBuilder: (_, _) => const MaterialPage<void>(child: SplashPage()),
      ),
      GoRoute(
        path: '/login',
        // MaterialPage — splash → login 期间 Flutter Hero 接管 mark 飞跃过渡
        pageBuilder: (_, _) => const MaterialPage<void>(child: LoginPage()),
      ),
      // /suggestions —— 用户反馈 / 路线图（顶层，不进 sidebar）
      GoRoute(
        path: '/suggestions',
        pageBuilder: (_, _) =>
            const MaterialPage<void>(child: SuggestionsPage()),
      ),
      // /connect?ar=... —— OAuth 同意页（外部 AI 客户端深链入口）
      // brain 端 /v1/wiki/oauth/authorize 302 redirect 到这里。
      GoRoute(
        path: '/connect',
        pageBuilder: (_, state) {
          final ar = state.uri.queryParameters['ar'] ?? '';
          return MaterialPage<void>(child: OAuthConnectPage(ar: ar));
        },
      ),
      // /apps/repo-window/:installId —— Repo App 伪独立窗口（M1.14）。
      // 在 ShellRoute 之外：无侧边栏的全屏页（先例 /suggestions，
      // MaterialPage 挂 rootNavigatorKey）。
      GoRoute(
        path: '/apps/repo-window/:installId',
        pageBuilder: (_, state) => MaterialPage<void>(
          child: RepoWindowPage(
            installId: state.pathParameters['installId'] ?? '',
          ),
        ),
      ),
      // /notes/edit/:noteId —— 移动端笔记全屏编辑（F1，方案 A 可深链）。
      // 在 ShellRoute 之外（先例 /apps/repo-window）：无 PhoneTabBar 的
      // 全屏页；Page 类型走 subPage 分流（手机 MaterialPage 拿转场 +
      // 右滑返回）。桌面宽屏深链 → redirect /notes（三栏内嵌编辑，
      // 无全屏形态；布局断点判定，不用平台 OS 判定）。
      GoRoute(
        path: '/notes/edit/:noteId',
        redirect: (context, state) {
          if (!isPhoneLayout(context)) return '/notes';
          return null;
        },
        pageBuilder: (context, state) {
          final noteId = state.pathParameters['noteId'] ?? '';
          if (noteId.isEmpty) return subPage(const NoteEditFallbackPage());
          return subPage(NoteEditorPage(noteId: noteId));
        },
      ),
      ShellRoute(
        builder: (ctx, state, child) => _AppShell(child: child),
        routes: [
          GoRoute(path: '/chat', pageBuilder: (_, _) => tabPage(const ThreadsShellPage())),
          // 知识库（wiki）— 内部走 knowcode 风格的两级 shell：
          //   /wiki                              工作区/项目列表
          //   /wiki/p/:pid                       项目浏览器（page list 分组）
          //   /wiki/p/:pid/pages/:pageId         单页深链
          //   /wiki/p/:pid/sources               源文件列表 + 上传 + ingest
          //   /wiki/p/:pid/ingest/tasks/:tid     ingest 进度 SSE 页
          //   /wiki/p/:pid/search                项目内三路搜索
          //   /wiki/p/:pid/chat                  项目内对话（独立通道）
          //   /wiki/p/:pid/research              Deep Research 列表 + 新建
          //   /wiki/p/:pid/reviews               审阅队列（dedup/lint/sweep 产物 + 扫描入口）
          //   /wiki/p/:pid/dedup                 重复页扫描 + 合并
          //   /wiki/p/:pid/mirror                项目导出 zip
          //   /wiki/p/:pid/graph                 项目图谱（force-directed + Louvain 着色）
          // NavRail「LLM 设置」/「全局设置」/「反馈」直接跳顶层 /settings
          // 或 /suggestions，不在 wiki shell 子树内。
          ShellRoute(
            builder: (ctx, state, child) => WikiShell(child: child),
            routes: [
              GoRoute(
                path: '/wiki',
                pageBuilder: (_, _) => tabPage(const ProjectsPage()),
              ),
              GoRoute(
                path: '/wiki/p/:pid',
                pageBuilder: (_, state) => subPage(ProjectBrowserPage(
                  projectId: state.pathParameters['pid'] ?? '',
                )),
                routes: [
                  GoRoute(
                    path: 'pages/:pageId',
                    pageBuilder: (_, state) => subPage(ProjectBrowserPage(
                      projectId: state.pathParameters['pid'] ?? '',
                      pageId: state.pathParameters['pageId'],
                    )),
                  ),
                  GoRoute(
                    path: 'sources',
                    pageBuilder: (_, state) => subPage(SourcesPage(
                      projectId: state.pathParameters['pid'] ?? '',
                    )),
                  ),
                  GoRoute(
                    path: 'ingest/tasks/:tid',
                    pageBuilder: (_, state) => subPage(IngestStreamPage(
                      projectId: state.pathParameters['pid'] ?? '',
                      taskId: state.pathParameters['tid'] ?? '',
                    )),
                  ),
                  GoRoute(
                    path: 'search',
                    pageBuilder: (_, state) => subPage(ProjectSearchPage(
                      projectId: state.pathParameters['pid'] ?? '',
                    )),
                  ),
                  GoRoute(
                    path: 'graph',
                    pageBuilder: (_, state) => subPage(ProjectGraphPage(
                      projectId: state.pathParameters['pid'] ?? '',
                    )),
                  ),
                  GoRoute(
                    path: 'chat',
                    pageBuilder: (_, state) => subPage(ProjectChatPage(
                      projectId: state.pathParameters['pid'] ?? '',
                    )),
                  ),
                  GoRoute(
                    path: 'research',
                    pageBuilder: (_, state) => subPage(wikiresearch.ResearchPage(
                      projectId: state.pathParameters['pid'] ?? '',
                    )),
                  ),
                  GoRoute(
                    path: 'reviews',
                    // biumind 现版 ReviewsPage 走 reviewsControllerProvider，
                    // 自动拿 wiki_controller.activeProject —— 不需要 projectId。
                    pageBuilder: (_, _) => subPage(const ReviewsPage()),
                  ),
                  GoRoute(
                    path: 'mirror',
                    pageBuilder: (_, state) => subPage(wikimirror.MirrorPage(
                      projectId: state.pathParameters['pid'] ?? '',
                    )),
                  ),
                  // /llm-settings 不挂子路由 —— NavRail 直接跳顶层 /settings。
                ],
              ),
            ],
          ),
          // 创作模块 (P4) — 内部二级 shell (220px NavRail / 移动 SegmentedTab).
          //   /creation              灵感首页 (Hero + GenerationPanel + 推荐瀑布流)
          //   /creation/center       创作中心 (纯生成面板)
          //   /creation/works        我的作品 (多选 + 批处理)
          //   /creation/works/:id    作品详情 (P4b-5)
          //   /creation/gallery      创意广场
          //   /creation/gallery/:id  画廊详情 (P4b-5)
          //
          // W7 IA 重设计: /creation/recharge 删除, 充值统一到 /membership.
          ShellRoute(
            builder: (ctx, state, child) => CreationShell(child: child),
            routes: [
              GoRoute(
                path: '/creation',
                pageBuilder: (_, _) => tabPage(const creation.InspirationPage()),
              ),
              GoRoute(
                path: '/creation/center',
                pageBuilder: (_, _) => tabPage(const creation.StudioPage()),
              ),
              GoRoute(
                path: '/creation/works',
                pageBuilder: (_, _) => tabPage(const creation.MyWorksPage()),
              ),
              GoRoute(
                path: '/creation/gallery',
                pageBuilder: (_, _) => tabPage(const creation.GalleryPage()),
              ),
            ],
          ),

          // 老入口 /wiki/reviews 和 /wiki/cleanup 已迁到 wiki shell 子树
          // (/wiki/p/:pid/reviews 等)。这里 redirect 回 /wiki，让用户先选项目。
          GoRoute(
            path: '/wiki/reviews',
            redirect: (_, _) => '/wiki',
          ),
          GoRoute(
            path: '/wiki/cleanup',
            redirect: (_, _) => '/wiki',
          ),
          GoRoute(
            path: '/search',
            pageBuilder: (_, _) => tabPage(const SearchPage()),
          ),
          // 笔记（notes）— 独立功能，与 wiki 无耦合（设计 O1：保持轻，
          // 不做块模型/双链/数据库视图），不进 WikiShell：
          //   /notes        笔记本 + 笔记列表 + Milkdown 编辑器 三栏
          //   /notes/trash  回收站（还原 / 彻底删除）
          GoRoute(
            path: '/notes',
            pageBuilder: (_, _) => tabPage(const NotesHomePage()),
          ),
          GoRoute(
            path: '/notes/trash',
            pageBuilder: (_, _) => tabPage(const NotesTrashPage()),
          ),
          // GoRoute(path: '/memory', pageBuilder: (_, _) => tabPage(const MemoryPage())), // TODO(memory): 下线, 恢复见顶部说明
          GoRoute(path: '/skills', pageBuilder: (_, _) => tabPage(const SkillsPage())),
          GoRoute(path: '/apps', pageBuilder: (_, _) => tabPage(const AppsPage())),
          GoRoute(
            path: '/apps/customize',
            pageBuilder: (_, _) => subPage(const SidebarCustomizePage()),
          ),
          GoRoute(
            path: '/apps/installed',
            pageBuilder: (_, _) => subPage(const AppSettingsPage()),
          ),
          GoRoute(
            path: '/apps/detail/:slug',
            pageBuilder: (_, state) => subPage(
              AppDetailPage(identifier: state.pathParameters['slug'] ?? ''),
            ),
          ),
          // Repo App 安装确认页（M1.14）—— /apps/repo-install?url=<repo>
          GoRoute(
            path: '/apps/repo-install',
            pageBuilder: (_, state) => subPage(
              RepoInstallPage(
                repoUrl: state.uri.queryParameters['url'] ?? '',
              ),
            ),
          ),
          // App view host —— /apps/host/:installId/:viewId
          // installId 可以是真实 install_id 或 "dev:<slug>"（开发模式）
          // routeParams 通过 query `?params=<jsonEncoded map>` 携带, 兼容
          // ListLayout / GridLayout 的 on_click → /apps/<slug>/<view>/<id> 跳转。
          GoRoute(
            path: '/apps/host/:installId/:viewId',
            pageBuilder: (_, state) {
              final raw = state.uri.queryParameters['params'];
              Map<String, dynamic> params = const {};
              if (raw != null && raw.isNotEmpty) {
                try {
                  final decoded = jsonDecode(raw);
                  if (decoded is Map) {
                    params = Map<String, dynamic>.from(decoded);
                  }
                } catch (_) {/* 静默忽略, fallback 空 routeParams */}
              }
              return subPage(AppViewHost(
                installId: state.pathParameters['installId'] ?? '',
                viewId: state.pathParameters['viewId'] ?? 'home',
                routeParams: params,
              ));
            },
          ),
          ...codeRouteSpecs.map(
            (r) => GoRoute(path: r.path, pageBuilder: (_, _) => tabPage(r.child)),
          ),
          // /profile — 移动端底部 tab 5「我的」落地 (R1.3): 账户卡 + 功能入口
          // 列表, 设置降为子项。桌面 sidebar 无此入口; ProfilePage build 内
          // fallback 到 SettingsPage 防御 Web 手动深链。
          GoRoute(path: '/profile', pageBuilder: (_, _) => tabPage(const ProfilePage())),
          GoRoute(path: '/settings', pageBuilder: (_, _) => tabPage(const SettingsPage())),
          GoRoute(path: '/settings/devices', pageBuilder: (_, _) => subPage(const DeviceManagementPage())),
          GoRoute(
            path: '/membership',
            pageBuilder: (_, _) => subPage(const MembershipCenterPage()),
          ),
          GoRoute(
            path: '/membership/compare',
            pageBuilder: (_, _) => subPage(const PlanComparePage()),
          ),
          GoRoute(
            path: '/membership/orders',
            pageBuilder: (_, _) => subPage(const OrderHistoryPage()),
          ),
          GoRoute(
            path: '/membership/checkout',
            pageBuilder: (_, state) {
              final extra = state.extra as Map<String, dynamic>? ?? const {};
              final isTopup = (extra['topup'] ?? false) == true;
              if (isTopup) {
                return subPage(CheckoutPage(
                  topup: true,
                  optionID: extra['option_id'] as String?,
                  topupDisplayName: extra['display_name'] as String?,
                  topupCreditsAmount: extra['credits_amount'] is int
                      ? extra['credits_amount'] as int
                      : null,
                  topupAmountCents: extra['amount_cents'] is int
                      ? extra['amount_cents'] as int
                      : null,
                ));
              }
              return subPage(CheckoutPage(
                planCode: (extra['plan_code'] ?? 'pro') as String,
                netChargeCents: extra['net_charge_cents'] is int
                    ? extra['net_charge_cents'] as int
                    : null,
              ));
            },
          ),
          GoRoute(
            path: '/membership/coupons',
            pageBuilder: (_, _) => subPage(const CouponRedeemPage()),
          ),
          GoRoute(
            path: '/membership/referrals',
            pageBuilder: (_, state) {
              final extra = state.extra as Map<String, dynamic>? ?? const {};
              return subPage(ReferralPage(
                currentUserID: (extra['user_id'] ?? '') as String,
              ));
            },
          ),
        ],
      ),
    ],
  );
}

/// Bridges Riverpod -> GoRouter's ChangeNotifier hook so the redirect
/// runs on sign-in / sign-out.
///
/// **只听 isAuthenticatedProvider (bool)，不听 hubCredentialsProvider 原值**：
/// token 每小时轮换，原值每次变新对象（resolve() 返新实例 + 无 == 重写），
/// 听原值会让 GoRouter 每小时 refresh → 整路由栈 rebuild → 所有页面闪动。
/// bool 只在 登录↔登出 翻转。详见 auth_service.dart isAuthenticatedProvider。
class _RouterListenable extends ChangeNotifier {
  _RouterListenable(this._container) {
    _authSub = _container.listen<bool>(
      isAuthenticatedProvider,
      (_, _) => notifyListeners(),
    );
  }
  final ProviderContainer _container;
  late final ProviderSubscription<bool> _authSub;

  @override
  void dispose() {
    _authSub.close();
    super.dispose();
  }
}

class _NavItem {
  final IconData icon;
  final String label;
  final String path;
  /// Stable system identifier (matches sidebar.Item.ref for kind=system).
  /// Used to look up hidden flag from the user's sidebar layout.
  final String systemId;
  /// install_id, 仅 pinned app 行有 (system 行 null)。给右键菜单用来
  /// 调 reorderPinnedApp / togglePinnedApp。
  final String? installId;
  /// 真图标 URL (含 Bearer header) — 来自 manifest.icon 的 `cas:<sha>`
  /// 解析后拼成的 brain `/v1/brain/files-by-sha/<sha>` URL。null = 用 [icon]
  /// 当 fallback。仅 pinned app 可能有, system 行始终 null。
  final String? iconUrl;
  const _NavItem(this.icon, this.label, this.path, this.systemId,
      {this.installId, this.iconUrl});
}

class _AppShell extends ConsumerWidget {
  const _AppShell({required this.child});
  final Widget child;

  /// System-fixed nav items, in default visual order. Decision §10A.2:
  /// users can hide individual entries via Sidebar Customize but the
  /// entries themselves are not removable (so a user who hid Chat by
  /// accident still has a way to reach it via the customize page).
  List<_NavItem> _systemItems(AppLocalizations t) => [
        _NavItem(Icons.chat_bubble_outline_rounded, t.navChat,    '/chat',     'chat'),
        _NavItem(Icons.search,                       '搜索',       '/search',   'search'),
        _NavItem(Icons.menu_book_outlined,           t.navWiki,    '/wiki',     'wiki'),
        _NavItem(Icons.note_outlined,                '笔记',       '/notes',    'notes'),
        // _NavItem(Icons.psychology_outlined,          t.navMemory,  '/memory',   'memory'), // TODO(memory): 下线, 恢复见顶部说明
        _NavItem(Icons.extension_outlined,           t.navSkills,  '/skills',   'skills'),
        _NavItem(Icons.apps_outlined,                t.appsTitle,  '/apps',     'apps'),
        _NavItem(Icons.auto_awesome_outlined,        t.navCreation, '/creation', 'creation'),
        _NavItem(Icons.terminal_rounded,             t.navCode,    '/code',     'code'),
      ];

  /// 哪些 route 把外层 sidebar 折叠成图标栏（IDE 风工作台空间优先）。
  /// /wiki 内部自带 knowcode 风格 NavRail（220px）+ StatusBar，所以
  /// 顶层 sidebar 折叠为 48px 图标条避免双 sidebar 占空间。/creation 同理 —
  /// 内部有 220px 二级 NavRail (灵感 / 中心 / 作品 / 广场 / 积分)。
  /// /notes 自带三栏（笔记本 + 列表 + 编辑器），同样收窄。
  static bool _shouldCompact(String path) =>
      path.startsWith('/code') ||
      path.startsWith('/wiki') ||
      path.startsWith('/notes') ||
      path.startsWith('/creation');

  @override
  Widget build(BuildContext ctx, WidgetRef ref) {
    final t = AppLocalizations.of(ctx)!;
    final loc = GoRouterState.of(ctx).uri.path;
    // 用户三态 × 路由强制收窄(/code /wiki /creation 工作台)合成:
    //   hidden 恒 0;iconsOnly 恒 48;expanded 在强制路由下临时 48
    //   (布局级收窄, 不写回用户偏好 — 离开该路由自动恢复 232)。
    final sidebarMode = ref.watch(sidebarModeProvider);
    final routeCompact = _shouldCompact(loc);
    final compact = sidebarMode != SidebarMode.expanded || routeCompact;
    final sidebarWidth = switch (sidebarMode) {
      SidebarMode.hidden => 0.0,
      SidebarMode.iconsOnly => _Sidebar._compactWidth,
      SidebarMode.expanded =>
        routeCompact ? _Sidebar._compactWidth : _Sidebar._expandedWidth,
    };
    // 手机形态 (<600px): 侧栏收进 Drawer, 内容区全宽 + SafeArea。
    final phone = isPhoneLayout(ctx);
    // creds 让 pinned app icon 拼 brain by-sha URL 用 — 跟 _Sidebar.build
    // 拿同一份。
    final creds = ref.watch(hubCredentialsProvider);

    // Apply user customization to system items: hidden=true entries
    // are filtered out, 顺序按 items 数组中 kind=system 项的相对位置
    // (设计 §10A.6 / customize 页支持 system 顺序拖拽)。layout 加载
    // 中 / 用户没改过 → fall back _systemItems 默认顺序。
    final layoutAsync = ref.watch(sidebarLayoutProvider('desktop'));
    final layout = layoutAsync.valueOrNull;
    final allSystem = _systemItems(t);
    final byId = {for (final s in allSystem) s.systemId: s};
    final orderedIds = <String>[];
    final hiddenSystem = <String>{};
    for (final i in (layout?.items ?? const <SidebarItem>[])) {
      if (i.kind != 'system') continue;
      if (!orderedIds.contains(i.ref)) orderedIds.add(i.ref);
      if (i.hidden) hiddenSystem.add(i.ref);
    }
    final systemItems = <_NavItem>[];
    final consumed = <String>{};
    // step 1: items 里出现过的 system 项按其相对顺序渲染 (跳过 hidden)。
    for (final id in orderedIds) {
      final nav = byId[id];
      if (nav == null) continue; // 未知 id (升级删了某个 system) 静默忽略
      consumed.add(id);
      if (hiddenSystem.contains(id)) continue;
      systemItems.add(nav);
    }
    // step 2: 用户从未碰过的 system 项 (新加的 / 老用户没改过) 按默认
    // 顺序 append, 同样跳过 hidden。
    for (final s in allSystem) {
      if (consumed.contains(s.systemId)) continue;
      if (hiddenSystem.contains(s.systemId)) continue;
      systemItems.add(s);
    }
    // 隐藏「编码」入口 — code workbench 功能层桌面限定 (daemon 仅桌面,
    // tasks 走 DummyAdapter)。手机形态不可用 (方案 §4.7);Web 上整个 code 树
    // 已条件编译剔除 (codeModuleEnabled=false)。
    if (phone || !codeModuleEnabled) {
      systemItems.removeWhere((i) => i.systemId == 'code');
    }

    // Pinned app entries: rebuild from layout × installations so we
    // can resolve install_id → identifier → label/icon.
    final installs = ref.watch(installationsProvider('user')).valueOrNull
        ?? const <Installation>[];
    final installById = {for (final i in installs) i.id: i};
    final pinnedItems = <_NavItem>[];
    for (final i in (layout?.items ?? const <SidebarItem>[])) {
      if (i.kind != 'app') continue;
      final inst = installById[i.ref];
      if (inst == null) continue; // skipped silently — DB trigger may
                                  // have nulled this out post-uninstall
      // 拉 manifest 看 icon 字段 — `cas:<sha>` 解析成 brain by-sha URL
      // 让 sidebar 渲真图标 (设计 §10A user_webview favicon 链路)。
      // manifestProvider 是 Riverpod family cache, 同 identifier 共享
      // 一份, 不会反复拉。null = 还没加载到 / 没声明 icon → 用 fallback
      // Icons.widgets_outlined。
      final manifest =
          ref.watch(manifestProvider(inst.identifier)).valueOrNull;
      final iconRaw = manifest?['icon'] as String? ?? '';
      final (iconUrl, _) = resolveAppIcon(iconRaw, creds);
      // 点 pinned 项直接进应用本体 `/apps/host/<installId>/<viewId>`,
      // 而非 manifest 详情页。home view 由 manifest 解析(与 AppDetailPage
      // 「打开」按钮同源)。两种 fallback 都回退到 detail 页:
      //   ① manifest 还没加载到(transient,manifestProvider 是缓存 family,
      //      通常已就绪) → 暂用 detail,加载完下一帧自动切;
      //   ② app 没声明任何 view(backend-only kind,无可宿主 UI) → detail
      //      页让用户读 description,符合现有「打开」按钮不显示的语义。
      final homeViewId =
          manifest == null ? null : resolveAppHomeViewId(manifest);
      final target = homeViewId != null
          ? '/apps/host/${inst.id}/$homeViewId'
          : '/apps/detail/${inst.identifier}';
      pinnedItems.add(_NavItem(
        Icons.widgets_outlined,
        inst.identifier,
        target,
        'app:${inst.id}',
        installId: inst.id,
        iconUrl: iconUrl,
      ));
    }

    // OfflineBadge 浮在每个 page 的右上角. 用 Stack 不挤压 child
    // 自身布局; online 状态下 widget 自动消失 (SizedBox.shrink),
    // 不影响 hit-testing。
    // 离线徽标浮在 page 右上角,不挤压 child 布局;在线时 shrink。
    // DocprocEngineView: docproc 本机解析引擎（无 UI 隐藏 webview），
    // 应用级常驻 —— import_dialog 关闭后队列仍在后台解析（§3.5 W1）。
    final caps = ref.watch(platformCapsProvider);
    final content = Stack(
      children: [
        Positioned.fill(child: child),
        if (caps.hasLocalDocproc)
          SizedBox(
            width: 0,
            height: 0,
            child: DocprocEngineView(
              controller: ref.watch(docprocEngineControllerProvider),
            ),
          ),
        const Positioned(
          top: 8,
          right: 12,
          child: OfflineBadge(),
        ),
      ],
    );

    return Scaffold(
      backgroundColor: BiuTokens.bg,
      // R1.6: 手机形态移除主导航 Drawer — 底部 tab 是唯一主导航, 顶部 ☰
      // (PhoneMenuButton) 改为「更多」popup (搜索 / 帮助反馈)。pinned app 从
      // 「应用」tab 进, 侧边栏定制是桌面功能移动端不暴露。wiki 模块有自己的
      // Drawer (内部 rail 导航), 不受影响。桌面 sidebar 分支不变。
      // (systemItems / pinnedItems 仍计算供桌面 _Sidebar 用; phone 分支不再渲染。)
      // 桌面也无需 Scaffold key (无 Drawer 可开)。
      // SecurityAlertBanner 跨整个 shell 顶上一条 (24h 内有 reuse 时), 必须
      // 在 Sidebar 上面才能跨列显示。无事件时 SizedBox.shrink 完全不占位。
      // 手机形态整体包 SafeArea (top+bottom) — 一处修掉所有自绘 header
      // 顶进状态栏 / 底部手势区的问题; 桌面维持现状 (SafeArea 全 false,
      // 侧栏自己包了 right:false 的那份)。
      // 桌面: 顶栏 _DesktopTitleBar 在最上 (macOS 红绿灯内嵌区), banner
      // 在顶栏之下 — 红绿灯位置固定, banner 不能顶到窗口最顶。
      body: SafeArea(
        top: phone,
        // bottom 留给手机形态 PhoneTabBar 的 SafeArea 接管 (home indicator
        // 让位); 桌面 bottom 本就 false, 行为不变 (R1.1)。
        bottom: false,
        left: false,
        right: false,
        child: phone
            ? Column(
                children: [
                  const SecurityAlertBanner(),
                  const UpdateBanner(),
                  Expanded(child: content),
                  // 手机形态底部主导航 (R1.1): 6 高频 tab (对话/知识库/笔记/
                  // 创作/应用/我的)。低频入口 (搜索/技能/会员/设备/pinned app/侧边栏
                  // 定制) 仍走顶部 ☰ Drawer, R1.6 收口语义。
                  const PhoneTabBar(),
                ],
              )
            // Cmd/Ctrl+B 切 sidebar (VSCode 惯例)。TextField 不消费该组合,
            // 事件沿 focus chain 上浮到这里。
            : CallbackShortcuts(
                bindings: {
                  const SingleActivator(LogicalKeyboardKey.keyB, meta: true):
                      () =>
                          ref.read(sidebarModeProvider.notifier).toggle(),
                  const SingleActivator(
                    LogicalKeyboardKey.keyB,
                    control: true,
                  ): () =>
                      ref.read(sidebarModeProvider.notifier).toggle(),
                },
                child: Focus(
                  autofocus: true,
                  child: Column(
                    children: [
                      _DesktopTitleBar(sidebarWidth: sidebarWidth),
                      const SecurityAlertBanner(),
                      const UpdateBanner(),
                      Expanded(
                        child: Row(
                          children: [
                            _Sidebar(
                              items: systemItems,
                              pinnedItems: pinnedItems,
                              currentPath: loc,
                              t: t,
                              compact: compact,
                              width: sidebarWidth,
                            ),
                            Container(
                              width: sidebarWidth == 0 ? 0 : 1,
                              color: BiuTokens.borderSubtle,
                            ),
                            Expanded(child: content),
                          ],
                        ),
                      ),
                    ],
                  ),
                ),
              ),
      ),
    );
  }
}

// ─── Desktop title bar ─────────────────────────────────
//
// 桌面顶栏 — macOS 红绿灯内嵌 (fullSizeContentView) 后, 原生标题栏区域
// 由这一行接管: 左 = sidebar toggle, 整行可拖动窗口 / 双击缩放
// (WindowDragArea)。背景左段跟 sidebar 同色 (surf-1) 同步伸缩, 红绿灯区
// 视觉上是 sidebar 的延伸; 右段跟内容同色。
//
// 垂直对齐: 红绿灯中心距窗口顶是 AppKit 决定的 (实测 ≈16pt), 顶栏高度
// 取 centerY*2 (经 biumind/window channel 实测), 让 toggle 与红绿灯严格
// 同一水平线; 查询未返回前用实测默认值 16/69。Windows/Linux 无红绿灯,
// 保留 40px 普通顶栏高度。

class _DesktopTitleBar extends ConsumerStatefulWidget {
  const _DesktopTitleBar({required this.sidebarWidth});

  /// 当前 sidebar 目标宽度 (0 / 48 / 232) — 背景左段跟着同步动画。
  final double sidebarWidth;

  @override
  ConsumerState<_DesktopTitleBar> createState() => _DesktopTitleBarState();
}

class _DesktopTitleBarState extends ConsumerState<_DesktopTitleBar> {
  /// 实测默认 (macOS 15): 红绿灯中心 16pt, 绿灯右缘 69pt。
  double _centerY = 16;
  double _right = 69;

  @override
  void initState() {
    super.initState();
    TrafficLightMetrics.query().then((m) {
      if (m != null && mounted) {
        setState(() {
          _centerY = m.centerY;
          _right = m.right;
        });
      }
    });
  }

  @override
  Widget build(BuildContext ctx) {
    final isMac = !kIsWeb && defaultTargetPlatform == TargetPlatform.macOS;
    final surface1 =
        Theme.of(ctx).extension<BiuColors>()?.surface1 ?? BiuTokens.bg;
    // macOS: 高度 = 红绿灯中心 × 2 → toggle 中心与红绿灯同线。
    // 其他桌面平台用 40px 常规顶栏。
    final height = isMac ? (_centerY * 2).clamp(28.0, 44.0) : 40.0;
    return SizedBox(
      height: height,
      child: Stack(
        fit: StackFit.expand,
        children: [
          Row(
            children: [
              Container(
                width: widget.sidebarWidth,
                color: surface1,
              ),
              Expanded(child: Container(color: BiuTokens.bg)),
            ],
          ),
          WindowDragArea(
            child: Row(
              children: [
                // macOS 红绿灯 (实测右缘 69pt) 让位; 其他平台原生标题栏
                // 在窗口条之外, 正常留边。
                SizedBox(width: isMac ? _right + 9 : 8),
                const _SidebarToggle(),
                const Spacer(),
              ],
            ),
          ),
        ],
      ),
    );
  }
}

/// 顶栏的 sidebar 开关 — 单击三态循环 (收起 → 只显示图标 → 图标和文字);
/// 右键/长按 弹三态菜单直选。
class _SidebarToggle extends ConsumerWidget {
  const _SidebarToggle();

  Future<void> _showModeMenu(
    BuildContext ctx,
    WidgetRef ref,
    Offset globalPos,
  ) async {
    final t = AppLocalizations.of(ctx)!;
    final current = ref.read(sidebarModeProvider);
    String label(SidebarMode m) => switch (m) {
          SidebarMode.expanded => t.sidebarModeExpanded,
          SidebarMode.iconsOnly => t.sidebarModeIconsOnly,
          SidebarMode.hidden => t.sidebarModeHidden,
        };
    final picked = await showMenu<SidebarMode>(
      context: ctx,
      position: popupPositionAt(ctx, globalPos),
      items: [
        for (final m in SidebarMode.values)
          PopupMenuItem<SidebarMode>(
            value: m,
            child: Row(
              children: [
                SizedBox(
                  width: 20,
                  child: m == current
                      ? Icon(
                          Icons.check,
                          size: 16,
                          color: Theme.of(ctx).colorScheme.primary,
                        )
                      : null,
                ),
                const SizedBox(width: 8),
                Text(label(m)),
              ],
            ),
          ),
      ],
    );
    if (picked != null) {
      await ref.read(sidebarModeProvider.notifier).setMode(picked);
    }
  }

  @override
  Widget build(BuildContext ctx, WidgetRef ref) {
    final t = AppLocalizations.of(ctx)!;
    final mode = ref.watch(sidebarModeProvider);
    // tooltip 预告「点击后切到哪一态」, 循环顺序与 toggle() 一致:
    // hidden → iconsOnly → expanded → hidden。
    final nextLabel = switch (mode) {
      SidebarMode.hidden => t.sidebarModeIconsOnly,
      SidebarMode.iconsOnly => t.sidebarModeExpanded,
      SidebarMode.expanded => t.sidebarModeHidden,
    };
    return GestureDetector(
      onSecondaryTapUp: (d) => _showModeMenu(ctx, ref, d.globalPosition),
      onLongPressStart: (d) => _showModeMenu(ctx, ref, d.globalPosition),
      child: IconButton(
        tooltip: nextLabel,
        // view_sidebar 默认面板在右, 水平镜像成左侧栏形态。
        icon: Transform.flip(
          flipX: true,
          child: Icon(
            mode == SidebarMode.hidden
                ? Icons.view_sidebar
                : Icons.view_sidebar_outlined,
            size: 18,
          ),
        ),
        color: BiuTokens.textSecondary,
        onPressed: () => ref.read(sidebarModeProvider.notifier).toggle(),
        padding: EdgeInsets.zero,
        // 28×28 — 跟 32px 高的顶栏 (macOS 红绿灯中心 16×2) 留 2px 上下边。
        constraints: const BoxConstraints(minWidth: 28, minHeight: 28),
        style: IconButton.styleFrom(tapTargetSize: MaterialTapTargetSize.shrinkWrap),
      ),
    );
  }
}

// ─── Sidebar ───────────────────────────────────────────

class _Sidebar extends ConsumerWidget {
  const _Sidebar({
    required this.items,
    required this.pinnedItems,
    required this.currentPath,
    required this.t,
    this.compact = false,
    this.width,
  });
  final List<_NavItem> items;
  final List<_NavItem> pinnedItems;
  final String currentPath;
  final AppLocalizations t;

  /// compact = true 时 sidebar 折叠成 48px 图标栏（仅 icon + tooltip），
  /// 让出空间给 IDE 风四区工作台。
  final bool compact;

  /// 目标宽度 (0 / 48 / 232)。null 时按 compact 推导 (旧行为)。
  /// hidden 态传 0: 内容保持 compact 布局, 由 clip 随宽度动画裁掉。
  final double? width;

  /// 切换 tab 前先清空内层 Navigator 栈。
  ///
  /// 历史问题：AppsPage / AppSettingsPage 等用 `Navigator.push` 把
  /// AppDetailPage 等推到 ShellRoute 内层 Navigator 顶部。当用户点
  /// 侧边栏时 ctx.go 只换底层 ShellRoute child，**push 进栈的页面
  /// 仍然盖在新 child 之上**——结果用户看不到切换、点击全被截。
  ///
  /// 治标方案（本函数）：在 ctx.go 之前把内层 Navigator 全部 pop 到
  /// 第一项；这样 push 上去的所有详情页都消失，再换底层 child 就
  /// 看得到了。
  ///
  /// 治本方案（后续）：把 AppDetailPage / AppSettingsPage / 等都
  /// 注册成 GoRoute 子路由，全部用 context.push / context.go，
  /// 顺带支持深链。
  static void _navigateTo(BuildContext ctx, String path) {
    final nav = Navigator.of(ctx);
    if (nav.canPop()) {
      nav.popUntil((r) => r.isFirst);
    }
    ctx.go(path);
  }

  /// 系统菜单项是否点亮。`/apps`（应用中心）是目录入口,只覆盖
  /// `/apps`、`/apps/detail`、`/apps/installed`、`/apps/customize`;
  /// `/apps/host/...` 是「运行中的 app 本体」,归属对应的 pinned 项,
  /// 不应点亮应用中心(否则进任意 app 都会双高亮)。其余菜单维持
  /// 前缀匹配。
  static bool _systemSelected(String current, String itemPath) {
    if (itemPath == '/apps' && current.startsWith('/apps/host/')) {
      return false;
    }
    return current.startsWith(itemPath);
  }

  /// pinned app 项是否点亮:当前在该 install 的任意 view 下
  /// (`/apps/host/<installId>/...`)即点亮,这样在 app 内部切换子 view
  /// 时侧边栏入口保持高亮。manifest 未就绪 fallback 到 detail 路由时
  /// 退化为精确前缀匹配。
  static bool _pinnedSelected(String current, _NavItem item) {
    final id = item.installId;
    if (id != null && item.path.startsWith('/apps/host/')) {
      return current.startsWith('/apps/host/$id');
    }
    return current.startsWith(item.path);
  }

  static const _expandedWidth = BiuTokens.sidebarWidth;
  static const _compactWidth = 48.0;

  @override
  Widget build(BuildContext ctx, WidgetRef ref) {
    final creds = ref.watch(hubCredentialsProvider);
    final pinnedSet = <String>{
      for (final p in pinnedItems)
        if (p.installId != null) p.installId!,
    };
    // 应用中心 tile 拖入 → 固定到 sidebar (设计 §10A.3 "直接拖拽")。
    // DragTarget.builder 通过 candidateData 提供拖动悬停态; 高亮整条
    // sidebar 给用户"这里能放"的视觉信号。
    return DragTarget<String>(
      onWillAcceptWithDetails: (details) => !pinnedSet.contains(details.data),
      onAcceptWithDetails: (details) =>
          _onSidebarDragAccept(ctx, ref, details.data),
      builder: (ctx, candidates, _) {
        final hovering = candidates.isNotEmpty;
        return Container(
          width: width ?? (compact ? _compactWidth : _expandedWidth),
          // hidden → 0 时内容仍按完整宽度渲染, 由 clip 裁掉超出部分 —
          // 不裁的话 Column/Row 报 overflow。
          clipBehavior: Clip.hardEdge,
          decoration: hovering
              ? BoxDecoration(
                  color: BiuTokens.purpleSoft.withValues(alpha: 0.6),
                  border: Border.all(color: BiuTokens.purple, width: 2),
                )
              // prototype `.nav { background: var(--surf-1) }` 是平涂 surf-1,
              // 没有顶部 brand 染色。早期参考 lobehub 的 `--side-grad` 给 sidebar
              // 加了 4% brand → bg 60% 渐变,但跟 v3 prototype 实际渲染不一致,
              // 浅色模式下顶部一抹紫看起来像"误染色"。改为平涂跟 prototype 对齐。
              : BoxDecoration(
                  color: Theme.of(ctx).extension<BiuColors>()?.surface1 ??
                      BiuTokens.bg,
                ),
          child: OverflowBox(
            minWidth: 0,
            maxWidth: double.infinity,
            alignment: Alignment.topLeft,
            child: SizedBox(
              width: compact ? _compactWidth : _expandedWidth,
              child: SafeArea(
                right: false,
                child: Column(
                  children: [
            // Brand mark — 原 38px 红绿灯 spacer 已移除: macOS 红绿灯内嵌
            // 后交通灯区由顶栏 _DesktopTitleBar 接管, sidebar 从顶栏下方开始。
            // prototype `.brand { padding: 8px 8px 14px; mark 32px;
            //   name 15px / 700 / -0.02em }` — 之前用 BiuLogoSize.small (20+13)
            //   偏小,sidebar 顶部空荡。改用 BiuLogoSize.sidebar (32+15) 让
            //   品牌区跟 prototype 1:1 站位。底部 14px 留呼吸给主导航。
            Padding(
              padding: EdgeInsets.fromLTRB(
                compact ? 12 : 8,
                BiuTokens.space1,
                compact ? 12 : 8,
                14,
              ),
              child: BiuLogo(
                size: compact ? BiuLogoSize.small : BiuLogoSize.sidebar,
                onlyMark: compact,
              ),
            ),

            // Primary nav: System segment + Pinned Apps segment.
            // Pinned segment renders only when non-empty; the
            // segment divider is also conditional so an empty pinned
            // list doesn't waste vertical space.
            Expanded(
              child: ListView(
                padding: EdgeInsets.symmetric(
                  horizontal: compact ? 6 : BiuTokens.space2,
                ),
                children: [
                  for (final i in items)
                    _NavRow(
                      item: i,
                      selected: _systemSelected(currentPath, i.path),
                      onTap: () => _navigateTo(ctx, i.path),
                      compact: compact,
                    ),
                  if (pinnedItems.isNotEmpty) ...[
                    if (!compact)
                      // prototype `.nav-section { font: 10px / w600 / upper /
                      //   letter-spacing 0.08em / text-muted; padding 12 12 6 }`
                      //   — BiuSectionLabel 已是 12px,在 sidebar 这种紧凑列表里
                      //   12 跟 prototype 10 视觉差异很小;比之前 labelSmall +
                      //   letterSpacing 0.5 + 13px 字号克制许多。文案改 "已固定"
                      //   跟 prototype 一致。
                      const Padding(
                        padding: EdgeInsets.fromLTRB(12, 12, 12, 6),
                        child: BiuSectionLabel(
                          '已固定',
                          padding: EdgeInsets.zero,
                        ),
                      ),
                    for (final p in pinnedItems)
                      _NavRow(
                        item: p,
                        selected: _pinnedSelected(currentPath, p),
                        onTap: () => _navigateTo(ctx, p.path),
                        onSecondaryTapDown: (d) => _showPinnedContextMenu(
                          ctx, ref, p.installId!, d.globalPosition,
                        ),
                        // 触屏等价物: 长按弹同一个 pinned 菜单 (P1-11)。
                        onLongPressStart: (d) => _showPinnedContextMenu(
                          ctx, ref, p.installId!, d.globalPosition,
                        ),
                        compact: compact,
                      ),
                  ],
                ],
              ),
            ),

            // 创作积分 chip — 全局可见 (设计 §3.5 "顶部积分挪到 sidebar 底部").
            // 仅登录后显示 (creds 为 null 时不挂, 避免对未登录用户误展示账户元素).
            if (creds != null) CreditIndicator(compact: compact),

            // Bottom: connection indicator + settings shortcut
            Divider(height: 1, color: BiuTokens.borderSubtle),
            _UserFooter(
              t: t,
              connected: creds != null,
              onSettings: () => _navigateTo(ctx, '/settings'),
              onCustomize: () => _navigateTo(ctx, '/apps/customize'),
              onSignOut: () => confirmAndSignOut(ctx, ref),
              compact: compact,
            ),
                  ],
                ),
              ),
            ),
          ),
        );
      },
    );
  }
}

class _NavRow extends ConsumerStatefulWidget {
  const _NavRow({
    required this.item,
    required this.selected,
    required this.onTap,
    this.onSecondaryTapDown,
    this.onLongPressStart,
    this.compact = false,
  });
  final _NavItem item;
  final bool selected;
  final VoidCallback onTap;
  /// 仅 pinned app 行有效; system 行传 null = 不弹右键菜单。
  final void Function(TapDownDetails)? onSecondaryTapDown;
  /// 触屏的右键等价物 (长按); pinned app 行与 onSecondaryTapDown 同菜单。
  final void Function(LongPressStartDetails)? onLongPressStart;
  final bool compact;

  @override
  ConsumerState<_NavRow> createState() => _NavRowState();
}

class _NavRowState extends ConsumerState<_NavRow> {
  bool _hover = false;

  @override
  Widget build(BuildContext context) {
    final selected = widget.selected;
    final compact = widget.compact;
    final bg = selected
        ? BiuTokens.purpleSoft
        : (_hover ? BiuTokens.surfaceMuted : Colors.transparent);
    final fg = selected ? BiuTokens.purple : BiuTokens.text;

    // Pinned app 行才有 badge (system 行 installId=null)。
    final installId = widget.item.installId;
    final badge = installId == null ? null : badgeFor(ref, installId);

    // 真图标 (manifest.icon = "cas:<sha>" 的 user_webview app) — 拉
    // brain by-sha 端点带 Bearer header。HTTP cache + Cache-Control
    // immutable 让二次渲染秒出 (server 设了 max-age=1y)。
    final creds = ref.watch(hubCredentialsProvider);
    Widget iconWidget(double size) {
      final url = widget.item.iconUrl;
      if (url != null && creds != null) {
        return Image.network(
          url,
          width: size,
          height: size,
          fit: BoxFit.cover,
          headers: {'Authorization': 'Bearer ${creds.bearerToken}'},
          // 加载中 / 失败兜底通用图标, 避免空白闪烁。
          errorBuilder: (_, _, _) =>
              Icon(widget.item.icon, size: size, color: fg),
          loadingBuilder: (_, child, progress) {
            if (progress == null) return child;
            return Icon(widget.item.icon, size: size, color: fg);
          },
        );
      }
      return Icon(widget.item.icon, size: size, color: fg);
    }

    final row = MouseRegion(
      cursor: SystemMouseCursors.click,
      onEnter: (_) => setState(() => _hover = true),
      onExit: (_) => setState(() => _hover = false),
      child: GestureDetector(
        onTap: widget.onTap,
        onSecondaryTapDown: widget.onSecondaryTapDown,
        onLongPressStart: widget.onLongPressStart,
        behavior: HitTestBehavior.opaque,
        // 用 Stack 让左侧 3x18px 的 brand 色 indicator 浮在 row 之外。
        // prototype 是 `nav-item.active::before { left: -12px; width: 3px;
        // height: 18px; brand; border-radius: 0 3px 3px 0 }`,Flutter 等价
        // 用绝对定位的小条占位。compact 态下隐藏(整 sidebar 宽度只有 48px,
        // 左侧条会贴边出问题)。
        child: Stack(
          clipBehavior: Clip.none,
          children: [
            // 关键修复:hover bg 不做动画。
            // 旧实现 AnimatedContainer 160ms easeOutCubic 在快速划过 sidebar
            // 时,多个 row 同时在 160ms 淡出窗口,曲线头段 80ms 颜色还很浓 →
            // 视觉残影("记忆 + 技能 + 应用中心" 同时挂着 hover bg)。
            //
            // CSS 浏览器是 GPU 合成的 alpha 平滑插值不会有此问题,Flutter Skia
            // 每帧 rebuild 整个 BoxDecoration 在曲线密度高的位置看起来"色块"还在。
            //
            // 解法:hover 状态(高频)用普通 Container 即时响应 + 0ms 切换;
            // selected 状态(低频,仅路由切换)才需要 AnimatedContainer 平滑过渡。
            Container(
              height: BiuTokens.sidebarItemHeight,
              margin: const EdgeInsets.symmetric(vertical: 1),
              padding: EdgeInsets.symmetric(
                  horizontal: compact ? 0 : BiuTokens.space2),
              decoration: BoxDecoration(
                color: bg,
                borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
              ),
              child: compact
                  ? Center(
                      child: badge != null
                          ? _BadgedIcon(
                              icon: widget.item.icon,
                              color: fg,
                              badge: badge,
                              compact: true,
                              iconBuilder: iconWidget,
                            )
                          : ClipRRect(
                              borderRadius: BorderRadius.circular(4),
                              child: iconWidget(18),
                            ),
                    )
                  : Row(
                      children: [
                        ClipRRect(
                          borderRadius: BorderRadius.circular(4),
                          child: iconWidget(16),
                        ),
                        const SizedBox(width: BiuTokens.space3),
                        Expanded(
                          child: Text(
                            widget.item.label,
                            style: TextStyle(
                              fontSize: 13,
                              fontWeight: selected
                                  ? FontWeight.w600
                                  : FontWeight.w500,
                              color: fg,
                            ),
                            overflow: TextOverflow.ellipsis,
                          ),
                        ),
                        if (badge != null) _BadgePill(badge: badge),
                      ],
                    ),
            ),
            if (selected && !compact)
              Positioned(
                left: -BiuTokens.space2 - 1,
                top: 0,
                bottom: 0,
                child: Center(
                  child: AnimatedContainer(
                    duration: const Duration(milliseconds: 160),
                    width: 3,
                    height: 18,
                    decoration: BoxDecoration(
                      color: BiuTokens.purple,
                      borderRadius: const BorderRadius.only(
                        topRight: Radius.circular(3),
                        bottomRight: Radius.circular(3),
                      ),
                    ),
                  ),
                ),
              ),
          ],
        ),
      ),
    );

    return compact
        ? Tooltip(
            message: widget.item.label,
            waitDuration: const Duration(milliseconds: 400),
            child: row,
          )
        : row;
  }
}

/// Badge 颜色映射: severity → seed color。info=主题紫, warn=橙,
/// error=红 (用 BiuTokens.error)。
Color _badgeColor(BuildContext ctx, BadgeSeverity sev) {
  switch (sev) {
    case BadgeSeverity.error:
      return BiuTokens.error;
    case BadgeSeverity.warn:
      return NamedPaletteStrong.orange;
    case BadgeSeverity.info:
      return BiuTokens.purple;
  }
}

/// 展开态侧边栏 app 行右侧的小药丸 — 数字 (>=99 截 "99+") 或纯红点。
///
/// prototype `.nav-item .badge { background: var(--surf-3); color: var(--text-3);
/// font-size: 10px; padding: 1px 6px; weight: 600 }` — **安静的灰药丸**,
/// 不夺主行视觉。早期实现用 severity 实色(红/橙/紫)+ 白字 w700,所有 nav 行
/// 都被 badge 抢走视觉中心,跟 prototype 设计意图相反。改回安静色,只 error 严重
/// 告警保留实红 + 白字(罕见,真正需要"快看!"的少数场景)。
class _BadgePill extends StatelessWidget {
  const _BadgePill({required this.badge});
  final BadgeData badge;

  @override
  Widget build(BuildContext context) {
    final c = Theme.of(context).extension<BiuColors>();
    final cs = Theme.of(context).colorScheme;
    final isError = badge.severity == BadgeSeverity.error;
    final bg = isError
        ? cs.error
        : (c?.surface3 ?? cs.surfaceContainerHigh);
    final fg = isError
        ? Colors.white
        : (c?.text3 ?? cs.onSurfaceVariant);
    final label = badge.count > 99 ? '99+' : '${badge.count}';
    return Container(
      margin: const EdgeInsets.only(left: BiuTokens.space2),
      padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 1),
      decoration: BoxDecoration(
        color: bg,
        borderRadius: BorderRadius.circular(999),
      ),
      constraints: const BoxConstraints(minWidth: 18),
      child: Text(
        label,
        textAlign: TextAlign.center,
        style: TextStyle(
          color: fg,
          fontSize: 10,
          fontWeight: FontWeight.w600,
          height: 1.2,
        ),
      ),
    );
  }
}

/// compact 态: icon + 角标红点 (不显示数字, 48px 太挤)。
class _BadgedIcon extends StatelessWidget {
  const _BadgedIcon({
    required this.icon,
    required this.color,
    required this.badge,
    required this.compact,
    this.iconBuilder,
  });
  final IconData icon;
  final Color color;
  final BadgeData badge;
  final bool compact;
  /// 优先用这个渲图标 (e.g. user_webview 的真图标 Image.network); null
  /// 则回退到 [icon] IconData。
  final Widget Function(double size)? iconBuilder;

  @override
  Widget build(BuildContext context) {
    final dotColor = _badgeColor(context, badge.severity);
    final inner = iconBuilder != null
        ? ClipRRect(borderRadius: BorderRadius.circular(4), child: iconBuilder!(18))
        : Icon(icon, size: 18, color: color);
    return Stack(
      clipBehavior: Clip.none,
      children: [
        inner,
        Positioned(
          right: -3,
          top: -2,
          child: Container(
            width: 8,
            height: 8,
            decoration: BoxDecoration(
              color: dotColor,
              shape: BoxShape.circle,
              border: Border.all(color: BiuTokens.bg, width: 1.5),
            ),
          ),
        ),
      ],
    );
  }
}

class _UserFooter extends StatelessWidget {
  const _UserFooter({
    required this.t,
    required this.connected,
    required this.onSettings,
    required this.onCustomize,
    required this.onSignOut,
    this.compact = false,
  });
  final AppLocalizations t;
  final bool connected;
  final VoidCallback onSettings;
  final VoidCallback onCustomize;
  final VoidCallback onSignOut;
  final bool compact;

  @override
  Widget build(BuildContext context) {
    if (compact) {
      // 图标栏模式:垂直堆叠 customize + settings (settings 带连接状态点)。
      // tune 入口跟 settings 平行放,占位最少。
      return Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          Tooltip(
            message: t.sidebarCustomizeTitle,
            child: IconButton(
              icon: const Icon(Icons.tune, size: 18),
              color: BiuTokens.textSecondary,
              onPressed: onCustomize,
              padding: EdgeInsets.zero,
              constraints: const BoxConstraints(minWidth: 32, minHeight: 32),
            ),
          ),
          Tooltip(
            message: t.navSettings,
            child: IconButton(
              icon: Stack(
                clipBehavior: Clip.none,
                children: [
                  const Icon(Icons.settings_outlined, size: 18),
                  Positioned(
                    right: -2,
                    bottom: -2,
                    child: Container(
                      width: 7,
                      height: 7,
                      decoration: BoxDecoration(
                        color: connected ? BiuTokens.green : BiuTokens.textMuted,
                        shape: BoxShape.circle,
                        border: Border.all(color: BiuTokens.bg, width: 1.5),
                      ),
                    ),
                  ),
                ],
              ),
              color: BiuTokens.textSecondary,
              onPressed: onSettings,
              padding: EdgeInsets.zero,
              constraints: const BoxConstraints(minWidth: 32, minHeight: 32),
            ),
          ),
          Tooltip(
            message: '退出登录',
            child: IconButton(
              icon: const Icon(Icons.logout, size: 18),
              color: BiuTokens.textSecondary,
              onPressed: onSignOut,
              padding: EdgeInsets.zero,
              constraints: const BoxConstraints(minWidth: 32, minHeight: 32),
            ),
          ),
          const SizedBox(height: BiuTokens.space2),
        ],
      );
    }
    return Padding(
      padding: const EdgeInsets.fromLTRB(
        BiuTokens.space3,
        BiuTokens.space3,
        BiuTokens.space3,
        BiuTokens.space3,
      ),
      child: Row(
        children: [
          // 连接时用 BiuPulseDot 跟 prototype `.dot-live` 一致 — 8px 实点
          // + 6px 半径外晕 2.4s ease loop。未连接灰色静态点。
          if (connected)
            const BiuPulseDot(
              color: SemanticTokens.success,
              size: 8,
              maxRingRadius: 6,
            )
          else
            Container(
              width: 8,
              height: 8,
              decoration: BoxDecoration(
                color: BiuTokens.textMuted,
                shape: BoxShape.circle,
              ),
            ),
          const SizedBox(width: BiuTokens.space2),
          Expanded(
            child: Text(
              connected ? '已连接' : '未连接',
              // prototype `.connection { font-size: 11px; color: text-3;
              //   padding 8px 4px 0 }` — sidebar 底部状态行字号小一档,跟
              //   nav-item 14px 拉开层级差。
              style: TextStyle(
                fontSize: 11,
                color: BiuTokens.textSecondary,
              ),
              overflow: TextOverflow.ellipsis,
            ),
          ),
          IconButton(
            tooltip: t.sidebarCustomizeTitle,
            icon: const Icon(Icons.tune, size: 16),
            color: BiuTokens.textSecondary,
            onPressed: onCustomize,
            padding: EdgeInsets.zero,
            constraints: const BoxConstraints(minWidth: 28, minHeight: 28),
          ),
          IconButton(
            tooltip: t.navSettings,
            icon: const Icon(Icons.settings_outlined, size: 16),
            color: BiuTokens.textSecondary,
            onPressed: onSettings,
            padding: EdgeInsets.zero,
            constraints: const BoxConstraints(minWidth: 28, minHeight: 28),
          ),
          IconButton(
            tooltip: '退出登录',
            icon: const Icon(Icons.logout, size: 16),
            color: BiuTokens.textSecondary,
            onPressed: onSignOut,
            padding: EdgeInsets.zero,
            constraints: const BoxConstraints(minWidth: 28, minHeight: 28),
          ),
        ],
      ),
    );
  }
}

/// 应用中心 tile 拖入 sidebar 触发的固定动作 (设计 §10A.3 "直接拖拽")。
/// onWillAccept 已经过滤了重复 pin 的情况, 这里只处理"真正要 pin"的
/// 路径。冲突走 togglePinnedApp 内置的 retry, 仍失败 toast 提示。
Future<void> _onSidebarDragAccept(
    BuildContext ctx, WidgetRef ref, String installId) async {
  try {
    final nowPinned = await togglePinnedApp(ref, installId: installId);
    if (nowPinned == null || !nowPinned || !ctx.mounted) return;
    ScaffoldMessenger.of(ctx).showSnackBar(const SnackBar(
      content: Text('已添加到侧边栏。'),
      duration: Duration(seconds: 2),
    ));
  } on SidebarConflict {
    if (ctx.mounted) {
      ScaffoldMessenger.of(ctx).showSnackBar(const SnackBar(
        content: Text('另一设备刚改了侧边栏，已重新载入'),
      ));
    }
  } catch (e) {
    if (ctx.mounted) {
      ScaffoldMessenger.of(ctx).showSnackBar(SnackBar(content: Text('操作失败: $e')));
    }
  }
}

/// 侧边栏 pinned app 行右键菜单 (设计 §10A.3 "右键 App tile 快捷菜单"):
/// 置顶 / 上移 / 下移 / 移到底部 / 取消固定。冲突走 reorderPinnedApp
/// 内置自动重试; 仍失败 toast 提示。
Future<void> _showPinnedContextMenu(
    BuildContext ctx, WidgetRef ref, String installId, Offset pos) async {
  final selected = await showMenu<String>(
    context: ctx,
    position: popupPositionAt(ctx, pos),
    items: const [
      PopupMenuItem(value: 'top', child: ListTile(
        leading: Icon(Icons.vertical_align_top, size: 18),
        title: Text('置顶'),
        dense: true, contentPadding: EdgeInsets.zero,
      )),
      PopupMenuItem(value: 'up', child: ListTile(
        leading: Icon(Icons.keyboard_arrow_up, size: 18),
        title: Text('上移'),
        dense: true, contentPadding: EdgeInsets.zero,
      )),
      PopupMenuItem(value: 'down', child: ListTile(
        leading: Icon(Icons.keyboard_arrow_down, size: 18),
        title: Text('下移'),
        dense: true, contentPadding: EdgeInsets.zero,
      )),
      PopupMenuItem(value: 'bottom', child: ListTile(
        leading: Icon(Icons.vertical_align_bottom, size: 18),
        title: Text('移到底部'),
        dense: true, contentPadding: EdgeInsets.zero,
      )),
      PopupMenuDivider(),
      PopupMenuItem(value: 'unpin', child: ListTile(
        leading: Icon(Icons.push_pin_outlined, size: 18),
        title: Text('取消固定'),
        dense: true, contentPadding: EdgeInsets.zero,
      )),
    ],
  );
  if (selected == null || !ctx.mounted) return;
  try {
    if (selected == 'unpin') {
      await togglePinnedApp(ref, installId: installId);
      return;
    }
    final action = switch (selected) {
      'top' => SidebarReorder.top,
      'up' => SidebarReorder.up,
      'down' => SidebarReorder.down,
      'bottom' => SidebarReorder.bottom,
      _ => null,
    };
    if (action == null) return;
    await reorderPinnedApp(ref, installId: installId, action: action);
  } on SidebarConflict {
    if (ctx.mounted) {
      ScaffoldMessenger.of(ctx).showSnackBar(const SnackBar(
        content: Text('另一设备刚改了侧边栏，已重新载入'),
      ));
    }
  } catch (e) {
    if (ctx.mounted) {
      ScaffoldMessenger.of(ctx).showSnackBar(SnackBar(content: Text('操作失败: $e')));
    }
  }
}


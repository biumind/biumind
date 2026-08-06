// Hand-built RSS app page — replaces the generic AppViewHost rendering
// when installation.identifier == 'rss'. Three top-level tabs:
//   1. 收件箱 (inbox) — feeds | entries | reader 三栏
//   2. 雷达 (radar)   — rules | hits 双栏
//   3. 榜单 (boards)  — newsnow 热榜卡片网格
//
// Internal navigation is owned by a TabController + Riverpod selection
// state; we deliberately don't push new routes for sub-views (clicking
// an entry, a rule, or a board) so the user can flip between tabs
// without losing context.

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../../app/theme.dart';
import '../../../../data/api/apps_client.dart';
import 'providers.dart';
import 'widgets/add_feed_sheet.dart';
import 'widgets/boards_tab.dart';
import 'widgets/discover_tab.dart';
import 'widgets/inbox_tab.dart';
import 'widgets/radar_tab.dart';
import 'widgets/rule_editor_sheet.dart';
import 'widgets/saved_tab.dart';
import 'widgets/settings_page.dart';
import 'widgets/share_dialog.dart';
import 'widgets/ai_copilot_drawer.dart';
import 'widgets/today_tab.dart';

class RssAppPage extends ConsumerStatefulWidget {
  const RssAppPage({
    super.key,
    required this.installation,
    this.viewId = 'home',
    this.routeParams = const {},
  });

  final Installation installation;
  final String viewId;
  final Map<String, dynamic> routeParams;

  @override
  ConsumerState<RssAppPage> createState() => _RssAppPageState();
}

class _RssAppPageState extends ConsumerState<RssAppPage>
    with SingleTickerProviderStateMixin {
  late final TabController _tabs;

  static int _viewIdToTab(String viewId) {
    switch (viewId) {
      case 'today':
        return 0;
      case 'home':
      case 'add':
        return 1;
      case 'radar':
      case 'rules':
      case 'rule_add':
        return 2;
      case 'boards':
      case 'board_detail':
        return 3;
      case 'discover':
        return 4;
      case 'saved':
        return 5;
      default:
        return 0; // default → Today
    }
  }

  // M11.3: tab → shareable view_kind. null for boards/discover (no
  // public read-only rendering for those).
  static String? _shareKind(int tab) {
    switch (tab) {
      case 0:
        return 'today';
      case 1:
        return 'inbox';
      case 2:
        return 'radar';
      case 5:
        return 'saved';
      default:
        return null;
    }
  }

  @override
  void initState() {
    super.initState();
    _tabs = TabController(
      length: 6,
      vsync: this,
      initialIndex: _viewIdToTab(widget.viewId),
    );
    _tabs.addListener(_onTabChange);
  }

  @override
  void dispose() {
    _tabs.removeListener(_onTabChange);
    _tabs.dispose();
    super.dispose();
  }

  void _onTabChange() {
    if (_tabs.indexIsChanging) return;
    setState(() {});
  }

  Future<void> _refreshActiveTab() async {
    switch (_tabs.index) {
      case 0:
        ref.invalidate(todayProvider);
        break;
      case 1:
        await _refreshInbox();
        break;
      case 2:
        ref.refreshRules();
        ref.refreshHits();
        break;
      case 3:
        ref.refreshBoards();
        break;
    }
  }

  Future<void> _refreshInbox() async {
    final actions = ref.read(rssActionsProvider);
    if (actions == null) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('尚未登录')),
      );
      return;
    }
    try {
      final stats = await actions.feedsRefresh();
      ref.refreshFeeds();
      ref.refreshEntries();
      if (!mounted) return;
      final summary = '已刷新 ${stats['ok'] ?? 0}/${stats['considered'] ?? 0}'
          ' · 新增 ${stats['new_entries'] ?? 0}'
          '${(stats['errors'] ?? 0) == 0 ? '' : ' · 错误 ${stats['errors']}'}';
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text(summary)),
      );
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text('刷新失败: $e'),
          action: SnackBarAction(label: '重试', onPressed: _refreshInbox),
        ),
      );
    }
  }

  Widget? _buildFab() {
    switch (_tabs.index) {
      case 0:
        return null; // Today: read-only curated
      case 1:
        return FloatingActionButton.extended(
          onPressed: () => showAddFeedSheet(context, ref),
          icon: const Icon(Icons.add),
          label: const Text('订阅'),
        );
      case 2:
        return FloatingActionButton.extended(
          onPressed: () => showRuleEditorSheet(context, ref),
          icon: const Icon(Icons.add),
          label: const Text('新规则'),
        );
      case 3:
        return FloatingActionButton(
          onPressed: () => ref.refreshBoards(),
          child: const Icon(Icons.refresh),
        );
      case 4: // Discover
      case 5: // Saved
        return null;
      default:
        return null;
    }
  }

  final _scaffoldKey = GlobalKey<ScaffoldState>();

  void _toggleCopilot() {
    final state = _scaffoldKey.currentState;
    if (state == null) return;
    if (state.isEndDrawerOpen) {
      Navigator.of(context).pop();
    } else {
      state.openEndDrawer();
    }
  }

  @override
  Widget build(BuildContext context) {
    return CallbackShortcuts(
      bindings: {
        const SingleActivator(LogicalKeyboardKey.keyR): _refreshActiveTab,
        // ⌘J / Ctrl+J 切换 RSS Co-Pilot 抽屉.
        const SingleActivator(LogicalKeyboardKey.keyJ, meta: true): _toggleCopilot,
        const SingleActivator(LogicalKeyboardKey.keyJ, control: true): _toggleCopilot,
      },
      child: Focus(
        autofocus: true,
        child: Scaffold(
          key: _scaffoldKey,
          backgroundColor: BiuTokens.bg,
          endDrawer: const Drawer(
            width: 420,
            child: AICopilotDrawer(),
          ),
          appBar: AppBar(
            backgroundColor: BiuTokens.bg,
            surfaceTintColor: BiuTokens.bg,
            elevation: 0,
            scrolledUnderElevation: 0,
            automaticallyImplyLeading: false,
            shape: Border(bottom: BorderSide(color: BiuTokens.borderSubtle)),
            titleSpacing: BiuTokens.space5,
            title: Row(
              children: [
                Icon(Icons.rss_feed, size: 20, color: BiuTokens.purple),
                const SizedBox(width: BiuTokens.space2),
                const Text(
                  'RSS 订阅',
                  style: TextStyle(fontSize: 18, fontWeight: FontWeight.w700),
                ),
              ],
            ),
            actions: [
              // M11.2: 我的 / 团队 scope 切换. 仅当 JWT 带 org_id 时显示;
              // 个人用户看不到团队段.
              if (ref.watch(rssOrgIdProvider) != null) ...[
                const _ScopeSwitcher(),
                const SizedBox(width: BiuTokens.space2),
              ],
              if (_tabs.index == 0)
                IconButton(
                  tooltip: '重新生成 Today',
                  onPressed: () => ref.invalidate(todayProvider),
                  icon: const Icon(Icons.refresh),
                ),
              if (_tabs.index == 1)
                IconButton(
                  tooltip: '刷新所有订阅',
                  onPressed: _refreshInbox,
                  icon: const Icon(Icons.refresh),
                ),
              if (_tabs.index == 2) _RadarFilterDropdown(),
              // M11.3: 分享当前视图 (Today / 收件箱 / 雷达 / 已收藏).
              if (_shareKind(_tabs.index) != null)
                IconButton(
                  tooltip: '分享此视图',
                  onPressed: () => showShareDialog(context, ref,
                      viewKind: _shareKind(_tabs.index)!),
                  icon: const Icon(Icons.ios_share, size: 20),
                ),
              IconButton(
                tooltip: 'AI Co-Pilot (⌘J)',
                onPressed: _toggleCopilot,
                icon: Icon(Icons.auto_awesome,
                    size: 20, color: BiuTokens.purple),
              ),
              // M11.5: ⋯ 菜单 — 设置 / 已收藏.
              PopupMenuButton<String>(
                tooltip: '更多',
                icon: const Icon(Icons.more_vert, size: 20),
                onSelected: (v) {
                  switch (v) {
                    case 'settings':
                      Navigator.of(context).push(MaterialPageRoute(
                          builder: (_) => const RssSettingsPage()));
                    case 'saved':
                      _tabs.animateTo(5);
                  }
                },
                itemBuilder: (_) => const [
                  PopupMenuItem(
                      value: 'saved',
                      child: ListTile(
                          dense: true,
                          leading: Icon(Icons.bookmark_outline, size: 18),
                          title: Text('已收藏'))),
                  PopupMenuItem(
                      value: 'settings',
                      child: ListTile(
                          dense: true,
                          leading: Icon(Icons.settings_outlined, size: 18),
                          title: Text('设置'))),
                ],
              ),
              const SizedBox(width: BiuTokens.space2),
            ],
            bottom: TabBar(
              controller: _tabs,
              isScrollable: true,
              labelColor: BiuTokens.purple,
              unselectedLabelColor: BiuTokens.textSecondary,
              indicatorColor: BiuTokens.purple,
              indicatorWeight: 2,
              labelStyle:
                  const TextStyle(fontSize: 14, fontWeight: FontWeight.w600),
              unselectedLabelStyle:
                  const TextStyle(fontSize: 14, fontWeight: FontWeight.w500),
              tabs: const [
                Tab(icon: Icon(Icons.today, size: 16), text: 'Today'),
                Tab(text: '收件箱'),
                Tab(text: '雷达'),
                Tab(text: '榜单'),
                Tab(icon: Icon(Icons.explore_outlined, size: 16), text: '探索'),
                Tab(icon: Icon(Icons.bookmark_outline, size: 16), text: '已收藏'),
              ],
            ),
          ),
          body: TabBarView(
            controller: _tabs,
            // Disable swipe — the inbox / radar tabs have their own
            // gestures (drawer toggles, scrolling lists) and an outer
            // swipe would steal them.
            physics: const NeverScrollableScrollPhysics(),
            children: const [
              TodayTab(),
              InboxTab(),
              RadarTab(),
              BoardsTab(),
              DiscoverTab(),
              SavedTab(),
            ],
          ),
          floatingActionButton: _buildFab(),
        ),
      ),
    );
  }
}

/// M11.2: 我的 / 团队 scope 切换. 切到团队后所有列表 / 规则 / 命中按
/// org scope 拉取 (org 成员可读, 写需 admin —— 由 30-rss.cedar 把关).
class _ScopeSwitcher extends ConsumerWidget {
  const _ScopeSwitcher();

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final scope = ref.watch(rssScopeProvider);
    return SegmentedButton<String>(
      style: SegmentedButton.styleFrom(
        visualDensity: VisualDensity.compact,
        textStyle: const TextStyle(fontSize: 12),
      ),
      segments: const [
        ButtonSegment(
            value: 'user',
            label: Text('我的'),
            icon: Icon(Icons.person_outline, size: 14)),
        ButtonSegment(
            value: 'org',
            label: Text('团队'),
            icon: Icon(Icons.groups_outlined, size: 14)),
      ],
      selected: {scope},
      showSelectedIcon: false,
      onSelectionChanged: (s) =>
          ref.read(rssScopeProvider.notifier).state = s.first,
    );
  }
}

class _RadarFilterDropdown extends ConsumerWidget {
  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final unreadOnly = ref.watch(rssSelectionProvider).radarUnreadOnly;
    return PopupMenuButton<bool>(
      tooltip: '筛选',
      icon: const Icon(Icons.filter_list),
      initialValue: unreadOnly,
      itemBuilder: (_) => const [
        PopupMenuItem(value: false, child: Text('全部')),
        PopupMenuItem(value: true, child: Text('未读')),
      ],
      onSelected: (v) =>
          ref.read(rssSelectionProvider.notifier).setRadarUnreadOnly(v),
    );
  }
}

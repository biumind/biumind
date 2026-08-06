// Riverpod providers for the hand-built RssAppPage.
//
// All queries route through AppsClient.invoke(identifier:'rss', ...).
// The generic AppViewHost has its own cache (viewDataFutureProvider)
// that we deliberately do NOT reuse — keys there carry route params /
// view ids that don't map cleanly onto our 3-tab UI, and our own
// providers expose strongly-typed model objects rather than raw maps.

import 'dart:convert';

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../../data/api/apps_client.dart';
import '../../../../data/apps_providers.dart';
import '../../../../data/wiki_providers.dart' show appDbProvider;
import 'data/rss_cache_dao.dart';
import 'models.dart';

/// Bundles the bits of state we hold per RSS app session: which feed
/// is selected in the inbox, which entry is open in the reader, which
/// rule is selected in the radar, and any optimistic local overrides
/// for read state (so flipping unread → read paints instantly without
/// waiting for a server round-trip).
class RssSelection {
  /// 'all' = pseudo-feed showing entries across every subscription.
  final String selectedFeedId;
  final String? selectedEntryId;
  final String? selectedRuleId; // null = "全部"
  final bool radarUnreadOnly;
  final Map<String, bool> entryReadOverride; // id → read?
  final Map<int, bool> hitReadOverride;      // id → read? (hits.id is int)

  const RssSelection({
    this.selectedFeedId = 'all',
    this.selectedEntryId,
    this.selectedRuleId,
    this.radarUnreadOnly = false,
    this.entryReadOverride = const {},
    this.hitReadOverride = const {},
  });

  RssSelection copyWith({
    String? selectedFeedId,
    Object? selectedEntryId = _sentinel,
    Object? selectedRuleId = _sentinel,
    bool? radarUnreadOnly,
    Map<String, bool>? entryReadOverride,
    Map<int, bool>? hitReadOverride,
  }) {
    return RssSelection(
      selectedFeedId: selectedFeedId ?? this.selectedFeedId,
      selectedEntryId: identical(selectedEntryId, _sentinel)
          ? this.selectedEntryId
          : selectedEntryId as String?,
      selectedRuleId: identical(selectedRuleId, _sentinel)
          ? this.selectedRuleId
          : selectedRuleId as String?,
      radarUnreadOnly: radarUnreadOnly ?? this.radarUnreadOnly,
      entryReadOverride: entryReadOverride ?? this.entryReadOverride,
      hitReadOverride: hitReadOverride ?? this.hitReadOverride,
    );
  }
}

const _sentinel = Object();

class RssSelectionController extends Notifier<RssSelection> {
  @override
  RssSelection build() => const RssSelection();

  void selectFeed(String feedId) =>
      state = state.copyWith(selectedFeedId: feedId, selectedEntryId: null);

  void selectEntry(String? entryId) =>
      state = state.copyWith(selectedEntryId: entryId);

  void selectRule(String? ruleId) =>
      state = state.copyWith(selectedRuleId: ruleId);

  void setRadarUnreadOnly(bool v) =>
      state = state.copyWith(radarUnreadOnly: v);

  void markEntryRead(String id, bool read) {
    final next = Map<String, bool>.from(state.entryReadOverride);
    next[id] = !read; // override stores the "unread?" boolean
    state = state.copyWith(entryReadOverride: next);
  }

  void markHitRead(int id, bool read) {
    final next = Map<int, bool>.from(state.hitReadOverride);
    next[id] = !read;
    state = state.copyWith(hitReadOverride: next);
  }
}

final rssSelectionProvider =
    NotifierProvider<RssSelectionController, RssSelection>(
        RssSelectionController.new);

/// Bundle (client, token) so action calls are one provider.read away.
class RssApi {
  final AppsClient client;
  final String token;
  const RssApi(this.client, this.token);

  Future<Map<String, dynamic>> invoke(String action,
      [Map<String, dynamic> input = const {}]) {
    return client.invoke(
      identifier: 'rss',
      action: action,
      input: input,
      token: token,
    );
  }
}

final rssApiProvider = Provider<RssApi?>((ref) {
  final client = ref.watch(appsClientProvider);
  final token = ref.watch(appsBearerProvider);
  if (client == null || token == null || token.isEmpty) return null;
  return RssApi(client, token);
});

/// M10.1: 从 bearer JWT 解出 sub 作 cache 隔离 key. 只解 payload 不验签
/// —— 纯本地命名空间用途, 串了也只是缓存隔离失效不是安全问题. 解不出
/// 时返 'anon' (单设备单用户场景仍工作).
final rssScopeIdProvider = Provider<String>((ref) {
  final token = ref.watch(appsBearerProvider);
  if (token == null || token.isEmpty) return 'anon';
  return _jwtClaim(token, 'sub') ?? 'anon';
});

/// M11.2: caller 的 org_id (从 JWT org_id claim 解). null = 个人用户 (无
/// org), 此时「团队」段不显示, scope 永远 'user'.
final rssOrgIdProvider = Provider<String?>((ref) {
  final token = ref.watch(appsBearerProvider);
  if (token == null || token.isEmpty) return null;
  return _jwtClaim(token, 'org_id');
});

/// M11.2: 当前视图 scope —— 'user'(我的) / 'org'(团队). 顶部 SegmentedButton
/// 切换. 个人用户(无 org_id)固定 'user'.
final rssScopeProvider = StateProvider<String>((ref) => 'user');

/// M11.2: 缓存隔离 key. org scope 用 `org:<org_id>`, user scope 用
/// `user:<sub>` —— 防止「我的 / 团队」两套数据串缓存. org_id 缺失时
/// 回退 user scope.
final rssCacheScopeProvider = Provider<String>((ref) {
  final scope = ref.watch(rssScopeProvider);
  if (scope == 'org') {
    final orgId = ref.watch(rssOrgIdProvider);
    if (orgId != null && orgId.isNotEmpty) return 'org:$orgId';
  }
  return 'user:${ref.watch(rssScopeIdProvider)}';
});

String? _jwtClaim(String jwt, String key) {
  try {
    final parts = jwt.split('.');
    if (parts.length != 3) return null;
    var payload = parts[1].replaceAll('-', '+').replaceAll('_', '/');
    switch (payload.length % 4) {
      case 2:
        payload += '==';
        break;
      case 3:
        payload += '=';
        break;
    }
    final decoded = utf8.decode(base64.decode(payload));
    final map = jsonDecode(decoded) as Map<String, dynamic>;
    final v = map[key];
    return v is String && v.isNotEmpty ? v : null;
  } catch (_) {
    return null;
  }
}

/// M10.1: RSS 本地缓存 DAO. 复用全局 appDbProvider (单例 Drift), 不 new
/// 第二个 AppDb.
final rssCacheDaoProvider = Provider<RssCacheDao>((ref) {
  return RssCacheDao(ref.watch(appDbProvider), DateTime.now);
});

List<Map<String, dynamic>> _items(Map<String, dynamic> resp) {
  final r = resp['result'];
  if (r is Map) {
    final items = r['items'];
    if (items is List) {
      return items.whereType<Map>().map((e) => e.cast<String, dynamic>()).toList();
    }
  }
  // Some action responses (eg. rules_list) may not be wrapped under
  // `result`; handle both shapes.
  final items = resp['items'];
  if (items is List) {
    return items.whereType<Map>().map((e) => e.cast<String, dynamic>()).toList();
  }
  return const [];
}

// ─── feeds ────────────────────────────────────────────────────────

/// feeds 列表 —— cache-first: 先 yield 本地缓存 (秒显), 再 yield 网络
/// 结果并刷新缓存. 离线 / 网络失败时缓存仍可用 (网络异常吞掉只记不抛,
/// 已有缓存时不让整个 provider 进 error 态).
final feedsProvider = StreamProvider<List<Feed>>((ref) async* {
  final api = ref.watch(rssApiProvider);
  if (api == null) {
    yield const [];
    return;
  }
  final dao = ref.watch(rssCacheDaoProvider);
  final scopeId = ref.watch(rssCacheScopeProvider);
  final scope = ref.watch(rssScopeProvider);

  // 1. 缓存先行
  final cached = await dao.readFeeds(scopeId);
  if (cached.isNotEmpty) yield cached;

  // 2. 网络刷新
  try {
    final r = await api.invoke('feeds_list', {if (scope != 'user') 'scope': scope});
    final raw = _items(r);
    await dao.replaceFeeds(scopeId, raw);
    yield raw.map(Feed.fromJson).toList(growable: false);
  } catch (e) {
    // 有缓存就不抛 (离线可读); 无缓存才把错误冒出去让 UI 显示 retry.
    if (cached.isEmpty) rethrow;
  }
});

// ─── entries ──────────────────────────────────────────────────────

class EntriesQuery {
  final String feedId; // 'all' = no feed filter
  final bool unreadOnly;
  const EntriesQuery({required this.feedId, this.unreadOnly = false});

  @override
  bool operator ==(Object other) =>
      other is EntriesQuery &&
      other.feedId == feedId &&
      other.unreadOnly == unreadOnly;

  @override
  int get hashCode => Object.hash(feedId, unreadOnly);
}

final entriesProvider =
    StreamProvider.family<List<Entry>, EntriesQuery>((ref, q) async* {
  final api = ref.watch(rssApiProvider);
  if (api == null) {
    yield const [];
    return;
  }
  final dao = ref.watch(rssCacheDaoProvider);
  final scopeId = ref.watch(rssCacheScopeProvider);
  final scope = ref.watch(rssScopeProvider);

  // 1. 缓存先行 (杀进程重启秒显). unreadOnly 时缓存里也过滤一下.
  var cached = await dao.readEntries(scopeId, feedId: q.feedId);
  if (q.unreadOnly) cached = cached.where((e) => e.unread).toList();
  if (cached.isNotEmpty) yield _sortEntries(cached);

  // 2. 网络刷新 + 回写缓存
  try {
    final input = <String, dynamic>{
      if (q.feedId != 'all' && q.feedId.isNotEmpty) 'feed_id': q.feedId,
      if (q.unreadOnly) 'unread_only': true,
      if (scope != 'user') 'scope': scope,
      'limit': 100,
    };
    final r = await api.invoke('entries_list', input);
    final raw = _items(r);
    await dao.upsertEntries(scopeId, raw);
    yield _sortEntries(raw.map(Entry.fromJson).toList());
  } catch (e) {
    if (cached.isEmpty) rethrow;
  }
});

List<Entry> _sortEntries(List<Entry> list) {
  list.sort((a, b) {
    final ad = a.publishedAt ?? a.fetchedAt ?? DateTime(1970);
    final bd = b.publishedAt ?? b.fetchedAt ?? DateTime(1970);
    return bd.compareTo(ad);
  });
  return list;
}

// ─── rules + hits ─────────────────────────────────────────────────

/// M9: 雷达规则的 action 执行历史. family 按 ruleId 分流.
/// 切换规则会自动加载新规则历史; 同 rule 内自动复用 cache (5min TTL).
final actionRunsProvider =
    FutureProvider.family<List<Map<String, dynamic>>, String>((ref, ruleId) async {
  final actions = ref.watch(rssActionsProvider);
  if (actions == null) return [];
  return actions.actionRunsList(ruleId, limit: 20);
});

final rulesProvider = FutureProvider<List<Rule>>((ref) async {
  final api = ref.watch(rssApiProvider);
  if (api == null) return const [];
  final scope = ref.watch(rssScopeProvider);
  final r = await api.invoke('rules_list', {if (scope != 'user') 'scope': scope});
  return _items(r).map(Rule.fromJson).toList(growable: false);
});

class HitsQuery {
  final String? ruleId;
  final bool unreadOnly;
  const HitsQuery({this.ruleId, this.unreadOnly = false});

  @override
  bool operator ==(Object other) =>
      other is HitsQuery &&
      other.ruleId == ruleId &&
      other.unreadOnly == unreadOnly;

  @override
  int get hashCode => Object.hash(ruleId, unreadOnly);
}

final hitsProvider =
    FutureProvider.family<List<Hit>, HitsQuery>((ref, q) async {
  final api = ref.watch(rssApiProvider);
  if (api == null) return const [];
  final scope = ref.watch(rssScopeProvider);
  final input = <String, dynamic>{
    if (q.ruleId != null && q.ruleId!.isNotEmpty) 'rule_id': q.ruleId,
    if (q.unreadOnly) 'unread_only': true,
    if (scope != 'user') 'scope': scope,
    'limit': 200,
  };
  final r = await api.invoke('hits_list', input);
  return _items(r).map(Hit.fromJson).toList(growable: false);
});

// ─── today ────────────────────────────────────────────────────────

final todayProvider = FutureProvider<TodayPicks>((ref) async {
  final api = ref.watch(rssApiProvider);
  if (api == null) return const TodayPicks();
  final r = await api.invoke('today_picks');
  final result = (r['result'] as Map?)?.cast<String, dynamic>() ?? r;
  return TodayPicks.fromJson(result);
});

// ─── starter packs / saved (M6) ───────────────────────────────────

final starterPacksProvider = FutureProvider<List<StarterPack>>((ref) async {
  final api = ref.watch(rssApiProvider);
  if (api == null) return const [];
  final r = await api.invoke('starter_packs_list');
  return _items(r).map(StarterPack.fromJson).toList();
});

final savedProvider = FutureProvider.family<List<SavedItem>, String>(
    (ref, mark) async {
  final api = ref.watch(rssApiProvider);
  if (api == null) return const [];
  final r = await api.invoke('marks_list', {'mark': mark, 'limit': 200});
  return _items(r).map(SavedItem.fromJson).toList();
});

// ─── boards ───────────────────────────────────────────────────────

final boardsProvider = FutureProvider<List<Board>>((ref) async {
  final api = ref.watch(rssApiProvider);
  if (api == null) return const [];
  final r = await api.invoke('boards_list');
  return _items(r).map(Board.fromJson).toList(growable: false);
});

// Each card shows the full top-30 list (newsnow-style); no separate
// "expanded" provider needed.
final boardSnapshotProvider =
    FutureProvider.family<BoardSnapshot, String>((ref, boardId) async {
  final api = ref.watch(rssApiProvider);
  if (api == null) {
    return BoardSnapshot(board: Board(id: boardId, name: ''));
  }
  final r = await api.invoke('boards_snapshot', {
    'board_id': boardId,
    'limit': 30,
  });
  final result = (r['result'] as Map?)?.cast<String, dynamic>() ?? const {};
  return BoardSnapshot.fromJson(result);
});

// ─── action callers (thin wrappers over invoke) ────────────────────

class RssActions {
  RssActions(this._api, [this._scope = 'user']);
  final RssApi _api;
  // M11.2: ambient view scope ('user'/'org'). Injected from
  // rssScopeProvider so org-scoped mutations carry scope without every
  // call site threading it. user scope omits the field (server default).
  final String _scope;

  Map<String, dynamic> _scoped([Map<String, dynamic> base = const {}]) => {
        ...base,
        if (_scope != 'user') 'scope': _scope,
      };

  Future<void> feedsAdd(String url,
      {String? title, String? category, String? kind}) async {
    await _api.invoke('feeds_add', _scoped({
      'url': url,
      if (title != null && title.isNotEmpty) 'title': title,
      if (category != null && category.isNotEmpty) 'category': category,
      // M13.3/13.4 — explicit source kind tells the server the relay URL is
      // already final (skip auto-discovery) and drives the inbox badge.
      if (kind != null && kind.isNotEmpty && kind != 'rss') 'kind': kind,
    }));
  }

  Future<void> feedsRemove(String id) async {
    await _api.invoke('feeds_remove', _scoped({'id': id}));
  }

  Future<Map<String, dynamic>> feedsRefresh() async {
    final r = await _api.invoke('feeds_refresh');
    final result = (r['result'] as Map?)?.cast<String, dynamic>();
    return result ?? r;
  }

  Future<void> entriesMarkRead(String id, bool read) async {
    await _api.invoke('entries_mark_read', {'id': id, 'read': read});
  }

  Future<void> entriesStar(String id, bool starred) async {
    await _api.invoke('entries_star', {'id': id, 'starred': starred});
  }

  // T10.4.3 — 跨设备续读. pct = 已滚动比例 0..1. 位置按 user_id 键(服务端
  // 从 claims 取),与 feed scope 无关,故不走 _scoped.
  Future<void> entryProgressSet(String id, double pct) async {
    await _api.invoke('entry_progress_set', {'entry_id': id, 'pct': pct});
  }

  /// 返回保存的滚动比例 0..1;无记录返回 0.
  Future<double> entryProgressGet(String id) async {
    final r = await _api.invoke('entry_progress_get', {'entry_id': id});
    final result = (r['result'] as Map?)?.cast<String, dynamic>() ?? r;
    final v = result['pct'];
    return v is num ? v.toDouble() : 0.0;
  }

  Future<void> rulesCreate({
    required String name,
    List<String> matchAny = const [],
    List<String> matchAll = const [],
    List<String> exclude = const [],
    List<String> sources = const ['*'],
    String onHitBadge = 'warn',
    int cooldownSec = 1800,
    String? semanticQuery,
    List<Map<String, dynamic>>? actions,
  }) async {
    await _api.invoke('rules_create', _scoped({
      'name': name,
      'match_any': matchAny,
      'match_all': matchAll,
      'exclude': exclude,
      'sources': sources,
      'on_hit_badge': onHitBadge,
      'on_hit_notify': const <String>[],
      'cooldown_sec': cooldownSec,
      if (semanticQuery != null && semanticQuery.isNotEmpty)
        'semantic_query': semanticQuery,
      'actions': ?actions,
    }));
  }

  Future<void> rulesUpdate({
    required String id,
    bool? enabled,
    String? name,
    String? onHitBadge,
    int? cooldownSec,
    String? semanticQuery,
    List<Map<String, dynamic>>? actions,
  }) async {
    await _api.invoke('rules_update', _scoped({
      'id': id,
      'enabled': ?enabled,
      'name': ?name,
      'on_hit_badge': ?onHitBadge,
      'cooldown_sec': ?cooldownSec,
      'semantic_query': ?semanticQuery,
      'actions': ?actions,
    }));
  }

  /// M9: 雷达规则的 action 执行历史 (rule_id, limit).
  Future<List<Map<String, dynamic>>> actionRunsList(String ruleId,
      {int limit = 20}) async {
    final r = await _api.invoke('action_runs_list', {
      'rule_id': ruleId,
      'limit': limit,
    });
    final result = (r['result'] as Map?)?.cast<String, dynamic>() ?? r;
    final items = result['items'];
    if (items is List) {
      return items
          .whereType<Map>()
          .map((m) => m.cast<String, dynamic>())
          .toList();
    }
    return [];
  }

  /// M9.4: RSS Co-Pilot 同步 Q&A. 返 (answer markdown, citations, items_seen).
  Future<CopilotAnswer> copilotAsk({
    required String question,
    String viewKind = 'today',
    String? currentEntryId,
  }) async {
    final r = await _api.invoke('copilot_ask', {
      'view_kind': viewKind,
      'question': question,
      'current_entry_id': ?currentEntryId,
    });
    final result = (r['result'] as Map?)?.cast<String, dynamic>() ?? r;
    final cits = (result['citations'] as List?) ?? const [];
    return CopilotAnswer(
      answer: result['answer']?.toString() ?? '',
      citations: cits
          .whereType<Map>()
          .map((m) => CopilotCitation.fromJson(m.cast<String, dynamic>()))
          .toList(),
      itemsSeen: result['items_seen'] is int ? result['items_seen'] as int : 0,
    );
  }

  Future<void> rulesDelete(String id) async {
    await _api.invoke('rules_delete', _scoped({'id': id}));
  }

  /// Asks the LLM to draft a rule from natural language. Caller wraps
  /// the resulting fields back into the form for human review before
  /// saving. Throws on LLM failure — caller should surface a SnackBar.
  Future<RuleDraft> rulesFromNL(String text) async {
    final r = await _api.invoke('rules_from_nl', {'text': text});
    final result = (r['result'] as Map?)?.cast<String, dynamic>() ?? r;
    return RuleDraft.fromJson(result);
  }

  Future<void> hitsMarkRead(int id) async {
    await _api.invoke('hits_mark_read', _scoped({'id': id}));
  }

  /// M11.3 — mint a public read-only share link for a view. Returns the
  /// absolute URL + expiry. Carries the ambient scope so a team view is
  /// shared with org-scoped data.
  Future<ShareLink> sharesCreate(String viewKind,
      {Map<String, dynamic>? filter, int expiresInDays = 30}) async {
    final r = await _api.invoke('shares_create', _scoped({
      'view_kind': viewKind,
      if (filter != null && filter.isNotEmpty) 'filter': filter,
      'expires_in_days': expiresInDays,
    }));
    final result = (r['result'] as Map?)?.cast<String, dynamic>() ?? r;
    return ShareLink(
      url: result['url']?.toString() ?? '',
      token: result['token']?.toString() ?? '',
      expiresAt: DateTime.tryParse(result['expires_at']?.toString() ?? ''),
    );
  }

  /// M11.5 — read the caller's RSS preferences (whole config object).
  Future<Map<String, dynamic>> userPrefsGet() async {
    final r = await _api.invoke('user_prefs_get');
    final result = (r['result'] as Map?)?.cast<String, dynamic>() ?? r;
    final cfg = result['config'];
    return cfg is Map ? cfg.cast<String, dynamic>() : <String, dynamic>{};
  }

  /// M11.5 — write the whole RSS preferences object.
  Future<void> userPrefsUpdate(Map<String, dynamic> config) async {
    await _api.invoke('user_prefs_update', {'config': config});
  }

  /// M11.5 — export all subscriptions as OPML XML.
  Future<String> opmlExport() async {
    final r = await _api.invoke('opml_export');
    final result = (r['result'] as Map?)?.cast<String, dynamic>() ?? r;
    return result['opml']?.toString() ?? result['xml']?.toString() ?? '';
  }

  /// M14.3 — full data takeout. Returns {archive_b64, filename, size, counts}.
  Future<Map<String, dynamic>> exportArchive() async {
    final r = await _api.invoke('export_archive');
    return (r['result'] as Map?)?.cast<String, dynamic>() ?? r;
  }

  /// M3 — sink an entry into the user's BiuMind Wiki. Returns the
  /// created page id + suggested tags from ai_topics.
  Future<WikiSinkResult> entriesToWiki(String entryId) async {
    final r = await _api.invoke('entries_to_wiki', {'id': entryId});
    final result = (r['result'] as Map?)?.cast<String, dynamic>() ?? r;
    return WikiSinkResult.fromJson(result);
  }
}

/// M11.3 — public share link returned by shares_create.
class ShareLink {
  final String url;
  final String token;
  final DateTime? expiresAt;
  const ShareLink({required this.url, required this.token, this.expiresAt});
}

class WikiSinkResult {
  final String pageId;
  final String projectId;
  final List<String> suggestedTags;
  const WikiSinkResult({
    required this.pageId,
    required this.projectId,
    required this.suggestedTags,
  });
  factory WikiSinkResult.fromJson(Map<String, dynamic> j) => WikiSinkResult(
        pageId: (j['page_id'] as String?) ?? '',
        projectId: (j['project_id'] as String?) ?? '',
        suggestedTags: ((j['suggested_tags'] as List?) ?? const [])
            .whereType<String>()
            .toList(),
      );
}

/// LLM suggestion projection. Mirrors the server's RuleSuggestion JSON.
class RuleDraft {
  final String name;
  final List<String> matchAny;
  final List<String> matchAll;
  final List<String> exclude;
  final String onHitBadge;
  final int cooldownSec;
  const RuleDraft({
    required this.name,
    required this.matchAny,
    required this.matchAll,
    required this.exclude,
    required this.onHitBadge,
    required this.cooldownSec,
  });
  factory RuleDraft.fromJson(Map<String, dynamic> j) {
    List<String> arr(dynamic v) =>
        (v as List?)?.whereType<String>().toList() ?? const [];
    return RuleDraft(
      name: (j['name'] as String?) ?? '',
      matchAny: arr(j['match_any']),
      matchAll: arr(j['match_all']),
      exclude: arr(j['exclude']),
      onHitBadge: (j['on_hit_badge'] as String?) ?? 'warn',
      cooldownSec: (j['cooldown_sec'] as int?) ?? 1800,
    );
  }
}

final rssActionsProvider = Provider<RssActions?>((ref) {
  final api = ref.watch(rssApiProvider);
  if (api == null) return null;
  return RssActions(api, ref.watch(rssScopeProvider));
});

/// Convenience: invalidate every read-only fetch when an action runs.
/// We drop a cache rather than try to surgically patch each list — the
/// volumes here are tiny (≤ a few hundred rows) so a re-fetch is fast
/// and the resulting state is guaranteed-correct.
extension RssRefresh on Ref {
  void refreshFeeds() => invalidate(feedsProvider);
  void refreshEntries() => invalidate(entriesProvider);
  void refreshRules() => invalidate(rulesProvider);
  void refreshHits() => invalidate(hitsProvider);
  void refreshActionRuns() => invalidate(actionRunsProvider);
  void refreshBoards() {
    invalidate(boardsProvider);
    invalidate(boardSnapshotProvider);
  }
}

extension RssRefreshOnRef on WidgetRef {
  void refreshFeeds() => invalidate(feedsProvider);
  void refreshEntries() => invalidate(entriesProvider);
  void refreshRules() => invalidate(rulesProvider);
  void refreshHits() => invalidate(hitsProvider);
  void refreshActionRuns() => invalidate(actionRunsProvider);
  void refreshBoards() {
    invalidate(boardsProvider);
    invalidate(boardSnapshotProvider);
  }
}

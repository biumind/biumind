// RSS app domain models — thin Dart projections of the JSON returned
// by services/app_center → biuapp/rss action handlers.
//
// We deliberately keep these dumb data classes (no business logic).
// All mutations go through the actions layer; UI state derives from
// fresh fetches + optimistic local overrides.

import 'dart:convert';

DateTime? _parseTime(Object? v) {
  if (v == null) return null;
  if (v is String) {
    if (v.isEmpty) return null;
    return DateTime.tryParse(v)?.toLocal();
  }
  return null;
}

int _asInt(Object? v) {
  if (v is int) return v;
  if (v is num) return v.toInt();
  if (v is String) return int.tryParse(v) ?? 0;
  return 0;
}

double _asDouble(Object? v) {
  if (v is num) return v.toDouble();
  if (v is String) return double.tryParse(v) ?? 0;
  return 0;
}

String _asString(Object? v) => v?.toString() ?? '';

bool _asBool(Object? v) {
  if (v is bool) return v;
  if (v is num) return v != 0;
  if (v is String) return v == 'true' || v == '1';
  return false;
}

List<String> _asStringList(Object? v) {
  if (v is List) {
    return v.map((e) => e?.toString() ?? '').where((s) => s.isNotEmpty).toList();
  }
  return const [];
}

List<Map<String, dynamic>> _asMapList(Object? v) {
  if (v is List) {
    return v
        .whereType<Map>()
        .map((m) => m.cast<String, dynamic>())
        .toList();
  }
  return const [];
}

class StarterPack {
  final String id;
  final String name;
  final String description;
  final String iconEmoji;
  final List<Map<String, dynamic>> feeds;
  const StarterPack({
    required this.id,
    required this.name,
    required this.description,
    required this.iconEmoji,
    required this.feeds,
  });
  factory StarterPack.fromJson(Map<String, dynamic> j) => StarterPack(
        id: _asString(j['id']),
        name: _asString(j['name']),
        description: _asString(j['description']),
        iconEmoji: _asString(j['icon_emoji']),
        feeds: ((j['feeds'] as List?) ?? const [])
            .whereType<Map>()
            .map((m) => m.cast<String, dynamic>())
            .toList(),
      );
  int get feedCount => feeds.length;
}

class SavedItem {
  final String id;
  final String feedId;
  final String feedTitle;
  final String title;
  final String url;
  final String aiTakeaway;
  final List<String> aiTopics;
  final DateTime? markedAt;
  final String wikiBlockId;
  const SavedItem({
    required this.id,
    this.feedId = '',
    this.feedTitle = '',
    required this.title,
    this.url = '',
    this.aiTakeaway = '',
    this.aiTopics = const [],
    this.markedAt,
    this.wikiBlockId = '',
  });
  factory SavedItem.fromJson(Map<String, dynamic> j) => SavedItem(
        id: _asString(j['id']),
        feedId: _asString(j['feed_id']),
        feedTitle: _asString(j['feed_title']),
        title: _asString(j['title']),
        url: _asString(j['url']),
        aiTakeaway: _asString(j['ai_takeaway']),
        aiTopics: _asStringList(j['ai_topics']),
        markedAt: _parseTime(j['marked_at']),
        wikiBlockId: _asString(j['wiki_block_id']),
      );
}

class TodayPicks {
  final List<TodayEntry> headline;
  final List<TodayEntry> missed;
  final List<TodayTrend> trends;
  final TodayStats stats;
  final DateTime? generatedAt;
  const TodayPicks({
    this.headline = const [],
    this.missed = const [],
    this.trends = const [],
    this.stats = const TodayStats(),
    this.generatedAt,
  });
  factory TodayPicks.fromJson(Map<String, dynamic> j) => TodayPicks(
        headline: (j['headline'] as List?)
                ?.whereType<Map>()
                .map((e) => TodayEntry.fromJson(e.cast<String, dynamic>()))
                .toList() ??
            const [],
        missed: (j['missed'] as List?)
                ?.whereType<Map>()
                .map((e) => TodayEntry.fromJson(e.cast<String, dynamic>()))
                .toList() ??
            const [],
        trends: (j['trends'] as List?)
                ?.whereType<Map>()
                .map((e) => TodayTrend.fromJson(e.cast<String, dynamic>()))
                .toList() ??
            const [],
        stats: TodayStats.fromJson(
            (j['stats'] as Map?)?.cast<String, dynamic>() ?? const {}),
        generatedAt: _parseTime(j['generated_at']),
      );
}

class TodayEntry {
  final String id;
  final String feedId;
  final String feedTitle;
  final String url;
  final String title;
  final String author;
  final String aiTakeaway;
  final List<String> aiBullets;
  final List<String> aiTopics;
  final int aiImportance;
  final int wordCount;
  final int readingSeconds;
  final int clusterSize;
  final List<String> otherUrls;
  final DateTime? publishedAt;
  const TodayEntry({
    required this.id,
    this.feedId = '',
    this.feedTitle = '',
    this.url = '',
    required this.title,
    this.author = '',
    this.aiTakeaway = '',
    this.aiBullets = const [],
    this.aiTopics = const [],
    this.aiImportance = 0,
    this.wordCount = 0,
    this.readingSeconds = 0,
    this.clusterSize = 1,
    this.otherUrls = const [],
    this.publishedAt,
  });
  factory TodayEntry.fromJson(Map<String, dynamic> j) => TodayEntry(
        id: _asString(j['id']),
        feedId: _asString(j['feed_id']),
        feedTitle: _asString(j['feed_title']),
        url: _asString(j['url']),
        title: _asString(j['title']).isEmpty ? '(无标题)' : _asString(j['title']),
        author: _asString(j['author']),
        aiTakeaway: _asString(j['ai_takeaway']),
        aiBullets: _asStringList(j['ai_bullets']),
        aiTopics: _asStringList(j['ai_topics']),
        aiImportance: _asInt(j['ai_importance']),
        wordCount: _asInt(j['word_count']),
        readingSeconds: _asInt(j['reading_seconds']),
        clusterSize: _asInt(j['cluster_size']) <= 0 ? 1 : _asInt(j['cluster_size']),
        otherUrls: _asStringList(j['other_urls']),
        publishedAt: _parseTime(j['published_at']),
      );
}

class TodayTrend {
  final String topic;
  final int count;
  const TodayTrend({required this.topic, required this.count});
  factory TodayTrend.fromJson(Map<String, dynamic> j) => TodayTrend(
        topic: _asString(j['topic']),
        count: _asInt(j['count']),
      );
}

class TodayStats {
  final int unreadTotal;
  final int readToday;
  final int streakDays;
  final int wikiThisWeek;
  const TodayStats({
    this.unreadTotal = 0,
    this.readToday = 0,
    this.streakDays = 0,
    this.wikiThisWeek = 0,
  });
  factory TodayStats.fromJson(Map<String, dynamic> j) => TodayStats(
        unreadTotal: _asInt(j['unread_total']),
        readToday: _asInt(j['read_today']),
        streakDays: _asInt(j['streak_days']),
        wikiThisWeek: _asInt(j['wiki_this_week']),
      );
}

class Feed {
  final String id;
  final String feedUrl;
  final String siteUrl;
  final String title;
  final String description;
  final String iconUrl;
  final String category;
  final String lastStatus;
  final String lastError;
  final bool enabled;
  final bool forced; // M11.4 — 由组织强制订阅, 成员不可删
  final String kind; // M13.1 — 来源类型: rss/wechat/x/podcast (用于来源角标)
  final int unread;
  final DateTime? lastFetchedAt;
  final DateTime? createdAt;

  const Feed({
    required this.id,
    required this.feedUrl,
    this.siteUrl = '',
    required this.title,
    this.description = '',
    this.iconUrl = '',
    this.category = '',
    this.lastStatus = '',
    this.lastError = '',
    this.enabled = true,
    this.forced = false,
    this.kind = 'rss',
    this.unread = 0,
    this.lastFetchedAt,
    this.createdAt,
  });

  factory Feed.fromJson(Map<String, dynamic> j) => Feed(
        id: _asString(j['id']),
        feedUrl: _asString(j['feed_url'] ?? j['url']),
        siteUrl: _asString(j['site_url']),
        title: _asString(j['title']).isEmpty
            ? _asString(j['feed_url'])
            : _asString(j['title']),
        description: _asString(j['description']),
        iconUrl: _asString(j['icon_url']),
        category: _asString(j['category']),
        lastStatus: _asString(j['last_status']),
        lastError: _asString(j['last_error']),
        enabled: _asBool(j['enabled']),
        forced: _asBool(j['forced']),
        kind: _asString(j['kind']).isEmpty ? 'rss' : _asString(j['kind']),
        unread: _asInt(j['unread']),
        lastFetchedAt: _parseTime(j['last_fetched_at'] ?? j['last_fetch']),
        createdAt: _parseTime(j['created_at'] ?? j['added_at']),
      );
}

class Entry {
  final String id;
  final String feedId;
  final String guid;
  final String url;
  final String title;
  final String author;
  final String snippet;
  final String contentHtml; // best-effort; backend may only return snippet
  final bool unread;
  final bool starred;
  final DateTime? publishedAt;
  final DateTime? fetchedAt;

  // M1 — AI digest. aiProcessed flips true on either success or
  // permanent error; UI uses aiTakeaway / aiBullets / aiImportance to
  // decide whether to render the AI Card or the error state.
  final bool aiProcessed;
  final String aiTakeaway;
  final List<String> aiBullets;
  final List<String> aiTopics;
  final int aiImportance; // 0 unset, 1-3 valid
  final String aiLang;
  final String aiError;

  // Word / read-time estimate (200wpm). 0 when content_text empty.
  final int wordCount;
  final int readingSeconds;

  // M13.5 — podcast audio enclosure. enclosureUrl empty for ordinary
  // articles; transcribed = true once the worker filled content from audio.
  final String enclosureUrl;
  final String enclosureType;
  final bool transcribed;
  // M13.5 Tier2 — sentence segments for synced playback (empty unless a
  // transcribed podcast). Sorted by start time.
  final List<TranscriptSegment> transcriptSegments;

  const Entry({
    required this.id,
    required this.feedId,
    this.guid = '',
    this.url = '',
    required this.title,
    this.author = '',
    this.snippet = '',
    this.contentHtml = '',
    this.unread = true,
    this.starred = false,
    this.publishedAt,
    this.fetchedAt,
    this.aiProcessed = false,
    this.aiTakeaway = '',
    this.aiBullets = const [],
    this.aiTopics = const [],
    this.aiImportance = 0,
    this.aiLang = '',
    this.aiError = '',
    this.wordCount = 0,
    this.readingSeconds = 0,
    this.enclosureUrl = '',
    this.enclosureType = '',
    this.transcribed = false,
    this.transcriptSegments = const [],
  });

  Entry copyWith({bool? unread, bool? starred}) => Entry(
        id: id,
        feedId: feedId,
        guid: guid,
        url: url,
        title: title,
        author: author,
        snippet: snippet,
        contentHtml: contentHtml,
        unread: unread ?? this.unread,
        starred: starred ?? this.starred,
        publishedAt: publishedAt,
        fetchedAt: fetchedAt,
        aiProcessed: aiProcessed,
        aiTakeaway: aiTakeaway,
        aiBullets: aiBullets,
        aiTopics: aiTopics,
        aiImportance: aiImportance,
        aiLang: aiLang,
        aiError: aiError,
        wordCount: wordCount,
        readingSeconds: readingSeconds,
        enclosureUrl: enclosureUrl,
        enclosureType: enclosureType,
        transcribed: transcribed,
        transcriptSegments: transcriptSegments,
      );

  factory Entry.fromJson(Map<String, dynamic> j) => Entry(
        id: _asString(j['id']),
        feedId: _asString(j['feed_id']),
        guid: _asString(j['guid']),
        url: _asString(j['url']),
        title: _asString(j['title']).isEmpty ? '(无标题)' : _asString(j['title']),
        author: _asString(j['author']),
        snippet: _asString(j['snippet']),
        contentHtml: _asString(j['content_html']),
        unread: _asBool(j['unread']),
        starred: _asBool(j['starred']),
        publishedAt: _parseTime(j['published_at']),
        fetchedAt: _parseTime(j['fetched_at']),
        aiProcessed: _asBool(j['ai_processed']),
        aiTakeaway: _asString(j['ai_takeaway']),
        aiBullets: _asStringList(j['ai_bullets']),
        aiTopics: _asStringList(j['ai_topics']),
        aiImportance: _asInt(j['ai_importance']),
        aiLang: _asString(j['ai_lang']),
        aiError: _asString(j['ai_error']),
        wordCount: _asInt(j['word_count']),
        readingSeconds: _asInt(j['reading_seconds']),
        enclosureUrl: _asString(j['enclosure_url']),
        enclosureType: _asString(j['enclosure_type']),
        transcribed: _asBool(j['transcribed']),
        transcriptSegments: (j['transcript_segments'] as List?)
                ?.whereType<Map>()
                .map((m) => TranscriptSegment.fromJson(m.cast<String, dynamic>()))
                .toList() ??
            const [],
      );
}

/// M13.5 Tier2 — one transcript sentence with its audio time range (seconds).
class TranscriptSegment {
  final double start;
  final double end;
  final String text;
  const TranscriptSegment(
      {required this.start, required this.end, required this.text});

  factory TranscriptSegment.fromJson(Map<String, dynamic> j) => TranscriptSegment(
        start: _asDouble(j['start']),
        end: _asDouble(j['end']),
        text: _asString(j['text']),
      );
}

class Rule {
  final String id;
  final String name;
  final List<String> matchAny;
  final List<String> matchAll;
  final List<String> exclude;
  final List<String> sources;
  final String onHitBadge; // 'info' | 'warn' | 'error'
  final List<String> onHitNotify;
  final int cooldownSec;
  final bool enabled;
  final DateTime? createdAt;
  // M9: rule.actions[] — 命中后顺序执行的动作配方.
  final List<Map<String, dynamic>> actions;
  // M8.2: 语义查询 (rule.semantic_query). 客户端只读 + 可编辑, 真正
  // embedding 计算在后端.
  final String semanticQuery;

  const Rule({
    required this.id,
    required this.name,
    this.matchAny = const [],
    this.matchAll = const [],
    this.exclude = const [],
    this.sources = const ['*'],
    this.onHitBadge = 'warn',
    this.onHitNotify = const [],
    this.cooldownSec = 1800,
    this.enabled = true,
    this.createdAt,
    this.actions = const [],
    this.semanticQuery = '',
  });

  factory Rule.fromJson(Map<String, dynamic> j) => Rule(
        id: _asString(j['id']),
        name: _asString(j['name']),
        matchAny: _asStringList(j['match_any']),
        matchAll: _asStringList(j['match_all']),
        exclude: _asStringList(j['exclude']),
        sources: _asStringList(j['sources']).isEmpty
            ? const ['*']
            : _asStringList(j['sources']),
        onHitBadge: _asString(j['on_hit_badge']).isEmpty
            ? 'warn'
            : _asString(j['on_hit_badge']),
        onHitNotify: _asStringList(j['on_hit_notify']),
        cooldownSec: _asInt(j['cooldown_sec']) == 0 ? 1800 : _asInt(j['cooldown_sec']),
        enabled: j.containsKey('enabled') ? _asBool(j['enabled']) : true,
        createdAt: _parseTime(j['created_at']),
        actions: _asMapList(j['actions']),
        semanticQuery: _asString(j['semantic_query']),
      );

  Rule copyWith({
    String? name,
    bool? enabled,
    String? onHitBadge,
    int? cooldownSec,
    List<String>? matchAny,
    List<String>? matchAll,
    List<String>? exclude,
    List<String>? sources,
  }) =>
      Rule(
        id: id,
        name: name ?? this.name,
        matchAny: matchAny ?? this.matchAny,
        matchAll: matchAll ?? this.matchAll,
        exclude: exclude ?? this.exclude,
        sources: sources ?? this.sources,
        onHitBadge: onHitBadge ?? this.onHitBadge,
        onHitNotify: onHitNotify,
        cooldownSec: cooldownSec ?? this.cooldownSec,
        enabled: enabled ?? this.enabled,
        createdAt: createdAt,
      );
}

class Hit {
  final int id;
  final String ruleId;
  final String ruleName;
  final String source;
  final String title;
  final String url;
  final bool unread;
  final String severity; // info|warn|error
  final String severityLabel;
  final DateTime? hitAt;

  const Hit({
    required this.id,
    required this.ruleId,
    required this.ruleName,
    this.source = '',
    required this.title,
    this.url = '',
    this.unread = true,
    this.severity = 'info',
    this.severityLabel = '',
    this.hitAt,
  });

  Hit copyWith({bool? unread}) => Hit(
        id: id,
        ruleId: ruleId,
        ruleName: ruleName,
        source: source,
        title: title,
        url: url,
        unread: unread ?? this.unread,
        severity: severity,
        severityLabel: severityLabel,
        hitAt: hitAt,
      );

  factory Hit.fromJson(Map<String, dynamic> j) => Hit(
        id: _asInt(j['id']),
        ruleId: _asString(j['rule_id']),
        ruleName: _asString(j['rule_name']),
        source: _asString(j['source']),
        title: _asString(j['title']).isEmpty ? '(无标题)' : _asString(j['title']),
        url: _asString(j['url']),
        unread: _asBool(j['unread']),
        severity: _asString(j['severity']).isEmpty ? 'info' : _asString(j['severity']),
        severityLabel: _asString(j['severity_label']),
        hitAt: _parseTime(j['hit_at']),
      );
}

class Board {
  final String id;
  final String name;
  final String color; // tailwind-ish name from newsnow: blue/red/green/...
  final bool enabled;
  final int refreshSec;
  final String lastStatus;
  final String lastError;
  final DateTime? lastFetchedAt;

  const Board({
    required this.id,
    required this.name,
    this.color = 'gray',
    this.enabled = true,
    this.refreshSec = 0,
    this.lastStatus = '',
    this.lastError = '',
    this.lastFetchedAt,
  });

  factory Board.fromJson(Map<String, dynamic> j) => Board(
        id: _asString(j['id']),
        name: _asString(j['name']),
        color: _asString(j['color']).isEmpty ? 'gray' : _asString(j['color']),
        enabled: j.containsKey('enabled') ? _asBool(j['enabled']) : true,
        refreshSec: _asInt(j['refresh_sec']),
        lastStatus: _asString(j['last_status']),
        lastError: _asString(j['last_error']),
        lastFetchedAt: _parseTime(j['last_fetched_at']),
      );
}

class BoardItem {
  final String id;
  final int rank;
  final String rankLabel;
  final String title;
  final String url;
  final String mobileUrl;
  final bool isNew;
  final String newLabel;
  final int rankDelta;
  final String deltaLabel;
  final String info;

  const BoardItem({
    this.id = '',
    required this.rank,
    this.rankLabel = '',
    required this.title,
    this.url = '',
    this.mobileUrl = '',
    this.isNew = false,
    this.newLabel = '',
    this.rankDelta = 0,
    this.deltaLabel = '',
    this.info = '',
  });

  factory BoardItem.fromJson(Map<String, dynamic> j) => BoardItem(
        id: _asString(j['id']),
        rank: _asInt(j['rank']),
        rankLabel: _asString(j['rank_label']),
        title: _asString(j['title']).isEmpty ? '(无标题)' : _asString(j['title']),
        url: _asString(j['url']),
        mobileUrl: _asString(j['mobile_url']),
        isNew: _asBool(j['is_new']),
        newLabel: _asString(j['new_label']),
        rankDelta: _asInt(j['rank_delta']),
        deltaLabel: _asString(j['delta_label']),
        info: _asString(j['info']),
      );
}

class BoardSnapshot {
  final Board board;
  final List<BoardItem> items;
  final DateTime? capturedAt;

  const BoardSnapshot({
    required this.board,
    this.items = const [],
    this.capturedAt,
  });

  factory BoardSnapshot.fromJson(Map<String, dynamic> j) {
    final boardMap = (j['board'] as Map?)?.cast<String, dynamic>() ?? const {};
    final list = (j['items'] as List?) ?? const [];
    return BoardSnapshot(
      board: Board.fromJson(boardMap),
      items: list
          .whereType<Map>()
          .map((e) => BoardItem.fromJson(e.cast<String, dynamic>()))
          .toList(growable: false),
      capturedAt: _parseTime(j['captured_at']),
    );
  }
}

/// Helper: human-readable relative time. Pure Dart; no `intl` formats
/// because we want zh-CN copy that matches the rest of the app.
String relativeTime(DateTime? when) {
  if (when == null) return '';
  final now = DateTime.now();
  final diff = now.difference(when);
  if (diff.isNegative) return '刚刚';
  if (diff.inSeconds < 60) return '刚刚';
  if (diff.inMinutes < 60) return '${diff.inMinutes} 分钟前';
  if (diff.inHours < 24) return '${diff.inHours} 小时前';
  if (diff.inDays < 7) return '${diff.inDays} 天前';
  if (diff.inDays < 30) return '${(diff.inDays / 7).floor()} 周前';
  if (diff.inDays < 365) return '${(diff.inDays / 30).floor()} 个月前';
  return '${(diff.inDays / 365).floor()} 年前';
}

/// Helper: full local date for the reader header.
String fullDate(DateTime? when) {
  if (when == null) return '';
  final y = when.year.toString();
  final m = when.month.toString().padLeft(2, '0');
  final d = when.day.toString().padLeft(2, '0');
  final hh = when.hour.toString().padLeft(2, '0');
  final mm = when.minute.toString().padLeft(2, '0');
  return '$y-$m-$d $hh:$mm';
}

/// Stable Uri parser for url_launcher. Returns null on garbage input
/// so callers can disable buttons rather than crash.
Uri? safeParseUri(String s) {
  if (s.isEmpty) return null;
  try {
    final u = Uri.parse(s);
    if (u.scheme.isEmpty) return null;
    return u;
  } catch (_) {
    return null;
  }
}

/// Convenience: pretty-print a debug-only JSON dump (used by error
/// fallback panes when an action returns an unexpected shape).
String debugJson(Object? v) => const JsonEncoder.withIndent('  ').convert(v);

// ─── M9.4 RSS Co-Pilot ───────────────────────────────────────────

class CopilotAnswer {
  final String answer; // markdown
  final List<CopilotCitation> citations;
  final int itemsSeen;
  const CopilotAnswer({
    required this.answer,
    required this.citations,
    required this.itemsSeen,
  });
}

class CopilotCitation {
  final int n;
  final String entryId;
  final String title;
  final String url;
  final String source;
  const CopilotCitation({
    required this.n,
    required this.entryId,
    required this.title,
    required this.url,
    required this.source,
  });
  factory CopilotCitation.fromJson(Map<String, dynamic> j) => CopilotCitation(
        n: _asInt(j['n']),
        entryId: _asString(j['entry_id']),
        title: _asString(j['title']),
        url: _asString(j['url']),
        source: _asString(j['source']),
      );
}

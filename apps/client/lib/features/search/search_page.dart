// SearchPage — biumind unified search.
//
// Integrates the best of two reference implementations:
//
//   knowcode/client/lib/features/search/search_page.dart  (云端)
//     · 300ms debounce + race-cancel
//     · AsyncValue 三态 + 多空态
//     · per-result thumbs (位置预留, 后续接 search_feedback 表)
//
//   llm_wiki/src/components/search/search-view.tsx          (Tauri)
//     · 双区: Images grid (top) + Pages list (bottom)
//     · Lightbox 大图 + jump-to-source
//     · CJK-aware token 高亮 (`<mark>`)
//     · IME composing 守护 (中文输入法 Enter 不误触)
//     · 「matching / supporting」分级 + 折叠 toggle
//
// 加上 biumind 自家增量:
//   · 4 路徽章 (BM25 / Vector / Graph / Web) — RRF 融合可解释性
//   · 图谱 hit 显示 "via [[seed]]" 来源
//   · 点击 page 命中 → /wiki?pageId= deep link (P2-H-wiki)

import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import 'package:url_launcher/url_launcher.dart';

import '../../app/theme.dart';
import '../../data/api/search_client.dart';
import '../../data/search_providers.dart';
import '../../shared/page_scaffold.dart';
import '../notes/application/notes_ui_providers.dart';
import '../settings/application/settings_controller.dart';

class SearchPage extends ConsumerStatefulWidget {
  const SearchPage({super.key});

  @override
  ConsumerState<SearchPage> createState() => _SearchPageState();
}

class _SearchPageState extends ConsumerState<SearchPage> {
  final _controller = TextEditingController();
  final _focus = FocusNode();
  Timer? _debounce;

  /// Active query that the in-flight (or last-completed) request is
  /// looking up. We compare against this in async callbacks to drop
  /// stale results when the user has typed past them.
  String _activeQuery = '';
  AsyncValue<SearchResponse?> _state = const AsyncValue.data(null);
  bool _showSupportingImages = false;
  // True while the IME is mid-composition (e.g. typing pinyin). We
  // disable the Enter-to-search shortcut while composing so the user
  // committing a Chinese candidate doesn't accidentally fire a search.
  bool _composing = false;

  /// Local thumbs state, keyed by page_id. Driven by user clicks +
  /// optimistic update; on RPC failure we roll back. We don't read
  /// past feedback from the server on first render — the UI shows
  /// neutral until the user opines, which keeps the search latency
  /// budget tight (the existing `/search` call alone is the
  /// critical-path; layering a `/feedback?for=` lookup on top would
  /// just delay the result list for a feature most users ignore).
  /// In-flight set blocks double-clicks.
  final Map<String, String> _feedbackByPage = {}; // pageId → "up"/"down"
  final Set<String> _feedbackInFlight = {};

  @override
  void dispose() {
    _debounce?.cancel();
    _controller.dispose();
    _focus.dispose();
    super.dispose();
  }

  void _onChanged(String value) {
    _debounce?.cancel();
    _debounce = Timer(const Duration(milliseconds: 300), () {
      _runSearch(value.trim());
    });
  }

  Future<void> _runSearch(String query) async {
    if (query.isEmpty) {
      setState(() {
        _activeQuery = '';
        _state = const AsyncValue.data(null);
      });
      return;
    }
    final client = ref.read(searchClientProvider);
    if (client == null) {
      setState(() {
        _state = AsyncValue.error(
          const _NoCredentialsError(), StackTrace.current);
      });
      return;
    }
    setState(() {
      _activeQuery = query;
      _state = const AsyncValue.loading();
      _showSupportingImages = false; // reset toggle on new search
      _feedbackByPage.clear();        // verdicts are per-query
      _feedbackInFlight.clear();
    });
    try {
      // 设置 → 搜索「在统一搜索中包含笔记」开关（本地持久化，默认关）；
      // 开启后请求带 include_notes=true，响应附 notes 分组。
      final includeNotes = ref
              .read(settingsControllerProvider)
              .valueOrNull
              ?.searchIncludeNotes ??
          false;
      // Run search + feedback hydrate in parallel — feedback is a
      // small key-value query, no reason to serialise it behind the
      // RRF round trip. We swallow feedback failures: thumbs render
      // neutral on miss, which is the same as no-prior-vote.
      final results = await Future.wait<Object?>([
        client.search(
            query: query, scope: 'all', limit: 30, includeNotes: includeNotes),
        client.listFeedback(query: query).catchError((Object _) =>
            <String, String>{}),
      ]);
      if (!mounted || _activeQuery != query) return;
      final resp = results[0] as SearchResponse;
      final verdicts = results[1] as Map<String, String>;
      setState(() {
        _state = AsyncValue.data(resp);
        _feedbackByPage
          ..clear()
          ..addAll(verdicts);
      });
    } catch (e, st) {
      if (!mounted || _activeQuery != query) return;
      setState(() => _state = AsyncValue.error(e, st));
    }
  }

  /// Toggle the thumbs verdict for one page. Optimistic update:
  /// flip local state immediately, then fire the RPC in the
  /// background; on failure roll back + snackbar. Three transitions
  /// possible:
  ///   neutral → up      : POST {signal:"up"}
  ///   up      → neutral : DELETE
  ///   up      → down    : POST {signal:"down"}  (server upsert flips)
  Future<void> _toggleThumb({
    required String pageId,
    required String signal,
    required int rank,
    required String source,
  }) async {
    if (_feedbackInFlight.contains(pageId)) return;
    final client = ref.read(searchClientProvider);
    if (client == null) return;
    final query = _activeQuery;
    if (query.isEmpty) return;

    final prev = _feedbackByPage[pageId];
    final next = prev == signal ? null : signal;

    setState(() {
      if (next == null) {
        _feedbackByPage.remove(pageId);
      } else {
        _feedbackByPage[pageId] = next;
      }
      _feedbackInFlight.add(pageId);
    });

    try {
      if (next == null) {
        await client.clearFeedback(query: query, pageId: pageId);
      } else {
        await client.submitFeedback(
          query: query,
          pageId: pageId,
          signal: next,
          rank: rank,
          source: source,
        );
      }
    } catch (e) {
      if (!mounted) return;
      // Roll back optimistic update.
      setState(() {
        if (prev == null) {
          _feedbackByPage.remove(pageId);
        } else {
          _feedbackByPage[pageId] = prev;
        }
      });
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('反馈失败：$e')),
      );
    } finally {
      if (mounted) {
        setState(() => _feedbackInFlight.remove(pageId));
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    return PageScaffold(
      title: '搜索',
      subtitle: 'BM25 + 向量 + 图谱 + 网页 四路融合',
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          _SearchBox(
            controller: _controller,
            focus: _focus,
            onChanged: _onChanged,
            onSubmit: () {
              if (_composing) return;
              _runSearch(_controller.text.trim());
            },
            onCompositionChanged: (composing) {
              _composing = composing;
            },
            onClear: () {
              _controller.clear();
              _runSearch('');
              _focus.requestFocus();
            },
          ),
          const SizedBox(height: BiuTokens.space4),
          Expanded(
            child: _state.when(
              loading: () =>
                  const Center(child: CircularProgressIndicator()),
              error: (e, _) => _ErrorView(error: e),
              data: (resp) {
                if (_activeQuery.isEmpty) {
                  return const _InitialView();
                }
                if (resp == null ||
                    resp.fused.isEmpty &&
                        resp.images.isEmpty &&
                        resp.notes.isEmpty) {
                  return _NoResultsView(query: _activeQuery);
                }
                return _ResultsView(
                  query: _activeQuery,
                  resp: resp,
                  showSupportingImages: _showSupportingImages,
                  onToggleSupporting: () => setState(() {
                    _showSupportingImages = !_showSupportingImages;
                  }),
                  feedbackByPage: _feedbackByPage,
                  feedbackInFlight: _feedbackInFlight,
                  onToggleThumb: _toggleThumb,
                );
              },
            ),
          ),
        ],
      ),
    );
  }
}

class _NoCredentialsError implements Exception {
  const _NoCredentialsError();
  @override
  String toString() => '请先在「设置」中登录 BiuMind 账号。';
}

// ── search box ────────────────────────────────────────────────

class _SearchBox extends StatelessWidget {
  const _SearchBox({
    required this.controller,
    required this.focus,
    required this.onChanged,
    required this.onSubmit,
    required this.onCompositionChanged,
    required this.onClear,
  });

  final TextEditingController controller;
  final FocusNode focus;
  final ValueChanged<String> onChanged;
  final VoidCallback onSubmit;
  final ValueChanged<bool> onCompositionChanged;
  final VoidCallback onClear;

  @override
  Widget build(BuildContext context) {
    return Container(
      decoration: BoxDecoration(
        color: BiuTokens.surface,
        borderRadius: BorderRadius.circular(BiuTokens.radiusMd),
        border: Border.all(color: BiuTokens.borderSubtle),
      ),
      padding: const EdgeInsets.symmetric(
          horizontal: BiuTokens.space3, vertical: 4),
      child: Row(
        children: [
          Icon(Icons.search, size: 16, color: BiuTokens.textMuted),
          const SizedBox(width: BiuTokens.space2),
          Expanded(
            child: _ImeAwareTextField(
              controller: controller,
              focus: focus,
              onChanged: onChanged,
              onSubmit: onSubmit,
              onCompositionChanged: onCompositionChanged,
            ),
          ),
          if (controller.text.isNotEmpty)
            IconButton(
              icon: const Icon(Icons.close, size: 16),
              tooltip: '清空',
              onPressed: onClear,
            ),
        ],
      ),
    );
  }
}

/// TextField wrapper that surfaces IME composition state via
/// `onCompositionChanged`. Flutter's TextField composes inside a
/// platform plugin; we read `value.composing.isValid` on every change
/// to know when the user is mid-IME (typing pinyin) vs committed.
class _ImeAwareTextField extends StatefulWidget {
  const _ImeAwareTextField({
    required this.controller,
    required this.focus,
    required this.onChanged,
    required this.onSubmit,
    required this.onCompositionChanged,
  });

  final TextEditingController controller;
  final FocusNode focus;
  final ValueChanged<String> onChanged;
  final VoidCallback onSubmit;
  final ValueChanged<bool> onCompositionChanged;

  @override
  State<_ImeAwareTextField> createState() => _ImeAwareTextFieldState();
}

class _ImeAwareTextFieldState extends State<_ImeAwareTextField> {
  bool _wasComposing = false;

  @override
  void initState() {
    super.initState();
    widget.controller.addListener(_onTextChange);
    // Auto-focus on page open so users can start typing immediately,
    // matching the llm_wiki / knowcode experience.
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (mounted) widget.focus.requestFocus();
    });
  }

  @override
  void dispose() {
    widget.controller.removeListener(_onTextChange);
    super.dispose();
  }

  void _onTextChange() {
    final composing = widget.controller.value.composing.isValid;
    if (composing != _wasComposing) {
      _wasComposing = composing;
      widget.onCompositionChanged(composing);
    }
  }

  @override
  Widget build(BuildContext context) {
    return Shortcuts(
      // Enter triggers search ONLY when not composing. The shortcut
      // map is preferred over onSubmitted because onSubmitted fires
      // even mid-composition on some platforms.
      shortcuts: const <ShortcutActivator, Intent>{
        SingleActivator(LogicalKeyboardKey.enter): _SubmitIntent(),
      },
      child: Actions(
        actions: {
          _SubmitIntent: CallbackAction<_SubmitIntent>(
            onInvoke: (_) {
              widget.onSubmit();
              return null;
            },
          ),
        },
        child: TextField(
          controller: widget.controller,
          focusNode: widget.focus,
          onChanged: widget.onChanged,
          decoration: const InputDecoration(
            hintText: '搜索 BiuMind 知识库与网络…',
            border: InputBorder.none,
            isCollapsed: true,
            contentPadding: EdgeInsets.symmetric(vertical: 12),
          ),
          style: const TextStyle(fontSize: 14),
        ),
      ),
    );
  }
}

class _SubmitIntent extends Intent {
  const _SubmitIntent();
}

// ── empty / error / loading states ────────────────────────────

class _InitialView extends StatelessWidget {
  const _InitialView();

  @override
  Widget build(BuildContext context) {
    return Center(
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          Icon(Icons.search, size: 40, color: BiuTokens.textMuted),
          SizedBox(height: BiuTokens.space3),
          Text('输入关键词搜索 / Enter 立即查',
              style: TextStyle(color: BiuTokens.textMuted, fontSize: 12)),
        ],
      ),
    );
  }
}

class _NoResultsView extends StatelessWidget {
  const _NoResultsView({required this.query});
  final String query;

  @override
  Widget build(BuildContext context) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(BiuTokens.space5),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(Icons.search_off,
                size: 40, color: BiuTokens.textMuted),
            const SizedBox(height: BiuTokens.space3),
            Text('未找到 "$query" 的匹配',
                style:
                    TextStyle(color: BiuTokens.textMuted, fontSize: 12)),
          ],
        ),
      ),
    );
  }
}

class _ErrorView extends StatelessWidget {
  const _ErrorView({required this.error});
  final Object error;

  @override
  Widget build(BuildContext context) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(BiuTokens.space5),
        child: Text(
          error.toString(),
          textAlign: TextAlign.center,
          style: TextStyle(color: BiuTokens.textMuted, fontSize: 12),
        ),
      ),
    );
  }
}

// ── results layout ────────────────────────────────────────────

class _ResultsView extends StatelessWidget {
  const _ResultsView({
    required this.query,
    required this.resp,
    required this.showSupportingImages,
    required this.onToggleSupporting,
    required this.feedbackByPage,
    required this.feedbackInFlight,
    required this.onToggleThumb,
  });

  final String query;
  final SearchResponse resp;
  final bool showSupportingImages;
  final VoidCallback onToggleSupporting;
  final Map<String, String> feedbackByPage;
  final Set<String> feedbackInFlight;
  final Future<void> Function({
    required String pageId,
    required String signal,
    required int rank,
    required String source,
  }) onToggleThumb;

  @override
  Widget build(BuildContext context) {
    final matchingImages =
        resp.images.where((i) => i.altMatchesQuery).toList();
    final supportingImages =
        resp.images.where((i) => !i.altMatchesQuery).toList();
    final visibleImages =
        showSupportingImages ? resp.images : matchingImages;

    // include_notes=true 时 fused 里会混入 kind='note' 的条目 —— 笔记在
    // 下面独立「笔记」分组渲染（点击跳笔记编辑器），页面分组里剔掉，
    // 避免重复且 note 条目没有 page_id 点了没反应。
    final pages =
        resp.fused.where((h) => h.kind != 'note').toList(growable: false);

    final tokens = _tokenizeQuery(query);

    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        _StatsLine(
          fusedCount: pages.length,
          matchingImages: matchingImages.length,
          supportingImages: supportingImages.length,
          noteCount: resp.notes.length,
        ),
        if (visibleImages.isNotEmpty) ...[
          _SectionHeader(
            label: '图片',
            count: visibleImages.length,
            trailing: supportingImages.isNotEmpty
                ? TextButton(
                    onPressed: onToggleSupporting,
                    child: Text(
                      showSupportingImages
                          ? '隐藏辅助图'
                          : '显示全部 (+${supportingImages.length})',
                      style: const TextStyle(fontSize: 11),
                    ),
                  )
                : null,
          ),
          ConstrainedBox(
            // Cap at ~2 rows worth of cards (matches llm_wiki layout).
            // Anything beyond scrolls within this region; the Pages
            // list below keeps its own scroll independent.
            constraints: const BoxConstraints(maxHeight: 360),
            child: _ImageGrid(images: visibleImages, query: query),
          ),
          const SizedBox(height: BiuTokens.space3),
        ],
        if (resp.notes.isNotEmpty) ...[
          _SectionHeader(label: '笔记', count: resp.notes.length),
          ConstrainedBox(
            // 笔记分组最多占 ~3 张卡高度，超出内部滚动；页面列表保持主滚动。
            constraints: const BoxConstraints(maxHeight: 264),
            child: ListView.separated(
              shrinkWrap: true,
              itemCount: resp.notes.length,
              separatorBuilder: (_, _) =>
                  const SizedBox(height: BiuTokens.space2),
              itemBuilder: (_, i) => SearchNoteCard(
                hit: resp.notes[i],
                tokens: tokens,
              ),
            ),
          ),
          const SizedBox(height: BiuTokens.space3),
        ],
        _SectionHeader(label: '页面', count: pages.length),
        Expanded(
          child: ListView.separated(
            itemCount: pages.length,
            separatorBuilder: (_, _) => const SizedBox(height: BiuTokens.space2),
            itemBuilder: (_, i) {
              final hit = pages[i];
              final pid = hit.pageId;
              return _ResultCard(
                hit: hit,
                tokens: tokens,
                rank: i,
                currentSignal:
                    pid.isEmpty ? null : feedbackByPage[pid],
                disabled: pid.isEmpty || feedbackInFlight.contains(pid),
                onThumb: pid.isEmpty
                    ? null
                    : (signal) => onToggleThumb(
                          pageId: pid,
                          signal: signal,
                          rank: i,
                          source: hit.source,
                        ),
              );
            },
          ),
        ),
      ],
    );
  }
}

class _StatsLine extends StatelessWidget {
  const _StatsLine({
    required this.fusedCount,
    required this.matchingImages,
    required this.supportingImages,
    this.noteCount = 0,
  });

  final int fusedCount;
  final int matchingImages;
  final int supportingImages;
  final int noteCount;

  @override
  Widget build(BuildContext context) {
    final parts = <String>[];
    parts.add('$fusedCount 页');
    if (noteCount > 0) {
      parts.add('$noteCount 笔记');
    }
    if (matchingImages > 0) {
      parts.add('$matchingImages 图匹配');
    }
    if (supportingImages > 0) {
      parts.add('+$supportingImages 来自命中页');
    }
    return Padding(
      padding: const EdgeInsets.only(bottom: BiuTokens.space2),
      child: Text(
        parts.join(' · '),
        style: TextStyle(fontSize: 11, color: BiuTokens.textMuted),
      ),
    );
  }
}

class _SectionHeader extends StatelessWidget {
  const _SectionHeader({
    required this.label,
    required this.count,
    this.trailing,
  });

  final String label;
  final int count;
  final Widget? trailing;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.only(bottom: BiuTokens.space2),
      child: Row(
        children: [
          Text(
            label,
            style: TextStyle(
              fontSize: 11,
              fontWeight: FontWeight.w600,
              color: BiuTokens.textMuted,
              letterSpacing: 0.4,
            ),
          ),
          const SizedBox(width: 6),
          Text('($count)',
              style: TextStyle(
                  fontSize: 11, color: BiuTokens.textMuted)),
          const Spacer(),
          ?trailing,
        ],
      ),
    );
  }
}

// ── image grid + lightbox ─────────────────────────────────────

class _ImageGrid extends StatelessWidget {
  const _ImageGrid({required this.images, required this.query});
  final List<SearchImageHit> images;
  final String query;

  @override
  Widget build(BuildContext context) {
    return GridView.builder(
      gridDelegate: const SliverGridDelegateWithMaxCrossAxisExtent(
        maxCrossAxisExtent: 200,
        mainAxisSpacing: 8,
        crossAxisSpacing: 8,
        childAspectRatio: 1.1,
      ),
      itemCount: images.length,
      itemBuilder: (_, i) => _ImageCard(image: images[i], query: query),
    );
  }
}

class _ImageCard extends StatelessWidget {
  const _ImageCard({required this.image, required this.query});
  final SearchImageHit image;
  final String query;

  @override
  Widget build(BuildContext context) {
    return InkWell(
      onTap: () => showDialog<void>(
        context: context,
        barrierDismissible: true,
        builder: (_) => _Lightbox(image: image),
      ),
      child: Container(
        decoration: BoxDecoration(
          border: Border.all(color: BiuTokens.borderSubtle),
          borderRadius: BorderRadius.circular(BiuTokens.radiusMd),
          color: BiuTokens.surface,
        ),
        clipBehavior: Clip.antiAlias,
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Expanded(
              flex: 5,
              child: Container(
                color: BiuTokens.surfaceMuted,
                child: Image.network(
                  image.url,
                  fit: BoxFit.cover,
                  errorBuilder: (_, _, _) => Center(
                    child: Icon(Icons.broken_image,
                        size: 24, color: BiuTokens.textMuted),
                  ),
                  loadingBuilder: (_, child, progress) =>
                      progress == null ? child : const Center(
                        child: SizedBox(
                          width: 16, height: 16,
                          child: CircularProgressIndicator(strokeWidth: 2),
                        ),
                      ),
                ),
              ),
            ),
            Expanded(
              flex: 2,
              child: Padding(
                padding: const EdgeInsets.all(6),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    Expanded(
                      child: image.alt.isEmpty
                          ? Text('(无 alt)',
                              style: TextStyle(
                                fontSize: 10,
                                fontStyle: FontStyle.italic,
                                color: BiuTokens.textMuted,
                              ))
                          : _HighlightedText(
                              text: image.alt,
                              query: query,
                              maxLines: 2,
                              style: const TextStyle(fontSize: 11, height: 1.3),
                            ),
                    ),
                    Text(
                      image.pageTitle,
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                      style: TextStyle(
                          fontSize: 9, color: BiuTokens.textMuted),
                    ),
                  ],
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _Lightbox extends StatelessWidget {
  const _Lightbox({required this.image});
  final SearchImageHit image;

  @override
  Widget build(BuildContext context) {
    return Dialog(
      backgroundColor: Colors.transparent,
      insetPadding: const EdgeInsets.all(24),
      child: Container(
        decoration: BoxDecoration(
          color: BiuTokens.surface,
          borderRadius: BorderRadius.circular(BiuTokens.radiusLg),
        ),
        clipBehavior: Clip.antiAlias,
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Padding(
              padding: const EdgeInsets.all(BiuTokens.space3),
              child: Row(
                children: [
                  Expanded(
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Text(
                          image.alt.isEmpty ? '(无 alt)' : image.alt,
                          style: const TextStyle(
                              fontSize: 13, fontWeight: FontWeight.w500),
                        ),
                        const SizedBox(height: 2),
                        Text(
                          '来自：${image.pageTitle}',
                          style: TextStyle(
                              fontSize: 11, color: BiuTokens.textMuted),
                        ),
                      ],
                    ),
                  ),
                  IconButton(
                    icon: const Icon(Icons.close, size: 18),
                    onPressed: () => Navigator.of(context).pop(),
                  ),
                ],
              ),
            ),
            ConstrainedBox(
              constraints: BoxConstraints(
                maxHeight: MediaQuery.of(context).size.height * 0.7,
                maxWidth: 800,
              ),
              child: Image.network(
                image.url,
                fit: BoxFit.contain,
                errorBuilder: (_, _, _) => Padding(
                  padding: EdgeInsets.all(BiuTokens.space5),
                  child: Icon(Icons.broken_image,
                      size: 48, color: BiuTokens.textMuted),
                ),
              ),
            ),
            Padding(
              padding: const EdgeInsets.all(BiuTokens.space3),
              child: Row(
                mainAxisAlignment: MainAxisAlignment.end,
                children: [
                  FilledButton.icon(
                    onPressed: () {
                      Navigator.of(context).pop();
                      context.go('/wiki?pageId=${image.pageId}');
                    },
                    icon: const Icon(Icons.north_east, size: 14),
                    label: const Text('跳到来源页面'),
                  ),
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }
}

// ── note result card (include_notes 分组) ─────────────────────

/// 统一搜索「笔记」分组条目（N3）—— 标题 + snippet 高亮，点击跳笔记：
/// 选中 selectedNoteIdProvider 后 go('/notes')，三栏右栏直接打开编辑器。
/// public 以便 widget 测试直接挂载。
class SearchNoteCard extends ConsumerWidget {
  const SearchNoteCard({
    super.key,
    required this.hit,
    required this.tokens,
  });

  final SearchNoteHit hit;
  final List<String> tokens;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    return InkWell(
      onTap: () {
        ref.read(selectedNoteIdProvider.notifier).state = hit.id;
        context.go('/notes');
      },
      borderRadius: BorderRadius.circular(BiuTokens.radiusMd),
      child: Container(
        padding: const EdgeInsets.all(BiuTokens.space3),
        decoration: BoxDecoration(
          color: BiuTokens.surface,
          border: Border.all(color: BiuTokens.borderSubtle),
          borderRadius: BorderRadius.circular(BiuTokens.radiusMd),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                Icon(Icons.sticky_note_2_outlined,
                    size: 14, color: BiuTokens.textMuted),
                const SizedBox(width: BiuTokens.space2),
                Expanded(
                  child: _HighlightedText(
                    text: hit.title.isEmpty ? '(无标题笔记)' : hit.title,
                    query: '',
                    tokens: tokens,
                    maxLines: 1,
                    style: const TextStyle(
                      fontSize: 13,
                      fontWeight: FontWeight.w600,
                    ),
                  ),
                ),
              ],
            ),
            if (hit.snippet.isNotEmpty) ...[
              const SizedBox(height: BiuTokens.space2),
              _HighlightedText(
                text: hit.snippet,
                query: '',
                tokens: tokens,
                maxLines: 2,
                style: TextStyle(
                    fontSize: 11, color: BiuTokens.textSecondary, height: 1.4),
              ),
            ],
          ],
        ),
      ),
    );
  }
}

// ── result card + 4-path source badge ─────────────────────────

class _ResultCard extends StatelessWidget {
  const _ResultCard({
    required this.hit,
    required this.tokens,
    required this.rank,
    this.currentSignal,
    this.disabled = false,
    this.onThumb,
  });

  final SearchHit hit;
  final List<String> tokens;
  final int rank;
  final String? currentSignal; // "up" / "down" / null
  final bool disabled;
  final void Function(String signal)? onThumb;

  bool get _isWeb => hit.source == 'web';

  Future<void> _open(BuildContext context) async {
    if (_isWeb && hit.url.isNotEmpty) {
      final uri = Uri.tryParse(hit.url);
      if (uri == null) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('无效链接：${hit.url}')),
        );
        return;
      }
      final ok = await launchUrl(uri, mode: LaunchMode.externalApplication);
      if (!context.mounted) return;
      if (!ok) {
        // Fallback to clipboard for environments where the platform
        // browser intent isn't wired (CI / sandboxed builds).
        await Clipboard.setData(ClipboardData(text: hit.url));
        if (!context.mounted) return;
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('已复制链接：${hit.url}')),
        );
      }
      return;
    }
    if (hit.pageId.isNotEmpty) {
      context.go('/wiki?pageId=${hit.pageId}');
    }
  }

  @override
  Widget build(BuildContext context) {
    return InkWell(
      onTap: () => _open(context),
      borderRadius: BorderRadius.circular(BiuTokens.radiusMd),
      child: Container(
        padding: const EdgeInsets.all(BiuTokens.space3),
        decoration: BoxDecoration(
          color: BiuTokens.surface,
          border: Border.all(color: BiuTokens.borderSubtle),
          borderRadius: BorderRadius.circular(BiuTokens.radiusMd),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                _SourceBadge(source: hit.source),
                const SizedBox(width: BiuTokens.space2),
                Expanded(
                  child: _HighlightedText(
                    text: hit.title.isEmpty ? '(未命名)' : hit.title,
                    query: '',
                    tokens: tokens,
                    maxLines: 1,
                    style: const TextStyle(
                      fontSize: 13,
                      fontWeight: FontWeight.w600,
                    ),
                  ),
                ),
                Text(
                  hit.score.toStringAsFixed(3),
                  style: TextStyle(
                      fontSize: 10,
                      color: BiuTokens.textMuted,
                      fontFeatures: [FontFeature.tabularFigures()]),
                ),
                if (onThumb != null) ...[
                  const SizedBox(width: 4),
                  _ThumbButton(
                    icon: Icons.thumb_up_outlined,
                    iconActive: Icons.thumb_up,
                    activeColor: SearchSourceBadge.graphFg, // 复用绿色"相关"
                    active: currentSignal == 'up',
                    disabled: disabled,
                    tooltip: '相关',
                    onTap: () => onThumb!('up'),
                  ),
                  _ThumbButton(
                    icon: Icons.thumb_down_outlined,
                    iconActive: Icons.thumb_down,
                    activeColor: WikiReviewStatus.lintFg, // 复用红色"不相关"
                    active: currentSignal == 'down',
                    disabled: disabled,
                    tooltip: '不相关',
                    onTap: () => onThumb!('down'),
                  ),
                ],
              ],
            ),
            if (hit.snippet.isNotEmpty) ...[
              const SizedBox(height: BiuTokens.space2),
              _HighlightedText(
                text: hit.snippet,
                query: '',
                tokens: tokens,
                maxLines: 2,
                style: TextStyle(
                    fontSize: 11, color: BiuTokens.textSecondary, height: 1.4),
              ),
            ],
            if (hit.viaSeedPage.isNotEmpty) ...[
              const SizedBox(height: BiuTokens.space2),
              Text(
                'via ${hit.viaSeedPage.substring(0, 8)}…',
                style: TextStyle(
                    fontSize: 10,
                    color: BiuTokens.textMuted,
                    fontFamily: 'JetBrains Mono, ui-monospace, monospace'),
              ),
            ],
            if (_isWeb && hit.url.isNotEmpty) ...[
              const SizedBox(height: BiuTokens.space2),
              Text(
                hit.url,
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
                style: TextStyle(
                    fontSize: 10,
                    color: BiuTokens.textMuted,
                    fontFamily: 'JetBrains Mono, ui-monospace, monospace'),
              ),
            ],
          ],
        ),
      ),
    );
  }
}

/// Compact thumbs button — outlined when neutral, filled tinted when
/// active. Hit target is 24px tap (small but reachable on touch),
/// icon 13px so it stays balanced against the title font weight.
/// While `disabled` is true the icon greys out and taps are ignored
/// — used during the in-flight RPC window to block double-clicks.
class _ThumbButton extends StatelessWidget {
  const _ThumbButton({
    required this.icon,
    required this.iconActive,
    required this.activeColor,
    required this.active,
    required this.disabled,
    required this.tooltip,
    required this.onTap,
  });

  final IconData icon;
  final IconData iconActive;
  final Color activeColor;
  final bool active;
  final bool disabled;
  final String tooltip;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final color = disabled
        ? BiuTokens.textDisabled
        : (active ? activeColor : BiuTokens.textMuted);
    return Tooltip(
      message: tooltip,
      child: InkResponse(
        onTap: disabled ? null : onTap,
        radius: 14,
        child: Padding(
          padding: const EdgeInsets.all(4),
          child: Icon(
            active ? iconActive : icon,
            size: 13,
            color: color,
          ),
        ),
      ),
    );
  }
}

class _SourceBadge extends StatelessWidget {
  const _SourceBadge({required this.source});
  final String source;

  ({Color bg, Color fg, String label}) _palette() => switch (source) {
        'wiki'   => (bg: SearchSourceBadge.wikiBg,   fg: SearchSourceBadge.wikiFg,   label: 'BM25'),
        'vector' => (bg: SearchSourceBadge.vectorBg, fg: SearchSourceBadge.vectorFg, label: 'VEC'),
        'graph'  => (bg: SearchSourceBadge.graphBg,  fg: SearchSourceBadge.graphFg,  label: 'GRAPH'),
        'web'    => (bg: SearchSourceBadge.webBg,    fg: SearchSourceBadge.webFg,    label: 'WEB'),
        _        => (bg: SearchSourceBadge.otherBg,  fg: SearchSourceBadge.otherFg,
                     label: source.isEmpty ? '?' : source.toUpperCase()),
      };

  @override
  Widget build(BuildContext context) {
    final p = _palette();
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
      decoration: BoxDecoration(
        color: p.bg,
        borderRadius: BorderRadius.circular(4),
      ),
      child: Text(
        p.label,
        style: TextStyle(
          fontSize: 9,
          fontWeight: FontWeight.w700,
          color: p.fg,
          letterSpacing: 0.4,
        ),
      ),
    );
  }
}

// ── highlighted text + CJK-aware tokenizer ────────────────────

class _HighlightedText extends StatelessWidget {
  const _HighlightedText({
    required this.text,
    required this.query,
    this.tokens,
    this.maxLines,
    this.style,
  });

  final String text;
  final String query;
  final List<String>? tokens;
  final int? maxLines;
  final TextStyle? style;

  List<String> _resolveTokens() => tokens ?? _tokenizeQuery(query);

  @override
  Widget build(BuildContext context) {
    final toks = _resolveTokens();
    if (toks.isEmpty) {
      return Text(text,
          style: style,
          maxLines: maxLines,
          overflow:
              maxLines == null ? null : TextOverflow.ellipsis);
    }
    final spans = _buildSpans(text, toks, style);
    return Text.rich(
      TextSpan(children: spans, style: style),
      maxLines: maxLines,
      overflow: maxLines == null ? null : TextOverflow.ellipsis,
    );
  }

  static List<InlineSpan> _buildSpans(
      String text, List<String> tokens, TextStyle? base) {
    // Greedy left-to-right scan: at each position try to match any of
    // the tokens (case-insensitive, longest-first to avoid prefix
    // shadowing), emit a highlighted span if matched, else step one
    // rune. Tokenizer hands us atoms (CJK 1-char, ASCII whole-word)
    // so the longest-first sort just stabilises ordering.
    final lower = text.toLowerCase();
    final sorted = [...tokens]..sort((a, b) => b.length.compareTo(a.length));

    final spans = <InlineSpan>[];
    var i = 0;
    var plainStart = 0;
    while (i < text.length) {
      String? hit;
      for (final t in sorted) {
        if (t.isEmpty) continue;
        if (lower.startsWith(t, i)) {
          hit = t;
          break;
        }
      }
      if (hit != null) {
        if (i > plainStart) {
          spans.add(TextSpan(text: text.substring(plainStart, i)));
        }
        spans.add(TextSpan(
          text: text.substring(i, i + hit.length),
          style: const TextStyle(
            backgroundColor: SearchSourceBadge.highlightBg,
            fontWeight: FontWeight.w600,
          ),
        ));
        i += hit.length;
        plainStart = i;
      } else {
        // Step one rune (codepoint), not one byte — Dart strings are
        // UTF-16, so a single CJK char advances by 1 codeUnit.
        i += 1;
      }
    }
    if (plainStart < text.length) {
      spans.add(TextSpan(text: text.substring(plainStart)));
    }
    return spans;
  }
}

/// Mirror the server-side tokenizer (services/brain/internal/search/api/api.go
/// tokenizeQuery): ASCII split on whitespace+punctuation, CJK chars
/// as individual unigrams. Lowercased. Drives both alt-match (server)
/// and highlighting (here).
List<String> _tokenizeQuery(String q) {
  final s = q.trim().toLowerCase();
  if (s.isEmpty) return const [];
  final tokens = <String>[];
  final buf = StringBuffer();
  void flush() {
    if (buf.isNotEmpty) {
      tokens.add(buf.toString());
      buf.clear();
    }
  }
  for (final r in s.runes) {
    if (_isCJK(r)) {
      flush();
      tokens.add(String.fromCharCode(r));
    } else if (_isPunctOrSpace(r)) {
      flush();
    } else {
      buf.writeCharCode(r);
    }
  }
  flush();
  return tokens;
}

bool _isCJK(int r) {
  return (r >= 0x4E00 && r <= 0x9FFF) ||
      (r >= 0x3400 && r <= 0x4DBF) ||
      (r >= 0x3040 && r <= 0x30FF) ||
      (r >= 0xAC00 && r <= 0xD7AF);
}

bool _isPunctOrSpace(int r) {
  // Whitespace runs.
  if (r == 0x20 || r == 0x09 || r == 0x0A || r == 0x0D) return true;
  // Latin punctuation.
  if ((r >= 0x21 && r <= 0x2F) ||
      (r >= 0x3A && r <= 0x40) ||
      (r >= 0x5B && r <= 0x60) ||
      (r >= 0x7B && r <= 0x7E)) {
    return true;
  }
  // CJK punctuation block.
  if (r >= 0x3000 && r <= 0x303F) return true;
  // Fullwidth punctuation forms (e.g. ，。!?:;).
  if (r >= 0xFF00 && r <= 0xFF65) return true;
  return false;
}

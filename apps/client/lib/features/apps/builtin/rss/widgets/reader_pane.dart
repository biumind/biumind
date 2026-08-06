// Right pane of the inbox tab — renders the selected entry's
// full body. The current backend payload only includes a `snippet`
// (280-char text strip), not `content_html`, so we render the snippet
// as a fallback and surface a clear "在浏览器打开" button so users can
// always reach the full article. We still try `content_html` first
// in case backend grows the field.

import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_widget_from_html_core/flutter_widget_from_html_core.dart';
import 'package:just_audio/just_audio.dart';
import 'package:url_launcher/url_launcher.dart';

import 'entries_pane.dart' show kRssNarrowWidth;
import 'latex_html.dart';

import '../../../../../app/theme.dart';
import '../models.dart';
import '../providers.dart';
import '../rss_tokens.dart';

class ReaderPane extends ConsumerWidget {
  const ReaderPane({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final selection = ref.watch(rssSelectionProvider);
    final entryId = selection.selectedEntryId;
    if (entryId == null) {
      return const _EmptyReader();
    }
    // Resolve the entry from whichever entries provider currently caches
    // it. When the user clicks an entry the (feed_id) selection is
    // already set, so the entriesProvider for that query has it.
    final entries =
        ref.watch(entriesProvider(EntriesQuery(feedId: selection.selectedFeedId)));
    return entries.when(
      loading: () => const Center(child: CircularProgressIndicator()),
      error: (e, _) =>
          Center(child: SelectableText('$e', style: const TextStyle(fontSize: 13))),
      data: (list) {
        int idx = -1;
        for (var i = 0; i < list.length; i++) {
          if (list[i].id == entryId) {
            idx = i;
            break;
          }
        }
        if (idx < 0) return const _EmptyReader();
        // M12.1.3 — neighbours for swipe navigation (null at the ends).
        final prevId = idx > 0 ? list[idx - 1].id : null;
        final nextId = idx < list.length - 1 ? list[idx + 1].id : null;
        return _Reader(entry: list[idx], prevId: prevId, nextId: nextId);
      },
    );
  }
}

class _Reader extends ConsumerStatefulWidget {
  const _Reader({required this.entry, this.prevId, this.nextId});
  final Entry entry;
  // M12.1.3 — adjacent entry ids for swipe navigation (null at list ends).
  final String? prevId;
  final String? nextId;

  @override
  ConsumerState<_Reader> createState() => _ReaderState();
}

class _ReaderState extends ConsumerState<_Reader> {
  final _scroll = ScrollController();
  // M10.4 阅读进度 0..1. NotificationListener 更新, 顶部细条渲染.
  double _progress = 0;

  // T10.4.3 — 跨设备续读.
  Timer? _saveDebounce; // 滚动停下后节流落库
  String? _restoredFor; // 已对哪个 entry 应用过续读(避免重复 seek)
  bool _restoring = false; // 程序化 jumpTo 期间不触发保存
  RssActions? _actions; // initState 捕获,dispose 时无 ref 可用

  @override
  void initState() {
    super.initState();
    _scroll.addListener(_onScroll);
    _actions = ref.read(rssActionsProvider);
    _scheduleRestore(widget.entry.id);
  }

  @override
  void didUpdateWidget(_Reader old) {
    super.didUpdateWidget(old);
    if (old.entry.id != widget.entry.id) {
      // 切走前先把上一条的位置落库,再回到顶部 + 重置 + 安排新条目续读.
      _flushSave(old.entry.id, _progress);
      if (_scroll.hasClients) _scroll.jumpTo(0);
      setState(() => _progress = 0);
      _scheduleRestore(widget.entry.id);
    }
  }

  // 打开/切换到某条目后,等首帧布局完成,取保存的滚动比例并 seek.
  // 仅在中段(0.02..0.98)续读:近顶无意义,近底说明已读完。
  void _scheduleRestore(String entryId) {
    if (_restoredFor == entryId) return;
    final actions = _actions;
    if (actions == null) return;
    WidgetsBinding.instance.addPostFrameCallback((_) async {
      double pct;
      try {
        pct = await actions.entryProgressGet(entryId);
      } catch (_) {
        return; // 取不到就算了,不影响阅读
      }
      if (!mounted || widget.entry.id != entryId) return;
      _restoredFor = entryId;
      if (pct <= 0.02 || pct >= 0.98 || !_scroll.hasClients) return;
      final max = _scroll.position.maxScrollExtent;
      if (max <= 0) return;
      _restoring = true;
      _scroll.jumpTo(pct * max);
      _restoring = false;
      setState(() => _progress = pct);
    });
  }

  void _onScroll() {
    if (_restoring || !_scroll.hasClients) return;
    final max = _scroll.position.maxScrollExtent;
    final p = max <= 0 ? 0.0 : (_scroll.offset / max).clamp(0.0, 1.0);
    if ((p - _progress).abs() > 0.005) setState(() => _progress = p);
    // 停止滚动 800ms 后落库,避免高频写.
    _saveDebounce?.cancel();
    final entryId = widget.entry.id;
    _saveDebounce = Timer(const Duration(milliseconds: 800), () {
      _flushSave(entryId, _progress);
    });
  }

  // fire-and-forget 落库;吞掉错误(续读是优化,不能打断阅读).
  void _flushSave(String entryId, double pct) {
    _actions?.entryProgressSet(entryId, pct).catchError((_) {});
  }

  @override
  void dispose() {
    _saveDebounce?.cancel();
    _flushSave(widget.entry.id, _progress); // best-effort 收尾
    _scroll.removeListener(_onScroll);
    _scroll.dispose();
    super.dispose();
  }

  // M12.1.3 — navigate to an adjacent entry (selecting it also schedules the
  // standard mark-read via the entries pane on next view).
  void _goTo(String? id) {
    if (id == null) return;
    ref.read(rssSelectionProvider.notifier).selectEntry(id);
  }

  @override
  Widget build(BuildContext context) {
    final entry = widget.entry;
    final uri = safeParseUri(entry.url);
    final scheme = Theme.of(context).colorScheme;
    final narrow = MediaQuery.sizeOf(context).width < kRssNarrowWidth;
    final body = Container(
      color: RssReaderColors.bg(context), // M10.2 OLED 真黑
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          _ReaderHeader(entry: entry, uri: uri),
          // M10.4 阅读进度细条 (替代普通 divider).
          SizedBox(
            height: 2,
            child: LinearProgressIndicator(
              value: _progress,
              backgroundColor: BiuTokens.borderSubtle,
              valueColor: AlwaysStoppedAnimation(scheme.primary),
            ),
          ),
          Expanded(
            child: SingleChildScrollView(
              controller: _scroll,
              padding: const EdgeInsets.symmetric(
                  horizontal: BiuTokens.space6, vertical: BiuTokens.space5),
              child: Center(
                child: ConstrainedBox(
                  constraints: const BoxConstraints(maxWidth: 720),
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.stretch,
                    children: [
                      if (entry.aiTakeaway.isNotEmpty)
                        _AICard(entry: entry),
                      // M13.5 Tier2 — transcribed podcast with segments →
                      // synced player + tappable transcript (replaces the
                      // flat body, which is the same transcript text). Else
                      // fall back to the audio strip + normal body.
                      if (entry.enclosureUrl.isNotEmpty &&
                          entry.transcriptSegments.isNotEmpty)
                        _PodcastPlayer(
                            key: ValueKey('player-${entry.id}'), entry: entry)
                      else ...[
                        if (entry.enclosureUrl.isNotEmpty)
                          _PodcastStrip(entry: entry),
                        _ReaderBody(entry: entry, uri: uri),
                      ],
                    ],
                  ),
                ),
              ),
            ),
          ),
        ],
      ),
    );
    if (!narrow) return body;
    // M12.1.3 — phone: horizontal swipe switches entries. Horizontal (not the
    // DevPlan's literal vertical) deliberately avoids fighting the article's
    // vertical scroll. onHorizontalDragEnd doesn't claim vertical drags, so
    // scrolling still works. 左滑→下一条; 右滑→上一条.
    return GestureDetector(
      onHorizontalDragEnd: (d) {
        final v = d.primaryVelocity ?? 0;
        if (v < -250) {
          _goTo(widget.nextId);
        } else if (v > 250) {
          _goTo(widget.prevId);
        }
      },
      child: body,
    );
  }
}

/// M13.5 播客音频条 — 显示音频源 + 转写状态. 不内嵌播放器(避免引入新的音频
/// 依赖); 点「在浏览器中打开」走系统播放器. 转写完成后正文即为转写文字, 受
/// 既有 AI 摘要管线覆盖.
class _PodcastStrip extends StatelessWidget {
  const _PodcastStrip({required this.entry});
  final Entry entry;

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    final audioUri = safeParseUri(entry.enclosureUrl);
    final transcribeError =
        !entry.transcribed && entry.aiError.startsWith('quota');
    return Container(
      margin: const EdgeInsets.only(bottom: BiuTokens.space4),
      padding: const EdgeInsets.all(BiuTokens.space3),
      decoration: BoxDecoration(
        color: scheme.primary.withValues(alpha: 0.06),
        borderRadius: BorderRadius.circular(BiuTokens.radiusMd),
        border: Border.all(color: scheme.primary.withValues(alpha: 0.18)),
      ),
      child: Row(
        children: [
          Icon(Icons.podcasts_outlined, size: 18, color: scheme.primary),
          const SizedBox(width: BiuTokens.space2),
          Expanded(
            child: Text(
              entry.transcribed
                  ? '播客 · 已转写为文字'
                  : transcribeError
                      ? '播客 · 今日转写额度已用完'
                      : '播客 · 转写中…',
              style: TextStyle(fontSize: 13, color: BiuTokens.textSecondary),
            ),
          ),
          if (audioUri != null)
            TextButton.icon(
              onPressed: () =>
                  launchUrl(audioUri, mode: LaunchMode.externalApplication),
              icon: const Icon(Icons.play_circle_outline, size: 18),
              label: const Text('播放音频'),
            ),
        ],
      ),
    );
  }
}

/// M13.5 Tier2 — 播客转写联动播放器. 用已有的 just_audio(无新依赖):
/// 播放/暂停 + 进度条 + 倍速;转写按句渲染,点句跳播,播放时高亮当前句。
/// 这是把「已付费的转写」变成独家体验的护城河——通用播客 app 没有。
class _PodcastPlayer extends StatefulWidget {
  const _PodcastPlayer({super.key, required this.entry});
  final Entry entry;

  @override
  State<_PodcastPlayer> createState() => _PodcastPlayerState();
}

class _PodcastPlayerState extends State<_PodcastPlayer> {
  final AudioPlayer _player = AudioPlayer();
  StreamSubscription<Duration>? _posSub;
  static const _speeds = [1.0, 1.25, 1.5, 2.0];
  int _speedIdx = 0;
  int _currentSeg = -1;
  String? _error;

  List<TranscriptSegment> get _segs => widget.entry.transcriptSegments;

  @override
  void initState() {
    super.initState();
    _load();
    // Update the highlighted sentence only when the index actually changes
    // (not on every position tick) to avoid rebuilding the whole list.
    _posSub = _player.positionStream.listen((pos) {
      final idx = _segForPosition(pos.inMilliseconds / 1000.0);
      if (idx != _currentSeg && mounted) setState(() => _currentSeg = idx);
    });
  }

  Future<void> _load() async {
    try {
      await _player.setUrl(widget.entry.enclosureUrl);
    } catch (e) {
      if (mounted) setState(() => _error = '$e');
    }
  }

  int _segForPosition(double sec) {
    // Last segment whose start <= sec (segments are time-ordered).
    var idx = -1;
    for (var i = 0; i < _segs.length; i++) {
      if (_segs[i].start <= sec) {
        idx = i;
      } else {
        break;
      }
    }
    return idx;
  }

  @override
  void dispose() {
    _posSub?.cancel();
    _player.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        _controls(scheme),
        const SizedBox(height: BiuTokens.space4),
        if (_error != null)
          Padding(
            padding: const EdgeInsets.only(bottom: BiuTokens.space3),
            child: Text('音频加载失败: $_error',
                style: TextStyle(fontSize: 12, color: BiuTokens.error)),
          ),
        // Tappable, highlight-synced transcript.
        ..._segs.asMap().entries.map((e) {
          final i = e.key;
          final seg = e.value;
          final active = i == _currentSeg;
          return InkWell(
            onTap: () {
              _player.seek(Duration(milliseconds: (seg.start * 1000).round()));
              _player.play();
            },
            child: Container(
              width: double.infinity,
              padding: const EdgeInsets.symmetric(
                  vertical: 6, horizontal: BiuTokens.space2),
              decoration: BoxDecoration(
                color: active
                    ? scheme.primary.withValues(alpha: 0.10)
                    : Colors.transparent,
                borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
              ),
              child: Text(
                seg.text,
                style: TextStyle(
                  fontSize: 16,
                  height: 1.7,
                  color: active ? scheme.primary : BiuTokens.text,
                  fontWeight: active ? FontWeight.w600 : FontWeight.w400,
                ),
              ),
            ),
          );
        }),
      ],
    );
  }

  Widget _controls(ColorScheme scheme) {
    return Container(
      padding: const EdgeInsets.all(BiuTokens.space3),
      decoration: BoxDecoration(
        color: scheme.primary.withValues(alpha: 0.06),
        borderRadius: BorderRadius.circular(BiuTokens.radiusMd),
        border: Border.all(color: scheme.primary.withValues(alpha: 0.18)),
      ),
      child: Column(
        children: [
          Row(
            children: [
              StreamBuilder<PlayerState>(
                stream: _player.playerStateStream,
                builder: (_, snap) {
                  final playing = snap.data?.playing ?? false;
                  final done = snap.data?.processingState ==
                      ProcessingState.completed;
                  return IconButton(
                    iconSize: 36,
                    color: scheme.primary,
                    icon: Icon(playing && !done
                        ? Icons.pause_circle_filled
                        : Icons.play_circle_filled),
                    onPressed: () {
                      if (done) {
                        _player.seek(Duration.zero);
                        _player.play();
                      } else if (playing) {
                        _player.pause();
                      } else {
                        _player.play();
                      }
                    },
                  );
                },
              ),
              Expanded(child: _scrubber(scheme)),
              TextButton(
                onPressed: () {
                  setState(() => _speedIdx = (_speedIdx + 1) % _speeds.length);
                  _player.setSpeed(_speeds[_speedIdx]);
                },
                child: Text('${_speeds[_speedIdx]}×',
                    style: TextStyle(
                        fontSize: 13,
                        fontWeight: FontWeight.w600,
                        color: scheme.primary)),
              ),
            ],
          ),
        ],
      ),
    );
  }

  Widget _scrubber(ColorScheme scheme) {
    return StreamBuilder<Duration>(
      stream: _player.positionStream,
      builder: (_, posSnap) {
        final pos = posSnap.data ?? Duration.zero;
        final dur = _player.duration ?? Duration.zero;
        final maxMs = dur.inMilliseconds.toDouble();
        final value = maxMs <= 0
            ? 0.0
            : pos.inMilliseconds.clamp(0, dur.inMilliseconds).toDouble();
        return Column(
          children: [
            SliderTheme(
              data: SliderTheme.of(context).copyWith(
                trackHeight: 2,
                thumbShape:
                    const RoundSliderThumbShape(enabledThumbRadius: 6),
                overlayShape:
                    const RoundSliderOverlayShape(overlayRadius: 12),
              ),
              child: Slider(
                value: value,
                max: maxMs <= 0 ? 1 : maxMs,
                activeColor: scheme.primary,
                onChanged: maxMs <= 0
                    ? null
                    : (v) =>
                        _player.seek(Duration(milliseconds: v.round())),
              ),
            ),
            Padding(
              padding:
                  const EdgeInsets.symmetric(horizontal: BiuTokens.space2),
              child: Row(
                mainAxisAlignment: MainAxisAlignment.spaceBetween,
                children: [
                  Text(_fmt(pos),
                      style: TextStyle(
                          fontSize: 11, color: BiuTokens.textMuted)),
                  Text(_fmt(dur),
                      style: TextStyle(
                          fontSize: 11, color: BiuTokens.textMuted)),
                ],
              ),
            ),
          ],
        );
      },
    );
  }

  String _fmt(Duration d) {
    final m = d.inMinutes;
    final s = d.inSeconds % 60;
    return '$m:${s.toString().padLeft(2, '0')}';
  }
}

/// AI 摘要卡 — reader 顶部紫调圆角块. takeaway 大字 + 3 bullets +
/// topics chips + "重述"按钮.
class _AICard extends ConsumerWidget {
  const _AICard({required this.entry});
  final Entry entry;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final scheme = Theme.of(context).colorScheme;
    final tint = scheme.primary.withValues(alpha: 0.06);
    return Container(
      margin: const EdgeInsets.only(bottom: BiuTokens.space5),
      padding: const EdgeInsets.all(BiuTokens.space4),
      decoration: BoxDecoration(
        color: tint,
        borderRadius: BorderRadius.circular(BiuTokens.radiusLg),
        border: Border.all(color: scheme.primary.withValues(alpha: 0.18)),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Icon(Icons.auto_awesome, size: 14, color: scheme.primary),
              const SizedBox(width: 6),
              Text('AI 摘要',
                  style: TextStyle(
                      fontSize: 11,
                      fontWeight: FontWeight.w600,
                      color: scheme.primary,
                      letterSpacing: 0.5)),
              const Spacer(),
              if (entry.readingSeconds > 0)
                Text(_readingTime(entry.readingSeconds),
                    style: TextStyle(
                        fontSize: 11, color: BiuTokens.textMuted)),
            ],
          ),
          const SizedBox(height: BiuTokens.space2),
          Text(
            entry.aiTakeaway,
            style: TextStyle(
              fontSize: 15,
              height: 1.45,
              fontWeight: FontWeight.w600,
              color: scheme.onSurface,
            ),
          ),
          if (entry.aiBullets.isNotEmpty) ...[
            const SizedBox(height: BiuTokens.space3),
            ...entry.aiBullets.map((b) => Padding(
                  padding: const EdgeInsets.only(top: 4),
                  child: Row(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Padding(
                        padding: const EdgeInsets.only(top: 8, right: 8),
                        child: Container(
                          width: 4,
                          height: 4,
                          decoration: BoxDecoration(
                            color: scheme.primary.withValues(alpha: 0.6),
                            shape: BoxShape.circle,
                          ),
                        ),
                      ),
                      Expanded(
                        child: Text(
                          b,
                          style: TextStyle(
                            fontSize: 13,
                            height: 1.5,
                            color: scheme.onSurface.withValues(alpha: 0.85),
                          ),
                        ),
                      ),
                    ],
                  ),
                )),
          ],
          if (entry.aiTopics.isNotEmpty) ...[
            const SizedBox(height: BiuTokens.space3),
            Wrap(
              spacing: 6,
              runSpacing: 4,
              children: entry.aiTopics.map((t) => Container(
                    padding: const EdgeInsets.symmetric(
                        horizontal: 8, vertical: 2),
                    decoration: BoxDecoration(
                      color: scheme.primary.withValues(alpha: 0.12),
                      borderRadius:
                          BorderRadius.circular(BiuTokens.radiusXs),
                    ),
                    child: Text(t,
                        style: TextStyle(
                            fontSize: 10, color: scheme.primary)),
                  )).toList(),
            ),
          ],
          const SizedBox(height: BiuTokens.space3),
          Row(
            children: [
              FilledButton.tonalIcon(
                onPressed: () => _showRephraseSheet(context, ref, entry),
                icon: const Icon(Icons.translate, size: 14),
                label: const Text('换种说法'),
                style: FilledButton.styleFrom(
                  visualDensity: VisualDensity.compact,
                  textStyle: const TextStyle(fontSize: 12),
                ),
              ),
            ],
          ),
        ],
      ),
    );
  }

  String _readingTime(int seconds) {
    final m = (seconds + 30) ~/ 60;
    return m <= 1 ? '约 1 分钟' : '约 $m 分钟';
  }
}

Future<void> _sinkToWiki(BuildContext context, WidgetRef ref, Entry entry) async {
  final actions = ref.read(rssActionsProvider);
  if (actions == null) return;
  ScaffoldMessenger.of(context).showSnackBar(
    const SnackBar(content: Text('正在沉到 Wiki…'), duration: Duration(seconds: 2)),
  );
  try {
    final res = await actions.entriesToWiki(entry.id);
    if (!context.mounted) return;
    final tagText = res.suggestedTags.isEmpty
        ? ''
        : ' · 标签: ${res.suggestedTags.join("/")}';
    ScaffoldMessenger.of(context).showSnackBar(SnackBar(
      content: Text('已沉到 Wiki$tagText'),
      action: SnackBarAction(
        label: '查看',
        onPressed: () {
          // brain wiki 客户端路由 — 项目 + 页面 id
          if (res.pageId.isNotEmpty && res.projectId.isNotEmpty) {
            // TODO: navigate to wiki page once intra-app router is in
          }
        },
      ),
    ));
  } catch (e) {
    if (!context.mounted) return;
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(content: Text('沉 Wiki 失败: $e')),
    );
  }
}

void _showRephraseSheet(BuildContext context, WidgetRef ref, Entry entry) {
  showModalBottomSheet(
    context: context,
    showDragHandle: true,
    isScrollControlled: true,
    builder: (_) => _RephraseSheet(entry: entry),
  );
}

const _personaLabels = <String, ({String label, String desc, IconData icon})>{
  'child': (label: '5 岁小孩', desc: '比喻 + 生活化', icon: Icons.child_care),
  'layman': (label: '非技术读者', desc: '跳过细节, 强调影响', icon: Icons.person_outline),
  'boss': (label: '电梯演讲', desc: '30 秒讲完 + 2 论据', icon: Icons.business_center_outlined),
  'expert': (label: '同行专家', desc: '不解释基础概念', icon: Icons.school_outlined),
  'english': (label: '英文', desc: 'translate to English', icon: Icons.language),
};

class _RephraseSheet extends ConsumerStatefulWidget {
  const _RephraseSheet({required this.entry});
  final Entry entry;
  @override
  ConsumerState<_RephraseSheet> createState() => _RephraseSheetState();
}

class _RephraseSheetState extends ConsumerState<_RephraseSheet> {
  String? _selected;
  bool _busy = false;
  String? _result;
  String? _error;

  Future<void> _run(String persona) async {
    final api = ref.read(rssApiProvider);
    if (api == null) return;
    setState(() {
      _selected = persona;
      _busy = true;
      _result = null;
      _error = null;
    });
    try {
      final r = await api.invoke('entries_rephrase', {
        'id': widget.entry.id,
        'persona': persona,
      });
      final text = (r['result'] as Map?)?['rephrased'] as String? ??
          (r['rephrased'] as String?) ??
          '';
      if (!mounted) return;
      setState(() => _result = text.trim());
    } catch (e) {
      if (!mounted) return;
      setState(() => _error = '$e');
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    return SafeArea(
      child: Padding(
        padding: const EdgeInsets.fromLTRB(
            BiuTokens.space4, 0, BiuTokens.space4, BiuTokens.space4),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Row(
              children: [
                Icon(Icons.translate, size: 18, color: scheme.primary),
                const SizedBox(width: 8),
                Text('换种说法',
                    style: const TextStyle(
                        fontSize: 16, fontWeight: FontWeight.w600)),
              ],
            ),
            const SizedBox(height: BiuTokens.space3),
            Wrap(
              spacing: 8,
              runSpacing: 8,
              children: _personaLabels.entries.map((e) {
                final p = e.value;
                final selected = _selected == e.key;
                return ChoiceChip(
                  avatar: Icon(p.icon, size: 14),
                  label: Text(p.label),
                  selected: selected,
                  onSelected: _busy ? null : (_) => _run(e.key),
                );
              }).toList(),
            ),
            const SizedBox(height: BiuTokens.space4),
            ConstrainedBox(
              constraints: const BoxConstraints(minHeight: 100, maxHeight: 320),
              child: SingleChildScrollView(
                child: _busy
                    ? const Center(
                        child: Padding(
                          padding: EdgeInsets.all(24),
                          child: CircularProgressIndicator(strokeWidth: 2),
                        ),
                      )
                    : _error != null
                        ? Text('AI 调用失败: $_error',
                            style: TextStyle(
                                color: scheme.error, fontSize: 13))
                        : _result != null
                            ? SelectableText(
                                _result!,
                                style: const TextStyle(
                                    fontSize: 14, height: 1.6),
                              )
                            : Text(
                                '选一种风格让 AI 重述这篇文章',
                                style: TextStyle(
                                    color: BiuTokens.textMuted,
                                    fontSize: 13),
                              ),
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _ReaderHeader extends ConsumerWidget {
  const _ReaderHeader({required this.entry, required this.uri});
  final Entry entry;
  final Uri? uri;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final selection = ref.watch(rssSelectionProvider);
    final unread = selection.entryReadOverride[entry.id] ?? entry.unread;
    return Padding(
      padding: const EdgeInsets.fromLTRB(
        BiuTokens.space5,
        BiuTokens.space5,
        BiuTokens.space4,
        BiuTokens.space4,
      ),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  entry.title,
                  style: const TextStyle(
                    fontSize: 22,
                    fontWeight: FontWeight.w600,
                    height: 1.3,
                  ),
                ),
                const SizedBox(height: BiuTokens.space2),
                Text(
                  _subtitle(entry),
                  style: TextStyle(
                      fontSize: 12, color: BiuTokens.textMuted),
                ),
              ],
            ),
          ),
          const SizedBox(width: BiuTokens.space3),
          Wrap(
            spacing: BiuTokens.space2,
            children: [
              IconButton(
                tooltip: unread ? '标记为已读' : '标记为未读',
                icon: Icon(
                  unread ? Icons.mark_email_read_outlined : Icons.mark_email_unread_outlined,
                  size: 18,
                ),
                onPressed: () async {
                  final actions = ref.read(rssActionsProvider);
                  if (actions == null) return;
                  ref
                      .read(rssSelectionProvider.notifier)
                      .markEntryRead(entry.id, unread);
                  try {
                    await actions.entriesMarkRead(entry.id, unread);
                    ref.refreshEntries();
                    ref.refreshFeeds();
                  } catch (_) {
                    ref
                        .read(rssSelectionProvider.notifier)
                        .markEntryRead(entry.id, !unread);
                  }
                },
              ),
              IconButton(
                tooltip: entry.starred ? '取消收藏' : '收藏',
                icon: Icon(
                  entry.starred ? Icons.star : Icons.star_border,
                  size: 18,
                  color: entry.starred ? StarredColors.iconAlt : null,
                ),
                onPressed: () async {
                  final actions = ref.read(rssActionsProvider);
                  if (actions == null) return;
                  try {
                    await actions.entriesStar(entry.id, !entry.starred);
                    ref.refreshEntries();
                  } catch (e) {
                    if (!context.mounted) return;
                    ScaffoldMessenger.of(context).showSnackBar(
                        SnackBar(content: Text('操作失败: $e')));
                  }
                },
              ),
              IconButton(
                tooltip: '沉到 Wiki',
                icon: const Icon(Icons.bookmark_add_outlined, size: 18),
                onPressed: () => _sinkToWiki(context, ref, entry),
              ),
              if (uri != null)
                FilledButton.icon(
                  icon: const Icon(Icons.open_in_new, size: 16),
                  label: const Text('在浏览器打开'),
                  onPressed: () => launchUrl(uri!,
                      mode: LaunchMode.externalApplication),
                ),
            ],
          ),
        ],
      ),
    );
  }

  String _subtitle(Entry e) {
    final parts = <String>[];
    if (e.author.isNotEmpty) parts.add(e.author);
    // M10.4 阅读时间 + 字数 (v2 已有 word_count / reading_seconds 字段).
    if (e.readingSeconds > 0) {
      final m = (e.readingSeconds + 30) ~/ 60;
      parts.add(m <= 1 ? '≈ 1 分钟' : '≈ $m 分钟');
    }
    if (e.wordCount > 0) parts.add('${e.wordCount} 字');
    final full = fullDate(e.publishedAt ?? e.fetchedAt);
    if (full.isNotEmpty) parts.add(full);
    return parts.join(' · ');
  }
}

class _ReaderBody extends StatelessWidget {
  const _ReaderBody({required this.entry, required this.uri});
  final Entry entry;
  final Uri? uri;

  @override
  Widget build(BuildContext context) {
    final hasHtml = entry.contentHtml.trim().isNotEmpty;
    if (hasHtml) {
      // M10.3: 正文走 HtmlWidget. dark 模式用真黑配套的纯白正文;
      // code/pre 块给 monospace + 横向滚动避免长代码撑破布局.
      // LaTeX 块级公式($$..$$ / \[..\]): injectDisplayTex 先把它们换成
      // <x-tex>,再经 customWidgetBuilder 用纯 Dart 的 Math.tex 渲染(无 webview).
      // 行内 $..$ 与 mermaid 留后续 milestone.
      return HtmlWidget(
        injectDisplayTex(entry.contentHtml),
        onTapUrl: (url) async {
          final u = Uri.tryParse(url);
          if (u == null) return false;
          await launchUrl(u, mode: LaunchMode.externalApplication);
          return true;
        },
        textStyle: TextStyle(
          fontSize: 15,
          height: 1.7,
          color: RssReaderColors.text(context),
        ),
        customWidgetBuilder: (element) {
          if (element.localName == kTexTag) {
            return RssMathBlock(latex: decodeTex(element.text));
          }
          return null;
        },
        customStylesBuilder: (element) {
          if (element.localName == kTexTag) {
            return const {'display': 'block'}; // 强制块级,不挤进行内文本流
          }
          if (element.localName == 'pre' || element.localName == 'code') {
            return {
              'font-family': 'monospace',
              'font-size': '13px',
              'white-space': 'pre',
            };
          }
          return null;
        },
      );
    }
    final snippet = entry.snippet.trim();
    if (snippet.isEmpty) {
      return Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            '此文章暂无正文摘要。',
            style: TextStyle(fontSize: 14, color: BiuTokens.textSecondary),
          ),
          if (uri != null) ...[
            const SizedBox(height: BiuTokens.space3),
            FilledButton.icon(
              icon: const Icon(Icons.open_in_new, size: 16),
              label: const Text('打开原文'),
              onPressed: () =>
                  launchUrl(uri!, mode: LaunchMode.externalApplication),
            ),
          ],
        ],
      );
    }
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(
          snippet,
          style: const TextStyle(fontSize: 15, height: 1.75),
        ),
        const SizedBox(height: BiuTokens.space5),
        Container(
          padding: const EdgeInsets.all(BiuTokens.space3),
          decoration: BoxDecoration(
            color: BiuTokens.surfaceMuted,
            borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
            border: Border.all(color: BiuTokens.borderSubtle),
          ),
          child: Row(
            children: [
              Icon(Icons.info_outline,
                  size: 16, color: BiuTokens.textSecondary),
              const SizedBox(width: BiuTokens.space2),
              Expanded(
                child: Text(
                  '当前仅展示文章摘要。点击右上角“在浏览器打开”阅读完整内容。',
                  style:
                      TextStyle(fontSize: 12, color: BiuTokens.textSecondary),
                ),
              ),
            ],
          ),
        ),
      ],
    );
  }
}

class _EmptyReader extends StatelessWidget {
  const _EmptyReader();
  @override
  Widget build(BuildContext context) {
    return Container(
      color: BiuTokens.bg,
      child: Center(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(Icons.menu_book_outlined,
                size: 48, color: BiuTokens.textMuted),
            const SizedBox(height: BiuTokens.space3),
            Text('选择一篇文章',
                style: TextStyle(
                    fontSize: 14,
                    fontWeight: FontWeight.w500,
                    color: BiuTokens.textSecondary)),
            const SizedBox(height: BiuTokens.space1),
            Text('从中间列表点击任意文章开始阅读',
                style: TextStyle(fontSize: 12, color: BiuTokens.textMuted)),
          ],
        ),
      ),
    );
  }
}

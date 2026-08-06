// 数据统计页 (数据统计) — 账户级、跨设备。两视图 (概览 / 用量) 用 segmented
// 切换:
//   概览 ← brain GET /v1/chat/stats : 话题/消息/累计Token/模型(月环比)、
//          365 天活跃热力图、streak、模型榜、话题内容量榜。
//   用量 ← model-relay GET /v1/me/usage : 今日/本月积分、调用数、活跃模型、
//          按天柱状图(积分/Token)、per-model 分解、逐调用明细(分页)。
//
// 图表全部手搓 (无图表依赖):热力图 = 方格网格,排行 = 横条,按天 = CustomPainter。
// 字串参照同级 activity_pane 直接中文。

import 'dart:math' as math;

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../app/theme.dart';
import '../../../core/ui/biu_card.dart';
import '../application/stats_providers.dart';
import '../data/stats_client.dart';

class DataStatisticsPane extends ConsumerStatefulWidget {
  const DataStatisticsPane({super.key});

  @override
  ConsumerState<DataStatisticsPane> createState() => _DataStatisticsPaneState();
}

class _DataStatisticsPaneState extends ConsumerState<DataStatisticsPane> {
  // 概览
  ChatStats? _stats;
  // 用量 (累计 Token 卡片也用 summary)
  UsageReport? _usage;
  DateTime _month = DateTime.now(); // 选中月 (用其 year/month)
  int _page = 1;
  static const _pageSize = 10;

  bool _loadingStats = false;
  bool _loadingUsage = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (mounted) {
        _loadStats();
        _loadUsage();
      }
    });
  }

  String get _mo =>
      '${_month.year.toString().padLeft(4, '0')}-${_month.month.toString().padLeft(2, '0')}';

  Future<void> _loadStats() async {
    final client = ref.read(statsClientProvider);
    if (client == null) {
      setState(() => _error = '请先登录 BiuMind 账号');
      return;
    }
    setState(() {
      _loadingStats = true;
      _error = null;
    });
    try {
      final s = await client.fetchChatStats();
      if (!mounted) return;
      setState(() => _stats = s);
    } catch (e) {
      if (!mounted) return;
      setState(() => _error = '$e');
    } finally {
      if (mounted) setState(() => _loadingStats = false);
    }
  }

  Future<void> _loadUsage() async {
    final client = ref.read(statsClientProvider);
    if (client == null) {
      setState(() => _error = '请先登录 BiuMind 账号');
      return;
    }
    setState(() {
      _loadingUsage = true;
      _error = null;
    });
    try {
      final u = await client.fetchUsage(mo: _mo, page: _page, pageSize: _pageSize);
      if (!mounted) return;
      setState(() => _usage = u);
    } catch (e) {
      if (!mounted) return;
      setState(() => _error = '$e');
    } finally {
      if (mounted) setState(() => _loadingUsage = false);
    }
  }

  void _shiftMonth(int delta) {
    setState(() {
      _month = DateTime(_month.year, _month.month + delta);
      _page = 1;
    });
    _loadUsage();
  }

  void _gotoPage(int p) {
    setState(() => _page = p);
    _loadUsage();
  }

  @override
  Widget build(BuildContext context) {
    final c = Theme.of(context).extension<BiuColors>()!;
    return Center(
      child: ConstrainedBox(
        constraints: const BoxConstraints(maxWidth: 860),
        child: ListView(
          padding: const EdgeInsets.symmetric(
              horizontal: BiuTokens.space5, vertical: BiuTokens.space6),
          children: [
            Text('数据统计',
                style: Theme.of(context).textTheme.headlineLarge),
            const SizedBox(height: BiuTokens.space1),
            Text('账户级用量与活跃统计 · 跨设备汇总',
                style: TextStyle(fontSize: 12, color: c.textMuted)),
            const SizedBox(height: BiuTokens.space5),
            if (_error != null)
              _ErrorBox(message: _error!, onRetry: () {
                _loadStats();
                _loadUsage();
              }),
            // ── 活跃概览 ──
            _OverviewView(
              stats: _stats,
              usage: _usage,
              loading: _loadingStats && _stats == null,
            ),
            const SizedBox(height: BiuTokens.space6),
            _SectionLabel(label: '用量', icon: Icons.receipt_long_outlined),
            const SizedBox(height: BiuTokens.space4),
            // ── 用量明细 ──
            _UsageView(
              usage: _usage,
              loading: _loadingUsage && _usage == null,
              month: _mo,
              page: _page,
              pageSize: _pageSize,
              onPrevMonth: () => _shiftMonth(-1),
              onNextMonth: () => _shiftMonth(1),
              onPage: _gotoPage,
            ),
          ],
        ),
      ),
    );
  }
}

// ─── section label ───────────────────────────────────────

class _SectionLabel extends StatelessWidget {
  const _SectionLabel({required this.label, required this.icon});
  final String label;
  final IconData icon;
  @override
  Widget build(BuildContext context) {
    final c = Theme.of(context).extension<BiuColors>()!;
    return Row(children: [
      Icon(icon, size: 16, color: c.text2),
      const SizedBox(width: 6),
      Text(label,
          style: TextStyle(fontSize: 15, fontWeight: FontWeight.w700, color: c.text1)),
      const SizedBox(width: BiuTokens.space3),
      Expanded(child: Divider(color: c.borderHairline, height: 1)),
    ]);
  }
}

// ─── 概览 ────────────────────────────────────────────────

class _OverviewView extends StatelessWidget {
  const _OverviewView({
    required this.stats,
    required this.usage,
    required this.loading,
  });
  final ChatStats? stats;
  final UsageReport? usage;
  final bool loading;

  @override
  Widget build(BuildContext context) {
    if (loading) return const _Loading();
    final s = stats;
    if (s == null) return const _Empty();
    final tok = usage?.summary;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        // 4 卡
        Row(children: [
          Expanded(
              child: _StatCard(
                  label: '话题数', value: fmtInt(s.threads.count), mom: s.threads.momPercent, prev: s.threads.prev)),
          const SizedBox(width: BiuTokens.space3),
          Expanded(
              child: _StatCard(
                  label: '消息数', value: fmtInt(s.messages.count), mom: s.messages.momPercent, prev: s.messages.prev)),
          const SizedBox(width: BiuTokens.space3),
          Expanded(
              child: _StatCard(
                  label: '累计 Token',
                  value: tok == null ? '—' : fmtCompact(tok.totalTokens),
                  mom: tok?.tokenMomPercent,
                  prev: tok?.totalTokensPrev)),
          const SizedBox(width: BiuTokens.space3),
          Expanded(
              child: _StatCard(
                  label: '使用模型', value: fmtInt(s.models.count), mom: s.models.momPercent, prev: s.models.prev)),
        ]),
        const SizedBox(height: BiuTokens.space5),
        _HeatmapCard(stats: s),
        const SizedBox(height: BiuTokens.space5),
        Row(crossAxisAlignment: CrossAxisAlignment.start, children: [
          Expanded(
            child: _RankCard(
              title: '模型使用率',
              leftLabel: '模型',
              rightLabel: '消息数',
              items: [
                for (final m in s.modelRank) _RankRow(m.model, m.count),
              ],
            ),
          ),
          const SizedBox(width: BiuTokens.space3),
          Expanded(
            child: _RankCard(
              title: '话题内容量',
              leftLabel: '话题',
              rightLabel: '消息数',
              items: [
                for (final t in s.topicRank)
                  _RankRow(t.title.isEmpty ? '未命名对话' : t.title, t.count),
              ],
            ),
          ),
        ]),
      ],
    );
  }
}

// ─── 用量 ────────────────────────────────────────────────

class _UsageView extends StatefulWidget {
  const _UsageView({
    required this.usage,
    required this.loading,
    required this.month,
    required this.page,
    required this.pageSize,
    required this.onPrevMonth,
    required this.onNextMonth,
    required this.onPage,
  });
  final UsageReport? usage;
  final bool loading;
  final String month;
  final int page;
  final int pageSize;
  final VoidCallback onPrevMonth;
  final VoidCallback onNextMonth;
  final ValueChanged<int> onPage;

  @override
  State<_UsageView> createState() => _UsageViewState();
}

class _UsageViewState extends State<_UsageView> {
  bool _showTokens = false; // false=积分, true=Token

  @override
  Widget build(BuildContext context) {
    final c = Theme.of(context).extension<BiuColors>()!;
    if (widget.loading) return const _Loading();
    final u = widget.usage;
    if (u == null) return const _Empty();
    final s = u.summary;
    final totalPages = math.max(1, (u.total + widget.pageSize - 1) ~/ widget.pageSize);

    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        // 月选择
        Row(children: [
          _MonthStepper(month: widget.month, onPrev: widget.onPrevMonth, onNext: widget.onNextMonth),
          const Spacer(),
          _MetricToggle(
            showTokens: _showTokens,
            onChanged: (v) => setState(() => _showTokens = v),
          ),
        ]),
        const SizedBox(height: BiuTokens.space4),
        // 3 卡
        Row(children: [
          Expanded(child: _StatCard(label: '今日花费', value: '${fmtInt(s.todayCredits)} 积分')),
          const SizedBox(width: BiuTokens.space3),
          Expanded(
              child: _StatCard(
                  label: '本月花费',
                  value: '${fmtInt(s.monthCredits)} 积分',
                  sub: '${fmtInt(s.monthRequests)} 次调用')),
          const SizedBox(width: BiuTokens.space3),
          Expanded(child: _StatCard(label: '活跃模型', value: fmtInt(s.activeModels))),
        ]),
        const SizedBox(height: BiuTokens.space5),
        BiuCard(
          lift: 0,
          padding: const EdgeInsets.all(BiuTokens.space4),
          borderRadius: BorderRadius.circular(BiuTokens.radiusMd),
          child: Column(crossAxisAlignment: CrossAxisAlignment.stretch, children: [
            Text(_showTokens ? '按天 Token' : '按天积分消耗',
                style: TextStyle(fontSize: 13, fontWeight: FontWeight.w600, color: c.text1)),
            const SizedBox(height: BiuTokens.space4),
            SizedBox(
              height: 160,
              child: _DailyBarChart(
                daily: u.daily,
                month: widget.month,
                showTokens: _showTokens,
                bar: c.brand,
                axis: c.borderSoft,
                label: c.textMuted,
              ),
            ),
          ]),
        ),
        const SizedBox(height: BiuTokens.space5),
        if (u.byModel.isNotEmpty) ...[
          _RankCard(
            title: '模型花费分布',
            leftLabel: '模型',
            rightLabel: '积分',
            items: [for (final m in u.byModel) _RankRow(m.model, m.credits)],
          ),
          const SizedBox(height: BiuTokens.space5),
        ],
        _CallsTable(
          calls: u.calls,
          page: widget.page,
          totalPages: totalPages,
          onPage: widget.onPage,
        ),
      ],
    );
  }
}

// ─── stat card ───────────────────────────────────────────

class _StatCard extends StatelessWidget {
  const _StatCard({
    required this.label,
    required this.value,
    this.mom,
    this.prev,
    this.sub,
  });
  final String label;
  final String value;
  final double? mom; // 月环比 %
  final int? prev;
  final String? sub;

  @override
  Widget build(BuildContext context) {
    final c = Theme.of(context).extension<BiuColors>()!;
    return BiuCard(
      lift: 0,
      padding: const EdgeInsets.all(BiuTokens.space4),
      borderRadius: BorderRadius.circular(BiuTokens.radiusMd),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(children: [
            Flexible(
              child: Text(label,
                  style: TextStyle(fontSize: 12, color: c.textMuted),
                  overflow: TextOverflow.ellipsis),
            ),
            if (mom != null) ...[
              const SizedBox(width: 6),
              _MomChip(mom!),
            ],
          ]),
          const SizedBox(height: BiuTokens.space2),
          Text(value,
              style: TextStyle(
                  fontSize: 22, fontWeight: FontWeight.w700, color: c.text1)),
          if (sub != null) ...[
            const SizedBox(height: 2),
            Text(sub!, style: TextStyle(fontSize: 11, color: c.textMuted)),
          ] else if (prev != null) ...[
            const SizedBox(height: 2),
            Text('${fmtInt(prev!)} 上月',
                style: TextStyle(fontSize: 11, color: c.textMuted)),
          ],
        ],
      ),
    );
  }
}

class _MomChip extends StatelessWidget {
  const _MomChip(this.mom);
  final double mom;
  @override
  Widget build(BuildContext context) {
    final c = Theme.of(context).extension<BiuColors>()!;
    final up = mom >= 0;
    final color = up ? c.success : c.error;
    final sign = up ? '+' : '';
    return Text('$sign${mom.toStringAsFixed(1)}%',
        style: TextStyle(fontSize: 11, fontWeight: FontWeight.w600, color: color));
  }
}

// ─── heatmap ─────────────────────────────────────────────

class _HeatmapCard extends StatelessWidget {
  const _HeatmapCard({required this.stats});
  final ChatStats stats;

  @override
  Widget build(BuildContext context) {
    final c = Theme.of(context).extension<BiuColors>()!;
    return BiuCard(
      lift: 0,
      padding: const EdgeInsets.all(BiuTokens.space4),
      borderRadius: BorderRadius.circular(BiuTokens.radiusMd),
      child: Column(crossAxisAlignment: CrossAxisAlignment.stretch, children: [
        Row(children: [
          Text('过去一年活跃度',
              style: TextStyle(fontSize: 13, fontWeight: FontWeight.w600, color: c.text1)),
          const Spacer(),
          _Pill('活跃 ${stats.activeDays} 天', c.surface2, c.text2),
          const SizedBox(width: 6),
          _Pill('连续 ${stats.currentStreak} 天', c.successSoft, c.success),
        ]),
        const SizedBox(height: BiuTokens.space4),
        SingleChildScrollView(
          scrollDirection: Axis.horizontal,
          reverse: true,
          child: _HeatmapGrid(heatmap: stats.heatmap),
        ),
        const SizedBox(height: BiuTokens.space2),
        Row(mainAxisAlignment: MainAxisAlignment.end, children: [
          Text('较少', style: TextStyle(fontSize: 10, color: c.textMuted)),
          const SizedBox(width: 4),
          for (int l = 0; l <= 4; l++) ...[
            _cell(c, l),
            const SizedBox(width: 3),
          ],
          Text('较多', style: TextStyle(fontSize: 10, color: c.textMuted)),
        ]),
      ]),
    );
  }

  static Widget _cell(BiuColors c, int level) => Container(
        width: 11,
        height: 11,
        decoration: BoxDecoration(
          color: _levelColor(c, level),
          borderRadius: BorderRadius.circular(2),
        ),
      );
}

Color _levelColor(BiuColors c, int level) {
  switch (level) {
    case 0:
      return c.surface2;
    case 1:
      return c.brand.withValues(alpha: 0.28);
    case 2:
      return c.brand.withValues(alpha: 0.5);
    case 3:
      return c.brand.withValues(alpha: 0.74);
    default:
      return c.brand;
  }
}

class _HeatmapGrid extends StatelessWidget {
  const _HeatmapGrid({required this.heatmap});
  final List<HeatmapDay> heatmap;

  @override
  Widget build(BuildContext context) {
    final c = Theme.of(context).extension<BiuColors>()!;
    final counts = <String, int>{for (final h in heatmap) h.date: h.count};
    final maxCount = heatmap.fold<int>(0, (m, h) => math.max(m, h.count));

    final now = DateTime.now().toUtc();
    final today = DateTime.utc(now.year, now.month, now.day);
    final start = today.subtract(const Duration(days: 364));
    // 对齐到周一 (weekday: Mon=1 .. Sun=7)
    final startAligned = start.subtract(Duration(days: start.weekday - 1));

    String key(DateTime d) =>
        '${d.year.toString().padLeft(4, '0')}-${d.month.toString().padLeft(2, '0')}-${d.day.toString().padLeft(2, '0')}';

    final weeks = <Widget>[];
    var cursor = startAligned;
    while (!cursor.isAfter(today)) {
      final col = <Widget>[];
      for (int row = 0; row < 7; row++) {
        final isFuture = cursor.isAfter(today);
        final cnt = counts[key(cursor)] ?? 0;
        final lvl = isFuture ? -1 : _level(cnt, maxCount);
        col.add(Padding(
          padding: const EdgeInsets.all(1.5),
          child: Container(
            width: 11,
            height: 11,
            decoration: BoxDecoration(
              color: lvl < 0 ? Colors.transparent : _levelColor(c, lvl),
              borderRadius: BorderRadius.circular(2),
            ),
          ),
        ));
        cursor = cursor.add(const Duration(days: 1));
      }
      weeks.add(Column(children: col));
    }
    return Row(crossAxisAlignment: CrossAxisAlignment.start, children: weeks);
  }

  static int _level(int c, int max) {
    if (c <= 0) return 0;
    if (max <= 0) return 1;
    final r = c / max;
    if (r > 0.66) return 4;
    if (r > 0.33) return 3;
    if (r > 0.1) return 2;
    return 1;
  }
}

// ─── rank list ───────────────────────────────────────────

class _RankRow {
  final String label;
  final int value;
  const _RankRow(this.label, this.value);
}

class _RankCard extends StatelessWidget {
  const _RankCard({
    required this.title,
    required this.leftLabel,
    required this.rightLabel,
    required this.items,
  });
  final String title;
  final String leftLabel;
  final String rightLabel;
  final List<_RankRow> items;

  @override
  Widget build(BuildContext context) {
    final c = Theme.of(context).extension<BiuColors>()!;
    final shown = items.take(5).toList();
    final max = shown.fold<int>(0, (m, r) => math.max(m, r.value));
    return BiuCard(
      lift: 0,
      padding: const EdgeInsets.all(BiuTokens.space4),
      borderRadius: BorderRadius.circular(BiuTokens.radiusMd),
      child: Column(crossAxisAlignment: CrossAxisAlignment.stretch, children: [
        Text(title,
            style: TextStyle(fontSize: 13, fontWeight: FontWeight.w600, color: c.text1)),
        const SizedBox(height: BiuTokens.space3),
        Row(children: [
          Text(leftLabel, style: TextStyle(fontSize: 10, color: c.textMuted)),
          const Spacer(),
          Text(rightLabel, style: TextStyle(fontSize: 10, color: c.textMuted)),
        ]),
        const SizedBox(height: BiuTokens.space2),
        if (shown.isEmpty)
          Padding(
            padding: const EdgeInsets.symmetric(vertical: BiuTokens.space4),
            child: Text('暂无数据',
                style: TextStyle(fontSize: 12, color: c.textMuted)),
          )
        else
          for (final r in shown) ...[
            _bar(c, r, max),
            const SizedBox(height: BiuTokens.space2),
          ],
      ]),
    );
  }

  Widget _bar(BiuColors c, _RankRow r, int max) {
    final frac = max <= 0 ? 0.0 : r.value / max;
    return Stack(children: [
      // 背景条 (按比例)
      ClipRRect(
        borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
        child: LayoutBuilder(builder: (context, cons) {
          return Row(children: [
            Container(
              width: cons.maxWidth * frac.clamp(0.04, 1.0),
              height: 28,
              color: c.brand.withValues(alpha: 0.14),
            ),
          ]);
        }),
      ),
      // 文本覆盖
      Positioned.fill(
        child: Padding(
          padding: const EdgeInsets.symmetric(horizontal: 8),
          child: Row(children: [
            Expanded(
              child: Text(r.label,
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                  style: TextStyle(fontSize: 12, color: c.text1)),
            ),
            const SizedBox(width: 8),
            Text(fmtInt(r.value),
                style: TextStyle(
                    fontSize: 12, fontWeight: FontWeight.w600, color: c.text2)),
          ]),
        ),
      ),
    ]);
  }
}

// ─── daily bar chart (CustomPainter) ─────────────────────

class _DailyBarChart extends StatelessWidget {
  const _DailyBarChart({
    required this.daily,
    required this.month,
    required this.showTokens,
    required this.bar,
    required this.axis,
    required this.label,
  });
  final List<UsageDailyBucket> daily;
  final String month; // YYYY-MM
  final bool showTokens;
  final Color bar;
  final Color axis;
  final Color label;

  @override
  Widget build(BuildContext context) {
    // 填满整月天数 (按 day-of-month 对齐)。
    final parts = month.split('-');
    final year = int.tryParse(parts[0]) ?? DateTime.now().year;
    final mon = int.tryParse(parts.length > 1 ? parts[1] : '1') ?? 1;
    final days = DateTime(year, mon + 1, 0).day;
    final byDom = <int, int>{};
    for (final d in daily) {
      final dp = d.date.split('-');
      if (dp.length == 3) {
        final dom = int.tryParse(dp[2]) ?? 0;
        if (dom >= 1) byDom[dom] = showTokens ? d.tokens : d.credits;
      }
    }
    final values = [for (int i = 1; i <= days; i++) byDom[i] ?? 0];
    final hasAny = values.any((v) => v > 0);
    if (!hasAny) {
      final c = Theme.of(context).extension<BiuColors>()!;
      return Center(
        child: Text('本月暂无用量', style: TextStyle(fontSize: 12, color: c.textMuted)),
      );
    }
    return CustomPaint(
      painter: _BarPainter(values: values, bar: bar, axis: axis, label: label),
      size: Size.infinite,
    );
  }
}

class _BarPainter extends CustomPainter {
  _BarPainter({
    required this.values,
    required this.bar,
    required this.axis,
    required this.label,
  });
  final List<int> values;
  final Color bar;
  final Color axis;
  final Color label;

  @override
  void paint(Canvas canvas, Size size) {
    const leftPad = 8.0;
    const bottomPad = 18.0;
    final chartW = size.width - leftPad;
    final chartH = size.height - bottomPad;
    final maxV = values.fold<int>(0, math.max).toDouble();
    if (maxV <= 0) return;

    // 基线
    final axisPaint = Paint()
      ..color = axis
      ..strokeWidth = 1;
    canvas.drawLine(Offset(leftPad, chartH), Offset(size.width, chartH), axisPaint);

    final n = values.length;
    final slot = chartW / n;
    final barW = math.max(2.0, slot * 0.62);
    final barPaint = Paint()..color = bar;

    for (int i = 0; i < n; i++) {
      final v = values[i];
      if (v <= 0) continue;
      final h = (v / maxV) * (chartH - 4);
      final x = leftPad + slot * i + (slot - barW) / 2;
      final rect = RRect.fromRectAndRadius(
        Rect.fromLTWH(x, chartH - h, barW, h),
        const Radius.circular(2),
      );
      canvas.drawRRect(rect, barPaint);
    }

    // x 轴稀疏标签 (1 / 中 / 末)
    void drawLabel(int dom, double cx) {
      final tp = TextPainter(
        text: TextSpan(text: '$dom', style: TextStyle(fontSize: 9, color: label)),
        textDirection: TextDirection.ltr,
      )..layout();
      tp.paint(canvas, Offset(cx - tp.width / 2, chartH + 4));
    }

    drawLabel(1, leftPad + slot * 0.5);
    drawLabel((n / 2).round(), leftPad + slot * (n / 2 - 0.5));
    drawLabel(n, leftPad + slot * (n - 0.5));
  }

  @override
  bool shouldRepaint(covariant _BarPainter old) =>
      old.values != values || old.bar != bar;
}

// ─── calls table ─────────────────────────────────────────

class _CallsTable extends StatelessWidget {
  const _CallsTable({
    required this.calls,
    required this.page,
    required this.totalPages,
    required this.onPage,
  });
  final List<UsageCall> calls;
  final int page;
  final int totalPages;
  final ValueChanged<int> onPage;

  @override
  Widget build(BuildContext context) {
    final c = Theme.of(context).extension<BiuColors>()!;
    return BiuCard(
      lift: 0,
      padding: const EdgeInsets.all(BiuTokens.space4),
      borderRadius: BorderRadius.circular(BiuTokens.radiusMd),
      child: Column(crossAxisAlignment: CrossAxisAlignment.stretch, children: [
        Text('调用明细',
            style: TextStyle(fontSize: 13, fontWeight: FontWeight.w600, color: c.text1)),
        const SizedBox(height: BiuTokens.space3),
        _row(c,
            model: '模型',
            input: '输入',
            output: '输出',
            tps: 'TPS',
            credits: '花费',
            time: '时间',
            header: true),
        Divider(height: BiuTokens.space4, color: c.borderHairline),
        if (calls.isEmpty)
          Padding(
            padding: const EdgeInsets.symmetric(vertical: BiuTokens.space5),
            child: Center(
                child: Text('本月暂无调用',
                    style: TextStyle(fontSize: 12, color: c.textMuted))),
          )
        else
          for (final call in calls) ...[
            _row(c,
                model: call.model,
                input: fmtCompact(call.inputTokens),
                output: fmtCompact(call.outputTokens),
                tps: call.tps <= 0 ? '—' : call.tps.toStringAsFixed(1),
                credits: '${call.credits}',
                time: _fmtTime(call.createdAt)),
            const SizedBox(height: BiuTokens.space2),
          ],
        if (totalPages > 1) ...[
          const SizedBox(height: BiuTokens.space2),
          Row(mainAxisAlignment: MainAxisAlignment.center, children: [
            IconButton(
              iconSize: 16,
              onPressed: page > 1 ? () => onPage(page - 1) : null,
              icon: const Icon(Icons.chevron_left),
            ),
            Text('$page / $totalPages',
                style: TextStyle(fontSize: 12, color: c.text2)),
            IconButton(
              iconSize: 16,
              onPressed: page < totalPages ? () => onPage(page + 1) : null,
              icon: const Icon(Icons.chevron_right),
            ),
          ]),
        ],
      ]),
    );
  }

  Widget _row(
    BiuColors c, {
    required String model,
    required String input,
    required String output,
    required String tps,
    required String credits,
    required String time,
    bool header = false,
  }) {
    final style = TextStyle(
      fontSize: header ? 10 : 12,
      color: header ? c.textMuted : c.text1,
      fontWeight: header ? FontWeight.w500 : FontWeight.w400,
    );
    Widget cell(String s, int flex, {Alignment align = Alignment.centerLeft}) =>
        Expanded(
          flex: flex,
          child: Align(
            alignment: align,
            child: Text(s,
                maxLines: 1, overflow: TextOverflow.ellipsis, style: style),
          ),
        );
    return Row(children: [
      cell(model, 5),
      cell(input, 3, align: Alignment.centerRight),
      cell(output, 3, align: Alignment.centerRight),
      cell(tps, 3, align: Alignment.centerRight),
      cell(credits, 3, align: Alignment.centerRight),
      cell(time, 4, align: Alignment.centerRight),
    ]);
  }

  static String _fmtTime(DateTime t) {
    String two(int n) => n.toString().padLeft(2, '0');
    return '${two(t.month)}-${two(t.day)} ${two(t.hour)}:${two(t.minute)}';
  }
}

// ─── small shared widgets ────────────────────────────────

class _MonthStepper extends StatelessWidget {
  const _MonthStepper({required this.month, required this.onPrev, required this.onNext});
  final String month;
  final VoidCallback onPrev;
  final VoidCallback onNext;
  @override
  Widget build(BuildContext context) {
    final c = Theme.of(context).extension<BiuColors>()!;
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 4),
      decoration: BoxDecoration(
        color: c.surface2,
        borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
      ),
      child: Row(mainAxisSize: MainAxisSize.min, children: [
        IconButton(
            iconSize: 16, onPressed: onPrev, icon: const Icon(Icons.chevron_left)),
        Text(month, style: TextStyle(fontSize: 12, fontWeight: FontWeight.w600, color: c.text1)),
        IconButton(
            iconSize: 16, onPressed: onNext, icon: const Icon(Icons.chevron_right)),
      ]),
    );
  }
}

class _MetricToggle extends StatelessWidget {
  const _MetricToggle({required this.showTokens, required this.onChanged});
  final bool showTokens;
  final ValueChanged<bool> onChanged;
  @override
  Widget build(BuildContext context) {
    final c = Theme.of(context).extension<BiuColors>()!;
    Widget opt(String label, bool tokens) {
      final on = tokens == showTokens;
      return GestureDetector(
        onTap: () => onChanged(tokens),
        child: Container(
          padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 5),
          decoration: BoxDecoration(
            color: on ? c.surface0 : Colors.transparent,
            borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
          ),
          child: Text(label,
              style: TextStyle(
                  fontSize: 11,
                  fontWeight: on ? FontWeight.w600 : FontWeight.w500,
                  color: on ? c.text1 : c.textMuted)),
        ),
      );
    }

    return Container(
      padding: const EdgeInsets.all(2),
      decoration: BoxDecoration(
        color: c.surface2,
        borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
      ),
      child: Row(mainAxisSize: MainAxisSize.min, children: [
        opt('积分', false),
        opt('Token', true),
      ]),
    );
  }
}

class _Pill extends StatelessWidget {
  const _Pill(this.text, this.bg, this.fg);
  final String text;
  final Color bg;
  final Color fg;
  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 3),
      decoration: BoxDecoration(color: bg, borderRadius: BorderRadius.circular(BiuTokens.radiusFull)),
      child: Text(text, style: TextStyle(fontSize: 10, fontWeight: FontWeight.w600, color: fg)),
    );
  }
}

class _ErrorBox extends StatelessWidget {
  const _ErrorBox({required this.message, required this.onRetry});
  final String message;
  final VoidCallback onRetry;
  @override
  Widget build(BuildContext context) {
    final c = Theme.of(context).extension<BiuColors>()!;
    return Container(
      margin: const EdgeInsets.only(bottom: BiuTokens.space4),
      padding: const EdgeInsets.all(BiuTokens.space3),
      decoration: BoxDecoration(
        color: c.errorSoft,
        borderRadius: BorderRadius.circular(BiuTokens.radiusMd),
      ),
      child: Row(children: [
        Expanded(child: Text(message, style: TextStyle(fontSize: 12, color: c.error))),
        TextButton(onPressed: onRetry, child: const Text('重试')),
      ]),
    );
  }
}

class _Loading extends StatelessWidget {
  const _Loading();
  @override
  Widget build(BuildContext context) => const Padding(
        padding: EdgeInsets.symmetric(vertical: 80),
        child: Center(child: CircularProgressIndicator()),
      );
}

class _Empty extends StatelessWidget {
  const _Empty();
  @override
  Widget build(BuildContext context) {
    final c = Theme.of(context).extension<BiuColors>()!;
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 80),
      child: Center(
        child: Text('暂无数据 — 开始对话后这里会出现统计',
            style: TextStyle(fontSize: 13, color: c.textMuted)),
      ),
    );
  }
}

// ─── number formatting ───────────────────────────────────

String fmtInt(int n) {
  final s = n.abs().toString();
  final buf = StringBuffer();
  for (int i = 0; i < s.length; i++) {
    if (i > 0 && (s.length - i) % 3 == 0) buf.write(',');
    buf.write(s[i]);
  }
  return (n < 0 ? '-' : '') + buf.toString();
}

String fmtCompact(int n) {
  final a = n.abs();
  if (a >= 1000000000) return '${(n / 1e9).toStringAsFixed(1)}B';
  if (a >= 1000000) return '${(n / 1e6).toStringAsFixed(1)}M';
  if (a >= 1000) return '${(n / 1e3).toStringAsFixed(1)}K';
  return '$n';
}

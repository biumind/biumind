/// /wiki/p/:pid/graph —— 项目 wiki 关系图谱（B3.x 简化版）。
///
/// knowcode 原版拆 14 文件（force-directed simulation +
/// minimap + theme picker + filters + search + export + visuals）共 5781 行。
/// 本批做最小可用版：单文件 ~600 行，节点圆点 + 边 + label + 力导向
/// 一次性 layout（启动时跑 200 次迭代收敛后缓存）+ InteractiveViewer
/// 缩放/平移 + 节点点击跳页 + 顶部 toolbar (refresh / recompute)。
///
/// 完整视觉细节（minimap / 多主题 / 节点过滤 / 搜索高亮 / SVG 导出）
/// 等 brain graph worker 上线 + 真实数据规模上来再补。
library;

import 'dart:math' as math;

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../../app/theme.dart';
import '../../../../core/layout/phone_nav.dart';
import '../../../../data/api/wiki_client.dart'
    show
        WikiGraphData,
        WikiGraphEdge,
        WikiGraphNode,
        WikiInsightNodeBrief,
        WikiInsightStats,
        WikiInsights,
        WikiKnowledgeGap,
        WikiSurprisingConnection;
import '../../../../data/wiki_providers.dart' show wikiRepositoryProvider;
import '../research_dialog.dart';

final _graphProvider =
    FutureProvider.family<WikiGraphData, String>((ref, projectId) async {
  final repo = ref.watch(wikiRepositoryProvider);
  if (repo == null || projectId.isEmpty) {
    return const WikiGraphData(nodes: [], edges: []);
  }
  return repo.client.getGraph(projectId);
});

/// Graph insights — surprising connections + knowledge gaps。后端纯结构
/// 启发式（零 LLM），数据来自 relevance worker 预算的 page_relevance +
/// pages.community_id，所以复用 _graphProvider 的"repo 就绪"前置。
final _insightsProvider =
    FutureProvider.family<WikiInsights, String>((ref, projectId) async {
  final repo = ref.watch(wikiRepositoryProvider);
  if (repo == null || projectId.isEmpty) {
    return const WikiInsights(
      surprising: [],
      gaps: [],
      stats: WikiInsightStats(
          nodeCount: 0, edgeCount: 0, communityCount: 0),
    );
  }
  return repo.client.getGraphInsights(projectId);
});

class ProjectGraphPage extends ConsumerStatefulWidget {
  const ProjectGraphPage({super.key, required this.projectId});
  final String projectId;

  @override
  ConsumerState<ProjectGraphPage> createState() => _ProjectGraphPageState();
}

/// 图谱配色入口 — 4 套主题色,数据源在 lib/app/theme/category_colors.dart::GraphTheme。
const _graphThemes = GraphTheme.all;

class _ProjectGraphPageState extends ConsumerState<ProjectGraphPage> {
  bool _recomputing = false;
  String _searchQuery = '';
  int _themeIndex = 0;

  @override
  Widget build(BuildContext context) {
    final asyncData = ref.watch(_graphProvider(widget.projectId));
    return Column(
      children: [
        _Toolbar(
          recomputing: _recomputing,
          searchQuery: _searchQuery,
          themeIndex: _themeIndex,
          onRefresh: () => ref.invalidate(_graphProvider(widget.projectId)),
          onRecompute: _recomputing ? null : _recompute,
          onSearchChanged: (q) => setState(() => _searchQuery = q),
          onThemeChanged: (i) => setState(() => _themeIndex = i),
          onInsights: _showInsights,
        ),
        Divider(height: 1, color: BiuTokens.borderSubtle),
        Expanded(
          child: asyncData.when(
            loading: () => const Center(child: CircularProgressIndicator()),
            error: (e, _) => Center(
              child: Padding(
                padding: const EdgeInsets.all(24),
                child: SelectableText(
                  e.toString(),
                  style: TextStyle(color: BiuTokens.error, fontSize: 12),
                ),
              ),
            ),
            data: (g) {
              if (g.nodes.isEmpty) return const _EmptyView();
              return _GraphCanvas(
                data: g,
                projectId: widget.projectId,
                searchQuery: _searchQuery,
                palette: _graphThemes[_themeIndex].palette,
              );
            },
          ),
        ),
      ],
    );
  }

  Future<void> _recompute() async {
    setState(() => _recomputing = true);
    final repo = ref.read(wikiRepositoryProvider);
    if (repo == null) {
      setState(() => _recomputing = false);
      return;
    }
    try {
      await repo.client.recomputeGraph(widget.projectId);
      ref.invalidate(_graphProvider(widget.projectId));
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('已触发重建')),
      );
    } on Exception catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('重建失败：$e')),
      );
    } finally {
      if (mounted) setState(() => _recomputing = false);
    }
  }

  /// 洞察面板：surprising connections + knowledge gaps（纯结构启发式，
  /// 后端零 LLM）。数据依赖 relevance worker 跑过；空图谱时面板提示。
  void _showInsights() {
    showDialog<void>(
      context: context,
      builder: (_) => _InsightsDialog(projectId: widget.projectId),
    );
  }
}

/// 洞察弹窗 — Riverpod ConsumerWatcher 拉 _insightsProvider，渲染
/// surprising connections（跨社区/跨类型/外缘枢纽/弱边）+ knowledge gaps
///（孤立页/稀疏社区/桥节点）。点节点标题跳页。
class _InsightsDialog extends ConsumerWidget {
  const _InsightsDialog({required this.projectId});
  final String projectId;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final async = ref.watch(_insightsProvider(projectId));
    return Dialog(
      child: ConstrainedBox(
        constraints: const BoxConstraints(maxWidth: 560, maxHeight: 640),
        child: Padding(
          padding: const EdgeInsets.fromLTRB(20, 16, 20, 12),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Row(
                children: [
                  Icon(Icons.insights_outlined,
                      size: 18, color: BiuTokens.purple),
                  const SizedBox(width: 8),
                  Text('图谱洞察',
                      style: TextStyle(
                        color: BiuTokens.text,
                        fontSize: 15,
                        fontWeight: FontWeight.w600,
                      )),
                  const Spacer(),
                  IconButton(
                    tooltip: '关闭',
                    onPressed: () => Navigator.of(context).pop(),
                    icon: const Icon(Icons.close, size: 16),
                    padding: EdgeInsets.zero,
                    constraints:
                        const BoxConstraints(minWidth: 28, minHeight: 28),
                  ),
                ],
              ),
              Divider(height: 12, color: BiuTokens.borderSubtle),
              Flexible(
                child: async.when(
                  loading: () => const Center(
                      child: Padding(
                    padding: EdgeInsets.symmetric(vertical: 40),
                    child: CircularProgressIndicator(),
                  )),
                  error: (e, _) => Padding(
                    padding: const EdgeInsets.symmetric(vertical: 24),
                    child: SelectableText(
                      '洞察加载失败：$e',
                      style: TextStyle(color: BiuTokens.error, fontSize: 12),
                    ),
                  ),
                  data: (ins) => _InsightsBody(
                    insights: ins,
                    projectId: projectId,
                  ),
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

class _InsightsBody extends StatelessWidget {
  const _InsightsBody({required this.insights, required this.projectId});
  final WikiInsights insights;
  final String projectId;

  @override
  Widget build(BuildContext context) {
    final s = insights.stats;
    final empty = insights.surprising.isEmpty && insights.gaps.isEmpty;
    return SingleChildScrollView(
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Text(
            '${s.nodeCount} 页 · ${s.edgeCount} 条关联 · ${s.communityCount} 个聚类',
            style: TextStyle(fontSize: 11, color: BiuTokens.textMuted),
          ),
          if (empty)
            Padding(
              padding: const EdgeInsets.symmetric(vertical: 32),
              child: Center(
                child: Text(
                  insights.stats.nodeCount == 0
                      ? '图谱为空，暂无洞察。'
                      : '当前图谱结构良好，未发现值得关注的问题。',
                  style: TextStyle(fontSize: 12, color: BiuTokens.textMuted),
                  textAlign: TextAlign.center,
                ),
              ),
            ),
          if (insights.surprising.isNotEmpty) ...[
            _SectionHeader(icon: Icons.bolt_outlined, title: '意外连接'),
            for (final c in insights.surprising)
              _SurprisingCard(conn: c, projectId: projectId),
          ],
          if (insights.gaps.isNotEmpty) ...[
            const SizedBox(height: 8),
            _SectionHeader(icon: Icons.lightbulb_outline, title: '知识缺口'),
            for (final g in insights.gaps)
              _GapCard(gap: g, projectId: projectId),
          ],
        ],
      ),
    );
  }
}

class _SectionHeader extends StatelessWidget {
  const _SectionHeader({required this.icon, required this.title});
  final IconData icon;
  final String title;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.only(top: 8, bottom: 6),
      child: Row(
        children: [
          Icon(icon, size: 14, color: BiuTokens.textSecondary),
          const SizedBox(width: 6),
          Text(title,
              style: TextStyle(
                color: BiuTokens.text,
                fontSize: 13,
                fontWeight: FontWeight.w600,
              )),
        ],
      ),
    );
  }
}

class _SurprisingCard extends StatelessWidget {
  const _SurprisingCard({required this.conn, required this.projectId});
  final WikiSurprisingConnection conn;
  final String projectId;

  @override
  Widget build(BuildContext context) {
    return Container(
      margin: const EdgeInsets.only(bottom: 8),
      padding: const EdgeInsets.all(10),
      decoration: BoxDecoration(
        color: BiuTokens.purple.withValues(alpha: 0.04),
        borderRadius: BorderRadius.circular(8),
        border: Border.all(color: BiuTokens.borderSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              _NodeLink(brief: conn.source, projectId: projectId),
              Padding(
                padding: const EdgeInsets.symmetric(horizontal: 6),
                child: Icon(Icons.link,
                    size: 12, color: BiuTokens.textMuted),
              ),
              _NodeLink(brief: conn.target, projectId: projectId),
              const Spacer(),
              Container(
                padding:
                    const EdgeInsets.symmetric(horizontal: 6, vertical: 1),
                decoration: BoxDecoration(
                  color: BiuTokens.purple.withValues(alpha: 0.12),
                  borderRadius: BorderRadius.circular(4),
                ),
                child: Text(
                  '${conn.score}',
                  style: TextStyle(
                    fontSize: 10,
                    color: BiuTokens.purple,
                    fontWeight: FontWeight.w600,
                  ),
                ),
              ),
            ],
          ),
          const SizedBox(height: 4),
          Wrap(
            spacing: 6,
            runSpacing: 2,
            children: [
              for (final r in conn.reasons)
                Text('· $r',
                    style: TextStyle(
                        fontSize: 11, color: BiuTokens.textSecondary)),
            ],
          ),
        ],
      ),
    );
  }
}

class _GapCard extends StatelessWidget {
  const _GapCard({required this.gap, required this.projectId});
  final WikiKnowledgeGap gap;
  final String projectId;

  @override
  Widget build(BuildContext context) {
    final (icon, color) = _gapVisual(gap.type);
    return Container(
      margin: const EdgeInsets.only(bottom: 8),
      padding: const EdgeInsets.all(10),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.04),
        borderRadius: BorderRadius.circular(8),
        border: Border.all(color: BiuTokens.borderSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Icon(icon, size: 14, color: color),
              const SizedBox(width: 6),
              Expanded(
                child: Text(gap.title,
                    style: TextStyle(
                      color: BiuTokens.text,
                      fontSize: 12,
                      fontWeight: FontWeight.w600,
                    )),
              ),
            ],
          ),
          const SizedBox(height: 4),
          Text(gap.description,
              style:
                  TextStyle(fontSize: 11, color: BiuTokens.textSecondary)),
          if (gap.suggestion.isNotEmpty) ...[
            const SizedBox(height: 4),
            Text('建议：${gap.suggestion}',
                style: TextStyle(fontSize: 11, color: BiuTokens.textMuted)),
          ],
          if (gap.nodeIds.isNotEmpty)
            Padding(
              padding: const EdgeInsets.only(top: 6),
              child: Wrap(
                spacing: 6,
                runSpacing: 4,
                children: [
                  // 只渲染前 8 个节点链接，避免稀疏社区几十页撑爆面板。
                  for (final id in gap.nodeIds.take(8))
                    _PageChip(pageId: id, projectId: projectId),
                  if (gap.nodeIds.length > 8)
                    Text('+${gap.nodeIds.length - 8}',
                        style: TextStyle(
                            fontSize: 10, color: BiuTokens.textMuted)),
                ],
              ),
            ),
          // 缺口 → Deep Research 一键入口：预填话题 + 描述作为细化查询，
          // 用户在研究对话框里确认后提交。
          Align(
            alignment: Alignment.centerRight,
            child: TextButton.icon(
              onPressed: () => showResearchDialog(
                context,
                projectId: projectId,
                initialTopic: gap.title,
                initialQueries: [
                  if (gap.description.isNotEmpty) gap.description,
                ],
              ),
              icon: const Icon(Icons.travel_explore, size: 14),
              label: const Text('研究'),
              style: TextButton.styleFrom(
                foregroundColor: BiuTokens.purple,
                textStyle: const TextStyle(fontSize: 12),
                padding:
                    const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
                minimumSize: const Size(0, 28),
                tapTargetSize: MaterialTapTargetSize.shrinkWrap,
              ),
            ),
          ),
        ],
      ),
    );
  }

  (IconData, Color) _gapVisual(String type) {
    switch (type) {
      case 'isolated-node':
        return (Icons.circle_outlined, Colors.amber);
      case 'sparse-community':
        return (Icons.scatter_plot_outlined, BiuTokens.purple);
      case 'bridge-node':
        return (Icons.account_tree_outlined, Colors.teal);
      default:
        return (Icons.lightbulb_outline, BiuTokens.textSecondary);
    }
  }
}

/// 节点标题链接 — 点击 enterSubPage 跳到该页。空 title 回退 pageId 前 8 位。
class _NodeLink extends StatelessWidget {
  const _NodeLink({required this.brief, required this.projectId});
  final WikiInsightNodeBrief brief;
  final String projectId;

  @override
  Widget build(BuildContext context) {
    return InkWell(
      onTap: brief.id.isEmpty
          ? null
          : () => enterSubPage(context, '/wiki/p/$projectId/pages/${brief.id}'),
      child: Text(
        brief.title.isEmpty ? brief.id.substring(0, 8) : brief.title,
        style: TextStyle(
          fontSize: 12,
          color: BiuTokens.purple,
          fontWeight: FontWeight.w500,
        ),
      ),
    );
  }
}

class _PageChip extends StatelessWidget {
  const _PageChip({required this.pageId, required this.projectId});
  final String pageId;
  final String projectId;

  @override
  Widget build(BuildContext context) {
    return InkWell(
      onTap: () => enterSubPage(context, '/wiki/p/$projectId/pages/$pageId'),
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
        decoration: BoxDecoration(
          color: BiuTokens.purple.withValues(alpha: 0.08),
          borderRadius: BorderRadius.circular(4),
        ),
        child: Text(
          pageId.substring(0, 8),
          style: TextStyle(
              fontSize: 10,
              color: BiuTokens.purple,
              fontFamily: 'monospace'),
        ),
      ),
    );
  }
}

class _Toolbar extends StatelessWidget {
  const _Toolbar({
    required this.recomputing,
    required this.searchQuery,
    required this.themeIndex,
    required this.onRefresh,
    required this.onRecompute,
    required this.onSearchChanged,
    required this.onThemeChanged,
    required this.onInsights,
  });
  final bool recomputing;
  final String searchQuery;
  final int themeIndex;
  final VoidCallback onRefresh;
  final VoidCallback? onRecompute;
  final ValueChanged<String> onSearchChanged;
  final ValueChanged<int> onThemeChanged;
  final VoidCallback onInsights;

  @override
  Widget build(BuildContext context) {
    return Container(
      height: 48,
      padding: const EdgeInsets.symmetric(horizontal: 16),
      child: Row(
        children: [
          // 子页头左位 ← (手机形态; 桌面 shrink 不占位, §3.3)。
          const PhoneBackButton(),
          Icon(Icons.hub_outlined, size: 16, color: BiuTokens.textSecondary),
          const SizedBox(width: 8),
          Text(
            '图谱',
            style: TextStyle(
              color: BiuTokens.text,
              fontSize: 14,
              fontWeight: FontWeight.w600,
            ),
          ),
          const SizedBox(width: 12),
          // 节点搜索：实时过滤 + 高亮
          SizedBox(
            width: 240,
            height: 30,
            child: TextField(
              onChanged: onSearchChanged,
              decoration: InputDecoration(
                isDense: true,
                hintText: '搜节点 title…',
                hintStyle:
                    TextStyle(fontSize: 12, color: BiuTokens.textMuted),
                prefixIcon: Icon(Icons.search,
                    size: 14, color: BiuTokens.textMuted),
                prefixIconConstraints:
                    const BoxConstraints(minWidth: 28, minHeight: 0),
                contentPadding:
                    const EdgeInsets.symmetric(horizontal: 4, vertical: 6),
                border: OutlineInputBorder(
                  borderRadius: BorderRadius.circular(6),
                  borderSide: BorderSide(color: BiuTokens.borderSubtle),
                ),
              ),
              style: TextStyle(fontSize: 12, color: BiuTokens.text),
            ),
          ),
          const Spacer(),
          // Theme picker
          PopupMenuButton<int>(
            tooltip: '配色',
            initialValue: themeIndex,
            onSelected: onThemeChanged,
            icon: Icon(_graphThemes[themeIndex].icon,
                size: 16, color: BiuTokens.textSecondary),
            itemBuilder: (_) => [
              for (var i = 0; i < _graphThemes.length; i++)
                PopupMenuItem(
                  value: i,
                  child: Row(
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      Icon(_graphThemes[i].icon,
                          size: 14,
                          color: i == themeIndex
                              ? BiuTokens.purple
                              : BiuTokens.textSecondary),
                      const SizedBox(width: 8),
                      Text(_graphThemes[i].name,
                          style: TextStyle(
                            fontSize: 12,
                            color: i == themeIndex
                                ? BiuTokens.purple
                                : BiuTokens.text,
                            fontWeight: i == themeIndex
                                ? FontWeight.w600
                                : FontWeight.w400,
                          )),
                      const SizedBox(width: 24),
                      // 调色板小色块预览
                      for (final c in _graphThemes[i].palette.take(4))
                        Container(
                          width: 8,
                          height: 8,
                          margin: const EdgeInsets.only(right: 2),
                          decoration: BoxDecoration(
                            color: c,
                            borderRadius: BorderRadius.circular(2),
                          ),
                        ),
                    ],
                  ),
                ),
            ],
          ),
          IconButton(
            tooltip: '洞察',
            onPressed: onInsights,
            icon: const Icon(Icons.insights_outlined, size: 16),
            padding: EdgeInsets.zero,
            constraints: const BoxConstraints(minWidth: 32, minHeight: 32),
          ),
          const SizedBox(width: 4),
          IconButton(
            tooltip: '刷新',
            onPressed: onRefresh,
            icon: const Icon(Icons.refresh, size: 16),
            padding: EdgeInsets.zero,
            constraints: const BoxConstraints(minWidth: 32, minHeight: 32),
          ),
          const SizedBox(width: 4),
          OutlinedButton.icon(
            onPressed: onRecompute,
            icon: recomputing
                ? const SizedBox(
                    width: 12,
                    height: 12,
                    child: CircularProgressIndicator(strokeWidth: 1.5),
                  )
                : const Icon(Icons.auto_fix_high, size: 14),
            label: Text(recomputing ? '重建中…' : '重建关系'),
            style: OutlinedButton.styleFrom(
              foregroundColor: BiuTokens.purple,
              side: BorderSide(color: BiuTokens.purple),
              padding:
                  const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
              textStyle: const TextStyle(fontSize: 12),
              minimumSize: const Size(0, 32),
            ),
          ),
        ],
      ),
    );
  }
}

class _EmptyView extends StatelessWidget {
  const _EmptyView();

  @override
  Widget build(BuildContext context) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(32),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(Icons.hub_outlined, size: 48, color: BiuTokens.textMuted),
            const SizedBox(height: 12),
            Text(
              '当前项目尚未生成图谱',
              style: TextStyle(
                color: BiuTokens.text,
                fontSize: 14,
                fontWeight: FontWeight.w600,
              ),
            ),
            const SizedBox(height: 4),
            Text(
              '页面间需要至少 [[wikilink]] 引用 / 相似度 > 阈值 才会形成边。\n'
              '点击右上「重建关系」立即触发一次。',
              textAlign: TextAlign.center,
              style: TextStyle(color: BiuTokens.textMuted, fontSize: 12),
            ),
          ],
        ),
      ),
    );
  }
}

// ─── Graph canvas ───────────────────────────────────────────────────

class _GraphCanvas extends StatefulWidget {
  const _GraphCanvas({
    required this.data,
    required this.projectId,
    required this.searchQuery,
    required this.palette,
  });
  final WikiGraphData data;
  final String projectId;
  final String searchQuery;
  final List<Color> palette;

  @override
  State<_GraphCanvas> createState() => _GraphCanvasState();
}

class _GraphCanvasState extends State<_GraphCanvas> {
  /// 一次性 layout 后缓存的节点 (id → Offset)。recompute 触发时重新跑。
  late Map<String, Offset> _positions;
  late Size _layoutSize;
  String? _hoverId;
  String? _selectedId;
  final TransformationController _transform = TransformationController();

  static const double _nodeRadius = 10;

  @override
  void initState() {
    super.initState();
    _runLayout();
  }

  @override
  void didUpdateWidget(covariant _GraphCanvas old) {
    super.didUpdateWidget(old);
    if (old.data != widget.data) {
      _runLayout();
    }
  }

  @override
  void dispose() {
    _transform.dispose();
    super.dispose();
  }

  /// 一次性 force-directed simulation：200 次迭代收敛后缓存结果。
  /// 节点数 << 1k 的项目跑完瞬时（< 50ms），不需要每帧动画。
  void _runLayout() {
    final nodes = widget.data.nodes;
    final edges = widget.data.edges;

    // 画布尺寸：根据节点数粗略估计
    final n = nodes.length;
    final side = math.max(600, math.sqrt(n) * 80).toDouble();
    _layoutSize = Size(side, side);

    final rng = math.Random(42); // 固定 seed 让重启视图位置稳定
    final pos = <String, _Vec>{};
    final disp = <String, _Vec>{};

    // 初始位置：均匀分布在画布中
    for (final node in nodes) {
      pos[node.id] = _Vec(
        rng.nextDouble() * side,
        rng.nextDouble() * side,
      );
    }

    if (n == 0) {
      _positions = const {};
      return;
    }

    // Fruchterman-Reingold 简化版
    final area = side * side;
    final k = math.sqrt(area / n);
    var temperature = side / 10;

    final neighbors = <String, List<String>>{};
    for (final e in edges) {
      neighbors.putIfAbsent(e.source, () => []).add(e.target);
      neighbors.putIfAbsent(e.target, () => []).add(e.source);
    }

    const iterations = 200;
    for (var iter = 0; iter < iterations; iter++) {
      // 反作用力（每对节点）
      for (final v in nodes) {
        disp[v.id] = const _Vec(0, 0);
        final pv = pos[v.id]!;
        for (final u in nodes) {
          if (u.id == v.id) continue;
          final pu = pos[u.id]!;
          final delta = pv - pu;
          final dist = math.max(0.01, delta.magnitude);
          final force = (k * k) / dist;
          disp[v.id] = disp[v.id]! + delta.normalized * force;
        }
      }
      // 引力（边）
      for (final e in edges) {
        final pv = pos[e.source];
        final pu = pos[e.target];
        if (pv == null || pu == null) continue;
        final delta = pv - pu;
        final dist = math.max(0.01, delta.magnitude);
        final force = (dist * dist) / k;
        final pull = delta.normalized * force;
        disp[e.source] = disp[e.source]! - pull;
        disp[e.target] = disp[e.target]! + pull;
      }
      // 应用 displacement，限制最大移动 = temperature
      for (final v in nodes) {
        final d = disp[v.id]!;
        final dist = math.max(0.01, d.magnitude);
        final move = d.normalized * math.min(dist, temperature);
        var next = pos[v.id]! + move;
        // 边界 clamp
        next = _Vec(
          next.x.clamp(_nodeRadius, side - _nodeRadius),
          next.y.clamp(_nodeRadius, side - _nodeRadius),
        );
        pos[v.id] = next;
      }
      temperature *= 0.97;
    }

    _positions = {
      for (final n in nodes) n.id: Offset(pos[n.id]!.x, pos[n.id]!.y),
    };
  }

  void _onTapDown(TapDownDetails details) {
    // InteractiveViewer 内的坐标。命中检测按距离最近节点。
    final tap = details.localPosition;
    String? hit;
    var bestDist = _nodeRadius * 2;
    for (final entry in _positions.entries) {
      final d = (entry.value - tap).distance;
      if (d < bestDist) {
        bestDist = d;
        hit = entry.key;
      }
    }
    if (hit != null) {
      setState(() => _selectedId = hit);
      // 点选 + 单击就跳转
      enterSubPage(context, '/wiki/p/${widget.projectId}/pages/$hit');
    }
  }

  @override
  Widget build(BuildContext context) {
    return InteractiveViewer(
      transformationController: _transform,
      minScale: 0.2,
      maxScale: 4,
      boundaryMargin: const EdgeInsets.all(200),
      constrained: false,
      child: SizedBox(
        width: _layoutSize.width,
        height: _layoutSize.height,
        child: GestureDetector(
          onTapDown: _onTapDown,
          child: MouseRegion(
            onHover: (e) {
              String? hover;
              var best = _nodeRadius * 2;
              for (final entry in _positions.entries) {
                final d = (entry.value - e.localPosition).distance;
                if (d < best) {
                  best = d;
                  hover = entry.key;
                }
              }
              if (hover != _hoverId) setState(() => _hoverId = hover);
            },
            onExit: (_) {
              if (_hoverId != null) setState(() => _hoverId = null);
            },
            child: CustomPaint(
              size: _layoutSize,
              painter: _GraphPainter(
                nodes: widget.data.nodes,
                edges: widget.data.edges,
                positions: _positions,
                hoverId: _hoverId,
                selectedId: _selectedId,
                nodeRadius: _nodeRadius,
                searchQuery: widget.searchQuery.toLowerCase().trim(),
                palette: widget.palette,
              ),
            ),
          ),
        ),
      ),
    );
  }
}

// ─── Painter ────────────────────────────────────────────────────────

class _GraphPainter extends CustomPainter {
  _GraphPainter({
    required this.nodes,
    required this.edges,
    required this.positions,
    required this.hoverId,
    required this.selectedId,
    required this.nodeRadius,
    required this.searchQuery,
    required this.palette,
  });

  final List<WikiGraphNode> nodes;
  final List<WikiGraphEdge> edges;
  final Map<String, Offset> positions;
  final String? hoverId;
  final String? selectedId;
  final double nodeRadius;
  final String searchQuery;
  final List<Color> palette;

  /// 节点 id 是否命中搜索（titlematch / id 前缀 match）。
  /// 空 query → null（关闭高亮模式，所有节点正常透明度）。
  bool? _hits(WikiGraphNode node) {
    if (searchQuery.isEmpty) return null;
    return node.title.toLowerCase().contains(searchQuery) ||
        node.id.toLowerCase().startsWith(searchQuery);
  }

  @override
  void paint(Canvas canvas, Size size) {
    final searching = searchQuery.isNotEmpty;
    final hitIds = <String>{
      if (searching)
        for (final n in nodes)
          if (_hits(n) == true) n.id,
    };

    // 边
    final edgePaint = Paint()
      ..strokeWidth = 1
      ..style = PaintingStyle.stroke;
    for (final e in edges) {
      final a = positions[e.source];
      final b = positions[e.target];
      if (a == null || b == null) continue;
      final highlighted = hoverId == e.source ||
          hoverId == e.target ||
          selectedId == e.source ||
          selectedId == e.target;
      // 搜索模式下，仅命中节点连接的边保持正常；其他边大幅淡化
      final dimmed = searching &&
          !hitIds.contains(e.source) &&
          !hitIds.contains(e.target);
      edgePaint
        ..color = highlighted
            ? BiuTokens.purple.withValues(alpha: 0.6)
            : BiuTokens.textMuted
                .withValues(alpha: dimmed ? 0.08 : 0.3)
        ..strokeWidth = highlighted ? 1.5 : 1;
      canvas.drawLine(a, b, edgePaint);
    }

    // 节点
    final textStyle = TextStyle(
      color: BiuTokens.text,
      fontSize: 11,
      fontWeight: FontWeight.w500,
    );
    for (final node in nodes) {
      final p = positions[node.id];
      if (p == null) continue;

      final isHover = hoverId == node.id;
      final isSelected = selectedId == node.id;
      final isHit = searching && hitIds.contains(node.id);
      final isDimmed = searching && !isHit;
      final color = _communityColor(node.community);
      // 命中节点：半径放大 1.4×；其他节点：搜索时缩小
      var r = nodeRadius * (1 + math.log(1 + node.weight) * 0.2);
      if (isHit) r *= 1.4;
      final alpha = isDimmed ? 0.18 : 1.0;

      // 选中环
      if (isSelected) {
        canvas.drawCircle(
          p,
          r + 4,
          Paint()
            ..color = BiuTokens.purple
            ..style = PaintingStyle.stroke
            ..strokeWidth = 2,
        );
      }

      // 搜索命中：金色脉冲圈
      if (isHit) {
        canvas.drawCircle(
          p,
          r + 6,
          Paint()
            ..color = NamedPalette.yellow.withValues(alpha: 0.45)
            ..style = PaintingStyle.stroke
            ..strokeWidth = 2,
        );
      }

      // hover 半透明扩散圆
      if (isHover) {
        canvas.drawCircle(
          p,
          r + 6,
          Paint()..color = color.withValues(alpha: 0.2 * alpha),
        );
      }

      // 节点本体
      canvas.drawCircle(p, r, Paint()..color = color.withValues(alpha: alpha));
      canvas.drawCircle(
        p,
        r,
        Paint()
          ..color = Colors.white.withValues(alpha: alpha)
          ..style = PaintingStyle.stroke
          ..strokeWidth = 1.5,
      );

      // label：hover/selected 显示；搜索命中也强制显示
      if (isHover || isSelected || isHit) {
        final label = node.title.isEmpty ? '(未命名)' : node.title;
        final tp = TextPainter(
          text: TextSpan(text: label, style: textStyle),
          textDirection: TextDirection.ltr,
          maxLines: 1,
          ellipsis: '…',
        )..layout(maxWidth: 160);
        final bg = Rect.fromCenter(
          center: p.translate(0, r + 16),
          width: tp.width + 12,
          height: tp.height + 6,
        );
        canvas.drawRRect(
          RRect.fromRectAndRadius(bg, const Radius.circular(4)),
          Paint()..color = BiuTokens.surface,
        );
        canvas.drawRRect(
          RRect.fromRectAndRadius(bg, const Radius.circular(4)),
          Paint()
            ..color = BiuTokens.borderSubtle
            ..style = PaintingStyle.stroke
            ..strokeWidth = 1,
        );
        tp.paint(canvas, Offset(bg.left + 6, bg.top + 3));
      }
    }
  }

  /// 社区 → 调色板。由外部 palette 传入，相邻 community id 视觉距离大。
  Color _communityColor(int c) {
    if (palette.isEmpty) return BiuTokens.textMuted;
    return palette[c.abs() % palette.length];
  }

  @override
  bool shouldRepaint(covariant _GraphPainter old) {
    return old.nodes != nodes ||
        old.edges != edges ||
        old.positions != positions ||
        old.hoverId != hoverId ||
        old.selectedId != selectedId ||
        old.searchQuery != searchQuery ||
        old.palette != palette;
  }
}

// ─── 简单 2D 向量 helper ────────────────────────────────────────────

class _Vec {
  const _Vec(this.x, this.y);
  final double x;
  final double y;

  double get magnitude => math.sqrt(x * x + y * y);
  _Vec get normalized {
    final m = magnitude;
    if (m < 0.0001) return const _Vec(0, 0);
    return _Vec(x / m, y / m);
  }

  _Vec operator +(_Vec o) => _Vec(x + o.x, y + o.y);
  _Vec operator -(_Vec o) => _Vec(x - o.x, y - o.y);
  _Vec operator *(double f) => _Vec(x * f, y * f);
}

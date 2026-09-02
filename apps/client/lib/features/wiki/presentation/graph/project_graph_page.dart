/// /wiki/p/:pid/graph —— 项目 wiki 关系图谱。
///
/// 单文件实现：节点圆点 + 边 + label + 力导向一次性 layout（启动时跑
/// 200 次迭代收敛后缓存）+ InteractiveViewer 缩放/平移 + 节点点击跳页
/// + 顶部 toolbar（搜索 / 过滤 / 着色模式 / 配色 / 洞察 / 刷新 / 重建）。
///
/// P2 #21 体验补齐（思路参考 reference/llm_wiki graph-view.tsx，未拷贝）：
///   * 过滤器面板：结构页 / 孤立页 / page_type / 度数区间，作用于渲染与布局
///   * 着色双模式：按 Louvain 聚类 / 按 page_type（pageTypeConfigOf 色）
///   * 洞察卡片画布联动：定位 = 高亮 + 居中对应节点；逐条 dismiss（本地态）
///   * 知识缺口文案中文化：后端英文硬编码，客户端按 gap.type 重新生成
///
/// 过滤 / 文案的纯逻辑在 `../../application/graph_filters.dart`（可单测）。
library;

import 'dart:math' as math;

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../../app/theme.dart';
import '../../../../core/layout/form_factor.dart';
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
import '../../application/graph_filters.dart';
import '../pages/page_type_config.dart';
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
  GraphFilterState _filters = const GraphFilterState();
  GraphColorMode _colorMode = GraphColorMode.community;

  /// 洞察「定位」要高亮 + 居中的节点集合；nonce 让同一集合重复定位也触发。
  Set<String> _focusIds = const {};
  int _focusNonce = 0;

  /// 洞察逐条 dismiss 的 key 集合（surprising.key / gapDismissKey）。
  /// 本地态，跨面板开关保留、重启即失效 — 持久化非必须（P2 #21 取舍）。
  final Set<String> _dismissedInsightKeys = {};

  @override
  Widget build(BuildContext context) {
    final asyncData = ref.watch(_graphProvider(widget.projectId));
    final graph = asyncData.valueOrNull;
    return Column(
      children: [
        _Toolbar(
          recomputing: _recomputing,
          searchQuery: _searchQuery,
          themeIndex: _themeIndex,
          filterActive: _filters.isActive,
          colorMode: _colorMode,
          onRefresh: () => ref.invalidate(_graphProvider(widget.projectId)),
          onRecompute: _recomputing ? null : _recompute,
          onSearchChanged: (q) => setState(() => _searchQuery = q),
          onThemeChanged: (i) => setState(() => _themeIndex = i),
          onColorModeChanged: (m) => setState(() => _colorMode = m),
          onFilters: () => _showFilters(graph),
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
                colorMode: _colorMode,
                filterState: _filters,
                focusIds: _focusIds,
                focusNonce: _focusNonce,
                onClearFocus: _clearFocus,
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

  /// 过滤面板：手机形态 bottom sheet（§4.6 范式），桌面 dialog。
  /// 面板内每次变更即时生效（布局随过滤态重跑）。
  void _showFilters(WikiGraphData? graph) {
    final availableTypes = <String>{
      for (final n in graph?.nodes ?? const <WikiGraphNode>[])
        n.pageType ?? '',
    }.toList()
      ..sort();
    var maxDegree = 0;
    if (graph != null) {
      for (final d in computeNodeDegrees(graph.nodes, graph.edges).values) {
        if (d > maxDegree) maxDegree = d;
      }
    }
    final panel = _FilterPanel(
      initialState: _filters,
      availableTypes: availableTypes,
      maxDegree: maxDegree,
      onChanged: (f) => setState(() => _filters = f),
    );
    if (isPhoneLayout(context)) {
      showModalBottomSheet<void>(
        context: context,
        isScrollControlled: true,
        showDragHandle: true,
        builder: (_) => panel,
      );
    } else {
      showDialog<void>(
        context: context,
        builder: (_) => Dialog(
          child: ConstrainedBox(
            constraints: const BoxConstraints(maxWidth: 420),
            child: panel,
          ),
        ),
      );
    }
  }

  /// 洞察面板：surprising connections + knowledge gaps（纯结构启发式，
  /// 后端零 LLM）。手机 bottom sheet / 桌面 dialog。定位回调关面板后
  /// 高亮 + 居中画布节点。
  void _showInsights() {
    final panel = _InsightsPanel(
      projectId: widget.projectId,
      initialDismissedKeys: _dismissedInsightKeys,
      onDismiss: (key) => _dismissedInsightKeys.add(key),
      onFocusNodes: _focusNodesOnCanvas,
    );
    if (isPhoneLayout(context)) {
      showModalBottomSheet<void>(
        context: context,
        isScrollControlled: true,
        showDragHandle: true,
        builder: (sheetCtx) => SizedBox(
          height: MediaQuery.sizeOf(sheetCtx).height * 0.75,
          child: panel,
        ),
      );
    } else {
      showDialog<void>(
        context: context,
        builder: (_) => Dialog(
          child: ConstrainedBox(
            constraints: const BoxConstraints(maxWidth: 560, maxHeight: 640),
            child: panel,
          ),
        ),
      );
    }
  }

  void _focusNodesOnCanvas(Set<String> ids) {
    Navigator.of(context).pop(); // 关掉洞察面板
    if (ids.isEmpty) return;
    setState(() {
      _focusIds = ids;
      _focusNonce++;
    });
  }

  void _clearFocus() {
    if (_focusIds.isEmpty) return;
    setState(() => _focusIds = const {});
  }
}

// ─── 洞察面板 ─────────────────────────────────────────────────────

/// 洞察面板内容 — 手机 bottom sheet / 桌面 dialog 复用同一份。
/// Riverpod 拉 _insightsProvider，渲染 surprising connections（跨社区/
/// 跨类型/外缘枢纽/弱边）+ knowledge gaps（孤立页/稀疏社区/桥节点）。
/// 点节点标题跳页；⌖ 定位 = 关面板 + 画布高亮居中；× = 逐条 dismiss（本地态）。
class _InsightsPanel extends ConsumerStatefulWidget {
  const _InsightsPanel({
    required this.projectId,
    required this.initialDismissedKeys,
    required this.onDismiss,
    required this.onFocusNodes,
  });

  final String projectId;
  final Set<String> initialDismissedKeys;
  final ValueChanged<String> onDismiss;
  final ValueChanged<Set<String>> onFocusNodes;

  @override
  ConsumerState<_InsightsPanel> createState() => _InsightsPanelState();
}

class _InsightsPanelState extends ConsumerState<_InsightsPanel> {
  /// dismiss 集在面板内自持（showDialog/showModalBottomSheet 是新 route，
  /// 外层 setState 不会重建这里），同时回写外层让面板重开后仍生效。
  late final Set<String> _dismissed = {...widget.initialDismissedKeys};

  void _dismiss(String key) {
    setState(() => _dismissed.add(key));
    widget.onDismiss(key);
  }

  @override
  Widget build(BuildContext context) {
    final async = ref.watch(_insightsProvider(widget.projectId));
    return Padding(
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
                projectId: widget.projectId,
                dismissed: _dismissed,
                onDismiss: _dismiss,
                onFocusNodes: widget.onFocusNodes,
              ),
            ),
          ),
        ],
      ),
    );
  }
}

class _InsightsBody extends StatelessWidget {
  const _InsightsBody({
    required this.insights,
    required this.projectId,
    required this.dismissed,
    required this.onDismiss,
    required this.onFocusNodes,
  });

  final WikiInsights insights;
  final String projectId;
  final Set<String> dismissed;
  final ValueChanged<String> onDismiss;
  final ValueChanged<Set<String>> onFocusNodes;

  @override
  Widget build(BuildContext context) {
    final s = insights.stats;
    final surprising = [
      for (final c in insights.surprising)
        if (!dismissed.contains(c.key)) c,
    ];
    final gaps = [
      for (final g in insights.gaps)
        if (!dismissed.contains(gapDismissKey(g))) g,
    ];
    final empty = surprising.isEmpty && gaps.isEmpty;
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
          if (surprising.isNotEmpty) ...[
            _SectionHeader(icon: Icons.bolt_outlined, title: '意外连接'),
            for (final c in surprising)
              _SurprisingCard(
                conn: c,
                projectId: projectId,
                onLocate: () =>
                    onFocusNodes({c.source.id, c.target.id}),
                onDismiss: () => onDismiss(c.key),
              ),
          ],
          if (gaps.isNotEmpty) ...[
            const SizedBox(height: 8),
            _SectionHeader(icon: Icons.lightbulb_outline, title: '知识缺口'),
            for (final g in gaps)
              _GapCard(
                gap: g,
                projectId: projectId,
                onLocate: g.nodeIds.isEmpty
                    ? null
                    : () => onFocusNodes(g.nodeIds.toSet()),
                onDismiss: () => onDismiss(gapDismissKey(g)),
              ),
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

/// 卡片右上角的小图标按钮（定位 / dismiss）。
class _CardAction extends StatelessWidget {
  const _CardAction({
    required this.tooltip,
    required this.icon,
    required this.onPressed,
  });
  final String tooltip;
  final IconData icon;
  final VoidCallback onPressed;

  @override
  Widget build(BuildContext context) {
    return IconButton(
      tooltip: tooltip,
      onPressed: onPressed,
      icon: Icon(icon, size: 14, color: BiuTokens.textMuted),
      padding: EdgeInsets.zero,
      constraints: const BoxConstraints(minWidth: 24, minHeight: 24),
      visualDensity: VisualDensity.compact,
    );
  }
}

class _SurprisingCard extends StatelessWidget {
  const _SurprisingCard({
    required this.conn,
    required this.projectId,
    required this.onLocate,
    required this.onDismiss,
  });
  final WikiSurprisingConnection conn;
  final String projectId;
  final VoidCallback onLocate;
  final VoidCallback onDismiss;

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
              Flexible(child: _NodeLink(brief: conn.source, projectId: projectId)),
              Padding(
                padding: const EdgeInsets.symmetric(horizontal: 6),
                child: Icon(Icons.link,
                    size: 12, color: BiuTokens.textMuted),
              ),
              Flexible(child: _NodeLink(brief: conn.target, projectId: projectId)),
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
              _CardAction(
                tooltip: '在图谱中定位',
                icon: Icons.center_focus_strong,
                onPressed: onLocate,
              ),
              _CardAction(
                tooltip: '不再显示',
                icon: Icons.close,
                onPressed: onDismiss,
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
  const _GapCard({
    required this.gap,
    required this.projectId,
    required this.onLocate,
    required this.onDismiss,
  });
  final WikiKnowledgeGap gap;
  final String projectId;

  /// 孤立页 / 稀疏聚类有节点集可定位；未知类型兜底无定位按钮。
  final VoidCallback? onLocate;
  final VoidCallback onDismiss;

  @override
  Widget build(BuildContext context) {
    final (icon, color) = _gapVisual(gap.type);
    // 缺口文案客户端中文化（后端英文硬编码，payload 的 type/node_ids 足够
    // 重新生成，见 application/graph_filters.dart）。
    final title = gapTitleZh(gap);
    final description = gapDescriptionZh(gap);
    final suggestion = gapSuggestionZh(gap);
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
                child: Text(title,
                    style: TextStyle(
                      color: BiuTokens.text,
                      fontSize: 12,
                      fontWeight: FontWeight.w600,
                    )),
              ),
              if (onLocate != null)
                _CardAction(
                  tooltip: '在图谱中定位',
                  icon: Icons.center_focus_strong,
                  onPressed: onLocate!,
                ),
              _CardAction(
                tooltip: '不再显示',
                icon: Icons.close,
                onPressed: onDismiss,
              ),
            ],
          ),
          const SizedBox(height: 4),
          Text(description,
              style:
                  TextStyle(fontSize: 11, color: BiuTokens.textSecondary)),
          if (suggestion.isNotEmpty) ...[
            const SizedBox(height: 4),
            Text('建议：$suggestion',
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
                initialTopic: title,
                initialQueries: [
                  if (description.isNotEmpty) description,
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
        return (Icons.circle_outlined, NamedPalette.amber);
      case 'sparse-community':
        return (Icons.scatter_plot_outlined, BiuTokens.purple);
      case 'bridge-node':
        return (Icons.account_tree_outlined, NamedPalette.teal);
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
        maxLines: 1,
        overflow: TextOverflow.ellipsis,
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

// ─── 过滤面板 ─────────────────────────────────────────────────────

/// 过滤器面板 — 结构页 / 孤立页开关 + page_type 多选 + 度数区间。
/// 面板自持状态、每次变更回调外层即时生效（画布布局随过滤重跑）。
class _FilterPanel extends StatefulWidget {
  const _FilterPanel({
    required this.initialState,
    required this.availableTypes,
    required this.maxDegree,
    required this.onChanged,
  });

  final GraphFilterState initialState;

  /// 图谱里出现过的 page_type（'' = 无类型），已排序。
  final List<String> availableTypes;

  /// 当前图谱最大度数（RangeSlider 上界）。
  final int maxDegree;

  final ValueChanged<GraphFilterState> onChanged;

  @override
  State<_FilterPanel> createState() => _FilterPanelState();
}

class _FilterPanelState extends State<_FilterPanel> {
  late GraphFilterState _state = widget.initialState;

  void _update(GraphFilterState next) {
    setState(() => _state = next);
    widget.onChanged(next);
  }

  @override
  Widget build(BuildContext context) {
    final minDeg = _state.minDegree ?? 0;
    final maxDeg = _state.maxDegree ?? widget.maxDegree;
    return Padding(
      padding: const EdgeInsets.fromLTRB(20, 8, 20, 20),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Row(
            children: [
              Icon(Icons.filter_list,
                  size: 16, color: BiuTokens.textSecondary),
              const SizedBox(width: 8),
              Text('过滤',
                  style: TextStyle(
                    color: BiuTokens.text,
                    fontSize: 14,
                    fontWeight: FontWeight.w600,
                  )),
              const Spacer(),
              TextButton(
                onPressed: _state.isActive
                    ? () => _update(const GraphFilterState())
                    : null,
                child: const Text('重置', style: TextStyle(fontSize: 12)),
              ),
            ],
          ),
          SwitchListTile(
            dense: true,
            contentPadding: EdgeInsets.zero,
            title: const Text('隐藏结构页', style: TextStyle(fontSize: 13)),
            subtitle: Text('index / overview / log 等导航页',
                style: TextStyle(fontSize: 11, color: BiuTokens.textMuted)),
            value: _state.hideStructural,
            onChanged: (v) => _update(_state.copyWith(hideStructural: v)),
          ),
          SwitchListTile(
            dense: true,
            contentPadding: EdgeInsets.zero,
            title: const Text('隐藏孤立页', style: TextStyle(fontSize: 13)),
            subtitle: Text('没有任何连接的节点',
                style: TextStyle(fontSize: 11, color: BiuTokens.textMuted)),
            value: _state.hideIsolated,
            onChanged: (v) => _update(_state.copyWith(hideIsolated: v)),
          ),
          if (widget.availableTypes.isNotEmpty) ...[
            const SizedBox(height: 8),
            Text('页面类型（点击切换显示）',
                style:
                    TextStyle(fontSize: 12, color: BiuTokens.textSecondary)),
            const SizedBox(height: 6),
            Wrap(
              spacing: 6,
              runSpacing: 4,
              children: [
                for (final t in widget.availableTypes)
                  _TypeFilterChip(
                    type: t,
                    visible: !_state.hiddenTypes.contains(t),
                    onToggle: (visible) {
                      final next = {..._state.hiddenTypes};
                      if (visible) {
                        next.remove(t);
                      } else {
                        next.add(t);
                      }
                      _update(_state.copyWith(hiddenTypes: next));
                    },
                  ),
              ],
            ),
          ],
          if (widget.maxDegree > 0) ...[
            const SizedBox(height: 12),
            Text('连接数区间：$minDeg – $maxDeg',
                style:
                    TextStyle(fontSize: 12, color: BiuTokens.textSecondary)),
            RangeSlider(
              values: RangeValues(minDeg.toDouble(),
                  math.min(maxDeg, widget.maxDegree).toDouble()),
              min: 0,
              max: widget.maxDegree.toDouble(),
              divisions: widget.maxDegree,
              labels: RangeLabels('$minDeg', '$maxDeg'),
              onChanged: (v) => _update(_state.copyWith(
                    minDegree: () =>
                        v.start.round() <= 0 ? null : v.start.round(),
                    maxDegree: () => v.end.round() >= widget.maxDegree
                        ? null
                        : v.end.round(),
                  )),
            ),
          ],
        ],
      ),
    );
  }
}

class _TypeFilterChip extends StatelessWidget {
  const _TypeFilterChip({
    required this.type,
    required this.visible,
    required this.onToggle,
  });

  /// page_type；'' = 无类型节点。
  final String type;
  final bool visible;
  final ValueChanged<bool> onToggle;

  @override
  Widget build(BuildContext context) {
    final config = pageTypeConfigOf(type.isEmpty ? null : type);
    return FilterChip(
      selected: visible,
      showCheckmark: false,
      onSelected: onToggle,
      label: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          Container(
            width: 8,
            height: 8,
            decoration: BoxDecoration(
              color: config.color,
              shape: BoxShape.circle,
            ),
          ),
          const SizedBox(width: 6),
          Text(type.isEmpty ? '无类型' : config.label,
              style: const TextStyle(fontSize: 12)),
        ],
      ),
    );
  }
}

// ─── Toolbar ──────────────────────────────────────────────────────

class _Toolbar extends StatelessWidget {
  const _Toolbar({
    required this.recomputing,
    required this.searchQuery,
    required this.themeIndex,
    required this.filterActive,
    required this.colorMode,
    required this.onRefresh,
    required this.onRecompute,
    required this.onSearchChanged,
    required this.onThemeChanged,
    required this.onColorModeChanged,
    required this.onFilters,
    required this.onInsights,
  });
  final bool recomputing;
  final String searchQuery;
  final int themeIndex;
  final bool filterActive;
  final GraphColorMode colorMode;
  final VoidCallback onRefresh;
  final VoidCallback? onRecompute;
  final ValueChanged<String> onSearchChanged;
  final ValueChanged<int> onThemeChanged;
  final ValueChanged<GraphColorMode> onColorModeChanged;
  final VoidCallback onFilters;
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
          // 过滤器：结构页 / 孤立页 / 类型 / 度数区间
          IconButton(
            tooltip: '过滤',
            onPressed: onFilters,
            icon: Badge(
              isLabelVisible: filterActive,
              smallSize: 6,
              backgroundColor: BiuTokens.purple,
              child: const Icon(Icons.filter_list, size: 16),
            ),
            padding: EdgeInsets.zero,
            constraints: const BoxConstraints(minWidth: 32, minHeight: 32),
          ),
          const SizedBox(width: 4),
          // 着色模式：按聚类 ⇄ 按类型
          IconButton(
            tooltip: colorMode == GraphColorMode.community
                ? '按聚类着色（切换为按类型）'
                : '按类型着色（切换为按聚类）',
            onPressed: () => onColorModeChanged(
              colorMode == GraphColorMode.community
                  ? GraphColorMode.type
                  : GraphColorMode.community,
            ),
            icon: Icon(
              colorMode == GraphColorMode.community
                  ? Icons.groups_outlined
                  : Icons.category_outlined,
              size: 16,
            ),
            padding: EdgeInsets.zero,
            constraints: const BoxConstraints(minWidth: 32, minHeight: 32),
          ),
          const SizedBox(width: 4),
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
    required this.colorMode,
    required this.filterState,
    required this.focusIds,
    required this.focusNonce,
    required this.onClearFocus,
  });
  final WikiGraphData data;
  final String projectId;
  final String searchQuery;
  final List<Color> palette;
  final GraphColorMode colorMode;
  final GraphFilterState filterState;

  /// 洞察定位要高亮 + 居中的节点；nonce 变化即触发一次居中。
  final Set<String> focusIds;
  final int focusNonce;
  final VoidCallback onClearFocus;

  @override
  State<_GraphCanvas> createState() => _GraphCanvasState();
}

class _GraphCanvasState extends State<_GraphCanvas> {
  /// 一次性 layout 后缓存的节点 (id → Offset)。recompute / 过滤变化时重跑。
  late Map<String, Offset> _positions;
  late Size _layoutSize;

  /// 过滤后的可见节点 / 边 — 布局与渲染都只吃这份。
  List<WikiGraphNode> _visibleNodes = const [];
  List<WikiGraphEdge> _visibleEdges = const [];

  String? _hoverId;
  String? _selectedId;
  final TransformationController _transform = TransformationController();

  /// LayoutBuilder 拿到的视口尺寸，定位居中时算平移量用。
  Size _viewportSize = Size.zero;

  static const double _nodeRadius = 10;

  @override
  void initState() {
    super.initState();
    _applyFiltersAndLayout();
  }

  @override
  void didUpdateWidget(covariant _GraphCanvas old) {
    super.didUpdateWidget(old);
    if (old.data != widget.data || old.filterState != widget.filterState) {
      _applyFiltersAndLayout();
    }
    if (old.focusNonce != widget.focusNonce && widget.focusIds.isNotEmpty) {
      // 等本帧布局完再算视口居中。
      WidgetsBinding.instance.addPostFrameCallback((_) => _centerOnFocus());
    }
  }

  @override
  void dispose() {
    _transform.dispose();
    super.dispose();
  }

  void _applyFiltersAndLayout() {
    final filtered = applyGraphFilters(widget.data, widget.filterState);
    _visibleNodes = filtered.nodes;
    _visibleEdges = filtered.edges;
    _runLayout();
  }

  /// 洞察定位：把 focus 节点集合的质心平移到视口中心并适度放大。
  void _centerOnFocus() {
    if (!mounted) return;
    final pts = [
      for (final id in widget.focusIds) ?_positions[id],
    ];
    if (pts.isEmpty || _viewportSize == Size.zero) return;
    var cx = 0.0, cy = 0.0;
    for (final p in pts) {
      cx += p.dx;
      cy += p.dy;
    }
    final centroid = Offset(cx / pts.length, cy / pts.length);
    const scale = 1.2;
    _transform.value = Matrix4.identity()
      ..translateByDouble(
        _viewportSize.width / 2 - centroid.dx * scale,
        _viewportSize.height / 2 - centroid.dy * scale,
        0,
        1,
      )
      ..scaleByDouble(scale, scale, scale, 1);
  }

  /// 一次性 force-directed simulation：200 次迭代收敛后缓存结果。
  /// 节点数 << 1k 的项目跑完瞬时（< 50ms），不需要每帧动画。
  void _runLayout() {
    final nodes = _visibleNodes;
    final edges = _visibleEdges;

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
    // 空白处点按 = 清除洞察定位高亮。
    widget.onClearFocus();
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
    return LayoutBuilder(
      builder: (context, constraints) {
        _viewportSize = Size(constraints.maxWidth, constraints.maxHeight);
        // 过滤把所有节点都滤掉了：给提示而不是一块空画布。
        if (_visibleNodes.isEmpty) {
          return Center(
            child: Text(
              '当前过滤条件下没有可见节点',
              style: TextStyle(color: BiuTokens.textMuted, fontSize: 12),
            ),
          );
        }
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
                    nodes: _visibleNodes,
                    edges: _visibleEdges,
                    positions: _positions,
                    hoverId: _hoverId,
                    selectedId: _selectedId,
                    nodeRadius: _nodeRadius,
                    searchQuery: widget.searchQuery.toLowerCase().trim(),
                    palette: widget.palette,
                    colorMode: widget.colorMode,
                    focusIds: widget.focusIds,
                  ),
                ),
              ),
            ),
          ),
        );
      },
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
    required this.colorMode,
    required this.focusIds,
  });

  final List<WikiGraphNode> nodes;
  final List<WikiGraphEdge> edges;
  final Map<String, Offset> positions;
  final String? hoverId;
  final String? selectedId;
  final double nodeRadius;
  final String searchQuery;
  final List<Color> palette;
  final GraphColorMode colorMode;
  final Set<String> focusIds;

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
      // 洞察定位的边（两端都在 focus 集）也走高亮。
      final highlighted = hoverId == e.source ||
          hoverId == e.target ||
          selectedId == e.source ||
          selectedId == e.target ||
          (focusIds.contains(e.source) && focusIds.contains(e.target));
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
      final isFocus = focusIds.contains(node.id);
      final isDimmed = searching && !isHit;
      final color = _nodeColor(node);
      // 命中 / 定位节点：半径放大 1.4×；其他节点：搜索时缩小
      var r = nodeRadius * (1 + math.log(1 + node.weight) * 0.2);
      if (isHit || isFocus) r *= 1.4;
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

      // 搜索命中 / 洞察定位：金色脉冲圈
      if (isHit || isFocus) {
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

      // label：hover/selected 显示；搜索命中 / 洞察定位也强制显示
      if (isHover || isSelected || isHit || isFocus) {
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

  /// 节点着色：community 模式走外部调色板（Louvain id 取模）；
  /// type 模式走 page_type 语义色（page_type_config.dart，与项目浏览器一致）。
  Color _nodeColor(WikiGraphNode node) {
    if (colorMode == GraphColorMode.type) {
      return pageTypeConfigOf(node.pageType).color;
    }
    if (palette.isEmpty) return BiuTokens.textMuted;
    return palette[node.community.abs() % palette.length];
  }

  @override
  bool shouldRepaint(covariant _GraphPainter old) {
    return old.nodes != nodes ||
        old.edges != edges ||
        old.positions != positions ||
        old.hoverId != hoverId ||
        old.selectedId != selectedId ||
        old.searchQuery != searchQuery ||
        old.palette != palette ||
        old.colorMode != colorMode ||
        old.focusIds != focusIds;
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

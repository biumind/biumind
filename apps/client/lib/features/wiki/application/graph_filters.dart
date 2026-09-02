/// 图谱页纯逻辑 — 节点过滤 / 度数计算 / 结构页判定 / 缺口文案中文化。
///
/// 与 `project_graph_page.dart` 分离，保持无 Flutter 依赖可直接单测。
/// 思路对齐 reference/llm_wiki `graph-filters.ts`（We Take：隐藏结构页 /
/// 孤立页 / 类型多选 / 度数区间 + 边随节点收敛；We Skip：hiddenNodeIds
/// 单点隐藏——画布已支持点击跳页，单点隐藏价值低）。
library;

import '../../../data/api/wiki_client.dart';

/// 节点着色模式：按 Louvain 聚类 / 按 page_type。
enum GraphColorMode { community, type }

/// 过滤器状态（不可变）。默认隐藏结构页（index/overview/log 这类导航页
/// 连接到一切，会淹没拓扑信号，与后端 insights 的 isStructural 豁免一致）。
class GraphFilterState {
  const GraphFilterState({
    this.hideStructural = true,
    this.hideIsolated = false,
    this.hiddenTypes = const {},
    this.minDegree,
    this.maxDegree,
  });

  final bool hideStructural;

  /// 隐藏孤立页（在当前边集里度数 = 0 的节点）。
  final bool hideIsolated;

  /// 被隐藏的 page_type 集合（空串 key = 无类型节点）。
  final Set<String> hiddenTypes;

  /// 度数下界（含）。null = 不限。
  final int? minDegree;

  /// 度数上界（含）。null = 不限。
  final int? maxDegree;

  bool get isActive =>
      hideStructural ||
      hideIsolated ||
      hiddenTypes.isNotEmpty ||
      minDegree != null ||
      maxDegree != null;

  GraphFilterState copyWith({
    bool? hideStructural,
    bool? hideIsolated,
    Set<String>? hiddenTypes,
    int? Function()? minDegree,
    int? Function()? maxDegree,
  }) {
    return GraphFilterState(
      hideStructural: hideStructural ?? this.hideStructural,
      hideIsolated: hideIsolated ?? this.hideIsolated,
      hiddenTypes: hiddenTypes ?? this.hiddenTypes,
      minDegree: minDegree != null ? minDegree() : this.minDegree,
      maxDegree: maxDegree != null ? maxDegree() : this.maxDegree,
    );
  }

  @override
  bool operator ==(Object other) =>
      other is GraphFilterState &&
      other.hideStructural == hideStructural &&
      other.hideIsolated == hideIsolated &&
      other.minDegree == minDegree &&
      other.maxDegree == maxDegree &&
      other.hiddenTypes.length == hiddenTypes.length &&
      other.hiddenTypes.containsAll(hiddenTypes);

  @override
  int get hashCode => Object.hash(
        hideStructural,
        hideIsolated,
        minDegree,
        maxDegree,
        Object.hashAllUnordered(hiddenTypes),
      );
}

/// 结构页判定 — 镜像后端 `graph/store.go::isStructural`：
/// page_type = overview，或标题（小写去空白）∈ {index, log, overview}。
bool isStructuralGraphNode(WikiGraphNode node) {
  if (node.pageType == 'overview') return true;
  const structuralTitles = {'index', 'log', 'overview'};
  return structuralTitles.contains(node.title.trim().toLowerCase());
}

/// 节点度数表 — 从**边集**计数（而非节点 weight 字段），保证「孤立页」
/// 与画布上实际画出的边一致。
Map<String, int> computeNodeDegrees(
  List<WikiGraphNode> nodes,
  List<WikiGraphEdge> edges,
) {
  final degrees = {for (final n in nodes) n.id: 0};
  for (final e in edges) {
    if (degrees.containsKey(e.source)) {
      degrees[e.source] = degrees[e.source]! + 1;
    }
    if (degrees.containsKey(e.target)) {
      degrees[e.target] = degrees[e.target]! + 1;
    }
  }
  return degrees;
}

/// 过滤结果：可见节点 / 可见边（两端都可见）/ 被隐藏节点数。
class FilteredGraphData {
  const FilteredGraphData({
    required this.nodes,
    required this.edges,
    required this.hiddenCount,
  });

  final List<WikiGraphNode> nodes;
  final List<WikiGraphEdge> edges;
  final int hiddenCount;
}

/// 应用过滤器：节点按结构页 / 孤立页 / 类型 / 度数区间收敛，
/// 边随节点收敛（任一端被隐藏则边不渲染也不参与布局）。
FilteredGraphData applyGraphFilters(
  WikiGraphData data,
  GraphFilterState filters,
) {
  if (!filters.isActive) {
    return FilteredGraphData(
      nodes: data.nodes,
      edges: data.edges,
      hiddenCount: 0,
    );
  }
  final degrees = computeNodeDegrees(data.nodes, data.edges);
  final visible = <WikiGraphNode>[];
  var hidden = 0;
  for (final n in data.nodes) {
    final degree = degrees[n.id] ?? 0;
    final hide = (filters.hideStructural && isStructuralGraphNode(n)) ||
        (filters.hideIsolated && degree == 0) ||
        filters.hiddenTypes.contains(n.pageType ?? '') ||
        (filters.minDegree != null && degree < filters.minDegree!) ||
        (filters.maxDegree != null && degree > filters.maxDegree!);
    if (hide) {
      hidden++;
    } else {
      visible.add(n);
    }
  }
  final visibleIds = visible.map((n) => n.id).toSet();
  final edges = data.edges
      .where((e) => visibleIds.contains(e.source) && visibleIds.contains(e.target))
      .toList();
  return FilteredGraphData(nodes: visible, edges: edges, hiddenCount: hidden);
}

/// 缺口 dismiss key — 后端 Gap 没有稳定 key（Surprising 才有 `key` 字段），
/// 客户端用 type|title 合成。同一批 insights 内 type+title 唯一。
String gapDismissKey(WikiKnowledgeGap gap) => '${gap.type}|${gap.title}';

// ─── 知识缺口文案中文化 ────────────────────────────────────────────
//
// 后端 insights.go 的 title / description / suggestion 是英文硬编码
// （服务端不做 i18n）。payload 的 type / node_ids 足够客户端重新生成
// 中文文案；稀疏聚类 / 桥接页的「名字」嵌在后端 title 里，按已知前缀
// 剥出，剥不掉就原样展示（新类型兜底后端原文）。

String _stripPrefix(String title, String prefix) {
  if (title.startsWith(prefix)) {
    final rest = title.substring(prefix.length).trim();
    if (rest.isNotEmpty) return rest;
  }
  return title;
}

/// 缺口标题（中文）。isolated-node 的数量取 nodeIds 全长（后端 title
/// 里的数字与此一致，但客户端不解析英文文案里的数字）。
String gapTitleZh(WikiKnowledgeGap gap) {
  switch (gap.type) {
    case 'isolated-node':
      return '孤立页面（${gap.nodeIds.length} 个）';
    case 'sparse-community':
      return '稀疏聚类：${_stripPrefix(gap.title, 'Sparse cluster:')}';
    case 'bridge-node':
      return '关键桥接页：${_stripPrefix(gap.title, 'Key bridge:')}';
    default:
      return gap.title;
  }
}

/// 缺口描述（中文）。孤立页的示例页面已由卡片下方 node chips 展示，
/// 描述不再重复罗列标题。
String gapDescriptionZh(WikiKnowledgeGap gap) {
  switch (gap.type) {
    case 'isolated-node':
      return '这些页面连接数 ≤ 1，几乎没有与其他页面建立关联。';
    case 'sparse-community':
      return '该聚类共 ${gap.nodeIds.length} 个页面，内部连接很弱（凝聚力低于阈值）。';
    case 'bridge-node':
      return '该页面连接多个知识聚类，是整个 wiki 的关键枢纽。';
    default:
      return gap.description;
  }
}

/// 缺口建议（中文），对应后端 insights.go 三处英文硬编码 suggestion。
String gapSuggestionZh(WikiKnowledgeGap gap) {
  switch (gap.type) {
    case 'isolated-node':
      return '考虑为相关页面添加 [[wikilink]] 引用，或通过「研究」扩展这些页面的内容。';
    case 'sparse-community':
      return '该知识领域缺少内部交叉引用，考虑在这些页面之间补充链接，或通过「研究」补全。';
    case 'bridge-node':
      return '请确保该页面内容完善——若内容单薄，扩展它将增强整个 wiki 的连通性。';
    default:
      return gap.suggestion;
  }
}

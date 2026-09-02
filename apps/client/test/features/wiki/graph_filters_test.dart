import 'package:biumind/data/api/wiki_client.dart';
import 'package:biumind/features/wiki/application/graph_filters.dart';
import 'package:flutter_test/flutter_test.dart';

WikiGraphNode _node(
  String id, {
  String title = '',
  String? pageType,
  int community = 0,
  double weight = 1,
}) =>
    WikiGraphNode(
      id: id,
      title: title.isEmpty ? id : title,
      pageType: pageType,
      community: community,
      weight: weight,
    );

WikiGraphEdge _edge(String a, String b) =>
    WikiGraphEdge(source: a, target: b, weight: 1);

WikiKnowledgeGap _gap({
  required String type,
  String title = '',
  String description = 'desc',
  List<String> nodeIds = const [],
  String suggestion = 'sug',
}) =>
    WikiKnowledgeGap(
      type: type,
      title: title,
      description: description,
      nodeIds: nodeIds,
      suggestion: suggestion,
    );

void main() {
  group('isStructuralGraphNode', () {
    test('overview page_type is structural', () {
      expect(
        isStructuralGraphNode(_node('a', title: 'Anything', pageType: 'overview')),
        isTrue,
      );
    });

    test('index / log / overview titles are structural (case-insensitive)', () {
      for (final t in ['index', 'Index', ' LOG ', 'overview']) {
        expect(isStructuralGraphNode(_node('a', title: t)), isTrue,
            reason: t);
      }
    });

    test('normal pages are not structural', () {
      expect(
        isStructuralGraphNode(_node('a', title: 'Transformer', pageType: 'concept')),
        isFalse,
      );
      expect(isStructuralGraphNode(_node('a', title: 'indexer')), isFalse);
    });
  });

  group('computeNodeDegrees', () {
    test('counts both endpoints; isolated node has degree 0', () {
      final degrees = computeNodeDegrees(
        [_node('a'), _node('b'), _node('c')],
        [_edge('a', 'b'), _edge('a', 'b')],
      );
      expect(degrees, {'a': 2, 'b': 2, 'c': 0});
    });

    test('edges referencing unknown nodes are ignored', () {
      final degrees = computeNodeDegrees([_node('a')], [_edge('a', 'ghost')]);
      expect(degrees, {'a': 1});
    });
  });

  group('GraphFilterState', () {
    test('defaults: only hideStructural active', () {
      const f = GraphFilterState();
      expect(f.hideStructural, isTrue);
      expect(f.isActive, isTrue);
    });

    test('copyWith updates and clears nullable degree bounds', () {
      const f = GraphFilterState();
      final withRange = f.copyWith(minDegree: () => 2, maxDegree: () => 5);
      expect(withRange.minDegree, 2);
      expect(withRange.maxDegree, 5);
      final cleared = withRange.copyWith(minDegree: () => null);
      expect(cleared.minDegree, isNull);
      expect(cleared.maxDegree, 5);
    });

    test('equality ignores hiddenTypes ordering', () {
      final a = GraphFilterState(hiddenTypes: {'x', 'y'});
      final b = GraphFilterState(hiddenTypes: {'y', 'x'});
      expect(a, equals(b));
      expect(a.hashCode, b.hashCode);
      expect(a == const GraphFilterState(), isFalse);
    });
  });

  group('applyGraphFilters', () {
    final nodes = [
      _node('idx', title: 'index', pageType: 'overview'),
      _node('a', pageType: 'concept'),
      _node('b', pageType: 'concept'),
      _node('c', pageType: 'entity'),
      _node('d'), // 无类型 + 孤立
    ];
    final edges = [_edge('idx', 'a'), _edge('a', 'b'), _edge('b', 'c')];
    final data = WikiGraphData(nodes: nodes, edges: edges);

    test('inactive filter returns original data untouched', () {
      final out = applyGraphFilters(
        data,
        const GraphFilterState(hideStructural: false),
      );
      expect(out.nodes, same(nodes));
      expect(out.edges, same(edges));
      expect(out.hiddenCount, 0);
    });

    test('hideStructural removes structural node and its edges', () {
      final out = applyGraphFilters(data, const GraphFilterState());
      expect(out.nodes.map((n) => n.id), ['a', 'b', 'c', 'd']);
      expect(out.edges.map((e) => '${e.source}-${e.target}'),
          ['a-b', 'b-c']);
      expect(out.hiddenCount, 1);
    });

    test('hideIsolated removes degree-0 nodes', () {
      final out = applyGraphFilters(
        data,
        const GraphFilterState(hideStructural: false, hideIsolated: true),
      );
      expect(out.nodes.map((n) => n.id), ['idx', 'a', 'b', 'c']);
      expect(out.hiddenCount, 1);
    });

    test('hiddenTypes filters by page_type; empty key matches untyped', () {
      final out = applyGraphFilters(
        data,
        const GraphFilterState(
            hideStructural: false, hiddenTypes: {'concept', ''}),
      );
      expect(out.nodes.map((n) => n.id), ['idx', 'c']);
    });

    test('degree range is inclusive on both ends', () {
      // 度数：idx=1, a=2, b=2, c=1, d=0
      final out = applyGraphFilters(
        data,
        const GraphFilterState(
            hideStructural: false, minDegree: 1, maxDegree: 1),
      );
      expect(out.nodes.map((n) => n.id), ['idx', 'c']);
      // idx-a 和 b-c 都只剩一端可见，边全部收敛
      expect(out.edges, isEmpty);
    });
  });

  group('gapDismissKey', () {
    test('combines type and title', () {
      expect(
        gapDismissKey(_gap(type: 'isolated-node', title: '3 isolated pages')),
        'isolated-node|3 isolated pages',
      );
    });
  });

  group('gap copy (中文映射)', () {
    test('isolated-node: title uses nodeIds count', () {
      final gap = _gap(
        type: 'isolated-node',
        title: '3 isolated pages',
        nodeIds: const ['a', 'b', 'c'],
      );
      expect(gapTitleZh(gap), '孤立页面（3 个）');
      expect(gapDescriptionZh(gap), contains('连接数'));
      expect(gapSuggestionZh(gap), contains('wikilink'));
      // 不含英文原文
      expect(gapTitleZh(gap), isNot(contains('isolated page')));
    });

    test('sparse-community: strips English prefix, keeps cluster name', () {
      final gap = _gap(
        type: 'sparse-community',
        title: 'Sparse cluster: 机器学习',
        nodeIds: const ['a', 'b', 'c', 'd'],
      );
      expect(gapTitleZh(gap), '稀疏聚类：机器学习');
      expect(gapDescriptionZh(gap), contains('4 个页面'));
      expect(gapSuggestionZh(gap), contains('交叉引用'));
    });

    test('bridge-node: strips English prefix, keeps page title', () {
      final gap = _gap(type: 'bridge-node', title: 'Key bridge: 总览页');
      expect(gapTitleZh(gap), '关键桥接页：总览页');
      expect(gapDescriptionZh(gap), contains('枢纽'));
      expect(gapSuggestionZh(gap), contains('连通性'));
    });

    test('title without known prefix falls back to raw title', () {
      final gap = _gap(type: 'bridge-node', title: 'Something else');
      expect(gapTitleZh(gap), '关键桥接页：Something else');
    });

    test('unknown gap type passes backend copy through', () {
      final gap = _gap(
        type: 'future-type',
        title: 'Raw title',
        description: 'Raw desc',
        suggestion: 'Raw sug',
      );
      expect(gapTitleZh(gap), 'Raw title');
      expect(gapDescriptionZh(gap), 'Raw desc');
      expect(gapSuggestionZh(gap), 'Raw sug');
    });
  });
}

// 笔记本树组装纯函数单测（PR4 多级目录 UI）。
//
// 覆盖：嵌套结构、同级 position 排序、悬空 parentId 提升为根、环防御
// （不丢节点、不死循环）、collapsed 拍平、notebookSubtreeIds（移动目标
// 排除自身与后代）。

import 'package:biumind/data/notes_repository.dart';
import 'package:biumind/features/notes/application/notebook_tree.dart';
import 'package:flutter_test/flutter_test.dart';

RepoNotebook _nb(String id, {String? parentId, double position = 0.0}) =>
    RepoNotebook(id: id, name: 'n-$id', parentId: parentId, position: position);

void main() {
  group('buildNotebookTree', () {
    test('嵌套结构：父子深度与归属正确', () {
      final roots = buildNotebookTree([
        _nb('root'),
        _nb('child', parentId: 'root'),
        _nb('grand', parentId: 'child'),
        _nb('other'),
      ]);
      expect(roots.map((n) => n.notebook.id), containsAll(['root', 'other']));
      final root = roots.firstWhere((n) => n.notebook.id == 'root');
      expect(root.depth, 0);
      expect(root.children.single.notebook.id, 'child');
      expect(root.children.single.depth, 1);
      expect(root.children.single.children.single.notebook.id, 'grand');
      expect(root.children.single.children.single.depth, 2);
    });

    test('同级按 position 排序', () {
      final roots = buildNotebookTree([
        _nb('p'),
        _nb('c2', parentId: 'p', position: 2),
        _nb('c1', parentId: 'p', position: 1),
        _nb('c3', parentId: 'p', position: 3),
      ]);
      expect(
        roots.single.children.map((n) => n.notebook.id),
        ['c1', 'c2', 'c3'],
      );
    });

    test('悬空 parentId（指向不存在的本子）按根级渲染，不丢节点', () {
      final roots = buildNotebookTree([
        _nb('orphan', parentId: 'ghost'),
        _nb('orphan-child', parentId: 'orphan'),
      ]);
      expect(roots, hasLength(1));
      expect(roots.single.notebook.id, 'orphan');
      expect(roots.single.depth, 0);
      expect(roots.single.children.single.notebook.id, 'orphan-child');
    });

    test('环防御：a↔b 互指不死循环、节点不丢', () {
      final roots = buildNotebookTree([
        _nb('a', parentId: 'b'),
        _nb('b', parentId: 'a'),
        _nb('normal'),
      ]);
      final ids = <String>{};
      void collect(List<NotebookTreeNode> nodes) {
        for (final n in nodes) {
          ids.add(n.notebook.id);
          collect(n.children);
        }
      }

      collect(roots);
      expect(ids, containsAll(['a', 'b', 'normal']));
    });
  });

  group('flattenNotebookTree', () {
    test('collapsed 集合裁剪子树，其余展开', () {
      final roots = buildNotebookTree([
        _nb('p1'),
        _nb('c1', parentId: 'p1'),
        _nb('p2'),
        _nb('c2', parentId: 'p2'),
      ]);
      expect(
        flattenNotebookTree(roots, const {})
            .map((n) => n.notebook.id),
        ['p1', 'c1', 'p2', 'c2'],
      );
      expect(
        flattenNotebookTree(roots, {'p1'}).map((n) => n.notebook.id),
        ['p1', 'p2', 'c2'],
        reason: 'p1 收起 → c1 不可见；p2 仍展开',
      );
    });
  });

  group('notebookSubtreeIds', () {
    test('返回自身 + 全部后代', () {
      final flat = [
        _nb('a'),
        _nb('b', parentId: 'a'),
        _nb('c', parentId: 'b'),
        _nb('d'),
      ];
      expect(notebookSubtreeIds(flat, 'a'), {'a', 'b', 'c'});
      expect(notebookSubtreeIds(flat, 'b'), {'b', 'c'});
      expect(notebookSubtreeIds(flat, 'd'), {'d'});
    });

    test('成环输入安全终止', () {
      final flat = [
        _nb('a', parentId: 'b'),
        _nb('b', parentId: 'a'),
      ];
      expect(notebookSubtreeIds(flat, 'a'), {'a', 'b'});
    });
  });
}

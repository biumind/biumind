// 笔记本多级目录 —— 树组装纯函数（UI 层与单测共用）。
//
// 数据层（PR3）给出的是按 position 排序的平铺列表（RepoNotebook.parentId），
// 树在 UI 侧组装。防御两条脏数据路径：
//   * 悬空 parentId（指向不存在的本子 —— 理论上服务端/本地迁移都保证不
//     发生，但本地镜像可能脏）→ 按根级渲染，不丢节点；
//   * 环（a→b→a）→ visited 集合剪枝；够不到根的成环节点提升为根级，
//     同样不丢节点。

import '../../../data/notes_repository.dart';

/// 树节点：notebook + 子节点（同级按 position 排序）+ 深度（根=0）。
class NotebookTreeNode {
  NotebookTreeNode({
    required this.notebook,
    required this.children,
    required this.depth,
  });

  final RepoNotebook notebook;
  final List<NotebookTreeNode> children;
  final int depth;
}

/// 按 parentId 分组：key 为 parentId（null = 根级），组内按 position 排序
/// （与平铺列表的排序语义一致；position 相等时按 name 稳定次序）。
Map<String?, List<RepoNotebook>> _groupByParent(List<RepoNotebook> flat) {
  final byParent = <String?, List<RepoNotebook>>{};
  for (final nb in flat) {
    byParent.putIfAbsent(nb.parentId, () => []).add(nb);
  }
  for (final group in byParent.values) {
    group.sort((a, b) {
      final c = a.position.compareTo(b.position);
      return c != 0 ? c : a.name.compareTo(b.name);
    });
  }
  return byParent;
}

/// 平铺列表 → 树（根级节点列表，深度 0 起）。
List<NotebookTreeNode> buildNotebookTree(List<RepoNotebook> flat) {
  final byParent = _groupByParent(flat);
  final ids = {for (final nb in flat) nb.id};
  final visited = <String>{};

  List<NotebookTreeNode> build(String? parentId, int depth) {
    final out = <NotebookTreeNode>[];
    for (final nb in byParent[parentId] ?? const <RepoNotebook>[]) {
      if (!visited.add(nb.id)) continue; // 防环剪枝
      out.add(NotebookTreeNode(
        notebook: nb,
        depth: depth,
        children: build(nb.id, depth + 1),
      ));
    }
    return out;
  }

  // 根级：parentId 为 null 或悬空（parent 不在列表里）的节点。
  final roots = build(null, 0);
  for (final entry in byParent.entries) {
    final pid = entry.key;
    if (pid == null || ids.contains(pid)) continue;
    for (final nb in entry.value) {
      if (!visited.add(nb.id)) continue;
      roots.add(NotebookTreeNode(
        notebook: nb,
        depth: 0,
        children: build(nb.id, 1),
      ));
    }
  }
  // 成环且够不到根的节点（visited 剪枝后仍剩）—— 提升为根级，不丢。
  for (final nb in flat) {
    if (visited.contains(nb.id)) continue;
    visited.add(nb.id);
    roots.add(NotebookTreeNode(
      notebook: nb,
      depth: 0,
      children: const [],
    ));
  }
  return roots;
}

/// 树 → 可见行序列（展开状态裁剪），供 ListView 渲染。
/// [collapsed] 为收起的节点 id 集合（默认全展开）。
List<NotebookTreeNode> flattenNotebookTree(
  List<NotebookTreeNode> roots,
  Set<String> collapsed,
) {
  final out = <NotebookTreeNode>[];
  void walk(List<NotebookTreeNode> nodes) {
    for (final n in nodes) {
      out.add(n);
      if (n.children.isNotEmpty && !collapsed.contains(n.notebook.id)) {
        walk(n.children);
      }
    }
  }

  walk(roots);
  return out;
}

/// 返回 [id] 自身 + 全部后代 id（成环安全）。「移动到…」目标选择用它
/// 排除自身与后代（服务端也会拒 ErrNotebookCycle，UI 先过滤掉）。
Set<String> notebookSubtreeIds(List<RepoNotebook> flat, String id) {
  final byParent = _groupByParent(flat);
  final out = <String>{};
  final queue = <String>[id];
  while (queue.isNotEmpty) {
    final cur = queue.removeLast();
    if (!out.add(cur)) continue; // 防环
    for (final child in byParent[cur] ?? const <RepoNotebook>[]) {
      queue.add(child.id);
    }
  }
  return out;
}

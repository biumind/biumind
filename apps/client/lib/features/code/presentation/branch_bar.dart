// BranchBar — 任务面板顶部的分支条(M1 显示 → CORE-5 可切换/新建)。
//
// 点击展开分支菜单:列本地分支(点选 → checkout)+「新建分支…」。切换/新建走
// daemon bridge(git.listBranches / git.checkoutBranch / git.createBranch,M4-A 已就绪)。
// 失败以 SnackBar 提示,不静默吞。非 git 目录 / daemon 未起时菜单给提示。

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../application/projects_controller.dart';
import '../data/code_bridge_provider.dart';
import '../domain/project.dart';

class BranchBar extends ConsumerStatefulWidget {
  const BranchBar({super.key, required this.project});

  final CodeProject project;

  @override
  ConsumerState<BranchBar> createState() => _BranchBarState();
}

class _BranchBarState extends ConsumerState<BranchBar> {
  bool _busy = false;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) => _refreshBranch());
  }

  Future<void> _refreshBranch() async {
    final client = ref.read(codeBridgeClientProvider);
    if (client == null) return;
    try {
      final status = await client.gitStatus(widget.project.path);
      final branch = status['branch'];
      if (branch is String && branch.isNotEmpty && mounted) {
        await ref
            .read(codeProjectsControllerProvider.notifier)
            .setBranch(widget.project.id, branch);
      }
    } catch (_) {
      // 非 git 目录 / daemon 错误:保持已有显示,不打断。
    }
  }

  Future<void> _openMenu(Offset pos) async {
    final client = ref.read(codeBridgeClientProvider);
    final messenger = ScaffoldMessenger.of(context);
    if (client == null) {
      messenger.showSnackBar(
        const SnackBar(content: Text('本地 daemon 未就绪,无法切换分支')),
      );
      return;
    }
    setState(() => _busy = true);
    List<String> locals = const [];
    try {
      final branches = await client.gitListBranches(widget.project.path);
      locals = branches.where((b) => !b.isRemote).map((b) => b.name).toList();
    } catch (e) {
      messenger.showSnackBar(SnackBar(content: Text('读取分支失败: $e')));
      if (mounted) setState(() => _busy = false);
      return;
    }
    if (mounted) setState(() => _busy = false);
    if (!mounted) return;

    final current = ref.read(activeCodeProjectProvider)?.branch;
    final sel = await showMenu<String>(
      context: context,
      position: RelativeRect.fromLTRB(pos.dx, pos.dy, pos.dx, pos.dy),
      items: [
        for (final b in locals)
          PopupMenuItem<String>(
            value: 'co:$b',
            child: Row(
              children: [
                Icon(
                  b == current
                      ? Icons.check_rounded
                      : Icons.account_tree_outlined,
                  size: 14,
                  color: b == current
                      ? Theme.of(context).colorScheme.primary
                      : Theme.of(context).colorScheme.onSurfaceVariant,
                ),
                const SizedBox(width: 8),
                Text(b, style: const TextStyle(fontSize: 13)),
              ],
            ),
          ),
        const PopupMenuDivider(),
        const PopupMenuItem<String>(
          value: 'new',
          child: Row(
            children: [
              Icon(Icons.add_rounded, size: 14),
              SizedBox(width: 8),
              Text('新建分支…', style: TextStyle(fontSize: 13)),
            ],
          ),
        ),
        if (locals.where((b) => b != current).isNotEmpty)
          const PopupMenuItem<String>(
            value: 'del',
            child: Row(
              children: [
                Icon(Icons.delete_outline_rounded, size: 14),
                SizedBox(width: 8),
                Text('删除分支…', style: TextStyle(fontSize: 13)),
              ],
            ),
          ),
      ],
    );
    if (!mounted || sel == null) return;
    if (sel == 'new') {
      await _createBranch(client);
    } else if (sel == 'del') {
      await _deleteBranchFlow(client, pos, locals, current);
    } else if (sel.startsWith('co:')) {
      await _checkout(client, sel.substring(3));
    }
  }

  /// 删除分支:二级菜单选非当前分支 → 确认 → gitDeleteBranch;未合并(非 force
  /// 失败)时追问是否强删。
  Future<void> _deleteBranchFlow(
      dynamic client, Offset pos, List<String> locals, String? current) async {
    final messenger = ScaffoldMessenger.of(context);
    final deletable = locals.where((b) => b != current).toList();
    if (deletable.isEmpty) return;
    final target = await showMenu<String>(
      context: context,
      position: RelativeRect.fromLTRB(pos.dx, pos.dy, pos.dx, pos.dy),
      items: [
        for (final b in deletable)
          PopupMenuItem<String>(
            value: b,
            child: Row(
              children: [
                Icon(Icons.delete_outline_rounded,
                    size: 14, color: Colors.red.shade400),
                const SizedBox(width: 8),
                Text(b, style: const TextStyle(fontSize: 13)),
              ],
            ),
          ),
      ],
    );
    if (!mounted || target == null) return;
    final ok = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('删除分支', style: TextStyle(fontSize: 15)),
        content: Text('确认删除本地分支 `$target`?'),
        actions: [
          TextButton(
              onPressed: () => Navigator.pop(ctx, false),
              child: const Text('取消')),
          FilledButton(
              onPressed: () => Navigator.pop(ctx, true),
              child: const Text('删除')),
        ],
      ),
    );
    if (ok != true || !mounted) return;
    setState(() => _busy = true);
    try {
      await client.gitDeleteBranch(widget.project.path, target);
      messenger.showSnackBar(SnackBar(
          content: Text('已删除分支 $target'),
          duration: const Duration(seconds: 1)));
    } catch (e) {
      // 未合并 → git 拒绝(非 force)。追问强删。
      if (!mounted) {
        return;
      }
      final force = await showDialog<bool>(
        context: context,
        builder: (ctx) => AlertDialog(
          title: const Text('分支未合并', style: TextStyle(fontSize: 15)),
          content: Text('`$target` 有未合并的提交,删除会丢失。强制删除?'),
          actions: [
            TextButton(
                onPressed: () => Navigator.pop(ctx, false),
                child: const Text('取消')),
            FilledButton(
              style: FilledButton.styleFrom(backgroundColor: Colors.red.shade400),
              onPressed: () => Navigator.pop(ctx, true),
              child: const Text('强制删除'),
            ),
          ],
        ),
      );
      if (force == true && mounted) {
        try {
          await client.gitDeleteBranch(widget.project.path, target, force: true);
          messenger.showSnackBar(SnackBar(
              content: Text('已强制删除分支 $target'),
              duration: const Duration(seconds: 1)));
        } catch (e2) {
          messenger.showSnackBar(SnackBar(content: Text('删除失败: $e2')));
        }
      }
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _checkout(dynamic client, String name) async {
    final messenger = ScaffoldMessenger.of(context);
    if (name == ref.read(activeCodeProjectProvider)?.branch) return;
    setState(() => _busy = true);
    try {
      await client.gitCheckoutBranch(widget.project.path, name);
      await ref
          .read(codeProjectsControllerProvider.notifier)
          .setBranch(widget.project.id, name);
      messenger.showSnackBar(
        SnackBar(content: Text('已切换到 $name'), duration: const Duration(seconds: 1)),
      );
    } catch (e) {
      messenger.showSnackBar(SnackBar(content: Text('切换失败: $e')));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _createBranch(dynamic client) async {
    final messenger = ScaffoldMessenger.of(context);
    final ctrl = TextEditingController();
    final name = await showDialog<String>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('新建分支', style: TextStyle(fontSize: 15)),
        content: TextField(
          controller: ctrl,
          autofocus: true,
          decoration: const InputDecoration(hintText: '分支名,如 feature/x'),
          onSubmitted: (v) => Navigator.pop(ctx, v.trim()),
        ),
        actions: [
          TextButton(onPressed: () => Navigator.pop(ctx), child: const Text('取消')),
          FilledButton(
            onPressed: () => Navigator.pop(ctx, ctrl.text.trim()),
            child: const Text('创建并切换'),
          ),
        ],
      ),
    );
    if (name == null || name.isEmpty || !mounted) return;
    setState(() => _busy = true);
    try {
      await client.gitCreateBranch(widget.project.path, name, checkout: true);
      await ref
          .read(codeProjectsControllerProvider.notifier)
          .setBranch(widget.project.id, name);
      messenger.showSnackBar(
        SnackBar(content: Text('已创建并切换到 $name'),
            duration: const Duration(seconds: 1)),
      );
    } catch (e) {
      messenger.showSnackBar(SnackBar(content: Text('新建分支失败: $e')));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final branch = ref.watch(activeCodeProjectProvider)?.branch ??
        widget.project.branch;
    final scheme = Theme.of(context).colorScheme;

    return GestureDetector(
      onTapDown: _busy ? null : (d) => _openMenu(d.globalPosition),
      child: Container(
        height: 36,
        padding: const EdgeInsets.symmetric(horizontal: 12),
        decoration: BoxDecoration(
          border: Border(
            bottom: BorderSide(color: scheme.outlineVariant, width: 1),
          ),
        ),
        child: Row(
          children: [
            Icon(Icons.account_tree_outlined,
                size: 16, color: scheme.onSurfaceVariant),
            const SizedBox(width: 6),
            Expanded(
              child: Text(
                branch == null || branch.isEmpty ? '—' : branch,
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
                style: Theme.of(context).textTheme.bodySmall?.copyWith(
                      color: scheme.onSurfaceVariant,
                    ),
              ),
            ),
            if (_busy)
              const SizedBox(
                  width: 12, height: 12,
                  child: CircularProgressIndicator(strokeWidth: 1.6))
            else
              Icon(Icons.expand_more, size: 16, color: scheme.onSurfaceVariant),
          ],
        ),
      ),
    );
  }
}

// FileExplorerPanel —— 编码模块右栏「文件」面板(M4-D / CORE-1b)。懒加载目录树;
// 点文件 → openFilesProvider 在主区开编辑器 Tab(树留右栏、文件在主区
// 多 Tab 打开)。右键目录可新建文件/文件夹,右键任意节点可删除(二次确认)。
//
// 作用于当前活动项目根目录;无 daemon/项目时给提示。文件查看器已抽到
// widgets/file_content_view.dart,由主区 FileEditorTabs 复用。

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../app/theme.dart';
import '../application/file_explorer_controller.dart';
import '../application/open_files_controller.dart';
import '../application/projects_controller.dart';
import '../data/code_bridge_provider.dart';

class FileExplorerPanel extends ConsumerWidget {
  const FileExplorerPanel({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final bridgeReady = ref.watch(codeBridgeClientProvider) != null;
    final project = ref.watch(activeCodeProjectProvider);
    if (project == null) {
      return _hint(Icons.folder_off_outlined, '打开一个项目以浏览文件');
    }
    if (!bridgeReady) {
      return _hint(Icons.cloud_off_rounded,
          '本地 daemon 未就绪 —— 文件浏览不可用\n登录后桌面端会自动启动 biu serve');
    }
    return _Tree(root: project.path);
  }

  static Widget _hint(IconData icon, String text) => Center(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(icon, size: 32, color: BiuTokens.textMuted),
            const SizedBox(height: 10),
            Text(text,
                textAlign: TextAlign.center,
                style: TextStyle(
                    fontSize: 12.5,
                    color: BiuTokens.textSecondary,
                    height: 1.5)),
          ],
        ),
      );
}

class _Tree extends ConsumerWidget {
  const _Tree({required this.root});
  final String root;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final notifier = ref.read(fileExplorerControllerProvider.notifier);
    final state = ref.watch(fileExplorerControllerProvider);
    final openActive = ref.watch(openFilesProvider).active;
    final nodes = notifier.visibleNodes();

    return Column(
      children: [
        // 头:项目名 + 在根目录新建 + 刷新。
        Container(
          height: 32,
          padding: const EdgeInsets.only(left: 12, right: 4),
          decoration: BoxDecoration(
            border: Border(bottom: BorderSide(color: BiuTokens.borderSubtle)),
          ),
          child: Row(
            children: [
              Expanded(
                child: Text(
                  root.split('/').last,
                  overflow: TextOverflow.ellipsis,
                  style: TextStyle(
                      fontSize: 11.5,
                      fontWeight: FontWeight.w700,
                      letterSpacing: 0.3,
                      color: BiuTokens.textSecondary),
                ),
              ),
              _MiniBtn(
                icon: Icons.note_add_outlined,
                tip: '新建文件',
                onTap: () => _newFile(context, notifier, root),
              ),
              _MiniBtn(
                icon: Icons.create_new_folder_outlined,
                tip: '新建文件夹',
                onTap: () => _newFolder(context, notifier, root),
              ),
              _MiniBtn(
                icon: Icons.refresh_rounded,
                tip: '刷新',
                onTap: () => notifier.reloadDir(root),
              ),
            ],
          ),
        ),
        if (state.error != null)
          Container(
            width: double.infinity,
            padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 4),
            color: BiuTokens.error.withValues(alpha: 0.1),
            child: Text(state.error!,
                maxLines: 2,
                overflow: TextOverflow.ellipsis,
                style: TextStyle(fontSize: 11, color: BiuTokens.error)),
          ),
        Expanded(
          child: (nodes.isEmpty && state.loadingDirs.contains(root))
              ? const Center(child: CircularProgressIndicator(strokeWidth: 2))
              : ListView.builder(
                  itemCount: nodes.length,
                  itemBuilder: (ctx, i) {
                    final n = nodes[i];
                    return _NodeRow(
                      node: n,
                      expanded: state.expanded.contains(n.path),
                      selected: openActive == n.path,
                      loading: state.loadingDirs.contains(n.path),
                      onTap: () {
                        if (n.isDir) {
                          notifier.toggleDir(n.path);
                        } else {
                          // 点文件 → 主区开编辑 Tab + 聚焦文件区(CORE-1b / 修「切回」)。
                          ref.read(openFilesProvider.notifier).open(n.path);
                          ref.read(mainFocusProvider.notifier).state =
                              MainFocus.files;
                        }
                      },
                      onContext: (pos) =>
                          _menu(context, ref, notifier, n.path, n.isDir, pos),
                    );
                  },
                ),
        ),
      ],
    );
  }

  Future<void> _menu(BuildContext context, WidgetRef ref,
      FileExplorerController n, String path, bool isDir, Offset pos) async {
    final sel = await showMenu<String>(
      context: context,
      position: RelativeRect.fromLTRB(pos.dx, pos.dy, pos.dx, pos.dy),
      items: [
        if (isDir) ...[
          const PopupMenuItem(value: 'file', child: Text('新建文件')),
          const PopupMenuItem(value: 'folder', child: Text('新建文件夹')),
          const PopupMenuDivider(),
        ],
        const PopupMenuItem(value: 'delete', child: Text('删除')),
      ],
    );
    if (!context.mounted) return;
    switch (sel) {
      case 'file':
        await _newFile(context, n, path);
      case 'folder':
        await _newFolder(context, n, path);
      case 'delete':
        final ok = await _confirm(context, '删除 ${path.split('/').last}?',
            '此操作不可撤销(永久删除,不进回收站)。');
        if (ok) await n.delete(path);
    }
  }

  Future<void> _newFile(
      BuildContext context, FileExplorerController n, String dir) async {
    final name = await _prompt(context, '新建文件', '文件名');
    if (name != null && name.isNotEmpty) await n.createFile(dir, name);
  }

  Future<void> _newFolder(
      BuildContext context, FileExplorerController n, String dir) async {
    final name = await _prompt(context, '新建文件夹', '文件夹名');
    if (name != null && name.isNotEmpty) await n.createDirectory(dir, name);
  }
}

class _NodeRow extends StatelessWidget {
  const _NodeRow({
    required this.node,
    required this.expanded,
    required this.selected,
    required this.loading,
    required this.onTap,
    required this.onContext,
  });
  final FileTreeNode node;
  final bool expanded;
  final bool selected;
  final bool loading;
  final VoidCallback onTap;
  final ValueChanged<Offset> onContext;

  @override
  Widget build(BuildContext context) {
    return GestureDetector(
      onSecondaryTapDown: (d) => onContext(d.globalPosition),
      onLongPressStart: (d) => onContext(d.globalPosition),
      child: InkWell(
        onTap: onTap,
        child: Container(
          height: 26,
          padding: EdgeInsets.only(left: 8.0 + node.depth * 14, right: 8),
          color: selected ? BiuTokens.purpleSoft : Colors.transparent,
          child: Row(
            children: [
              SizedBox(
                width: 16,
                child: node.isDir
                    ? Icon(
                        expanded
                            ? Icons.keyboard_arrow_down_rounded
                            : Icons.chevron_right_rounded,
                        size: 16,
                        color: BiuTokens.textMuted)
                    : const SizedBox.shrink(),
              ),
              Icon(
                node.isDir
                    ? (expanded
                        ? Icons.folder_open_rounded
                        : Icons.folder_rounded)
                    : _fileIcon(node.name),
                size: 14,
                color: node.isDir ? BiuTokens.purple : BiuTokens.textSecondary,
              ),
              const SizedBox(width: 6),
              Expanded(
                child: Text(node.name,
                    overflow: TextOverflow.ellipsis,
                    style: TextStyle(
                        fontSize: 12.5,
                        color: BiuTokens.text,
                        fontWeight:
                            selected ? FontWeight.w600 : FontWeight.w400)),
              ),
              if (loading)
                const SizedBox(
                    width: 12,
                    height: 12,
                    child: CircularProgressIndicator(strokeWidth: 2)),
            ],
          ),
        ),
      ),
    );
  }

  IconData _fileIcon(String name) {
    final ext = name.contains('.') ? name.split('.').last.toLowerCase() : '';
    return switch (ext) {
      'png' || 'jpg' || 'jpeg' || 'gif' || 'webp' || 'bmp' || 'svg' =>
        Icons.image_outlined,
      'md' || 'markdown' || 'txt' => Icons.description_outlined,
      'json' || 'yaml' || 'yml' || 'toml' => Icons.data_object_rounded,
      _ => Icons.insert_drive_file_outlined,
    };
  }
}

class _MiniBtn extends StatelessWidget {
  const _MiniBtn({required this.icon, required this.tip, required this.onTap});
  final IconData icon;
  final String tip;
  final VoidCallback onTap;
  @override
  Widget build(BuildContext context) => Tooltip(
        message: tip,
        child: InkWell(
          onTap: onTap,
          borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
          child: SizedBox(
              width: 26,
              height: 26,
              child: Icon(icon, size: 15, color: BiuTokens.textSecondary)),
        ),
      );
}

Future<String?> _prompt(BuildContext context, String title, String hint) {
  final ctrl = TextEditingController();
  return showDialog<String>(
    context: context,
    builder: (ctx) => AlertDialog(
      title: Text(title, style: const TextStyle(fontSize: 15)),
      content: TextField(
        controller: ctrl,
        autofocus: true,
        decoration: InputDecoration(hintText: hint),
        onSubmitted: (v) => Navigator.pop(ctx, v.trim()),
      ),
      actions: [
        TextButton(
            onPressed: () => Navigator.pop(ctx), child: const Text('取消')),
        FilledButton(
          onPressed: () => Navigator.pop(ctx, ctrl.text.trim()),
          child: const Text('创建'),
        ),
      ],
    ),
  );
}

Future<bool> _confirm(BuildContext context, String title, String body) async {
  final res = await showDialog<bool>(
    context: context,
    builder: (ctx) => AlertDialog(
      title: Text(title, style: const TextStyle(fontSize: 15)),
      content: Text(body, style: const TextStyle(fontSize: 13)),
      actions: [
        TextButton(
            onPressed: () => Navigator.pop(ctx, false), child: const Text('取消')),
        FilledButton(
          onPressed: () => Navigator.pop(ctx, true),
          style: FilledButton.styleFrom(backgroundColor: BiuTokens.error),
          child: const Text('删除'),
        ),
      ],
    ),
  );
  return res ?? false;
}

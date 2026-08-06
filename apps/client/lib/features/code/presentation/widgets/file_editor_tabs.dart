// 主区文件编辑 Tab 区(点文件树/搜索结果 → 这里以 Tab 打开)。
//
// 读 openFilesProvider:渲染 Tab 栏(文件名 + 关闭)+ 当前文件内容。IndexedStack
// 让所有打开的文件保活(切 Tab 不重载、不丢滚动位置)。无打开文件时本组件不渲染
// (工作台据 openFiles.isEmpty 切回任务会话回放)。

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../../app/theme.dart';
import '../../application/open_files_controller.dart';
import '../../application/projects_controller.dart';
import 'file_content_view.dart';

class FileEditorTabs extends ConsumerWidget {
  const FileEditorTabs({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final open = ref.watch(openFilesProvider);
    final root = ref.watch(activeCodeProjectProvider)?.path ?? '';
    if (open.isEmpty) return const SizedBox.shrink();
    final activeIdx = open.active == null ? 0 : open.paths.indexOf(open.active!);

    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        // Tab 栏。
        Container(
          height: 36,
          decoration: BoxDecoration(
            color: BiuTokens.bg,
            border: Border(bottom: BorderSide(color: BiuTokens.borderSubtle)),
          ),
          child: ListView(
            scrollDirection: Axis.horizontal,
            children: [
              for (final path in open.paths)
                _Tab(
                  label: path.split('/').last,
                  selected: path == open.active,
                  onTap: () =>
                      ref.read(openFilesProvider.notifier).setActive(path),
                  onClose: () =>
                      ref.read(openFilesProvider.notifier).close(path),
                ),
            ],
          ),
        ),
        Expanded(
          child: IndexedStack(
            index: activeIdx < 0 ? 0 : activeIdx,
            children: [
              for (final path in open.paths)
                FileContentView(
                  key: ValueKey('editor_$path'),
                  path: path,
                  root: root,
                ),
            ],
          ),
        ),
      ],
    );
  }
}

class _Tab extends StatelessWidget {
  const _Tab({
    required this.label,
    required this.selected,
    required this.onTap,
    required this.onClose,
  });
  final String label;
  final bool selected;
  final VoidCallback onTap;
  final VoidCallback onClose;

  @override
  Widget build(BuildContext context) {
    return InkWell(
      onTap: onTap,
      child: Container(
        padding: const EdgeInsets.only(left: 12, right: 6),
        decoration: BoxDecoration(
          color: selected ? BiuTokens.surface : Colors.transparent,
          border: Border(
            right: BorderSide(color: BiuTokens.borderSubtle),
            bottom: BorderSide(
              color: selected ? BiuTokens.purple : Colors.transparent,
              width: 2,
            ),
          ),
        ),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            Text(
              label,
              style: TextStyle(
                fontSize: 12,
                fontWeight: selected ? FontWeight.w600 : FontWeight.w500,
                color: selected ? BiuTokens.text : BiuTokens.textSecondary,
              ),
            ),
            const SizedBox(width: 6),
            InkWell(
              onTap: onClose,
              borderRadius: BorderRadius.circular(BiuTokens.radiusXs),
              child: Padding(
                padding: const EdgeInsets.all(2),
                child: Icon(Icons.close_rounded,
                    size: 13, color: BiuTokens.textMuted),
              ),
            ),
          ],
        ),
      ),
    );
  }
}

// ProjectRail — 48px 左侧项目切换栏(M1)。
//
// 项目头像竖排,点选切换 activeProject;底部 "+" 添加项目(目录选择器)。
// 头像底色取 avatarColor,缺省由 name 哈希生成。

import 'package:file_selector/file_selector.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../application/projects_controller.dart';
import '../domain/project.dart';

/// 选目录并添加为项目,成功后切到它。返回新建/已存在的项目(取消返回 null)。
Future<CodeProject?> pickAndAddProject(WidgetRef ref) async {
  final dir = await getDirectoryPath(confirmButtonText: '添加');
  if (dir == null) return null;
  final proj =
      await ref.read(codeProjectsControllerProvider.notifier).addProjectByPath(dir);
  ref.read(activeCodeProjectIdProvider.notifier).state = proj.id;
  return proj;
}

class ProjectRail extends ConsumerWidget {
  const ProjectRail({super.key});

  static const double width = 48;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final projects = ref.watch(railProjectsProvider);
    final activeId = ref.watch(activeCodeProjectIdProvider);
    final scheme = Theme.of(context).colorScheme;

    return Container(
      width: width,
      color: scheme.surfaceContainerHighest.withValues(alpha: 0.4),
      child: Column(
        children: [
          const SizedBox(height: 8),
          Expanded(
            // 长按拖拽重排;轻点切换项目。隐藏默认拖拽手柄(48px 栏放不下),
            // 用 ReorderableDelayedDragStartListener 让长按发起拖拽。
            child: ReorderableListView.builder(
              padding: const EdgeInsets.symmetric(vertical: 4),
              buildDefaultDragHandles: false,
              itemCount: projects.length,
              onReorderItem: (oldIndex, newIndex) {
                ref
                    .read(codeProjectsControllerProvider.notifier)
                    .reorderVisible(oldIndex, newIndex);
              },
              proxyDecorator: (child, index, animation) => Material(
                color: Colors.transparent,
                child: child,
              ),
              itemBuilder: (context, i) {
                final p = projects[i];
                return ReorderableDelayedDragStartListener(
                  key: ValueKey(p.id),
                  index: i,
                  child: _RailAvatar(
                    project: p,
                    active: p.id == activeId,
                    onTap: () {
                      ref.read(activeCodeProjectIdProvider.notifier).state = p.id;
                      ref
                          .read(codeProjectsControllerProvider.notifier)
                          .touch(p.id);
                    },
                  ),
                );
              },
            ),
          ),
          _AddButton(onTap: () => pickAndAddProject(ref)),
          const SizedBox(height: 8),
        ],
      ),
    );
  }
}

class _RailAvatar extends StatelessWidget {
  const _RailAvatar({
    required this.project,
    required this.active,
    required this.onTap,
  });

  final CodeProject project;
  final bool active;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final color = avatarColorFor(project);
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 4, horizontal: 6),
      child: Tooltip(
        message: '${project.name}\n${project.path}',
        child: InkWell(
          onTap: onTap,
          borderRadius: BorderRadius.circular(active ? 10 : 18),
          child: AnimatedContainer(
            duration: const Duration(milliseconds: 150),
            width: 36,
            height: 36,
            decoration: BoxDecoration(
              color: color,
              borderRadius: BorderRadius.circular(active ? 10 : 18),
              border: active
                  ? Border.all(
                      color: Theme.of(context).colorScheme.primary, width: 2)
                  : null,
            ),
            alignment: Alignment.center,
            child: Text(
              _initials(project.name),
              style: const TextStyle(
                color: Colors.white,
                fontWeight: FontWeight.w600,
                fontSize: 14,
              ),
            ),
          ),
        ),
      ),
    );
  }
}

class _AddButton extends StatelessWidget {
  const _AddButton({required this.onTap});
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    return Tooltip(
      message: '添加项目',
      child: InkWell(
        onTap: onTap,
        borderRadius: BorderRadius.circular(18),
        child: Container(
          width: 36,
          height: 36,
          decoration: BoxDecoration(
            color: scheme.surfaceContainerHighest,
            borderRadius: BorderRadius.circular(18),
          ),
          child: Icon(Icons.add, color: scheme.onSurfaceVariant, size: 20),
        ),
      ),
    );
  }
}

/// 头像底色:优先 avatarColor(#rrggbb),否则由 name 哈希到一个稳定的鲜明色。
Color avatarColorFor(CodeProject p) {
  if (p.avatarColor != null && p.avatarColor!.isNotEmpty) {
    final parsed = _parseHexColor(p.avatarColor!);
    if (parsed != null) return parsed;
  }
  // name 哈希 → HSL 色相,固定饱和/亮度,保证可读且稳定。
  var h = 0;
  for (final c in p.name.codeUnits) {
    h = (h * 31 + c) & 0x7fffffff;
  }
  final hue = (h % 360).toDouble();
  return HSLColor.fromAHSL(1, hue, 0.55, 0.48).toColor();
}

Color? _parseHexColor(String hex) {
  var s = hex.replaceFirst('#', '');
  if (s.length == 6) s = 'ff$s';
  if (s.length != 8) return null;
  final v = int.tryParse(s, radix: 16);
  return v == null ? null : Color(v);
}

String _initials(String name) {
  final trimmed = name.trim();
  if (trimmed.isEmpty) return '?';
  return trimmed.characters.first.toUpperCase();
}

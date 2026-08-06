// 我的作品页 — tasksControllerProvider 全量瀑布流 + 多选 + 批处理.
//
// 多选模式: 长按或顶部「选择」按钮进入. 选中态显示工具条 (公开/私有/删除).
// 单选 tap → 详情 sheet (做同款 / 删除 / 公开私有切换).
// 失败任务的「重试」用 form.syncFromTask 填回去, 跳 /creation.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../../app/theme.dart';
import '../../../../l10n/app_localizations.dart';
import '../../application/generation_form_controller.dart';
import '../../application/tasks_controller.dart' as tc;
import '../../application/tasks_controller.dart' show tasksControllerProvider;
import '../../domain/creation_task.dart';
import '../widgets/creation_card.dart';
import 'task_detail_sheet.dart';

class MyWorksPage extends ConsumerStatefulWidget {
  const MyWorksPage({super.key});

  @override
  ConsumerState<MyWorksPage> createState() => _MyWorksPageState();
}

class _MyWorksPageState extends ConsumerState<MyWorksPage> {
  final Set<String> _selected = {};
  bool _selectMode = false;

  void _toggle(String id) {
    setState(() {
      if (_selected.contains(id)) {
        _selected.remove(id);
      } else {
        _selected.add(id);
      }
      if (_selected.isEmpty) _selectMode = false;
    });
  }

  void _enterSelect(String id) {
    setState(() {
      _selectMode = true;
      _selected.add(id);
    });
  }

  void _exitSelect() => setState(() {
        _selected.clear();
        _selectMode = false;
      });

  Future<void> _bulkDelete() async {
    final tasks = ref.read(tasksControllerProvider.notifier);
    final ids = List<String>.from(_selected);
    _exitSelect();
    for (final id in ids) {
      try {
        await tasks.delete(id);
      } catch (_) {/* swallow per-task; UI 会刷新剩余 */}
    }
  }

  Future<void> _bulkSetVisibility(bool isPublic) async {
    final tasks = ref.read(tasksControllerProvider.notifier);
    final ids = List<String>.from(_selected);
    _exitSelect();
    for (final id in ids) {
      try {
        await tasks.setVisibility(id, isPublic);
      } catch (_) {}
    }
  }

  void _onCardTap(CreationTask t) {
    if (_selectMode) {
      _toggle(t.id);
      return;
    }
    showTaskDetailSheet(context, ref, t);
  }

  void _onRetry(CreationTask t) {
    // 把失败任务的参数同步回 form, 跳 studio 让用户改后再提交.
    ref.read(generationFormControllerProvider.notifier).syncFromTask(
          type: GenerationType.fromWire(t.type),
          modelCode: t.modelCode,
          prompt: t.prompt,
          negativePrompt: t.negativePrompt ?? '',
          params: t.params,
          isPublic: t.isPublic,
        );
    context.go('/creation/center');
  }

  @override
  Widget build(BuildContext context) {
    final state = ref.watch(tasksControllerProvider);
    final tasks = state.sortedDesc();
    final t = AppLocalizations.of(context)!;

    return Container(
      color: BiuTokens.bg,
      child: Column(
        children: [
          _Toolbar(
            count: tasks.length,
            selectedCount: _selected.length,
            selectMode: _selectMode,
            connection: state.connection,
            onToggleSelectMode: () => setState(() {
              _selectMode = !_selectMode;
              if (!_selectMode) _selected.clear();
            }),
            onSelectAll: () => setState(() {
              _selected
                ..clear()
                ..addAll(tasks.map((x) => x.id));
            }),
            onBulkDelete: _selected.isEmpty ? null : _bulkDelete,
            onBulkPublic: _selected.isEmpty ? null : () => _bulkSetVisibility(true),
            onBulkPrivate: _selected.isEmpty ? null : () => _bulkSetVisibility(false),
          ),
          Expanded(
            child: tasks.isEmpty
                ? (state.initialFetchDone
                    ? _EmptyState(label: t.creationWorks)
                    : const Center(child: CircularProgressIndicator()))
                : _Grid(
                    tasks: tasks,
                    selectMode: _selectMode,
                    selected: _selected,
                    onTap: _onCardTap,
                    onLongPress: (t) {
                      if (!_selectMode) _enterSelect(t.id);
                    },
                    onRetry: _onRetry,
                  ),
          ),
        ],
      ),
    );
  }
}

class _Toolbar extends StatelessWidget {
  const _Toolbar({
    required this.count,
    required this.selectedCount,
    required this.selectMode,
    required this.connection,
    required this.onToggleSelectMode,
    required this.onSelectAll,
    required this.onBulkDelete,
    required this.onBulkPublic,
    required this.onBulkPrivate,
  });
  final int count;
  final int selectedCount;
  final bool selectMode;
  final tc.ConnectionState connection;
  final VoidCallback onToggleSelectMode;
  final VoidCallback onSelectAll;
  final VoidCallback? onBulkDelete;
  final VoidCallback? onBulkPublic;
  final VoidCallback? onBulkPrivate;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 20, vertical: 12),
      decoration: BoxDecoration(
        color: BiuTokens.surface,
        border: Border(bottom: BorderSide(color: BiuTokens.borderSubtle)),
      ),
      child: Row(
        children: [
          Text(
            selectMode ? '已选 $selectedCount' : '我的作品 · $count',
            style: TextStyle(
              fontSize: 14,
              fontWeight: FontWeight.w600,
              color: BiuTokens.text,
            ),
          ),
          if (connection == tc.ConnectionState.pollingFallback) ...[
            const SizedBox(width: 8),
            Tooltip(
              message: '主通道断, 30s 轮询兜底中',
              child: Icon(Icons.cloud_off, size: 14, color: BiuTokens.textMuted),
            ),
          ],
          const Spacer(),
          if (selectMode) ...[
            TextButton.icon(
              onPressed: onSelectAll,
              icon: const Icon(Icons.select_all, size: 16),
              label: const Text('全选'),
            ),
            IconButton(
              onPressed: onBulkPublic,
              icon: const Icon(Icons.public, size: 18),
              tooltip: '公开',
            ),
            IconButton(
              onPressed: onBulkPrivate,
              icon: const Icon(Icons.lock_outline, size: 18),
              tooltip: '私有',
            ),
            IconButton(
              onPressed: onBulkDelete,
              icon: Icon(Icons.delete_outline, size: 18, color: BiuTokens.error),
              tooltip: '删除',
            ),
            TextButton(
              onPressed: onToggleSelectMode,
              child: const Text('退出'),
            ),
          ] else
            TextButton.icon(
              onPressed: onToggleSelectMode,
              icon: const Icon(Icons.checklist, size: 16),
              label: const Text('选择'),
            ),
        ],
      ),
    );
  }
}

class _Grid extends StatelessWidget {
  const _Grid({
    required this.tasks,
    required this.selectMode,
    required this.selected,
    required this.onTap,
    required this.onLongPress,
    required this.onRetry,
  });
  final List<CreationTask> tasks;
  final bool selectMode;
  final Set<String> selected;
  final ValueChanged<CreationTask> onTap;
  final ValueChanged<CreationTask> onLongPress;
  final ValueChanged<CreationTask> onRetry;

  @override
  Widget build(BuildContext context) {
    final w = MediaQuery.of(context).size.width;
    final cols = (w / 220).floor().clamp(2, 6);
    return GridView.builder(
      padding: const EdgeInsets.all(16),
      itemCount: tasks.length,
      gridDelegate: SliverGridDelegateWithFixedCrossAxisCount(
        crossAxisCount: cols,
        mainAxisSpacing: 12,
        crossAxisSpacing: 12,
      ),
      itemBuilder: (_, i) {
        final t = tasks[i];
        final isSelected = selected.contains(t.id);
        return Stack(
          children: [
            GestureDetector(
              onLongPress: () => onLongPress(t),
              child: CreationCard(
                task: t,
                onTap: () => onTap(t),
                onRetry: () => onRetry(t),
              ),
            ),
            if (selectMode)
              Positioned(
                top: 8,
                left: 8,
                child: Container(
                  width: 22,
                  height: 22,
                  decoration: BoxDecoration(
                    color: isSelected ? BiuTokens.purple : Colors.black54,
                    shape: BoxShape.circle,
                    border: Border.all(color: Colors.white, width: 2),
                  ),
                  child: isSelected
                      ? const Icon(Icons.check, color: Colors.white, size: 14)
                      : null,
                ),
              ),
          ],
        );
      },
    );
  }
}

class _EmptyState extends StatelessWidget {
  const _EmptyState({required this.label});
  final String label;

  @override
  Widget build(BuildContext context) {
    return Center(
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          Icon(Icons.image_outlined, size: 56, color: BiuTokens.textMuted),
          const SizedBox(height: 12),
          Text(
            '还没有作品',
            style: TextStyle(
              fontSize: 16,
              fontWeight: FontWeight.w600,
              color: BiuTokens.text,
            ),
          ),
          const SizedBox(height: 6),
          Text(
            '生成的图片 / 视频 / 数字人会出现在这里',
            style: TextStyle(fontSize: 12, color: BiuTokens.textMuted),
          ),
          const SizedBox(height: 16),
          FilledButton.icon(
            onPressed: () => context.go('/creation/center'),
            icon: const Icon(Icons.auto_awesome, size: 16),
            label: const Text('去创作'),
          ),
        ],
      ),
    );
  }
}

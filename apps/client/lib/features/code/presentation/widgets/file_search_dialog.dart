// 文件查找弹窗(搜索浮层:输入文件名模糊查 Git 管理的文件 → 回车/点击
// 在主区开 Tab)。走 bridge fsSearch(项目根 + query),300ms 防抖。

import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../../app/theme.dart';
import '../../application/open_files_controller.dart';
import '../../application/projects_controller.dart';
import '../../data/code_bridge_provider.dart';
import '../../domain/git_models.dart' show FileSearchResult;

/// 打开文件查找弹窗;选中文件 → openFilesProvider.open + 关闭弹窗。
Future<void> showFileSearchDialog(BuildContext context, WidgetRef ref) async {
  final root = ref.read(activeCodeProjectProvider)?.path;
  if (root == null) {
    ScaffoldMessenger.of(context).showSnackBar(
      const SnackBar(content: Text('请先打开一个项目')),
    );
    return;
  }
  await showDialog<void>(
    context: context,
    barrierColor: Colors.black.withValues(alpha: 0.3),
    builder: (_) => _FileSearchDialog(root: root),
  );
}

class _FileSearchDialog extends ConsumerStatefulWidget {
  const _FileSearchDialog({required this.root});
  final String root;

  @override
  ConsumerState<_FileSearchDialog> createState() => _FileSearchDialogState();
}

class _FileSearchDialogState extends ConsumerState<_FileSearchDialog> {
  final _ctrl = TextEditingController();
  final _focus = FocusNode();
  Timer? _debounce;
  List<FileSearchResult> _results = const [];
  bool _loading = false;
  int _highlight = 0;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) => _focus.requestFocus());
  }

  @override
  void dispose() {
    _debounce?.cancel();
    _ctrl.dispose();
    _focus.dispose();
    super.dispose();
  }

  void _onChanged(String q) {
    _debounce?.cancel();
    _debounce = Timer(const Duration(milliseconds: 300), () => _search(q));
  }

  Future<void> _search(String q) async {
    final query = q.trim();
    if (query.isEmpty) {
      setState(() {
        _results = const [];
        _loading = false;
      });
      return;
    }
    final bridge = ref.read(codeBridgeClientProvider);
    if (bridge == null) return;
    setState(() => _loading = true);
    try {
      final r = await bridge.fsSearch(widget.root, query, limit: 50);
      if (!mounted) return;
      setState(() {
        _results = r;
        _loading = false;
        _highlight = 0;
      });
    } catch (_) {
      if (!mounted) return;
      setState(() {
        _results = const [];
        _loading = false;
      });
    }
  }

  void _openSelected() {
    if (_results.isEmpty) return;
    final pick = _results[_highlight.clamp(0, _results.length - 1)];
    ref.read(openFilesProvider.notifier).open(pick.path);
    ref.read(mainFocusProvider.notifier).state = MainFocus.files;
    Navigator.of(context).pop();
  }

  @override
  Widget build(BuildContext context) {
    return Dialog(
      alignment: Alignment.topCenter,
      insetPadding: const EdgeInsets.only(top: 90, left: 40, right: 40),
      backgroundColor: BiuTokens.surface,
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(BiuTokens.radiusLg),
      ),
      child: ConstrainedBox(
        constraints: const BoxConstraints(maxWidth: 720, maxHeight: 460),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            // 标题条。
            Padding(
              padding: const EdgeInsets.fromLTRB(16, 14, 12, 6),
              child: Row(
                children: [
                  Text('搜索',
                      style: TextStyle(
                          fontSize: 14,
                          fontWeight: FontWeight.w600,
                          color: BiuTokens.text)),
                  const Spacer(),
                  InkWell(
                    onTap: () => Navigator.of(context).pop(),
                    child: Icon(Icons.close_rounded,
                        size: 18, color: BiuTokens.textMuted),
                  ),
                ],
              ),
            ),
            // 输入框。
            Padding(
              padding: const EdgeInsets.symmetric(horizontal: 16),
              child: CallbackShortcuts(
                bindings: {
                  const SingleActivator(LogicalKeyboardKey.arrowDown): () =>
                      setState(() => _highlight =
                          (_highlight + 1).clamp(0, _results.length - 1)),
                  const SingleActivator(LogicalKeyboardKey.arrowUp): () =>
                      setState(() => _highlight =
                          (_highlight - 1).clamp(0, _results.length - 1)),
                },
                child: TextField(
                  controller: _ctrl,
                  focusNode: _focus,
                  onChanged: _onChanged,
                  onSubmitted: (_) => _openSelected(),
                  decoration: InputDecoration(
                    prefixIcon: Icon(Icons.search_rounded,
                        size: 18, color: BiuTokens.textMuted),
                    hintText: '搜索文件',
                    isDense: true,
                    border: OutlineInputBorder(
                      borderRadius: BorderRadius.circular(BiuTokens.radiusMd),
                    ),
                  ),
                ),
              ),
            ),
            const SizedBox(height: 8),
            Expanded(child: _body()),
          ],
        ),
      ),
    );
  }

  Widget _body() {
    if (_loading) {
      return const Center(child: CircularProgressIndicator(strokeWidth: 2));
    }
    if (_ctrl.text.trim().isEmpty) {
      return Center(
        child: Text('输入文件名搜索已被 Git 管理的文件',
            style: TextStyle(fontSize: 12.5, color: BiuTokens.textMuted)),
      );
    }
    if (_results.isEmpty) {
      return Center(
        child: Text('无匹配文件',
            style: TextStyle(fontSize: 12.5, color: BiuTokens.textMuted)),
      );
    }
    return ListView.builder(
      itemCount: _results.length,
      itemBuilder: (ctx, i) {
        final r = _results[i];
        final hot = i == _highlight;
        return InkWell(
          onTap: () {
            ref.read(openFilesProvider.notifier).open(r.path);
            ref.read(mainFocusProvider.notifier).state = MainFocus.files;
            Navigator.of(context).pop();
          },
          child: Container(
            color: hot ? BiuTokens.purpleSoft : Colors.transparent,
            padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 7),
            child: Row(
              children: [
                Icon(Icons.insert_drive_file_outlined,
                    size: 14, color: BiuTokens.textSecondary),
                const SizedBox(width: 8),
                Text(r.name,
                    style: TextStyle(
                        fontSize: 12.5,
                        fontWeight: FontWeight.w500,
                        color: BiuTokens.text)),
                const SizedBox(width: 8),
                Expanded(
                  child: Text(r.dir,
                      overflow: TextOverflow.ellipsis,
                      style: TextStyle(
                          fontSize: 11,
                          fontFamily: 'SF Mono',
                          color: BiuTokens.textMuted)),
                ),
              ],
            ),
          ),
        );
      },
    );
  }
}

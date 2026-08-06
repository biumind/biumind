// Frontmatter editor dialog — wraps FrontmatterEditor with save UX.
//
// Local state pattern: dialog holds a working copy of the frontmatter
// map; FrontmatterEditor mutates that copy via onChanged. On 保存
// the dialog calls /v1/wiki/projects/{pid}/pages/{id} with If-Match
// version, re-fetches if 409 conflict, otherwise pops.

import 'package:flutter/material.dart' hide showAdaptiveDialog;
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../../app/theme.dart';
import '../../../../core/ui/adaptive_dialog.dart';
import '../../../../data/wiki_providers.dart';
import 'frontmatter_editor.dart';
import 'frontmatter_helpers.dart';

/// Shows the frontmatter editor dialog. Returns true when the user
/// saved successfully (caller can refresh page state to pick up the
/// new title / frontmatter), false otherwise.
Future<bool> showFrontmatterDialog(
  BuildContext context, {
  required String projectId,
  required String pageId,
  required int version,
  required Map<String, dynamic> initial,
}) async {
  // barrierDismissible:false —— 编辑中防误触丢失草稿; 宽屏透传 showDialog,
  // 手机映射 sheet 的 isDismissible/enableDrag。
  final ok = await showAdaptiveDialog<bool>(
    context: context,
    barrierDismissible: false,
    builder: (_) => FrontmatterDialog(
      projectId: projectId,
      pageId: pageId,
      version: version,
      initial: initial,
    ),
  );
  return ok ?? false;
}

class FrontmatterDialog extends ConsumerStatefulWidget {
  const FrontmatterDialog({
    super.key,
    required this.projectId,
    required this.pageId,
    required this.version,
    required this.initial,
  });
  final String projectId;
  final String pageId;
  final int version;
  final Map<String, dynamic> initial;

  @override
  ConsumerState<FrontmatterDialog> createState() => _FrontmatterDialogState();
}

class _FrontmatterDialogState extends ConsumerState<FrontmatterDialog> {
  late Map<String, dynamic> _draft;
  bool _saving = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    _draft = Map<String, dynamic>.of(widget.initial);
  }

  bool get _dirty => isDirty(_draft, widget.initial);

  Future<void> _save() async {
    if (!_dirty) {
      Navigator.of(context).pop(false);
      return;
    }
    setState(() {
      _saving = true;
      _error = null;
    });
    try {
      final repo = ref.read(wikiRepositoryProvider);
      if (repo == null) throw StateError('请先登录');
      // We send title + frontmatter together. title is also a
      // first-class column on the page row, so it goes into the page
      // record proper rather than the jsonb. Other keys (description /
      // tags / sources / related / extras) all live in frontmatter.
      final title = stringFieldValue(_draft['title']);
      final fmWithoutTitle = Map<String, dynamic>.of(_draft)..remove('title');
      await repo.client.updatePage(
        widget.projectId,
        widget.pageId,
        ifMatchVersion: widget.version,
        title: title,
        frontmatter: fmWithoutTitle,
      );
      if (!mounted) return;
      Navigator.of(context).pop(true);
    } catch (e) {
      if (!mounted) return;
      setState(() => _error = e.toString());
    } finally {
      if (mounted) setState(() => _saving = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: Row(
        children: const [
          Icon(Icons.tune, size: 16),
          SizedBox(width: BiuTokens.space2),
          Text(
            '编辑 Frontmatter',
            style: TextStyle(fontSize: 14, fontWeight: FontWeight.w600),
          ),
        ],
      ),
      content: SizedBox(
        width: 560,
        child: SingleChildScrollView(
          child: FrontmatterEditor(
            frontmatter: _draft,
            onChanged: (next) => setState(() => _draft = next),
          ),
        ),
      ),
      actions: [
        if (_error != null)
          Expanded(
            child: Padding(
              padding: const EdgeInsets.only(right: BiuTokens.space2),
              child: Text(
                _error!,
                style: const TextStyle(fontSize: 11, color: BiuTokens.error),
                maxLines: 2,
                overflow: TextOverflow.ellipsis,
              ),
            ),
          ),
        TextButton(
          onPressed: _saving ? null : () => Navigator.of(context).pop(false),
          child: const Text('取消'),
        ),
        FilledButton(
          onPressed: _saving || !_dirty ? null : _save,
          child: _saving
              ? const SizedBox(
                  width: 14,
                  height: 14,
                  child: CircularProgressIndicator(strokeWidth: 2),
                )
              : Text(_dirty ? '保存' : '无修改'),
        ),
      ],
    );
  }
}

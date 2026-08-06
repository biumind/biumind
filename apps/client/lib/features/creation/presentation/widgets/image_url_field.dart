// ImageUrlField — 首帧 / 尾帧 / 参考图 的最小 MVP 上传占位.
//
// MVP: 让用户粘贴 cas:<sha> / https:// URL — 真正的文件选择 + 上传到 CAS
// 走 services/aigc 的上传端点是 P5 的工作 (要等 storage.persist 反向上传通道
// + identity bucket policy). 这里先把 form 字段拉通, UI 显示缩略 + 删除.
//
// 单图模式 (firstFrame / lastFrame): value String? + onChanged.
// 多图模式 (referenceImage): 用 ImageUrlListField (下方).

import 'package:flutter/material.dart';

import '../../../../app/theme.dart';

class ImageUrlField extends StatelessWidget {
  const ImageUrlField({
    super.key,
    required this.label,
    required this.value,
    required this.onChanged,
    this.icon = Icons.image_outlined,
  });

  final String label;
  final String? value;
  final ValueChanged<String?> onChanged;
  final IconData icon;

  @override
  Widget build(BuildContext context) {
    final has = value != null && value!.isNotEmpty;
    return InkWell(
      borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
      onTap: () async {
        final v = await _promptForUrl(context, label, value);
        if (v != null) onChanged(v.isEmpty ? null : v);
      },
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 8),
        decoration: BoxDecoration(
          color: has ? BiuTokens.purpleSoft : BiuTokens.surface,
          border: Border.all(
            color: has ? BiuTokens.purple : BiuTokens.border,
            style: has ? BorderStyle.solid : BorderStyle.solid,
          ),
          borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
        ),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(icon, size: 14, color: has ? BiuTokens.purple : BiuTokens.textSecondary),
            const SizedBox(width: 6),
            Text(
              has ? '$label ✓' : label,
              style: TextStyle(
                fontSize: 12,
                fontWeight: has ? FontWeight.w600 : FontWeight.w500,
                color: has ? BiuTokens.purple : BiuTokens.textSecondary,
              ),
            ),
            if (has) ...[
              const SizedBox(width: 6),
              InkWell(
                onTap: () => onChanged(null),
                child: Icon(Icons.close, size: 14, color: BiuTokens.purple),
              ),
            ],
          ],
        ),
      ),
    );
  }
}

class ImageUrlListField extends StatelessWidget {
  const ImageUrlListField({
    super.key,
    required this.label,
    required this.values,
    required this.onAdd,
    required this.onRemove,
    this.max = 5,
  });

  final String label;
  final List<String> values;
  final ValueChanged<String> onAdd;
  final ValueChanged<String> onRemove;
  final int max;

  @override
  Widget build(BuildContext context) {
    final canAdd = values.length < max;
    return Wrap(
      spacing: 6,
      runSpacing: 6,
      crossAxisAlignment: WrapCrossAlignment.center,
      children: [
        for (final v in values)
          Chip(
            label: Text(_short(v), style: const TextStyle(fontSize: 11)),
            onDeleted: () => onRemove(v),
            deleteIconColor: BiuTokens.textMuted,
            visualDensity: VisualDensity.compact,
            materialTapTargetSize: MaterialTapTargetSize.shrinkWrap,
          ),
        if (canAdd)
          InkWell(
            borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
            onTap: () async {
              final v = await _promptForUrl(context, label, null);
              if (v != null && v.isNotEmpty) onAdd(v);
            },
            child: Container(
              padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 7),
              decoration: BoxDecoration(
                color: BiuTokens.surface,
                border: Border.all(color: BiuTokens.border),
                borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
              ),
              child: Row(
                mainAxisSize: MainAxisSize.min,
                children: [
                  Icon(Icons.add_photo_alternate_outlined,
                      size: 14, color: BiuTokens.textSecondary),
                  const SizedBox(width: 4),
                  Text(
                    '+ $label',
                    style: TextStyle(
                      fontSize: 12,
                      color: BiuTokens.textSecondary,
                      fontWeight: FontWeight.w500,
                    ),
                  ),
                ],
              ),
            ),
          ),
      ],
    );
  }
}

String _short(String url) {
  if (url.startsWith('cas:')) {
    final sha = url.substring(4);
    return 'cas:${sha.length > 6 ? sha.substring(0, 6) : sha}…';
  }
  if (url.length <= 24) return url;
  return '${url.substring(0, 18)}…${url.substring(url.length - 4)}';
}

Future<String?> _promptForUrl(BuildContext context, String label, String? initial) async {
  final ctrl = TextEditingController(text: initial ?? '');
  return showDialog<String?>(
    context: context,
    builder: (ctx) => AlertDialog(
      title: Text(label, style: const TextStyle(fontSize: 16)),
      content: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          TextField(
            controller: ctrl,
            autofocus: true,
            decoration: const InputDecoration(
              hintText: 'cas:<sha> 或 https://…',
            ),
          ),
          const SizedBox(height: 8),
          Text(
            'MVP 阶段先粘贴 URL; 文件上传将在下一版接入.',
            style: TextStyle(fontSize: 11, color: BiuTokens.textMuted),
          ),
        ],
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.of(ctx).pop(null),
          child: const Text('取消'),
        ),
        FilledButton(
          onPressed: () => Navigator.of(ctx).pop(ctrl.text.trim()),
          child: const Text('确认'),
        ),
      ],
    ),
  );
}

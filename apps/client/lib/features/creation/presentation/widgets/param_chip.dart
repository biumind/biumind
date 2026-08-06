// ParamChip — 可点击 chip + 自适应选择器.
//
// 用于 aspect_ratio / resolution / duration / num_outputs / model 等
// 单选参数. value=null 时显示 placeholder; 有 value 时高亮主色边.
//
// 选择器: 屏幕宽度 ≥ 600 → 紧贴 chip 锚点的 popup 菜单 (桌面感);
//        < 600 → 底端 modal sheet (移动 / 小屏).
// 选中后 onChanged 回调.

import 'package:flutter/material.dart';

import '../../../../app/theme.dart';

class ParamChipOption<T> {
  final T value;
  final String label;
  final String? secondary;
  const ParamChipOption({
    required this.value,
    required this.label,
    this.secondary,
  });
}

class ParamChip<T> extends StatelessWidget {
  const ParamChip({
    super.key,
    required this.icon,
    required this.label,
    required this.value,
    required this.options,
    required this.onChanged,
    this.disabled = false,
    this.sheetTitle,
  });

  final IconData icon;
  final String label; // "比例", "时长" 等 placeholder 类
  final T? value;
  final List<ParamChipOption<T>> options;
  final ValueChanged<T> onChanged;
  final bool disabled;
  final String? sheetTitle;

  ParamChipOption<T>? get _selected {
    if (value == null) return null;
    for (final o in options) {
      if (o.value == value) return o;
    }
    return null;
  }

  // GlobalKey 让 popup 锚点能拿到 chip 在屏幕上的几何, 弹在它正下方.
  static final _anchorKeys = Expando<GlobalKey>();
  GlobalKey _anchorKey() {
    final existing = _anchorKeys[this];
    if (existing != null) return existing;
    final k = GlobalKey();
    _anchorKeys[this] = k;
    return k;
  }

  @override
  Widget build(BuildContext context) {
    final sel = _selected;
    final has = sel != null;
    final fg = disabled
        ? BiuTokens.textDisabled
        : (has ? BiuTokens.text : BiuTokens.textSecondary);
    final bg = has ? BiuTokens.purpleSoft : BiuTokens.surface;
    final border = has ? BiuTokens.purple : BiuTokens.border;

    return Material(
      key: _anchorKey(),
      color: bg,
      borderRadius: BorderRadius.circular(BiuTokens.radiusFull),
      child: InkWell(
        borderRadius: BorderRadius.circular(BiuTokens.radiusFull),
        onTap: disabled || options.isEmpty ? null : () => _open(context),
        child: Container(
          padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 7),
          decoration: BoxDecoration(
            border: Border.all(color: border, width: has ? 1.2 : 1),
            borderRadius: BorderRadius.circular(BiuTokens.radiusFull),
          ),
          child: Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              Icon(icon, size: 14, color: fg),
              const SizedBox(width: 6),
              Text(
                has ? sel.label : label,
                style: TextStyle(
                  fontSize: 12,
                  fontWeight: has ? FontWeight.w600 : FontWeight.w500,
                  color: fg,
                ),
              ),
              const SizedBox(width: 4),
              Icon(Icons.expand_more, size: 14, color: fg),
            ],
          ),
        ),
      ),
    );
  }

  Future<void> _open(BuildContext context) async {
    final isDesktop = MediaQuery.of(context).size.width >= 600;
    final T? picked;
    if (isDesktop) {
      picked = await _openPopup(context);
    } else {
      picked = await _openSheet(context);
    }
    if (picked != null) onChanged(picked);
  }

  Future<T?> _openSheet(BuildContext context) {
    return showModalBottomSheet<T>(
      context: context,
      backgroundColor: BiuTokens.surface,
      shape: const RoundedRectangleBorder(
        borderRadius:
            BorderRadius.vertical(top: Radius.circular(BiuTokens.radiusLg)),
      ),
      builder: (_) => _OptionsSheet<T>(
        title: sheetTitle ?? label,
        options: options,
        current: value,
      ),
    );
  }

  /// 桌面端: chip 正下方弹一个 320×N 的 Material 卡片. 用 RelativeRect
  /// 锚定 chip 的位置 (与系统 PopupMenu 同款定位).
  Future<T?> _openPopup(BuildContext context) async {
    final overlay =
        Overlay.of(context).context.findRenderObject() as RenderBox?;
    final anchorBox =
        _anchorKey().currentContext?.findRenderObject() as RenderBox?;
    if (overlay == null || anchorBox == null) {
      return _openSheet(context); // fallback
    }
    final anchorTopLeft = anchorBox.localToGlobal(Offset.zero, ancestor: overlay);
    final position = RelativeRect.fromLTRB(
      anchorTopLeft.dx,
      anchorTopLeft.dy + anchorBox.size.height + 6,
      overlay.size.width - anchorTopLeft.dx - 320,
      0,
    );
    return showMenu<T>(
      context: context,
      position: position,
      color: BiuTokens.surface,
      elevation: 8,
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(BiuTokens.radiusMd),
        side: BorderSide(color: BiuTokens.borderSubtle),
      ),
      items: [
        // 用一个 PopupMenuItem 撑出整块 — 内部 ListView 自带滚动 / 选中.
        PopupMenuItem<T>(
          enabled: false,
          padding: EdgeInsets.zero,
          child: SizedBox(
            width: 320,
            child: _OptionsSheet<T>(
              title: sheetTitle ?? label,
              options: options,
              current: value,
            ),
          ),
        ),
      ],
    );
  }
}

class _OptionsSheet<T> extends StatelessWidget {
  const _OptionsSheet({
    required this.title,
    required this.options,
    required this.current,
  });

  final String title;
  final List<ParamChipOption<T>> options;
  final T? current;

  @override
  Widget build(BuildContext context) {
    return SafeArea(
      child: Padding(
        padding: const EdgeInsets.symmetric(vertical: 16),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Padding(
              padding: const EdgeInsets.symmetric(horizontal: 20, vertical: 4),
              child: Text(
                title,
                style: TextStyle(
                  fontSize: 14,
                  fontWeight: FontWeight.w600,
                  color: BiuTokens.text,
                ),
              ),
            ),
            const SizedBox(height: 8),
            ConstrainedBox(
              constraints: const BoxConstraints(maxHeight: 360),
              child: ListView.builder(
                shrinkWrap: true,
                itemCount: options.length,
                itemBuilder: (_, i) {
                  final o = options[i];
                  final selected = o.value == current;
                  return ListTile(
                    dense: true,
                    title: Text(
                      o.label,
                      style: TextStyle(
                        fontSize: 14,
                        fontWeight: selected ? FontWeight.w600 : FontWeight.w500,
                        color: selected ? BiuTokens.purple : BiuTokens.text,
                      ),
                    ),
                    subtitle: o.secondary == null
                        ? null
                        : Text(
                            o.secondary!,
                            style: TextStyle(fontSize: 11, color: BiuTokens.textMuted),
                          ),
                    trailing: selected
                        ? Icon(Icons.check, size: 18, color: BiuTokens.purple)
                        : null,
                    onTap: () => Navigator.of(context).pop(o.value),
                  );
                },
              ),
            ),
          ],
        ),
      ),
    );
  }
}

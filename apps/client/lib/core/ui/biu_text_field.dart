// BiuTextField — TextField 包装,加 prototype 风格的 focus halo (外圈光晕)。
//
// 对应 prototype:
//   .input {
//     border: 1px solid var(--border-soft);
//     transition: border-color 160ms;
//   }
//   .input:focus {
//     border-color: var(--brand);
//     box-shadow: 0 0 0 3px var(--brand-soft);  /* ← Material InputDecoration 不支持 */
//   }
//
// Flutter `InputDecorationTheme.focusedBorder` 只能换 border 颜色,无法加外圈
// 阴影 (那是 Container 的 boxShadow,不是 OutlineInputBorder 的能力)。
//
// 解法:包一层 Container,监听 FocusNode 状态,focused 时 boxShadow: focusHalo(brand)。
// 内部 TextField 走主题默认装饰即可。
//
// 用法:
//   BiuTextField(
//     controller: _controller,
//     hintText: '搜索...',
//     onSubmitted: (v) {},
//   )
//
// 跟 TextField 完全兼容(转发主要参数);需要更复杂的 InputDecoration,直接
// 传 decoration 参数 override。

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../../app/theme/effects.dart';
import '../../app/theme/extensions.dart';
import '../../app/theme/tokens.dart';

class BiuTextField extends StatefulWidget {
  const BiuTextField({
    super.key,
    this.controller,
    this.focusNode,
    this.hintText,
    this.labelText,
    this.prefixIcon,
    this.suffixIcon,
    this.obscureText = false,
    this.keyboardType,
    this.textInputAction,
    this.onChanged,
    this.onSubmitted,
    this.onTap,
    this.maxLines = 1,
    this.minLines,
    this.enabled = true,
    this.autofocus = false,
    this.decoration,
    this.inputFormatters,
    this.autofillHints,
    this.maxLength,
    this.textAlign = TextAlign.start,
    this.style,
  });

  final TextEditingController? controller;
  final FocusNode? focusNode;
  final String? hintText;
  final String? labelText;
  final Widget? prefixIcon;
  final Widget? suffixIcon;
  final bool obscureText;
  final TextInputType? keyboardType;
  final TextInputAction? textInputAction;
  final ValueChanged<String>? onChanged;
  final ValueChanged<String>? onSubmitted;
  final VoidCallback? onTap;
  final int? maxLines;
  final int? minLines;
  final bool enabled;
  final bool autofocus;

  /// 完全 override 默认 decoration(配合主题已经够用,这个一般不用传)。
  final InputDecoration? decoration;

  /// 透传到内部 TextField — 数字键盘、长度上限、密码自动填充等场景需要。
  final List<TextInputFormatter>? inputFormatters;
  final Iterable<String>? autofillHints;
  final int? maxLength;
  final TextAlign textAlign;
  final TextStyle? style;

  @override
  State<BiuTextField> createState() => _BiuTextFieldState();
}

class _BiuTextFieldState extends State<BiuTextField> {
  late final FocusNode _focusNode;
  bool _ownNode = false;

  @override
  void initState() {
    super.initState();
    if (widget.focusNode == null) {
      _focusNode = FocusNode();
      _ownNode = true;
    } else {
      _focusNode = widget.focusNode!;
    }
    _focusNode.addListener(_onFocus);
  }

  @override
  void dispose() {
    _focusNode.removeListener(_onFocus);
    if (_ownNode) _focusNode.dispose();
    super.dispose();
  }

  void _onFocus() {
    if (mounted) setState(() {});
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final c = theme.extension<BiuColors>();
    final brand = c?.brand ?? theme.colorScheme.primary;
    final radius = BorderRadius.circular(RadiusTokens.md);

    return AnimatedContainer(
      duration: const Duration(milliseconds: 160),
      curve: MotionTokens.standard,
      decoration: BoxDecoration(
        borderRadius: radius,
        boxShadow: _focusNode.hasFocus ? focusHalo(brand) : null,
      ),
      child: TextField(
        controller: widget.controller,
        focusNode: _focusNode,
        decoration: widget.decoration ??
            InputDecoration(
              hintText: widget.hintText,
              labelText: widget.labelText,
              prefixIcon: widget.prefixIcon,
              suffixIcon: widget.suffixIcon,
            ),
        obscureText: widget.obscureText,
        keyboardType: widget.keyboardType,
        textInputAction: widget.textInputAction,
        onChanged: widget.onChanged,
        onSubmitted: widget.onSubmitted,
        onTap: widget.onTap,
        maxLines: widget.maxLines,
        minLines: widget.minLines,
        enabled: widget.enabled,
        autofocus: widget.autofocus,
        inputFormatters: widget.inputFormatters,
        autofillHints: widget.autofillHints,
        maxLength: widget.maxLength,
        textAlign: widget.textAlign,
        style: widget.style,
      ),
    );
  }
}

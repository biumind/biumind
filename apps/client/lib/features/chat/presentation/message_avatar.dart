// MessageAvatar — 24×24 圆形头像, 用于 chat 消息行 leading 区。
//
// 两个 named constructor 区分 user / assistant 来源:
//   * `MessageAvatar.assistant(model: ...)` — 按 provider 显示 logo 字符
//   * `MessageAvatar.user(email: ..., name: ...)` — 用户首字符
//
// 故意保持轻量 — 只渲染 1-2 字 + 背景色, 不引图片不做缓存。tooltip
// 显示 model 全名, 长按 / hover 都生效 (Flutter 默认行为)。

import 'package:flutter/material.dart';

import '../../../app/theme.dart';
import '../domain/avatar_meta.dart';

class MessageAvatar extends StatelessWidget {
  const MessageAvatar._({
    required this.meta,
    this.tooltip,
    this.size = 24,
  });

  /// Assistant 头像 — model id 决定 logo 字符 + 背景色。
  factory MessageAvatar.assistant({
    String? model,
    double size = 24,
  }) {
    final meta = resolveAssistantAvatar(model);
    return MessageAvatar._(
      meta: meta,
      tooltip: model,
      size: size,
    );
  }

  /// User 头像 — 优先 name, 退到 email; 都没有时通用人形图标。
  factory MessageAvatar.user({
    String? email,
    String? name,
    double size = 24,
  }) {
    final meta = resolveUserAvatar(email: email, name: name);
    return MessageAvatar._(
      meta: meta,
      tooltip: name ?? email,
      size: size,
    );
  }

  final AvatarMeta meta;
  final String? tooltip;
  final double size;

  @override
  Widget build(BuildContext context) {
    // emoji label 用 size*0.6 (emoji 字面值天生占满 em-box, 缩点更协调);
    // 字符 label 用 size*0.5。
    final isEmoji = meta.label.runes.first > 0x1F000;
    final fontSize = isEmoji ? size * 0.6 : size * 0.5;
    final widget = Container(
      width: size,
      height: size,
      decoration: BoxDecoration(
        color: meta.background,
        borderRadius: BorderRadius.circular(size / 2),
        border: Border.all(color: BiuTokens.borderSubtle, width: 0.5),
      ),
      alignment: Alignment.center,
      child: Text(
        meta.label,
        style: TextStyle(
          fontSize: fontSize,
          fontWeight: FontWeight.w600,
          color: meta.foreground,
          height: 1.0,
        ),
      ),
    );
    final tip = tooltip;
    if (tip == null || tip.isEmpty) return widget;
    return Tooltip(
      message: tip,
      waitDuration: const Duration(milliseconds: 400),
      child: widget,
    );
  }
}

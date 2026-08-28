// 双格式剪贴板（P2）：编辑器「复制」富内容（表格/代码块等）时把
// text(markdown) + html 同时写进系统剪贴板，粘到 Word/飞书/邮件等外部
// 应用保留格式。Flutter Clipboard 只支持纯文本，html 走 method channel
// 落到 macOS NSPasteboard（string + html 两个 representation）。
// 平台分流收敛在本文件（C5）：只有 macOS（PlatformCaps.hasRichClipboard）
// 走 channel，其余平台/通道失败一律返回 false，调用方回退纯文本。

import 'dart:convert' show base64Encode;

import 'package:flutter/services.dart';

import 'platform_caps.dart';

const MethodChannel _channel = MethodChannel('biumind/clipboard');

/// 写系统剪贴板双格式。返回 true = 双格式已写入；false = 平台不支持 /
/// 通道失败（调用方回退 Clipboard.setData 纯文本）。
Future<bool> writeRichClipboard(String text, String html) async {
  if (!PlatformCaps.detect().hasRichClipboard) return false;
  try {
    await _channel.invokeMethod<void>('writeRich', {'text': text, 'html': html});
    return true;
  } catch (_) {
    // channel 未注册（老原生壳 / 非 macOS 误配）——回退纯文本，不崩
    return false;
  }
}

/// 写系统剪贴板图片格式（单图复制）：PNG 本体 + text 兜底（+ 可选 html），
/// 粘到微信/备忘录/Word 直接出图。返回 true = 已写入；false = 平台不支持 /
/// 通道失败（调用方回退双格式/纯文本）。
Future<bool> writeImageClipboard(
  String text,
  String? html,
  Uint8List pngBytes,
) async {
  if (!PlatformCaps.detect().hasRichClipboard) return false;
  try {
    await _channel.invokeMethod<void>('writeImage', {
      'text': text,
      'html': ?html,
      'imageBase64': base64Encode(pngBytes),
    });
    return true;
  } catch (_) {
    // channel 未注册（老原生壳）——回退，不崩
    return false;
  }
}

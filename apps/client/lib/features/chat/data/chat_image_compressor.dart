// 聊天附件图片压缩 —— 发送前统一把图压到链路友好尺寸。
//
// 背景：图片 base64 内联进 `POST /v1/agent/sessions` 的 JSON body
// （体积 +33%）。链路上的大小约束：
//   * site nginx `client_max_body_size 20m`（不配置时默认 1m，曾是 413 根因）
//   * brain 契约单图 ≤1MB（services/brain internal/agentplane/router.go
//     Images 字段注释）
//   * Claude API 单图 5MB、智谱 GLM-4V 5MB、Qwen-VL base64 后 10MB
//   * Claude 服务端会把长边 >1568px 的图降采样到 ~1.15MP —— 发原图对
//     模型效果零增益，只费 token / 带宽 / 延迟
//
// 所以这里把所有入口（picker / 拖拽 / 粘贴）汇入的图统一：
//   长边 ≤1568px + JPEG 重压到 ≤1MB。
//
// 纯同步函数（不碰 async / isolate），方便直接丢给 `compute()` 跑；
// 也保证单测不依赖 Flutter binding。

import 'dart:typed_data';

import 'package:image/image.dart' as img;

/// 压缩目标：单图 ≤1MB（brain 契约 + 各厂商 5MB 下限的充分安全值）。
const int kChatImageTargetBytes = 1 * 1024 * 1024;

/// 缩放长边：对齐 Claude 最优输入尺寸 1568px。
const int kChatImageMaxEdge = 1568;

/// 直通阈值：≤1MB 且长边 ≤2000px 的图不重编码（保留 PNG 透明通道、
/// 避免小图二次有损）。
const int kChatImagePassthroughEdge = 2000;

/// 压缩结果。未重编码时 [bytes]/[mime]/[name] 与输入一致。
class CompressedImage {
  const CompressedImage({
    required this.bytes,
    required this.mime,
    required this.name,
    required this.reencoded,
  });

  final Uint8List bytes;
  final String mime;
  final String name;
  final bool reencoded;
}

/// `compute()` 入口：isolate 间只能传单参数，打个 record 包一层。
CompressedImage compressChatImageEntry(
  ({Uint8List bytes, String name, String mime}) args,
) =>
    compressChatImage(bytes: args.bytes, name: args.name, mime: args.mime);

/// 压缩一张聊天附件图。规则（按序）：
///
/// 1. GIF 原样放行 —— 重编码会杀掉动画（仍受 composer 10MB 硬上限约束）。
/// 2. 解码失败（HEIC 等 image 包不支持的格式）原样放行 —— 不阻塞用户，
///    交给下游大小校验与错误处理。
/// 3. 已达标（≤1MB 且长边 ≤2000px）原样放行。
/// 4. 否则长边等比缩到 1568px，JPEG q85 → 70 → 50 阶梯重压；极端情况
///    再缩到 1024px 压一轮。
CompressedImage compressChatImage({
  required Uint8List bytes,
  required String name,
  required String mime,
}) {
  CompressedImage passthrough() =>
      CompressedImage(bytes: bytes, mime: mime, name: name, reencoded: false);

  if (mime == 'image/gif') return passthrough();

  final decoded = img.decodeImage(bytes);
  if (decoded == null) return passthrough();

  final longEdge =
      decoded.width >= decoded.height ? decoded.width : decoded.height;
  if (bytes.length <= kChatImageTargetBytes &&
      longEdge <= kChatImagePassthroughEdge) {
    return passthrough();
  }

  var cur = decoded;
  if (longEdge > kChatImageMaxEdge) {
    cur = decoded.width >= decoded.height
        ? img.copyResize(decoded, width: kChatImageMaxEdge)
        : img.copyResize(decoded, height: kChatImageMaxEdge);
  }

  var out = Uint8List.fromList(img.encodeJpg(cur, quality: 85));
  if (out.length > kChatImageTargetBytes) {
    out = Uint8List.fromList(img.encodeJpg(cur, quality: 70));
  }
  if (out.length > kChatImageTargetBytes) {
    out = Uint8List.fromList(img.encodeJpg(cur, quality: 50));
  }
  if (out.length > kChatImageTargetBytes && cur.width > 1024) {
    // 极端情况（高噪点大图）：再缩一档重压。
    cur = cur.width >= cur.height
        ? img.copyResize(cur, width: 1024)
        : img.copyResize(cur, height: 1024);
    out = Uint8List.fromList(img.encodeJpg(cur, quality: 75));
  }

  return CompressedImage(
    bytes: out,
    mime: 'image/jpeg',
    name: _toJpgName(name),
    reencoded: true,
  );
}

/// 重编码后扩展名改 .jpg，保持附件名与真实格式一致。
String _toJpgName(String name) {
  final dot = name.lastIndexOf('.');
  final stem = dot > 0 ? name.substring(0, dot) : name;
  return '$stem.jpg';
}

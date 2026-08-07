// chat_image_compressor 行为测试。
//
// 覆盖四条规则：GIF 直通 / 解码失败直通 / 已达标直通 / 大图缩放重压。
// 测试图用 image 包构造（平滑渐变保证 JPEG 可压到 1MB 以内）。

import 'dart:typed_data';

import 'package:biumind/features/chat/data/chat_image_compressor.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:image/image.dart' as img;

/// 构造一张平滑渐变的 JPEG（可压缩性好，避免噪点图压不进 1MB 造成
/// 测试不稳定）。
Uint8List gradientJpeg(int w, int h, {int quality = 95}) {
  final im = img.Image(width: w, height: h);
  for (var y = 0; y < h; y++) {
    for (var x = 0; x < w; x++) {
      im.setPixelRgb(
        x,
        y,
        (x * 255) ~/ w,
        (y * 255) ~/ h,
        ((x + y) * 255) ~/ (w + h),
      );
    }
  }
  return Uint8List.fromList(img.encodeJpg(im, quality: quality));
}

void main() {
  group('compressChatImage', () {
    test('大图：长边缩到 1568、≤1MB、转 image/jpeg、改名 .jpg', () {
      final bytes = gradientJpeg(4096, 3072);
      final c = compressChatImage(
        bytes: bytes,
        name: 'rainbow.png',
        mime: 'image/png',
      );
      expect(c.reencoded, isTrue);
      expect(c.mime, 'image/jpeg');
      expect(c.name, 'rainbow.jpg');
      expect(c.bytes.length, lessThanOrEqualTo(kChatImageTargetBytes));
      final decoded = img.decodeImage(c.bytes)!;
      final longEdge =
          decoded.width >= decoded.height ? decoded.width : decoded.height;
      expect(longEdge, kChatImageMaxEdge);
    });

    test('竖图：按高缩放，宽高比保持', () {
      final bytes = gradientJpeg(2000, 4000);
      final c = compressChatImage(
        bytes: bytes,
        name: 'tall.jpg',
        mime: 'image/jpeg',
      );
      expect(c.reencoded, isTrue);
      final decoded = img.decodeImage(c.bytes)!;
      expect(decoded.height, kChatImageMaxEdge);
      expect(decoded.width, closeTo(784, 2));
    });

    test('已达标小图原样直通（bytes 引用相同）', () {
      final small = img.Image(width: 100, height: 80);
      img.fill(small, color: img.ColorRgb8(255, 0, 0));
      final bytes = Uint8List.fromList(img.encodePng(small));
      final c = compressChatImage(
        bytes: bytes,
        name: 'shot.png',
        mime: 'image/png',
      );
      expect(c.reencoded, isFalse);
      expect(identical(c.bytes, bytes), isTrue);
      expect(c.mime, 'image/png');
      expect(c.name, 'shot.png');
    });

    test('GIF 原样直通（保留动画）', () {
      final g = img.Image(width: 64, height: 64);
      img.fill(g, color: img.ColorRgb8(0, 255, 0));
      final bytes = Uint8List.fromList(img.encodeGif(g));
      final c = compressChatImage(
        bytes: bytes,
        name: 'anim.gif',
        mime: 'image/gif',
      );
      expect(c.reencoded, isFalse);
      expect(identical(c.bytes, bytes), isTrue);
    });

    test('解码失败（伪 HEIC）原样直通不抛异常', () {
      final bytes = Uint8List.fromList(List.generate(2048, (i) => i % 251));
      final c = compressChatImage(
        bytes: bytes,
        name: 'photo.heic',
        mime: 'image/heic',
      );
      expect(c.reencoded, isFalse);
      expect(identical(c.bytes, bytes), isTrue);
      expect(c.mime, 'image/heic');
    });

    test('无扩展名文件重编码后补 .jpg', () {
      final bytes = gradientJpeg(4096, 3072);
      final c = compressChatImage(bytes: bytes, name: 'pasted', mime: 'image/png');
      expect(c.name, 'pasted.jpg');
    });
  });
}

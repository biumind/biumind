// ignore_for_file: experimental_member_use
// (just_audio 0.9.x 的 StreamAudioSource/Response 标 experimental, 但是
// API 稳定的正式 byte-source playback 路径; 1.0 会去掉这个标记)
//
// BytesAudioSource — 让 just_audio 从内存 Uint8List 直接播放, 不落临时文件.
//
// just_audio 0.9.x 标准做法: 实现 StreamAudioSource.request, 一次性返完整
// 字节即可 (TTS mp3 通常 ~ 几百 KB, 内存里完全没问题, 不需要分段).
//
// 复用点:
//   - RSS Today 简报播放 (features/apps/builtin/rss/widgets/briefing_button.dart)
//   - 聊天消息朗读 (features/chat/application/tts_controller.dart, cosyvoice 路径)

import 'dart:typed_data';

import 'package:just_audio/just_audio.dart';

class BytesAudioSource extends StreamAudioSource {
  BytesAudioSource(this._bytes, {required this.mimeType});

  final Uint8List _bytes;
  final String mimeType;

  @override
  Future<StreamAudioResponse> request([int? start, int? end]) async {
    start ??= 0;
    end ??= _bytes.length;
    return StreamAudioResponse(
      sourceLength: _bytes.length,
      contentLength: end - start,
      offset: start,
      stream: Stream.value(_bytes.sublist(start, end)),
      contentType: mimeType,
    );
  }
}

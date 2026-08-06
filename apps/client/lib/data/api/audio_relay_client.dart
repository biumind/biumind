// AudioRelayClient — model-relay 多模态音频网关 (/v1/audio/*) 的瘦封装.
//
// 与 services/aigc 的 AigcClient (原生 AIGC 任务系统: gallery / 角色 / 我的
// 作品) 是**两个独立 surface**:
//   - AigcClient    → services/aigc, 异步任务 + 画廊
//   - AudioRelayClient → model-relay :7001, OpenAI 兼容直通端点
//
// 当前覆盖 (P0a):
//   POST /v1/audio/speech   OpenAI 兼容 TTS, 返 chunked 二进制音频 (audio/mpeg
//                            等). 上游 cosyvoice SSE 在服务端解析合并, 客户端
//                            只拿裸音频字节 → 喂 just_audio 播放.
//
// 后续 (P0b) 会加 /v1/audio/transcriptions (STT). 凭据走 hubCredentialsProvider
// (model-relay endpoint + JWT), 与 chat / wiki client 同源.

import 'dart:typed_data';

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../services/auth_service.dart';
import '_http_helpers.dart';

class AudioRelayClient {
  final Uri baseUrl;
  final String? Function() bearerProvider;

  AudioRelayClient({required this.baseUrl, required this.bearerProvider});

  Uri _uri(String path) {
    final base = baseUrl.toString().replaceAll(RegExp(r'/+$'), '');
    return Uri.parse('$base$path');
  }

  /// 合成语音. 返回完整音频字节 (默认 mp3 = audio/mpeg).
  ///
  /// [format] 对齐 OpenAI: mp3 / wav / pcm / opus / aac / flac. cosyvoice
  /// 默认 mp3. [speed] 0 时不传 (走上游默认 1.0).
  ///
  /// 失败 (4xx/5xx) 抛 [ApiError], body 是服务端 JSON 错误体. 上游静默无音频
  /// 时服务端会转 502 upstream_no_audio, 同样以 ApiError 抛出.
  Future<Uint8List> synthesizeSpeech({
    required String model,
    required String input,
    required String voice,
    String format = 'mp3',
    double? speed,
    int? sampleRate,
  }) async {
    final body = <String, dynamic>{
      'model': model,
      'input': input,
      'voice': voice,
      'response_format': format,
      if (speed != null && speed > 0) 'speed': speed,
      if (sampleRate != null && sampleRate > 0) 'sample_rate': sampleRate,
    };
    return binaryRequest(
      method: 'POST',
      url: _uri('/v1/audio/speech'),
      bearerToken: bearerProvider(),
      body: body,
      accept: 'audio/*',
    );
  }
}

/// AudioRelayClient — 未登录 / 未配 model-relay 凭据时返 null (调用方据此
/// 回落本地 flutter_tts).
final audioRelayClientProvider = Provider<AudioRelayClient?>((ref) {
  final creds = ref.watch(hubCredentialsProvider);
  if (creds == null) return null;
  return AudioRelayClient(
    baseUrl: creds.endpoint,
    bearerProvider: () => creds.bearerToken,
  );
});

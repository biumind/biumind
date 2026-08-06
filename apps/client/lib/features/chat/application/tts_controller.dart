// TtsController —— 单条消息朗读，跨平台。
// 同时只朗读一条；点同条切停止；点别条切换到新条。
//
// 双引擎，云优先 + 本地兜底（用户在「设置 > 智能体 > 聊天 > 语音朗读」配了
// 云端 TTS 模型 + 音色时优先走云端，否则/失败回落设备本地）：
//   - 云端: model-relay /v1/audio/speech (cosyvoice 高质量) → just_audio 播放
//           内存音频字节 (复用 RSS 简报同款 BytesAudioSource)。
//   - 本地: flutter_tts 设备系统合成 (离线、免费、机械音)。
//
// 状态 = 正在朗读的 messageId（null = 没在朗读）；云端/本地哪条引擎在响都
// 由同一个 state 表达，UI（_SpeakBtn）只看 activeId == messageId。

import 'dart:async';

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_tts/flutter_tts.dart';
import 'package:just_audio/just_audio.dart';

import '../../../core/audio/bytes_audio_source.dart';
import '../../../data/api/audio_relay_client.dart';
import 'chat_preferences.dart';

class TtsController extends StateNotifier<String?> {
  TtsController(this._ref) : super(null) {
    _tts.setCompletionHandler(_onLocalDone);
    _tts.setCancelHandler(_onLocalDone);
    _tts.setErrorHandler((_) => _onLocalDone());
  }

  final Ref _ref;
  final FlutterTts _tts = FlutterTts();

  // 云端播放器与其状态订阅 —— 每次播放新建、停止/切换时释放。
  AudioPlayer? _player;
  StreamSubscription<PlayerState>? _playerSub;

  void _onLocalDone() {
    if (mounted) state = null;
  }

  Future<void> speak({
    required String messageId,
    required String text,
    String localeTag = 'zh-CN',
  }) async {
    if (text.trim().isEmpty) return;
    await _stopAll();
    state = messageId;

    final prefs = _ref.read(chatPreferencesProvider);
    final client = _ref.read(audioRelayClientProvider);
    if (prefs.cloudTtsConfigured && client != null) {
      final ok = await _speakCloud(
        client,
        messageId: messageId,
        model: prefs.ttsModel!,
        voice: prefs.ttsVoice!,
        text: text,
      );
      if (ok) return;
      // 云端失败（网络 / 余额 / 配置错） → 不让用户白点，回落本地朗读。
      // state 仍是 messageId，继续本地。
    }
    await _speakLocal(text, localeTag);
  }

  /// 返回 true = 云端已开始播放（或被新请求取代，不需回落）；
  /// false = 失败（caller 回落本地）。
  Future<bool> _speakCloud(
    AudioRelayClient client, {
    required String messageId,
    required String model,
    required String voice,
    required String text,
  }) async {
    try {
      final bytes = await client.synthesizeSpeech(
        model: model,
        input: text,
        voice: voice,
        format: 'mp3',
      );
      // 合成期间用户已停止 / 切到别条 / controller 已释放 → 丢弃这次结果，
      // 不要"点了停止却响起来"。返 true 抑制本地回落。
      if (!mounted || state != messageId) return true;
      final player = AudioPlayer();
      _player = player;
      _playerSub = player.playerStateStream.listen((s) {
        if (s.processingState == ProcessingState.completed) {
          if (mounted && state == messageId) state = null;
        }
      });
      await player.setAudioSource(BytesAudioSource(bytes, mimeType: 'audio/mpeg'));
      // 不 await 到播放结束 —— 完成由 playerStateStream.completed 重置 state；
      // 播放期异常单独 catch 回落本地。
      unawaited(player.play().catchError((_) {
        if (mounted && state == messageId) state = null;
      }));
      return true;
    } catch (_) {
      await _disposePlayer();
      return false;
    }
  }

  Future<void> _speakLocal(String text, String localeTag) async {
    try {
      await _tts.setLanguage(localeTag);
      await _tts.setSpeechRate(0.5);
      await _tts.speak(text);
    } catch (_) {
      if (mounted) state = null;
    }
  }

  Future<void> _disposePlayer() async {
    await _playerSub?.cancel();
    _playerSub = null;
    await _player?.dispose();
    _player = null;
  }

  Future<void> _stopAll() async {
    await _tts.stop();
    await _disposePlayer();
  }

  Future<void> stop() async {
    await _stopAll();
    state = null;
  }

  @override
  void dispose() {
    _tts.stop();
    _playerSub?.cancel();
    _player?.dispose();
    super.dispose();
  }
}

final ttsControllerProvider =
    StateNotifierProvider<TtsController, String?>((ref) => TtsController(ref));

/// 启发式 locale 选择：中文字符占比 > 30% 选 zh-CN，否则 en-US。
String detectTtsLocale(String text) {
  if (text.isEmpty) return 'en-US';
  var zh = 0;
  for (final r in text.runes) {
    if (r >= 0x4E00 && r <= 0x9FFF) zh++;
  }
  return zh / text.length > 0.3 ? 'zh-CN' : 'en-US';
}

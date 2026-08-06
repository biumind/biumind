// M8.4 RSS Today 简报播放按钮.
//
// 状态机 (idle → loading → playing → paused → idle):
//   idle:    🎙 简报      → 点 → loading
//   loading: ⏳ 合成中…   (调 briefing_today_audio, ~1.5s 上游)
//   playing: ⏸ 0:08/0:30  → 点 → paused
//   paused:  ▶ 0:08/0:30  → 点 → playing
//
// just_audio 直接从 Uint8List 播放: 包一个 _BytesAudioSource (StreamAudioSource).
// mp3 ~ 100KB, 内存里完全没问题, 不需要落临时文件.
//
// cached 标记不影响 UI — 用户不关心是缓存还是新合成, 体验都一样;
// 只在 inspector 长按时显示出来 (debug aid).

import 'dart:async';
import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:just_audio/just_audio.dart';

import '../../../../../app/theme.dart';
import '../../../../../core/audio/bytes_audio_source.dart';
import '../providers.dart';

class BriefingButton extends ConsumerStatefulWidget {
  const BriefingButton({super.key});
  @override
  ConsumerState<BriefingButton> createState() => _BriefingButtonState();
}

class _BriefingButtonState extends ConsumerState<BriefingButton> {
  AudioPlayer? _player;
  bool _loading = false;
  String? _errorMsg;
  Duration _position = Duration.zero;
  Duration _duration = Duration.zero;
  bool _playing = false;
  StreamSubscription<PlayerState>? _stateSub;
  StreamSubscription<Duration>? _posSub;
  StreamSubscription<Duration?>? _durSub;
  String? _meta; // "cached · cosyvoice-v3-plus · 13 字" — 长按显示

  @override
  void dispose() {
    _stateSub?.cancel();
    _posSub?.cancel();
    _durSub?.cancel();
    _player?.dispose();
    super.dispose();
  }

  Future<void> _onTap() async {
    if (_loading) return;
    if (_player != null) {
      // 已加载, 切换 play/pause
      if (_playing) {
        await _player!.pause();
      } else {
        // 播完后重新点是从头播
        if (_position >= _duration && _duration > Duration.zero) {
          await _player!.seek(Duration.zero);
        }
        await _player!.play();
      }
      return;
    }

    // 第一次播 — 拉 audio bytes
    setState(() {
      _loading = true;
      _errorMsg = null;
    });
    try {
      final api = ref.read(rssApiProvider);
      if (api == null) {
        throw Exception('未登录');
      }
      final r = await api.invoke('briefing_today_audio');
      final result = (r['result'] as Map?)?.cast<String, dynamic>() ?? r;
      final b64 = result['audio_b64'] as String?;
      if (b64 == null || b64.isEmpty) {
        throw Exception('后端未返回音频');
      }
      final bytes = base64Decode(b64);
      final cached = result['cached'] == true;
      final model = result['model'] ?? 'cosyvoice';
      final chars = result['characters'] ?? 0;
      _meta = '${cached ? "缓存" : "新合成"} · $model · $chars 字';

      final player = AudioPlayer();
      _player = player;
      _stateSub = player.playerStateStream.listen((s) {
        if (!mounted) return;
        setState(() => _playing = s.playing && s.processingState != ProcessingState.completed);
      });
      _posSub = player.positionStream.listen((p) {
        if (!mounted) return;
        setState(() => _position = p);
      });
      _durSub = player.durationStream.listen((d) {
        if (!mounted || d == null) return;
        setState(() => _duration = d);
      });
      await player.setAudioSource(BytesAudioSource(bytes, mimeType: 'audio/mpeg'));
      await player.play();
    } catch (e) {
      if (mounted) setState(() => _errorMsg = '$e');
    } finally {
      if (mounted) setState(() => _loading = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;

    if (_errorMsg != null && _player == null) {
      return _ErrorButton(
        msg: _errorMsg!,
        onRetry: () {
          setState(() => _errorMsg = null);
          _onTap();
        },
      );
    }

    final hasPlayer = _player != null;
    final showProgress = hasPlayer && _duration > Duration.zero;

    return GestureDetector(
      onLongPress: _meta == null
          ? null
          : () => ScaffoldMessenger.of(context).showSnackBar(
                SnackBar(
                    content: Text(_meta!), duration: const Duration(seconds: 2)),
              ),
      child: FilledButton.tonalIcon(
        onPressed: _loading ? null : _onTap,
        icon: _loading
            ? const SizedBox(
                width: 14,
                height: 14,
                child: CircularProgressIndicator(strokeWidth: 2))
            : Icon(
                hasPlayer
                    ? (_playing ? Icons.pause : Icons.play_arrow)
                    : Icons.headphones,
                size: 16,
                color: scheme.primary,
              ),
        label: Text(
          _loading
              ? '合成中…'
              : showProgress
                  ? '${_fmtDur(_position)} / ${_fmtDur(_duration)}'
                  : '简报',
        ),
        style: FilledButton.styleFrom(
          visualDensity: VisualDensity.compact,
          textStyle: const TextStyle(fontSize: 12),
        ),
      ),
    );
  }
}

class _ErrorButton extends StatelessWidget {
  const _ErrorButton({required this.msg, required this.onRetry});
  final String msg;
  final VoidCallback onRetry;

  @override
  Widget build(BuildContext context) {
    return TextButton.icon(
      onPressed: onRetry,
      icon: const Icon(Icons.error_outline, size: 16, color: Colors.redAccent),
      label: Text(
        '简报失败 · 点击重试',
        style: TextStyle(fontSize: 11, color: BiuTokens.textSecondary),
      ),
    );
  }
}

String _fmtDur(Duration d) {
  final s = d.inSeconds;
  return '${s ~/ 60}:${(s % 60).toString().padLeft(2, '0')}';
}

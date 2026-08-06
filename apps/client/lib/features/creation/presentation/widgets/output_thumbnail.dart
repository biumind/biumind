// OutputThumbnail — 任务输出图 / 视频封面渲染器.
//
// 职责:
//   1. cas:<sha> → services/aigc + Bearer 拼真 URL; http(s):// 直拉.
//   2. BlurHash placeholder → 真图 fade-in (BlurHash 未实现时退回纯色).
//   3. 视频 kind 自动用 coverSha (没有则首帧 placeholder + 中央 play icon).
//
// 没有 blurhash 字段时, 用 sha256 末 6 位生成稳定的颜色 (免抖).
//
// flutter_blurhash 是 v2 工作 — 这里先拿渐变占位 + 真图 fade-in 跑通.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../../app/theme.dart';
import '../../../../services/auth_service.dart';
import '../../../settings/application/settings_controller.dart';
import '../../domain/creation_task.dart';

class OutputThumbnail extends ConsumerWidget {
  const OutputThumbnail({
    super.key,
    required this.output,
    this.fit = BoxFit.cover,
  });

  final TaskOutput output;
  final BoxFit fit;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final settings = ref.watch(settingsControllerProvider).valueOrNull;
    final creds = ref.watch(hubCredentialsProvider);
    final resolved = _resolve(output.url, settings?.aigcUri, creds);

    final placeholder = _placeholder(output);

    if (resolved == null) {
      return placeholder;
    }

    return Stack(
      fit: StackFit.expand,
      children: [
        placeholder,
        Image.network(
          resolved.$1,
          headers: resolved.$2,
          fit: fit,
          loadingBuilder: (_, child, progress) {
            if (progress == null) return child; // done
            return const SizedBox.shrink();
          },
          frameBuilder: (_, child, frame, wasSync) {
            if (wasSync || frame != null) {
              return AnimatedOpacity(
                opacity: 1,
                duration: const Duration(milliseconds: 240),
                child: child,
              );
            }
            return const SizedBox.shrink();
          },
          errorBuilder: (_, e, s) => placeholder,
        ),
        if (output.kind == 'video')
          const Center(
            child: Icon(Icons.play_circle_filled, size: 48, color: Colors.white70),
          ),
      ],
    );
  }

  Widget _placeholder(TaskOutput o) {
    final color = _stableColor(o.sha256.isNotEmpty ? o.sha256 : o.url);
    return Container(
      decoration: BoxDecoration(
        gradient: LinearGradient(
          begin: Alignment.topLeft,
          end: Alignment.bottomRight,
          colors: [
            color.withValues(alpha: 0.7),
            color.withValues(alpha: 0.4),
          ],
        ),
      ),
      child: Center(
        child: Icon(
          _kindIcon(o.kind),
          size: 28,
          color: Colors.white.withValues(alpha: 0.7),
        ),
      ),
    );
  }
}

(String, Map<String, String>?)? _resolve(
    String url, Uri? aigcBase, HubCredentials? creds) {
  if (url.isEmpty) return null;
  if (url.startsWith('http')) return (url, null);
  if (url.startsWith('cas:')) {
    final sha = url.substring(4);
    if (sha.length != 64) return null;
    if (creds == null) return null;
    // 创作产物是 aigc CAS —— 单 origin 下走 aigc 命名空间路径, 由 site nginx
    // 反代到 aigc (与 brain 通用文件 /v1/files/by-sha 消歧)。
    final base = aigcBase ?? creds.endpoint;
    final u = base.replace(path: '/v1/aigc/files-by-sha/$sha');
    return (u.toString(), {'Authorization': 'Bearer ${creds.bearerToken}'});
  }
  return null;
}

IconData _kindIcon(String kind) {
  switch (kind) {
    case 'video':
      return Icons.movie_outlined;
    case 'audio':
      return Icons.audiotrack_outlined;
    case 'hotparse':
      return Icons.local_fire_department_outlined;
    case 'cover':
    case 'image':
    default:
      return Icons.image_outlined;
  }
}

/// _stableColor: 用 hash 末 6 位生成稳定的色块, 同一 output 永远同色.
Color _stableColor(String key) {
  if (key.isEmpty) return BiuTokens.purpleSoft;
  final tail = key.length >= 6 ? key.substring(key.length - 6) : key.padLeft(6, '0');
  try {
    final v = int.parse(tail, radix: 16);
    return Color(0xFF000000 | v);
  } catch (_) {
    return BiuTokens.purpleSoft;
  }
}

// 数字人角色 + 音色 — services/aigc /v1/characters /v1/voices 投影.

import 'package:meta/meta.dart';

@immutable
class CharacterEntry {
  final String id;
  final String userId; // 空字符串 = 系统内置
  final String name;
  final String avatarUrl; // "cas:<sha>" / https / 空
  final String voiceDefault;
  final Map<String, dynamic> config;
  final bool isPublic;
  final bool isSystem; // user_id IS NULL
  final DateTime? createdAt;

  const CharacterEntry({
    required this.id,
    required this.userId,
    required this.name,
    required this.avatarUrl,
    required this.voiceDefault,
    required this.isPublic,
    required this.isSystem,
    this.config = const {},
    this.createdAt,
  });

  factory CharacterEntry.fromJson(Map<String, dynamic> j) => CharacterEntry(
        id: j['id'] as String? ?? '',
        userId: j['user_id'] as String? ?? '',
        name: j['name'] as String? ?? '',
        avatarUrl: j['avatar_url'] as String? ?? '',
        voiceDefault: j['voice_default'] as String? ?? '',
        config: (j['config'] as Map?)?.cast<String, dynamic>() ?? const {},
        isPublic: j['is_public'] as bool? ?? false,
        isSystem: j['is_system'] as bool? ?? (j['user_id'] == null || j['user_id'] == ''),
        createdAt: _parseDate(j['created_at']),
      );
}

@immutable
class VoiceEntry {
  final String id;
  final String name;
  final String provider; // volcengine | dashscope | azure
  final String language; // zh-CN | en-US
  final String gender; // male | female | neutral
  final String style;
  final String sampleUrl;

  const VoiceEntry({
    required this.id,
    required this.name,
    required this.provider,
    required this.language,
    required this.gender,
    this.style = '',
    this.sampleUrl = '',
  });

  factory VoiceEntry.fromJson(Map<String, dynamic> j) => VoiceEntry(
        id: j['id'] as String? ?? '',
        name: j['name'] as String? ?? '',
        provider: j['provider'] as String? ?? '',
        language: j['language'] as String? ?? '',
        gender: j['gender'] as String? ?? '',
        style: j['style'] as String? ?? '',
        sampleUrl: j['sample_url'] as String? ?? '',
      );
}

DateTime? _parseDate(dynamic v) {
  if (v is String && v.isNotEmpty) {
    try {
      return DateTime.parse(v);
    } catch (_) {
      return null;
    }
  }
  return null;
}

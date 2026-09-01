/// 文档处理位置偏好（设计文档 BiuMind-Client-Docproc-Design §3.4 / P2 W3）。
///
/// 三态：自动（默认，按 §3.4 矩阵）/ 优先本机（免费）/ 优先云端（花积分）。
/// 持久化：SharedPreferences 单一 JSON 在 key `biu.wiki.docproc`（先例：
/// chat 偏好 `biu.chat.prefs`）。非敏感 UI 偏好，不进 secure storage。
///
/// 队列侧只在新 item enqueue 时读取本设置（item 上快照 location），
/// 已在队列里的 item 不追溯。
library;

import 'dart:convert';

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:shared_preferences/shared_preferences.dart';

import '../../../core/platform/platform_caps.dart';

const _kKey = 'biu.wiki.docproc';

/// 文档处理位置三态。
enum DocprocProcessLocation {
  /// 自动（默认）：§3.4 矩阵 —— 桌面/Web ≤50MB 本机，移动 ≤10MB 本机，
  /// 否则云端。
  auto,

  /// 优先本机（免费）：≤ 队列字节上限（docprocQueueMaxBytes，桌面 200MB /
  /// 移动 80MB）就本机。
  preferLocal,

  /// 优先云端（花积分）：全部云端。
  preferCloud;

  static DocprocProcessLocation fromName(String? name) {
    for (final v in values) {
      if (v.name == name) return v;
    }
    return auto;
  }
}

/// §3.4 映射矩阵（纯函数，队列入队/调度与设置页共用同一个判定）。
/// [hasLocalDocproc]=false 的平台永远云端（无论哪档）。
bool docprocShouldParseLocally({
  required DocprocProcessLocation location,
  required PlatformCaps caps,
  required int byteSize,
}) {
  if (!caps.hasLocalDocproc) return false;
  switch (location) {
    case DocprocProcessLocation.preferCloud:
      return false;
    case DocprocProcessLocation.preferLocal:
      return byteSize <= caps.docprocQueueMaxBytes;
    case DocprocProcessLocation.auto:
      final limit = caps.isMobile ? 10 * 1024 * 1024 : 50 * 1024 * 1024;
      return byteSize <= limit;
  }
}

class DocprocPreferences {
  const DocprocPreferences({
    this.location = DocprocProcessLocation.auto,
  });

  final DocprocProcessLocation location;

  Map<String, dynamic> toJson() => {'location': location.name};

  static DocprocPreferences fromJson(Map<String, dynamic> j) {
    return DocprocPreferences(
      location: DocprocProcessLocation.fromName(j['location'] as String?),
    );
  }
}

class DocprocPreferencesNotifier extends StateNotifier<DocprocPreferences> {
  DocprocPreferencesNotifier() : super(const DocprocPreferences()) {
    _load();
  }

  Future<void> _load() async {
    try {
      final prefs = await SharedPreferences.getInstance();
      final raw = prefs.getString(_kKey);
      if (raw == null) return;
      final decoded = jsonDecode(raw);
      if (decoded is Map<String, dynamic>) {
        state = DocprocPreferences.fromJson(decoded);
      }
    } catch (_) {/* fail silent —— 起默认 */}
  }

  Future<void> setLocation(DocprocProcessLocation location) async {
    state = DocprocPreferences(location: location);
    try {
      final prefs = await SharedPreferences.getInstance();
      await prefs.setString(_kKey, jsonEncode(state.toJson()));
    } catch (_) {}
  }
}

final docprocPreferencesProvider =
    StateNotifierProvider<DocprocPreferencesNotifier, DocprocPreferences>(
  (ref) => DocprocPreferencesNotifier(),
);

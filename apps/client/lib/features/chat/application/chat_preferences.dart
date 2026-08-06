// ChatPreferences —— 全局 chat 用户偏好。
// 设计文档 docs/BiuMind-Chat-UI-Benchmark-Optimization.md（v2 设置面板）。
//
// 持久化：SharedPreferences 单一 JSON 在 key `biu.chat.prefs`。
//   {"defaultModel": null, "defaultMode": "agent", ...}
//
// defaultMode 出厂默认 = agent（智能模式）；agent 的工具执行环境默认 local
// （本机），见 NewThreadDialog._agentRuntimeEnv。
//
// defaultModel / defaultMode 是新会话(createDefaultThread)的默认值。
// 控件归在「设置 > 智能体 > 聊天」(ChatSettingsPane)；语言 localeOverride 的
// 控件归在「设置 > 通用 > 外观」(AppearancePane)。聊天文字字号已并入全局字号
// (设置 > 外观 > 字体大小)，不再有聊天专属 fontScale。

import 'dart:convert';

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:shared_preferences/shared_preferences.dart';

import '../domain/chat_models.dart';

const _kKey = 'biu.chat.prefs';

class ChatPreferences {
  const ChatPreferences({
    this.defaultModel,
    this.defaultProviderId,
    this.defaultMode = ThreadMode.agent,
    this.autoRenameEnabled = true,
    this.localeOverride,
    this.ttsModel,
    this.ttsVoice,
  });

  final String? defaultModel;

  /// 默认模型对应的 provider slug —— 与 defaultModel 配对,供新对话路由消歧
  /// (同 model code 可能在官方 + BYOK provider 下都有)。null = 不锁路由。
  final String? defaultProviderId;
  final ThreadMode defaultMode;
  /// 首条 user message 后自动从 prompt 推标题，覆盖 thread.title 为空的场景。
  /// 默认开。用户希望保留"新对话"占位时关掉。
  final bool autoRenameEnabled;
  /// 用户手动覆盖的 UI 语言。null = 跟系统；'zh' / 'en' = 强制。
  /// 仅 chat v2 + 同样接 AppLocalizations 的页面响应。
  final String? localeOverride;

  /// 消息「朗读」用的云端 TTS 模型 code (mode == 'audio_speech', 如
  /// cosyvoice-v3-plus)。null/空 = 不走云端, 朗读回落设备本地 flutter_tts。
  final String? ttsModel;

  /// 云端 TTS 音色 ID (cosyvoice 系统音色, 如 longanyang)。ttsModel 配了但
  /// ttsVoice 空时同样回落本地 (cosyvoice 无默认音色, 不传会 400)。
  final String? ttsVoice;

  /// 云端朗读是否就绪 = 同时配了模型 + 音色。
  bool get cloudTtsConfigured =>
      (ttsModel?.isNotEmpty ?? false) && (ttsVoice?.isNotEmpty ?? false);

  ChatPreferences copyWith({
    String? defaultModel,
    String? defaultProviderId,
    ThreadMode? defaultMode,
    bool? autoRenameEnabled,
    String? localeOverride,
    String? ttsModel,
    String? ttsVoice,
    bool clearDefaultModel = false,
    bool clearLocaleOverride = false,
    bool clearTts = false,
  }) {
    return ChatPreferences(
      defaultModel:
          clearDefaultModel ? null : (defaultModel ?? this.defaultModel),
      // providerId 与 model 配对：清 model 时一并清 providerId。
      defaultProviderId: clearDefaultModel
          ? null
          : (defaultProviderId ?? this.defaultProviderId),
      defaultMode: defaultMode ?? this.defaultMode,
      autoRenameEnabled: autoRenameEnabled ?? this.autoRenameEnabled,
      localeOverride: clearLocaleOverride
          ? null
          : (localeOverride ?? this.localeOverride),
      ttsModel: clearTts ? null : (ttsModel ?? this.ttsModel),
      ttsVoice: clearTts ? null : (ttsVoice ?? this.ttsVoice),
    );
  }

  Map<String, dynamic> toJson() => {
        'defaultModel': defaultModel,
        'defaultProviderId': defaultProviderId,
        'defaultMode': defaultMode.name,
        'autoRenameEnabled': autoRenameEnabled,
        'localeOverride': localeOverride,
        'ttsModel': ttsModel,
        'ttsVoice': ttsVoice,
      };

  static ChatPreferences fromJson(Map<String, dynamic> j) {
    return ChatPreferences(
      defaultModel: j['defaultModel'] as String?,
      defaultProviderId: j['defaultProviderId'] as String?,
      defaultMode:
          ThreadMode.fromName((j['defaultMode'] as String?) ?? 'agent'),
      autoRenameEnabled: (j['autoRenameEnabled'] as bool?) ?? true,
      localeOverride: j['localeOverride'] as String?,
      ttsModel: j['ttsModel'] as String?,
      ttsVoice: j['ttsVoice'] as String?,
    );
  }
}

class ChatPreferencesNotifier extends StateNotifier<ChatPreferences> {
  ChatPreferencesNotifier() : super(const ChatPreferences()) {
    _load();
  }

  Future<void> _load() async {
    try {
      final prefs = await SharedPreferences.getInstance();
      final raw = prefs.getString(_kKey);
      if (raw == null) return;
      final decoded = jsonDecode(raw);
      if (decoded is Map<String, dynamic>) {
        state = ChatPreferences.fromJson(decoded);
      }
    } catch (_) {/* fail silent —— 起默认 */}
  }

  Future<void> _persist(ChatPreferences p) async {
    try {
      final prefs = await SharedPreferences.getInstance();
      await prefs.setString(_kKey, jsonEncode(p.toJson()));
    } catch (_) {}
  }

  Future<void> setDefaultModel(String? code, {String? providerId}) async {
    state = state.copyWith(
      defaultModel: code,
      defaultProviderId: code == null ? null : providerId,
      clearDefaultModel: code == null,
    );
    await _persist(state);
  }

  Future<void> setDefaultMode(ThreadMode mode) async {
    state = state.copyWith(defaultMode: mode);
    await _persist(state);
  }

  Future<void> setAutoRenameEnabled(bool v) async {
    state = state.copyWith(autoRenameEnabled: v);
    await _persist(state);
  }

  /// 设云端朗读模型 code。null/空 = 关闭云端朗读(连同音色一起清,回落本地)。
  Future<void> setTtsModel(String? code) async {
    if (code == null || code.isEmpty) {
      state = state.copyWith(clearTts: true);
    } else {
      state = state.copyWith(ttsModel: code);
    }
    await _persist(state);
  }

  /// 设云端朗读音色 ID(cosyvoice 系统音色)。空字符串保留(云端朗读视为未就绪)。
  Future<void> setTtsVoice(String voice) async {
    state = state.copyWith(ttsVoice: voice);
    await _persist(state);
  }

  /// 设 UI 语言覆盖：'zh' / 'en' / null（跟随系统）。
  Future<void> setLocaleOverride(String? code) async {
    state = state.copyWith(
      localeOverride: code,
      clearLocaleOverride: code == null,
    );
    await _persist(state);
  }

  /// 一键恢复默认 —— Settings dialog "恢复默认"按钮调用。
  /// 直接 remove key 而不是写默认值；下次 _load 找不到 key 会起默认。
  Future<void> resetAll() async {
    state = const ChatPreferences();
    try {
      final prefs = await SharedPreferences.getInstance();
      await prefs.remove(_kKey);
    } catch (_) {}
  }
}

final chatPreferencesProvider =
    StateNotifierProvider<ChatPreferencesNotifier, ChatPreferences>(
  (ref) => ChatPreferencesNotifier(),
);

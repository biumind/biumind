// Generation form 状态管理 — 对齐 zhiying-portal/src/stores/generation.js.
//
// 用户在 GenerationPanel 上的所有操作 (切 type / 切模型 / 改 prompt / 调参数 /
// 拖首帧) 都走这个 controller. submit 时调 toRequest() 转成 AigcClient.submit 入参.
//
// 关键设计:
//   - selectType 默认重置 modelCode (避免选了 wanx-2.6-t2v 切到 image tab 再
//     提交时 type 与 model 类型不匹配 → 服务端 400 type_model_mismatch)
//   - selectModel 自动填模型 config 里的 default aspect_ratio / resolution /
//     duration (zhiying updateVideoOptionsFromConfig / updateImageOptionsFromConfig 等价)
//   - reset 不丢 type (避免提交后回到完全空白). prompt 清空, 参数回 default.

import 'package:flutter/foundation.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../data/aigc_client.dart';
import '../domain/ai_model.dart';

/// AIGC task type. wire 字符串与服务端对齐.
enum GenerationType {
  image,
  video,
  digitalHuman,
  hotparse;

  String get wire {
    switch (this) {
      case GenerationType.image:
        return 'image';
      case GenerationType.video:
        return 'video';
      case GenerationType.digitalHuman:
        return 'digital_human';
      case GenerationType.hotparse:
        return 'hotparse';
    }
  }

  static GenerationType fromWire(String? raw) {
    switch (raw) {
      case 'image':
        return GenerationType.image;
      case 'video':
        return GenerationType.video;
      case 'digital_human':
        return GenerationType.digitalHuman;
      case 'hotparse':
        return GenerationType.hotparse;
      default:
        return GenerationType.image;
    }
  }
}

/// 表单状态 - 不可变.
@immutable
class GenerationFormState {
  final GenerationType type;
  final String? modelCode; // null = 用户未选 (UI 上 disable submit)
  final String prompt;
  final String negativePrompt;

  // 通用参数
  final String? aspectRatio; // "16:9"
  final String? resolution; // "720p"
  final int numOutputs;
  final int seed; // -1 = 随机

  // 视频专用
  final int? durationSeconds;
  final String? firstFrameUrl; // "cas:<sha>"
  final String? lastFrameUrl;
  final List<String> referenceImageUrls;

  // 数字人专用
  final String? characterId;
  final String? voiceId;
  final String? audioUrl;

  // 爆款解析 — Phase1: 短视频公网直链 (mp4/m3u8)。worker 读 params.source_url。
  final String? hotparseSourceUrl;

  // 控制
  final bool aiOptimize;
  final bool isPublic;

  const GenerationFormState({
    this.type = GenerationType.image,
    this.modelCode,
    this.prompt = '',
    this.negativePrompt = '',
    this.aspectRatio,
    this.resolution,
    this.numOutputs = 1,
    this.seed = -1,
    this.durationSeconds,
    this.firstFrameUrl,
    this.lastFrameUrl,
    this.referenceImageUrls = const [],
    this.characterId,
    this.voiceId,
    this.audioUrl,
    this.hotparseSourceUrl,
    this.aiOptimize = false,
    this.isPublic = false,
  });

  GenerationFormState copyWith({
    GenerationType? type,
    Object? modelCode = _sentinel,
    String? prompt,
    String? negativePrompt,
    Object? aspectRatio = _sentinel,
    Object? resolution = _sentinel,
    int? numOutputs,
    int? seed,
    Object? durationSeconds = _sentinel,
    Object? firstFrameUrl = _sentinel,
    Object? lastFrameUrl = _sentinel,
    List<String>? referenceImageUrls,
    Object? characterId = _sentinel,
    Object? voiceId = _sentinel,
    Object? audioUrl = _sentinel,
    Object? hotparseSourceUrl = _sentinel,
    bool? aiOptimize,
    bool? isPublic,
  }) {
    return GenerationFormState(
      type: type ?? this.type,
      modelCode: modelCode == _sentinel ? this.modelCode : modelCode as String?,
      prompt: prompt ?? this.prompt,
      negativePrompt: negativePrompt ?? this.negativePrompt,
      aspectRatio:
          aspectRatio == _sentinel ? this.aspectRatio : aspectRatio as String?,
      resolution:
          resolution == _sentinel ? this.resolution : resolution as String?,
      numOutputs: numOutputs ?? this.numOutputs,
      seed: seed ?? this.seed,
      durationSeconds: durationSeconds == _sentinel
          ? this.durationSeconds
          : durationSeconds as int?,
      firstFrameUrl: firstFrameUrl == _sentinel
          ? this.firstFrameUrl
          : firstFrameUrl as String?,
      lastFrameUrl: lastFrameUrl == _sentinel
          ? this.lastFrameUrl
          : lastFrameUrl as String?,
      referenceImageUrls: referenceImageUrls ?? this.referenceImageUrls,
      characterId:
          characterId == _sentinel ? this.characterId : characterId as String?,
      voiceId: voiceId == _sentinel ? this.voiceId : voiceId as String?,
      audioUrl: audioUrl == _sentinel ? this.audioUrl : audioUrl as String?,
      hotparseSourceUrl: hotparseSourceUrl == _sentinel
          ? this.hotparseSourceUrl
          : hotparseSourceUrl as String?,
      aiOptimize: aiOptimize ?? this.aiOptimize,
      isPublic: isPublic ?? this.isPublic,
    );
  }

  /// canSubmit: UI 上 submit 按钮是否可点击.
  bool get canSubmit {
    if (modelCode == null || modelCode!.isEmpty) return false;
    if (type == GenerationType.hotparse) {
      // 爆款解析不需 prompt, 但必须有源视频链接。
      return (hotparseSourceUrl ?? '').trim().isNotEmpty;
    }
    if (prompt.trim().isEmpty) return false;
    return true;
  }

  /// 把 state 拼成 AigcClient.submit() 的 params map. 字段命名与 services/aigc
  /// proto.GenerationParams 对齐, 服务端按 jsonb 透传到 worker.
  Map<String, dynamic> buildParams() {
    final p = <String, dynamic>{};
    if (aspectRatio != null) p['aspect_ratio'] = aspectRatio;
    if (resolution != null) p['resolution'] = resolution;
    if (numOutputs > 1) p['num_outputs'] = numOutputs;
    if (seed >= 0) p['seed'] = seed;
    if (durationSeconds != null) p['duration_seconds'] = durationSeconds;
    if (firstFrameUrl != null && firstFrameUrl!.isNotEmpty) {
      p['first_frame_url'] = firstFrameUrl;
    }
    if (lastFrameUrl != null && lastFrameUrl!.isNotEmpty) {
      p['last_frame_url'] = lastFrameUrl;
    }
    if (referenceImageUrls.isNotEmpty) {
      p['reference_image_urls'] = referenceImageUrls;
    }
    if (characterId != null) p['character_id'] = characterId;
    if (voiceId != null) p['voice_id'] = voiceId;
    if (audioUrl != null) p['audio_url'] = audioUrl;
    if (hotparseSourceUrl != null && hotparseSourceUrl!.isNotEmpty) {
      // worker HotparseProvider 读 source_url; source 按 URL 推断 (B站/抖音走
      // yt-dlp, 直链=upload)。worker 端对 source 再做白名单 + 友好降级。
      p['source_url'] = hotparseSourceUrl;
      p['source'] = detectHotparseSource(hotparseSourceUrl!);
    }
    if (aiOptimize) p['prompt_optimize'] = true;
    return p;
  }
}

const _sentinel = Object();

/// 从 URL host 推断爆款来源 (与 worker hotparse/extractors.detect_source 对齐)。
String detectHotparseSource(String url) {
  final u = url.toLowerCase();
  if (u.contains('bilibili.com') || u.contains('b23.tv')) return 'bilibili';
  if (u.contains('douyin.com') || u.contains('iesdouyin')) return 'douyin';
  if (u.contains('xiaohongshu.com') || u.contains('xhslink.com')) {
    return 'xiaohongshu';
  }
  if (u.contains('kuaishou.com')) return 'kuaishou';
  return 'upload';
}

/// Controller — Riverpod StateNotifier. 与 Form widgets 双向绑定.
class GenerationFormController extends StateNotifier<GenerationFormState> {
  GenerationFormController() : super(const GenerationFormState());

  void selectType(GenerationType type) {
    if (type == state.type) return;
    // 切 type 时强制清掉 modelCode 让用户重选, 避免 type/model mismatch.
    state = GenerationFormState(
      type: type,
      // 保留 prompt — 用户切 tab 时希望保留草稿
      prompt: state.prompt,
      isPublic: state.isPublic,
      aiOptimize: state.aiOptimize,
    );
  }

  /// 选模型 + 用 model.config 默认值填表单 (zhiying updateVideoOptionsFromConfig 等价).
  void selectModel(AiModel model) {
    final ratios = model.aspectRatios;
    final resolutions = model.resolutions;
    final dur = model.duration;

    state = state.copyWith(
      modelCode: model.code,
      aspectRatio: state.aspectRatio ??
          (ratios.isNotEmpty ? ratios.first.key : null),
      resolution: state.resolution ??
          (resolutions.isNotEmpty ? resolutions.first.key : null),
      durationSeconds: state.durationSeconds ?? dur?.defaultValue,
    );
  }

  void setPrompt(String v) => state = state.copyWith(prompt: v);
  void setNegativePrompt(String v) => state = state.copyWith(negativePrompt: v);

  void setAspectRatio(String? v) => state = state.copyWith(aspectRatio: v);
  void setResolution(String? v) => state = state.copyWith(resolution: v);
  void setDurationSeconds(int? v) => state = state.copyWith(durationSeconds: v);

  void setNumOutputs(int n) =>
      state = state.copyWith(numOutputs: n.clamp(1, 8));
  void setSeed(int v) => state = state.copyWith(seed: v);

  void setFirstFrame(String? casUrl) =>
      state = state.copyWith(firstFrameUrl: casUrl);
  void setLastFrame(String? casUrl) =>
      state = state.copyWith(lastFrameUrl: casUrl);

  void addReferenceImage(String casUrl, {int max = 5}) {
    final list = [...state.referenceImageUrls];
    if (list.contains(casUrl)) return;
    if (list.length >= max) return;
    list.add(casUrl);
    state = state.copyWith(referenceImageUrls: list);
  }

  void removeReferenceImage(String casUrl) {
    final list = state.referenceImageUrls.where((u) => u != casUrl).toList();
    state = state.copyWith(referenceImageUrls: list);
  }

  void setCharacter(String? id) => state = state.copyWith(characterId: id);
  void setVoice(String? id) => state = state.copyWith(voiceId: id);
  void setAudioUrl(String? url) => state = state.copyWith(audioUrl: url);
  void setHotparseSourceUrl(String? url) =>
      state = state.copyWith(hotparseSourceUrl: url);

  void toggleAiOptimize() =>
      state = state.copyWith(aiOptimize: !state.aiOptimize);
  void toggleIsPublic() => state = state.copyWith(isPublic: !state.isPublic);

  /// 提交后调用: 仅清空 prompt + 临时输入, 保留 type/model/参数 让连续创作顺手.
  void resetAfterSubmit() {
    state = state.copyWith(
      prompt: '',
      negativePrompt: '',
      firstFrameUrl: null,
      lastFrameUrl: null,
      referenceImageUrls: const [],
      audioUrl: null,
    );
  }

  /// 全量 reset (回到全新). UI 上"清空"按钮用.
  void reset() {
    state = const GenerationFormState();
  }

  /// 「做同款」/「重新编辑」时把别人的作品参数填进表单.
  void syncFromTask({
    required GenerationType type,
    required String modelCode,
    required String prompt,
    String negativePrompt = '',
    Map<String, dynamic> params = const {},
    bool isPublic = false,
  }) {
    state = GenerationFormState(
      type: type,
      modelCode: modelCode,
      prompt: prompt,
      negativePrompt: negativePrompt,
      aspectRatio: params['aspect_ratio'] as String?,
      resolution: params['resolution'] as String?,
      durationSeconds: (params['duration_seconds'] as num?)?.toInt(),
      firstFrameUrl: params['first_frame_url'] as String?,
      lastFrameUrl: params['last_frame_url'] as String?,
      referenceImageUrls:
          (params['reference_image_urls'] as List?)?.cast<String>() ??
              const [],
      characterId: params['character_id'] as String?,
      voiceId: params['voice_id'] as String?,
      audioUrl: params['audio_url'] as String?,
      hotparseSourceUrl: params['source_url'] as String?,
      isPublic: isPublic,
      aiOptimize: params['prompt_optimize'] == true,
    );
  }

  /// 调用 AigcClient.submit. 调用方负责处理异常 (积分不足 / authz / 网络).
  /// 成功后调 [resetAfterSubmit] 让 UI 进入下一轮创作.
  Future<AigcSubmitResult> submit(
    AigcClient client, {
    String? parentSha,
    String? lineageOp,
    String? idempotencyKey,
  }) async {
    final s = state;
    return client.submit(
      type: s.type.wire,
      modelCode: s.modelCode!,
      prompt: s.prompt,
      negativePrompt: s.negativePrompt,
      params: s.buildParams(),
      isPublic: s.isPublic,
      parentSha: parentSha,
      lineageOp: lineageOp,
      idempotencyKey: idempotencyKey,
    );
  }
}

final generationFormControllerProvider =
    StateNotifierProvider<GenerationFormController, GenerationFormState>(
  (ref) => GenerationFormController(),
);

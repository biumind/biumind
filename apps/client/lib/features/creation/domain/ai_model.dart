// AiModel — 服务端 GET /v1/models 返回的模型字典.
//
// config / pricing_rule 是 raw JSON, 客户端按需 parse 成 UI 需要的结构.
// 字段命名与 services/aigc/internal/api/models.go projectModels() 对齐.

import 'package:meta/meta.dart';

@immutable
class AiModel {
  final String code; // "wanx-2.6-t2v" / "doubao-seedream-4.0"
  final String type; // image | video | digital_human | hotparse
  final String displayName; // "通义万相 2.6"
  final String providerCode;
  final int priceCredits;

  /// raw JSON: aspect_ratios / resolutions / duration / features
  final Map<String, dynamic> config;

  /// raw JSON: by_duration / by_resolution 的加价倍率 (可选)
  final Map<String, dynamic> pricingRule;

  final int sortOrder;

  const AiModel({
    required this.code,
    required this.type,
    required this.displayName,
    required this.providerCode,
    required this.priceCredits,
    this.config = const {},
    this.pricingRule = const {},
    this.sortOrder = 0,
  });

  factory AiModel.fromJson(Map<String, dynamic> j) => AiModel(
        code: j['code'] as String? ?? '',
        type: j['type'] as String? ?? 'image',
        displayName: j['display_name'] as String? ?? '',
        providerCode: j['provider_code'] as String? ?? '',
        priceCredits: (j['price_credits'] as num?)?.toInt() ?? 0,
        config: (j['config'] as Map?)?.cast<String, dynamic>() ?? const {},
        pricingRule:
            (j['pricing_rule'] as Map?)?.cast<String, dynamic>() ?? const {},
        sortOrder: (j['sort_order'] as num?)?.toInt() ?? 0,
      );

  // ─── config 解析 helpers ──────────────────────────────

  /// aspect_ratios: [{"key":"16:9","value":"1280*720","label":"16:9"}, ...]
  List<AspectRatioOption> get aspectRatios => _parseOptions('aspect_ratios');

  /// resolutions: [{"key":"720p","value":"720p","label":"720P"}, ...]
  List<AspectRatioOption> get resolutions => _parseOptions('resolutions');

  /// duration: {"min":5,"max":15,"step":5,"default":5} (视频专用)
  DurationConfig? get duration {
    final d = config['duration'];
    if (d is! Map) return null;
    return DurationConfig(
      min: (d['min'] as num?)?.toInt() ?? 5,
      max: (d['max'] as num?)?.toInt() ?? 15,
      step: (d['step'] as num?)?.toInt() ?? 1,
      defaultValue: (d['default'] as num?)?.toInt() ?? 5,
    );
  }

  /// features: {"reference_image": "Y", "first_frame": "Y", ...}
  bool feature(String name) {
    final f = config['features'];
    if (f is! Map) return false;
    final v = f[name];
    return v == 'Y' || v == true || v == 'true';
  }

  int featureInt(String name, {int fallback = 0}) {
    final f = config['features'];
    if (f is! Map) return fallback;
    final v = f[name];
    if (v is num) return v.toInt();
    if (v is String) return int.tryParse(v) ?? fallback;
    return fallback;
  }

  bool get supportsReferenceImage => feature('reference_image');
  bool get supportsFirstFrame => feature('first_frame');
  bool get supportsLastFrame => feature('last_frame');
  int get referenceImageCount => featureInt('reference_image_count', fallback: 1);

  List<AspectRatioOption> _parseOptions(String key) {
    final raw = config[key];
    if (raw is! List) return const [];
    return raw
        .whereType<Map>()
        .map((m) => m.cast<String, dynamic>())
        .map(AspectRatioOption.fromJson)
        .toList();
  }
}

@immutable
class AspectRatioOption {
  final String key; // "16:9"
  final String value; // "1280*720"
  final String label; // "16:9" 或 "720P"

  const AspectRatioOption({
    required this.key,
    required this.value,
    required this.label,
  });

  factory AspectRatioOption.fromJson(Map<String, dynamic> j) => AspectRatioOption(
        key: j['key'] as String? ?? '',
        value: j['value'] as String? ?? '',
        label: j['label'] as String? ?? j['key'] as String? ?? '',
      );
}

@immutable
class DurationConfig {
  final int min;
  final int max;
  final int step;
  final int defaultValue;

  const DurationConfig({
    required this.min,
    required this.max,
    required this.step,
    required this.defaultValue,
  });

  /// 可选的离散值列表 (用于 chip / segment), e.g. [5, 10, 15].
  List<int> get steps {
    final out = <int>[];
    for (var v = min; v <= max; v += step) {
      out.add(v);
    }
    return out;
  }
}

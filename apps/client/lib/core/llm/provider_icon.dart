// provider_icon.dart — provider 品牌 icon (SVG 优先, fallback 首字母+品牌色).
//
// 用于 API Keys 设置卡片 / 模型选择器等. SVG 来自 simple-icons (CC0):
//   anthropic / openai / google / deepseek / doubao(=bytedance logo).
// 其余 provider (dashscope/volcengine/azure_openai/moonshot/qwen/baichuan/custom)
// fallback 圆角方块 + 首字母 + 品牌色 (复用 category_colors ProviderBrand).

import 'package:flutter/material.dart';
import 'package:flutter_svg/flutter_svg.dart';

import '../../app/theme/category_colors.dart';

/// 有 SVG asset 的 provider (assets/icons/provider/`<slug>`.svg).
const _svgAssets = <String, String>{
  'anthropic': 'assets/icons/provider/anthropic.svg',
  'openai': 'assets/icons/provider/openai.svg',
  'google': 'assets/icons/provider/google.svg',
  'deepseek': 'assets/icons/provider/deepseek.svg',
  'doubao': 'assets/icons/provider/doubao.svg',
};

/// fallback 品牌色 (无 SVG 的 provider). 复用 ProviderBrand + 国内 provider 近似色.
const _brandColors = <String, Color>{
  'anthropic': ProviderBrand.anthropic,
  'openai': ProviderBrand.openai,
  'google': ProviderBrand.google,
  'deepseek': ProviderBrand.deepseek,
  'qwen': ProviderBrand.qwen,
  'azure_openai': ProviderBrand.azure,
  'azure': ProviderBrand.azure,
  'dashscope': ProviderBrand.qwen, // 阿里灵积, 沿用通义橙
  'volcengine': Color(0xFF1664FF), // 火山方舟蓝
  'doubao': Color(0xFFE53935), // 豆包红 (SVG 是 bytedance logo, fallback 用豆包红)
  'moonshot': Color(0xFF1F1F1F), // Kimi 黑
  'baichuan': Color(0xFF6C2BD9), // 百川紫
};

/// 渲染 provider 品牌 icon.
///
/// 有 SVG (simple-icons) → 单色 logo (currentColor/黑). 否则 fallback:
/// 圆角方块 + 首字母 + 品牌色. 未知 slug 中性灰 + '?'.
Widget providerIcon(String slug, {double size = 24}) {
  final asset = _svgAssets[slug];
  if (asset != null) {
    return SvgPicture.asset(asset, width: size, height: size);
  }
  return _LetterBadge(
    slug: slug,
    size: size,
    color: _brandColors[slug] ?? const Color(0xFF8A8A8A),
  );
}

class _LetterBadge extends StatelessWidget {
  const _LetterBadge({required this.slug, required this.size, required this.color});
  final String slug;
  final double size;
  final Color color;

  @override
  Widget build(BuildContext context) {
    final letter = slug.isEmpty ? '?' : slug[0].toUpperCase();
    return Container(
      width: size,
      height: size,
      decoration: BoxDecoration(
        color: color,
        borderRadius: BorderRadius.circular(size * 0.22),
      ),
      alignment: Alignment.center,
      child: Text(
        letter,
        style: TextStyle(
          color: Colors.white,
          fontSize: size * 0.5,
          fontWeight: FontWeight.w600,
        ),
      ),
    );
  }
}

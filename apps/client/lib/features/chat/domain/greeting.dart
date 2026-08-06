// Greeting —— Hero 欢迎页用的问候语 + 起点 prompt 卡片定义。
// 设计文档 docs/BiuMind-Chat-UI-Benchmark-Optimization.md P1-15。
//
// 时段问候根据本地小时切；起点 prompt 是 6 张固定卡，覆盖常见场景。

import 'package:flutter/material.dart';

import '../../../app/theme/category_colors.dart';

String greetingForHour(int hour, {String? userName}) {
  final base = switch (hour) {
    >= 0 && < 6 => '夜深了',
    >= 6 && < 11 => '早上好',
    >= 11 && < 14 => '中午好',
    >= 14 && < 18 => '下午好',
    >= 18 && < 22 => '晚上好',
    _ => '夜深了',
  };
  return userName == null || userName.isEmpty ? base : '$base，$userName';
}

class StarterPrompt {
  const StarterPrompt({
    required this.icon,
    required this.title,
    required this.prompt,
    required this.tone,
  });

  final IconData icon;
  /// 卡片上显示的短标题
  final String title;
  /// 点击后塞进 composer 或直接发送的 prompt 全文
  final String prompt;
  /// 卡片图标颜色 hint
  final Color tone;
}

const kStarterPrompts = <StarterPrompt>[
  StarterPrompt(
    icon: Icons.auto_awesome,
    title: '帮我写一段文案',
    prompt: '帮我写一段产品落地页的开篇文案，目标用户是 SaaS 创始人，强调三个核心价值。',
    tone: StarterPromptTones.writing,
  ),
  StarterPrompt(
    icon: Icons.lightbulb_outline,
    title: '解释一个概念',
    prompt: '用一个生活类比解释一下「向量数据库」是什么，并说明它跟传统数据库的关键区别。',
    tone: StarterPromptTones.concept,
  ),
  StarterPrompt(
    icon: Icons.code,
    title: '写一段代码',
    prompt: '用 Python 写一个脚本，遍历当前目录所有 markdown 文件，把 H1 标题汇总到一个 README。',
    tone: StarterPromptTones.code,
  ),
  StarterPrompt(
    icon: Icons.translate,
    title: '翻译润色',
    prompt: '把下面这段中文翻译成自然流畅的英文，风格偏正式：',
    tone: StarterPromptTones.translate,
  ),
  StarterPrompt(
    icon: Icons.summarize_outlined,
    title: '帮我总结',
    prompt: '我会贴一段长文，请帮我提炼出三条核心观点 + 一句话总结，附原文里最有力的引用。',
    tone: StarterPromptTones.summarize,
  ),
  StarterPrompt(
    icon: Icons.bug_report_outlined,
    title: '帮我 debug',
    prompt: '我贴一段代码 + 报错信息，请按 1) 病因 2) 最小可复现 3) 修复方案 三步分析。',
    tone: StarterPromptTones.debug,
  ),
];

/// 相对时间（"刚刚 / 5 分钟前 / 3 小时前 / 2 天前 / yyyy-mm-dd"）。
String relativeTime(DateTime t, {DateTime? now}) {
  final n = now ?? DateTime.now();
  final diff = n.difference(t);
  if (diff.inSeconds < 60) return '刚刚';
  if (diff.inMinutes < 60) return '${diff.inMinutes} 分钟前';
  if (diff.inHours < 24) return '${diff.inHours} 小时前';
  if (diff.inDays < 30) return '${diff.inDays} 天前';
  String two(int n) => n.toString().padLeft(2, '0');
  return '${t.year}-${two(t.month)}-${two(t.day)}';
}

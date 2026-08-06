// 项目级 Skills 安装模型(PERI-3)。镜像 Go skillinstall 的 HubSkill / Installation。
import 'package:flutter/foundation.dart';

/// hub(~/.biumind/skills)里一个可安装的 skill。
@immutable
class HubSkill {
  const HubSkill({required this.name, required this.description, required this.dir});
  final String name;
  final String description;
  final String dir;

  factory HubSkill.fromJson(Map<String, dynamic> j) => HubSkill(
        name: j['name'] as String? ?? '',
        description: j['description'] as String? ?? '',
        dir: j['dir'] as String? ?? '',
      );
}

/// 项目内一条 skill 安装记录(扫 symlink 反推)。health ∈ ok | broken | diverged。
@immutable
class SkillInstallation {
  const SkillInstallation({required this.name, required this.agent, required this.health});
  final String name;
  final String agent; // claude | codex
  final String health;

  factory SkillInstallation.fromJson(Map<String, dynamic> j) => SkillInstallation(
        name: j['name'] as String? ?? '',
        agent: j['agent'] as String? ?? '',
        health: j['health'] as String? ?? '',
      );
}

// hook 安装状态 / 就绪态模型(PERI-1)。镜像 Go hooks 包的 InstallStatus / AgentReadiness。
import 'package:flutter/foundation.dart';

/// hook 当前安装状态(daemon hooks.status / hooks.install 返回)。
@immutable
class HookInstallStatus {
  const HookInstallStatus({
    required this.nodePath,
    required this.scriptPath,
    required this.claudeInstalled,
    required this.codexInstalled,
    this.error = '',
  });

  final String nodePath;
  final String scriptPath;
  final bool claudeInstalled;
  final bool codexInstalled;
  final String error;

  bool get hasNode => nodePath.isNotEmpty;

  factory HookInstallStatus.fromJson(Map<String, dynamic> j) => HookInstallStatus(
        nodePath: j['node_path'] as String? ?? '',
        scriptPath: j['script_path'] as String? ?? '',
        claudeInstalled: j['claude_installed'] == true,
        codexInstalled: j['codex_installed'] == true,
        error: j['error'] as String? ?? '',
      );
}

/// 单 agent 的 hook 就绪态(hooks.readiness)。reason ∈
/// ok | no_node | not_installed | version_too_low | not_found。
@immutable
class HookAgentReadiness {
  const HookAgentReadiness({
    required this.agent,
    required this.usable,
    required this.reason,
    required this.detectedVersion,
    required this.minVersion,
  });

  final String agent;
  final bool usable;
  final String reason;
  final String detectedVersion;
  final String minVersion;

  factory HookAgentReadiness.fromJson(Map<String, dynamic> j) => HookAgentReadiness(
        agent: j['agent'] as String? ?? '',
        usable: j['usable'] == true,
        reason: j['reason'] as String? ?? '',
        detectedVersion: j['detected_version'] as String? ?? '',
        minVersion: j['min_version'] as String? ?? '',
      );

  /// 面向用户的中文说明。
  String get reasonLabel => switch (reason) {
        'ok' => '已就绪',
        'no_node' => '缺少 node(hook 脚本需 Node.js)',
        'not_installed' => '未安装 hook',
        'version_too_low' => '版本过低(需 ≥ $minVersion,当前 $detectedVersion)',
        'not_found' => '未检测到该 agent',
        _ => reason,
      };
}

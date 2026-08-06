// 项目级配置模型(PERI-2)。镜像 Go projcfg.Config(.biu/config.toml 的 [agent] 段)。
import 'package:flutter/foundation.dart';

@immutable
class ProjectConfig {
  const ProjectConfig({
    this.agentDefault = 'biu',
    this.defaultPermissionMode = 'ask',
    this.promptPrefix = '',
  });

  /// 新任务默认 agent:'biu' | 'claude' | 'codex'。
  final String agentDefault;

  /// 默认权限档:'ask' | 'auto_edit' | 'full_access'。
  final String defaultPermissionMode;

  /// 自动拼到每个任务 prompt 最前的前缀。
  final String promptPrefix;

  ProjectConfig copyWith({
    String? agentDefault,
    String? defaultPermissionMode,
    String? promptPrefix,
  }) =>
      ProjectConfig(
        agentDefault: agentDefault ?? this.agentDefault,
        defaultPermissionMode:
            defaultPermissionMode ?? this.defaultPermissionMode,
        promptPrefix: promptPrefix ?? this.promptPrefix,
      );

  factory ProjectConfig.fromJson(Map<String, dynamic> j) {
    final agent = (j['agent'] as Map?)?.cast<String, dynamic>() ?? const {};
    return ProjectConfig(
      agentDefault: agent['default'] as String? ?? 'biu',
      defaultPermissionMode:
          agent['default_permission_mode'] as String? ?? 'ask',
      promptPrefix: agent['prompt_prefix'] as String? ?? '',
    );
  }

  Map<String, dynamic> toJson() => {
        'agent': {
          'default': agentDefault,
          'default_permission_mode': defaultPermissionMode,
          'prompt_prefix': promptPrefix,
        },
      };
}

// Environment —— 一台 worker 机器的客户端视图。
//
// 跟 brain 端 services/brain/internal/agentplane/store.go 的 Environment
// 字段对齐（JSON tag 不变）。Dart 端只读：repo 不修改 environment
// 状态，brain 通过 worker 心跳 + janitor 自己维护 state 流转。

class AgentEnvironment {
  /// brain 颁的 UUID，session attach / pool 选择都用它。
  final String environmentId;

  /// 'biu_daemon' | 'biu_cli' | 'runtime'
  final String workerKind;

  /// hostname / 用户起的别名。UI 列表展示。
  final String machineName;

  /// 'darwin/arm64' / 'linux/amd64' / 等。可空（早期注册没填）。
  final String? osArch;

  /// 自陈能力清单（'sandbox' / 'skills' / 'apps' / ...）。当前 brain
  /// 不读它做调度，仅 UI 展示。
  final List<String> capabilities;

  /// runtime 池标签 —— task mode 按 pool_tag 选 runtime。
  final String? poolTag;

  /// 'online' / 'offline' / 'dead'. UI 状态徽章 + 列表过滤。
  final String state;

  /// brain 端用 last_seen_at - now() > 90s 标 offline。客户端展示
  /// "X 秒前活跃"用。
  final DateTime? lastSeenAt;

  /// 注册时间。UI 排序 secondary key。
  final DateTime? createdAt;

  AgentEnvironment({
    required this.environmentId,
    required this.workerKind,
    required this.machineName,
    this.osArch,
    this.capabilities = const [],
    this.poolTag,
    required this.state,
    this.lastSeenAt,
    this.createdAt,
  });

  bool get isOnline => state == 'online';

  factory AgentEnvironment.fromJson(Map<String, dynamic> json) {
    return AgentEnvironment(
      environmentId: json['environment_id'] as String,
      workerKind: json['worker_kind'] as String? ?? 'unknown',
      machineName: json['machine_name'] as String? ?? '',
      osArch: json['os_arch'] as String?,
      capabilities: (json['capabilities'] as List?)?.cast<String>() ?? const [],
      poolTag: json['pool_tag'] as String?,
      state: json['state'] as String? ?? 'unknown',
      lastSeenAt: _parseTime(json['last_seen_at']),
      createdAt: _parseTime(json['created_at']),
    );
  }

  static DateTime? _parseTime(Object? raw) {
    if (raw == null) return null;
    if (raw is String) {
      try {
        return DateTime.parse(raw);
      } catch (_) {
        return null;
      }
    }
    return null;
  }
}

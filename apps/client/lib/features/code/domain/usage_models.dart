// 用量快照模型 —— 对应 biu CLI code/usage 包(usage.read RPC)的 JSON。
//
// 数据源(详见 Go 包注释):Claude = 用户个人订阅 5h/7d 窗(经 keychain OAuth +
// Anthropic usage 端点);Codex = codex app-server RPC。任一源可能 unavailable
// (未装/未登录/限流),UI 据 available 降级显示 reason。

/// 一个用量窗口:已用/剩余百分比(0–100 整数)+ 可选重置时刻(Unix 秒)。
class UsageWindow {
  final int usedPercent;
  final int remainingPercent;
  final int? resetAt;

  const UsageWindow({
    required this.usedPercent,
    required this.remainingPercent,
    this.resetAt,
  });

  factory UsageWindow.fromJson(Map<String, dynamic> j) => UsageWindow(
        usedPercent: (j['usedPercent'] as num?)?.toInt() ?? 0,
        remainingPercent: (j['remainingPercent'] as num?)?.toInt() ?? 0,
        resetAt: (j['resetAt'] as num?)?.toInt(),
      );

  /// 重置时刻的 DateTime(本地时区);无则 null。
  DateTime? get resetAtTime => resetAt == null
      ? null
      : DateTime.fromMillisecondsSinceEpoch(resetAt! * 1000);
}

/// Claude 订阅的两档窗口。
class ClaudeUsage {
  final UsageWindow? fiveHour;
  final UsageWindow? sevenDay;

  const ClaudeUsage({this.fiveHour, this.sevenDay});

  factory ClaudeUsage.fromJson(Map<String, dynamic> j) => ClaudeUsage(
        fiveHour: _win(j['fiveHour']),
        sevenDay: _win(j['sevenDay']),
      );
}

/// Codex 账户信息 + 两档限流窗口。
class CodexUsage {
  final String? email;
  final String? planType;
  final UsageWindow? primary;
  final UsageWindow? secondary;

  const CodexUsage({this.email, this.planType, this.primary, this.secondary});

  factory CodexUsage.fromJson(Map<String, dynamic> j) => CodexUsage(
        email: j['email'] as String?,
        planType: j['planType'] as String?,
        primary: _win(j['primary']),
        secondary: _win(j['secondary']),
      );
}

/// 一个数据源的可用态 + 数据 or 不可用原因。
class UsageSource<T> {
  final bool available;
  final T? data;
  final String? reason;

  const UsageSource({required this.available, this.data, this.reason});

  static UsageSource<T> fromJson<T>(
    Map<String, dynamic>? j,
    T Function(Map<String, dynamic>) parse,
  ) {
    j ??= const {};
    final ok = j['status'] == 'available';
    return UsageSource<T>(
      available: ok,
      data: ok ? parse((j['data'] as Map<String, dynamic>?) ?? const {}) : null,
      reason: j['reason'] as String?,
    );
  }
}

/// 一次用量读取的完整结果。
class UsageSnapshot {
  final UsageSource<ClaudeUsage> claude;
  final UsageSource<CodexUsage> codex;
  final int fetchedAt;

  const UsageSnapshot({
    required this.claude,
    required this.codex,
    required this.fetchedAt,
  });

  factory UsageSnapshot.fromJson(Map<String, dynamic> j) => UsageSnapshot(
        claude: UsageSource.fromJson(
            j['claude'] as Map<String, dynamic>?, ClaudeUsage.fromJson),
        codex: UsageSource.fromJson(
            j['codex'] as Map<String, dynamic>?, CodexUsage.fromJson),
        fetchedAt: (j['fetchedAt'] as num?)?.toInt() ?? 0,
      );
}

UsageWindow? _win(dynamic v) =>
    v is Map<String, dynamic> ? UsageWindow.fromJson(v) : null;

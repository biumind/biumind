// agent 自动检测结果 —— 对应 biu CLI 的 agent.detect RPC(agent/detect.go)。
// 设置面板「自动检测」用:扫 PATH + 候选目录找到的路径 + 版本。

class AgentDetectResult {
  /// 检测到的二进制绝对路径(found=false 时为空)。
  final String path;

  /// `--version` 输出首行(探测失败为空,不影响 found)。
  final String version;

  /// PATH 或候选位置是否找到该 binary。
  final bool found;

  const AgentDetectResult({
    required this.path,
    required this.version,
    required this.found,
  });

  factory AgentDetectResult.fromJson(Map<String, dynamic> j) =>
      AgentDetectResult(
        path: j['path'] as String? ?? '',
        version: j['version'] as String? ?? '',
        found: j['found'] as bool? ?? false,
      );
}

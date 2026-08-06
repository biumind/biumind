// 静态 binary 路径解析 — adapter spawn 前必调, 避免 Process.start 在 binary
// 不存在时落入 macOS hardened runtime native 边界条件 (会让整 app crash)。
//
// 跟 settings/coding_workbench_pane.dart 的 _BinaryTester._resolveBinary
// 同一逻辑, 这里抽到共享位置让 3 个 adapter 复用。

import 'dart:io';

/// 静态解析 binary 路径 (不 spawn)。
///   - 包含 "/" → 当作绝对/相对路径, 检查文件存在性 + 可执行位
///   - 否则 → 在 PATH 各 segment 里挨个找 (优先 extraSearchPath, fallback
///     Platform.environment['PATH'])
///
/// 找不到返回 null。caller 直接 emit error 不要进 Process.start。
Future<String?> resolveBinary(
  String name, {
  String? extraSearchPath,
}) async {
  if (name.contains('/')) {
    return await _isExecutable(name) ? name : null;
  }
  final candidates = <String>{};
  if (extraSearchPath != null && extraSearchPath.isNotEmpty) {
    candidates.addAll(extraSearchPath.split(':'));
  }
  final pathEnv = Platform.environment['PATH'] ?? '';
  candidates.addAll(pathEnv.split(':'));
  for (final dir in candidates) {
    if (dir.isEmpty) continue;
    final candidate = '$dir/$name';
    if (await _isExecutable(candidate)) return candidate;
  }
  return null;
}

Future<bool> _isExecutable(String path) async {
  try {
    final f = File(path);
    if (!await f.exists()) return false;
    final stat = await f.stat();
    // executable 位检查 (任意 user/group/other x 位置 1)
    return (stat.mode & 0x49) != 0; // 0o111 = 0x49
  } catch (_) {
    return false;
  }
}

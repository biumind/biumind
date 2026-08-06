// ShellTerminalController — 独立 shell 终端会话管理(Code-Design §5.1「PTY(任务
// + Shell)」的 Shell 半边)。
//
// 与任务 PTY(code.runTask,pty_id=task_id,Agent Tab 看 agent 干活)彻底解耦:
// shell 是用户在项目目录里随手开的交互终端(zsh/bash),跟任何任务无关。后端复用
// 已有的 pty.open(开任意命令、server 自分配 pty_id),无需 code.runTask。
//
// 作用域:每项目可多开。state 按 projectId 存一组 ShellSession;切项目
// 切 shell 列表,后台 shell 跨 Tab/项目切换保活(进程在 daemon 侧),直到显式关闭 /
// daemon 退出(app 退出时 SIGTERM 同杀)。

import 'dart:io' show Platform;

import 'package:flutter/foundation.dart' show kIsWeb;
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:uuid/uuid.dart';

import '../data/code_bridge_client.dart';
import '../data/code_bridge_provider.dart';

/// 一个 shell PTY 会话。
class ShellSession {
  const ShellSession({
    required this.id,
    required this.ptyId,
    required this.label,
  });

  /// 本地稳定 id(tab key 用;不等于 server pty_id)。
  final String id;

  /// server 分配的 pty_id —— CodeTerminalView 订阅 / sendInput / kill 都用它。
  final String ptyId;

  /// 展示名,如 "Shell 1"。
  final String label;
}

class ShellTerminalController
    extends StateNotifier<Map<String, List<ShellSession>>> {
  ShellTerminalController(this._client) : super(const {});

  /// 当前 live bridge(null = daemon 未就绪 / 非桌面)。
  final CodeBridgeClient? _client;
  final _uuid = const Uuid();

  /// 解析登录 shell:优先 $SHELL,否则平台兜底。web 不会走到(bridge 必 null)。
  String _resolveShell() {
    if (kIsWeb) return '/bin/sh';
    final s = Platform.environment['SHELL'];
    if (s != null && s.isNotEmpty) return s;
    return Platform.isMacOS ? '/bin/zsh' : '/bin/bash';
  }

  List<ShellSession> shellsFor(String projectId) =>
      state[projectId] ?? const [];

  /// 在 [cwd] 开一个新 shell,挂到 [projectId] 名下。daemon 未就绪 / bridge 未连上
  /// → 返回 null(不抛),交给 UI 显示「连接失败 · 重试」而非崩溃。
  Future<ShellSession?> open(String projectId, String cwd) async {
    final c = _client;
    if (c == null) return null;
    final shell = _resolveShell();
    try {
      // -l 登录 shell:加载用户 rc(PATH/别名),贴近用户平时的终端体验。
      final ptyId = await c.openPty(
        cmd: shell,
        args: const ['-l'],
        cwd: cwd,
        cols: 80,
        rows: 24,
      );
      final list = state[projectId] ?? const [];
      final session = ShellSession(
        id: _uuid.v4(),
        ptyId: ptyId,
        label: 'Shell ${list.length + 1}',
      );
      state = {...state, projectId: [...list, session]};
      return session;
    } on CodeBridgeException {
      // WS 尚未升级成功 / daemon 刚起还没连上 → 静默失败,UI 可重试。
      return null;
    }
  }

  /// 关闭某个 shell:杀 PTY + 从列表移除。
  Future<void> close(String projectId, String sessionId) async {
    final list = state[projectId] ?? const [];
    ShellSession? target;
    for (final s in list) {
      if (s.id == sessionId) {
        target = s;
        break;
      }
    }
    if (target != null) {
      try {
        await _client?.killPty(target.ptyId);
      } on CodeBridgeException {
        // 进程可能已退出;忽略,继续从列表移除。
      }
    }
    state = {
      ...state,
      projectId: list.where((s) => s.id != sessionId).toList(),
    };
  }
}

/// live ShellTerminalController —— 随 bridge client 重建(daemon 重连时会重置,
/// M2 单次连接稳定,可接受;真重连保活留后续里程碑)。
final shellTerminalControllerProvider = StateNotifierProvider<
    ShellTerminalController, Map<String, List<ShellSession>>>((ref) {
  final client = ref.watch(codeBridgeClientProvider);
  return ShellTerminalController(client);
});

// CodeTerminalView — 真终端渲染(M2)。xterm 渲染从本地 bridge 收到的 PTY 字节流
// (code_pty_chunk),用户输入/尺寸回送 bridge(code_pty_input/resize)。
//
// PTY 字节是原始 UTF-8 流,可能在任意字节处被分块切断。xterm 的 Terminal.write
// 收 String,故这里做增量 UTF-8 解码:暂存尾部不完整的多字节序列,等下一块拼齐
// 再 decode(leftover 逻辑放在消费端)。
//
// 按 ptyId(=task_id)过滤该 PTY 的流;daemon 未连(codeBridgeClientProvider==null)
// 时显示提示。

import 'dart:async';
import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:xterm/xterm.dart';

import '../../../data/api/sdkproto/v1/code.dart' show CodePtyChunk, CodePtyExit;
import '../data/code_bridge_provider.dart';
import '../data/incremental_utf8.dart';

// 终端 ANSI 调色板 —— 跟随 app 明暗切换(LIGHT/DARK 主题)。浅色模式下终端用
// 浅底深字(GitHub light 调色板),不再是突兀的黑框;深色模式用柔和深色。
//
// 注:这里的 Color(0xFF..) 是「终端 ANSI 16 色调色板」,属固定色彩数据(同
// palettes.dart 性质,无法用品牌/中性 token 表达),是 T1「禁硬编码色」的合理
// 例外 —— 不是业务 UI 配色。
const TerminalTheme _kLightTerminalTheme = TerminalTheme(
  cursor: Color(0xFF24292F),
  selection: Color(0xFFB3D7FF),
  foreground: Color(0xFF24292F),
  background: Color(0xFFFFFFFF),
  black: Color(0xFF24292F),
  red: Color(0xFFCF222E),
  green: Color(0xFF116329),
  yellow: Color(0xFF9A6700),
  blue: Color(0xFF0550AE),
  magenta: Color(0xFF8250DF),
  cyan: Color(0xFF1B7C83),
  white: Color(0xFF6E7781),
  brightBlack: Color(0xFF57606A),
  brightRed: Color(0xFFA40E26),
  brightGreen: Color(0xFF1A7F37),
  brightYellow: Color(0xFF633C01),
  brightBlue: Color(0xFF0969DA),
  brightMagenta: Color(0xFF6639BA),
  brightCyan: Color(0xFF3192AA),
  brightWhite: Color(0xFF8C959F),
  searchHitBackground: Color(0xFFFFFF2B),
  searchHitBackgroundCurrent: Color(0xFF31FF26),
  searchHitForeground: Color(0xFF000000),
);

const TerminalTheme _kDarkTerminalTheme = TerminalTheme(
  cursor: Color(0xFFCDD6F4),
  selection: Color(0xFF45475A),
  foreground: Color(0xFFCDD6F4),
  background: Color(0xFF1E2230),
  black: Color(0xFF484F58),
  red: Color(0xFFFF7B72),
  green: Color(0xFF3FB950),
  yellow: Color(0xFFD29922),
  blue: Color(0xFF58A6FF),
  magenta: Color(0xFFD2A8FF),
  cyan: Color(0xFF39C5CF),
  white: Color(0xFFB1BAC4),
  brightBlack: Color(0xFF6E7681),
  brightRed: Color(0xFFFFA198),
  brightGreen: Color(0xFF56D364),
  brightYellow: Color(0xFFE3B341),
  brightBlue: Color(0xFF79C0FF),
  brightMagenta: Color(0xFFF0A1FF),
  brightCyan: Color(0xFF56D4DD),
  brightWhite: Color(0xFFF0F6FC),
  searchHitBackground: Color(0xFFFFFF2B),
  searchHitBackgroundCurrent: Color(0xFF31FF26),
  searchHitForeground: Color(0xFF000000),
);

class CodeTerminalView extends ConsumerStatefulWidget {
  const CodeTerminalView({super.key, required this.ptyId, this.finished = false});

  /// 要渲染的 PTY id(编码任务 = task_id)。
  final String ptyId;

  /// 任务是否已终态(done/failed/interrupted)。终态任务的 PTY 进程已死,
  /// 不会再有实时字节;若本会话也没缓冲到内容(典型:app 重启后),原始终端
  /// 无内容可显 —— 此时显空态引导看「结构化」回放(已结束任务只看
  /// 结构化 SessionView,从不展示空的原始终端;我们不持久化 PTY scrollback)。
  final bool finished;

  @override
  ConsumerState<CodeTerminalView> createState() => _CodeTerminalViewState();
}

class _CodeTerminalViewState extends ConsumerState<CodeTerminalView> {
  late final Terminal _terminal;
  final _decoder = IncrementalUtf8Decoder();
  StreamSubscription<CodePtyChunk>? _chunkSub;
  StreamSubscription<CodePtyExit>? _exitSub;
  bool _exited = false;
  bool _wroteAnything = false;
  bool _replayDone = false;

  @override
  void initState() {
    super.initState();
    _terminal = Terminal(maxLines: 10000);
    // 用户在终端里敲键 → 回送 PTY 输入。
    _terminal.onOutput = (data) {
      ref
          .read(codeBridgeClientProvider)
          ?.sendInput(widget.ptyId, utf8.encode(data));
    };
    // 终端尺寸变化 → 通知 PTY(SIGWINCH),让 TUI 正确排版。
    _terminal.onResize = (w, h, pw, ph) {
      ref.read(codeBridgeClientProvider)?.resize(widget.ptyId, w, h);
    };
    _replayThenAttach();
  }

  /// 先回放落盘的 PTY 历史(重开后原始终端有内容),再挂 live 流。已结束任务无
  /// live(PTY 已死),回放即全部;运行中任务日志为空(刚起)或仅历史,live 续上。
  Future<void> _replayThenAttach() async {
    final client = ref.read(codeBridgeClientProvider);
    if (client != null) {
      try {
        final hist = await client.ptyReplayLog(widget.ptyId);
        if (mounted && hist.isNotEmpty) {
          final text = _decoder.decode(hist);
          if (text.isNotEmpty) {
            _terminal.write(text);
            _wroteAnything = true;
          }
        }
      } catch (_) {
        // 回放失败不致命:退化成仅 live(与未持久化时等价)。
      }
    }
    if (!mounted) return;
    _attach();
    setState(() => _replayDone = true);
  }

  void _attach() {
    final client = ref.read(codeBridgeClientProvider);
    if (client == null) return; // daemon 未就绪
    _chunkSub = client.ptyChunks
        .where((c) => c.ptyId == widget.ptyId)
        .listen(_onChunk);
    _exitSub =
        client.ptyExits.where((e) => e.ptyId == widget.ptyId).listen(_onExit);
  }

  void _onChunk(CodePtyChunk chunk) {
    final text = _decoder.decode(chunk.data);
    if (text.isNotEmpty) {
      _terminal.write(text);
      if (!_wroteAnything) {
        _wroteAnything = true;
        // 终态任务首帧到 → 从空态切回终端视图(live 任务本就直接显终端)。
        if (widget.finished && mounted) setState(() {});
      }
    }
  }

  void _onExit(CodePtyExit exit) {
    if (_exited) return;
    _exited = true;
    final suffix = exit.error != null && exit.error!.isNotEmpty
        ? ' (${exit.error})'
        : '';
    _terminal.write('\r\n\x1b[90m[进程已退出 code=${exit.exitCode}$suffix]\x1b[0m\r\n');
  }

  @override
  void dispose() {
    _chunkSub?.cancel();
    _exitSub?.cancel();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final connected = ref.watch(codeBridgeClientProvider) != null;
    if (!connected) {
      return Center(
        child: Text('本地 daemon 未就绪 —— 终端不可用',
            style: Theme.of(context).textTheme.bodySmall),
      );
    }
    // 已结束任务:等回放读盘完成再判定,避免空态闪烁。回放后仍无内容(无日志,
    // 如 T1 之前的旧任务 / 从未产出)→ 显空态提示。
    if (widget.finished && !_replayDone) {
      return const Center(
        child: SizedBox(
          width: 18, height: 18, child: CircularProgressIndicator(strokeWidth: 2)),
      );
    }
    if (widget.finished && _replayDone && !_wroteAnything) {
      final muted = Theme.of(context).textTheme.bodySmall?.color;
      return Center(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(Icons.terminal_rounded, size: 28, color: muted),
            const SizedBox(height: 10),
            Text('该任务没有可回放的终端记录',
                style: Theme.of(context).textTheme.bodyMedium),
            const SizedBox(height: 4),
            Text('(早于终端持久化的旧任务,或未产生输出)',
                style: Theme.of(context).textTheme.bodySmall),
          ],
        ),
      );
    }
    // 终端配色跟随 app 明暗(修浅色模式下终端是黑框的问题)。
    final isDark = Theme.of(context).brightness == Brightness.dark;
    return TerminalView(
      _terminal,
      theme: isDark ? _kDarkTerminalTheme : _kLightTerminalTheme,
    );
  }
}

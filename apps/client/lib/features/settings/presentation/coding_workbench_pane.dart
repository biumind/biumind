// 编码工作台 settings pane — 配置 4 个字段：
//   - workingDir：任务执行的工作目录 (默认 user HOME)
//   - biu / claude / codex 三个 binary 的路径 (默认走 PATH 解析)
//
// 每个字段带 [Test] 按钮: 执行 `<path> --version`/`version`, 显示成功/失败。

import 'dart:async';
import 'dart:convert' show utf8;
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../app/theme.dart';
import '../../../features/code/data/code_bridge_provider.dart';
import '../../../services/login_shell_env.dart';
import '../application/settings_controller.dart';

class CodingWorkbenchPane extends ConsumerStatefulWidget {
  const CodingWorkbenchPane({super.key});
  @override
  ConsumerState<CodingWorkbenchPane> createState() => _CodingWorkbenchPaneState();
}

class _CodingWorkbenchPaneState extends ConsumerState<CodingWorkbenchPane> {
  late final _cwd = TextEditingController();
  late final _biu = TextEditingController();
  late final _claude = TextEditingController();
  late final _codex = TextEditingController();
  bool _initialized = false;
  bool _saving = false;
  bool _useWorktree = true;
  bool _detecting = false;

  @override
  void dispose() {
    _cwd.dispose();
    _biu.dispose();
    _claude.dispose();
    _codex.dispose();
    super.dispose();
  }

  void _hydrateOnce() {
    if (_initialized) return;
    final s = ref.read(settingsControllerProvider).valueOrNull;
    if (s == null) return;
    _cwd.text = s.codeWorkingDir ?? '';
    _biu.text = s.codeBiuPath ?? '';
    _claude.text = s.codeClaudePath ?? '';
    _codex.text = s.codeCodexPath ?? '';
    _useWorktree = s.codeUseWorktree;
    _initialized = true;
  }

  Future<void> _save() async {
    setState(() => _saving = true);
    try {
      await ref.read(settingsControllerProvider.notifier).updateCodingPaths(
            workingDir: _cwd.text,
            biuPath: _biu.text,
            claudePath: _claude.text,
            codexPath: _codex.text,
            useWorktree: _useWorktree,
          );
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Saved'), duration: Duration(seconds: 1)),
      );
    } finally {
      if (mounted) setState(() => _saving = false);
    }
  }

  /// 即时持久化 toggle 状态 — 跟点「保存」按钮的全字段写入不同, 这条
  /// 仅同步两个 boolean, 不动用户在 path 输入框里还没按保存的草稿。
  /// 不阻塞 UI (Switch 已经走了 setState 立刻反馈)。
  Future<void> _persistToggles() async {
    try {
      await ref.read(settingsControllerProvider.notifier).updateCodingPaths(
            useWorktree: _useWorktree,
          );
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('保存失败: $e')),
      );
    }
  }

  void _reset() {
    setState(() {
      _cwd.clear();
      _biu.clear();
      _claude.clear();
      _codex.clear();
    });
  }

  /// 自动检测:经 daemon 扫 PATH + 候选目录(nvm/brew/.local/bin…)找 claude/
  /// codex/biu。只填**空**字段(不覆盖用户已填),并 snackbar 汇总各 agent 版本。
  /// daemon 未连接时按钮禁用(逐字段 Test 仍可用,见下方)。
  Future<void> _autoDetect() async {
    final client = ref.read(codeBridgeClientProvider);
    if (client == null) return;
    setState(() => _detecting = true);
    try {
      final res = await client.detectAgents();
      if (!mounted) return;
      void fill(TextEditingController c, String key) {
        final r = res[key];
        if (r != null && r.found && c.text.trim().isEmpty) c.text = r.path;
      }
      fill(_biu, 'biu');
      fill(_claude, 'claude');
      fill(_codex, 'codex');
      setState(() {});
      final summary = ['claude', 'codex', 'biu'].map((k) {
        final r = res[k];
        if (r == null || !r.found) return '$k 未找到';
        return '$k ${r.version.isEmpty ? "✓" : r.version}';
      }).join('   ');
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text(summary), duration: const Duration(seconds: 3)),
      );
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('自动检测失败: $e')),
      );
    } finally {
      if (mounted) setState(() => _detecting = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    _hydrateOnce();
    final shell = ref.watch(loginShellEnvProvider).valueOrNull;
    final pathPreview = shell?.path ?? Platform.environment['PATH'] ?? '(loading…)';

    return ListView(
      padding: const EdgeInsets.all(BiuTokens.space5),
      children: [
        const _Title('编码工作台 · 二进制 & 工作目录'),
        const SizedBox(height: 6),
        Text(
          '配置 AI 编程 agent 的可执行文件路径与默认工作目录。'
          '留空走 PATH 自动查找；有特殊安装位置时填绝对路径。',
          style: TextStyle(fontSize: 12.5, color: BiuTokens.textSecondary, height: 1.6),
        ),
        const SizedBox(height: BiuTokens.space3),

        // 自动检测:经 daemon 扫 PATH + nvm/brew/.local/bin 找 binary,填空字段。
        // daemon 未连接(非桌面 / 未起)时禁用 —— 逐字段 Test 仍可用。
        Builder(builder: (context) {
          final connected = ref.watch(codeBridgeClientProvider) != null;
          return Align(
            alignment: Alignment.centerLeft,
            child: OutlinedButton.icon(
              onPressed: (connected && !_detecting) ? _autoDetect : null,
              icon: _detecting
                  ? const SizedBox(
                      width: 13,
                      height: 13,
                      child: CircularProgressIndicator(strokeWidth: 1.5),
                    )
                  : const Icon(Icons.auto_fix_high_rounded, size: 15),
              label: Text(connected ? '自动检测路径与版本' : '自动检测(需 daemon 在线)'),
              style: OutlinedButton.styleFrom(
                visualDensity: VisualDensity.compact,
                padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 10),
                side: BorderSide(color: BiuTokens.borderSubtle),
                foregroundColor: BiuTokens.purple,
                textStyle:
                    const TextStyle(fontSize: 12.5, fontWeight: FontWeight.w500),
              ),
            ),
          );
        }),
        const SizedBox(height: BiuTokens.space5),

        // 隔离 toggle — 放在最显眼位置, 影响"任务跑在哪"的根本行为
        Container(
          padding: const EdgeInsets.all(BiuTokens.space3),
          decoration: BoxDecoration(
            color: BiuTokens.bg,
            borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
            border: Border.all(color: BiuTokens.borderSubtle),
          ),
          child: Row(
            children: [
              Icon(
                Icons.merge_type_rounded,
                size: 18,
                color: _useWorktree ? BiuTokens.purple : BiuTokens.textMuted,
              ),
              const SizedBox(width: 10),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    const Text(
                      '任务隔离 (worktree)',
                      style: TextStyle(fontSize: 13, fontWeight: FontWeight.w600),
                    ),
                    const SizedBox(height: 2),
                    Text(
                      _useWorktree
                          ? '每个任务独立 git worktree + 分支 (biu/<agent>-<id>)，'
                              '多任务并行互不干扰'
                          : '所有任务共享 working dir，多任务并行可能互相覆盖文件',
                      style: TextStyle(
                        fontSize: 11.5,
                        color: BiuTokens.textSecondary,
                        height: 1.5,
                      ),
                    ),
                  ],
                ),
              ),
              Switch(
                value: _useWorktree,
                activeThumbColor: BiuTokens.purple,
                onChanged: (v) async {
                  setState(() => _useWorktree = v);
                  await _persistToggles();
                },
              ),
            ],
          ),
        ),
        const SizedBox(height: BiuTokens.space4),

        _PathField(
          label: 'Working dir',
          icon: Icons.folder_outlined,
          controller: _cwd,
          placeholder: Platform.environment['HOME'] ?? '/',
          hint: '任务执行的目录，agent 在此读写文件',
          tester: null, // dir 本地存在性: 只有路径校验, 不 spawn binary
          dirCheck: true,
        ),
        const SizedBox(height: BiuTokens.space4),

        _PathField(
          label: 'biu',
          icon: Icons.terminal_rounded,
          color: BiuTokens.purple,
          controller: _biu,
          placeholder: 'biu',
          hint: 'BiuMind 自家 CLI · `task cli:install` 装到 ~/.local/bin/biu',
          tester: _BinaryTester(
            getPath: () => _biu.text.trim().isEmpty ? 'biu' : _biu.text.trim(),
            args: const ['version'],
          ),
        ),
        const SizedBox(height: BiuTokens.space4),

        _PathField(
          label: 'Claude',
          icon: Icons.diamond_outlined,
          color: AgentKindColors.claude,
          controller: _claude,
          placeholder: 'claude',
          hint: 'Claude Code · npm install -g @anthropic-ai/claude-code',
          tester: _BinaryTester(
            getPath: () => _claude.text.trim().isEmpty ? 'claude' : _claude.text.trim(),
            args: const ['--version'],
          ),
        ),
        const SizedBox(height: BiuTokens.space4),

        _PathField(
          label: 'Codex',
          icon: Icons.psychology_alt_outlined,
          color: AgentKindColors.codex,
          controller: _codex,
          placeholder: 'codex',
          hint: 'OpenAI Codex CLI · npm install -g @openai/codex',
          tester: _BinaryTester(
            getPath: () => _codex.text.trim().isEmpty ? 'codex' : _codex.text.trim(),
            args: const ['--version'],
          ),
        ),

        const SizedBox(height: BiuTokens.space5),
        Divider(color: BiuTokens.borderSubtle),
        const SizedBox(height: BiuTokens.space3),
        const _Title('当前生效的 PATH'),
        const SizedBox(height: 6),
        Text(
          '从 login shell (\$SHELL -l -i) 解析后的 PATH，BiuAdapter / Claude / Codex '
          'spawn 子进程时合并使用。',
          style: TextStyle(fontSize: 12, color: BiuTokens.textSecondary, height: 1.5),
        ),
        const SizedBox(height: 8),
        Container(
          padding: const EdgeInsets.all(10),
          decoration: BoxDecoration(
            color: BiuTokens.bg,
            borderRadius: BorderRadius.circular(BiuTokens.radiusXs),
            border: Border.all(color: BiuTokens.borderSubtle),
          ),
          child: SelectableText(
            pathPreview.split(':').join(':\n'),
            style: const TextStyle(
              fontSize: 11.5,
              fontFamily: 'SF Mono',
              height: 1.5,
            ),
          ),
        ),

        const SizedBox(height: BiuTokens.space5),
        Row(
          children: [
            const Spacer(),
            TextButton(
              onPressed: _saving ? null : _reset,
              style: TextButton.styleFrom(foregroundColor: BiuTokens.textSecondary),
              child: const Text('恢复默认'),
            ),
            const SizedBox(width: 8),
            FilledButton(
              onPressed: _saving ? null : _save,
              style: FilledButton.styleFrom(
                backgroundColor: BiuTokens.purple,
                padding: const EdgeInsets.symmetric(horizontal: 18, vertical: 10),
                shape: RoundedRectangleBorder(
                  borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
                ),
              ),
              child: _saving
                  ? const SizedBox(
                      width: 14,
                      height: 14,
                      child: CircularProgressIndicator(
                        strokeWidth: 1.5,
                        color: Colors.white,
                      ),
                    )
                  : const Text('保存'),
            ),
          ],
        ),
      ],
    );
  }
}

// ─── 内部 widgets ──────────────────────────────────────────

class _Title extends StatelessWidget {
  const _Title(this.text);
  final String text;
  @override
  Widget build(BuildContext context) {
    return Text(
      text,
      style: const TextStyle(
        fontSize: 16,
        fontWeight: FontWeight.w600,
        letterSpacing: -0.2,
      ),
    );
  }
}

/// 一行 path 配置: label + icon + 输入框 + Test 按钮 + hint + 状态。
class _PathField extends ConsumerStatefulWidget {
  const _PathField({
    required this.label,
    required this.icon,
    required this.controller,
    required this.placeholder,
    required this.hint,
    this.color,
    this.tester,
    this.dirCheck = false,
  });

  final String label;
  final IconData icon;
  final Color? color;
  final TextEditingController controller;
  final String placeholder;
  final String hint;
  final _BinaryTester? tester;
  final bool dirCheck;

  @override
  ConsumerState<_PathField> createState() => _PathFieldState();
}

class _PathFieldState extends ConsumerState<_PathField> {
  _TestStatus _status = const _TestStatus.idle();
  bool _testing = false;

  Future<void> _runTest() async {
    setState(() {
      _testing = true;
      _status = const _TestStatus.idle();
    });
    try {
      if (widget.dirCheck) {
        final path = widget.controller.text.trim().isEmpty
            ? Platform.environment['HOME'] ?? '/'
            : widget.controller.text.trim();
        final exists = await Directory(path).exists();
        if (!mounted) return;
        setState(() {
          _status = exists
              ? _TestStatus.ok('$path ✓')
              : _TestStatus.error('目录不存在: $path');
        });
      } else if (widget.tester != null) {
        // 用 login shell 解析出来的完整 PATH 搜索 (Dock 启动时 process env
        // 的 PATH 不全), 找不到就 fallback 到 Platform.environment。
        final shellPath = ref.read(loginShellEnvProvider).valueOrNull?.path;
        final res = await widget.tester!.run(searchPath: shellPath);
        if (!mounted) return;
        setState(() => _status = res);
      }
    } finally {
      if (mounted) setState(() => _testing = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final color = widget.color ?? BiuTokens.textSecondary;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Row(
          children: [
            Icon(widget.icon, size: 16, color: color),
            const SizedBox(width: 8),
            Text(
              widget.label,
              style: const TextStyle(fontSize: 13, fontWeight: FontWeight.w600),
            ),
          ],
        ),
        const SizedBox(height: 6),
        Row(
          children: [
            Expanded(
              child: TextField(
                controller: widget.controller,
                style: const TextStyle(fontSize: 12.5, fontFamily: 'SF Mono'),
                decoration: InputDecoration(
                  hintText: widget.placeholder,
                  hintStyle: TextStyle(
                    color: BiuTokens.textMuted,
                    fontFamily: 'SF Mono',
                    fontSize: 12.5,
                  ),
                  filled: true,
                  fillColor: BiuTokens.bg,
                  isDense: true,
                  contentPadding: const EdgeInsets.symmetric(
                    horizontal: 10,
                    vertical: 10,
                  ),
                  border: OutlineInputBorder(
                    borderRadius: BorderRadius.circular(BiuTokens.radiusXs),
                    borderSide: BorderSide(color: BiuTokens.borderSubtle),
                  ),
                  enabledBorder: OutlineInputBorder(
                    borderRadius: BorderRadius.circular(BiuTokens.radiusXs),
                    borderSide: BorderSide(color: BiuTokens.borderSubtle),
                  ),
                  focusedBorder: OutlineInputBorder(
                    borderRadius: BorderRadius.circular(BiuTokens.radiusXs),
                    borderSide: BorderSide(color: BiuTokens.purple, width: 1.4),
                  ),
                ),
                onChanged: (_) {
                  if (_status.kind != _StatusKind.idle) {
                    setState(() => _status = const _TestStatus.idle());
                  }
                },
              ),
            ),
            const SizedBox(width: 8),
            OutlinedButton.icon(
              onPressed: _testing ? null : _runTest,
              icon: _testing
                  ? const SizedBox(
                      width: 12,
                      height: 12,
                      child: CircularProgressIndicator(strokeWidth: 1.4),
                    )
                  : const Icon(Icons.bolt_rounded, size: 14),
              label: const Text('Test'),
              style: OutlinedButton.styleFrom(
                visualDensity: VisualDensity.compact,
                padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 11),
                side: BorderSide(color: BiuTokens.borderSubtle),
                foregroundColor: BiuTokens.text,
                textStyle: const TextStyle(fontSize: 12, fontWeight: FontWeight.w500),
              ),
            ),
          ],
        ),
        const SizedBox(height: 4),
        Text(
          widget.hint,
          style: TextStyle(fontSize: 11.5, color: BiuTokens.textSecondary),
        ),
        if (_status.kind != _StatusKind.idle)
          Padding(
            padding: const EdgeInsets.only(top: 6),
            child: Row(
              children: [
                Icon(
                  _status.kind == _StatusKind.ok
                      ? Icons.check_circle_rounded
                      : Icons.error_outline_rounded,
                  size: 14,
                  color: _status.kind == _StatusKind.ok
                      ? BiuTokens.green
                      : Colors.red,
                ),
                const SizedBox(width: 6),
                Expanded(
                  child: SelectableText(
                    _status.message,
                    style: TextStyle(
                      fontSize: 11.5,
                      fontFamily: 'SF Mono',
                      color: _status.kind == _StatusKind.ok
                          ? BiuTokens.green
                          : Colors.red,
                    ),
                  ),
                ),
              ],
            ),
          ),
      ],
    );
  }
}

class _BinaryTester {
  const _BinaryTester({required this.getPath, required this.args});
  final String Function() getPath;
  final List<String> args;

  Future<_TestStatus> run({String? searchPath}) async {
    final path = getPath();
    // ── 第一道：不 spawn 直接用 PATH 解析探测 ────────────────
    // Dart Process.run 在 binary 不存在时, 极少情况下会让 macOS hardened
    // runtime / dyld layer crash native 端, 不能保证 ProcessException 被
    // 优雅 throw。所以先静态解析:
    //   - 包含 "/" → 当作绝对/相对路径, 检查文件存在性 + 可执行位
    //   - 否则 → 在 PATH 各 segment 里挨个找 (优先用 login shell PATH)
    // 找不到直接返回错误, 不进入 Process.run 的"未知"领域。
    String? resolved;
    try {
      resolved = await _resolveBinary(path, searchPath);
    } catch (e) {
      return _TestStatus.error('resolve error: $e');
    }
    if (resolved == null) {
      return _TestStatus.error('not found in PATH: $path');
    }

    // ── 第二道：spawn 已确认存在的 binary, 顶层 try/catch 兜底 ────
    try {
      final result = await Process.run(
        resolved,
        args,
        runInShell: false,
        stdoutEncoding: utf8,
        stderrEncoding: utf8,
      ).timeout(const Duration(seconds: 5));
      final out = result.stdout is String
          ? (result.stdout as String).trim()
          : '';
      final err = result.stderr is String
          ? (result.stderr as String).trim()
          : '';
      if (result.exitCode == 0) {
        final summary = out.isNotEmpty
            ? out.split('\n').first
            : err.split('\n').first;
        return _TestStatus.ok(summary.isEmpty ? '✓ exit 0' : summary);
      }
      return _TestStatus.error(
        'exit ${result.exitCode}: ${err.isNotEmpty ? err : out}',
      );
    } on ProcessException catch (e) {
      return _TestStatus.error('process error: ${e.message}');
    } on TimeoutException {
      return _TestStatus.error('timeout (5s)');
    } catch (e) {
      return _TestStatus.error('unexpected: $e');
    }
  }

  /// 静态解析 binary 路径 (不 spawn)。
  ///   - 包含 "/" → 直接 file existence + executable 检查
  ///   - 否则 → 优先用 login shell PATH 找, fallback Platform.environment PATH
  Future<String?> _resolveBinary(String name, String? shellPath) async {
    if (name.contains('/')) {
      return await _isExecutable(name) ? name : null;
    }
    final candidates = <String>{};
    if (shellPath != null && shellPath.isNotEmpty) {
      candidates.addAll(shellPath.split(':'));
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
      // mode 的 executable 位检查 — 任意 user/group/other x 位置 1 即可
      return (stat.mode & 0x49) != 0; // 0o111 = 0x49
    } catch (_) {
      return false;
    }
  }
}

enum _StatusKind { idle, ok, error }

class _TestStatus {
  const _TestStatus.idle()
      : kind = _StatusKind.idle,
        message = '';
  const _TestStatus.ok(this.message) : kind = _StatusKind.ok;
  const _TestStatus.error(this.message) : kind = _StatusKind.error;
  final _StatusKind kind;
  final String message;
}

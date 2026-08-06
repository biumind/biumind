// Login shell 环境探测 — 解决 macOS Dock / Finder 启动 Flutter app 时 PATH
// 不全的问题。
//
// 现象：用户从 Dock 启动 BiuMind 后，Process.start('biu') 直接 ProcessException
// "biu: not found"，因为 GUI app 继承的 PATH 仅含系统级目录 (/usr/bin:/bin:
// /usr/sbin:/sbin)，没有 /opt/homebrew/bin、~/.local/bin、~/.npm-global/bin
// 等用户安装目录。
//
// 解决：app 启动时跑一次 `$SHELL -l -i -c env`，把 login shell 解析后的完整
// 环境（含 PATH）提取出来，缓存。BiuAdapter spawn 子进程时合并这份环境。
//
// 同时强制 UTF-8 locale (Tauri / Flutter 应用从 Dock 启动时 LANG 默认空)。

import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:flutter/foundation.dart' show kIsWeb;
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:logging/logging.dart';

final _log = Logger('biumind.login_shell_env');

/// 用 sentinel 隔离 shell rc stdout (echo / banner) 与 env 输出。
/// env -0 用 NUL 字节分隔每条 KEY=VALUE，能正确处理含等号 / 换行的值。
const _envSentinel = '__BIUMIND_ENV_START__';
const int _nul = 0;

/// macOS / Linux 常见命令安装目录。Login shell 探测失败时作为 fallback PATH。
const _fallbackPathExtras = <String>[
  '/usr/local/bin',
  '/usr/local/sbin',
  '/opt/homebrew/bin', // Apple Silicon brew
  '/opt/homebrew/sbin',
  '/opt/local/bin', // MacPorts
];

class LoginShellEnv {
  const LoginShellEnv({required this.env, required this.path});

  /// 完整环境变量映射（含 PATH）。
  final Map<String, String> env;

  /// PATH 字符串（冒号分隔），便于直接显示 / debug。
  final String path;
}

/// 异步加载 login shell 环境。
///
/// macOS / Linux：跑 `$SHELL -l -i -c "..."` 提取 env。
/// Windows：直接用 Platform.environment（PATH 通常正常）。
/// Web：返回空。
Future<LoginShellEnv> loadLoginShellEnv() async {
  if (kIsWeb) {
    return const LoginShellEnv(env: {}, path: '');
  }
  if (!Platform.isMacOS && !Platform.isLinux) {
    final base = Map<String, String>.from(Platform.environment);
    return LoginShellEnv(env: base, path: base['PATH'] ?? '');
  }

  final shell = Platform.environment['SHELL'] ??
      (Platform.isMacOS ? '/bin/zsh' : '/bin/bash');

  try {
    // -l = login shell (load .zprofile / .bash_profile)
    // -i = interactive (load .zshrc / .bashrc — 必要，因为很多用户把 PATH
    //                    放在 .zshrc 而不是 .zprofile 里)
    // -c "printf SENTINEL\0; env -0" → stdout 前置 sentinel\0 区分 rc 噪声
    //                                  env -0 用 NUL 分隔变量
    //
    // stdoutEncoding=null → 拿到 raw bytes List<int>，自己处理 NUL 边界
    final result = await Process.run(
      shell,
      ['-l', '-i', '-c', "printf '$_envSentinel'; printf '\\0'; env -0"],
      stdoutEncoding: null,
      stderrEncoding: systemEncoding,
    ).timeout(const Duration(seconds: 4));

    if (result.exitCode != 0) {
      _log.warning('login shell exit ${result.exitCode}: ${result.stderr}');
      return _fallback();
    }

    final bytes = result.stdout as List<int>;
    // 找 sentinel 串的 byte 起点
    final sentinelBytes = utf8.encode(_envSentinel);
    final start = _indexOfBytes(bytes, sentinelBytes);
    if (start < 0) {
      _log.warning('sentinel not found in shell output');
      return _fallback();
    }
    // sentinel 后跟一个 NUL，再后面是 env -0 的输出
    final envBodyStart = start + sentinelBytes.length + 1;
    if (envBodyStart >= bytes.length) {
      _log.warning('no env body after sentinel');
      return _fallback();
    }
    final envBody = bytes.sublist(envBodyStart);

    // 按 NUL 切分
    final env = <String, String>{};
    var cursor = 0;
    for (var i = 0; i <= envBody.length; i++) {
      if (i == envBody.length || envBody[i] == _nul) {
        if (i > cursor) {
          final entry = utf8.decode(
            envBody.sublist(cursor, i),
            allowMalformed: true,
          );
          final eq = entry.indexOf('=');
          if (eq > 0) {
            env[entry.substring(0, eq)] = entry.substring(eq + 1);
          }
        }
        cursor = i + 1;
      }
    }

    if (env.isEmpty) {
      _log.warning('login shell returned empty env');
      return _fallback();
    }

    // 强制 UTF-8 locale（Dock 启动 GUI app 默认无 LANG）
    env['LANG'] ??= 'en_US.UTF-8';
    env['LC_CTYPE'] ??= 'UTF-8';

    // PATH 清洗: dedup + 过滤含 U+FFFD (无效 UTF-8 字节解码后的 replacement
    // character) 的段。某些用户的 .zshrc 多次 export 累加 PATH 会导致重复, 而
    // env 中混入非 UTF-8 字节 (如 macOS 老风格路径名编码差异) 会让 PATH 部分
    // 段乱码，后续 UI 显示会很丑。
    env['PATH'] = _cleanPath(env['PATH'] ?? '');

    final path = env['PATH']!;
    _log.info(
      'login shell env loaded: ${env.length} vars, PATH segments=${path.split(':').length}',
    );
    return LoginShellEnv(env: env, path: path);
  } catch (e, st) {
    _log.warning('login shell probe failed', e, st);
    return _fallback();
  }
}

/// fallback：把当前 process 的 env + 几个常见目录拼起来。
LoginShellEnv _fallback() {
  final base = Map<String, String>.from(Platform.environment);
  final home = base['HOME'] ?? '';
  final extras = <String>[
    if (home.isNotEmpty) ...[
      '$home/.local/bin',
      '$home/.npm-global/bin',
      '$home/.cargo/bin',
      '$home/go/bin',
    ],
    ..._fallbackPathExtras,
  ];
  final existing = (base['PATH'] ?? '').split(':').where((s) => s.isNotEmpty);
  final merged = <String>{...existing, ...extras}.join(':');
  base['PATH'] = merged;
  base['LANG'] ??= 'en_US.UTF-8';
  base['LC_CTYPE'] ??= 'UTF-8';
  return LoginShellEnv(env: base, path: merged);
}

/// 清洗 PATH: dedup + 过滤含 invalid UTF-8 字节解码后的 replacement
/// character (U+FFFD) 的段。
String _cleanPath(String raw) {
  if (raw.isEmpty) return raw;
  final seen = <String>{};
  final out = <String>[];
  for (final seg in raw.split(':')) {
    if (seg.isEmpty) continue;
    // U+FFFD 是 utf8.decode allowMalformed=true 遇到无效字节时的替代字符,
    // 包含说明这段路径在 env 输出里就是乱的, UI 显示会出 "æ▒▒" 这种 lookalike,
    // 子进程 spawn 时拿到也用不了, 直接丢。
    if (seg.contains('�')) continue;
    if (seen.add(seg)) out.add(seg);
  }
  return out.join(':');
}

/// 在 byte 数组中找 needle 起点。返回 -1 找不到。
int _indexOfBytes(List<int> haystack, List<int> needle) {
  if (needle.isEmpty) return 0;
  outer:
  for (var i = 0; i + needle.length <= haystack.length; i++) {
    for (var j = 0; j < needle.length; j++) {
      if (haystack[i + j] != needle[j]) continue outer;
    }
    return i;
  }
  return -1;
}

/// app 全生命周期单例 — 启动时加载一次，缓存到结束。
final loginShellEnvProvider = FutureProvider<LoginShellEnv>((ref) async {
  return loadLoginShellEnv();
});

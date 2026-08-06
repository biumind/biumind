// 会话导出 Markdown (PERI-5)。
//
// 把一个完成/失败的编码任务序列化成可读 markdown。实现要点:
//   - Flutter 侧已持有全量 transcript(CodeTask.events,Drift 全量持久化,
//     见 code_tasks_dao.dart),无需重解析 JSONL、无需 daemon RPC,直接序列化内存数据。
//   - 元信息齐全:tokens / 费用 / 耗时 / 模型。
//   - 工具调用以紧凑 blockquote 行保留(name + 参数摘要 + 结果首段),完整又不淹没正文。
//
// 纯函数 [buildSessionMarkdown] 可单测;[exportSessionToFile] 落盘
// (getApplicationDocumentsDirectory,与 wiki/mirror_page 同套,无新依赖),
// Web 无文件系统时由调用方走剪贴板兜底。

import 'dart:io' show File;

import 'package:flutter/foundation.dart' show kIsWeb;
import 'package:path/path.dart' as p;
import 'package:path_provider/path_provider.dart';

import '../domain/code_task.dart';

/// 把任务序列化成 markdown 全文。纯函数,无 IO。
String buildSessionMarkdown(CodeTask task) {
  final b = StringBuffer();

  // ── 标题 ──
  final title = task.title.trim().isNotEmpty
      ? task.title.trim()
      : _firstLine(task.prompt);
  b.writeln('# ${_inline(title)}');
  b.writeln();

  // ── 元信息 ──
  b.writeln('## 元信息');
  b.writeln();
  b.writeln('- **Agent**: ${task.agent.label}');
  b.writeln('- **权限模式**: ${task.mode.label}');
  if (task.model != null && task.model!.trim().isNotEmpty) {
    b.writeln('- **模型**: `${_codeSpan(task.model!)}`');
  }
  b.writeln('- **状态**: ${_statusLabel(task.status)}');
  b.writeln('- **创建时间**: ${_fmtTime(task.createdAt)}');
  if (task.completedAt != null) {
    b.writeln('- **完成时间**: ${_fmtTime(task.completedAt!)}');
  }
  final dur = task.duration;
  if (dur != null) {
    b.writeln('- **耗时**: ${_fmtDuration(dur)}');
  }
  if (task.cost.inputTokens > 0 || task.cost.outputTokens > 0) {
    b.writeln(
        '- **Tokens**: 输入 ${task.cost.inputTokens} / 输出 ${task.cost.outputTokens}');
  }
  if (task.cost.usd > 0) {
    b.writeln('- **费用**: \$${task.cost.usd.toStringAsFixed(4)}');
  }
  final ws = task.workspace;
  if (ws != null && (ws.branchName ?? '').isNotEmpty) {
    final base = ws.baseBranch;
    if (base != null && base.isNotEmpty) {
      b.writeln(
          '- **工作区**: `${_codeSpan(ws.branchName!)}` → `${_codeSpan(base)}`');
    } else {
      b.writeln('- **工作区**: `${_codeSpan(ws.branchName!)}`');
    }
  }
  if ((task.originDeviceLabel ?? '').isNotEmpty) {
    b.writeln('- **设备**: ${_inline(task.originDeviceLabel!)}');
  }
  if ((task.errorMessage ?? '').isNotEmpty) {
    b.writeln('- **错误**: ${_inline(task.errorMessage!)}');
  }
  b.writeln();

  // ── 任务 prompt ──
  b.writeln('## 任务');
  b.writeln();
  if (task.prompt.trim().isEmpty) {
    b.writeln('> _(空)_');
  } else {
    for (final line in task.prompt.split('\n')) {
      b.writeln('> $line');
    }
  }
  b.writeln();

  // ── 对话 ──
  b.writeln('## 对话');
  b.writeln();
  _writeConversation(b, task.events);

  b.writeln();
  b.writeln('---');
  b.writeln('*由 BiuMind 导出*');
  return b.toString();
}

/// 把扁平事件流重组为可读对话:连续 text_delta 合并成一段助手文本,工具调用
/// 以紧凑 blockquote 行穿插,cost/status/permission 跳过(已在元信息)。
void _writeConversation(StringBuffer b, List<AgentEvent> events) {
  final textBuf = StringBuffer();

  void flushText() {
    final t = textBuf.toString().trim();
    if (t.isNotEmpty) {
      b.writeln(t);
      b.writeln();
    }
    textBuf.clear();
  }

  var emitted = false;
  for (final e in events) {
    switch (e) {
      case TextDelta(:final text):
        textBuf.write(text);
        emitted = true;
      case ToolUseStart(:final name, :final args):
        flushText();
        final summary = _argsSummary(name, args);
        final suffix =
            (summary != null && summary.isNotEmpty) ? ' `${_codeSpan(summary)}`' : '';
        b.writeln('> 🔧 **${_inline(name)}**$suffix');
        b.writeln();
        emitted = true;
      case ToolUseResult(:final result, :final isError):
        final r = result.trim();
        if (r.isNotEmpty) {
          final shown = _truncate(r, 600);
          final prefix = isError ? '> ⚠️ ' : '> ↳ ';
          // 结果可能多行 —— 每行前缀 blockquote,保持紧凑。
          for (final line in shown.split('\n')) {
            b.writeln('$prefix${_inline(line)}');
          }
          b.writeln();
        }
        emitted = true;
      case TaskFinished(:final reason, :final errorMessage):
        flushText();
        if (reason != 'end_turn') {
          final extra =
              (errorMessage ?? '').isNotEmpty ? ' — ${_inline(errorMessage!)}' : '';
          b.writeln('_（任务结束:$reason$extra）_');
          b.writeln();
        }
      case PermissionAsk():
      case CostUpdate():
      case AgentStatus():
      case SessionInfo():
        break; // 不进对话正文
    }
  }
  flushText();
  if (!emitted) {
    b.writeln('_(无对话内容)_');
    b.writeln();
  }
}

/// 落盘到 app Documents 目录,返回绝对路径。Web 无文件系统返回 null
/// (调用方走剪贴板兜底)。
Future<String?> exportSessionToFile(CodeTask task) async {
  final md = buildSessionMarkdown(task);
  if (kIsWeb) return null;
  final dir = await getApplicationDocumentsDirectory();
  final ts = DateTime.now()
      .toIso8601String()
      .replaceAll(':', '-')
      .substring(0, 19);
  final slug = _safeFilename(
      task.title.trim().isNotEmpty ? task.title : _firstLine(task.prompt));
  final outPath = p.join(dir.path, 'biumind-session-$slug-$ts.md');
  await File(outPath).writeAsString(md);
  return outPath;
}

// ─── helpers ─────────────────────────────────────────────────────

String _firstLine(String s) {
  final line = s.trim().split('\n').first.trim();
  return line.isEmpty ? '未命名任务' : line;
}

/// 行内安全:换行/制表折叠成空格(标题、列表项不能跨行)。
String _inline(String s) =>
    s.replaceAll(RegExp(r'[\r\n\t]+'), ' ').trim();

/// code span 内转义反引号。
String _codeSpan(String s) => _inline(s).replaceAll('`', 'ˋ');

String _truncate(String s, int max) =>
    s.length <= max ? s : '${s.substring(0, max)}… (已截断)';

String _statusLabel(CodeTaskStatus st) => switch (st) {
      CodeTaskStatus.queued => '排队中',
      CodeTaskStatus.running => '运行中',
      CodeTaskStatus.paused => '已暂停',
      CodeTaskStatus.inputRequired => '等待输入',
      CodeTaskStatus.done => '已完成',
      CodeTaskStatus.failed => '失败',
      CodeTaskStatus.interrupted => '已中断',
      CodeTaskStatus.detached => '终端连接断开',
    };

String _fmtTime(DateTime t) {
  final lt = t.toLocal();
  String two(int n) => n.toString().padLeft(2, '0');
  return '${lt.year}-${two(lt.month)}-${two(lt.day)} ${two(lt.hour)}:${two(lt.minute)}';
}

String _fmtDuration(Duration d) {
  if (d.inSeconds < 60) return '${d.inSeconds}s';
  if (d.inMinutes < 60) return '${d.inMinutes}m ${d.inSeconds % 60}s';
  return '${d.inHours}h ${d.inMinutes % 60}m';
}

/// 工具参数摘要 —— 常见 key 优先,否则简单拼接,统一截断成一行。
String? _argsSummary(String tool, Map<String, dynamic> args) {
  if (args.isEmpty) return null;
  for (final key in ['command', 'file_path', 'path', 'pattern', 'query', 'url']) {
    final v = args[key];
    if (v is String && v.trim().isNotEmpty) {
      return _truncate(_inline(v), 120);
    }
  }
  // 兜底:拼 key=value,截断。
  final parts = args.entries
      .map((e) => '${e.key}=${e.value}')
      .join(', ');
  return _truncate(_inline(parts), 120);
}

String _safeFilename(String s) {
  final cleaned = s
      .trim()
      .replaceAll(RegExp(r'[^\w一-龥-]+'), '_')
      .replaceAll(RegExp(r'^_+|_+$'), '');
  final slug = cleaned.isEmpty ? 'session' : cleaned;
  return slug.length <= 50 ? slug : slug.substring(0, 50);
}

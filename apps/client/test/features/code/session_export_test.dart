import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:biumind/features/code/application/session_export.dart';
import 'package:biumind/features/code/domain/code_task.dart';

CodeTask _task({
  String title = '修复登录 bug',
  String prompt = '帮我修复登录页的崩溃',
  List<AgentEvent> events = const [],
  CodeTaskStatus status = CodeTaskStatus.done,
  TaskCost cost = const TaskCost(usd: 0.0123, inputTokens: 1200, outputTokens: 800),
  String? model = 'claude-opus-4-8',
}) {
  final created = DateTime(2026, 6, 28, 10, 0, 0);
  return CodeTask(
    id: 't1',
    title: title,
    prompt: prompt,
    agent: AgentKind.claudeCode,
    mode: PermissionMode.autoEdit,
    status: status,
    events: events,
    cost: cost,
    createdAt: created,
    completedAt: created.add(const Duration(minutes: 2, seconds: 30)),
    model: model,
  );
}

void main() {
  group('buildSessionMarkdown', () {
    test('元信息 + prompt blockquote + 标题', () {
      final md = buildSessionMarkdown(_task());
      expect(md, contains('# 修复登录 bug'));
      expect(md, contains('## 元信息'));
      expect(md, contains('- **Agent**: Claude'));
      expect(md, contains('- **权限模式**: auto_edit'));
      expect(md, contains('- **模型**: `claude-opus-4-8`'));
      expect(md, contains('- **状态**: 已完成'));
      expect(md, contains('- **耗时**: 2m 30s'));
      expect(md, contains('- **Tokens**: 输入 1200 / 输出 800'));
      expect(md, contains('费用'));
      expect(md, contains('## 任务'));
      expect(md, contains('> 帮我修复登录页的崩溃'));
    });

    test('对话:合并连续 text_delta,工具调用紧凑行,结果截断', () {
      final ts = DateTime(2026, 6, 28, 10, 1);
      final md = buildSessionMarkdown(_task(events: [
        TextDelta(ts: ts, text: '我先'),
        TextDelta(ts: ts, text: '看一下代码。'),
        ToolUseStart(ts: ts, toolId: '1', name: 'Bash', args: {
          'command': 'grep -rn login lib/',
        }),
        ToolUseResult(ts: ts, toolId: '1', result: 'lib/login.dart:10', isError: false),
        TextDelta(ts: ts, text: '找到了,问题在第 10 行。'),
        TaskFinished(ts: ts, reason: 'end_turn'),
      ]));
      expect(md, contains('## 对话'));
      // 连续 text_delta 合并
      expect(md, contains('我先看一下代码。'));
      // 工具调用行 + 参数摘要
      expect(md, contains('🔧 **Bash**'));
      expect(md, contains('grep -rn login lib/'));
      // 工具结果
      expect(md, contains('lib/login.dart:10'));
      expect(md, contains('找到了,问题在第 10 行。'));
    });

    test('空标题回退到 prompt 首行', () {
      final md = buildSessionMarkdown(_task(title: '', prompt: '第一行任务\n第二行'));
      expect(md, contains('# 第一行任务'));
    });

    test('无事件 → 占位', () {
      final md = buildSessionMarkdown(_task(events: const []));
      expect(md, contains('_(无对话内容)_'));
    });

    test('失败任务带错误 + 非 end_turn 结束注记', () {
      final ts = DateTime(2026, 6, 28, 10, 1);
      final t = CodeTask(
        id: 't2',
        title: '炸了',
        prompt: 'do x',
        agent: AgentKind.codex,
        mode: PermissionMode.ask,
        status: CodeTaskStatus.failed,
        events: [
          TextDelta(ts: ts, text: '尝试中'),
          TaskFinished(ts: ts, reason: 'error', errorMessage: 'boom'),
        ],
        cost: const TaskCost(),
        createdAt: DateTime(2026, 6, 28, 10),
        completedAt: DateTime(2026, 6, 28, 10, 1),
        errorMessage: 'boom',
      );
      final md = buildSessionMarkdown(t);
      expect(md, contains('- **状态**: 失败'));
      expect(md, contains('- **错误**: boom'));
      expect(md, contains('任务结束:error'));
    });
  });
}

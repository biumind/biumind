// ActiveTaskBanner 纯展示测试 — R2 任务2.
//
// 不 override provider (ActiveTaskBanner 是 StatelessWidget, 接收 activeTasks
// + terminal 作参). 造 CreationTask fixture 直测三态: 进度浮条 / 终态浮条 / 隐藏.

import 'package:biumind/features/creation/domain/creation_task.dart';
import 'package:biumind/features/creation/presentation/widgets/active_task_banner.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

CreationTask _task({
  String id = 't1',
  int progress = 50,
  TaskStatus status = TaskStatus.running,
}) {
  final now = DateTime.utc(2026, 7, 1);
  return CreationTask(
    id: id,
    userId: 'u1',
    type: 'image',
    modelCode: 'm1',
    prompt: 'test',
    status: status,
    progress: progress,
    createdAt: now,
    updatedAt: now,
  );
}

void main() {
  testWidgets('active task → 进度浮条显示 NN% + 进度条 + 取消', (tester) async {
    await tester.pumpWidget(MaterialApp(
      home: Scaffold(
        body: ActiveTaskBanner(
          activeTasks: [_task(progress: 68)],
          onCancel: () {},
        ),
      ),
    ));
    expect(find.text('正在生成 · 68%'), findsOneWidget);
    expect(find.byType(LinearProgressIndicator), findsOneWidget);
    expect(find.byIcon(Icons.close), findsOneWidget);
  });

  testWidgets('空 activeTasks + 无 terminal → 隐藏 (无 banner Container)', (tester) async {
    await tester.pumpWidget(const MaterialApp(
      home: Scaffold(body: ActiveTaskBanner(activeTasks: [])),
    ));
    // banner 退化 SizedBox.shrink — 其下无 Container (_bar 用 Container).
    expect(
      find.descendant(
        of: find.byType(ActiveTaskBanner),
        matching: find.byType(Container),
      ),
      findsNothing,
    );
  });

  testWidgets('terminal → 终态浮条覆盖进度态', (tester) async {
    await tester.pumpWidget(MaterialApp(
      home: Scaffold(
        body: ActiveTaskBanner(
          activeTasks: [_task(progress: 50)],
          terminal: const TerminalMessage(
            color: Color(0xFF16A34A),
            icon: Icons.auto_awesome,
            text: '生成完成 ✨',
          ),
        ),
      ),
    ));
    expect(find.text('生成完成 ✨'), findsOneWidget);
    // 进度态被覆盖 (终态优先).
    expect(find.text('正在生成 · 50%'), findsNothing);
    // 终态无进度条 / 无取消按钮.
    expect(find.byType(LinearProgressIndicator), findsNothing);
    expect(find.byIcon(Icons.close), findsNothing);
  });

  testWidgets('多 active → N 个生成中 (最新进度)', (tester) async {
    await tester.pumpWidget(MaterialApp(
      home: Scaffold(
        body: ActiveTaskBanner(
          activeTasks: [
            _task(id: 'a', progress: 30),
            _task(id: 'b', progress: 80),
          ],
        ),
      ),
    ));
    expect(find.text('2 个生成中… (最新 30%)'), findsOneWidget);
  });
}

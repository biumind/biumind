// NoteSmoothEditor widget 测试 —— 两条最易回归的链路：
//   * insertMarkdown 走正常 onChanged→autosave（附件插入）
//   * setDoc 远端覆盖被回声守卫拦截，不触发 onChanged（否则远端推进会
//     反过来覆盖本地正在编辑的内容，等价原 Milkdown applyingExternalEdit）
//
// 控制器（MarkdownEditorController）本身是包内实现，IME 组合态抑制等
// 行为不在本仓库测试范畴。

import 'package:biumind/features/notes/presentation/note_smooth_editor.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  testWidgets('insertMarkdown 触发 onChanged；setDoc 被回声守卫拦截',
      (tester) async {
    NoteSmoothEditorHandle? handle;
    String? emitted;

    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: NoteSmoothEditor(
            initialMarkdown: '# hi',
            onChanged: (md) => emitted = md,
            onControllerReady: (h) => handle = h,
          ),
        ),
      ),
    );
    // 编辑器首帧构建（pumpAndSettle 可能被持续动画卡住，用有限次 pump）。
    for (var i = 0; i < 5; i++) {
      await tester.pump(const Duration(milliseconds: 50));
    }
    expect(handle, isNotNull, reason: 'onControllerReady 应回填 handle');

    // 附件插入链路：handle.insertMarkdown 应触发 onChanged，且初始内容保留。
    handle!.insertMarkdown('**bold**');
    await tester.pump();
    expect(emitted, isNotNull, reason: 'insertMarkdown 应触发 onChanged');
    expect(emitted, contains('**bold**'));
    expect(emitted, contains('# hi'));

    // 远端覆盖链路：handle.setDoc 不应触发 onChanged（回声守卫）。
    emitted = null;
    handle!.setDoc('# replaced by remote');
    await tester.pump();
    expect(emitted, isNull,
        reason: 'setDoc 是远端推进，触发 onChanged 会回声覆盖本地编辑');
  });

  testWidgets('editable=false 不崩（归档只读态）', (tester) async {
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: NoteSmoothEditor(
            initialMarkdown: '# archived',
            editable: false,
          ),
        ),
      ),
    );
    for (var i = 0; i < 5; i++) {
      await tester.pump(const Duration(milliseconds: 50));
    }
    // 构建无异常即通过（preview 模式渲染）。
    expect(find.byType(NoteSmoothEditor), findsOneWidget);
  });
}

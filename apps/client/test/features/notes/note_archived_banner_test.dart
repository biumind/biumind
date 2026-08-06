// N3「已转入知识库」只读提示条 widget 测试 —— promotedPageId 非空显示、
// 为空不占空间。
//
// 单独成文件：与 loopback HttpServer 的测试混放时 testWidgets 初始化的
// TestWidgetsFlutterBinding 会把真 HTTP 拦成 400（见
// notes_revisions_test.dart 头部注释同款约束）。

import 'package:biumind/data/notes_repository.dart';
import 'package:biumind/features/notes/presentation/note_editor_view.dart'
    show NoteArchivedBanner;
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

RepoNote _repoNote(String id, {String? promotedPageId}) => RepoNote(
      id: id,
      title: 't-$id',
      contentMd: '',
      isTodo: false,
      position: 0.0,
      version: 1,
      promotedPageId: promotedPageId,
      updatedAt: DateTime.utc(2026, 7, 29),
    );

void main() {
  testWidgets('promotedPageId 非空显示提示条，为空不占空间', (tester) async {
    await tester.pumpWidget(MaterialApp(
      home: Scaffold(
        body: NoteArchivedBanner(note: _repoNote('n1')),
      ),
    ));
    expect(find.text('已转入知识库，此笔记已归档'), findsNothing);

    await tester.pumpWidget(MaterialApp(
      home: Scaffold(
        body: NoteArchivedBanner(note: _repoNote('n1', promotedPageId: 'p1')),
      ),
    ));
    expect(find.text('已转入知识库，此笔记已归档'), findsOneWidget);
  });
}

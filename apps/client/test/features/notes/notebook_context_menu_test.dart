// _NotebookTile 右键/长按上下文菜单回归测试。
// 脚手架形态同 notebook_tree_ui_test.dart（AppDb.memory + 真 repo +
// StreamController 手推 provider）。

import 'dart:async';

import 'package:biumind/data/api/notes_client.dart' as api;
import 'package:biumind/data/local/db.dart';
import 'package:biumind/data/local/notes_dao.dart';
import 'package:biumind/data/notes_providers.dart';
import 'package:biumind/data/notes_repository.dart';
import 'package:biumind/features/notes/application/notes_ui_providers.dart';
import 'package:biumind/features/notes/presentation/notes_home_page.dart';
import 'package:flutter/gestures.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

void main() {
  testWidgets('右键笔记本行：弹出上下文菜单（含删除笔记本）', (tester) async {
    SharedPreferences.setMockInitialValues({});
    final db = AppDb.memory();
    final dao = NotesDao(db, scope: 'test');
    final client = api.NotesClient(Uri.parse('http://127.0.0.1:1'), 'tok');
    final repo = NotesRepository(dao: dao, client: client);
    final notebooksCtrl = StreamController<List<RepoNotebook>>.broadcast();
    addTearDown(() async {
      await notebooksCtrl.close();
      await db.close();
    });

    await dao.upsertNotebook(LocalNoteNotebook(
      id: 'P',
      name: '父目录',
      parentId: null,
      position: 0,
      ownerKey: 'test',
      updatedAt: DateTime.utc(2100),
    ));

    await tester.pumpWidget(ProviderScope(
      overrides: [
        notesRepositoryProvider.overrideWithValue(repo),
        notesNotebooksProvider.overrideWith((ref) => notebooksCtrl.stream),
        notesTagsProvider
            .overrideWith((ref) => const Stream<List<RepoTag>>.empty()),
      ],
      child: const MaterialApp(
        home: Scaffold(body: SizedBox(width: 280, child: NotebookColumn())),
      ),
    ));
    notebooksCtrl.add([
      for (final r in await dao.listNotebooks()) RepoNotebook.fromLocal(r),
    ]);
    await tester.pump();
    await tester.pump();

    expect(find.text('父目录'), findsOneWidget);

    // 右键（鼠标次键 down+up）。
    await tester.tap(find.text('父目录'), buttons: kSecondaryMouseButton);
    await tester.pumpAndSettle();

    expect(find.text('新建子目录'), findsOneWidget, reason: '右键应弹出菜单');
    expect(find.text('删除笔记本'), findsOneWidget, reason: '菜单应含删除项');
  });
}

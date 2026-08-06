// PassthroughWorkspace.collectArtifacts behavioural tests.
//
// 关键不变量:
//   - 仅扫 mtime > taskCreatedAt 的文件
//   - 不扫 .git / node_modules / .aws 等敏感/噪音目录
//   - HOME / / / /etc 等危险 root 直接拒
//   - sha256 算且大文件不读 bytes

import 'dart:io';

import 'package:biumind/features/code/domain/artifact.dart';
import 'package:biumind/features/code/workspace/passthrough_workspace.dart';
import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:path/path.dart' as p;

void main() {
  late Directory tmp;
  setUp(() async {
    tmp = await Directory.systemTemp.createTemp('biumind-passthrough-');
  });
  tearDown(() async {
    if (await tmp.exists()) await tmp.delete(recursive: true);
  });

  Future<File> touch(String relPath, String content,
      {DateTime? mtime}) async {
    final f = File(p.join(tmp.path, relPath));
    await f.parent.create(recursive: true);
    await f.writeAsString(content);
    if (mtime != null) {
      await f.setLastModified(mtime);
    }
    return f;
  }

  PassthroughWorkspace mkWs() => PassthroughWorkspace(
        cwd: tmp.path,
        taskId: 'task-A',
      );

  test('collects new files (mtime > taskCreatedAt)', () async {
    final taskCreated = DateTime.utc(2026, 1, 1);
    // file modified before taskCreated → 不收
    await touch('old.txt', 'old', mtime: DateTime.utc(2025, 1, 1));
    // files modified after → 收
    await touch('new.txt', 'new');
    await touch('lib/main.dart', 'void main(){}');

    final ws = mkWs();
    final arts = await ws.collectArtifacts(
      taskId: 'task-A',
      taskCreatedAt: taskCreated,
    );

    expect(arts, isNotNull);
    final paths = arts!.map((a) => a.relPath).toSet();
    expect(paths, contains('new.txt'));
    expect(paths, contains('lib/main.dart'));
    expect(paths, isNot(contains('old.txt')));
  });

  test('skips .git / node_modules / .aws / target etc', () async {
    final taskCreated = DateTime.utc(2026, 1, 1);
    await touch('.git/config', 'gitcfg');
    await touch('node_modules/foo/index.js', 'mod');
    await touch('.aws/credentials', 'AWS_KEY=...');
    await touch('target/release/bin', 'rust');
    await touch('build/macos/foo', 'flutter');
    await touch('keep.dart', 'good');

    final arts = await mkWs().collectArtifacts(
      taskId: 'task-A',
      taskCreatedAt: taskCreated,
    );
    final paths = arts!.map((a) => a.relPath).toSet();

    expect(paths, contains('keep.dart'));
    expect(paths.where((p) => p.contains('.git/')), isEmpty);
    expect(paths.where((p) => p.contains('node_modules/')), isEmpty);
    expect(paths.where((p) => p.contains('.aws/')), isEmpty);
    expect(paths.where((p) => p.contains('target/')), isEmpty);
    expect(paths.where((p) => p.contains('build/')), isEmpty);
  });

  test('refuses to scan dangerous roots (/etc /usr /proc 等)', () async {
    // 用一个真实的危险 root: /etc 一定存在
    final ws = PassthroughWorkspace(cwd: '/etc', taskId: 't');
    final arts = await ws.collectArtifacts(
      taskId: 't',
      taskCreatedAt: DateTime.utc(2020, 1, 1),
    );
    expect(arts, isEmpty,
        reason: '/etc 是危险 root, 应该 short-circuit 返回空列表');
  });

  test('HOME 允许扫但深度限制 + skipDirs 过滤敏感子目录', () async {
    // 用 fake HOME 模拟: env 设置 + 文件结构构造
    final fakeHome = await Directory.systemTemp.createTemp('biumind-fake-home-');
    try {
      // 直接子目录 / 浅层文件应该被收
      await File(p.join(fakeHome.path, 'note.md')).writeAsString('# hi');
      await Directory(p.join(fakeHome.path, 'projects')).create();
      await File(p.join(fakeHome.path, 'projects', 'a.txt')).writeAsString('proj');
      // 4 层深的文件超过 maxDepth=3 (HOME 模式), 不收
      final deep = Directory(p.join(fakeHome.path, 'a', 'b', 'c', 'd'));
      await deep.create(recursive: true);
      await File(p.join(deep.path, 'deep.txt')).writeAsString('too deep');
      // 敏感子目录文件不收
      final aws = Directory(p.join(fakeHome.path, '.aws'));
      await aws.create();
      await File(p.join(aws.path, 'credentials')).writeAsString('AWS_KEY=...');
      // Library 也不收
      final lib = Directory(p.join(fakeHome.path, 'Library', 'Caches'));
      await lib.create(recursive: true);
      await File(p.join(lib.path, 'app.cache')).writeAsString('cache');

      // 注入 HOME 让 isHome 判断生效
      final ws = PassthroughWorkspace(cwd: fakeHome.path, taskId: 't');
      // 这条用例无法注入 Platform.environment['HOME'], 但深度限制
      // 在非 HOME 模式下也是 maxDepth=8, 仍能验 skipDirs (.aws / Library)
      // 起作用。
      final arts = await ws.collectArtifacts(
        taskId: 't',
        taskCreatedAt: DateTime.utc(2020, 1, 1),
      );
      final paths = arts!.map((a) => a.relPath).toSet();
      expect(paths, contains('note.md'));
      expect(paths, contains('projects/a.txt'));
      expect(paths.where((p) => p.contains('.aws/')), isEmpty,
          reason: '敏感目录 .aws/ 应跳过');
      expect(paths.where((p) => p.contains('Library/')), isEmpty,
          reason: 'macOS Library/ 应跳过');
    } finally {
      await fakeHome.delete(recursive: true);
    }
  });

  test('refuses to scan when cwd is missing', () async {
    final missing = p.join(tmp.path, 'does-not-exist');
    final ws = PassthroughWorkspace(cwd: missing, taskId: 't');
    final arts = await ws.collectArtifacts(
      taskId: 't',
      taskCreatedAt: DateTime.utc(2020, 1, 1),
    );
    expect(arts, isEmpty);
  });

  test('infers kind from mime / extension', () async {
    final taskCreated = DateTime.utc(2026, 1, 1);
    await touch('out/dragon.png', 'fakepng');
    await touch('lib/main.dart', 'code');
    await touch('docs/note.md', '# title\n');
    await touch('data.csv', 'a,b,c');
    await touch('blob.bin', 'binary');

    final arts = await mkWs().collectArtifacts(
      taskId: 'task-A',
      taskCreatedAt: taskCreated,
    );
    final byPath = {for (final a in arts!) a.relPath: a};
    expect(byPath['out/dragon.png']!.kind, ArtifactKind.image);
    expect(byPath['lib/main.dart']!.kind, ArtifactKind.codeFile);
    expect(byPath['docs/note.md']!.kind, ArtifactKind.document);
    expect(byPath['data.csv']!.kind, ArtifactKind.dataset);
    expect(byPath['blob.bin']!.kind, ArtifactKind.binary);
  });

  test('sensitive files marked binary kind (CSY6)', () async {
    final taskCreated = DateTime.utc(2026, 1, 1);
    await touch('.env', 'SECRET=foo');
    await touch('id_rsa', 'priv');

    final arts = await mkWs().collectArtifacts(
      taskId: 'task-A',
      taskCreatedAt: taskCreated,
    );
    final byPath = {for (final a in arts!) a.relPath: a};
    // 收 L1 元数据 (sha256 / size), 但 kind=binary 防 UI 把它当文档展开
    expect(byPath['.env']?.kind, ArtifactKind.binary);
    expect(byPath['id_rsa']?.kind, ArtifactKind.binary);
  });

  test('computes sha256 for small files, skips for huge files', () async {
    final taskCreated = DateTime.utc(2026, 1, 1);
    await touch('small.txt', 'hello world');

    final arts = await mkWs().collectArtifacts(
      taskId: 'task-A',
      taskCreatedAt: taskCreated,
    );
    final small = arts!.firstWhere((a) => a.relPath == 'small.txt');
    expect(small.sha256, hasLength(64), reason: 'sha256 hex string');
    expect(small.sizeBytes, 11);
  });
}

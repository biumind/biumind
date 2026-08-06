// FileExplorerController —— 编码模块文件浏览器的状态 + 动作(M4-D)。
//
// 懒加载目录树(每展开一个目录才 fs.list 拉它的子项,结果缓存),选中文件经 fs.read
// 拉文本 / fs.imagePreview 拉图片。作用于当前活动项目根目录。无 daemon/项目 → 空。
//
// 真相源是本地磁盘(经 daemon 的 biumindkit/code/fs)。

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../data/code_bridge_client.dart';
import '../data/code_bridge_provider.dart';
import '../domain/git_models.dart';
import 'projects_controller.dart';

/// 选中文件的内容载荷:文本 / 图片 / 提示(过大或二进制)。
sealed class FileContent {
  const FileContent();
}

class TextFileContent extends FileContent {
  const TextFileContent(this.text, {this.truncated = false});
  final String text;
  final bool truncated;
}

class ImageFileContent extends FileContent {
  const ImageFileContent(this.preview);
  final FileImagePreview preview;
}

class NoticeFileContent extends FileContent {
  const NoticeFileContent(this.message);
  final String message;
}

class FileExplorerState {
  const FileExplorerState({
    this.root,
    this.children = const {},
    this.expanded = const {},
    this.loadingDirs = const {},
    this.selectedPath,
    this.content,
    this.contentLoading = false,
    this.error,
  });

  /// 项目根绝对路径(为空 = 无项目/无 daemon)。
  final String? root;

  /// 目录绝对路径 → 直接子项(已加载过的才有)。
  final Map<String, List<FsEntry>> children;

  /// 已展开的目录绝对路径集合。
  final Set<String> expanded;

  /// 正在加载子项的目录(转圈)。
  final Set<String> loadingDirs;

  final String? selectedPath;
  final FileContent? content;
  final bool contentLoading;
  final String? error;

  FileExplorerState copyWith({
    Object? root = _s,
    Map<String, List<FsEntry>>? children,
    Set<String>? expanded,
    Set<String>? loadingDirs,
    Object? selectedPath = _s,
    Object? content = _s,
    bool? contentLoading,
    Object? error = _s,
  }) =>
      FileExplorerState(
        root: root == _s ? this.root : root as String?,
        children: children ?? this.children,
        expanded: expanded ?? this.expanded,
        loadingDirs: loadingDirs ?? this.loadingDirs,
        selectedPath:
            selectedPath == _s ? this.selectedPath : selectedPath as String?,
        content: content == _s ? this.content : content as FileContent?,
        contentLoading: contentLoading ?? this.contentLoading,
        error: error == _s ? this.error : error as String?,
      );

  static const _s = Object();
}

/// 树里渲染用的一个节点(从 expanded + children 扁平化推导)。
class FileTreeNode {
  const FileTreeNode({
    required this.path,
    required this.name,
    required this.isDir,
    required this.depth,
  });
  final String path;
  final String name;
  final bool isDir;
  final int depth;
}

const _imageExts = {'png', 'jpg', 'jpeg', 'gif', 'webp', 'bmp', 'svg'};
const _maxTextBytes = 1 << 20; // 1MB 以上不在浏览器里读,提示去主编辑器

class FileExplorerController extends StateNotifier<FileExplorerState> {
  FileExplorerController({required this.bridge, required this.root})
      : super(FileExplorerState(root: root)) {
    if (_ready) {
      Future.microtask(() => _loadDir(root!));
    }
  }

  final CodeBridgeClient? bridge;
  final String? root;

  bool get _ready => bridge != null && root != null && root!.isNotEmpty;

  /// 扁平化可见节点列表(深度优先,跟随 expanded)。
  List<FileTreeNode> visibleNodes() {
    if (root == null) return const [];
    final out = <FileTreeNode>[];
    void walk(String dir, int depth) {
      final kids = state.children[dir];
      if (kids == null) return;
      for (final e in kids) {
        final p = '$dir/${e.name}';
        out.add(FileTreeNode(
            path: p, name: e.name, isDir: e.isDir, depth: depth));
        if (e.isDir && state.expanded.contains(p)) {
          walk(p, depth + 1);
        }
      }
    }

    walk(root!, 0);
    return out;
  }

  /// 展开/收起目录(展开时若未缓存则加载)。
  Future<void> toggleDir(String path) async {
    final expanded = Set<String>.from(state.expanded);
    if (expanded.contains(path)) {
      expanded.remove(path);
      state = state.copyWith(expanded: expanded);
      return;
    }
    expanded.add(path);
    state = state.copyWith(expanded: expanded);
    if (!state.children.containsKey(path)) {
      await _loadDir(path);
    }
  }

  Future<void> _loadDir(String dir) async {
    if (bridge == null) return;
    state = state.copyWith(loadingDirs: {...state.loadingDirs, dir});
    try {
      final entries = await bridge!.fsListEntries(dir);
      state = state.copyWith(
        children: {...state.children, dir: entries},
        loadingDirs: state.loadingDirs.difference({dir}),
      );
    } catch (e) {
      state = state.copyWith(
        loadingDirs: state.loadingDirs.difference({dir}),
        error: _msg(e),
      );
    }
  }

  /// 选中文件并加载内容(图片走预览,文本走 read,超限/二进制给提示)。
  Future<void> selectFile(String path, {int size = 0}) async {
    if (bridge == null || root == null) return;
    state = state.copyWith(
        selectedPath: path, contentLoading: true, content: null, error: null);
    try {
      final ext = _ext(path);
      if (_imageExts.contains(ext)) {
        final preview = await bridge!.fsImagePreview(root!, path);
        state = state.copyWith(
            content: ImageFileContent(preview), contentLoading: false);
        return;
      }
      if (size > _maxTextBytes) {
        state = state.copyWith(
          content: NoticeFileContent(
              '文件过大(${(size / 1024 / 1024).toStringAsFixed(1)} MB),'
              '不在浏览器内打开'),
          contentLoading: false,
        );
        return;
      }
      final r = await bridge!.fsReadFile(path);
      if (_looksBinary(r.content)) {
        state = state.copyWith(
            content: const NoticeFileContent('二进制文件,无法以文本显示'),
            contentLoading: false);
        return;
      }
      state = state.copyWith(
          content: TextFileContent(r.content, truncated: r.truncated),
          contentLoading: false);
    } catch (e) {
      state = state.copyWith(
          content: NoticeFileContent('打开失败: ${_msg(e)}'), contentLoading: false);
    }
  }

  /// 重新加载某目录(新建/删除后刷新它);未加载过则忽略。
  Future<void> reloadDir(String dir) async {
    if (state.children.containsKey(dir)) await _loadDir(dir);
  }

  Future<bool> createFile(String dir, String name) =>
      _mutate(() => bridge!.fsCreateFile(root!, '$dir/$name'), dir);
  Future<bool> createDirectory(String dir, String name) =>
      _mutate(() => bridge!.fsCreateDirectory(root!, '$dir/$name'), dir);

  /// 删除节点;刷新其父目录。若删的是当前选中文件,清空内容。
  Future<bool> delete(String path) async {
    if (!_ready) return false;
    final parent = _parent(path);
    final ok = await _mutate(() => bridge!.fsDelete(root!, path), parent);
    if (ok && state.selectedPath == path) {
      state = state.copyWith(selectedPath: null, content: null);
    }
    return ok;
  }

  Future<bool> _mutate(Future<void> Function() act, String dirToReload) async {
    if (!_ready) return false;
    try {
      await act();
      await reloadDir(dirToReload);
      return true;
    } catch (e) {
      state = state.copyWith(error: _msg(e));
      return false;
    }
  }

  String _parent(String path) {
    final i = path.lastIndexOf('/');
    return i > 0 ? path.substring(0, i) : (root ?? path);
  }

  static String _ext(String path) {
    final dot = path.lastIndexOf('.');
    final slash = path.lastIndexOf('/');
    if (dot <= slash) return '';
    return path.substring(dot + 1).toLowerCase();
  }

  /// 粗判二进制:前若干字符含 NUL。
  static bool _looksBinary(String s) {
    final n = s.length < 4096 ? s.length : 4096;
    for (var i = 0; i < n; i++) {
      if (s.codeUnitAt(i) == 0) return true;
    }
    return false;
  }

  static String _msg(Object e) =>
      e is CodeBridgeException ? (e.error ?? e.method) : e.toString();
}

/// 随活动项目 + bridge 重建。
final fileExplorerControllerProvider = StateNotifierProvider.autoDispose<
    FileExplorerController, FileExplorerState>((ref) {
  final bridge = ref.watch(codeBridgeClientProvider);
  final project = ref.watch(activeCodeProjectProvider);
  return FileExplorerController(bridge: bridge, root: project?.path);
});

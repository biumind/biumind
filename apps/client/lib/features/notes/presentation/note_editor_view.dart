/// 笔记编辑器 —— 标题输入框 + Milkdown WYSIWYG（PageEditorView）。
///
/// 三条接线链路：
///   * 自动保存：editor onMarkdownChanged → AutoSaveController.schedule
///     （1.5s 防抖）→ saver 调 NotesRepository.updateNote（乐观落 Drift +
///     outbox，离线天然可用）。标题走第二个 AutoSaveController。
///   * 远端覆盖：noteByIdProvider 流里 version 变化且非本机 pending 时
///     （changes 轮询落库的他端变更），经 controllerRef 拿到的
///     EditorBridgeController.setDoc() 推进编辑器。
///   * 409 冲突：订阅 NoteOutboxFlusher.conflicts，命中本笔记时弹
///     SnackBar「当前编辑基于旧版本，已被服务端更新覆盖」+「另存为副本」
///     （repository.saveAsCopy，设计 §4 D4 用户裁决，禁止 latest-wins）。
///   * 附件：标题行「插入图片 / 插入附件」→ 选文件 → presign 直传 →
///     光标处插 `![name](url)` 图片或 `[name](url)` 链接；正文里的
///     biu-file:// 经 NoteAttachmentResolver 进编辑器换临时 URL、
///     落库前换回规范 URI。
///
/// 桌面三栏右栏 / 手机详情页共用。dispose 前 flush() 防丢半句输入。
/// 桌面三栏切笔记走 didUpdateWidget 复用同一编辑器 webview（不重建），
/// 新正文加载完成后经 EditorBridgeController.setDoc 推进。
library;

import 'dart:async';

import 'package:file_selector/file_selector.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:image_picker/image_picker.dart';

import '../../../app/theme.dart';
import '../../../core/editor/editor_bridge_controller.dart';
import '../../../core/editor/editor_bridge_protocol.dart';
import '../../../core/editor/page_autosave.dart';
import '../../../core/editor/page_editor_view.dart';
import '../../../core/layout/form_factor.dart';
import '../../../data/notes_providers.dart';
import '../../../data/notes_repository.dart';
import '../../../data/outbox/note_outbox_flusher.dart';
import '../../../services/auth_service.dart';
import '../../code/data/files_client.dart';
import '../application/notes_ui_providers.dart';
import '../data/note_attachment_resolver.dart';
import 'note_merge_dialog.dart';
import 'note_revisions_dialog.dart';
import 'notes_home_page.dart' show relativeTime;

class NoteEditorView extends ConsumerStatefulWidget {
  const NoteEditorView({super.key, required this.noteId});

  final String noteId;

  @override
  ConsumerState<NoteEditorView> createState() => _NoteEditorViewState();
}

class _NoteEditorViewState extends ConsumerState<NoteEditorView> {
  final _titleController = TextEditingController();

  /// 首屏加载（initialMarkdown 只能给一次，之后都走 setDoc）。
  Future<RepoNote?>? _initial;
  RepoNote? _note;

  /// 自动保存归属的笔记 id。didUpdateWidget 里 widget 已是新值，flush
  /// 旧笔记 pending 内容时 saver 必须仍读旧 id —— 故单独存一份，
  /// 切笔记时「先 flush、再切 id」。
  late String _currentNoteId;

  EditorBridgeController? _editorController;
  late final AutoSaveController _contentAutosave;
  late final AutoSaveController _titleAutosave;
  StreamSubscription<NoteOutboxConflict>? _conflictSub;

  /// biu-file:// ↔ 临时 URL 重写（进编辑器换 URL、落库换回规范 URI）。
  /// 凭证在调用当下读取，token 轮换不影响已建立的映射。
  late final NoteAttachmentResolver _attachmentResolver;

  /// 首屏喂给编辑器的正文（biu-file 已换成临时 URL）。
  String? _resolvedContent;
  bool _insertingAttachment = false;

  int _lastVersion = -1;
  bool _conflictSnackbarVisible = false;
  bool _mergeDialogVisible = false;

  /// 最近一次落库的 canonical markdown（biu-file 形式）。_onRemoteNote 据此
  /// 识别本机 save 的服务端回声 —— flush 成功后 flusher 回写本地行触发 watch
  /// 二次回放，内容 == 刚存 → 是自己，setDoc 会冲掉此后敲的字（丢字根因）。
  String? _lastSavedCanonical;

  @override
  void initState() {
    super.initState();
    _currentNoteId = widget.noteId;
    _attachmentResolver = NoteAttachmentResolver(
      presignGet: (fileId) {
        final creds = ref.read(hubCredentialsProvider);
        if (creds == null) throw StateError('未连接 hub');
        return presignGetFileUrl(creds.endpoint, creds.bearerToken, fileId);
      },
    );
    _contentAutosave = AutoSaveController(saver: _saveContent)
      ..addListener(_onAutosaveStatus);
    _titleAutosave = AutoSaveController(saver: _saveTitle)
      ..addListener(_onAutosaveStatus);
    _initial = _loadNote(_currentNoteId);
    // 409 冲突 → 用户裁决（另存为副本）。flusher 跟随 credentials 重建，
    // N1 生命周期内基本不变，initState 订阅一次即可。
    _conflictSub =
        ref.read(noteOutboxFlusherProvider)?.conflicts.listen(_onConflict);
  }

  @override
  void didUpdateWidget(covariant NoteEditorView oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.noteId != widget.noteId) _switchNote();
  }

  /// 桌面三栏切笔记：复用同一编辑器 webview，不重建。
  ///
  /// 时序：先 flush 旧笔记的未保存内容（saver 读 _currentNoteId，此刻
  /// 仍是旧 id），再切 id、重置状态、重新加载 —— 顺序反了会把旧内容
  /// 存到新笔记上。加载期间保留旧 _note，让 PageEditorView 挂在树上
  /// （置空会触发 FutureBuilder 的 spinner 分支把 webview 卸载）；
  /// 新正文加载完成后在 _loadNote 里经 setDoc 推进。
  void _switchNote() {
    unawaited(_contentAutosave.flush());
    unawaited(_titleAutosave.flush());
    _currentNoteId = widget.noteId;
    // 旧笔记的冲突 snackbar 不再适用（「另存为副本」按当前 id 落）。
    ScaffoldMessenger.of(context).hideCurrentSnackBar();
    _conflictSnackbarVisible = false;
    _resolvedContent = null;
    _lastVersion = -1;
    _lastSavedCanonical = null;
    _initial = _loadNote(_currentNoteId);
  }

  /// 首屏 / 切笔记共用的加载链路：getNote → biu-file 换临时 URL →
  /// setState。编辑器已挂载时（切笔记）正文改走 setDoc 推进，
  /// initialMarkdown 只在编辑器首次创建时生效。
  Future<RepoNote?>? _loadNote(String noteId) {
    final repo = ref.read(notesRepositoryProvider);
    if (repo == null) return null;
    return repo.getNote(noteId).then((note) async {
      // 快速连切时丢弃过期的加载结果（last-click-wins）。
      if (!mounted || noteId != _currentNoteId) return note;
      if (note != null) {
        // 进编辑器前把 biu-file:// 换成可渲染的临时 URL（换取失败的
        // 保留原 URI，编辑器里裂开但正文不丢）。
        final resolved =
            await _attachmentResolver.resolveForEditor(note.contentMd);
        if (mounted && noteId == _currentNoteId) {
          setState(() {
            _note = note;
            _resolvedContent = resolved;
            _lastVersion = note.version;
            _titleController.text = note.title;
          });
          // 切笔记场景编辑器（webview）已存在，直接 setDoc 复用；首屏
          // controller 还没挂上，由 initialMarkdown 喂入。
          final controller = _editorController;
          if (controller != null) unawaited(controller.setDoc(resolved));
        }
      } else {
        setState(() => _note = null);
      }
      return note;
    });
  }

  @override
  void dispose() {
    // dispose 前 flush 防丢半句输入（fire-and-forget：_fire 在第一个
    // await 之前已同步取出 pending content 并启动 saver）。
    unawaited(_contentAutosave.flush());
    unawaited(_titleAutosave.flush());
    _contentAutosave.dispose();
    _titleAutosave.dispose();
    _conflictSub?.cancel();
    _titleController.dispose();
    super.dispose();
  }

  // ─── 自动保存 ─────────────────────────────────────────────

  Future<AutoSaveOutcome> _saveContent(String md) async {
    final repo = ref.read(notesRepositoryProvider);
    if (repo == null) {
      return const AutoSaveOutcome(
          status: AutoSaveStatus.error, errorMessage: '未连接 hub');
    }
    // 记下刚存的 canonical 串，供 _onRemoteNote 识别本机 save 的服务端回声
    // （flush 成功后 flusher 回写本地行 + 删 outbox → pendingUpdate 清零 →
    // watch 二次回放，内容 == 刚存 → 是自己，setDoc 会冲掉此后敲的字 → 丢字）。
    _lastSavedCanonical = md;
    await repo.updateNote(_currentNoteId, contentMd: md);
    return const AutoSaveOutcome(status: AutoSaveStatus.saved);
  }

  Future<AutoSaveOutcome> _saveTitle(String title) async {
    final repo = ref.read(notesRepositoryProvider);
    if (repo == null) {
      return const AutoSaveOutcome(
          status: AutoSaveStatus.error, errorMessage: '未连接 hub');
    }
    await repo.updateNote(_currentNoteId, title: title);
    return const AutoSaveOutcome(status: AutoSaveStatus.saved);
  }

  void _onAutosaveStatus() {
    if (mounted) setState(() {});
  }

  // ─── 远端覆盖（changes 轮询落库的他端变更）──────────────────

  void _onRemoteNote(RepoNote note) {
    // version 没变 = 本地乐观回显（updateNote 保持 version 基线），跳过；
    // 本机 pending 时跳过，避免回声冲掉正在编辑的内容。
    if (note.version == _lastVersion || note.pendingUpdate) return;
    _lastVersion = note.version;
    // 本机 save 的服务端回声：flush 成功后 flusher._upsertFromDto 回写本地行
    // + 删 outbox → pendingUpdate 清零 → watch 二次回放来到这里。内容 == 刚存
    // → 编辑器里已有（或更新），setDoc 会冲掉此后敲的字（丢字根因）→ 跳过。
    // 真他端编辑内容不同 → 不等 → 照常应用，多设备无损。
    if (note.contentMd == _lastSavedCanonical) return;
    _note = note;
    unawaited(_pushRemoteContent(note.contentMd));
    if (note.title != _titleController.text) {
      _titleController.text = note.title;
    }
  }

  /// 远端正文推进编辑器前，同样要把 biu-file:// 换成临时 URL。
  Future<void> _pushRemoteContent(String canonical) async {
    final resolved = await _attachmentResolver.resolveForEditor(canonical);
    await _editorController?.setDoc(resolved);
  }

  // ─── 附件（N2）：图片 + 任意文件 ────────────────────────────

  /// 上传单个附件并登记映射，返回可渲染临时 URL。大小超 10MB 抛异常。
  Future<String> _uploadAndResolve(
    FilesClient filesClient,
    XFile f, {
    required String mime,
  }) async {
    final bytes = await f.readAsBytes();
    if (bytes.length > 10 * 1024 * 1024) {
      throw Exception('${f.name} 超过 10MB 上限');
    }
    final result = await filesClient.uploadViaPresign(
      bytes: bytes,
      filename: f.name,
      mime: mime,
      source: 'note-attachment',
    );
    return _attachmentResolver.resolveOne(result.fileId);
  }

  /// 选图 → 走 chat 同款 presign 直传上传 → 换取临时 URL → 在编辑器
  /// 当前光标处插入 `![name](url)`（docChanged 回流时按映射换回
  /// `biu-file://<uuid>` 落库）。上传失败 SnackBar 报错，正文不留占位。
  Future<void> _insertImage() async {
    if (_insertingAttachment) return;
    final filesClient = ref.read(filesClientProvider);
    if (filesClient == null) {
      _showSnack('未连接 hub，无法上传图片');
      return;
    }
    List<XFile> picked;
    try {
      picked = await _pickImages();
    } on Exception catch (e) {
      _showSnack('选择图片失败：$e');
      return;
    }
    if (picked.isEmpty || !mounted) return;
    final messenger = ScaffoldMessenger.of(context);
    setState(() => _insertingAttachment = true);
    messenger
      ..hideCurrentSnackBar()
      ..showSnackBar(const SnackBar(
        content: Text('图片上传中…'),
        duration: Duration(days: 1),
      ));
    try {
      for (final f in picked) {
        // 上传成功再换临时 URL 并登记映射，然后才让编辑器插入 ——
        // 任何一步失败正文都不会出现半截占位。
        final url = await _uploadAndResolve(
          filesClient,
          f,
          mime: f.mimeType ?? _guessImageMime(f.name),
        );
        final alt = f.name.replaceAll(RegExp(r'[\[\]]'), '');
        await _editorController?.command(
          'insertText',
          args: {'text': '![$alt]($url)'},
        );
      }
      messenger.hideCurrentSnackBar();
    } on Exception catch (e) {
      messenger.hideCurrentSnackBar();
      messenger.showSnackBar(SnackBar(content: Text('图片上传失败：$e')));
    } finally {
      if (mounted) setState(() => _insertingAttachment = false);
    }
  }

  /// 选任意文件 → 同一条 presign 直传通路 → 光标处插入
  /// `[文件名](biu-file://<uuid>)` 链接（先换成临时 URL，回流时换回）。
  Future<void> _insertAttachment() async {
    if (_insertingAttachment) return;
    final filesClient = ref.read(filesClientProvider);
    if (filesClient == null) {
      _showSnack('未连接 hub，无法上传附件');
      return;
    }
    XFile? picked;
    try {
      picked = await openFile();
    } on Exception catch (e) {
      _showSnack('选择文件失败：$e');
      return;
    }
    if (picked == null || !mounted) return;
    final messenger = ScaffoldMessenger.of(context);
    setState(() => _insertingAttachment = true);
    messenger
      ..hideCurrentSnackBar()
      ..showSnackBar(const SnackBar(
        content: Text('附件上传中…'),
        duration: Duration(days: 1),
      ));
    try {
      final url = await _uploadAndResolve(
        filesClient,
        picked,
        mime: picked.mimeType ?? 'application/octet-stream',
      );
      // 链接文本里的 \ [ ] 会破坏 markdown 语法，先转义。
      final name = picked.name
          .replaceAll(r'\', r'\\')
          .replaceAll('[', r'\[')
          .replaceAll(']', r'\]');
      await _editorController?.command(
        'insertText',
        args: {'text': '[$name]($url)'},
      );
      messenger.hideCurrentSnackBar();
    } on Exception catch (e) {
      messenger.hideCurrentSnackBar();
      messenger.showSnackBar(SnackBar(content: Text('附件上传失败：$e')));
    } finally {
      if (mounted) setState(() => _insertingAttachment = false);
    }
  }

  /// 选图入口跟 chat 一致：手机先弹「拍照 / 从相册选择」，桌面走
  /// file_selector（两端都是 cross_file 的 XFile，统一处理）。
  Future<List<XFile>> _pickImages() async {
    if (isPhoneLayout(context)) {
      final source = await showModalBottomSheet<ImageSource>(
        context: context,
        builder: (ctx) => SafeArea(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: <Widget>[
              ListTile(
                leading: const Icon(Icons.photo_camera_outlined),
                title: const Text('拍照'),
                onTap: () => Navigator.of(ctx).pop(ImageSource.camera),
              ),
              ListTile(
                leading: const Icon(Icons.photo_library_outlined),
                title: const Text('从相册选择'),
                onTap: () => Navigator.of(ctx).pop(ImageSource.gallery),
              ),
            ],
          ),
        ),
      );
      if (source == null || !mounted) return const [];
      if (source == ImageSource.camera) {
        final photo =
            await ImagePicker().pickImage(source: ImageSource.camera);
        return [?photo];
      }
      return ImagePicker().pickMultiImage();
    }
    return openFiles(
      acceptedTypeGroups: const [
        XTypeGroup(
          label: 'Images',
          extensions: ['png', 'jpg', 'jpeg', 'webp', 'gif', 'heic'],
          mimeTypes: ['image/png', 'image/jpeg', 'image/webp', 'image/gif'],
        ),
      ],
    );
  }

  static String _guessImageMime(String name) {
    final ext = name.split('.').last.toLowerCase();
    return switch (ext) {
      'png' => 'image/png',
      'jpg' || 'jpeg' => 'image/jpeg',
      'webp' => 'image/webp',
      'gif' => 'image/gif',
      'heic' => 'image/heic',
      _ => 'image/jpeg',
    };
  }

  void _showSnack(String message) {
    if (!mounted) return;
    ScaffoldMessenger.of(context)
        .showSnackBar(SnackBar(content: Text(message)));
  }

  // ─── 409 冲突（设计 §4 D4 用户裁决）────────────────────────

  void _onConflict(NoteOutboxConflict conflict) {
    if (conflict.entityId != widget.noteId || !mounted) return;

    // 三方合并有冲突段 → 弹合并对话框（替换 SnackBar）。一次只弹一个，
    // 防止连续 flush 轮次堆叠。
    if (conflict.hasMergeBundle) {
      if (_mergeDialogVisible) return;
      _mergeDialogVisible = true;
      NoteMergeDialog.show(
        context,
        noteId: widget.noteId,
        conflict: conflict,
      ).then((_) => _mergeDialogVisible = false);
      return;
    }

    // legacy（base 缺失 / current 不可解析，无法三方合并）→ 老 SnackBar +
    // 另存为副本兜底。
    if (_conflictSnackbarVisible) return;
    _conflictSnackbarVisible = true;
    ScaffoldMessenger.of(context)
        .showSnackBar(
          SnackBar(
            content: const Text('当前编辑基于旧版本，已被服务端更新覆盖'),
            duration: const Duration(seconds: 8),
            action: SnackBarAction(
              label: '另存为副本',
              onPressed: () => unawaited(_saveAsCopy()),
            ),
          ),
        )
        .closed
        .then((_) => _conflictSnackbarVisible = false);
  }

  Future<void> _saveAsCopy() async {
    final repo = ref.read(notesRepositoryProvider);
    if (repo == null) return;
    try {
      final copy = await repo.saveAsCopy(widget.noteId);
      ref.read(selectedNoteIdProvider.notifier).state = copy.id;
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(content: Text('已另存为副本，可手动合并后删除本条')),
        );
      }
    } on Exception catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context)
            .showSnackBar(SnackBar(content: Text('另存失败：$e')));
      }
    }
  }

  Future<void> _trashThisNote() async {
    final repo = ref.read(notesRepositoryProvider);
    if (repo == null) return;
    await repo.trashNote(widget.noteId);
    if (ref.read(selectedNoteIdProvider) == widget.noteId) {
      ref.read(selectedNoteIdProvider.notifier).state = null;
    }
  }

  // ─── 历史版本 / 转知识库（N3）──────────────────────────────

  void _showHistory() {
    unawaited(NoteRevisionsDialog.show(context, noteId: widget.noteId));
  }

  /// 转入知识库：选 wiki project → promote（服务端归档笔记 + 建 page，
  /// 幂等）→ toast → 返回列表。归档 note 落库后 archivedAt 置位，自动
  /// 从默认列表消失。
  Future<void> _promoteToWiki() async {
    final repo = ref.read(notesRepositoryProvider);
    if (repo == null) {
      _showSnack('未连接 hub');
      return;
    }
    final projectId = await showDialog<String>(
      context: context,
      builder: (_) => const _PromoteProjectDialog(),
    );
    if (projectId == null || !mounted) return;
    try {
      await repo.promoteNote(widget.noteId, projectId);
      if (!mounted) return;
      _showSnack('已转入知识库');
      // 返回列表：桌面三栏清选中，手机 pop 详情页。
      if (ref.read(selectedNoteIdProvider) == widget.noteId) {
        ref.read(selectedNoteIdProvider.notifier).state = null;
      }
      if (isPhoneLayout(context) && Navigator.of(context).canPop()) {
        Navigator.of(context).pop();
      }
    } on Exception catch (e) {
      _showSnack('转入失败：$e');
    }
  }

  // ─── 待办（N2）────────────────────────────────────────────

  /// 本地 updateNote 乐观落库后 watch 流回声被 pendingUpdate 抑制
  /// （_onRemoteNote 跳过），元信息开关改完手动重拉一次本地行刷新 UI。
  Future<void> _reloadMeta() async {
    final repo = ref.read(notesRepositoryProvider);
    final note = await repo?.getNote(widget.noteId);
    if (note != null && mounted) {
      setState(() => _note = note);
    }
  }

  Future<void> _toggleTodo() async {
    final repo = ref.read(notesRepositoryProvider);
    final note = _note;
    if (repo == null || note == null) return;
    await repo.updateNote(widget.noteId, isTodo: !note.isTodo);
    await _reloadMeta();
  }

  Future<void> _toggleTodoCompleted() async {
    final repo = ref.read(notesRepositoryProvider);
    final note = _note;
    if (repo == null || note == null) return;
    if (note.todoCompletedAt == null) {
      await repo.updateNote(widget.noteId,
          todoCompletedAt: DateTime.now().toUtc());
    } else {
      await repo.updateNote(widget.noteId, clearTodoCompleted: true);
    }
    await _reloadMeta();
  }

  // ─── build ────────────────────────────────────────────────

  @override
  Widget build(BuildContext context) {
    // 远端变更监听（changes 轮询落库后驱动 setDoc）。
    ref.listen<AsyncValue<RepoNote?>>(noteByIdProvider(widget.noteId),
        (_, next) {
      final note = next.valueOrNull;
      if (note != null && _note != null) _onRemoteNote(note);
    });

    if (_initial == null) {
      return const Center(child: Text('未连接 hub，无法编辑笔记'));
    }
    return FutureBuilder<RepoNote?>(
      future: _initial,
      builder: (context, snap) {
        final note = _note;
        // _note 非空就渲染编辑器 —— 切笔记加载新正文期间保留旧内容
        // 在树上，webview 不卸载（加载完经 setDoc 换正文）。
        if (note == null) {
          if (snap.connectionState == ConnectionState.done) {
            return const Center(child: Text('笔记不存在或已删除'));
          }
          return const Center(child: CircularProgressIndicator());
        }
        final theme = Theme.of(context).brightness == Brightness.dark
            ? BridgeTheme.dark
            : BridgeTheme.light;
        return Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: <Widget>[
            NoteArchivedBanner(note: note),
            _TitleRow(
              controller: _titleController,
              onChanged: _titleAutosave.schedule,
              onTrash: _trashThisNote,
              note: note,
              onToggleTodo: _toggleTodo,
              onInsertImage: _insertImage,
              onInsertAttachment: _insertAttachment,
              onShowHistory: _showHistory,
              onPromoteToWiki:
                  note.promotedPageId == null ? _promoteToWiki : null,
            ),
            if (note.isTodo)
              _TodoCompletionBar(
                completedAt: note.todoCompletedAt,
                onToggle: _toggleTodoCompleted,
              ),
            _NoteTagsRow(noteId: widget.noteId),
            Divider(height: 1, color: BiuTokens.borderSubtle),
            Expanded(
              child: PageEditorView(
                // 喂给编辑器的是临时 URL 形式；落库前在 onMarkdownChanged
                // 里换回 biu-file://（见 _attachmentResolver）。
                initialMarkdown: _resolvedContent ?? note.contentMd,
                theme: theme,
                // 双链是 wiki 功能，笔记侧关掉 wikilink（mermaid 保留）。
                // engine 默认 'milkdown' = ProseMirror 连续 WYSIWYG（点哪编哪，
                // 整篇一个可编辑面，Joplin 式富文本）；与 wiki 同内核同 bundle。
                features: const BridgeFeatures(wikilink: false),
                onMarkdownChanged: (md) => _contentAutosave
                    .schedule(_attachmentResolver.toCanonical(md)),
                controllerRef: (c) => _editorController = c,
              ),
            ),
            Divider(height: 1, color: BiuTokens.borderSubtle),
            _StatusBar(
              contentStatus: _contentAutosave.status,
              titleStatus: _titleAutosave.status,
              errorMessage: _contentAutosave.errorMessage ??
                  _titleAutosave.errorMessage,
            ),
          ],
        );
      },
    );
  }
}

class _TitleRow extends StatelessWidget {
  const _TitleRow({
    required this.controller,
    required this.onChanged,
    required this.onTrash,
    required this.note,
    required this.onToggleTodo,
    required this.onInsertImage,
    required this.onInsertAttachment,
    required this.onShowHistory,
    required this.onPromoteToWiki,
  });

  final TextEditingController controller;
  final ValueChanged<String> onChanged;
  final VoidCallback onTrash;
  final RepoNote note;
  final VoidCallback onToggleTodo;
  final VoidCallback onInsertImage;
  final VoidCallback onInsertAttachment;
  final VoidCallback onShowHistory;

  /// null = 已转入知识库（归档），菜单里隐藏该入口。
  final VoidCallback? onPromoteToWiki;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.only(left: 20, right: 8, top: 4),
      child: Row(
        children: <Widget>[
          Expanded(
            child: TextField(
              controller: controller,
              onChanged: onChanged,
              style: TextStyle(
                color: BiuTokens.text,
                fontSize: 18,
                fontWeight: FontWeight.w600,
              ),
              decoration: InputDecoration(
                border: InputBorder.none,
                hintText: '无标题笔记',
                hintStyle: TextStyle(color: BiuTokens.textMuted),
              ),
            ),
          ),
          IconButton(
            tooltip: '插入图片',
            onPressed: onInsertImage,
            icon: Icon(Icons.image_outlined,
                size: 18, color: BiuTokens.textSecondary),
          ),
          IconButton(
            tooltip: '插入附件',
            onPressed: onInsertAttachment,
            icon: Icon(Icons.attach_file,
                size: 18, color: BiuTokens.textSecondary),
          ),
          IconButton(
            tooltip: note.isTodo ? '取消待办' : '转为待办',
            onPressed: onToggleTodo,
            icon: Icon(
              note.isTodo ? Icons.check_box : Icons.check_box_outline_blank,
              size: 18,
              color: note.isTodo ? BiuTokens.purple : BiuTokens.textSecondary,
            ),
          ),
          IconButton(
            tooltip: '移入回收站',
            onPressed: onTrash,
            icon: Icon(Icons.delete_outline,
                size: 18, color: BiuTokens.textSecondary),
          ),
          PopupMenuButton<String>(
            tooltip: '更多操作',
            icon: Icon(Icons.more_vert,
                size: 18, color: BiuTokens.textSecondary),
            onSelected: (value) {
              switch (value) {
                case 'history':
                  onShowHistory();
                case 'promote':
                  onPromoteToWiki?.call();
              }
            },
            itemBuilder: (context) => <PopupMenuEntry<String>>[
              const PopupMenuItem<String>(
                value: 'history',
                child: ListTile(
                  dense: true,
                  leading: Icon(Icons.history, size: 18),
                  title: Text('历史版本'),
                  contentPadding: EdgeInsets.zero,
                ),
              ),
              if (onPromoteToWiki != null)
                const PopupMenuItem<String>(
                  value: 'promote',
                  child: ListTile(
                    dense: true,
                    leading: Icon(Icons.library_add_outlined, size: 18),
                    title: Text('转入知识库'),
                    contentPadding: EdgeInsets.zero,
                  ),
                ),
            ],
          ),
        ],
      ),
    );
  }
}

/// 「已转入知识库」只读提示条（N3 最小实现）—— 笔记 promotedPageId 非空
/// （已归档）时显示在编辑器顶部；未转入时不占空间。
class NoteArchivedBanner extends StatelessWidget {
  const NoteArchivedBanner({super.key, required this.note});

  final RepoNote note;

  @override
  Widget build(BuildContext context) {
    if (note.promotedPageId == null) return const SizedBox.shrink();
    return Container(
      height: 34,
      color: BiuTokens.surface,
      padding: const EdgeInsets.symmetric(horizontal: 12),
      child: Row(
        children: <Widget>[
          Icon(Icons.library_books_outlined,
              size: 14, color: BiuTokens.textSecondary),
          const SizedBox(width: 8),
          Expanded(
            child: Text(
              '已转入知识库，此笔记已归档',
              style: TextStyle(fontSize: 12, color: BiuTokens.textSecondary),
              overflow: TextOverflow.ellipsis,
            ),
          ),
        ],
      ),
    );
  }
}

/// 转入知识库的 wiki project 选择弹窗 —— 项目列表复用 wiki 数据栈
/// （wikiProjectsForPromoteProvider：先服务端刷新、失败用本地缓存）。
class _PromoteProjectDialog extends ConsumerWidget {
  const _PromoteProjectDialog();

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final projects = ref.watch(wikiProjectsForPromoteProvider);
    return AlertDialog(
      title: const Text('转入知识库'),
      content: SizedBox(
        width: 360,
        child: projects.when(
          loading: () => const SizedBox(
            height: 80,
            child: Center(child: CircularProgressIndicator()),
          ),
          error: (e, _) => Text('加载项目失败：$e'),
          data: (list) => list.isEmpty
              ? const Text('暂无知识库项目，请先在知识库中创建')
              : ListView(
                  shrinkWrap: true,
                  children: <Widget>[
                    for (final p in list)
                      ListTile(
                        dense: true,
                        leading: const Icon(Icons.menu_book_outlined,
                            size: 18),
                        title: Text(p.name),
                        onTap: () => Navigator.of(context).pop(p.id),
                      ),
                  ],
                ),
        ),
      ),
      actions: <Widget>[
        TextButton(
          onPressed: () => Navigator.of(context).pop(),
          child: const Text('取消'),
        ),
      ],
    );
  }
}

/// 待办笔记的完成状态条 —— checkbox + 完成时间（编辑器顶部，标题行下）。
class _TodoCompletionBar extends StatelessWidget {
  const _TodoCompletionBar({required this.completedAt, required this.onToggle});

  final DateTime? completedAt;
  final VoidCallback onToggle;

  @override
  Widget build(BuildContext context) {
    final completed = completedAt != null;
    return Container(
      height: 34,
      color: BiuTokens.surface,
      padding: const EdgeInsets.symmetric(horizontal: 12),
      child: Row(
        children: <Widget>[
          SizedBox(
            width: 20,
            height: 20,
            child: Checkbox(
              value: completed,
              onChanged: (_) => onToggle(),
              materialTapTargetSize: MaterialTapTargetSize.shrinkWrap,
              visualDensity: VisualDensity.compact,
            ),
          ),
          const SizedBox(width: 8),
          Text(
            completed
                ? '已完成 · ${relativeTime(completedAt!)}'
                : '勾选标记完成',
            style: TextStyle(
              fontSize: 12,
              color:
                  completed ? BiuTokens.textMuted : BiuTokens.textSecondary,
              decoration: completed ? TextDecoration.lineThrough : null,
            ),
          ),
        ],
      ),
    );
  }
}

/// 编辑器标签行 —— 当前笔记标签 chips（可移除）+「+ 标签」选择弹窗。
///
/// 标签 id 走 noteTagIdsProvider（FutureProvider：本地 setNoteTags 后
/// invalidate；数据层无 note-tag 关联 watch，远端变更重开编辑器生效）。
class _NoteTagsRow extends ConsumerWidget {
  const _NoteTagsRow({required this.noteId});

  final String noteId;

  Future<void> _saveTagIds(WidgetRef ref, List<String> tagIds) async {
    final repo = ref.read(notesRepositoryProvider);
    if (repo == null) return;
    await repo.setNoteTags(noteId, tagIds);
    ref.invalidate(noteTagIdsProvider(noteId));
  }

  Future<void> _openTagPicker(
      BuildContext context, WidgetRef ref, List<String> current) async {
    final result = await showDialog<List<String>>(
      context: context,
      builder: (ctx) => _TagPickerDialog(initialSelected: current),
    );
    if (result == null) return;
    await _saveTagIds(ref, result);
  }

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final allTags =
        ref.watch(notesTagsProvider).valueOrNull ?? const <RepoTag>[];
    final tagIds = ref.watch(noteTagIdsProvider(noteId)).valueOrNull;
    if (tagIds == null) return const SizedBox(height: 34);
    final noteTags = <RepoTag>[
      for (final id in tagIds)
        for (final t in allTags)
          if (t.id == id) t,
    ];
    return Container(
      constraints: const BoxConstraints(minHeight: 34),
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 3),
      color: BiuTokens.surface,
      child: Wrap(
        spacing: 6,
        runSpacing: 4,
        crossAxisAlignment: WrapCrossAlignment.center,
        children: <Widget>[
          for (final tag in noteTags)
            InputChip(
              label: Text(tag.name),
              labelStyle: TextStyle(fontSize: 11, color: BiuTokens.text),
              deleteIcon: const Icon(Icons.close, size: 12),
              onDeleted: () => _saveTagIds(
                ref,
                tagIds.where((id) => id != tag.id).toList(),
              ),
              visualDensity: VisualDensity.compact,
              materialTapTargetSize: MaterialTapTargetSize.shrinkWrap,
              padding: EdgeInsets.zero,
            ),
          ActionChip(
            avatar: Icon(Icons.add, size: 12, color: BiuTokens.textSecondary),
            label: const Text('标签'),
            labelStyle:
                TextStyle(fontSize: 11, color: BiuTokens.textSecondary),
            onPressed: () => _openTagPicker(context, ref, tagIds),
            visualDensity: VisualDensity.compact,
            materialTapTargetSize: MaterialTapTargetSize.shrinkWrap,
            padding: EdgeInsets.zero,
          ),
        ],
      ),
    );
  }
}

/// 标签选择弹窗 —— 已有标签多选 + 底部输入新标签名（保存时创建）。
class _TagPickerDialog extends ConsumerStatefulWidget {
  const _TagPickerDialog({required this.initialSelected});

  final List<String> initialSelected;

  @override
  ConsumerState<_TagPickerDialog> createState() => _TagPickerDialogState();
}

class _TagPickerDialogState extends ConsumerState<_TagPickerDialog> {
  late final Set<String> _selected = {...widget.initialSelected};
  final _newTagController = TextEditingController();
  bool _saving = false;

  @override
  void dispose() {
    _newTagController.dispose();
    super.dispose();
  }

  Future<void> _save() async {
    if (_saving) return;
    setState(() => _saving = true);
    final newName = _newTagController.text.trim();
    if (newName.isNotEmpty) {
      final repo = ref.read(notesRepositoryProvider);
      if (repo != null) {
        final tag = await repo.createTag(newName);
        _selected.add(tag.id);
      }
    }
    if (mounted) Navigator.of(context).pop(_selected.toList());
  }

  @override
  Widget build(BuildContext context) {
    final tags =
        ref.watch(notesTagsProvider).valueOrNull ?? const <RepoTag>[];
    return AlertDialog(
      title: const Text('编辑标签'),
      content: SizedBox(
        width: 320,
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: <Widget>[
            if (tags.isEmpty)
              Padding(
                padding: const EdgeInsets.symmetric(vertical: 8),
                child: Text(
                  '暂无标签，可在下方新建',
                  style: TextStyle(fontSize: 12, color: BiuTokens.textMuted),
                ),
              )
            else
              Flexible(
                child: ListView(
                  shrinkWrap: true,
                  children: <Widget>[
                    for (final tag in tags)
                      CheckboxListTile(
                        dense: true,
                        controlAffinity: ListTileControlAffinity.leading,
                        title: Text(tag.name,
                            style: const TextStyle(fontSize: 13)),
                        value: _selected.contains(tag.id),
                        onChanged: (v) => setState(() {
                          if (v ?? false) {
                            _selected.add(tag.id);
                          } else {
                            _selected.remove(tag.id);
                          }
                        }),
                      ),
                  ],
                ),
              ),
            TextField(
              controller: _newTagController,
              decoration: const InputDecoration(hintText: '新标签名称（可选）'),
              onSubmitted: (_) => _save(),
            ),
          ],
        ),
      ),
      actions: <Widget>[
        TextButton(
          onPressed: () => Navigator.of(context).pop(),
          child: const Text('取消'),
        ),
        FilledButton(
          onPressed: _saving ? null : _save,
          child: const Text('确定'),
        ),
      ],
    );
  }
}

class _StatusBar extends StatelessWidget {
  const _StatusBar({
    required this.contentStatus,
    required this.titleStatus,
    this.errorMessage,
  });

  final AutoSaveStatus contentStatus;
  final AutoSaveStatus titleStatus;
  final String? errorMessage;

  @override
  Widget build(BuildContext context) {
    // 两个 autosave 任一在 saving 就算保存中；任一 error 就算失败。
    final status = switch ((contentStatus, titleStatus)) {
      (AutoSaveStatus.error, _) || (_, AutoSaveStatus.error) =>
        AutoSaveStatus.error,
      (AutoSaveStatus.saving, _) || (_, AutoSaveStatus.saving) =>
        AutoSaveStatus.saving,
      (AutoSaveStatus.saved, _) || (_, AutoSaveStatus.saved) =>
        AutoSaveStatus.saved,
      _ => AutoSaveStatus.idle,
    };
    final (label, color) = switch (status) {
      AutoSaveStatus.saving => ('保存中…', BiuTokens.textMuted),
      AutoSaveStatus.saved => ('已保存', BiuTokens.textMuted),
      AutoSaveStatus.error => ('保存失败', BiuTokens.error),
      _ => ('', BiuTokens.textMuted),
    };
    return Container(
      height: 24,
      alignment: Alignment.centerRight,
      padding: const EdgeInsets.symmetric(horizontal: 12),
      child: Text(
        status == AutoSaveStatus.error && errorMessage != null
            ? '$label：$errorMessage'
            : label,
        style: TextStyle(fontSize: 11, color: color),
        overflow: TextOverflow.ellipsis,
      ),
    );
  }
}

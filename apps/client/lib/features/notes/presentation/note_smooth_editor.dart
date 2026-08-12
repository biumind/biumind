/// 笔记正文编辑器 —— flutter_smooth_markdown 封装层（隔离层）。
///
/// 取代笔记侧原 WebView+Milkdown（`core/editor/PageEditorView`）：
///   * 冷启动零成本（无 webview / 无 bundle 构建）
///   * 中文 IME 走系统 TextField（smooth_markdown controller 在 composing
///     态显式抑制 onChanged / setState，候选词不上屏不重解析，见包内
///     `_handleControllerChanged` 的 `if (!composing)` 守卫）
///   * markdown 源码即权威、无损，匹配 `content_md` TEXT 列
///
/// 接线（与原 `PageEditorView` 形状一致，宿主 `note_editor_view` 改动最小）：
///   * 自动保存：`onChanged(md)` → 宿主 `toCanonical(md)` → AutoSaveController
///     （1.5s 防抖）→ NotesRepository.updateNote（乐观落 Drift + outbox）。
///   * 远端覆盖：宿主拿 `NoteSmoothEditorHandle.setDoc(md)` 推进 —— 这里置
///     `_applyingExternal` 守卫，避免 setDoc 触发的 onChanged 回声冲掉正在
///     编辑的内容（等价原 Milkdown `applyingExternalEdit` 标志）。
///   * 附件：宿主 `Handle.insertMarkdown(text)` 光标处插 `![](url)` /
///     `[](biu-file://uuid)`（上传在宿主侧完成，见 `_uploadAndResolve`）。
///
/// 把对 flutter_smooth_markdown API 的依赖收敛在本文件内 —— 升级包只动这一处。
/// 模式：split（源码 + 预览同屏，直击「markdown 与富文本同屏」需求）默认；源码侧
/// 是整篇一个连续 TextField（CJK 安全）。formatted / source / preview 仍在编辑器
/// 自带 toolbar 可切。归档态 `editable=false` 强制 preview。正文居中限宽（split
/// 1100 / 单栏 760，见 build）。
library;

import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_smooth_markdown/flutter_smooth_markdown_editor.dart';

/// 宿主持有的编辑器句柄 —— 暴露 setDoc（远端覆盖，带回声守卫）与
/// insertMarkdown（附件插入，走正常 onChanged→autosave 链路）。
/// 持回调闭包而非 state 引用，避免私有类型泄漏到 public API。
class NoteSmoothEditorHandle {
  NoteSmoothEditorHandle({
    required void Function(String markdown) setDoc,
    required void Function(String markdown) insertMarkdown,
    required this.editable,
  })  : _setDoc = setDoc,
        _insertMarkdown = insertMarkdown;

  final void Function(String) _setDoc;
  final void Function(String) _insertMarkdown;

  /// 是否还能编辑（归档态 false）。
  final bool editable;

  /// 远端覆盖：把整篇 markdown 推进编辑器。置回声守卫，不触发 onChanged。
  /// 传入前宿主应已 `resolveForEditor`（biu-file → 临时 URL）。
  void setDoc(String markdown) => _setDoc(markdown);

  /// 光标处插入 markdown 片段（图片 / 附件链接）。走正常编辑链路，会触发
  /// onChanged → autosave。
  void insertMarkdown(String markdown) => _insertMarkdown(markdown);
}

class NoteSmoothEditor extends StatefulWidget {
  const NoteSmoothEditor({
    super.key,
    required this.initialMarkdown,
    this.editable = true,
    this.initialMode = MarkdownEditorMode.split,
    this.onChanged,
    this.onControllerReady,
    this.onPickImage,
    this.toolbarLeading = const <Widget>[],
  });

  /// 首屏 markdown（已过 `NoteAttachmentResolver.resolveForEditor` 的临时 URL
  /// 形式）。后续内容变更走 `NoteSmoothEditorHandle.setDoc`。
  final String initialMarkdown;

  /// false = 归档只读态，强制 preview 模式 + 禁用编辑。
  final bool editable;

  /// 默认 split（源码 + 预览同屏，直击「markdown 与富文本同屏」需求）。源码侧是
  /// 整篇一个连续 TextField（`_controller.textController`，CJK IME 安全、无盒子、
  /// 无 per-block 焦点），右侧实时渲染预览。formatted 是 Notion 式 block 编辑器
  /// （一次只编一个 block + 活跃 block 套边框），实测不适合连续写作，不作默认；
  /// 仍可在 toolbar 切。归档态忽略，用 preview。
  final MarkdownEditorMode initialMode;

  /// markdown 源码变化（IME 组合态期间被包内抑制，组合提交后才发）。
  /// 传入的是编辑器形态（临时 URL），宿主负责 `toCanonical` 后再存。
  final ValueChanged<String>? onChanged;

  /// 句柄就绪回调 —— 宿主存下供 setDoc / insertMarkdown 调用。
  final ValueChanged<NoteSmoothEditorHandle>? onControllerReady;

  /// 编辑器工具栏「插入图片」按钮回调：宿主完成选图 + presign 上传，返回
  /// 带「临时 URL」的 selection，编辑器自行插入 `![alt](url)`。
  final FutureOr<MarkdownEditorImageSelection?> Function()? onPickImage;

  /// 前置到编辑器自带 toolbar 最左的自定义按钮（笔记侧挂「插入图片/附件」，
  /// 与 bold/italic 等格式命令同栏）。空 = 仅包内默认按钮。
  final List<Widget> toolbarLeading;

  @override
  State<NoteSmoothEditor> createState() => _NoteSmoothEditorState();
}

class _NoteSmoothEditorState extends State<NoteSmoothEditor> {
  late final MarkdownEditorController _controller;
  late MarkdownEditorMode _mode;

  /// 远端 setDoc 进行中 —— 抑制其触发的 onChanged 回声。
  bool _applyingExternal = false;

  @override
  void initState() {
    super.initState();
    _controller = MarkdownEditorController(text: widget.initialMarkdown);
    _mode = widget.editable
        ? widget.initialMode
        : MarkdownEditorMode.preview;
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!mounted) return;
      widget.onControllerReady?.call(NoteSmoothEditorHandle(
        setDoc: _setMarkdownExternal,
        insertMarkdown: _controller.insertMarkdown,
        editable: widget.editable,
      ));
    });
  }

  @override
  void didUpdateWidget(covariant NoteSmoothEditor oldWidget) {
    super.didUpdateWidget(oldWidget);
    // editable 切换 → 归档/取消归档时同步模式。
    if (oldWidget.editable != widget.editable) {
      _mode = widget.editable
          ? widget.initialMode
          : MarkdownEditorMode.preview;
    }
    // 首屏 markdown 变更（防御：宿主正常走 Handle.setDoc，不走这里）。
    if (oldWidget.initialMarkdown != widget.initialMarkdown &&
        widget.initialMarkdown != _controller.text) {
      _setMarkdownExternal(widget.initialMarkdown);
    }
  }

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  /// 远端覆盖入口：置回声守卫后赋值。controller.set text 自带相等去重。
  void _setMarkdownExternal(String markdown) {
    if (!mounted) return;
    _applyingExternal = true;
    _controller.text = markdown;
    _applyingExternal = false;
  }

  void _onChanged(String md) {
    if (_applyingExternal) return; // 抑制 setDoc 回声
    widget.onChanged?.call(md);
  }

  void _onModeChanged(MarkdownEditorMode mode) {
    if (!widget.editable) return; // 归档态锁 preview
    setState(() => _mode = mode);
  }

  @override
  Widget build(BuildContext context) {
    // 居中限宽：宽屏（桌面右栏 Expanded 可达 ~2500px）单行拉极长难读。
    // 按模式给不同上限：split 两半屏各需 ~550 → 总 1100；formatted/preview
    // 单栏阅读 → 760。手机窄屏（< 上限）自然不触发。编辑器自带 toolbar
    // 也随之居中，与正文对齐（对标 Typora / MarkText 双栏）。
    final maxWidth =
        _mode == MarkdownEditorMode.split ? 1100.0 : 760.0;
    return Center(
      child: ConstrainedBox(
        constraints: BoxConstraints(maxWidth: maxWidth),
        child: SmoothMarkdownEditor(
          controller: _controller,
          // host 权威模式（归档态强制 preview）。
          mode: _mode,
          onModeChanged: _onModeChanged,
          enabled: widget.editable,
          showToolbar: true,
          toolbarLeading: widget.toolbarLeading,
          onChanged: _onChanged,
          onPickImage: widget.onPickImage,
          // 笔记侧关 wikilink（wiki 才有双链）；mermaid / math / table 保留。
          enableWikilinks: false,
          wikilinkSuggestions: const <String>[],
          capabilities: const MarkdownEditorCapabilities(
            disabledCommands: <MarkdownEditorCommand>{
              MarkdownEditorCommand.wikilink,
            },
          ),
          // 笔记随宿主主题；编辑器主题（toolbar / 分割线 / block 边框色）
          // 由 buildTheme 注册的 MarkdownEditorThemeData 提供。
          autofocus: false,
        ),
      ),
    );
  }
}

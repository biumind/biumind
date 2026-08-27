// 单文件内容视图 —— 主区文件编辑 Tab 的内容体(只读)。
//
// 自取内容:按路径在 init / 路径变化时经 bridge 拉文本(fsReadFile)或图片
// (fsImagePreview),文本走 flutter_highlight 语法高亮,图片走 Image.memory。
// 从 file_explorer_panel 的 _TextView/_ImageView 抽出,供 FileEditorTabs 复用。

import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_highlight/flutter_highlight.dart';
import 'package:flutter_highlight/themes/atom-one-dark.dart';
import 'package:flutter_highlight/themes/atom-one-light.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../../app/theme.dart';
import '../../../../core/ui/biu_scrollbar.dart';
import '../../data/code_bridge_provider.dart';
import '../../domain/git_models.dart' show FileImagePreview;

const _imageExts = {'png', 'jpg', 'jpeg', 'gif', 'webp', 'bmp', 'svg'};
const _maxTextBytes = 2 * 1024 * 1024;

sealed class _Loaded {
  const _Loaded();
}

class _Text extends _Loaded {
  const _Text(this.text, this.truncated);
  final String text;
  final bool truncated;
}

class _Image extends _Loaded {
  const _Image(this.preview);
  final FileImagePreview preview;
}

class _Notice extends _Loaded {
  const _Notice(this.message);
  final String message;
}

class FileContentView extends ConsumerStatefulWidget {
  const FileContentView({super.key, required this.path, required this.root});

  /// 文件绝对路径。
  final String path;

  /// 项目根(图片预览的越界校验基准)。
  final String root;

  @override
  ConsumerState<FileContentView> createState() => _FileContentViewState();
}

class _FileContentViewState extends ConsumerState<FileContentView> {
  _Loaded? _content;
  bool _loading = true;

  @override
  void initState() {
    super.initState();
    _load();
  }

  @override
  void didUpdateWidget(covariant FileContentView old) {
    super.didUpdateWidget(old);
    if (old.path != widget.path) _load();
  }

  Future<void> _load() async {
    setState(() {
      _loading = true;
      _content = null;
    });
    final bridge = ref.read(codeBridgeClientProvider);
    if (bridge == null) {
      setState(() {
        _loading = false;
        _content = const _Notice('本地 daemon 未就绪');
      });
      return;
    }
    try {
      final ext = widget.path.contains('.')
          ? widget.path.split('.').last.toLowerCase()
          : '';
      if (_imageExts.contains(ext)) {
        final preview = await bridge.fsImagePreview(widget.root, widget.path);
        if (!mounted) return;
        setState(() {
          _loading = false;
          _content = _Image(preview);
        });
        return;
      }
      final r = await bridge.fsReadFile(widget.path, maxBytes: _maxTextBytes);
      if (!mounted) return;
      setState(() {
        _loading = false;
        _content = _looksBinary(r.content)
            ? const _Notice('二进制文件,无法以文本显示')
            : _Text(r.content, r.truncated);
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _loading = false;
        _content = _Notice('打开失败: $e');
      });
    }
  }

  static bool _looksBinary(String s) {
    final n = s.length < 1024 ? s.length : 1024;
    for (var i = 0; i < n; i++) {
      if (s.codeUnitAt(i) == 0) return true;
    }
    return false;
  }

  @override
  Widget build(BuildContext context) {
    if (_loading) {
      return const Center(child: CircularProgressIndicator(strokeWidth: 2));
    }
    return switch (_content) {
      _Text t => _EditableTextView(
          key: ValueKey('edit_${widget.path}'),
          path: widget.path,
          root: widget.root,
          text: t.text,
          truncated: t.truncated,
        ),
      _Image img => _ImageView(preview: img.preview),
      _Notice n => Center(
          child: Text(n.message,
              style: TextStyle(fontSize: 12.5, color: BiuTokens.textMuted)),
        ),
      null => const SizedBox.shrink(),
    };
  }
}

/// 文本文件视图:默认只读语法高亮;点「编辑」切到可编辑 TextField,「保存」经
/// fs.write 写回(CORE-6 修「点击文件无法编辑」)。截断的大文件不可编辑(内容不全)。
class _EditableTextView extends ConsumerStatefulWidget {
  const _EditableTextView({
    super.key,
    required this.path,
    required this.root,
    required this.text,
    required this.truncated,
  });
  final String path;
  final String root;
  final String text;
  final bool truncated;

  @override
  ConsumerState<_EditableTextView> createState() => _EditableTextViewState();
}

class _EditableTextViewState extends ConsumerState<_EditableTextView> {
  late String _saved = widget.text;
  TextEditingController? _draft;
  bool _editing = false;
  bool _saving = false;

  bool get _dirty => _draft != null && _draft!.text != _saved;

  void _startEdit() {
    setState(() {
      _draft = TextEditingController(text: _saved);
      _editing = true;
    });
  }

  void _cancel() {
    _draft?.dispose();
    setState(() {
      _draft = null;
      _editing = false;
    });
  }

  Future<void> _save() async {
    final bridge = ref.read(codeBridgeClientProvider);
    final messenger = ScaffoldMessenger.of(context);
    if (bridge == null) {
      messenger.showSnackBar(const SnackBar(content: Text('daemon 未就绪,无法保存')));
      return;
    }
    final content = _draft!.text;
    setState(() => _saving = true);
    try {
      await bridge.fsWrite(widget.root, widget.path, content);
      _draft?.dispose();
      setState(() {
        _saved = content;
        _draft = null;
        _editing = false;
        _saving = false;
      });
      messenger.showSnackBar(
        const SnackBar(content: Text('已保存'), duration: Duration(seconds: 1)),
      );
    } catch (e) {
      setState(() => _saving = false);
      messenger.showSnackBar(SnackBar(content: Text('保存失败: $e')));
    }
  }

  @override
  void dispose() {
    _draft?.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        // 路径条 + 编辑/保存/取消/复制。
        Container(
          padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 4),
          decoration: BoxDecoration(
            border: Border(bottom: BorderSide(color: BiuTokens.borderSubtle)),
          ),
          child: Row(
            children: [
              Expanded(
                child: Text(
                  _dirty ? '${widget.path} ●' : widget.path,
                  overflow: TextOverflow.ellipsis,
                  style: TextStyle(
                      fontSize: 11.5,
                      fontFamily: 'SF Mono',
                      color: BiuTokens.textSecondary),
                ),
              ),
              if (_editing) ...[
                if (_saving)
                  const Padding(
                    padding: EdgeInsets.symmetric(horizontal: 8),
                    child: SizedBox(
                        width: 13,
                        height: 13,
                        child: CircularProgressIndicator(strokeWidth: 1.6)),
                  )
                else
                  _MiniBtn(icon: Icons.check_rounded, tip: '保存 (写回磁盘)', onTap: _save),
                _MiniBtn(icon: Icons.close_rounded, tip: '取消', onTap: _cancel),
              ] else ...[
                if (!widget.truncated)
                  _MiniBtn(icon: Icons.edit_outlined, tip: '编辑', onTap: _startEdit),
                _MiniBtn(
                  icon: Icons.copy_rounded,
                  tip: '复制全文',
                  onTap: () =>
                      Clipboard.setData(ClipboardData(text: _saved)),
                ),
              ],
            ],
          ),
        ),
        Expanded(child: _editing ? _editor() : _readonly()),
      ],
    );
  }

  Widget _readonly() {
    final theme = BiuTokens.brightness == Brightness.dark
        ? atomOneDarkTheme
        : atomOneLightTheme;
    final lang = _langForExt(widget.path);
    return SelectionArea(
      child: BiuScrollbar(
        child: SingleChildScrollView(
          primary: true,
          child: SingleChildScrollView(
            scrollDirection: Axis.horizontal,
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                HighlightView(
                  _saved.isEmpty ? ' ' : _saved,
                  language: lang,
                  theme: theme,
                  padding: const EdgeInsets.all(12),
                  textStyle: const TextStyle(
                      fontFamily: 'JetBrains Mono, ui-monospace, monospace',
                      fontSize: 12.5,
                      height: 1.5),
                ),
                if (widget.truncated)
                  Padding(
                    padding: const EdgeInsets.all(12),
                    child: Text('…(文件过大,已截断,不可编辑)',
                        style: TextStyle(
                            fontSize: 11.5, color: BiuTokens.textMuted)),
                  ),
              ],
            ),
          ),
        ),
      ),
    );
  }

  Widget _editor() {
    return TextField(
      controller: _draft,
      maxLines: null,
      expands: true,
      onChanged: (_) => setState(() {}),
      textAlignVertical: TextAlignVertical.top,
      style: const TextStyle(
          fontFamily: 'JetBrains Mono, ui-monospace, monospace',
          fontSize: 12.5,
          height: 1.5),
      decoration: const InputDecoration(
        isDense: true,
        contentPadding: EdgeInsets.all(12),
        border: InputBorder.none,
        focusedBorder: InputBorder.none,
      ),
    );
  }
}

String _langForExt(String path) {
    final ext = path.contains('.') ? path.split('.').last.toLowerCase() : '';
    return switch (ext) {
      'dart' => 'dart',
      'go' => 'go',
      'py' => 'python',
      'js' || 'mjs' || 'cjs' => 'javascript',
      'ts' => 'typescript',
      'jsx' || 'tsx' => 'typescript',
      'rs' => 'rust',
      'java' => 'java',
      'kt' => 'kotlin',
      'swift' => 'swift',
      'c' || 'h' => 'c',
      'cpp' || 'cc' || 'hpp' => 'cpp',
      'rb' => 'ruby',
      'php' => 'php',
      'sh' || 'bash' || 'zsh' => 'bash',
      'sql' => 'sql',
      'yaml' || 'yml' => 'yaml',
      'toml' => 'ini',
      'json' => 'json',
      'md' || 'markdown' => 'markdown',
      'html' => 'xml',
      'css' => 'css',
      'xml' => 'xml',
      _ => 'plaintext',
    };
}

class _MiniBtn extends StatelessWidget {
  const _MiniBtn({required this.icon, required this.tip, required this.onTap});
  final IconData icon;
  final String tip;
  final VoidCallback onTap;
  @override
  Widget build(BuildContext context) => Tooltip(
        message: tip,
        child: InkWell(
          onTap: onTap,
          borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
          child: SizedBox(
            width: 26,
            height: 26,
            child: Icon(icon, size: 15, color: BiuTokens.textSecondary),
          ),
        ),
      );
}

class _ImageView extends StatelessWidget {
  const _ImageView({required this.preview});
  final FileImagePreview preview;

  @override
  Widget build(BuildContext context) {
    if (preview.mimeType == 'image/svg+xml') {
      return Center(
        child: Text('SVG 预览暂不支持(${preview.byteLength} B)',
            style: TextStyle(fontSize: 12.5, color: BiuTokens.textMuted)),
      );
    }
    final comma = preview.dataUrl.indexOf(',');
    if (comma < 0) {
      return Center(
          child: Text('图片数据无效',
              style: TextStyle(fontSize: 12.5, color: BiuTokens.textMuted)));
    }
    final bytes = base64Decode(preview.dataUrl.substring(comma + 1));
    return Container(
      color: BiuTokens.surfaceMuted,
      child: Center(
        child: InteractiveViewer(
          maxScale: 8,
          child: Image.memory(bytes, fit: BoxFit.contain),
        ),
      ),
    );
  }
}

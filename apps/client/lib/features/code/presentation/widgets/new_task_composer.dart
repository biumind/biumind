// 新建任务 —— 主区内嵌居中页(替代原弹窗)。
//
// 布局:logo + 「今天想构建什么?」标题 → 居中输入卡(maxWidth 820)
//   ├ 多行 prompt(占位「描述你的任务… 输入 @ 可引用文件」,键入 @ 唤起文件选择)
//   ├ 附件预览行(贴图缩略 / 文本 chip)
//   └ 底部工具条:[+ 附件] [Agent▾] [权限▾] ……spacer…… [启动终端 ⌘↵]
// 卡下方:[运行环境▾]( + worktree 时基分支▾)。
//
// **不含**模型选择与对比模式(那是 biumind 增项,此页去掉)。Cmd/Ctrl+Enter
// 提交;空 prompt 也可提交(起交互式终端会话)。
// 提交即 createTask 并切到该任务,主区随之显终端。

import 'dart:async';

import 'package:file_selector/file_selector.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:pasteboard/pasteboard.dart';
import 'package:uuid/uuid.dart';

import '../../../../app/theme.dart';
import '../../../../shared/brand/biu_mark.dart';
import '../../application/projects_controller.dart';
import '../../application/tasks_controller.dart';
import '../../data/code_bridge_provider.dart';
import '../../domain/code_task.dart';
import '../../domain/git_models.dart' show FileSearchResult;

class NewTaskComposer extends ConsumerStatefulWidget {
  const NewTaskComposer({super.key});

  @override
  ConsumerState<NewTaskComposer> createState() => _NewTaskComposerState();
}

class _NewTaskComposerState extends ConsumerState<NewTaskComposer> {
  final _ctrl = TextEditingController();
  final _focus = FocusNode();

  AgentKind _agent = AgentKind.biu;
  PermissionMode _mode = PermissionMode.ask;
  CodeLaunchMode _launchMode = CodeLaunchMode.auto;

  String? _baseBranch;
  List<String> _branches = const [];
  bool _loadingBranches = false;

  final List<({String name, Uint8List bytes})> _imageAttachments = [];
  final List<({String name, String text})> _textAttachments = [];

  // @ 自动唤起 mention 的去重 / 重入守卫。
  String _lastPrompt = '';
  bool _mentionInFlight = false;
  bool _submitting = false;

  @override
  void initState() {
    super.initState();
    _ctrl.addListener(_onPromptChanged);
    WidgetsBinding.instance.addPostFrameCallback((_) {
      _focus.requestFocus();
      _loadProjectDefaults();
    });
  }

  @override
  void dispose() {
    _ctrl.dispose();
    _focus.dispose();
    super.dispose();
  }

  /// 用项目级 .biu/config.toml 预填默认 agent / 权限档(读失败静默)。
  Future<void> _loadProjectDefaults() async {
    final bridge = ref.read(codeBridgeClientProvider);
    final root = ref.read(activeCodeProjectProvider)?.path;
    if (bridge == null || root == null) return;
    try {
      final cfg = await bridge.configRead(root);
      if (!mounted) return;
      setState(() {
        _agent = switch (cfg.agentDefault) {
          'claude' => AgentKind.claudeCode,
          'codex' => AgentKind.codex,
          _ => AgentKind.biu,
        };
        _mode = switch (cfg.defaultPermissionMode) {
          'auto_edit' => PermissionMode.autoEdit,
          'full_access' => PermissionMode.fullAccess,
          _ => PermissionMode.ask,
        };
      });
    } catch (_) {/* 默认即兜底 */}
  }

  // ── @ mention:键入即唤起 ───────────────────────────────────
  void _onPromptChanged() {
    final text = _ctrl.text;
    final sel = _ctrl.selection;
    if (_mentionInFlight) {
      _lastPrompt = text;
      return;
    }
    if (text.length == _lastPrompt.length + 1 && sel.isValid && sel.isCollapsed) {
      final caret = sel.start;
      if (caret > 0 && caret <= text.length && text[caret - 1] == '@') {
        final before = caret >= 2 ? text[caret - 2] : ' ';
        if (before == ' ' || before == '\n') {
          _lastPrompt = text;
          final atPos = caret;
          WidgetsBinding.instance
              .addPostFrameCallback((_) => _triggerMention(atPos));
          return;
        }
      }
    }
    _lastPrompt = text;
    setState(() {}); // 刷新发送按钮文案(启动终端 ↔ 发送)
  }

  Future<void> _triggerMention(int atPos) async {
    if (_mentionInFlight) return;
    _mentionInFlight = true;
    try {
      final text = _ctrl.text;
      if (atPos >= 1 && atPos <= text.length && text[atPos - 1] == '@') {
        _ctrl.text = text.replaceRange(atPos - 1, atPos, '');
        _ctrl.selection = TextSelection.collapsed(offset: atPos - 1);
      }
      await _insertMention();
    } finally {
      _mentionInFlight = false;
      _lastPrompt = _ctrl.text;
    }
  }

  Future<void> _insertMention() async {
    final root = ref.read(activeCodeProjectProvider)?.path;
    if (root == null) {
      _toast('请先打开一个项目');
      return;
    }
    final rel = await showDialog<String>(
      context: context,
      builder: (_) => _MentionPicker(root: root),
    );
    if (rel == null || rel.isEmpty) return;
    final sel = _ctrl.selection;
    final text = _ctrl.text;
    final at = sel.isValid ? sel.start : text.length;
    final insert = '@$rel ';
    _ctrl.text =
        text.replaceRange(at, sel.isValid ? sel.end : text.length, insert);
    _ctrl.selection = TextSelection.collapsed(offset: at + insert.length);
    _focus.requestFocus();
  }

  // ── 附件 ──────────────────────────────────────────────────
  Future<void> _pasteImage() async {
    final bytes = await Pasteboard.image;
    if (bytes == null) {
      _toast('剪贴板没有图片');
      return;
    }
    setState(() => _imageAttachments
        .add((name: 'pasted-${_imageAttachments.length + 1}.png', bytes: bytes)));
  }

  Future<void> _pickImage() async {
    const group = XTypeGroup(
        label: 'images', extensions: ['png', 'jpg', 'jpeg', 'gif', 'webp', 'bmp']);
    final file = await openFile(acceptedTypeGroups: [group]);
    if (file == null) return;
    final bytes = await file.readAsBytes();
    if (!mounted) return;
    setState(() => _imageAttachments.add((name: file.name, bytes: bytes)));
  }

  Future<void> _addTextAttachment() async {
    final data = await Clipboard.getData(Clipboard.kTextPlain);
    final text = data?.text;
    if (text == null || text.trim().isEmpty) {
      _toast('剪贴板没有文本');
      return;
    }
    setState(() => _textAttachments
        .add((name: '粘贴文本 ${_textAttachments.length + 1}', text: text)));
  }

  // ── worktree 基分支懒加载 ──────────────────────────────────
  Future<void> _loadBranches() async {
    if (_loadingBranches || _branches.isNotEmpty) return;
    final bridge = ref.read(codeBridgeClientProvider);
    final root = ref.read(activeCodeProjectProvider)?.path;
    if (bridge == null || root == null) return;
    setState(() => _loadingBranches = true);
    try {
      final branches = await bridge.gitListBranches(root);
      if (!mounted) return;
      setState(() {
        _branches =
            branches.where((b) => !b.isRemote).map((b) => b.name).toList();
        _loadingBranches = false;
      });
    } catch (_) {
      if (mounted) setState(() => _loadingBranches = false);
    }
  }

  // ── 提交 ──────────────────────────────────────────────────
  Future<void> _submit() async {
    if (_submitting) return;
    _submitting = true;
    final ctl = ref.read(codeTasksProvider.notifier);
    final projectId = ref.read(activeCodeProjectIdProvider);
    var prompt = _ctrl.text.trim();
    for (final a in _textAttachments) {
      prompt += '\n\n--- 附件:${a.name} ---\n```\n${a.text}\n```';
    }
    if (_imageAttachments.isNotEmpty) {
      final root = ref.read(activeCodeProjectProvider)?.path;
      final bridge = ref.read(codeBridgeClientProvider);
      if (root != null && bridge != null) {
        final dir = '.biu/attachments/${const Uuid().v4()}';
        for (final img in _imageAttachments) {
          final rel = '$dir/${img.name}';
          try {
            await bridge.fsWriteBytes(root, rel, img.bytes);
            prompt += '\n\n图片附件:$root/$rel';
          } catch (_) {/* 单张失败不阻断 */}
        }
      }
    }
    final id = ctl.createTask(
      prompt: prompt,
      agent: _agent,
      mode: _mode,
      projectId: projectId,
      launchMode: _launchMode,
      baseRef: _launchMode == CodeLaunchMode.worktree ? _baseBranch : null,
    );
    // 切到新任务 → 主区显终端;本 composer 随之卸载。
    ref.read(activeCodeTaskIdProvider.notifier).state = id;
  }

  void _toast(String msg) {
    if (mounted) {
      ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(msg)));
    }
  }

  bool get _hasAttachments =>
      _imageAttachments.isNotEmpty || _textAttachments.isNotEmpty;

  @override
  Widget build(BuildContext context) {
    return CallbackShortcuts(
      bindings: {
        const SingleActivator(LogicalKeyboardKey.enter, meta: true): _submit,
        const SingleActivator(LogicalKeyboardKey.enter, control: true): _submit,
      },
      child: Center(
        child: SingleChildScrollView(
          padding: const EdgeInsets.symmetric(horizontal: 48, vertical: 32),
          child: ConstrainedBox(
            constraints: const BoxConstraints(maxWidth: 820),
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                const BiuMark(size: 44),
                const SizedBox(height: 18),
                Text('今天想构建什么?',
                    style: Theme.of(context).textTheme.titleLarge?.copyWith(
                          fontWeight: FontWeight.w600,
                          letterSpacing: -0.3,
                        )),
                const SizedBox(height: 26),
                _composeCard(),
                const SizedBox(height: 8),
                _launchModeRow(),
              ],
            ),
          ),
        ),
      ),
    );
  }

  Widget _composeCard() {
    return Container(
      decoration: BoxDecoration(
        color: BiuTokens.surface,
        borderRadius: BorderRadius.circular(20),
        border: Border.all(color: BiuTokens.borderSubtle),
        boxShadow: [
          BoxShadow(
            color: Colors.black.withValues(alpha: 0.05),
            blurRadius: 24,
            offset: const Offset(0, 8),
          ),
        ],
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Padding(
            padding: const EdgeInsets.fromLTRB(20, 18, 20, 4),
            child: TextField(
              controller: _ctrl,
              focusNode: _focus,
              maxLines: 6,
              minLines: 4,
              style: const TextStyle(fontSize: 14, height: 1.6),
              decoration: const InputDecoration(
                isCollapsed: true,
                border: InputBorder.none,
                hintText: '描述你的任务… 输入 @ 可引用文件',
              ),
            ),
          ),
          if (_hasAttachments)
            Padding(
              padding: const EdgeInsets.fromLTRB(20, 6, 20, 0),
              child: Wrap(
                spacing: 8,
                runSpacing: 8,
                crossAxisAlignment: WrapCrossAlignment.center,
                children: [
                  for (var i = 0; i < _imageAttachments.length; i++)
                    _ImageThumb(
                      bytes: _imageAttachments[i].bytes,
                      onRemove: () =>
                          setState(() => _imageAttachments.removeAt(i)),
                    ),
                  for (var i = 0; i < _textAttachments.length; i++)
                    _AttachmentChip(
                      label: _textAttachments[i].name,
                      onRemove: () =>
                          setState(() => _textAttachments.removeAt(i)),
                    ),
                ],
              ),
            ),
          Padding(
            padding: const EdgeInsets.fromLTRB(12, 10, 12, 12),
            child: _toolbar(),
          ),
        ],
      ),
    );
  }

  Widget _toolbar() {
    return Row(
      children: [
        _PlusMenu(
          onPickImage: _pickImage,
          onPasteImage: _pasteImage,
          onPasteText: _addTextAttachment,
        ),
        const SizedBox(width: 6),
        _ToolbarDropdown<AgentKind>(
          icon: Icons.auto_awesome_rounded,
          label: _agent.label,
          value: _agent,
          items: const [
            (AgentKind.biu, 'biu'),
            (AgentKind.claudeCode, 'Claude Code'),
            (AgentKind.codex, 'Codex'),
          ],
          onSelected: (v) => setState(() => _agent = v),
        ),
        const SizedBox(width: 6),
        _ToolbarDropdown<PermissionMode>(
          icon: Icons.pan_tool_outlined,
          label: _permLabel(_mode),
          value: _mode,
          items: const [
            (PermissionMode.ask, 'Ask Permission'),
            (PermissionMode.autoEdit, 'Auto Edit'),
            (PermissionMode.fullAccess, 'Full Access'),
          ],
          onSelected: (v) => setState(() => _mode = v),
        ),
        const Spacer(),
        _SendButton(
          label: _ctrl.text.trim().isEmpty ? '启动终端' : '发送',
          onTap: _submit,
        ),
      ],
    );
  }

  Widget _launchModeRow() {
    return ConstrainedBox(
      constraints: const BoxConstraints(maxWidth: 820),
      child: Row(
        children: [
          _ToolbarDropdown<CodeLaunchMode>(
            icon: Icons.devices_outlined,
            label: _launchMode.label,
            value: _launchMode,
            items: const [
              (CodeLaunchMode.auto, '跟随设置'),
              (CodeLaunchMode.local, '本地处理'),
              (CodeLaunchMode.worktree, '新工作树'),
            ],
            onSelected: (v) {
              setState(() => _launchMode = v);
              if (v == CodeLaunchMode.worktree) _loadBranches();
            },
          ),
          if (_launchMode == CodeLaunchMode.worktree) ...[
            const SizedBox(width: 6),
            _ToolbarDropdown<String?>(
              icon: Icons.account_tree_outlined,
              label: _loadingBranches
                  ? '加载分支…'
                  : (_baseBranch == null ? '基: 当前 HEAD' : '基: $_baseBranch'),
              value: _baseBranch,
              items: [
                (null, '基: 当前 HEAD'),
                for (final b in _branches) (b, '基: $b'),
              ],
              onSelected: (v) => setState(() => _baseBranch = v),
            ),
          ],
        ],
      ),
    );
  }

  String _permLabel(PermissionMode m) => switch (m) {
        PermissionMode.ask => 'Ask Permission',
        PermissionMode.autoEdit => 'Auto Edit',
        PermissionMode.fullAccess => 'Full Access',
      };
}

// ─── 工具条控件 ─────────────────────────────────────────────

/// 「+」附件菜单(图片 / 粘贴图片 / 粘贴文本)。
class _PlusMenu extends StatelessWidget {
  const _PlusMenu({
    required this.onPickImage,
    required this.onPasteImage,
    required this.onPasteText,
  });
  final VoidCallback onPickImage;
  final VoidCallback onPasteImage;
  final VoidCallback onPasteText;

  @override
  Widget build(BuildContext context) {
    return PopupMenuButton<int>(
      tooltip: '附件',
      position: PopupMenuPosition.over,
      onSelected: (v) => switch (v) {
        0 => onPickImage(),
        1 => onPasteImage(),
        _ => onPasteText(),
      },
      itemBuilder: (_) => const [
        PopupMenuItem(value: 0, height: 36, child: _MenuRow(Icons.image_outlined, '选择图片')),
        PopupMenuItem(value: 1, height: 36, child: _MenuRow(Icons.content_paste_rounded, '粘贴图片')),
        PopupMenuItem(value: 2, height: 36, child: _MenuRow(Icons.notes_rounded, '粘贴文本')),
      ],
      child: Container(
        width: 30,
        height: 30,
        decoration: BoxDecoration(
          borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
          border: Border.all(color: BiuTokens.borderSubtle),
        ),
        child: Icon(Icons.add_rounded, size: 17, color: BiuTokens.textSecondary),
      ),
    );
  }
}

class _MenuRow extends StatelessWidget {
  const _MenuRow(this.icon, this.label);
  final IconData icon;
  final String label;
  @override
  Widget build(BuildContext context) => Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          Icon(icon, size: 15, color: BiuTokens.textSecondary),
          const SizedBox(width: 8),
          Text(label, style: const TextStyle(fontSize: 13)),
        ],
      );
}

/// 紧凑下拉(icon + label + ▾),同工具条按钮样式。
class _ToolbarDropdown<T> extends StatelessWidget {
  const _ToolbarDropdown({
    required this.icon,
    required this.label,
    required this.value,
    required this.items,
    required this.onSelected,
  });
  final IconData icon;
  final String label;
  final T value;
  final List<(T, String)> items;
  final ValueChanged<T> onSelected;

  @override
  Widget build(BuildContext context) {
    return PopupMenuButton<T>(
      tooltip: '',
      position: PopupMenuPosition.under,
      onSelected: onSelected,
      itemBuilder: (_) => [
        for (final (v, lbl) in items)
          PopupMenuItem<T>(
            value: v,
            height: 36,
            child: Row(
              children: [
                if (v == value)
                  Icon(Icons.check_rounded, size: 14, color: BiuTokens.purple)
                else
                  const SizedBox(width: 14),
                const SizedBox(width: 8),
                Text(lbl, style: const TextStyle(fontSize: 13)),
              ],
            ),
          ),
      ],
      child: Container(
        height: 30,
        padding: const EdgeInsets.symmetric(horizontal: 9),
        decoration: BoxDecoration(
          borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
          border: Border.all(color: BiuTokens.borderSubtle),
        ),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(icon, size: 14, color: BiuTokens.textSecondary),
            const SizedBox(width: 6),
            Text(label,
                style: TextStyle(
                    fontSize: 12,
                    fontWeight: FontWeight.w500,
                    color: BiuTokens.textSecondary)),
            const SizedBox(width: 4),
            Icon(Icons.keyboard_arrow_down_rounded,
                size: 15, color: BiuTokens.textMuted),
          ],
        ),
      ),
    );
  }
}

/// 启动终端 / 发送 按钮(带 ⌘↵ 提示)。
class _SendButton extends StatelessWidget {
  const _SendButton({required this.label, required this.onTap});
  final String label;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return FilledButton(
      onPressed: onTap,
      style: FilledButton.styleFrom(
        backgroundColor: BiuTokens.purple,
        padding: const EdgeInsets.symmetric(horizontal: 14),
        minimumSize: const Size(0, 32),
        shape: RoundedRectangleBorder(
            borderRadius: BorderRadius.circular(BiuTokens.radiusSm)),
        tapTargetSize: MaterialTapTargetSize.shrinkWrap,
      ),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          Text(label,
              style: const TextStyle(fontSize: 12.5, fontWeight: FontWeight.w600)),
          const SizedBox(width: 8),
          const Icon(Icons.keyboard_command_key, size: 12),
          const Icon(Icons.keyboard_return_rounded, size: 13),
        ],
      ),
    );
  }
}

// ─── 附件预览 ───────────────────────────────────────────────

class _AttachmentChip extends StatelessWidget {
  const _AttachmentChip({required this.label, required this.onRemove});
  final String label;
  final VoidCallback onRemove;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.only(left: 8, right: 4, top: 4, bottom: 4),
      decoration: BoxDecoration(
        color: BiuTokens.purpleSoft,
        borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
      ),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          Icon(Icons.description_outlined, size: 12, color: BiuTokens.purple),
          const SizedBox(width: 4),
          Text(label,
              style: TextStyle(
                  fontSize: 11,
                  color: BiuTokens.purple,
                  fontWeight: FontWeight.w500)),
          const SizedBox(width: 2),
          InkWell(
            onTap: onRemove,
            borderRadius: BorderRadius.circular(BiuTokens.radiusXs),
            child: Icon(Icons.close_rounded, size: 13, color: BiuTokens.purple),
          ),
        ],
      ),
    );
  }
}

class _ImageThumb extends StatelessWidget {
  const _ImageThumb({required this.bytes, required this.onRemove});
  final Uint8List bytes;
  final VoidCallback onRemove;

  @override
  Widget build(BuildContext context) {
    return Stack(
      clipBehavior: Clip.none,
      children: [
        ClipRRect(
          borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
          child: Image.memory(bytes, width: 40, height: 40, fit: BoxFit.cover),
        ),
        Positioned(
          top: -6,
          right: -6,
          child: InkWell(
            onTap: onRemove,
            child: Container(
              decoration: BoxDecoration(
                color: BiuTokens.surface,
                shape: BoxShape.circle,
                border: Border.all(color: BiuTokens.borderSubtle),
              ),
              padding: const EdgeInsets.all(1),
              child: Icon(Icons.close_rounded,
                  size: 12, color: BiuTokens.textSecondary),
            ),
          ),
        ),
      ],
    );
  }
}

// ─── @mention 文件选择器(模态,键入 @ 唤起) ─────────────────

class _MentionPicker extends ConsumerStatefulWidget {
  const _MentionPicker({required this.root});
  final String root;

  @override
  ConsumerState<_MentionPicker> createState() => _MentionPickerState();
}

class _MentionPickerState extends ConsumerState<_MentionPicker> {
  final _ctrl = TextEditingController();
  final _focus = FocusNode();
  Timer? _debounce;
  List<FileSearchResult> _results = const [];
  bool _loading = false;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) => _focus.requestFocus());
  }

  @override
  void dispose() {
    _debounce?.cancel();
    _ctrl.dispose();
    _focus.dispose();
    super.dispose();
  }

  void _onChanged(String q) {
    _debounce?.cancel();
    _debounce = Timer(const Duration(milliseconds: 280), () => _search(q));
  }

  Future<void> _search(String q) async {
    final query = q.trim();
    if (query.isEmpty) {
      setState(() => _results = const []);
      return;
    }
    final bridge = ref.read(codeBridgeClientProvider);
    if (bridge == null) return;
    setState(() => _loading = true);
    try {
      final r = await bridge.fsSearch(widget.root, query, limit: 40);
      if (!mounted) return;
      setState(() {
        _results = r;
        _loading = false;
      });
    } catch (_) {
      if (mounted) setState(() => _loading = false);
    }
  }

  String _rel(String abs) => abs.startsWith('${widget.root}/')
      ? abs.substring(widget.root.length + 1)
      : abs;

  @override
  Widget build(BuildContext context) {
    return Dialog(
      backgroundColor: BiuTokens.surface,
      shape: RoundedRectangleBorder(
          borderRadius: BorderRadius.circular(BiuTokens.radiusLg)),
      child: ConstrainedBox(
        constraints: const BoxConstraints(maxWidth: 560, maxHeight: 420),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Padding(
              padding: const EdgeInsets.fromLTRB(16, 14, 16, 6),
              child: TextField(
                controller: _ctrl,
                focusNode: _focus,
                onChanged: _onChanged,
                style: const TextStyle(fontSize: 13),
                decoration: InputDecoration(
                  prefixIcon: Icon(Icons.alternate_email_rounded,
                      size: 16, color: BiuTokens.textMuted),
                  hintText: '引用文件 — 输入文件名',
                  isDense: true,
                  border: OutlineInputBorder(
                      borderRadius: BorderRadius.circular(BiuTokens.radiusMd)),
                ),
              ),
            ),
            Expanded(child: _body()),
          ],
        ),
      ),
    );
  }

  Widget _body() {
    if (_loading) {
      return const Center(child: CircularProgressIndicator(strokeWidth: 2));
    }
    if (_ctrl.text.trim().isEmpty) {
      return Center(
        child: Text('输入文件名,选中后以 @ 引用插入 prompt',
            style: TextStyle(fontSize: 12.5, color: BiuTokens.textMuted)),
      );
    }
    if (_results.isEmpty) {
      return Center(
        child: Text('无匹配文件',
            style: TextStyle(fontSize: 12.5, color: BiuTokens.textMuted)),
      );
    }
    return ListView.builder(
      itemCount: _results.length,
      itemBuilder: (ctx, i) {
        final r = _results[i];
        return InkWell(
          onTap: () => Navigator.of(context).pop(_rel(r.path)),
          child: Padding(
            padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 7),
            child: Row(
              children: [
                Icon(Icons.insert_drive_file_outlined,
                    size: 14, color: BiuTokens.textSecondary),
                const SizedBox(width: 8),
                Text(r.name,
                    style: TextStyle(
                        fontSize: 12.5,
                        fontWeight: FontWeight.w500,
                        color: BiuTokens.text)),
                const SizedBox(width: 8),
                Expanded(
                  child: Text(_rel(r.path),
                      overflow: TextOverflow.ellipsis,
                      style: TextStyle(
                          fontSize: 11,
                          fontFamily: 'SF Mono',
                          color: BiuTokens.textMuted)),
                ),
              ],
            ),
          ),
        );
      },
    );
  }
}

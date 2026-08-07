// ComposerV2 —— Chat 重构 R4 + UI Benchmark 多波加固。
//
// 当前能力：
//   * Enter 发送 / Shift+Enter 换行
//   * ↑ / ↓：浏览历史指令栈（输入框为空 或 处于浏览态时拦截；其它情况
//     交给 TextField 默认行为，不打断多行编辑）
//   * 外部通过 composerInjectProvider 注入文本（引用回复 / @ 提及 等）
//   * 发送时把 trim 后的原文 push 到 draft history
//   * 输入 `/` 弹出 SlashMenuV2（内置命令 + 技能统一列表）—— ↑↓ 选择 +
//     Enter 触发；'/' 后跟空格 / 参数时菜单让位
//   * 输入框右下显示 ~N tokens（启发式，不调远端）
//   * 附件：桌面 file_selector picker / desktop_drop 拖拽 / Pasteboard.image
//     粘贴; 手机 (isPhoneLayout) 走 image_picker 拍照/相册 + file_selector 文件
//     三选一 bottom sheet；输入框上方 chip 行（缩略 + 删除）；附件随消息一起
//     发到 BiuSession
//   * `/` 技能调用：slash 菜单里选中技能插入 `/<identifier> `（服务端按
//     文本约定识别，见 slash_menu.dart）
//
// R5+ 再扩联网 hint 开关。

import 'dart:async';

import 'package:desktop_drop/desktop_drop.dart';
import 'package:file_selector/file_selector.dart';
import 'package:flutter/foundation.dart' show compute;
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:image_picker/image_picker.dart';
import 'package:pasteboard/pasteboard.dart';
import 'package:uuid/uuid.dart';

import '../../../../app/theme/effects.dart';
import '../../../../app/theme/extensions.dart';
import '../../../../core/layout/form_factor.dart';
import '../../../../data/agent_plane/biu_daemon_manager.dart'
    show biuDaemonManagerProvider;
import '../../../../data/api/skill_client.dart' show Skill;
import '../../../../data/providers_providers.dart';
import '../../../../data/skill_providers.dart';
import '../../../../l10n/app_localizations.dart';
import '../../../../services/token_manager.dart'
    show ConnectivityState, connectivityStateProvider;
import '../../application/attachments_provider.dart';
import '../../application/chat_controller.dart';
import '../../application/composer_draft_store.dart';
import '../../application/draft_history_controller.dart';
import '../../application/web_search_provider.dart';
import '../../data/chat_image_compressor.dart';
import '../../domain/chat_models.dart'
    show AttachmentInput, AutoApproveMode, ThreadMode;
import '../../domain/slash_commands.dart';
import '../../domain/token_estimate.dart';
import 'attachment_chip.dart';
import 'estimate_chip.dart';
import 'model_picker_dialog.dart';
import 'slash_menu.dart';

class ComposerV2 extends ConsumerStatefulWidget {
  const ComposerV2({
    super.key,
    required this.onSend,
    required this.onCancel,
    required this.streaming,
    this.cancelling = false,
    this.enabled = true,
    this.onSlashCommand,
    this.threadId,
  });

  final void Function(String text, List<AttachmentInput> attachments) onSend;
  final VoidCallback onCancel;
  final bool streaming;

  /// 用户已按 stop, 等服务端 Done{interrupted} 落地的中间态。stop 按钮
  /// disable + 显示 spinner / "Stopping..." 提示,防止用户重复 spam 触
  /// 发多次 cancel frame 把 brain 端干扰花。
  final bool cancelling;
  final bool enabled;

  /// 处理 SlashCommand —— 由 ChatPageV2 注入。null 时所有命令转兜底（仅 /clear）。
  final void Function(SlashCommand cmd)? onSlashCommand;

  /// 当前 thread —— 非空时启用 per-thread 草稿（切 thread 自动恢复 / 保存）。
  /// null 时只在内存里保留草稿（NewThreadDialog 之前用）。
  final String? threadId;

  @override
  ConsumerState<ComposerV2> createState() => _ComposerV2State();
}

class _ComposerV2State extends ConsumerState<ComposerV2> {
  final _ctrl = TextEditingController();
  final _focus = FocusNode();

  /// P0-6: 我们自己往 controller 注入文本（history / inject）时记一次值，
  /// onTextChanged 看到相同值时不退出浏览模式。
  String? _lastNavValue;
  String _liveText = '';

  /// 斜杠菜单当前高亮项（commands + skills 合并计数）；showSlash=false 时无意义。
  int _slashCursor = 0;

  /// per-thread 草稿写入 debounce（500ms）。
  Timer? _draftSaveTimer;

  /// 已加载过草稿的 thread id，避免重复 load。
  String? _loadedDraftFor;

  @override
  void initState() {
    super.initState();
    _ctrl.addListener(_onTextChanged);
    // focus halo (prototype `.input:focus { box-shadow: 0 0 0 3px brand-soft }`)
    // 走外层 AnimatedContainer 的 boxShadow,build 阶段读 _focus.hasFocus,所以
    // focus 变更要触发 rebuild。
    _focus.addListener(_onFocusChanged);
    _maybeLoadDraft();
  }

  void _onFocusChanged() {
    if (mounted) setState(() {});
  }

  @override
  void didUpdateWidget(covariant ComposerV2 old) {
    super.didUpdateWidget(old);
    // 切到新 thread → flush 旧 thread 的当前文本 + 加载新 thread 草稿。
    if (old.threadId != widget.threadId) {
      _flushDraftSync(old.threadId, _ctrl.text);
      _ctrl.clear();
      _liveText = '';
      _loadedDraftFor = null;
      _maybeLoadDraft();
    }
  }

  @override
  void dispose() {
    _draftSaveTimer?.cancel();
    // 离开 page 时把当前文本 flush 到 prefs，下次回来还在。
    _flushDraftSync(widget.threadId, _ctrl.text);
    _ctrl.removeListener(_onTextChanged);
    _ctrl.dispose();
    _focus.removeListener(_onFocusChanged);
    _focus.dispose();
    super.dispose();
  }

  Future<void> _maybeLoadDraft() async {
    final tid = widget.threadId;
    if (tid == null) return;
    if (_loadedDraftFor == tid) return;
    final draft = await ComposerDraftStore.load(tid);
    if (!mounted || widget.threadId != tid) return;
    _loadedDraftFor = tid;
    if (draft.isEmpty) return;
    // 仅在用户尚未输入新内容时才覆盖（避免抢用户当前击键）。
    if (_ctrl.text.isEmpty) {
      _ctrl.value = TextEditingValue(
        text: draft,
        selection: TextSelection.collapsed(offset: draft.length),
      );
    }
  }

  void _scheduleDraftSave(String text) {
    final tid = widget.threadId;
    if (tid == null) return;
    _draftSaveTimer?.cancel();
    _draftSaveTimer = Timer(const Duration(milliseconds: 500), () {
      // 发送中文本会被清空 → 下面 flush 自然把 key remove。
      ComposerDraftStore.save(tid, text);
    });
  }

  void _flushDraftSync(String? tid, String text) {
    if (tid == null) return;
    _draftSaveTimer?.cancel();
    // unawaited —— 调用方不在乎结果（最佳努力）。
    ComposerDraftStore.save(tid, text);
  }

  void _onTextChanged() {
    final t = _ctrl.text;
    if (t == _liveText) return;
    setState(() {
      _liveText = t;
      // 文本变 → 重置 slash 高亮（保持在 0，避免上次留下的越界）。
      _slashCursor = 0;
    });
    _scheduleDraftSave(t);
    if (_lastNavValue != null && t == _lastNavValue) return;
    _lastNavValue = null;
    final cur = ref.read(draftHistoryProvider).cursor;
    if (cur != null) {
      ref.read(draftHistoryProvider.notifier).resetCursor();
    }
  }

  /// 斜杠菜单里选中技能：slash 模式下整个文本就是 `/<prefix>`，直接替换成
  /// `/<identifier> ` 含尾随空格。尾随空格让 parseSlash 出参 → 菜单让位。
  void _applySlashSkill(Skill skill) {
    final replacement = '/${skill.identifier} ';
    _ctrl.value = TextEditingValue(
      text: replacement,
      selection: TextSelection.collapsed(offset: replacement.length),
    );
    _focus.requestFocus();
  }

  void _applyHistoryText(String t) {
    _lastNavValue = t;
    _ctrl.value = TextEditingValue(
      text: t,
      selection: TextSelection.collapsed(offset: t.length),
    );
  }

  void _submit() {
    final text = _ctrl.text.trim();
    final tid = widget.threadId;
    final attachments = tid == null
        ? const <Attachment>[]
        : ref.read(composerAttachmentsProvider(tid));
    if (text.isEmpty && attachments.isEmpty) return;
    if (text.isNotEmpty) {
      ref.read(draftHistoryProvider.notifier).push(text);
    }
    final inputs = attachments
        .where((a) => a.status == AttachmentStatus.ready)
        .map((a) => AttachmentInput(mimeType: a.mime, bytes: a.bytes))
        .toList(growable: false);
    // 联网搜索 hint：开启时把 hint 前缀拼到原文；发送后一次性 clear。
    final webHintOn = ref.read(webSearchHintProvider);
    final finalText = applyWebSearchHint(text, webHintOn);
    widget.onSend(finalText, inputs);
    _ctrl.clear();
    if (webHintOn) {
      ref.read(webSearchHintProvider.notifier).clear();
    }
    if (tid != null) {
      ref.read(composerAttachmentsProvider(tid).notifier).clear();
      ComposerDraftStore.clear(tid);
    }
    _focus.requestFocus();
  }

  // ─── 附件拾取 / drop / paste ────────────────────────────────

  /// 从 file_selector 选图片。多端通用；mobile 上 file_selector 不可用时
  /// 失败 fail-silent（按钮 tooltip 已经预告）。
  Future<void> _pickImages() async {
    final tid = widget.threadId;
    if (tid == null) return;
    try {
      final files = await openFiles(
        acceptedTypeGroups: const [
          XTypeGroup(
            label: 'Images',
            extensions: ['png', 'jpg', 'jpeg', 'webp', 'gif', 'heic'],
            mimeTypes: ['image/png', 'image/jpeg', 'image/webp', 'image/gif'],
          ),
        ],
      );
      for (final f in files) {
        await _ingestXFile(tid, f);
      }
    } catch (e) {
      _toastError(e);
    }
  }

  /// 手机附件入口: 拍照 / 相册 / 文件 三选一 bottom sheet。
  /// image_picker 处理相机与相册 (权限 / HEIC 转换由插件负责);
  /// "文件" 沿用 file_selector (Android SAF 可用)。
  Future<void> _pickImagesPhone() async {
    final tid = widget.threadId;
    if (tid == null) return;
    final l = AppLocalizations.of(context)!;
    final action = await showModalBottomSheet<String>(
      context: context,
      showDragHandle: true,
      builder: (ctx) => SafeArea(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            ListTile(
              leading: const Icon(Icons.photo_camera_outlined),
              title: Text(l.chatV2ComposerAttachCamera),
              onTap: () => Navigator.of(ctx).pop('camera'),
            ),
            ListTile(
              leading: const Icon(Icons.photo_library_outlined),
              title: Text(l.chatV2ComposerAttachGallery),
              onTap: () => Navigator.of(ctx).pop('gallery'),
            ),
            ListTile(
              leading: const Icon(Icons.insert_drive_file_outlined),
              title: Text(l.chatV2ComposerAttachFile),
              onTap: () => Navigator.of(ctx).pop('file'),
            ),
          ],
        ),
      ),
    );
    if (action == null || !mounted) return;
    try {
      final picker = ImagePicker();
      switch (action) {
        case 'camera':
          final f = await picker.pickImage(source: ImageSource.camera);
          if (f != null) await _ingestXFile(tid, f);
        case 'gallery':
          final files = await picker.pickMultiImage();
          for (final f in files) {
            await _ingestXFile(tid, f);
          }
        case 'file':
          await _pickImages();
      }
    } catch (e) {
      _toastError(e);
    }
  }

  /// desktop_drop 拖拽落地 → 解析 image/* 并入队。
  Future<void> _onDrop(DropDoneDetails detail) async {
    final tid = widget.threadId;
    if (tid == null) return;
    for (final f in detail.files) {
      await _ingestXFile(tid, f);
    }
  }

  /// Cmd/Ctrl+V 粘贴：优先看剪贴板里有没有图片；有 → 入队 + 同时 pass
  /// 文本插入到光标处（截图工具同时塞两份是常态）。
  Future<void> _handlePasteShortcut() async {
    final tid = widget.threadId;
    Uint8List? imgBytes;
    try {
      imgBytes = await Pasteboard.image;
    } catch (_) {
      imgBytes = null;
    }
    if (tid != null && imgBytes != null && imgBytes.isNotEmpty) {
      await _ingestBytes(
        tid,
        bytes: imgBytes,
        name: 'pasted-${DateTime.now().millisecondsSinceEpoch}.png',
        mime: 'image/png',
      );
    }
    // 同时尝试粘贴文字（默认 V 行为被 Shortcuts 包吃掉了）。
    if (!mounted) return;
    final clip = await Clipboard.getData(Clipboard.kTextPlain);
    final text = clip?.text ?? '';
    if (text.isEmpty) return;
    final ctl = _ctrl;
    final sel = ctl.selection;
    final start = sel.isValid
        ? sel.start.clamp(0, ctl.text.length)
        : ctl.text.length;
    final end = sel.isValid
        ? sel.end.clamp(0, ctl.text.length)
        : ctl.text.length;
    final next = ctl.text.replaceRange(start, end, text);
    ctl.value = TextEditingValue(
      text: next,
      selection: TextSelection.collapsed(offset: start + text.length),
    );
  }

  Future<void> _ingestXFile(String threadId, XFile f) async {
    try {
      final bytes = await f.readAsBytes();
      final mime = (f.mimeType ?? _guessMimeFromName(f.name)) ?? '';
      if (!mime.startsWith('image/')) {
        if (!mounted) return;
        _toastError(
          AppLocalizations.of(context)!.chatV2ComposerErrAttachOnlyImage(f.name),
        );
        return;
      }
      await _ingestBytes(threadId, bytes: bytes, name: f.name, mime: mime);
    } catch (e) {
      _toastError(e);
    }
  }

  static const _uuid = Uuid();

  Future<void> _ingestBytes(
    String threadId, {
    required Uint8List bytes,
    required String name,
    required String mime,
  }) async {
    if (bytes.length > 10 * 1024 * 1024) {
      _toastError(AppLocalizations.of(context)!.chatV2ComposerErrAttachTooLarge);
      return;
    }
    // 压缩到链路友好尺寸（长边 ≤1568px / ≤1MB）：原图 base64 内联会撞
    // 网关 body 上限与厂商单图限制，且 Claude 反正会把 >1568px 的图
    // 降采样，发原图零收益。大图解码放 compute 防 UI 掉帧。
    final c = await compute(
      compressChatImageEntry,
      (bytes: bytes, name: name, mime: mime),
    );
    if (!mounted) return;
    ref
        .read(composerAttachmentsProvider(threadId).notifier)
        .add(Attachment(id: _uuid.v4(), name: c.name, mime: c.mime, bytes: c.bytes));
  }

  void _toastError(Object err) {
    if (!mounted) return;
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(
        content: Text(
          AppLocalizations.of(context)!.chatV2ComposerErrAttach('$err'),
        ),
      ),
    );
  }

  static String? _guessMimeFromName(String name) {
    final n = name.toLowerCase();
    if (n.endsWith('.png')) return 'image/png';
    if (n.endsWith('.jpg') || n.endsWith('.jpeg')) return 'image/jpeg';
    if (n.endsWith('.webp')) return 'image/webp';
    if (n.endsWith('.gif')) return 'image/gif';
    if (n.endsWith('.heic')) return 'image/heic';
    return null;
  }

  /// 斜杠命令派发。/clear 的副作用直接在 composer 处理（清输入），其它转给
  /// host page。
  void _runSlash(SlashCommand cmd) {
    if (cmd.id == 'clear') {
      _ctrl.clear();
      _focus.requestFocus();
      return;
    }
    if (cmd.id == 'help') {
      _showHelp();
      _ctrl.clear();
      return;
    }
    final h = widget.onSlashCommand;
    if (h != null) h(cmd);
    _ctrl.clear();
    _focus.requestFocus();
  }

  void _showHelp() {
    showDialog<void>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: Text(AppLocalizations.of(ctx)!.chatV2ComposerSlashDialogTitle),
        content: SizedBox(
          width: 320,
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              for (final c in kSlashCommands)
                ListTile(
                  leading: Icon(c.icon, size: 18),
                  title: Text(
                    c.label,
                    style: const TextStyle(fontFamily: 'monospace'),
                  ),
                  subtitle: Text(c.hint),
                  dense: true,
                ),
            ],
          ),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(ctx).pop(),
            child: Text(AppLocalizations.of(ctx)!.chatV2DialogOk),
          ),
        ],
      ),
    );
  }

  /// Focus.onKeyEvent 处理顺序：slash menu > history nav。
  /// 上层模式拦截后下层不处理，避免 ↑ 同时触发多件事。
  KeyEventResult _handleKey(FocusNode node, KeyEvent event) {
    if (event is! KeyDownEvent) return KeyEventResult.ignored;

    // 1) slash 模式 —— 内置命令 + 技能合并列表（commands 在前）。
    final parsed = parseSlash(_liveText);
    final showSlash =
        parsed != null && parsed.args.isEmpty && !widget.streaming;
    final cmdMatches =
        showSlash ? filterSlashCommands(parsed.name) : const <SlashCommand>[];
    final skillMatches =
        showSlash ? _filteredSkills(parsed.name) : const <Skill>[];
    final slashTotal = cmdMatches.length + skillMatches.length;

    if (showSlash && slashTotal > 0) {
      if (event.logicalKey == LogicalKeyboardKey.arrowUp) {
        setState(() {
          _slashCursor = (_slashCursor - 1).clamp(0, slashTotal - 1);
        });
        return KeyEventResult.handled;
      }
      if (event.logicalKey == LogicalKeyboardKey.arrowDown) {
        setState(() {
          _slashCursor = (_slashCursor + 1).clamp(0, slashTotal - 1);
        });
        return KeyEventResult.handled;
      }
      if (event.logicalKey == LogicalKeyboardKey.enter &&
          !HardwareKeyboard.instance.isShiftPressed) {
        final i = _slashCursor.clamp(0, slashTotal - 1);
        if (i < cmdMatches.length) {
          _runSlash(cmdMatches[i]);
        } else {
          _applySlashSkill(skillMatches[i - cmdMatches.length]);
        }
        return KeyEventResult.handled;
      }
      if (event.logicalKey == LogicalKeyboardKey.escape) {
        _ctrl.clear();
        return KeyEventResult.handled;
      }
      // 在 slash 模式下不走 history nav，免得 ↑ 同时触发两件事。
      return KeyEventResult.ignored;
    }

    // 2) 历史指令导航（输入框为空 或 浏览态时拦截）。
    final st = ref.read(draftHistoryProvider);
    final inBrowse = st.cursor != null;
    final empty = _ctrl.text.trim().isEmpty;
    if (event.logicalKey == LogicalKeyboardKey.arrowUp) {
      if (!(inBrowse || empty)) return KeyEventResult.ignored;
      final prev = ref.read(draftHistoryProvider.notifier).prev();
      if (prev != null) _applyHistoryText(prev);
      return KeyEventResult.handled;
    }
    if (event.logicalKey == LogicalKeyboardKey.arrowDown) {
      if (!inBrowse) return KeyEventResult.ignored;
      final n = ref.read(draftHistoryProvider.notifier).next();
      if (n != null) _applyHistoryText(n);
      return KeyEventResult.handled;
    }
    return KeyEventResult.ignored;
  }

  /// 按前缀过滤 skills（大小写不敏感 startsWith）。
  List<Skill> _filteredSkills(String prefix) {
    final list = ref.read(skillsListProvider).valueOrNull ?? const <Skill>[];
    if (prefix.isEmpty) return list.take(8).toList(growable: false);
    final lower = prefix.toLowerCase();
    return list
        .where((s) => s.identifier.toLowerCase().startsWith(lower))
        .take(8)
        .toList(growable: false);
  }

  void _applyInject(String injected) {
    final ctl = _ctrl;
    final sel = ctl.selection;
    final start = sel.isValid
        ? sel.start.clamp(0, ctl.text.length)
        : ctl.text.length;
    final end = sel.isValid
        ? sel.end.clamp(0, ctl.text.length)
        : ctl.text.length;
    final needsLeadingNewline = start > 0 && ctl.text[start - 1] != '\n';
    final inserted = needsLeadingNewline ? '\n$injected' : injected;
    final next = ctl.text.replaceRange(start, end, inserted);
    _lastNavValue = null;
    ctl.value = TextEditingValue(
      text: next,
      selection: TextSelection.collapsed(offset: start + inserted.length),
    );
    _focus.requestFocus();
  }

  @override
  Widget build(BuildContext context) {
    ref.listen<String?>(composerInjectProvider, (prev, cur) {
      if (cur == null || cur.isEmpty) return;
      _applyInject(cur);
      ref.read(composerInjectProvider.notifier).consume();
    });

    final theme = Theme.of(context);
    final l = AppLocalizations.of(context)!;
    final parsed = parseSlash(_liveText);
    final showSlash =
        parsed != null &&
        parsed.args.isEmpty &&
        !widget.streaming &&
        _liveText.startsWith('/');
    final slashCommands = showSlash
        ? filterSlashCommands(parsed.name)
        : const <SlashCommand>[];
    final tokens = estimateTokens(_liveText);
    // watch skills 让 list ready 后 slash 菜单能即时弹出技能段。
    ref.watch(skillsListProvider);
    final slashSkills =
        showSlash ? _filteredSkills(parsed.name) : const <Skill>[];
    final slashTotal = slashCommands.length + slashSkills.length;
    final tid = widget.threadId;
    final attachments = tid == null
        ? const <Attachment>[]
        : ref.watch(composerAttachmentsProvider(tid));
    // B1 离线 grace: offlineWithCache 状态下 access token 已过期 + refresh
    // 失败,业务请求 100% 401。disable send / pick image,挂出明确提示,
    // 避免用户白点没反应。
    final connectivity = ref.watch(connectivityStateProvider);
    final isOffline = connectivity == ConnectivityState.offlineWithCache;
    final canSend = widget.enabled && !isOffline;
    // 当前 thread 选中模型是否支持 vision。null = 未匹配上(走 BiuMind 默
    // 认 fallback,默认放行 — 防止 picker 没拉到时禁用按钮)。
    final modelVisionState = tid == null
        ? _ModelVisionState.unknown
        : _resolveModelVision(ref, tid);
    final visionAllowed = modelVisionState != _ModelVisionState.notSupported;

    // 卡片化 composer (WorkBuddy 风): 悬浮圆角卡 + 无边框多行输入 +
    // 卡内底部选项条 (左: 附件/联网/mode/workdir/auto-approve; 右: 估算/
    // 字数/模型/发送)。焦点态卡片边框换 brand + 外圈 halo。
    final brand =
        theme.extension<BiuColors>()?.brand ?? theme.colorScheme.primary;
    final cs = theme.colorScheme;
    final card = AnimatedContainer(
      duration: const Duration(milliseconds: 160),
      curve: Curves.easeOutCubic,
      decoration: BoxDecoration(
        // surfaceContainerLowest (纯白/深色下的最高层) — 跟聊天区底色
        // (bgApp) 拉开层级, 卡片"浮"在页面上。
        color: cs.surfaceContainerLowest,
        borderRadius: BorderRadius.circular(18),
        border: Border.all(
          color: _focus.hasFocus ? brand : cs.outlineVariant,
          width: _focus.hasFocus ? 1.5 : 1,
        ),
        boxShadow: _focus.hasFocus
            ? focusHalo(brand)
            : [
                BoxShadow(
                  color: Colors.black.withValues(alpha: 0.06),
                  blurRadius: 16,
                  offset: const Offset(0, 3),
                ),
              ],
      ),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          if (attachments.isNotEmpty)
            Padding(
              padding: const EdgeInsets.fromLTRB(12, 10, 12, 0),
              child: Wrap(
                spacing: 6,
                runSpacing: 6,
                children: [
                  for (final a in attachments)
                    AttachmentChipV2(
                      attachment: a,
                      onRemove: () => ref
                          .read(composerAttachmentsProvider(tid!).notifier)
                          .remove(a.id),
                    ),
                ],
              ),
            ),
          Shortcuts(
            shortcuts: <ShortcutActivator, Intent>{
              LogicalKeySet(LogicalKeyboardKey.enter): const _SendIntent(),
              LogicalKeySet(
                LogicalKeyboardKey.shift,
                LogicalKeyboardKey.enter,
              ): const DoNothingAndStopPropagationIntent(),
            },
            child: Actions(
              actions: <Type, Action<Intent>>{
                _SendIntent: CallbackAction<_SendIntent>(
                  onInvoke: (_) {
                    // slash 模式下 Enter 由 Focus.onKeyEvent 拦截先行；这里
                    // 走到说明非 slash 模式，正常发送。
                    // slash 模式下 Enter 已被 Focus.onKeyEvent 拦截走完
                    // _runSlash / _applySlashSkill；这里二次防御 noop。
                    if (showSlash && slashTotal > 0) return null;
                    _submit();
                    return null;
                  },
                ),
              },
              child: Focus(
                onKeyEvent: _handleKey,
                child: TextField(
                  controller: _ctrl,
                  focusNode: _focus,
                  enabled: widget.enabled,
                  minLines: 2,
                  maxLines: 8,
                  // 移动软键盘: 回车 = 换行, 发送走右下按钮 (硬件键盘
                  // 的 Enter 发送走上面 Shortcuts, 不受影响)。
                  textInputAction: TextInputAction.newline,
                  // 字号跟随全局 (设置 > 外观 > 字体大小) 的 bodyLarge,
                  // 行高压到 1.5 让多行输入不局促。
                  style: theme.textTheme.bodyLarge?.copyWith(height: 1.5),
                  decoration: InputDecoration(
                    hintText: widget.cancelling
                        ? l.chatV2ComposerStopping
                        : widget.streaming
                        ? l.chatV2ComposerHintStreaming
                        : l.chatV2ComposerHint,
                    hintStyle: theme.textTheme.bodyLarge?.copyWith(
                      height: 1.5,
                      color: cs.onSurfaceVariant.withValues(alpha: 0.55),
                    ),
                    // 完全透明 + 无边框 — 视觉边界/焦点态由外层卡片承担。
                    // 全局 inputDecorationTheme 是 filled + OutlineInputBorder
                    // (表单用), 这里必须逐项显式关掉, 否则输入区会多出一个
                    // 灰底内框, hover 时还会再叠一层深色 "黑边"。
                    filled: false,
                    fillColor: Colors.transparent,
                    hoverColor: Colors.transparent,
                    border: InputBorder.none,
                    enabledBorder: InputBorder.none,
                    focusedBorder: InputBorder.none,
                    errorBorder: InputBorder.none,
                    focusedErrorBorder: InputBorder.none,
                    disabledBorder: InputBorder.none,
                    contentPadding: const EdgeInsets.fromLTRB(16, 12, 16, 6),
                  ),
                ),
              ),
            ),
          ),
          // 卡内底部选项条 — 左操作组 (附件/联网/模式 chips) + 右状态组
          // (估算/字数/模型/发送)。图标统一 18px、命中区 32px, chip 统一
          // 12px/w500 (见 _ChipShell), 避免大小混排造成的"不齐"感。
          Padding(
            padding: const EdgeInsets.fromLTRB(6, 2, 6, 6),
            child: Row(
              children: [
                IconButton(
                  onPressed:
                      tid == null ||
                          widget.streaming ||
                          isOffline ||
                          !visionAllowed
                      ? null
                      : (isPhoneLayout(context)
                          ? _pickImagesPhone
                          : _pickImages),
                  icon: const Icon(Icons.add, size: 20),
                  color: cs.onSurfaceVariant,
                  padding: EdgeInsets.zero,
                  constraints: const BoxConstraints(
                    minWidth: 32,
                    minHeight: 32,
                  ),
                  style: IconButton.styleFrom(
                    tapTargetSize: MaterialTapTargetSize.shrinkWrap,
                  ),
                  tooltip: isOffline
                      ? '离线中,等待网络恢复'
                      : tid == null
                      ? l.chatV2ComposerAttachNeedThread
                      : !visionAllowed
                      ? l.chatV2ComposerAttachNoVision
                      : l.chatV2ComposerAttachTooltip,
                ),
                _WebSearchToggle(streaming: widget.streaming),
                if (tid != null)
                  Expanded(
                    child: SingleChildScrollView(
                      scrollDirection: Axis.horizontal,
                      child: Row(
                        mainAxisSize: MainAxisSize.min,
                        children: [
                          _ModeChipInline(threadId: tid),
                          // 仅 Agent 模式时显示 workdir + auto-approve 两个
                          // chip。chat 模式下没意义,留白让 row 不挤。
                          Consumer(
                            builder: (_, r, child) {
                              final th = r.watch(threadProvider(tid));
                              if (th.value?.mode != ThreadMode.agent) {
                                return const SizedBox.shrink();
                              }
                              return Row(
                                mainAxisSize: MainAxisSize.min,
                                children: [
                                  const SizedBox(width: 4),
                                  _WorkdirChipInline(threadId: tid),
                                  const SizedBox(width: 4),
                                  _AutoApproveChipInline(threadId: tid),
                                ],
                              );
                            },
                          ),
                        ],
                      ),
                    ),
                  )
                else
                  const Spacer(),
                Row(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    if (tid != null) ...[
                      if (_liveText.length >= 20)
                        Consumer(
                          builder: (_, r, child) {
                            final th = r.watch(threadProvider(tid));
                            final m = th.value?.model ?? 'biumind-default';
                            return Padding(
                              padding: const EdgeInsets.only(right: 8),
                              child: EstimateChip(model: m, content: _liveText),
                            );
                          },
                        ),
                      if (_liveText.isNotEmpty)
                        Padding(
                          padding: const EdgeInsets.only(right: 6),
                          child: Text(
                            l.chatV2ComposerCharTokens(
                              _liveText.length,
                              tokens,
                            ),
                            // 11px 等宽数字 + 60% 弱化 — 纯辅助信息,
                            // 不跟 chips (12px/w500) 抢视觉层级。
                            style: TextStyle(
                              fontSize: 11,
                              height: 1.2,
                              color: cs.onSurfaceVariant.withValues(
                                alpha: 0.6,
                              ),
                              fontFeatures: const [
                                FontFeature.tabularFigures(),
                              ],
                            ),
                          ),
                        ),
                      _ModelInlinePicker(threadId: tid),
                      const SizedBox(width: 6),
                    ],
                    if (widget.streaming)
                      // cancelling 中间态:按钮 disable,图标换 spinner 提示
                      // 「已经在停止」。再点一次没意义 —— BiuSessionConnection
                      // .cancel 内部 _cancelling 守护也会吞掉重复帧,UI 这层先
                      // short-circuit 减少抖动。
                      widget.cancelling
                          ? IconButton.filled(
                              onPressed: null,
                              icon: const SizedBox(
                                width: 16,
                                height: 16,
                                child: CircularProgressIndicator(
                                  strokeWidth: 2,
                                  valueColor: AlwaysStoppedAnimation<Color>(
                                    Colors.white,
                                  ),
                                ),
                              ),
                              style: _sendButtonStyle(),
                              tooltip: l.chatV2ComposerStoppingTooltip,
                            )
                          : IconButton.filled(
                              onPressed: widget.onCancel,
                              icon: const Icon(Icons.stop, size: 18),
                              style: _sendButtonStyle(),
                              tooltip: l.chatV2ComposerCancelTooltip,
                            )
                    else
                      IconButton.filled(
                        onPressed: canSend ? _submit : null,
                        icon: const Icon(Icons.arrow_upward, size: 18),
                        style: _sendButtonStyle(),
                        tooltip: isOffline
                            ? '离线中,等待网络恢复'
                            : l.chatV2ComposerSendTooltip,
                      ),
                  ],
                ),
              ],
            ),
          ),
        ],
      ),
    );

    final phone = isPhoneLayout(context);
    final inner = Padding(
      padding: EdgeInsets.fromLTRB(phone ? 12 : 24, 4, phone ? 12 : 24, 10),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          if (showSlash && slashTotal > 0) ...[
            SlashMenuV2(
              commands: slashCommands,
              skills: slashSkills,
              selectedIndex: _slashCursor.clamp(0, slashTotal - 1),
              onPickCommand: _runSlash,
              onPickSkill: _applySlashSkill,
            ),
            const SizedBox(height: 6),
          ],
          Center(
            child: ConstrainedBox(
              constraints: const BoxConstraints(maxWidth: 860),
              child: SizedBox(width: double.infinity, child: card),
            ),
          ),
          Padding(
            padding: const EdgeInsets.only(top: 8, bottom: 2),
            child: Text(
              l.chatV2ComposerDisclaimer,
              textAlign: TextAlign.center,
              // 脚注层级: 11px / 55% 弱化 / 微字距 — 可读但明确退到
              // 背景, 不与卡内 12px chips / 正文竞争。
              style: TextStyle(
                fontSize: 11,
                height: 1.4,
                letterSpacing: 0.1,
                color: cs.onSurfaceVariant.withValues(alpha: 0.55),
              ),
            ),
          ),
        ],
      ),
    );

    // 输入框字号跟随全局字号(设置 > 外观 > 字体大小,经 theme 生效),不再叠加
    // 聊天专属 textScaler。
    final scaled = inner;

    // tid==null 时（NewThreadDialog 之前）不开启拖拽 / 粘贴附件路径。
    if (tid == null) return scaled;
    // 桌面拖入图片走 desktop_drop；Cmd/Ctrl+V 优先吃图（粘文本由 paste
    // shortcut 自己复刻）。
    return DropTarget(
      onDragDone: _onDrop,
      child: CallbackShortcuts(
        bindings: {
          const SingleActivator(LogicalKeyboardKey.keyV, meta: true):
              _handlePasteShortcut,
          const SingleActivator(LogicalKeyboardKey.keyV, control: true):
              _handlePasteShortcut,
          // P0-补：Cmd/Ctrl+/ 直接弹斜杠菜单 —— 输入框聚焦时把 "/" 插到光标
          // 处，触发 slash 菜单显示。已经有 "/" 时不重复插。
          const SingleActivator(LogicalKeyboardKey.slash, meta: true):
              _toggleSlashMenu,
          const SingleActivator(LogicalKeyboardKey.slash, control: true):
              _toggleSlashMenu,
        },
        child: scaled,
      ),
    );
  }

  /// Cmd/Ctrl+/ 行为：输入框为空 → 直接插 `/`；非空 → 不打扰。
  void _toggleSlashMenu() {
    if (_ctrl.text.isEmpty) {
      _ctrl.value = const TextEditingValue(
        text: '/',
        selection: TextSelection.collapsed(offset: 1),
      );
      _focus.requestFocus();
    }
  }
}

class _SendIntent extends Intent {
  const _SendIntent();
}

/// 发送/停止按钮统一外观 — 34px 正圆、brand 实心、禁用态自动弱化。
/// 圆钮是各家 chat composer 的收敛形态 (WorkBuddy / ChatGPT / Claude)。
ButtonStyle _sendButtonStyle() {
  return IconButton.styleFrom(
    shape: const CircleBorder(),
    padding: EdgeInsets.zero,
    minimumSize: const Size(34, 34),
    fixedSize: const Size(34, 34),
    tapTargetSize: MaterialTapTargetSize.shrinkWrap,
  );
}

/// 当前 thread 选中模型对 vision 的支持状态。
enum _ModelVisionState {
  /// 模型已知支持 vision (capabilities.vision=true),attach 启用。
  supported,

  /// 模型已知不支持 (vision=false),attach 禁用 + 提示切模型。
  notSupported,

  /// 还在加载 / 未匹配(走 BiuMind 默认 fallback、模型 picker 还没 ready),
  /// 默认放行,避免误禁用 — 真送了图非 vision 模型,上游会 400 提示。
  unknown,
}

/// 解析当前 thread 的 (providerId, modelId) 是不是 vision 模型。
/// providersListProvider / modelsListProvider 还在 loading 时返回 unknown,
/// 都加载完但找不到匹配条目同样 unknown(可能是远端 catalog 同步延迟)。
_ModelVisionState _resolveModelVision(WidgetRef ref, String threadId) {
  final th = ref.watch(threadProvider(threadId)).valueOrNull;
  if (th == null) return _ModelVisionState.unknown;
  final code = th.model;
  final providerId = th.providerId;
  if (code == null || code.isEmpty) return _ModelVisionState.unknown;
  final providers = ref.watch(providersListProvider).valueOrNull;
  if (providers == null) return _ModelVisionState.unknown;
  for (final p in providers) {
    if (providerId != null && p.providerId != providerId) continue;
    final models = ref.watch(modelsListProvider(p.id)).valueOrNull;
    if (models == null) continue;
    for (final m in models) {
      if (m.modelId == code) {
        return m.hasVision
            ? _ModelVisionState.supported
            : _ModelVisionState.notSupported;
      }
    }
  }
  return _ModelVisionState.unknown;
}

/// Composer 底部内联模型 chip —— 走统一的 ModelPickerDialogV2。
/// 显示当前 thread.model 的 displayName(从 providersListProvider /
/// modelsListProvider 解析,非 admin 视角);选中后一并 set
/// thread.model + thread.providerId。
class _ModelInlinePicker extends ConsumerWidget {
  const _ModelInlinePicker({required this.threadId});
  final String threadId;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final l = AppLocalizations.of(context)!;
    final threadAsync = ref.watch(threadProvider(threadId));
    final t = threadAsync.value;
    final current = t?.model;
    final currentProviderId = t?.providerId;
    return Tooltip(
      message: l.chatV2ComposerModelSwitchTooltip,
      child: InkWell(
        borderRadius: BorderRadius.circular(8),
        onTap: () async {
          final picked = await showModelPickerDialog(
            context,
            currentModel: current,
            currentProviderId: currentProviderId,
          );
          if (picked == null) return;
          await ref
              .read(chatControllerDepsProvider)
              .repo
              .setThreadModel(
                threadId,
                picked.modelCode,
                providerId: picked.providerId,
              );
        },
        // 复用 _ChipShell — 与 mode/workdir/auto-approve chips 同规格,
        // 整条工具栏一套字体节奏。
        child: _ChipShell(
          icon: Icons.psychology_outlined,
          label: _displayName(ref, current, currentProviderId, l),
        ),
      ),
    );
  }

  /// 解析当前 thread.model 的 displayName。null/未匹配 → "BiuMind 默认" / code。
  String _displayName(
    WidgetRef ref,
    String? code,
    String? providerId,
    AppLocalizations l,
  ) {
    if (code == null) return l.chatV2ComposerModelDefault;
    final providers = ref.watch(providersListProvider).valueOrNull ?? const [];
    for (final p in providers) {
      if (providerId != null && p.providerId != providerId) continue;
      final models =
          ref.watch(modelsListProvider(p.id)).valueOrNull ?? const [];
      for (final m in models) {
        if (m.modelId == code) {
          return m.displayName.isEmpty ? m.modelId : m.displayName;
        }
      }
    }
    return code;
  }
}

/// 联网搜索一次性开关 —— 地球图标，开启时变 primary 色 + 显 SnackBar。
/// 发送后 ComposerV2._submit 自动 clear。
class _WebSearchToggle extends ConsumerWidget {
  const _WebSearchToggle({required this.streaming});
  final bool streaming;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final theme = Theme.of(context);
    final l = AppLocalizations.of(context)!;
    final on = ref.watch(webSearchHintProvider);
    return IconButton(
      onPressed: streaming
          ? null
          : () {
              ref.read(webSearchHintProvider.notifier).toggle();
              if (!on) {
                ScaffoldMessenger.of(context).showSnackBar(
                  SnackBar(
                    content: Text(l.chatV2ComposerWebSnack),
                    duration: const Duration(seconds: 2),
                    behavior: SnackBarBehavior.floating,
                  ),
                );
              }
            },
      icon: Icon(
        Icons.public,
        size: 18,
        color: on
            ? theme.colorScheme.primary
            : theme.colorScheme.onSurfaceVariant,
      ),
      padding: EdgeInsets.zero,
      constraints: const BoxConstraints(minWidth: 32, minHeight: 32),
      style: IconButton.styleFrom(
        tapTargetSize: MaterialTapTargetSize.shrinkWrap,
      ),
      tooltip: on ? l.chatV2ComposerWebOn : l.chatV2ComposerWebOff,
    );
  }
}

/// 对话/智能 行内切换 —— lobehub 同款 chip。点击弹 popover 选「对话」or
/// 「智能」。切到「智能」时如果当前 thread 没绑 daemon,自动从
/// agentEnvironmentsProvider 拾取第一台 online biu_daemon。
class _ModeChipInline extends ConsumerWidget {
  const _ModeChipInline({required this.threadId});
  final String threadId;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final theme = Theme.of(context);
    final l = AppLocalizations.of(context)!;
    final th = ref.watch(threadProvider(threadId));
    final mode = th.value?.mode ?? ThreadMode.chat;
    final envsAsync = ref.watch(agentEnvironmentsProvider);
    final hasOnlineDaemon =
        envsAsync.valueOrNull?.any(
          (e) => e.workerKind == 'biu_daemon' && e.isOnline,
        ) ??
        false;
    final isAgent = mode == ThreadMode.agent;
    final label = isAgent
        ? l.chatV2ComposerModeAgent
        : l.chatV2ComposerModeChat;
    final icon = isAgent ? Icons.smart_toy_outlined : Icons.chat_bubble_outline;
    final color = isAgent ? theme.colorScheme.primary : null;

    return PopupMenuButton<ThreadMode>(
      tooltip: isAgent
          ? l.chatV2ComposerModeAgentHint
          : l.chatV2ComposerModeChatHint,
      // lobehub 同款 popover 外观:大圆角 + 轻投影 + 菜单四周留白,配合
      // 下面 _ModeMenuItem 的圆形图标 + 主副标题 + 选中整行高亮。
      color: theme.colorScheme.surface,
      surfaceTintColor: Colors.transparent,
      elevation: 8,
      shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(14)),
      menuPadding: const EdgeInsets.all(6),
      // chip 在底部工具栏,默认 over 从锚点向下覆盖会压住下方选项。菜单是
      // 固定两项(每项 ~52 + 上下留白 6,总高 ~118),向上偏移 -128 让菜单
      // 底缘紧贴 chip 顶上方一点(lobehub 同款:浮层弹在 chip 正上方,点完
      // 即消失,不刻意越过输入行)。两项内容固定故高度稳定,此经验值可靠。
      position: PopupMenuPosition.over,
      offset: const Offset(0, -128),
      onSelected: (m) async {
        final repo = ref.read(chatControllerDepsProvider).repo;
        if (m == ThreadMode.chat) {
          await repo.setThreadMode(threadId, ThreadMode.chat);
          return;
        }
        // m == agent: 切之前强制 refresh 一次 environments,避免 autoDispose
        // 缓存的过期列表里的 daemon 在 brain 已经 GC 掉了导致后续 createSession
        // 404 environment_not_found。
        ref.invalidate(agentEnvironmentsProvider);
        final envs = await ref.read(agentEnvironmentsProvider.future);
        final online = envs
            .where((e) => e.workerKind == 'biu_daemon' && e.isOnline)
            .toList();
        if (online.isEmpty) {
          if (context.mounted) {
            ScaffoldMessenger.of(context).showSnackBar(
              SnackBar(
                content: Text(l.chatV2ComposerModeNoDaemon),
                duration: const Duration(seconds: 2),
              ),
            );
          }
          return;
        }
        await repo.setThreadMode(
          threadId,
          ThreadMode.agent,
          environmentId: online.first.environmentId,
        );
      },
      itemBuilder: (_) => [
        PopupMenuItem(
          value: ThreadMode.chat,
          padding: EdgeInsets.zero,
          child: _ModeMenuItem(
            icon: Icons.chat_bubble_outline,
            title: l.chatV2ComposerModeChat,
            subtitle: l.chatV2ComposerModeChatHint,
            selected: mode == ThreadMode.chat,
          ),
        ),
        PopupMenuItem(
          value: ThreadMode.agent,
          padding: EdgeInsets.zero,
          // 不再 disable —— hasOnlineDaemon 仅作为 hint 渲染。点击触发的
          // onSelected 已经强 invalidate + 再读 envs (line ~1057),即使缓存
          // 说"无 daemon"实际新启动的 daemon 也能在那一刻被检测到。
          // disable 反而会卡住"daemon 刚启动 client 缓存还没过期"的窗口。
          child: _ModeMenuItem(
            icon: Icons.smart_toy_outlined,
            title: l.chatV2ComposerModeAgent,
            subtitle: hasOnlineDaemon
                ? l.chatV2ComposerModeAgentHint
                : l.chatV2ComposerModeNoDaemon,
            selected: mode == ThreadMode.agent,
            dimmed: !hasOnlineDaemon,
          ),
        ),
      ],
      child: _ChipShell(icon: icon, label: label, color: color),
    );
  }
}

/// 模式 popover 单项 —— lobehub 同款:左侧圆形图标容器 + 右侧主标题/副标题
/// 两行,选中项整行浅色高亮背景(不再用左侧勾)。[dimmed] 用于「智能」在
/// 无在线 daemon 时把图标/副标题压暗作软提示(不禁用,仍可点)。
class _ModeMenuItem extends StatelessWidget {
  const _ModeMenuItem({
    required this.icon,
    required this.title,
    required this.subtitle,
    required this.selected,
    this.dimmed = false,
  });
  final IconData icon;
  final String title;
  final String subtitle;
  final bool selected;
  final bool dimmed;

  @override
  Widget build(BuildContext context) {
    final cs = Theme.of(context).colorScheme;
    final iconFg = selected
        ? cs.primary
        : (dimmed
              ? cs.onSurfaceVariant.withValues(alpha: 0.6)
              : cs.onSurfaceVariant);
    final iconBg = selected ? cs.primaryContainer : cs.surfaceContainerHighest;
    return Container(
      width: 272,
      padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 9),
      decoration: BoxDecoration(
        color: selected ? cs.primaryContainer.withValues(alpha: 0.45) : null,
        borderRadius: BorderRadius.circular(10),
      ),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.center,
        children: [
          Container(
            width: 34,
            height: 34,
            alignment: Alignment.center,
            decoration: BoxDecoration(color: iconBg, shape: BoxShape.circle),
            child: Icon(icon, size: 18, color: iconFg),
          ),
          const SizedBox(width: 11),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              mainAxisSize: MainAxisSize.min,
              children: [
                Text(
                  title,
                  style: TextStyle(
                    fontSize: 13.5,
                    fontWeight: FontWeight.w600,
                    color: selected ? cs.primary : cs.onSurface,
                  ),
                ),
                const SizedBox(height: 2),
                Text(
                  subtitle,
                  maxLines: 2,
                  overflow: TextOverflow.ellipsis,
                  style: TextStyle(
                    fontSize: 11.5,
                    height: 1.25,
                    color: cs.onSurfaceVariant,
                  ),
                ),
              ],
            ),
          ),
        ],
      ),
    );
  }
}

/// 工作目录 chip —— 仅 Agent 模式显示。点击弹 file_selector 选目录;
/// 已设置 → 显示路径末尾;长按清空。
class _WorkdirChipInline extends ConsumerWidget {
  const _WorkdirChipInline({required this.threadId});
  final String threadId;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final l = AppLocalizations.of(context)!;
    final th = ref.watch(threadProvider(threadId));
    final workdir = th.value?.workdir;
    final hasDir = workdir != null && workdir.isNotEmpty;
    final shortLabel = hasDir
        ? _tailPath(workdir)
        : l.chatV2ComposerWorkdirNone;

    return GestureDetector(
      onLongPress: hasDir
          ? () async {
              await ref
                  .read(chatControllerDepsProvider)
                  .repo
                  .setThreadWorkdir(threadId, null);
            }
          : null,
      child: Tooltip(
        message: hasDir
            ? '$workdir\n(${l.chatV2ComposerWorkdirClear})'
            : l.chatV2ComposerWorkdirSet,
        child: InkWell(
          borderRadius: BorderRadius.circular(12),
          onTap: () async {
            final picked = await getDirectoryPath();
            if (picked == null || !context.mounted) return;
            // 选目录即显式授权 —— 把它加入本机 daemon 的允许根(D7 安全地板),
            // 否则 daemon 只信任启动 cwd,选 Downloads 等目录会被拒
            // (work.Workdir outside allowed roots)。daemon 在跑则重启带上新根。
            await ref.read(biuDaemonManagerProvider)?.ensureRootTrusted(picked);
            if (!context.mounted) return;
            await ref
                .read(chatControllerDepsProvider)
                .repo
                .setThreadWorkdir(threadId, picked);
          },
          child: _ChipShell(icon: Icons.folder_outlined, label: shortLabel),
        ),
      ),
    );
  }

  static String _tailPath(String p) {
    // /Users/me/projects/biumind → biumind;过长截断到 18 字符
    final parts = p.split(RegExp(r'[\\/]'));
    final tail = parts.where((s) => s.isNotEmpty).toList();
    final last = tail.isEmpty ? p : tail.last;
    return last.length > 18 ? '…${last.substring(last.length - 17)}' : last;
  }
}

/// 自动批准 chip —— 三档 popover (auto / whitelist / manual)。
class _AutoApproveChipInline extends ConsumerWidget {
  const _AutoApproveChipInline({required this.threadId});
  final String threadId;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final theme = Theme.of(context);
    final l = AppLocalizations.of(context)!;
    final th = ref.watch(threadProvider(threadId));
    final m = th.value?.autoApprove ?? AutoApproveMode.manual;
    final label = switch (m) {
      AutoApproveMode.auto => l.chatV2ComposerAutoApproveAuto,
      AutoApproveMode.whitelist => l.chatV2ComposerAutoApproveWhitelist,
      AutoApproveMode.manual => l.chatV2ComposerAutoApproveManual,
    };
    final icon = switch (m) {
      AutoApproveMode.auto => Icons.flash_on,
      AutoApproveMode.whitelist => Icons.playlist_add_check,
      AutoApproveMode.manual => Icons.pan_tool_outlined,
    };
    final color = m == AutoApproveMode.auto
        ? theme.colorScheme.tertiary
        : (m == AutoApproveMode.manual ? null : theme.colorScheme.primary);

    return PopupMenuButton<AutoApproveMode>(
      tooltip: l.chatV2ComposerAutoApproveTooltip,
      onSelected: (next) => ref
          .read(chatControllerDepsProvider)
          .repo
          .setThreadAutoApprove(threadId, next),
      itemBuilder: (_) => [
        PopupMenuItem(
          value: AutoApproveMode.auto,
          child: _AutoApproveItem(
            checked: m == AutoApproveMode.auto,
            icon: Icons.flash_on,
            label: l.chatV2ComposerAutoApproveAuto,
          ),
        ),
        PopupMenuItem(
          value: AutoApproveMode.whitelist,
          child: _AutoApproveItem(
            checked: m == AutoApproveMode.whitelist,
            icon: Icons.playlist_add_check,
            label: l.chatV2ComposerAutoApproveWhitelist,
          ),
        ),
        PopupMenuItem(
          value: AutoApproveMode.manual,
          child: _AutoApproveItem(
            checked: m == AutoApproveMode.manual,
            icon: Icons.pan_tool_outlined,
            label: l.chatV2ComposerAutoApproveManual,
          ),
        ),
      ],
      child: _ChipShell(icon: icon, label: label, color: color),
    );
  }
}

class _AutoApproveItem extends StatelessWidget {
  const _AutoApproveItem({
    required this.checked,
    required this.icon,
    required this.label,
  });
  final bool checked;
  final IconData icon;
  final String label;

  @override
  Widget build(BuildContext context) {
    return Row(
      children: [
        Icon(checked ? Icons.check : icon, size: 14),
        const SizedBox(width: 8),
        Text(label),
      ],
    );
  }
}

/// _ChipShell —— composer 底部 chip 统一样式。所有 inline picker 复用,
/// 让对话/智能、workdir、auto-approve、模型 视觉一致:
/// 图标 15px + 文字 12px/w500 + 15px expand_more, 命中区 26px 高。
class _ChipShell extends StatelessWidget {
  const _ChipShell({required this.icon, required this.label, this.color});
  final IconData icon;
  final String label;
  final Color? color;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final c = color ?? theme.colorScheme.onSurfaceVariant;
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 4),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          Icon(icon, size: 15, color: c),
          const SizedBox(width: 4),
          ConstrainedBox(
            constraints: const BoxConstraints(maxWidth: 140),
            child: Text(
              label,
              style: TextStyle(
                fontSize: 12,
                height: 1.2,
                fontWeight: FontWeight.w500,
                color: c,
              ),
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
            ),
          ),
          Icon(Icons.expand_more, size: 15, color: c),
        ],
      ),
    );
  }
}

/// 数据源导入对话框 —— /wiki/p/:pid/sources 的「上传」按钮入口。
///
/// knowcode 原版（connector_import_dialog.dart, 1002 行）针对
/// cooper / Notion / Obsidian 树形 browse 工作流设计；biumind 当前不
/// 需要那些 enterprise 数据源整合，本版只做最常见三种：
///
///   - 单文件：file_selector 选一个 PDF/DOCX/MD/HTML/TXT
///   - 多文件：file_selector 一次选多个，串行上传 + 进度条
///   - URL：输入 URL，http GET 抓字节，上传，external_id=url 入库
///
/// 处理位置（P1 本机解析，设计文档 BiuMind-Client-Docproc-Design §3.4）：
///   - 本机解析（免费，默认）：docproc-web bundle 本地解析出文本后走
///     createSource(rawText + contentHash + parseMeta)，跳过服务端解析
///   - 云端（花积分）：现有 multipart 上传 + createSource(file_id) 不变
/// 平台不支持本机解析（hasLocalDocproc=false）时不显示本机选项；
/// 本机解析失败自动回退云端路径。
library;

import 'dart:async' show unawaited;
import 'dart:convert' show utf8;
import 'dart:io' show File;

import 'package:crypto/crypto.dart';
import 'package:file_selector/file_selector.dart';
import 'package:flutter/foundation.dart' show Uint8List, kIsWeb;
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:http/http.dart' as http;

import '../../../../app/theme.dart';
import '../../../../core/docproc/docproc_bridge_controller.dart';
import '../../../../core/docproc/docproc_view.dart';
import '../../../../core/platform/platform_caps.dart';
import '../../../../core/ui/biu_text_field.dart';
import '../../../../data/api/wiki_client.dart' show WikiClient;
import '../../../../data/wiki_providers.dart'
    show sourcesListProvider, wikiRepositoryProvider;
import '../../../../features/code/data/files_client.dart'
    show FilesClient, filesClientProvider;
import '../../data/docproc_task_mirror.dart';

class ImportDialog {
  ImportDialog._();

  /// 弹出导入对话框。完成后自动 invalidate sourcesListProvider 让列表
  /// 同步刷新。
  static Future<void> show(
    BuildContext context, {
    required String projectId,
  }) {
    return showDialog<void>(
      context: context,
      builder: (_) => _Dialog(projectId: projectId),
    );
  }
}

enum _Tab { single, multi, url }

/// 处理位置（设计文档 §3.4）：本机解析免费，云端花积分。
enum _ProcessLocation { local, cloud }

class _Dialog extends ConsumerStatefulWidget {
  const _Dialog({required this.projectId});
  final String projectId;

  @override
  ConsumerState<_Dialog> createState() => _DialogState();
}

class _DialogState extends ConsumerState<_Dialog> {
  _Tab _tab = _Tab.single;

  /// 已选 / 已抓的待上传项；上传中 / 完成时同条目就地更新状态。
  final List<_ImportItem> _items = [];

  /// 当前是否在批量上传中（屏蔽切 tab + 关闭按钮）。
  bool _running = false;

  /// 处理位置：hasLocalDocproc 时默认本机（§3.4 矩阵：桌面/Web ≤50MB、
  /// 移动端 ≤10MB 默认本机；超限文件上传时自动走云端）。
  _ProcessLocation _location = _ProcessLocation.local;

  /// 本机解析引擎（docproc-web bundle，隐藏 webview 挂在 dialog 树里）。
  late final DocprocBridgeController _docprocController;

  /// 当前在途的镜像任务（用于 dialog 被关时 PATCH cancelled）。
  DocprocTaskMirror? _activeMirror;

  /// 用户在上传/解析中途关闭 dialog：停止批量循环、不回退云端、
  /// 镜像任务 PATCH cancelled。
  bool _userCancelled = false;

  final TextEditingController _urlCtrl = TextEditingController();

  @override
  void initState() {
    super.initState();
    _docprocController = DocprocBridgeController(
      caps: ref.read(platformCapsProvider),
    );
  }

  @override
  void dispose() {
    if (_running) {
      _userCancelled = true;
      // 用户主动取消必须 PATCH cancelled（区别于进程死亡靠 reaper 检测）。
      // fire-and-forget：dispose 不能 await。
      unawaited(_activeMirror?.cancelled());
    }
    _urlCtrl.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final caps = ref.watch(platformCapsProvider);
    return Dialog(
      backgroundColor: BiuTokens.surface,
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(BiuTokens.radiusLg),
      ),
      child: ConstrainedBox(
        constraints: const BoxConstraints(maxWidth: 560, maxHeight: 600),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            // 本机解析引擎：无 UI 隐藏 webview，挂在 dialog 树里随开随用。
            if (caps.hasLocalDocproc)
              SizedBox(
                width: 0,
                height: 0,
                child: DocprocEngineView(controller: _docprocController),
              ),
            _Header(onClose: _running ? null : _close),
            _TabBar(
              tab: _tab,
              onChanged: _running ? null : (t) => setState(() => _tab = t),
            ),
            if (caps.hasLocalDocproc) ...[
              const SizedBox(height: 8),
              _LocationBar(
                location: _location,
                onChanged:
                    _running ? null : (l) => setState(() => _location = l),
              ),
            ],
            Divider(height: 1, color: BiuTokens.borderSubtle),
            Flexible(
              child: SingleChildScrollView(
                padding: const EdgeInsets.fromLTRB(20, 16, 20, 16),
                child: switch (_tab) {
                  _Tab.single => _SingleTab(onPick: _pickSingle),
                  _Tab.multi => _MultiTab(onPick: _pickMulti),
                  _Tab.url => _UrlTab(
                      controller: _urlCtrl,
                      onAdd: _addUrl,
                    ),
                },
              ),
            ),
            if (_items.isNotEmpty) ...[
              Divider(height: 1, color: BiuTokens.borderSubtle),
              Flexible(
                child: ListView.builder(
                  shrinkWrap: true,
                  padding: const EdgeInsets.symmetric(
                    horizontal: 16,
                    vertical: 8,
                  ),
                  itemCount: _items.length,
                  itemBuilder: (_, i) => _ItemRow(item: _items[i]),
                ),
              ),
            ],
            Divider(height: 1, color: BiuTokens.borderSubtle),
            _Footer(
              itemCount: _items.length,
              running: _running,
              onCancel: _running ? null : _close,
              onUpload: _items.isEmpty || _running ? null : _runUpload,
            ),
          ],
        ),
      ),
    );
  }

  void _close() => Navigator.of(context).pop();

  Future<void> _pickSingle() async {
    final f = await openFile(acceptedTypeGroups: _typeGroups);
    if (f == null) return;
    setState(() => _items.add(_ImportItem.fromFile(f)));
  }

  Future<void> _pickMulti() async {
    final files = await openFiles(acceptedTypeGroups: _typeGroups);
    if (files.isEmpty) return;
    setState(() {
      for (final f in files) {
        _items.add(_ImportItem.fromFile(f));
      }
    });
  }

  void _addUrl() {
    final raw = _urlCtrl.text.trim();
    if (raw.isEmpty) return;
    final uri = Uri.tryParse(raw);
    if (uri == null || !(uri.scheme == 'http' || uri.scheme == 'https')) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('URL 格式不正确（需要 http/https）')),
      );
      return;
    }
    setState(() {
      _items.add(_ImportItem.fromUrl(uri));
      _urlCtrl.clear();
    });
  }

  Future<void> _runUpload() async {
    final repo = ref.read(wikiRepositoryProvider);
    final filesClient = ref.read(filesClientProvider);
    final caps = ref.read(platformCapsProvider);
    if (repo == null || filesClient == null) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('未配置后端凭证')),
      );
      return;
    }
    setState(() => _running = true);
    for (var i = 0; i < _items.length; i++) {
      if (_userCancelled) break;
      if (_items[i].status == _ItemStatus.done) continue;
      _updateItem(i, status: _ItemStatus.uploading);
      try {
        await _uploadOne(_items[i], i, filesClient, repo.client, caps);
        _updateItem(i, status: _ItemStatus.done);
      } on Exception catch (e) {
        _updateItem(i,
            status: _ItemStatus.error, errorMessage: e.toString());
      }
    }
    setState(() => _running = false);
    ref.invalidate(sourcesListProvider(widget.projectId));
    if (!mounted) return;
    final ok = _items.where((it) => it.status == _ItemStatus.done).length;
    final fail = _items.where((it) => it.status == _ItemStatus.error).length;
    if (fail == 0) {
      Navigator.of(context).pop();
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('已导入 $ok 项')),
      );
    } else {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('完成 $ok / 失败 $fail')),
      );
    }
  }

  Future<void> _uploadOne(
    _ImportItem item,
    int index,
    FilesClient filesClient,
    WikiClient wikiClient,
    PlatformCaps caps,
  ) async {
    // 1) 拿到字节
    final List<int> bytes;
    final String filename;
    final String mime;
    String? externalId;
    if (item.kind == _ItemKind.url) {
      final url = item.url!;
      final resp = await http.get(url).timeout(const Duration(seconds: 30));
      if (resp.statusCode >= 400) {
        throw Exception('HTTP ${resp.statusCode}');
      }
      bytes = resp.bodyBytes;
      filename = _filenameFromUrl(url);
      mime = resp.headers['content-type']?.split(';').first.trim() ??
          _guessMime(filename);
      externalId = url.toString();
    } else {
      final f = item.file!;
      filename = f.name;
      mime = f.mimeType ?? _guessMime(filename);
      if (kIsWeb || f.path.isEmpty) {
        bytes = await f.readAsBytes();
      } else {
        bytes = await File(f.path).readAsBytes();
      }
    }

    // 2) 本机解析路径（P1 + W2 镜像，设计文档 §3.1/§3.5）：docproc-web
    //    本地解析出文本，sha256 幂等 hash + parse_meta 一并入库，跳过
    //    服务端解析；同时把生命周期镜像进 ingest_tasks（processor=client）。
    //    失败（DocprocException 等任何异常）自动回退云端路径。
    if (_shouldParseLocally(bytes.length, caps)) {
      final ok = await _parseLocally(
        wikiClient,
        filename: filename,
        mime: mime,
        bytes: bytes,
        externalId: externalId,
      );
      if (ok) return;
      if (_userCancelled) throw Exception('已取消');
      // else：本机解析失败，回退云端 multipart 上传（§8 失败自动转云端兜底）。
    }

    // 3) 云端路径：上传到 brain.files
    final upload = await filesClient.uploadBytes(
      bytes: bytes,
      filename: filename,
      contentType: mime,
      source: 'wiki-source',
      metadata: {
        'project_id': widget.projectId,
        'rel_path': filename,
        'external_id': ?externalId,
      },
      onProgress: (sent, total) =>
          _updateItem(index, sent: sent, total: total),
    );

    // 4) 创建 wiki source 记录
    await wikiClient.createSource(
      widget.projectId,
      relPath: filename,
      fileId: upload.fileId,
      filename: filename,
      mime: upload.mimeType ?? mime,
      byteSize: upload.sizeBytes,
      externalId: externalId,
    );
  }

  /// §3.4 策略矩阵：hasLocalDocproc + 用户选本机 + 大小在阈值内
  /// （桌面/Web ≤50MB，移动端 ≤10MB）才走本机解析。
  bool _shouldParseLocally(int byteSize, PlatformCaps caps) {
    if (!caps.hasLocalDocproc || _location != _ProcessLocation.local) {
      return false;
    }
    final limit = caps.isMobile ? 10 * 1024 * 1024 : 50 * 1024 * 1024;
    return byteSize <= limit;
  }

  /// 本机解析 + ingest_tasks 镜像（W2）。返回 true = 已入库（调用方
  /// 直接结束）；false = 失败，调用方回退云端路径。
  ///
  /// 时序（设计文档 §3.5）：
  ///   a. 占位 source（parseStatus=processing，无 file_id 无文本）
  ///   b. 镜像任务 processor=client（best-effort，不 publish 给 wiki-llm）
  ///   c. docproc parse；progress 回调节流 PATCH {phase, percent}
  ///   d. 成功：同 relPath upsert（rawText + contentHash + parseMeta +
  ///      parseStatus=done）→ PATCH done
  ///   e. 失败：best-effort PATCH failed → 返回 false 走云端回退
  Future<bool> _parseLocally(
    WikiClient wikiClient, {
    required String filename,
    required String mime,
    required List<int> bytes,
    String? externalId,
  }) async {
    final mirror = DocprocTaskMirror(
      client: wikiClient,
      projectId: widget.projectId,
    );
    _activeMirror = mirror;
    try {
      // a. 占位 source —— 镜像任务要求 source_id；parseStatus=processing
      //    让 sources 列表显示「解析中」而不是「待解析」。
      final placeholder = await wikiClient.createSource(
        widget.projectId,
        relPath: filename,
        filename: filename,
        mime: mime,
        byteSize: bytes.length,
        externalId: externalId,
        parseStatus: 'processing',
      );

      // b. 镜像任务（内部 best-effort；失败 taskId=null，后续 PATCH 全 no-op）
      await mirror.start(sourceId: placeholder.id, title: filename);

      // c. 本机解析 + 进度镜像（节流在 mirror 内）
      _docprocController.onProgress = (id, phase, percent) {
        unawaited(mirror.progress(phase, percent));
      };
      final result = await _docprocController.parse(
        fileName: filename,
        bytes: bytes is Uint8List ? bytes : Uint8List.fromList(bytes),
        mimeHint: mime,
      );

      // d. 同 relPath upsert 真实内容 + PATCH done
      final contentHash = sha256.convert(utf8.encode(result.text));
      await wikiClient.createSource(
        widget.projectId,
        relPath: filename,
        filename: filename,
        mime: mime,
        byteSize: bytes.length,
        externalId: externalId,
        rawText: result.text,
        contentHash: contentHash.toString(),
        // 本机解析已完成，明确标记 done —— 否则服务端默认 queued，
        // sources 列表会把这条源显示为「待解析」。
        parseStatus: 'done',
        parseMeta: {
          'parser': 'docproc-web',
          'version': result.parserVersion,
          'format': result.format,
          'page_count': ?result.pageCount,
        },
      );
      await mirror.done();
      return true;
    } on Exception catch (e) {
      // e. 失败：镜像标 failed（best-effort）→ 回退云端路径。云端回退
      //    会再次 createSource 同 relPath upsert 带 file_id，覆盖占位行，
      //    这是对的。
      await mirror.failed(e);
      return false;
    } finally {
      _docprocController.onProgress = null;
      if (identical(_activeMirror, mirror)) _activeMirror = null;
    }
  }

  void _updateItem(
    int i, {
    _ItemStatus? status,
    int? sent,
    int? total,
    String? errorMessage,
  }) {
    if (!mounted) return;
    setState(() {
      _items[i] = _items[i].copyWith(
        status: status,
        sent: sent,
        total: total,
        errorMessage: errorMessage,
      );
    });
  }
}

const List<XTypeGroup> _typeGroups = <XTypeGroup>[
  XTypeGroup(
    label: '文档',
    extensions: ['pdf', 'docx', 'md', 'txt', 'html', 'htm'],
  ),
];

String _filenameFromUrl(Uri u) {
  final segs = u.pathSegments.where((s) => s.isNotEmpty).toList();
  if (segs.isNotEmpty) return segs.last;
  return '${u.host}.html';
}

String _guessMime(String filename) {
  final lower = filename.toLowerCase();
  if (lower.endsWith('.pdf')) return 'application/pdf';
  if (lower.endsWith('.docx')) {
    return 'application/vnd.openxmlformats-officedocument'
        '.wordprocessingml.document';
  }
  if (lower.endsWith('.md')) return 'text/markdown';
  if (lower.endsWith('.txt')) return 'text/plain';
  if (lower.endsWith('.html') || lower.endsWith('.htm')) return 'text/html';
  return 'application/octet-stream';
}

// ─── data model ────────────────────────────────────────────────────────

enum _ItemKind { file, url }

enum _ItemStatus { pending, uploading, done, error }

class _ImportItem {
  _ImportItem({
    required this.kind,
    this.file,
    this.url,
    this.status = _ItemStatus.pending,
    this.sent = 0,
    this.total = 0,
    this.errorMessage,
  });

  factory _ImportItem.fromFile(XFile f) =>
      _ImportItem(kind: _ItemKind.file, file: f);

  factory _ImportItem.fromUrl(Uri u) =>
      _ImportItem(kind: _ItemKind.url, url: u);

  final _ItemKind kind;
  final XFile? file;
  final Uri? url;
  final _ItemStatus status;
  final int sent;
  final int total;
  final String? errorMessage;

  String get title => kind == _ItemKind.url
      ? url!.toString()
      : (file?.name ?? 'unknown');

  _ImportItem copyWith({
    _ItemStatus? status,
    int? sent,
    int? total,
    String? errorMessage,
  }) =>
      _ImportItem(
        kind: kind,
        file: file,
        url: url,
        status: status ?? this.status,
        sent: sent ?? this.sent,
        total: total ?? this.total,
        errorMessage: errorMessage ?? this.errorMessage,
      );

  double? get progress {
    if (total <= 0) return null;
    return (sent / total).clamp(0.0, 1.0);
  }
}

// ─── widgets ───────────────────────────────────────────────────────────

class _Header extends StatelessWidget {
  const _Header({required this.onClose});
  final VoidCallback? onClose;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(20, 16, 12, 12),
      child: Row(
        children: [
          Icon(Icons.upload_file_outlined,
              size: 18, color: BiuTokens.text),
          const SizedBox(width: 8),
          Text(
            '导入数据源',
            style: TextStyle(
              color: BiuTokens.text,
              fontSize: 16,
              fontWeight: FontWeight.w700,
            ),
          ),
          const Spacer(),
          IconButton(
            tooltip: '关闭',
            onPressed: onClose,
            icon: const Icon(Icons.close, size: 18),
            padding: EdgeInsets.zero,
            constraints: const BoxConstraints(minWidth: 28, minHeight: 28),
          ),
        ],
      ),
    );
  }
}

class _TabBar extends StatelessWidget {
  const _TabBar({required this.tab, required this.onChanged});
  final _Tab tab;
  final ValueChanged<_Tab>? onChanged;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 16),
      child: Row(
        children: [
          _TabButton(
            label: '单文件',
            icon: Icons.insert_drive_file_outlined,
            active: tab == _Tab.single,
            onTap: () => onChanged?.call(_Tab.single),
          ),
          _TabButton(
            label: '多文件',
            icon: Icons.folder_open_outlined,
            active: tab == _Tab.multi,
            onTap: () => onChanged?.call(_Tab.multi),
          ),
          _TabButton(
            label: 'URL',
            icon: Icons.link,
            active: tab == _Tab.url,
            onTap: () => onChanged?.call(_Tab.url),
          ),
        ],
      ),
    );
  }
}

class _TabButton extends StatelessWidget {
  const _TabButton({
    required this.label,
    required this.icon,
    required this.active,
    required this.onTap,
  });
  final String label;
  final IconData icon;
  final bool active;
  final VoidCallback? onTap;

  @override
  Widget build(BuildContext context) {
    final brand = Theme.of(context).colorScheme.primary;
    final color = active ? brand : BiuTokens.textSecondary;
    return Padding(
      padding: const EdgeInsets.only(right: 6),
      child: InkWell(
        onTap: onTap,
        borderRadius: BorderRadius.circular(6),
        child: Container(
          padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 8),
          decoration: BoxDecoration(
            border: Border(
              bottom: BorderSide(
                color: active ? brand : Colors.transparent,
                width: 2,
              ),
            ),
          ),
          child: Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              Icon(icon, size: 14, color: color),
              const SizedBox(width: 6),
              Text(
                label,
                style: TextStyle(
                  color: color,
                  fontSize: 13,
                  fontWeight: active ? FontWeight.w600 : FontWeight.w500,
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

/// 「处理位置」选择条：本机解析（免费）/ 云端（花积分）。
/// 仅 hasLocalDocproc 的平台渲染（桌面/Web ≤50MB、移动端 ≤10MB 默认本机，
/// 超限文件上传时自动走云端，见 _shouldParseLocally）。
class _LocationBar extends StatelessWidget {
  const _LocationBar({required this.location, required this.onChanged});
  final _ProcessLocation location;
  final ValueChanged<_ProcessLocation>? onChanged;

  @override
  Widget build(BuildContext context) {
    Widget chip(_ProcessLocation value, String label) {
      final selected = location == value;
      final brand = Theme.of(context).colorScheme.primary;
      return Padding(
        padding: const EdgeInsets.only(right: 6),
        child: InkWell(
          onTap: onChanged == null ? null : () => onChanged!(value),
          borderRadius: BorderRadius.circular(6),
          child: Container(
            padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 5),
            decoration: BoxDecoration(
              color: selected ? brand.withValues(alpha: 0.12) : null,
              borderRadius: BorderRadius.circular(6),
              border: Border.all(
                color: selected ? brand : BiuTokens.borderSubtle,
              ),
            ),
            child: Text(
              label,
              style: TextStyle(
                color: selected ? brand : BiuTokens.textSecondary,
                fontSize: 12,
                fontWeight: selected ? FontWeight.w600 : FontWeight.w500,
              ),
            ),
          ),
        ),
      );
    }

    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 20),
      child: Row(
        children: [
          Text(
            '处理位置',
            style: TextStyle(color: BiuTokens.textSecondary, fontSize: 12),
          ),
          const SizedBox(width: 10),
          chip(_ProcessLocation.local, '本机解析（免费）'),
          chip(_ProcessLocation.cloud, '云端（花积分）'),
        ],
      ),
    );
  }
}

class _SingleTab extends StatelessWidget {
  const _SingleTab({required this.onPick});
  final VoidCallback onPick;
  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(
          '选择一个 PDF / DOCX / Markdown / HTML 文档',
          style: TextStyle(color: BiuTokens.textSecondary, fontSize: 12),
        ),
        const SizedBox(height: 12),
        OutlinedButton.icon(
          onPressed: onPick,
          icon: const Icon(Icons.attach_file, size: 16),
          label: const Text('选择文件'),
          style: OutlinedButton.styleFrom(
            foregroundColor: BiuTokens.text,
            side: BorderSide(color: BiuTokens.borderSubtle),
            padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 10),
          ),
        ),
      ],
    );
  }
}

class _MultiTab extends StatelessWidget {
  const _MultiTab({required this.onPick});
  final VoidCallback onPick;

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(
          '一次选多个文件，系统会按顺序串行上传 + 解析。',
          style: TextStyle(color: BiuTokens.textSecondary, fontSize: 12),
        ),
        const SizedBox(height: 12),
        OutlinedButton.icon(
          onPressed: onPick,
          icon: const Icon(Icons.folder_open_outlined, size: 16),
          label: const Text('选择多个文件'),
          style: OutlinedButton.styleFrom(
            foregroundColor: BiuTokens.text,
            side: BorderSide(color: BiuTokens.borderSubtle),
            padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 10),
          ),
        ),
      ],
    );
  }
}

class _UrlTab extends StatelessWidget {
  const _UrlTab({required this.controller, required this.onAdd});
  final TextEditingController controller;
  final VoidCallback onAdd;

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(
          '抓取一个 URL（HTML / PDF / Markdown）作为源文件入库。',
          style: TextStyle(color: BiuTokens.textSecondary, fontSize: 12),
        ),
        const SizedBox(height: 12),
        Row(
          children: [
            Expanded(
              child: BiuTextField(
                controller: controller,
                hintText: 'https://example.com/article',
                style: TextStyle(fontSize: 13, color: BiuTokens.text),
                onSubmitted: (_) => onAdd(),
              ),
            ),
            const SizedBox(width: 8),
            FilledButton(
              onPressed: onAdd,
              style: FilledButton.styleFrom(
                backgroundColor: Theme.of(context).colorScheme.primary,
                padding: const EdgeInsets.symmetric(
                  horizontal: 16,
                  vertical: 12,
                ),
              ),
              child: const Text('添加'),
            ),
          ],
        ),
      ],
    );
  }
}

class _ItemRow extends StatelessWidget {
  const _ItemRow({required this.item});
  final _ImportItem item;

  @override
  Widget build(BuildContext context) {
    final (icon, color, label) = switch (item.status) {
      _ItemStatus.pending => (
        Icons.hourglass_empty,
        BiuTokens.textMuted,
        '待上传',
      ),
      _ItemStatus.uploading => (
        Icons.cloud_upload_outlined,
        BiuTokens.purple,
        '上传中',
      ),
      _ItemStatus.done => (
        Icons.check_circle_outline,
        BiuTokens.success,
        '已入库',
      ),
      _ItemStatus.error => (
        Icons.error_outline,
        BiuTokens.error,
        '失败',
      ),
    };

    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 6),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Icon(icon, size: 14, color: color),
              const SizedBox(width: 6),
              Expanded(
                child: Text(
                  item.title,
                  style: TextStyle(
                    color: BiuTokens.text,
                    fontSize: 12,
                  ),
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                ),
              ),
              Text(
                label,
                style: TextStyle(color: color, fontSize: 11),
              ),
            ],
          ),
          if (item.status == _ItemStatus.uploading) ...[
            const SizedBox(height: 4),
            ClipRRect(
              borderRadius: BorderRadius.circular(2),
              child: LinearProgressIndicator(
                value: item.progress,
                minHeight: 3,
                backgroundColor: BiuTokens.surfaceMuted,
                color: BiuTokens.purple,
              ),
            ),
          ],
          if (item.status == _ItemStatus.error &&
              item.errorMessage != null) ...[
            const SizedBox(height: 4),
            Text(
              item.errorMessage!,
              style: TextStyle(color: BiuTokens.error, fontSize: 11),
              maxLines: 2,
              overflow: TextOverflow.ellipsis,
            ),
          ],
        ],
      ),
    );
  }
}

class _Footer extends StatelessWidget {
  const _Footer({
    required this.itemCount,
    required this.running,
    required this.onCancel,
    required this.onUpload,
  });
  final int itemCount;
  final bool running;
  final VoidCallback? onCancel;
  final VoidCallback? onUpload;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
      child: Row(
        children: [
          if (itemCount > 0)
            Text(
              '$itemCount 项待上传',
              style: TextStyle(color: BiuTokens.textMuted, fontSize: 12),
            ),
          const Spacer(),
          TextButton(
            onPressed: onCancel,
            child: Text(
              '取消',
              style: TextStyle(color: BiuTokens.textSecondary),
            ),
          ),
          const SizedBox(width: 8),
          FilledButton.icon(
            onPressed: onUpload,
            icon: running
                ? const SizedBox(
                    width: 12,
                    height: 12,
                    child: CircularProgressIndicator(
                      strokeWidth: 1.5,
                      color: Colors.white,
                    ),
                  )
                : const Icon(Icons.upload, size: 14),
            label: Text(running ? '上传中…' : '上传 + 入库'),
            style: FilledButton.styleFrom(
              backgroundColor: Theme.of(context).colorScheme.primary,
              padding: const EdgeInsets.symmetric(
                horizontal: 14,
                vertical: 10,
              ),
            ),
          ),
        ],
      ),
    );
  }
}

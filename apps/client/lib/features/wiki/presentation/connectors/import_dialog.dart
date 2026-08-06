/// 数据源导入对话框 —— /wiki/p/:pid/sources 的「上传」按钮入口。
///
/// knowcode 原版（connector_import_dialog.dart, 1002 行）针对
/// cooper / Notion / Obsidian 树形 browse 工作流设计；biumind 当前不
/// 需要那些 enterprise 数据源整合，本版只做最常见三种：
///
///   - 单文件：file_selector 选一个 PDF/MD/HTML/TXT
///   - 多文件：file_selector 一次选多个，串行上传 + 进度条
///   - URL：输入 URL，http GET 抓字节，上传，external_id=url 入库
///
/// 上传走 [FilesClient.uploadBytes/uploadFile] → [WikiClient.createSource]，
/// 与 sources_page 单文件路径一致；只是这里支持批量 + URL 抓取。
library;

import 'dart:io' show File;

import 'package:file_selector/file_selector.dart';
import 'package:flutter/foundation.dart' show kIsWeb;
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:http/http.dart' as http;

import '../../../../app/theme.dart';
import '../../../../core/ui/biu_text_field.dart';
import '../../../../data/api/wiki_client.dart' show WikiClient;
import '../../../../data/wiki_providers.dart'
    show sourcesListProvider, wikiRepositoryProvider;
import '../../../../features/code/data/files_client.dart'
    show FilesClient, filesClientProvider;

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

  final TextEditingController _urlCtrl = TextEditingController();

  @override
  void dispose() {
    _urlCtrl.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
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
            _Header(onClose: _running ? null : _close),
            _TabBar(
              tab: _tab,
              onChanged: _running ? null : (t) => setState(() => _tab = t),
            ),
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
    if (repo == null || filesClient == null) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('未配置后端凭证')),
      );
      return;
    }
    setState(() => _running = true);
    for (var i = 0; i < _items.length; i++) {
      if (_items[i].status == _ItemStatus.done) continue;
      _updateItem(i, status: _ItemStatus.uploading);
      try {
        await _uploadOne(_items[i], i, filesClient, repo.client);
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

    // 2) 上传到 brain.files
    final upload = await filesClient.uploadBytes(
      bytes: bytes,
      filename: filename,
      contentType: mime,
      source: 'wiki-source',
      metadata: {
        'project_id': widget.projectId,
        'rel_path': filename,
        if (externalId != null) 'external_id': externalId,
      },
      onProgress: (sent, total) =>
          _updateItem(index, sent: sent, total: total),
    );

    // 3) 创建 wiki source 记录
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
    extensions: ['pdf', 'md', 'txt', 'html', 'htm'],
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

class _SingleTab extends StatelessWidget {
  const _SingleTab({required this.onPick});
  final VoidCallback onPick;

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(
          '选择一个 PDF / Markdown / HTML 文档',
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

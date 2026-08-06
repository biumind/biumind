// Add-WebView dialog (M12.3 + favicon auto-fetch).
//
// Three fields: title + URL + (auto-resolved) icon. URL onChange 后 debounce
// 400ms 自动抓 origin/favicon.ico, 成功上传到 Files CAS 拿 sha256 hash,
// 提交时连同 title/url 一起发给 createUserWebView。失败 (非 image / 体积
// 过大 / 网络错误) 静默跳过 — install 仍然可创建, hash 字段为空。
//
// 体积 cap 256 KiB (favicon 一般 < 32 KiB; 大于这个值多半被 host 当通用
// fallback 页 200 返回 HTML 了, 不该当图标存)。
//
// dialog 中 24px 预览给用户即时反馈"抓到了什么", 不喜欢可重新输 URL 触发
// 再抓一遍。sidebar 渲染图标按 hash 拉 Files 的逻辑独立 follow-up;
// 当前阶段先把"上传 + hash 落 install row"链路打通。

import 'dart:async';
import 'dart:typed_data';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:http/http.dart' as http;

import '../../../app/theme.dart';
import '../../../data/api/_http_helpers.dart';
import '../../../data/api/apps_client.dart';
import '../../../data/apps_providers.dart';
import '../../code/data/files_client.dart';
import 'apps_error.dart';

/// Shows the dialog and returns the new install on success.
Future<Installation?> showAddWebViewDialog(BuildContext context) {
  return showDialog<Installation>(
    context: context,
    builder: (_) => const _AddWebViewDialog(),
  );
}

class _AddWebViewDialog extends ConsumerStatefulWidget {
  const _AddWebViewDialog();
  @override
  ConsumerState<_AddWebViewDialog> createState() => _AddWebViewDialogState();
}

class _AddWebViewDialogState extends ConsumerState<_AddWebViewDialog> {
  final _formKey = GlobalKey<FormState>();
  final _title = TextEditingController();
  final _url = TextEditingController(text: 'https://');
  bool _submitting = false;
  String? _serverErr;

  // Favicon 抓取状态 — debounce + 上传后的 sha256。
  Timer? _iconDebounce;
  bool _fetchingIcon = false;
  Uint8List? _iconBytes;
  String? _iconFileHash;

  @override
  void initState() {
    super.initState();
    _url.addListener(_scheduleIconFetch);
  }

  @override
  void dispose() {
    _iconDebounce?.cancel();
    _url.removeListener(_scheduleIconFetch);
    _title.dispose();
    _url.dispose();
    super.dispose();
  }

  void _scheduleIconFetch() {
    _iconDebounce?.cancel();
    // URL 还在快速键入 — 等用户停下来再抓, 否则每按一键发一次请求。
    _iconDebounce = Timer(const Duration(milliseconds: 400), _tryFetchFavicon);
  }

  Future<void> _tryFetchFavicon() async {
    if (!mounted) return;
    final raw = _url.text.trim();
    // _validateUrl 通过才尝试抓 — 避免 https:// 等 partial 输入触发 HTTP。
    if (_validateUrl(raw) != null) {
      if (_iconBytes != null || _iconFileHash != null || _fetchingIcon) {
        setState(() {
          _iconBytes = null;
          _iconFileHash = null;
          _fetchingIcon = false;
        });
      }
      return;
    }
    final origin = Uri.parse(raw);
    final faviconUri = origin.replace(path: '/favicon.ico', query: '', fragment: '');

    setState(() {
      _fetchingIcon = true;
      _iconBytes = null;
      _iconFileHash = null;
    });

    Uint8List? bytes;
    String contentType = 'image/x-icon';
    final client = http.Client();
    try {
      final resp = await client
          .get(faviconUri)
          .timeout(const Duration(seconds: 3));
      if (resp.statusCode == 200) {
        final ct = resp.headers['content-type']?.toLowerCase() ?? '';
        // 体积 cap 256 KiB, 防止 host 把通用 404 / fallback HTML 当 200 返回
        // 给用户当 "图标" 上传一坨垃圾。
        if (ct.startsWith('image/') &&
            resp.bodyBytes.isNotEmpty &&
            resp.bodyBytes.length <= 256 * 1024) {
          bytes = resp.bodyBytes;
          contentType = ct.split(';').first.trim();
        }
      }
    } catch (_) {
      // 静默失败 — favicon 抓不到不影响装应用本身。
    } finally {
      client.close();
    }

    if (!mounted) return;
    if (bytes == null) {
      setState(() => _fetchingIcon = false);
      return;
    }

    // 上传到 Files CAS。失败也静默 — 用户能看到本地预览即可。
    String? hash;
    final filesClient = ref.read(filesClientProvider);
    if (filesClient != null) {
      try {
        final result = await filesClient.uploadBytes(
          bytes: bytes,
          filename: 'favicon-${origin.host}.ico',
          contentType: contentType,
          source: 'app-icon',
        );
        hash = result.sha256;
      } catch (_) {
        // 仅本地预览, 不阻塞创建。
      }
    }

    if (!mounted) return;
    setState(() {
      _iconBytes = bytes;
      _iconFileHash = hash;
      _fetchingIcon = false;
    });
  }

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: const Text('添加 WebView 应用'),
      content: SizedBox(
        width: 400,
        child: Form(
          key: _formKey,
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              TextFormField(
                controller: _title,
                autofocus: true,
                decoration: const InputDecoration(
                  labelText: '名称 *',
                  hintText: '例：Kimi',
                  helperText: '将显示在 App Center 与侧边栏',
                ),
                validator: (v) =>
                    (v == null || v.trim().isEmpty) ? '请填写名称' : null,
                textInputAction: TextInputAction.next,
              ),
              const SizedBox(height: BiuTokens.space3),
              TextFormField(
                controller: _url,
                decoration: InputDecoration(
                  labelText: 'URL *',
                  hintText: 'https://kimi.moonshot.cn',
                  helperText: '只支持 http(s); 必须是完整域名或 localhost',
                  // 右侧站位: 抓取中 / 已抓到 / 未抓 — 让用户感知 favicon
                  // 自动 fetch 进度。
                  suffixIcon: _faviconAdornment(),
                ),
                keyboardType: TextInputType.url,
                validator: _validateUrl,
                onFieldSubmitted: (_) => _submit(),
              ),
              if (_serverErr != null) ...[
                const SizedBox(height: BiuTokens.space2),
                Text(
                  _serverErr!,
                  style: TextStyle(
                    color: Theme.of(context).colorScheme.error,
                    fontSize: 12,
                  ),
                ),
              ],
              const SizedBox(height: BiuTokens.space3),
              Container(
                padding: const EdgeInsets.all(BiuTokens.space2),
                decoration: BoxDecoration(
                  color: Theme.of(context)
                      .colorScheme
                      .surfaceContainerHigh,
                  borderRadius: BorderRadius.circular(6),
                ),
                child: Row(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Icon(Icons.info_outline,
                        size: 14,
                        color: Theme.of(context).colorScheme.onSurfaceVariant),
                    const SizedBox(width: 6),
                    Expanded(
                      child: Text(
                        'WebView 应用共享一份登录态：在 BiuMind 内登录 Kimi 后，所有 webview 应用看到的 cookie 一致；与系统浏览器互相隔离。如需多账号，请使用系统浏览器。',
                        style: Theme.of(context)
                            .textTheme
                            .bodySmall
                            ?.copyWith(
                                color: Theme.of(context)
                                    .colorScheme
                                    .onSurfaceVariant),
                      ),
                    ),
                  ],
                ),
              ),
            ],
          ),
        ),
      ),
      actions: [
        TextButton(
          onPressed: _submitting ? null : () => Navigator.of(context).pop(),
          child: const Text('取消'),
        ),
        FilledButton(
          onPressed: _submitting ? null : _submit,
          child: _submitting
              ? const SizedBox(
                  width: 16,
                  height: 16,
                  child: CircularProgressIndicator(strokeWidth: 2),
                )
              : const Text('创建'),
        ),
      ],
    );
  }

  Widget? _faviconAdornment() {
    if (_fetchingIcon) {
      return const Padding(
        padding: EdgeInsets.all(10),
        child: SizedBox(
          width: 16,
          height: 16,
          child: CircularProgressIndicator(strokeWidth: 1.5),
        ),
      );
    }
    if (_iconBytes != null) {
      return Padding(
        padding: const EdgeInsets.all(8),
        child: ClipRRect(
          borderRadius: BorderRadius.circular(4),
          child: Image.memory(
            _iconBytes!,
            width: 20,
            height: 20,
            fit: BoxFit.cover,
            // 渲染失败 (e.g. ICO 格式 Flutter 不支持) 用通用站点图标兜底。
            errorBuilder: (_, _, _) => const Icon(Icons.public, size: 18),
          ),
        ),
      );
    }
    return null;
  }

  String? _validateUrl(String? raw) {
    if (raw == null || raw.trim().isEmpty || raw == 'https://') {
      return '请填写 URL';
    }
    final u = Uri.tryParse(raw.trim());
    if (u == null) return 'URL 格式无效';
    if (u.scheme != 'http' && u.scheme != 'https') {
      return '协议必须是 http 或 https';
    }
    if (u.host.isEmpty) return 'URL 缺少主机名';
    if (!u.host.contains('.') && u.host != 'localhost') {
      return '主机名必须是完整域名（如 kimi.moonshot.cn）或 localhost';
    }
    return null;
  }

  Future<void> _submit() async {
    if (!(_formKey.currentState?.validate() ?? false)) return;
    final client = ref.read(appsClientProvider);
    final token = ref.read(appsBearerProvider);
    if (client == null || token == null) {
      setState(() => _serverErr = '尚未配置 model-relay 凭据');
      return;
    }
    setState(() {
      _submitting = true;
      _serverErr = null;
    });
    try {
      final install = await client.createUserWebView(
        title: _title.text.trim(),
        url: _url.text.trim(),
        iconFileHash: _iconFileHash,
        token: token,
      );
      if (!mounted) return;
      // Refresh providers so the new app shows up in catalog + installs.
      // 只失效 scope='user' 的列表（创建者就是当前用户）, 不动 'org'。
      ref.invalidate(appsCatalogProvider);
      ref.invalidateInstallScope('user');
      Navigator.of(context).pop(install);
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _serverErr = _humanize(context, e);
        _submitting = false;
      });
    }
  }

  String _humanize(BuildContext context, Object e) {
    // 优先解析 createUserWebView 专属的 `invalid_input: ...` 形状: 服务端
    // 在 400 body 里以 plain text 抛 "invalid_input: url must be https",
    // 用户看到 humanizer 的"请求参数有误："+这条 detail 比通用 4xx
    // 文案更直白。
    if (e is ApiError) {
      final marker = 'invalid_input:';
      final idx = e.body.indexOf(marker);
      if (idx >= 0) return e.body.substring(idx + marker.length).trim();
    }
    return humanizeAppsError(context, e);
  }
}

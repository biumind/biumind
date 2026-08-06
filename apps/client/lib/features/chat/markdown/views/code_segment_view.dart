// Code 段渲染 — header (lang + copy) + flutter_highlight 语法高亮。
//
// 主题选择: atom-one-light. 长期可以 watch user.themeMode 切到 dark
// (atom-one-dark / one-dark)。当前 BiuMind 仍是浅色主线, 只用 light 主题。
//
// 特殊处理: 当 lang ∈ {markdown, md} 时, 在源码块下方追加一段 inline
// 预览 — AI 经常用 ```markdown ... ``` 展示嵌套 mermaid / 代码块语法,
// 用户既要看源码也要看渲染图。preview 通过递归走一次 pipeline 拿到。
//
// flutter_highlight 不识别的 language 会回退到 plain monospace — 不
// 阻塞渲染。

import 'dart:convert' show utf8;

import 'package:file_selector/file_selector.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_highlight/flutter_highlight.dart';
import 'package:flutter_highlight/themes/atom-one-dark.dart';
import 'package:flutter_highlight/themes/atom-one-light.dart';
import 'package:shared_preferences/shared_preferences.dart';

import '../../../../app/theme.dart';
import '../pipeline.dart';

/// 高亮主题随系统亮度切换。BiuTokens 已经按 brightness 切色, 这里复用同
/// 个信号. 暗色用 atom-one-dark (跟 atom-one-light 同系, 风格一致)。
Map<String, TextStyle> _highlightTheme() =>
    BiuTokens.brightness == Brightness.dark ? atomOneDarkTheme : atomOneLightTheme;

class CodeSegmentView extends StatefulWidget {
  const CodeSegmentView({
    super.key,
    required this.language,
    required this.code,
    required this.closed,
  });

  final String language;
  final String code;
  /// fence 未闭合时 (流式) 不显示 copy 按钮 — 内容还会变。
  final bool closed;

  @override
  State<CodeSegmentView> createState() => _CodeSegmentViewState();
}

class _CodeSegmentViewState extends State<CodeSegmentView> {
  /// 行号默认关，避免短代码段视觉冗余；超过 8 行 default 也保持关，让用户
  /// 主动切。
  bool _showLineNumbers = false;
  /// 长行换行 vs 横向滚动。默认关（保留代码原貌 + 横滚），用户主动切到换
  /// 行模式（适合阅读小屏 / 长 prose-style 文本）。
  bool _wrapLines = false;
  /// 用户手动覆盖的渲染语言。null = 用 widget.language（fence 标的）。
  /// 模型偶尔标错语言（json 标 plaintext / dart 标 js 之类），让用户能纠正。
  String? _languageOverride;

  /// SharedPreferences key —— 用 code.hashCode 做稳定 ID。Dart 2.14+
  /// String.hashCode 是确定的（不会跨 isolate / 进程变）。
  String get _prefsKey => 'biu.chat.code_lang.${widget.code.hashCode}';

  @override
  void initState() {
    super.initState();
    // 已有 saved override → 恢复。streaming 中 (closed=false) 不读，避免
    // 部分 fence 内容触发错误的旧 hash 命中。
    if (widget.closed) {
      SharedPreferences.getInstance().then((prefs) {
        final saved = prefs.getString(_prefsKey);
        if (!mounted || saved == null || saved.isEmpty) return;
        final original = widget.language.isEmpty ? 'plaintext' : widget.language;
        if (saved != original) {
          setState(() => _languageOverride = saved);
        }
      });
    }
  }

  Future<void> _persistLanguageOverride(String? next, String original) async {
    try {
      final prefs = await SharedPreferences.getInstance();
      if (next == null || next == original) {
        await prefs.remove(_prefsKey);
      } else {
        await prefs.setString(_prefsKey, next);
      }
    } catch (_) {/* fail silent */}
  }

  @override
  Widget build(BuildContext context) {
    final code = widget.code;
    final closed = widget.closed;
    final originalLang = widget.language.isEmpty ? 'plaintext' : widget.language;
    final lang = _languageOverride ?? originalLang;
    final showInnerPreview =
        closed && (lang == 'markdown' || lang == 'md') && code.trim().isNotEmpty;
    // 行号 toggle 仅 closed 状态出（流式中行数还在变）+ 至少 3 行。
    final lineCount = '\n'.allMatches(code).length + 1;
    final canShowLineNumbers = closed && lineCount >= 3;
    const codeTextStyle = TextStyle(
      fontFamily: 'JetBrains Mono, ui-monospace, monospace',
      fontSize: 12.5,
      height: 1.5,
    );
    final highlightCode = HighlightView(
      code,
      language: lang,
      theme: _highlightTheme(),
      padding: EdgeInsets.zero,
      textStyle: codeTextStyle,
    );
    // wrap 模式：代码自然换行（不再横滚），适合阅读 prose-style 长行；
    // 默认关 —— 大多数代码 width fits, 横滚反而保留对齐。
    Widget wrapOrScroll(Widget child) {
      return _wrapLines
          ? child
          : SingleChildScrollView(
              scrollDirection: Axis.horizontal,
              child: child,
            );
    }

    Widget codeBody;
    if (canShowLineNumbers && _showLineNumbers) {
      // 行号列 + 代码区域。两栏共享同一垂直 layout，行间距由
      // codeTextStyle.height 控制。
      final numberStyle = codeTextStyle.copyWith(
        color: BiuTokens.textMuted,
      );
      codeBody = Padding(
        padding: const EdgeInsets.symmetric(
          horizontal: BiuTokens.space3,
          vertical: BiuTokens.space2,
        ),
        child: Row(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Padding(
              padding: const EdgeInsets.only(right: 12),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.end,
                children: [
                  for (var i = 0; i < lineCount; i++)
                    Text('${i + 1}', style: numberStyle),
                ],
              ),
            ),
            Expanded(child: wrapOrScroll(highlightCode)),
          ],
        ),
      );
    } else {
      codeBody = Padding(
        padding: const EdgeInsets.symmetric(
          horizontal: BiuTokens.space3,
          vertical: BiuTokens.space2,
        ),
        child: wrapOrScroll(highlightCode),
      );
    }

    final block = Container(
      width: double.infinity,
      margin: const EdgeInsets.symmetric(vertical: BiuTokens.space2),
      decoration: BoxDecoration(
        color: BiuTokens.surfaceMuted,
        borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
        border: Border.all(color: BiuTokens.borderSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          _Header(
            lang: lang,
            code: code,
            closed: closed,
            showLineNumbers: _showLineNumbers,
            canToggleLineNumbers: canShowLineNumbers,
            onToggleLineNumbers: () =>
                setState(() => _showLineNumbers = !_showLineNumbers),
            wrapLines: _wrapLines,
            onToggleWrap: closed
                ? () => setState(() => _wrapLines = !_wrapLines)
                : null,
            onChangeLanguage: closed
                ? (next) {
                    setState(() {
                      _languageOverride =
                          next == originalLang ? null : next;
                    });
                    _persistLanguageOverride(_languageOverride, originalLang);
                  }
                : null,
          ),
          codeBody,
        ],
      ),
    );
    if (!showInnerPreview) return block;
    // 把 markdown 源码同时跑一遍 pipeline, 在源码块下方渲染预览。
    // ChatMarkdownView 自带 State 缓存, 同 code 多 frame 重 build 也只
    // split 一次。
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        block,
        const _PreviewLabel(),
        ChatMarkdownView(text: code),
      ],
    );
  }
}

class _PreviewLabel extends StatelessWidget {
  const _PreviewLabel();
  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.only(top: 4, bottom: 2, left: 4),
      child: Row(
        children: [
          Icon(Icons.visibility_outlined,
              size: 12, color: BiuTokens.textMuted),
          const SizedBox(width: 4),
          Text(
            '渲染预览',
            style: TextStyle(
              fontSize: 11,
              color: BiuTokens.textMuted,
              fontWeight: FontWeight.w500,
            ),
          ),
        ],
      ),
    );
  }
}

class _Header extends StatelessWidget {
  const _Header({
    required this.lang,
    required this.code,
    required this.closed,
    this.showLineNumbers = false,
    this.canToggleLineNumbers = false,
    this.onToggleLineNumbers,
    this.wrapLines = false,
    this.onToggleWrap,
    this.onChangeLanguage,
  });
  final String lang;
  final String code;
  final bool closed;
  final bool showLineNumbers;
  final bool canToggleLineNumbers;
  final VoidCallback? onToggleLineNumbers;
  final bool wrapLines;
  final VoidCallback? onToggleWrap;
  /// 非空 → lang label 变 dropdown，让用户切渲染语言（覆盖 fence 标的）。
  final ValueChanged<String>? onChangeLanguage;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(
        horizontal: BiuTokens.space3,
        vertical: BiuTokens.space1,
      ),
      decoration: BoxDecoration(
        border: Border(
          bottom: BorderSide(color: BiuTokens.borderSubtle),
        ),
      ),
      child: Row(
        children: [
          if (onChangeLanguage != null)
            _LanguagePicker(current: lang, onChange: onChangeLanguage!)
          else
            Text(
              lang,
              style: TextStyle(
                fontSize: 11,
                color: BiuTokens.textMuted,
                fontFamily: 'monospace',
              ),
            ),
          const Spacer(),
          if (closed) ...[
            if (onToggleWrap != null) ...[
              _WrapToggle(active: wrapLines, onTap: onToggleWrap!),
              const SizedBox(width: 4),
            ],
            if (canToggleLineNumbers && onToggleLineNumbers != null) ...[
              _LineNumbersToggle(
                active: showLineNumbers,
                onTap: onToggleLineNumbers!,
              ),
              const SizedBox(width: 4),
            ],
            _ExpandButton(lang: lang, code: code),
            const SizedBox(width: 4),
            _SaveButton(lang: lang, code: code),
            const SizedBox(width: 4),
            _CopyButton(text: code),
          ],
        ],
      ),
    );
  }
}

/// 代码块语言切换器 —— PopupMenu 让用户改渲染语言（覆盖 fence 标）。
/// 模型偶尔标错（json 标 plaintext / dart 标 js），这里给用户兜底。
class _LanguagePicker extends StatelessWidget {
  const _LanguagePicker({required this.current, required this.onChange});
  final String current;
  final ValueChanged<String> onChange;

  static const _options = <String>[
    'plaintext',
    'dart',
    'python',
    'javascript',
    'typescript',
    'go',
    'rust',
    'java',
    'kotlin',
    'swift',
    'c',
    'cpp',
    'cs',
    'ruby',
    'php',
    'shell',
    'sql',
    'json',
    'yaml',
    'xml',
    'html',
    'css',
    'markdown',
    'mermaid',
    'lua',
  ];

  @override
  Widget build(BuildContext context) {
    return PopupMenuButton<String>(
      tooltip: '切换渲染语言',
      onSelected: onChange,
      itemBuilder: (_) => [
        for (final opt in _options)
          PopupMenuItem<String>(
            value: opt,
            child: Row(
              children: [
                Icon(
                  opt == current ? Icons.check : Icons.circle_outlined,
                  size: 12,
                  color: BiuTokens.textMuted,
                ),
                const SizedBox(width: 8),
                Text(opt,
                    style: const TextStyle(
                      fontSize: 12,
                      fontFamily: 'monospace',
                    )),
              ],
            ),
          ),
      ],
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 4, vertical: 2),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            Text(
              current,
              style: TextStyle(
                fontSize: 11,
                color: BiuTokens.textMuted,
                fontFamily: 'monospace',
              ),
            ),
            Icon(Icons.expand_more, size: 12, color: BiuTokens.textMuted),
          ],
        ),
      ),
    );
  }
}

/// 长行换行开关 —— 同样的 11px 文字 + textMuted 配色，active 走 purple。
class _WrapToggle extends StatelessWidget {
  const _WrapToggle({required this.active, required this.onTap});
  final bool active;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return InkWell(
      onTap: onTap,
      borderRadius: BorderRadius.circular(4),
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(
              active ? Icons.wrap_text : Icons.swap_horiz,
              size: 12,
              color: active ? BiuTokens.purple : BiuTokens.textMuted,
            ),
            const SizedBox(width: 4),
            Text(
              active ? 'Wrap' : 'NoWrap',
              style: TextStyle(
                fontSize: 11,
                color: active ? BiuTokens.purple : BiuTokens.textMuted,
              ),
            ),
          ],
        ),
      ),
    );
  }
}

/// 行号开关 —— 跟其他 header 按钮一致样式（icon + 11px 文字 + textMuted）。
class _LineNumbersToggle extends StatelessWidget {
  const _LineNumbersToggle({required this.active, required this.onTap});
  final bool active;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return InkWell(
      onTap: onTap,
      borderRadius: BorderRadius.circular(4),
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(
              active ? Icons.format_list_numbered : Icons.format_list_numbered_rtl,
              size: 12,
              color: active ? BiuTokens.purple : BiuTokens.textMuted,
            ),
            const SizedBox(width: 4),
            Text(
              active ? '隐行号' : '显行号',
              style: TextStyle(
                fontSize: 11,
                color: active ? BiuTokens.purple : BiuTokens.textMuted,
              ),
            ),
          ],
        ),
      ),
    );
  }
}

/// 把代码块保存为本地文件 —— file_selector 跨端拿目标路径，按 lang 自动
/// 推荐扩展名（dart/py/ts/...），失败 / 取消都 fail-silent。
class _SaveButton extends StatelessWidget {
  const _SaveButton({required this.lang, required this.code});
  final String lang;
  final String code;

  @override
  Widget build(BuildContext context) {
    return InkWell(
      onTap: () => _save(context, lang: lang, code: code),
      borderRadius: BorderRadius.circular(4),
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(Icons.download_outlined,
                size: 12, color: BiuTokens.textMuted),
            const SizedBox(width: 4),
            Text('Save',
                style: TextStyle(fontSize: 11, color: BiuTokens.textMuted)),
          ],
        ),
      ),
    );
  }
}

Future<void> _save(BuildContext context,
    {required String lang, required String code}) async {
  final messenger = ScaffoldMessenger.of(context);
  final ext = _extForLang(lang);
  final stamp = DateTime.now().millisecondsSinceEpoch;
  final filename = 'snippet-$stamp.$ext';
  try {
    final loc = await getSaveLocation(
      suggestedName: filename,
      acceptedTypeGroups: [
        XTypeGroup(label: lang, extensions: [ext]),
      ],
    );
    if (loc == null) return;
    final file = XFile.fromData(
      Uint8List.fromList(utf8.encode(code)),
      name: filename,
      mimeType: 'text/plain',
    );
    await file.saveTo(loc.path);
    messenger.showSnackBar(SnackBar(
      content: Text('已保存 $filename'),
      duration: const Duration(seconds: 2),
    ));
  } catch (e) {
    messenger.showSnackBar(SnackBar(content: Text('保存失败: $e')));
  }
}

/// 常见 highlight.js language id → 文件扩展名映射；漏的回 'txt'。
String _extForLang(String lang) {
  final l = lang.toLowerCase();
  return switch (l) {
    'dart' => 'dart',
    'python' || 'py' => 'py',
    'javascript' || 'js' => 'js',
    'typescript' || 'ts' => 'ts',
    'tsx' => 'tsx',
    'jsx' => 'jsx',
    'go' || 'golang' => 'go',
    'rust' || 'rs' => 'rs',
    'java' => 'java',
    'kotlin' || 'kt' => 'kt',
    'swift' => 'swift',
    'c' => 'c',
    'cpp' || 'c++' || 'cc' || 'cxx' => 'cpp',
    'cs' || 'csharp' => 'cs',
    'rb' || 'ruby' => 'rb',
    'php' => 'php',
    'shell' || 'bash' || 'sh' || 'zsh' => 'sh',
    'sql' => 'sql',
    'json' => 'json',
    'yaml' || 'yml' => 'yaml',
    'xml' => 'xml',
    'html' => 'html',
    'css' => 'css',
    'scss' || 'sass' => 'scss',
    'markdown' || 'md' => 'md',
    'lua' => 'lua',
    'ini' || 'toml' => l == 'toml' ? 'toml' : 'ini',
    'dockerfile' => 'Dockerfile',
    'makefile' => 'Makefile',
    _ => 'txt',
  };
}

/// 全屏查看按钮 — 点击后弹一个 80% 屏幕宽高的 dialog, 让用户看长代码
/// 不被消息列宽 720px 限制。复用 HighlightView, 主题一致。
class _ExpandButton extends StatelessWidget {
  const _ExpandButton({required this.lang, required this.code});
  final String lang;
  final String code;

  @override
  Widget build(BuildContext context) {
    return InkWell(
      onTap: () => _showFullscreen(context, lang: lang, code: code),
      borderRadius: BorderRadius.circular(4),
      child: Padding(
        padding: EdgeInsets.symmetric(horizontal: 6, vertical: 2),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(Icons.fullscreen, size: 12, color: BiuTokens.textMuted),
            SizedBox(width: 4),
            Text('Expand',
                style: TextStyle(fontSize: 11, color: BiuTokens.textMuted)),
          ],
        ),
      ),
    );
  }
}

void _showFullscreen(
  BuildContext context, {
  required String lang,
  required String code,
}) {
  showDialog<void>(
    context: context,
    barrierDismissible: true,
    builder: (ctx) {
      final size = MediaQuery.of(ctx).size;
      return Dialog(
        insetPadding: EdgeInsets.symmetric(
          horizontal: size.width * 0.05,
          vertical: size.height * 0.05,
        ),
        backgroundColor: BiuTokens.surfaceMuted,
        shape: RoundedRectangleBorder(
          borderRadius: BorderRadius.circular(BiuTokens.radiusMd),
        ),
        child: Column(
          children: [
            Container(
              padding: const EdgeInsets.symmetric(
                  horizontal: BiuTokens.space3, vertical: BiuTokens.space2),
              decoration: BoxDecoration(
                border: Border(
                  bottom: BorderSide(color: BiuTokens.borderSubtle),
                ),
              ),
              child: Row(
                children: [
                  Text(lang,
                      style: TextStyle(
                        fontSize: 12,
                        color: BiuTokens.textSecondary,
                        fontFamily: 'monospace',
                      )),
                  const Spacer(),
                  _CopyButton(text: code),
                  IconButton(
                    icon: const Icon(Icons.close, size: 16),
                    tooltip: '关闭 (Esc)',
                    visualDensity: VisualDensity.compact,
                    onPressed: () => Navigator.of(ctx).pop(),
                  ),
                ],
              ),
            ),
            Expanded(
              child: SingleChildScrollView(
                padding: const EdgeInsets.all(BiuTokens.space4),
                child: SingleChildScrollView(
                  scrollDirection: Axis.horizontal,
                  child: HighlightView(
                    code,
                    language: lang,
                    theme: _highlightTheme(),
                    padding: EdgeInsets.zero,
                    textStyle: const TextStyle(
                      fontFamily: 'JetBrains Mono, ui-monospace, monospace',
                      fontSize: 13,
                      height: 1.5,
                    ),
                  ),
                ),
              ),
            ),
          ],
        ),
      );
    },
  );
}

class _CopyButton extends StatefulWidget {
  const _CopyButton({required this.text});
  final String text;
  @override
  State<_CopyButton> createState() => _CopyButtonState();
}

class _CopyButtonState extends State<_CopyButton> {
  bool _copied = false;
  @override
  Widget build(BuildContext context) {
    return InkWell(
      onTap: () async {
        await Clipboard.setData(ClipboardData(text: widget.text));
        if (!mounted) return;
        setState(() => _copied = true);
        Future.delayed(const Duration(milliseconds: 1500), () {
          if (mounted) setState(() => _copied = false);
        });
      },
      borderRadius: BorderRadius.circular(4),
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(_copied ? Icons.check : Icons.copy,
                size: 12, color: BiuTokens.textMuted),
            const SizedBox(width: 4),
            Text(_copied ? 'Copied' : 'Copy',
                style: TextStyle(
                    fontSize: 11, color: BiuTokens.textMuted)),
          ],
        ),
      ),
    );
  }
}

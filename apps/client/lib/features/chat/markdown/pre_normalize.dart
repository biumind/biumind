// Stage 1 — preNormalize: AI 古怪输出修正。
//
// 纯函数 String → String。所有规则按顺序应用; 一条规则的输出是下一条
// 的输入。每条规则都按"幂等"设计 — 跑两次结果一样, 利于增量 streaming
// 多次 normalize 而不抖。
//
// 规则:
//   N1  ~~~ fence → ``` fence (波浪号统一为反引号)
//       — 4+ 反引号 NOT 动 (AI 用 4-tick 外层包 3-tick 内层做 showcase
//         嵌套, 合 CommonMark, splitSegments 自己会按 backtick 计数匹配)
//   N2  HTML <pre><code class="language-X"> 反包裹成 ```X fence
//   N3  fence lang 含尾参 (e.g. "mermaid theme=dark") → 取 first token
//   N4  fence 内容首行就是单独的语言关键字 → 提到 fence lang 上, 内容剥首行
//   N5  trim 尾部空白 (前导保留 — 缩进可能有意义)
//
// 注意: 不在这里干 math placeholder 替换 — math 行内/块都先留给 split,
// split 直接产出 MathSegment 更清晰 (避免双向占位符 round-trip)。

const _kKnownLangs = <String>{
  'mermaid', 'mmd',
  'html', 'htm',
  'svg',
  // 常见编程语言, 用于 N4 的"首行误写为 lang" 检测
  'python', 'py', 'dart', 'javascript', 'js', 'typescript', 'ts',
  'go', 'rust', 'rs', 'java', 'kotlin', 'kt', 'swift', 'cpp', 'c++',
  'c', 'csharp', 'cs', 'ruby', 'rb', 'php', 'shell', 'bash', 'sh',
  'zsh', 'fish', 'powershell', 'ps1', 'sql', 'json', 'yaml', 'yml',
  'toml', 'xml', 'markdown', 'md', 'css', 'scss', 'less',
};

String preNormalize(String input) {
  if (input.isEmpty) return input;
  var out = input;
  out = _n1NormalizeFences(out);
  out = _n2UnwrapHtmlCodePre(out);
  out = _n3StripFenceLangParams(out);
  out = _n4PromoteFirstLineLang(out);
  out = _n5TrimTrailing(out);
  return out;
}

// ─── N1: 波浪号 fence 统一为反引号 ────────────────────────

/// `~~~` fence 转成 ``` (GFM 等价但社区主流是反引号; 减下游分支)。
/// 4+ 反引号 fence **不动** — AI 用 4-tick 外层包 3-tick 内层做嵌套
/// 展示是合 CommonMark 的, splitSegments 按 backtick 计数自己匹配。
String _n1NormalizeFences(String input) {
  return input.replaceAllMapped(
    RegExp(r'^~{3,}', multiLine: true),
    (m) => '```',
  );
}

// ─── N2: HTML <pre><code class="language-X"> 反包裹 ────────

/// AI 偶尔输出:
///   <pre><code class="language-mermaid">
///   sequenceDiagram
///   ...
///   </code></pre>
/// 这种 GFM-extended HTML 其实是要标 lang=mermaid 的代码块。还原成 fence。
String _n2UnwrapHtmlCodePre(String input) {
  final pattern = RegExp(
    r'<pre>\s*<code\s+class="language-([a-zA-Z0-9_+\-]+)">'
    r'([\s\S]*?)'
    r'</code>\s*</pre>',
    multiLine: true,
  );
  return input.replaceAllMapped(pattern, (m) {
    final lang = m.group(1) ?? '';
    final body = (m.group(2) ?? '').trim();
    return '```$lang\n$body\n```';
  });
}

// ─── N3: fence lang 剥参数 ─────────────────────────────────

/// `` ```mermaid theme=dark `` → `` ```mermaid ``
/// 取 first whitespace-separated token; 后面的当注释丢掉。
String _n3StripFenceLangParams(String input) {
  return input.replaceAllMapped(
    RegExp(r'^```([^\s`\n]+)([^\n]*)$', multiLine: true),
    (m) {
      final firstToken = m.group(1) ?? '';
      // 仅当后跟空白参数才剥; ```mermaid 本身不动 (group2 为 '')
      return '```$firstToken';
    },
  );
}

// ─── N4: 内容首行误写为 lang ───────────────────────────────

/// AI 写的:
///   ```
///   mermaid
///   sequenceDiagram
///   ...
///   ```
/// 把内容首行的 `mermaid` 提到 fence lang 位置, 同时从内容里剥掉。
/// 仅当外层 fence 无 lang 且首行 trim 后是已知关键字才动手 — 避免误伤
/// "首行就是叫 python 的代码注释" 这种正常文本。
String _n4PromoteFirstLineLang(String input) {
  // 匹配空 lang 的 fence + 直到下一个 ``` 之间的内容
  final pattern = RegExp(
    r'^```\s*\n([^\n]*)\n([\s\S]*?)(\n```)',
    multiLine: true,
  );
  return input.replaceAllMapped(pattern, (m) {
    final firstLine = (m.group(1) ?? '').trim().toLowerCase();
    final rest = m.group(2) ?? '';
    final closingFence = m.group(3) ?? '';
    if (_kKnownLangs.contains(firstLine)) {
      return '```$firstLine\n$rest$closingFence';
    }
    // 不动
    return m.group(0)!;
  });
}

// ─── N5: trim 尾部空白 ────────────────────────────────────

String _n5TrimTrailing(String input) {
  return input.replaceAll(RegExp(r'\s+$'), '');
}

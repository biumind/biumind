// lib/code_highlight.ts — 轻量代码高亮 tokenizer.
//
// 不引入 highlight.js (~300KB), 自己写 ~250 行覆盖最常见 7 种语言:
//   javascript / typescript / python / go / json / bash / sql
//
// 其他语言退化成 plain — 仍按等宽字体显示, 只是没着色.
//
// Token 类型: keyword / type / builtin / function / string / comment /
// number / plain. 颜色取自 Atom One Dark 调色板, 在 CodeBlock 暗底上易读.
//
// tokenize 算法 — 单遍扫描, 按字符决定走哪条规则:
//   1. 注释 (行/块)
//   2. 字符串 (含转义, 同行结束 or 跨行 template literal)
//   3. 数字 (整/浮/十六进制/科学计数)
//   4. 标识符 → 查 keyword/type/builtin 表, 若后跟 ( 视为 function call
//   5. 其余字符进 plain buffer

export interface Token {
  text: string;
  color: string;
}

const COLORS = {
  keyword: '#c084fc',  // purple-400
  type: '#38bdf8',     // sky-400
  builtin: '#fb923c',  // orange-400
  function: '#60a5fa', // blue-400
  string: '#86efac',   // green-300
  comment: '#64748b',  // slate-500
  number: '#fbbf24',   // amber-400
  plain: '#e2e8f0',    // slate-200
};

interface LangConfig {
  keywords: Set<string>;
  types: Set<string>;
  builtins: Set<string>;
  /** 行注释起始 (// / # / -- ); null 表示无 */
  lineComment: string | null;
  /** 块注释 [start, end]; null 表示无 */
  blockComment: [string, string] | null;
  /** 可作为字符串引号的字符 */
  stringQuotes: Set<string>;
  /** 关键字大小写不敏感 (SQL) */
  caseInsensitive?: boolean;
}

// ── 关键字表 ──────────────────────────────────────────────────────

const JS_KEYWORDS = new Set([
  'async', 'await', 'break', 'case', 'catch', 'class', 'const', 'continue',
  'debugger', 'default', 'delete', 'do', 'else', 'export', 'extends',
  'finally', 'for', 'from', 'function', 'get', 'if', 'import', 'in',
  'instanceof', 'let', 'new', 'of', 'return', 'set', 'static', 'super',
  'switch', 'this', 'throw', 'try', 'typeof', 'var', 'void', 'while',
  'with', 'yield',
]);

const TS_KEYWORDS = new Set([
  ...JS_KEYWORDS,
  'abstract', 'as', 'declare', 'enum', 'implements', 'interface', 'is',
  'keyof', 'module', 'namespace', 'never', 'public', 'private', 'protected',
  'readonly', 'satisfies', 'type',
]);

const TS_TYPES = new Set([
  'string', 'number', 'boolean', 'any', 'void', 'never', 'unknown',
  'object', 'bigint', 'symbol', 'null', 'undefined',
]);

const JS_BUILTINS = new Set([
  'console', 'window', 'document', 'globalThis', 'process', 'Buffer',
  'Promise', 'Array', 'Object', 'String', 'Number', 'Boolean', 'Math',
  'JSON', 'Date', 'RegExp', 'Error', 'Map', 'Set', 'Symbol',
  'true', 'false', 'null', 'undefined', 'NaN', 'Infinity',
]);

const PY_KEYWORDS = new Set([
  'False', 'None', 'True', 'and', 'as', 'assert', 'async', 'await',
  'break', 'class', 'continue', 'def', 'del', 'elif', 'else', 'except',
  'finally', 'for', 'from', 'global', 'if', 'import', 'in', 'is',
  'lambda', 'nonlocal', 'not', 'or', 'pass', 'raise', 'return', 'try',
  'while', 'with', 'yield',
]);

const PY_BUILTINS = new Set([
  'print', 'len', 'range', 'str', 'int', 'float', 'bool', 'list', 'dict',
  'tuple', 'set', 'type', 'isinstance', 'issubclass', 'super', 'self',
  'cls', 'open', 'input', 'sum', 'min', 'max', 'abs', 'round', 'sorted',
  'enumerate', 'zip', 'map', 'filter', 'any', 'all',
]);

const GO_KEYWORDS = new Set([
  'break', 'case', 'chan', 'const', 'continue', 'default', 'defer',
  'else', 'fallthrough', 'for', 'func', 'go', 'goto', 'if', 'import',
  'interface', 'map', 'package', 'range', 'return', 'select', 'struct',
  'switch', 'type', 'var',
]);

const GO_TYPES = new Set([
  'bool', 'byte', 'complex64', 'complex128', 'error', 'float32', 'float64',
  'int', 'int8', 'int16', 'int32', 'int64', 'rune', 'string', 'uint',
  'uint8', 'uint16', 'uint32', 'uint64', 'uintptr', 'any',
]);

const GO_BUILTINS = new Set([
  'append', 'cap', 'close', 'copy', 'delete', 'len', 'make', 'new',
  'panic', 'print', 'println', 'recover', 'true', 'false', 'nil', 'iota',
]);

const BASH_KEYWORDS = new Set([
  'if', 'then', 'else', 'elif', 'fi', 'case', 'esac', 'for', 'in', 'do',
  'done', 'while', 'until', 'function', 'return', 'select', 'break',
  'continue', 'export', 'local', 'declare', 'readonly', 'set', 'unset',
  'echo', 'printf', 'read', 'source', 'exit', 'cd', 'pwd',
]);

const SQL_KEYWORDS = new Set([
  'select', 'from', 'where', 'and', 'or', 'not', 'in', 'is', 'null',
  'like', 'between', 'as', 'distinct', 'group', 'by', 'order', 'having',
  'limit', 'offset', 'join', 'inner', 'left', 'right', 'outer', 'cross',
  'on', 'union', 'all', 'insert', 'into', 'values', 'update', 'set',
  'delete', 'create', 'table', 'drop', 'alter', 'index', 'view', 'case',
  'when', 'then', 'else', 'end', 'true', 'false', 'asc', 'desc',
  'primary', 'key', 'foreign', 'references', 'default', 'unique',
]);

const JSON_BUILTINS = new Set(['true', 'false', 'null']);

// ── 语言配置 ──────────────────────────────────────────────────────

const LANG_CONFIGS: Record<string, LangConfig> = {
  javascript: {
    keywords: JS_KEYWORDS,
    types: new Set(),
    builtins: JS_BUILTINS,
    lineComment: '//',
    blockComment: ['/*', '*/'],
    stringQuotes: new Set(['"', "'", '`']),
  },
  typescript: {
    keywords: TS_KEYWORDS,
    types: TS_TYPES,
    builtins: JS_BUILTINS,
    lineComment: '//',
    blockComment: ['/*', '*/'],
    stringQuotes: new Set(['"', "'", '`']),
  },
  python: {
    keywords: PY_KEYWORDS,
    types: new Set(),
    builtins: PY_BUILTINS,
    lineComment: '#',
    blockComment: null,
    stringQuotes: new Set(['"', "'"]),
  },
  go: {
    keywords: GO_KEYWORDS,
    types: GO_TYPES,
    builtins: GO_BUILTINS,
    lineComment: '//',
    blockComment: ['/*', '*/'],
    stringQuotes: new Set(['"', '`']),
  },
  bash: {
    keywords: BASH_KEYWORDS,
    types: new Set(),
    builtins: new Set(),
    lineComment: '#',
    blockComment: null,
    stringQuotes: new Set(['"', "'"]),
  },
  sql: {
    keywords: SQL_KEYWORDS,
    types: new Set(),
    builtins: new Set(),
    lineComment: '--',
    blockComment: ['/*', '*/'],
    stringQuotes: new Set(["'"]),
    caseInsensitive: true,
  },
  json: {
    keywords: new Set(),
    types: new Set(),
    builtins: JSON_BUILTINS,
    lineComment: null,
    blockComment: null,
    stringQuotes: new Set(['"']),
  },
};

// 语言别名归一化 — js → javascript 等
const LANG_ALIASES: Record<string, string> = {
  js: 'javascript',
  jsx: 'javascript',
  ts: 'typescript',
  tsx: 'typescript',
  py: 'python',
  golang: 'go',
  sh: 'bash',
  shell: 'bash',
  zsh: 'bash',
};

function getConfig(lang: string): LangConfig | null {
  if (!lang) return null;
  const norm = (LANG_ALIASES[lang.toLowerCase()] || lang.toLowerCase());
  return LANG_CONFIGS[norm] || null;
}

// ── 公共入口 ──────────────────────────────────────────────────────

export function highlightCode(code: string, lang: string): Token[] {
  const cfg = getConfig(lang);
  if (!cfg) {
    // 不支持的语言: 整段返回 plain
    return code ? [{ text: code, color: COLORS.plain }] : [];
  }
  return tokenize(code, cfg);
}

// ── tokenizer 主循环 ──────────────────────────────────────────────

function tokenize(code: string, cfg: LangConfig): Token[] {
  const tokens: Token[] = [];
  let buf = '';
  let i = 0;

  const flushPlain = () => {
    if (buf) {
      tokens.push({ text: buf, color: COLORS.plain });
      buf = '';
    }
  };

  while (i < code.length) {
    const ch = code[i];

    // 行注释
    if (cfg.lineComment && code.startsWith(cfg.lineComment, i)) {
      flushPlain();
      const end = code.indexOf('\n', i);
      const stop = end === -1 ? code.length : end;
      tokens.push({ text: code.slice(i, stop), color: COLORS.comment });
      i = stop;
      continue;
    }

    // 块注释
    if (cfg.blockComment && code.startsWith(cfg.blockComment[0], i)) {
      flushPlain();
      const [, closeMark] = cfg.blockComment;
      const end = code.indexOf(closeMark, i + cfg.blockComment[0].length);
      const stop = end === -1 ? code.length : end + closeMark.length;
      tokens.push({ text: code.slice(i, stop), color: COLORS.comment });
      i = stop;
      continue;
    }

    // 字符串
    if (cfg.stringQuotes.has(ch)) {
      flushPlain();
      const quote = ch;
      let j = i + 1;
      while (j < code.length) {
        const c = code[j];
        if (c === '\\' && j + 1 < code.length) {
          j += 2;
          continue;
        }
        if (c === quote) {
          j++;
          break;
        }
        // 单/双引号字符串遇到换行强制结束 (避免吞下整段)
        if (c === '\n' && quote !== '`') {
          break;
        }
        j++;
      }
      tokens.push({ text: code.slice(i, j), color: COLORS.string });
      i = j;
      continue;
    }

    // 数字 (避免标识符内的数字: 上一字符不是字母/下划线)
    if (
      /[0-9]/.test(ch) &&
      (i === 0 || !/[a-zA-Z_$]/.test(code[i - 1]))
    ) {
      flushPlain();
      let j = i;
      // 整数 / 浮点 / 十六进制 / 科学计数
      while (
        j < code.length &&
        /[0-9a-fA-FxXoObB._]/.test(code[j])
      ) {
        j++;
      }
      // 科学计数 e+10 / e-5
      if (j < code.length && /[eE]/.test(code[j - 1]) && /[+-]/.test(code[j])) {
        j++;
        while (j < code.length && /[0-9]/.test(code[j])) j++;
      }
      tokens.push({ text: code.slice(i, j), color: COLORS.number });
      i = j;
      continue;
    }

    // 标识符
    if (/[a-zA-Z_$]/.test(ch)) {
      flushPlain();
      let j = i;
      while (j < code.length && /[a-zA-Z0-9_$]/.test(code[j])) j++;
      const word = code.slice(i, j);
      const lookup = cfg.caseInsensitive ? word.toLowerCase() : word;
      let color = COLORS.plain;
      if (cfg.keywords.has(lookup)) {
        color = COLORS.keyword;
      } else if (cfg.types.has(lookup)) {
        color = COLORS.type;
      } else if (cfg.builtins.has(lookup)) {
        color = COLORS.builtin;
      } else if (code[j] === '(') {
        color = COLORS.function;
      }
      tokens.push({ text: word, color });
      i = j;
      continue;
    }

    buf += ch;
    i++;
  }
  flushPlain();
  return tokens;
}

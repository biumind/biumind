// zh-Hans 字典 —— key 是 crepe / @milkdown/components 源码里的英文原文
// （msgid 风格，Joplin 同款思路：升级内核时直接对照源码 grep 结果核对）。
// 新增语言 = 新增一个同结构文件并在 LOCALES 里登记，mapper 不用动。
export const zhHans: Record<string, string> = {
  // ── block-edit（/ 斜杠菜单 + 块拖拽手柄菜单）──
  Text: '文本',
  Paragraph: '正文',
  List: '列表',
  Advanced: '高级',
  'Heading 1': '标题 1',
  'Heading 2': '标题 2',
  'Heading 3': '标题 3',
  'Heading 4': '标题 4',
  'Heading 5': '标题 5',
  'Heading 6': '标题 6',
  Quote: '引用',
  Divider: '分割线',
  'Bullet List': '无序列表',
  'Ordered List': '有序列表',
  'Task List': '任务列表',
  Image: '图片',
  Code: '代码块',
  Table: '表格',
  Math: '数学公式',
  LaTeX: 'LaTeX',

  // ── placeholder ──
  'Please enter...': '请输入…',

  // ── link-tooltip ──
  'Paste link...': '粘贴链接…',

  // ── image-block ──
  Upload: '上传',
  'Upload file': '上传文件',
  Confirm: '确认',
  'Write Image Caption': '填写图片说明',
  'or paste link': '或粘贴链接',

  // ── code-mirror（含 @milkdown/components code-block 兜底）──
  'Search language': '搜索语言',
  'Search languages…': '搜索语言…',
  Copy: '复制',
  'No result': '无结果',
  Edit: '编辑',
  Hide: '隐藏',
  Preview: '预览',
  'Loading...': '加载中…',

  // ── toolbar（选中文字弹出的浮动工具栏）──
  Bold: '加粗',
  Italic: '斜体',
  Strikethrough: '删除线',
  'Inline code': '行内代码',
  'Inline math': '行内公式',
  Link: '链接',

  // ── 自绘右键菜单（src/context-menu/model.ts 注册表）──
  Cut: '剪切',
  Paste: '粘贴',
  'Paste as Plain Text': '粘贴为纯文本',
  'Select All': '全选',
  'Convert to': '转换为',
  Insert: '插入',
  Timestamp: '时间戳',
  'Open Link': '打开链接',
  'Copy Link': '复制链接',
  'Remove Link': '移除链接',
  'Delete Table': '删除表格',
  'Replace Image...': '替换图片…',
  'Edit Caption': '编辑说明',
  'Copy Image': '复制图片',
  Delete: '删除',
  'Copy Code': '复制代码',
  'Ask AI': '询问 AI',
  'Edit with AI': '用 AI 编辑选区',
  More: '更多',
  Back: '返回',
}

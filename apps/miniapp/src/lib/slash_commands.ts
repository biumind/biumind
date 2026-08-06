// lib/slash_commands.ts — 输入 / 弹快捷指令面板.
//
// 用户输入 "/" 时 chat 页弹 ActionSheet, 选完把 prompt 模板填入 draft.
// 模板里的 {input} 占位符会保留, 提示用户继续在后面写实际内容.
//
// 这一层只是 prompt 文本组装; 真正的执行还是走标准 streamMessage,
// 不需要后端额外接口.

export interface SlashCommand {
  /** 触发关键字, 不含 / */
  key: string;
  /** 显示标签 */
  label: string;
  /** 简短说明 */
  hint: string;
  /** 应用后填入 draft 的模板 */
  template: string;
}

export const SLASH_COMMANDS: SlashCommand[] = [
  {
    key: 'summary',
    label: '总结',
    hint: '提炼要点 + 行动项',
    template: '请帮我总结以下内容的核心要点和可执行的行动项:\n\n',
  },
  {
    key: 'translate',
    label: '翻译',
    hint: '中英互译 (自动判断方向)',
    template:
      '请把以下内容翻译成对应语言 (中文→英文 或 英文→中文), 保持原意和语气:\n\n',
  },
  {
    key: 'explain',
    label: '解释',
    hint: '用通俗语言讲清楚',
    template: '请用通俗易懂的方式解释下面这段内容, 多用类比和例子:\n\n',
  },
  {
    key: 'rewrite',
    label: '润色',
    hint: '改写成更自然的中文',
    template:
      '请把以下内容润色, 改成更地道流畅的中文表达, 保留原意和数据:\n\n',
  },
  {
    key: 'continue',
    label: '续写',
    hint: '基于上文继续写',
    template: '请基于上文继续写, 保持风格和逻辑连贯:\n\n',
  },
  {
    key: 'critique',
    label: '挑刺',
    hint: '指出问题和改进点',
    template:
      '请站在挑剔的读者角度, 指出以下内容的逻辑漏洞 / 用词问题 / 改进点:\n\n',
  },
];

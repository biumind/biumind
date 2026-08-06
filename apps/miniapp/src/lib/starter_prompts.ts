// lib/starter_prompts.ts — Hero 6 张推荐卡数据.
//
// 与 Flutter 端 (apps/client/.../starter_prompts.dart) 同步: id / emoji /
// title / hint / prompt 一一对应, 文案直接复用 zh.arb 默认值.
//
// 顺序就是 Hero 上 2x3 grid 的展示顺序.
// prompt 字段是用户点击后 prefill 进 draft 的实际文本; 含代码块占位时
// 让用户继续在原位输入.

export interface StarterPrompt {
  id: string;
  emoji: string;
  title: string;
  hint: string;
  prompt: string;
  /** emoji 背景色 — 浅色调 */
  bg: string;
}

export const STARTER_PROMPTS: StarterPrompt[] = [
  {
    id: 'writing',
    emoji: '✍️',
    title: '写作助手',
    hint: '帮我润色一段文字',
    prompt: '请帮我润色以下文字, 让它更专业:\n\n',
    bg: '#ede9fe', // purple-100
  },
  {
    id: 'code',
    emoji: '💻',
    title: '代码 Review',
    hint: '审查我贴的代码',
    prompt: '请帮我 review 以下代码, 找出可改进的地方:\n\n```\n\n```',
    bg: '#dbeafe', // blue-100
  },
  {
    id: 'research',
    emoji: '🔍',
    title: '深度研究',
    hint: '展开一个主题',
    prompt: '请深入分析以下主题, 给出多角度观点:\n\n',
    bg: '#cffafe', // cyan-100
  },
  {
    id: 'translate',
    emoji: '🌐',
    title: '翻译',
    hint: '中英互译',
    prompt: '请翻译以下内容, 保持原意和语气:\n\n',
    bg: '#d1fae5', // emerald-100
  },
  {
    id: 'data',
    emoji: '📊',
    title: '数据分析',
    hint: '分析数据',
    prompt: '请帮我分析以下数据, 给出关键洞察:\n\n',
    bg: '#ffedd5', // orange-100
  },
  {
    id: 'ideas',
    emoji: '🎨',
    title: '头脑风暴',
    hint: '生成想法',
    prompt: '请就以下话题给我 10 个创意点子:\n\n',
    bg: '#fce7f3', // pink-100
  },
];

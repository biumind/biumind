<script setup lang="ts">
import { ref, nextTick, onBeforeUnmount, computed, watch } from 'vue';
import { onShow, onHide } from '@dcloudio/uni-app';
import {
  ensureThread,
  listMessages,
  listThreads,
  patchThread,
  streamMessage,
  type ChatMessage,
  type StreamHandle,
  type Thread,
} from '@/data/api/chat';
import { loadModelEntries, type ModelEntry } from '@/data/api/models';
import {
  loadPreferredModel,
  savePreferredModel,
} from '@/lib/preferred_model';
import { modelDisplayName } from '@/lib/provider_catalog';
import { useFontScale, useFontScaleStyle, ratioFor } from '@/lib/font_scale';

const fontStyle = useFontScaleStyle();
const fontScale = useFontScale();
import { isLoggedIn } from '@/core/token_manager';
import { parseMessageSegments, type MessageSegment } from '@/lib/markdown';
import CodeBlock from '@/components/CodeBlock.vue';
import { loadDraft, saveDraft, clearDraft } from '@/lib/draft_store';
import { formatChatTime, shouldShowTimeBefore } from '@/lib/time_format';
import { SLASH_COMMANDS } from '@/lib/slash_commands';
import ShareCard from '@/components/ShareCard.vue';
import HeroView from '@/components/HeroView.vue';
import ModelPicker from '@/components/ModelPicker.vue';
import type { CardMessage } from '@/lib/share_card';

const shareCardRef = ref<InstanceType<typeof ShareCard> | null>(null);
const recentThreads = ref<Thread[]>([]);

// ── 模型选择 ─────────────────────────────────────────────────────
// currentModel: 用户选中的 model id (持久化在 preferred_model storage)
// modelEntries: 后端动态拉的可用模型 (含 catalog fallback)
const currentModel = ref<string>(loadPreferredModel());
const modelEntries = ref<ModelEntry[]>([]);

async function reloadModels() {
  try {
    modelEntries.value = await loadModelEntries();
    // 如果当前 preferred 不在最新 entries 里, 切到第一项 (避免选了个已禁用的)
    if (
      modelEntries.value.length > 0 &&
      !modelEntries.value.find((e) => e.modelId === currentModel.value)
    ) {
      currentModel.value = modelEntries.value[0].modelId;
      savePreferredModel(currentModel.value);
    }
  } catch {
    // 静默失败 — picker 会显示原 catalog fallback
  }
}

async function onPickModel(modelId: string) {
  if (modelId === currentModel.value) return;
  const old = currentModel.value;
  currentModel.value = modelId;
  savePreferredModel(modelId);

  // 在已有 thread 内 → 同步切到云端, 让下条消息走新模型
  if (threadId.value) {
    try {
      await patchThread(threadId.value, { model: modelId });
      uni.showToast({ title: '已切换模型', icon: 'success' });
    } catch (e: unknown) {
      // 回滚
      currentModel.value = old;
      savePreferredModel(old);
      const msg = e instanceof Error ? e.message : String(e);
      uni.showToast({ title: '切换失败: ' + msg, icon: 'none' });
    }
  } else {
    uni.showToast({ title: '已切换默认模型', icon: 'none' });
  }
}

const PENDING_THREAD_KEY = 'biumind.pending_thread_id';

// HeroView 接管空状态. 仅在 streaming 错误等极端情况下用占位; 一般不再
// 把欢迎语塞入 messages, 避免 hero / message list 双显或被 share_card 抓到.
const FALLBACK_HINT = '你好, 我是 BiuMind. 输入消息开始对话.';

const messages = ref<ChatMessage[]>([]);
const draft = ref('');
const sending = ref(false);
const threadId = ref<string | undefined>(undefined);
const loadingHistory = ref(false);
const scrollTop = ref(0);
// 跳到底部浮按 — scroll-view scroll 事件中算 (scrollHeight - scrollTop - clientHeight),
// 离底 > 300rpx (≈ 150pt) 时显示
const showJumpBottom = ref(false);
let activeStream: StreamHandle | null = null;
const streamingIdx = ref<number>(-1);

// segments 缓存 — 流式 splice 替换会建新对象, WeakMap 自动失效.
// 切成 [markdown, code, markdown, ...] 交错段; markdown 段走 rich-text,
// code 段走 CodeBlock 组件 (支持复制按钮 + 语法高亮).
//
// 字号档位变化时 cache.ratio !== current → 重 parse, 让 rich-text inline
// style 里的字号跟随档位.
interface CachedSeg {
  ratio: number;
  segs: MessageSegment[];
}
const segCache = new WeakMap<ChatMessage, CachedSeg>();
function segs(m: ChatMessage): MessageSegment[] {
  const ratio = ratioFor(fontScale.value);
  let cached = segCache.get(m);
  if (!cached || cached.ratio !== ratio) {
    cached = { ratio, segs: parseMessageSegments(m.content || '', ratio) };
    segCache.set(m, cached);
  }
  return cached.segs;
}

const lastAssistantIdx = computed(() => {
  for (let i = messages.value.length - 1; i >= 0; i--) {
    if (messages.value[i].role === 'assistant' && messages.value[i].id) return i;
  }
  return -1;
});

// ── 时间分组 ──────────────────────────────────────────────────
// 把 messages 转成 [time-bar, msg, msg, time-bar, msg ...] 给模板渲染.
interface DisplayItem {
  type: 'time' | 'msg';
  time?: string;
  ts?: number;
  msg?: ChatMessage;
  msgIndex?: number;
}
const display = computed<DisplayItem[]>(() => {
  const items: DisplayItem[] = [];
  let prevTs = 0;
  messages.value.forEach((m, idx) => {
    const ts = m.createdAt || 0;
    if (ts && shouldShowTimeBefore(prevTs, ts)) {
      items.push({ type: 'time', time: formatChatTime(ts), ts });
    }
    items.push({ type: 'msg', msg: m, msgIndex: idx });
    if (ts) prevTs = ts;
  });
  return items;
});

// ── 生命周期 ──────────────────────────────────────────────────

onShow(async () => {
  if (!isLoggedIn()) {
    uni.redirectTo({ url: '/pages/me/login' });
    return;
  }

  let pending = '';
  try {
    pending = uni.getStorageSync(PENDING_THREAD_KEY) || '';
    if (pending) uni.removeStorageSync(PENDING_THREAD_KEY);
  } catch {
    /* noop */
  }

  if (pending && pending !== threadId.value) {
    threadId.value = pending;
    await loadHistory(pending);
  }
  // 没有 pending 且无 thread 时, 让 HeroView 接管空状态 (不再塞 WELCOME)

  // 恢复草稿 (按 thread 隔离)
  draft.value = loadDraft(threadId.value);

  // 拉最近会话给 Hero "最近会话" 区
  reloadRecents();
  // 拉可用模型清单 (动态后端 + catalog 兜底)
  reloadModels();
});

async function reloadRecents() {
  try {
    const list = await listThreads();
    recentThreads.value = list;
  } catch {
    // 静默失败 — Hero 会显示 "还没有会话"
    recentThreads.value = [];
  }
}

onHide(() => {
  // 离开页面时持久化 draft (H5 路由切换 + 小程序 hide 都触发)
  saveDraft(threadId.value, draft.value);
});

onBeforeUnmount(() => {
  saveDraft(threadId.value, draft.value);
  activeStream?.cancel();
  activeStream = null;
});

// thread 切换时草稿也跟着切
watch(threadId, (newId, oldId) => {
  if (newId !== oldId) {
    saveDraft(oldId, draft.value);
    draft.value = loadDraft(newId);
  }
});

async function loadHistory(tid: string) {
  loadingHistory.value = true;
  try {
    const list = await listMessages(tid);
    // 历史为空时也让 Hero (emptyThread 变体) 接管, 不塞欢迎语
    messages.value = list;
    await scrollBottom();
  } catch (e: unknown) {
    const msg = e instanceof Error ? e.message : String(e);
    messages.value = [
      {
        role: 'assistant',
        content: '⚠️ 加载会话历史失败: ' + msg,
        createdAt: Date.now(),
      },
    ];
  } finally {
    loadingHistory.value = false;
  }
}

// ── 输入 / 快捷指令 ────────────────────────────────────────────

function onDraftInput(e: { detail: { value: string } }) {
  const v = e.detail.value;
  // 用户刚敲入 "/" 单字符 — 弹快捷指令面板
  if (v === '/' && draft.value !== '/') {
    showSlashMenu();
    return;
  }
  draft.value = v;
}

function showSlashMenu() {
  uni.showActionSheet({
    itemList: SLASH_COMMANDS.map((c) => c.label + ' — ' + c.hint),
    success: (res) => {
      const cmd = SLASH_COMMANDS[res.tapIndex];
      if (cmd) {
        draft.value = cmd.template;
      } else {
        draft.value = '';
      }
    },
    fail: () => {
      // 用户取消 (例如点遮罩或返回键) — 把 / 也一并清掉
      draft.value = '';
    },
  });
}

// ── 发送 ──────────────────────────────────────────────────────

async function onSend() {
  const text = draft.value.trim();
  if (!text || sending.value) return;
  await runUserPrompt(text);
}

async function runUserPrompt(text: string) {
  sending.value = true;
  draft.value = '';
  clearDraft(threadId.value);

  const userTs = Date.now();
  const startedAt = userTs;            // 流式响应总时长起点
  const usedModel = currentModel.value; // 锁定本次发送用的模型 (避免 race)

  messages.value.push({ role: 'user', content: text, createdAt: userTs });
  const assistantIdx = messages.value.length;
  messages.value.push({
    role: 'assistant',
    content: '',
    createdAt: userTs + 1,
    model: usedModel,
  });
  streamingIdx.value = assistantIdx;
  await scrollBottom();

  try {
    const tid = await ensureThread(threadId.value, text, usedModel);
    threadId.value = tid;

    activeStream = streamMessage(
      tid,
      text,
      {
        onAssistantStart: (m) => {
          const cur = messages.value[assistantIdx];
          if (cur) {
            // 服务端如果在 assistant_message 事件返回 model, 用它覆盖
            // (例如官方渠道实际选了哪个具体模型)
            const sm = (m as ChatMessage).model;
            messages.value.splice(assistantIdx, 1, {
              ...cur,
              id: m.id,
              ...(sm ? { model: sm } : {}),
            });
          }
        },
        onDelta: (chunk) => {
          const cur = messages.value[assistantIdx];
          if (cur) {
            messages.value.splice(assistantIdx, 1, {
              ...cur,
              content: (cur.content || '') + chunk,
            });
          }
          scrollBottom();
        },
        onDone: () => {
          const cur = messages.value[assistantIdx];
          if (cur) {
            messages.value.splice(assistantIdx, 1, {
              ...cur,
              elapsedMs: Date.now() - startedAt,
            });
          }
          sending.value = false;
          streamingIdx.value = -1;
          activeStream = null;
          scrollBottom();
        },
        onError: (msg) => {
          const cur = messages.value[assistantIdx];
          const friendly = friendlyError(msg);
          if (cur) {
            const newContent = cur.content
              ? cur.content + '\n\n⚠️ ' + friendly
              : '⚠️ ' + friendly;
            messages.value.splice(assistantIdx, 1, {
              ...cur,
              content: newContent,
              failedPrompt: text,
              elapsedMs: Date.now() - startedAt,
            });
          }
          sending.value = false;
          streamingIdx.value = -1;
          activeStream = null;
          scrollBottom();
        },
      },
      usedModel,
    );
  } catch (e: unknown) {
    const msg = e instanceof Error ? e.message : String(e);
    messages.value.splice(assistantIdx, 1, {
      role: 'assistant',
      content: '⚠️ 发送失败: ' + friendlyError(msg),
      createdAt: Date.now(),
      model: usedModel,
      failedPrompt: text,
    });
    sending.value = false;
    streamingIdx.value = -1;
    activeStream = null;
    await scrollBottom();
  }
}

// ── msg-meta 显示 helper ────────────────────────────────────────

function modelDisplay(modelId: string | undefined): string {
  if (!modelId) return '';
  if (modelId === 'biumind-default') return 'BiuMind';
  return modelDisplayName(modelId);
}

function formatElapsed(ms: number | undefined): string {
  if (!ms || ms < 0) return '';
  if (ms < 1000) return ms + 'ms';
  if (ms < 60_000) return (ms / 1000).toFixed(1) + 's';
  const m = Math.floor(ms / 60_000);
  const s = Math.floor((ms % 60_000) / 1000);
  return m + 'm ' + s + 's';
}

async function onRetryFailed(idx: number) {
  const m = messages.value[idx];
  if (!m?.failedPrompt) return;
  // 删失败的 (assistant) + 上一条 user, 重发原 prompt
  let userIdx = -1;
  for (let i = idx - 1; i >= 0; i--) {
    if (messages.value[i].role === 'user') {
      userIdx = i;
      break;
    }
  }
  const prompt = m.failedPrompt;
  if (userIdx >= 0) {
    messages.value.splice(userIdx, idx - userIdx + 1);
  } else {
    messages.value.splice(idx, 1);
  }
  await runUserPrompt(prompt);
}

function friendlyError(raw: string): string {
  if (!raw) return '网络异常, 请检查后重试';
  if (/timeout|timed out/i.test(raw)) return '请求超时, 网络较慢';
  if (/abort/i.test(raw)) return '已取消';
  if (/network|fail|ENOTFOUND|ECONN/i.test(raw)) return '网络异常, 请检查后重试';
  if (/no_model/i.test(raw)) return '会话未配置模型, 请新建会话';
  if (/unauth|401|expired/i.test(raw)) return '登录已过期, 请重新登录';
  return raw;
}

function onCancel() {
  activeStream?.cancel();
  activeStream = null;
  sending.value = false;
  streamingIdx.value = -1;
}

async function scrollBottom() {
  await nextTick();
  scrollTop.value = 99999;
  showJumpBottom.value = false;
}

// scroll-view 滚动事件 — detail 含 scrollTop / scrollHeight, 拿不到 clientHeight,
// 用一个粗略阈值: 当 scrollHeight - scrollTop > 视口高度估值 + 阈值时认为离底.
// 视口估算: window 高度通过 system info 取一次, 每次滚动复用.
let _viewportH = 0;
function ensureViewport(): number {
  if (_viewportH > 0) return _viewportH;
  try {
    const info = uni.getSystemInfoSync();
    _viewportH = info.windowHeight || 600;
  } catch {
    _viewportH = 600;
  }
  return _viewportH;
}

function onMessagesScroll(e: {
  detail: { scrollTop: number; scrollHeight?: number };
}) {
  const st = e.detail.scrollTop || 0;
  const sh = e.detail.scrollHeight || 0;
  const vh = ensureViewport();
  // distance to bottom = scrollHeight - (scrollTop + viewportHeight)
  const distance = sh - (st + vh);
  // 阈值 150pt ≈ 视觉两个气泡高度
  showJumpBottom.value = distance > 150;
}

// ── 长按消息菜单 ───────────────────────────────────────────────

function onLongPress(idx: number) {
  if (sending.value) return;
  const m = messages.value[idx];
  if (!m || !m.content) return;

  const isLastAssistant = idx === lastAssistantIdx.value;
  const items: { label: string; key: string }[] = [
    { label: '复制', key: 'copy' },
    { label: '引用回复', key: 'quote' },
  ];
  if (m.role === 'assistant' && isLastAssistant) {
    items.push({ label: '重新生成', key: 'regen' });
  }
  if (m.role === 'assistant') {
    items.push({ label: '分享为卡片', key: 'share-card' });
    items.push({ label: '导出整段对话长截图', key: 'share-long' });
  }
  items.push({ label: '收藏到 Wiki', key: 'wiki' });

  uni.showActionSheet({
    itemList: items.map((x) => x.label),
    success: (res) => {
      const action = items[res.tapIndex];
      if (!action) return;
      switch (action.key) {
        case 'copy':
          doCopy(m.content);
          break;
        case 'quote':
          doQuote(m.content);
          break;
        case 'regen':
          doRegenerate(idx);
          break;
        case 'share-card':
          doShareCard(idx);
          break;
        case 'share-long':
          doShareLong();
          break;
        case 'wiki':
          doSaveToWiki(m);
          break;
      }
    },
  });
}

function doCopy(text: string) {
  uni.setClipboardData({
    data: text,
    success: () => uni.showToast({ title: '已复制', icon: 'none' }),
  });
}

function doQuote(text: string) {
  const quoted = text
    .split('\n')
    .map((l) => '> ' + l)
    .join('\n');
  draft.value = quoted + '\n\n' + (draft.value || '');
}

async function doRegenerate(assistantIdx: number) {
  let userIdx = -1;
  for (let i = assistantIdx - 1; i >= 0; i--) {
    if (messages.value[i].role === 'user') {
      userIdx = i;
      break;
    }
  }
  if (userIdx < 0) {
    uni.showToast({ title: '找不到上一条提问', icon: 'none' });
    return;
  }
  const prompt = messages.value[userIdx].content;
  messages.value.splice(userIdx, assistantIdx - userIdx + 1);
  await runUserPrompt(prompt);
}

function doSaveToWiki(m: ChatMessage) {
  uni.showToast({ title: '已加入收藏队列', icon: 'none' });
  console.log('[wiki] save:', m);
}

// ── 分享卡片 / 长截图 ─────────────────────────────────────────────

async function doShareCard(assistantIdx: number) {
  let userIdx = -1;
  for (let i = assistantIdx - 1; i >= 0; i--) {
    if (messages.value[i].role === 'user') {
      userIdx = i;
      break;
    }
  }
  if (userIdx < 0) {
    uni.showToast({ title: '找不到对应提问', icon: 'none' });
    return;
  }
  const prompt = messages.value[userIdx].content;
  const m = messages.value[assistantIdx];
  if (!m?.content) {
    uni.showToast({ title: '消息为空', icon: 'none' });
    return;
  }

  uni.showLoading({ title: '生成中...', mask: true });
  try {
    if (!shareCardRef.value) throw new Error('shareCard 未就绪');
    const path = await shareCardRef.value.exportSingle({
      prompt,
      answer: m.content,
      scene: m.id ? 'm=' + m.id.slice(0, 30) : undefined,
    });
    uni.hideLoading();
    presentSharePath(path);
  } catch (e: unknown) {
    uni.hideLoading();
    const msg = e instanceof Error ? e.message : String(e);
    uni.showToast({ title: '生成失败: ' + msg, icon: 'none' });
  }
}

async function doShareLong() {
  // 过滤错误占位 / 空消息 (Hero 接管后已无 WELCOME_MSG)
  const real = messages.value.filter(
    (m) => m.content && !m.content.startsWith('⚠️'),
  );
  if (real.length < 2) {
    uni.showToast({ title: '还没有足够的对话', icon: 'none' });
    return;
  }
  const cardMsgs: CardMessage[] = real.map((m) => ({
    role: m.role === 'user' ? 'user' : 'assistant',
    content: m.content,
  }));

  uni.showLoading({ title: '生成长截图...', mask: true });
  try {
    if (!shareCardRef.value) throw new Error('shareCard 未就绪');
    const path = await shareCardRef.value.exportLong({
      messages: cardMsgs,
      threadTitle: '与 BiuMind 的对话',
      scene: threadId.value ? 't=' + threadId.value.slice(0, 30) : undefined,
    });
    uni.hideLoading();
    presentSharePath(path);
  } catch (e: unknown) {
    uni.hideLoading();
    const msg = e instanceof Error ? e.message : String(e);
    uni.showToast({ title: '生成失败: ' + msg, icon: 'none' });
  }
}

function presentSharePath(path: string) {
  uni.showActionSheet({
    itemList: ['预览图片', '保存到相册'],
    success: (res) => {
      if (res.tapIndex === 0) {
        uni.previewImage({ urls: [path], current: path });
      } else if (res.tapIndex === 1) {
        saveToAlbum(path);
      }
    },
  });
}

function saveToAlbum(path: string) {
  uni.getSetting({
    success: (r) => {
      const setting = r.authSetting as Record<string, boolean | undefined>;
      if (setting['scope.writePhotosAlbum'] === false) {
        uni.showModal({
          title: '需要相册权限',
          content: '请在系统设置中开启"保存到相册"权限',
          confirmText: '去设置',
          success: (m) => {
            if (m.confirm) uni.openSetting({});
          },
        });
        return;
      }
      doSave(path);
    },
    fail: () => doSave(path),
  });
}

function doSave(path: string) {
  uni.saveImageToPhotosAlbum({
    filePath: path,
    success: () => uni.showToast({ title: '已保存到相册', icon: 'success' }),
    fail: (e: { errMsg?: string }) => {
      const msg = e?.errMsg || '';
      if (/cancel|deny/i.test(msg)) {
        uni.showToast({ title: '已取消', icon: 'none' });
      } else {
        uni.showToast({ title: '保存失败', icon: 'none' });
      }
    },
  });
}

// ── Hero 起点卡 / 最近会话事件 ────────────────────────────────────

function onStarterTap(prompt: string) {
  // prefill draft 让用户继续在占位处写内容; 不直接发送, 因为模板带 "..." 占位
  draft.value = prompt;
  saveDraft(threadId.value, prompt);
}

async function onRecentTap(tid: string) {
  if (tid === threadId.value) return;
  // 切 thread — 先存当前 draft, watch 会接管新 thread 的 draft 切换
  saveDraft(threadId.value, draft.value);
  threadId.value = tid;
  await loadHistory(tid);
}

function onShareAppMessage() {
  return {
    title: 'BiuMind — 你的 AI 第二大脑',
    path: '/pages/chat/index',
  };
}
defineExpose({ onShareAppMessage });
</script>

<template>
  <view class="page" :style="fontStyle">
    <scroll-view
      class="messages"
      scroll-y
      :scroll-top="scrollTop"
      :scroll-with-animation="true"
      @scroll="onMessagesScroll"
    >
      <!-- 空状态 — HeroView 接管 -->
      <HeroView
        v-if="messages.length === 0 && !loadingHistory"
        :variant="threadId ? 'emptyThread' : 'noThread'"
        :current-model="currentModel"
        :recent-threads="recentThreads"
        @starter-tap="onStarterTap"
        @recent-tap="onRecentTap"
      />
      <view v-else-if="loadingHistory" class="loading-hint">
        <text>加载历史中...</text>
      </view>
      <template v-for="item in display">
        <view
          v-if="item.type === 'time'"
          :key="'t-' + item.ts"
          class="time-bar"
        >
          <text class="time-text">{{ item.time }}</text>
        </view>
        <view
          v-else
          :key="item.msg?.id || 'm-' + item.msgIndex"
          :class="[
            'msg',
            item.msg?.role === 'user' ? 'msg-user' : 'msg-assistant',
          ]"
          @longpress="onLongPress(item.msgIndex!)"
        >
          <text v-if="item.msg?.role === 'user'" class="msg-text">{{
            item.msg.content
          }}</text>
          <template v-else-if="item.msg">
            <text v-if="!item.msg.content" class="msg-typing">···</text>
            <template v-else>
              <template v-for="(seg, sIdx) in segs(item.msg)" :key="sIdx">
                <rich-text
                  v-if="seg.kind === 'markdown'"
                  class="msg-rich"
                  :nodes="seg.nodes"
                />
                <CodeBlock
                  v-else
                  :lang="seg.lang"
                  :code="seg.code"
                />
              </template>
            </template>
            <text
              v-if="item.msgIndex === streamingIdx && sending"
              class="cursor"
              >▍</text
            >
            <!-- meta: 模型 · 耗时 · 重试 (仅完整 assistant 消息显示, 流式中跳过) -->
            <view
              v-if="
                item.msg.content &&
                item.msgIndex !== streamingIdx &&
                (item.msg.model || item.msg.elapsedMs || item.msg.failedPrompt)
              "
              class="msg-meta"
            >
              <text v-if="item.msg.model" class="meta-text">{{ modelDisplay(item.msg.model) }}</text>
              <text
                v-if="item.msg.model && item.msg.elapsedMs"
                class="meta-dot"
                > · </text
              >
              <text v-if="item.msg.elapsedMs" class="meta-text">{{ formatElapsed(item.msg.elapsedMs) }}</text>
              <text
                v-if="(item.msg.model || item.msg.elapsedMs) && item.msg.failedPrompt"
                class="meta-dot"
                > · </text
              >
              <view
                v-if="item.msg.failedPrompt"
                class="meta-retry"
                hover-class="meta-retry-hover"
                :hover-stay-time="80"
                @tap.stop="onRetryFailed(item.msgIndex!)"
              >
                <text class="meta-retry-text">↻ 重试</text>
              </view>
            </view>
          </template>
        </view>
      </template>
    </scroll-view>

    <!-- 跳到底部浮按 - 离底足够远时浮在 composer 上方右下角 -->
    <view
      v-if="showJumpBottom"
      class="jump-bottom"
      hover-class="jump-bottom-hover"
      :hover-stay-time="80"
      @tap="scrollBottom"
    >
      <text class="jump-bottom-arrow">↓</text>
    </view>

    <view class="composer-bar">
      <ModelPicker
        :current="currentModel"
        :entries="modelEntries"
        @change="onPickModel"
      />
    </view>
    <view class="composer">
      <textarea
        :value="draft"
        class="input"
        :disabled="sending"
        :auto-height="true"
        :show-confirm-bar="false"
        :adjust-position="true"
        :maxlength="8000"
        placeholder="发消息, 输入 / 试试快捷指令..."
        @input="onDraftInput"
      />
      <button
        v-if="!sending"
        class="btn-send"
        :disabled="!draft.trim()"
        @tap="onSend"
      >
        发送
      </button>
      <button v-else class="btn-cancel" @tap="onCancel">停止</button>
    </view>
    <ShareCard ref="shareCardRef" />
  </view>
</template>

<style lang="scss" scoped>
.page {
  display: flex;
  flex-direction: column;
  height: 100vh;
  width: 100%;
  position: relative; // 让 .jump-bottom 浮按按页面定位
  box-sizing: border-box;
  overflow: hidden; // 兜底防任何子元素横向溢出
}
.messages {
  flex: 1;
  padding: 24rpx;
  box-sizing: border-box; // 关键: 不加这行 scroll-view 会因 padding 而宽度超出父 100% (mp-weixin 默认 content-box)
  width: 100%;
}
.loading-hint {
  padding: 96rpx 24rpx;
  text-align: center;
  color: #9ca3af;
  font-size: 28rpx;
}
.time-bar {
  text-align: center;
  margin: 24rpx 0 8rpx;
}
.time-text {
  font-size: 22rpx;
  color: #9ca3af;
  background: rgba(0, 0, 0, 0.04);
  padding: 4rpx 16rpx;
  border-radius: 12rpx;
}
.msg {
  margin: 12rpx 0;
  max-width: 80%;
  padding: 20rpx 24rpx;
  border-radius: 16rpx;
}
.msg-user {
  background: #3b82f6;
  color: #fff;
  margin-left: auto;
}
.msg-assistant {
  background: #fff;
  color: #1f2937;
}
.msg-text {
  font-size: calc(30rpx * var(--font-scale, 1));
  line-height: 1.5;
  word-break: break-all;
}
.msg-rich {
  font-size: calc(30rpx * var(--font-scale, 1));
  line-height: 1.6;
  color: #1f2937;
}
.msg-typing {
  font-size: calc(30rpx * var(--font-scale, 1));
  color: #9ca3af;
  letter-spacing: 4rpx;
}
.cursor {
  display: inline-block;
  margin-left: 4rpx;
  font-size: 30rpx;
  color: #3b82f6;
  animation: blink 1s steps(2, start) infinite;
}
.msg-meta {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  margin-top: 12rpx;
  padding-top: 10rpx;
  border-top: 1rpx dashed #e5e7eb;
  gap: 4rpx;
}
.meta-text {
  font-size: 22rpx;
  color: #94a3b8;
  line-height: 1.4;
}
.meta-dot {
  font-size: 22rpx;
  color: #cbd5e1;
}
.meta-retry {
  display: inline-flex;
  align-items: center;
  padding: 4rpx 16rpx;
  border-radius: 999rpx;
  background: #eff6ff;
  margin-left: 4rpx;
}
.meta-retry-hover {
  background: #dbeafe;
}
.meta-retry-text {
  font-size: 22rpx;
  color: #2563eb;
  font-weight: 500;
}
@keyframes blink {
  to {
    visibility: hidden;
  }
}
.jump-bottom {
  position: absolute;
  right: 32rpx;
  // 距离 composer + composer-bar 上方一点 — composer 高 ~120rpx + 间距 24rpx
  bottom: 220rpx;
  width: 80rpx;
  height: 80rpx;
  border-radius: 50%;
  background: #ffffff;
  display: flex;
  align-items: center;
  justify-content: center;
  box-shadow: 0 4rpx 16rpx rgba(15, 23, 42, 0.12);
  z-index: 10;
  border: 1rpx solid #e5e7eb;
}
.jump-bottom-hover {
  background: #f1f5f9;
  transform: scale(0.94);
}
.jump-bottom-arrow {
  font-size: 36rpx;
  color: #64748b;
  font-weight: 600;
  line-height: 1;
}
.composer-bar {
  // ModelPicker 单独一行, 紧贴 composer 顶部
  display: flex;
  align-items: center;
  padding: 8rpx 16rpx 0;
  background: #fff;
  border-top: 1px solid #e5e7eb;
}
.composer {
  display: flex;
  align-items: flex-end;
  padding: 8rpx 16rpx 16rpx;
  background: #fff;
  gap: 16rpx;
}
.input {
  flex: 1;
  min-height: 72rpx;
  max-height: 320rpx;
  padding: 18rpx 24rpx;
  border: 1px solid #d1d5db;
  border-radius: 16rpx;
  font-size: calc(28rpx * var(--font-scale, 1));
  line-height: 1.4;
  background: #f9fafb;
}
.btn-send {
  flex-shrink: 0;
  height: 72rpx;
  line-height: 72rpx;
  padding: 0 32rpx;
  background: #3b82f6;
  color: #fff;
  border-radius: 12rpx;
  font-size: 28rpx;
}
.btn-send[disabled] {
  opacity: 0.5;
}
.btn-cancel {
  flex-shrink: 0;
  height: 72rpx;
  line-height: 72rpx;
  padding: 0 32rpx;
  background: #ef4444;
  color: #fff;
  border-radius: 12rpx;
  font-size: 28rpx;
}
</style>

<script setup lang="ts">
// HeroView.vue — 空状态欢迎页. 参考 Flutter 端 hero_view.dart 设计.
//
// 两个变体:
//   noThread     用户没有 active thread (启动 / 删完会话) → 问候 + 6 卡 + 最近会话
//   emptyThread  选了 thread 但 0 消息 → 问候 + 当前模型 + 3 卡 (无最近会话)
//
// 起点卡点击统一 emit('starter-tap', prompt) — 上层 (chat 页) 决定填 draft 还是
// 直接发送. Flutter 端选了 prefill, 我们小程序也跟随.
//
// 布局: 双层 DOM (cell > card). cell 严格 width:50% 纯百分比, card 100% 撑满
// cell. 不依赖 calc / grid / flex-basis 等在 mp-weixin 上不稳的特性.

import { computed } from 'vue';
import { greetingTextFor, formatRelative } from '@/lib/greeting';
import { STARTER_PROMPTS } from '@/lib/starter_prompts';
import type { Thread } from '@/data/api/chat';

interface Props {
  variant?: 'noThread' | 'emptyThread';
  currentModel?: string;
  recentThreads?: Thread[];
}
const props = withDefaults(defineProps<Props>(), {
  variant: 'noThread',
  currentModel: '',
  recentThreads: () => [],
});

const emit = defineEmits<{
  'starter-tap': [prompt: string];
  'recent-tap': [threadId: string];
}>();

const greeting = computed(() => greetingTextFor());
const subtitle = computed(() =>
  props.variant === 'noThread' ? '今天想聊点什么?' : '开始你的对话',
);

// emptyThread 仅显示 3 张卡 (与 Flutter 端一致, 节省垂直空间)
const visiblePrompts = computed(() =>
  props.variant === 'emptyThread' ? STARTER_PROMPTS.slice(0, 3) : STARTER_PROMPTS,
);

const visibleRecents = computed(() => (props.recentThreads || []).slice(0, 5));

function relTime(updatedAt: string): string {
  const t = new Date(updatedAt);
  if (isNaN(t.getTime())) return '';
  return formatRelative(t);
}

function onStarterTap(prompt: string) {
  emit('starter-tap', prompt);
}
function onRecentTap(threadId: string) {
  emit('recent-tap', threadId);
}
</script>

<template>
  <view class="hero">
    <!-- ─── 问候 ─── -->
    <view class="greeting">
      <text class="greeting-title">{{ greeting }}</text>
      <text class="greeting-subtitle">{{ subtitle }}</text>
      <text
        v-if="variant === 'emptyThread' && currentModel"
        class="greeting-model"
      >
        当前模型 · {{ currentModel }}
      </text>
    </view>

    <!-- ─── 起点卡 grid: cell > card 双层 DOM ─── -->
    <view class="starter-grid">
      <view
        v-for="p in visiblePrompts"
        :key="p.id"
        class="starter-cell"
      >
        <view
          class="starter-card"
          hover-class="starter-card-hover"
          :hover-stay-time="80"
          @tap="onStarterTap(p.prompt)"
        >
          <view class="starter-icon" :style="{ background: p.bg }">
            <text class="starter-emoji">{{ p.emoji }}</text>
          </view>
          <view class="starter-text">
            <text class="starter-title">{{ p.title }}</text>
            <text class="starter-hint">{{ p.hint }}</text>
          </view>
        </view>
      </view>
    </view>

    <!-- ─── 最近会话 (仅 noThread) ─── -->
    <view v-if="variant === 'noThread'" class="recent-section">
      <view class="recent-header">
        <text class="recent-section-title">最近会话</text>
        <text v-if="visibleRecents.length > 0" class="recent-section-count">
          {{ visibleRecents.length }}
        </text>
      </view>
      <view v-if="visibleRecents.length === 0" class="recent-empty">
        <text>还没有会话, 上面挑一个开始吧</text>
      </view>
      <view v-else class="recent-list">
        <view
          v-for="t in visibleRecents"
          :key="t.id"
          class="recent-row"
          hover-class="recent-row-hover"
          :hover-stay-time="80"
          @tap="onRecentTap(t.id)"
        >
          <view class="recent-dot" />
          <text class="recent-title">{{ t.title || '(未命名)' }}</text>
          <text class="recent-time">{{ relTime(t.updated_at) }}</text>
        </view>
      </view>
    </view>
  </view>
</template>

<style lang="scss" scoped>
// 父 scroll-view (.messages) 已有 padding:24rpx, hero 不再叠加左右 padding.
// max-width 在大屏 H5 端限制最大宽度.
.hero {
  padding: 16rpx 0 64rpx;
  max-width: 720rpx;
  margin: 0 auto;
  box-sizing: border-box;
}

// ── Greeting ─────────────────────────────────────────────────
.greeting {
  margin-bottom: 40rpx;
  // 不加左右 padding, 让标题严格对齐左列卡片左边
}
.greeting-title {
  display: block;
  font-size: 64rpx;
  font-weight: 700;
  color: #0f172a;
  line-height: 1.1;
  letter-spacing: -1rpx;
}
.greeting-subtitle {
  display: block;
  margin-top: 8rpx;
  font-size: 28rpx;
  color: #64748b;
  font-weight: 400;
}
.greeting-model {
  display: inline-block;
  margin-top: 16rpx;
  font-size: 22rpx;
  color: #94a3b8;
  background: #f1f5f9;
  padding: 4rpx 16rpx;
  border-radius: 999rpx;
}

// ── Starter grid (双层 DOM: cell > card) ─────────────────────
//
// 关键设计 — 完全不依赖 box-sizing:
//   1. cell width:50% 纯百分比, NO padding (避免 box-sizing 陷阱)
//   2. card 不设 width — 默认 block-level fill cell 内容区
//   3. card 用 margin 收缩自己 → 留出卡片间隙 (不依赖 box-sizing 计算)
//
//   左 cell (350rpx) > card { margin-right: 8rpx } → card 342rpx
//   右 cell (350rpx) > card { margin-left:  8rpx } → card 342rpx
//   两 card 之间 = 16rpx, 总宽严格 = 100%
.starter-grid {
  display: flex;
  flex-wrap: wrap;
  margin-bottom: 24rpx;
}
.starter-cell {
  width: 50%;
  flex: none;
  // 故意不设 padding / box-sizing, 杜绝 box-sizing 解析差异
}
.starter-card {
  // 不设 width — 默认 block-level fill 父 cell 减自身 margin
  display: flex;
  flex-direction: row;
  align-items: center;
  margin-bottom: 16rpx;
  padding: 24rpx 20rpx;
  background: #ffffff;
  border-radius: 20rpx;
  border: 1px solid #f1f5f9;
  min-height: 140rpx;
  box-shadow: 0 2rpx 8rpx rgba(15, 23, 42, 0.04);
  transition: transform 0.15s ease, box-shadow 0.15s ease,
    background 0.15s ease;
  box-sizing: border-box; // padding 不撑大已收缩的 width
}
// 左列 card: 右侧 8rpx 间距, 左对齐 hero 边
.starter-cell:nth-child(2n + 1) .starter-card {
  margin-right: 8rpx;
}
// 右列 card: 左侧 8rpx 间距, 右对齐 hero 边
.starter-cell:nth-child(2n) .starter-card {
  margin-left: 8rpx;
}
.starter-card-hover {
  background: #f8fafc;
  transform: scale(0.97);
  box-shadow: 0 4rpx 16rpx rgba(15, 23, 42, 0.08);
}
.starter-icon {
  width: 72rpx;
  height: 72rpx;
  border-radius: 18rpx;
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  margin-right: 16rpx;
}
.starter-emoji {
  font-size: 36rpx;
  line-height: 1;
}
.starter-text {
  flex: 1;
  min-width: 0; // 让内层 ellipsis 生效, 不被内容撑大
  display: flex;
  flex-direction: column;
}
.starter-title {
  display: block;
  font-size: 28rpx;
  font-weight: 600;
  color: #0f172a;
  line-height: 1.25;
  margin-bottom: 4rpx;
  // ellipsis 三件套
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.starter-hint {
  display: block;
  font-size: 22rpx;
  color: #94a3b8;
  line-height: 1.3;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

// ── Recent section ───────────────────────────────────────────
.recent-section {
  display: flex;
  flex-direction: column;
  margin-top: 24rpx;
  padding-top: 32rpx;
  border-top: 1px solid #f1f5f9;
}
.recent-header {
  display: flex;
  align-items: center;
  margin-bottom: 16rpx;
}
.recent-section-title {
  font-size: 24rpx;
  font-weight: 600;
  color: #475569;
  letter-spacing: 0.5rpx;
  margin-right: 12rpx;
}
.recent-section-count {
  font-size: 20rpx;
  color: #64748b;
  background: #f1f5f9;
  padding: 2rpx 14rpx;
  border-radius: 999rpx;
  line-height: 1.6;
}
.recent-empty {
  font-size: 24rpx;
  color: #94a3b8;
  padding: 16rpx 0 24rpx;
}
.recent-list {
  display: flex;
  flex-direction: column;
}
.recent-row {
  display: flex;
  align-items: center;
  padding: 20rpx 8rpx;
  border-radius: 12rpx;
}
.recent-row-hover {
  background: #f8fafc;
}
.recent-dot {
  width: 8rpx;
  height: 8rpx;
  border-radius: 50%;
  background: #cbd5e1;
  flex-shrink: 0;
  margin-right: 16rpx;
}
.recent-title {
  flex: 1;
  min-width: 0;
  font-size: 28rpx;
  color: #1e293b;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  margin-right: 16rpx;
}
.recent-time {
  font-size: 22rpx;
  color: #94a3b8;
  flex-shrink: 0;
}
</style>

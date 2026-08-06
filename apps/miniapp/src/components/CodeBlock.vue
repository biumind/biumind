<script setup lang="ts">
// CodeBlock.vue — chat 消息中的代码块独立组件.
//
// 替代 markdown.ts 内嵌 rich-text 的 <pre>: rich-text 不能响应 click,
// 复制按钮做不出来; 拆出来用原生 view 渲染就解决了.
//
// 设计:
//   header: 语言标签 + 复制按钮 (右上)
//   body:   横向滚动 (scroll-x), token 着色, 等宽字体
//
// 横向滚动方案: 外层 scroll-view scroll-x, 内层 view inline-block + pre
// — 长行不 wrap 撑大内层, 触发横滚; 多行 \n 因 white-space:pre 保留.

import { computed, ref } from 'vue';
import { highlightCode } from '@/lib/code_highlight';

interface Props {
  lang: string;
  code: string;
}
const props = defineProps<Props>();

const tokens = computed(() => highlightCode(props.code, props.lang));
const copied = ref(false);
const langLabel = computed(() => (props.lang || 'text').toLowerCase());

function onCopy() {
  uni.setClipboardData({
    data: props.code,
    success: () => {
      copied.value = true;
      // wx 自带"内容已复制"toast, 自定义需 success: false
      setTimeout(() => {
        copied.value = false;
      }, 2000);
    },
    fail: () => {
      uni.showToast({ title: '复制失败', icon: 'none' });
    },
  });
}
</script>

<template>
  <view class="code-block">
    <view class="code-header">
      <text class="code-lang">{{ langLabel }}</text>
      <view
        class="code-copy"
        hover-class="code-copy-hover"
        :hover-stay-time="80"
        @tap="onCopy"
      >
        <text class="code-copy-text">{{ copied ? '✓ 已复制' : '复制' }}</text>
      </view>
    </view>
    <scroll-view
      scroll-x
      class="code-scroll"
      :show-scrollbar="false"
    >
      <view class="code-body">
        <text
          v-for="(tok, i) in tokens"
          :key="i"
          :style="{ color: tok.color }"
          >{{ tok.text }}</text>
      </view>
    </scroll-view>
  </view>
</template>

<style lang="scss" scoped>
.code-block {
  margin: 12rpx 0;
  border-radius: 12rpx;
  overflow: hidden;
  background: #0f172a;
  box-sizing: border-box;
}
.code-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 10rpx 24rpx;
  background: #1e293b;
  box-sizing: border-box;
}
.code-lang {
  font-size: 22rpx;
  color: #94a3b8;
  font-family: Menlo, Consolas, monospace;
  letter-spacing: 1rpx;
  text-transform: lowercase;
}
.code-copy {
  padding: 4rpx 18rpx;
  border-radius: 8rpx;
  background: #334155;
}
.code-copy-hover {
  background: #475569;
}
.code-copy-text {
  font-size: 22rpx;
  color: #e2e8f0;
}
// scroll-view 自身不 wrap 子内容, 内层 inline-block 触发横向滚动
.code-scroll {
  white-space: nowrap;
  width: 100%;
}
.code-body {
  display: inline-block;
  min-width: 100%;
  padding: 20rpx 24rpx;
  font-size: 26rpx;
  line-height: 1.6;
  font-family: Menlo, Consolas, monospace;
  // pre — 保留空格和换行符, 不 wrap; 配合外层横滚长行
  white-space: pre;
  color: #e2e8f0;
  vertical-align: top;
  box-sizing: border-box;
}
</style>

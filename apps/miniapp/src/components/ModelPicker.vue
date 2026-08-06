<script setup lang="ts">
// ModelPicker.vue — chat 输入框上方的一行模型切换器.
//
// 显示当前选中模型 (从 entries 中匹配); 点击 ActionSheet 切换. 自身不做
// 持久化 / 后端写, 上层 (chat 页) 收到 'change' emit 后决定:
//   - hero 状态: 只 savePreferredModel
//   - thread 内: patchThread + savePreferredModel
//
// entries 由上层负责拉取 (data/api/models.ts loadModelEntries), 启动可
// 先用 fallback catalog 兜底.

import { computed } from 'vue';
import type { ModelEntry } from '@/data/api/models';

interface Props {
  current: string;
  entries: ModelEntry[];
}
const props = defineProps<Props>();

const emit = defineEmits<{
  change: [modelId: string];
}>();

const currentEntry = computed<ModelEntry | undefined>(() => {
  return props.entries.find((e) => e.modelId === props.current);
});

const label = computed(() => currentEntry.value?.label || props.current || '选择模型');
const isOfficial = computed(() => currentEntry.value?.isOfficial ?? false);
const subLabel = computed(() => {
  const e = currentEntry.value;
  if (!e || e.isOfficial) return '';
  return e.providerName || '';
});

function onTap() {
  if (props.entries.length === 0) {
    uni.showToast({ title: '模型列表加载中...', icon: 'none' });
    return;
  }
  // 把 entries 折叠成 ActionSheet item: "<emoji> <label> · <provider>"
  const items = props.entries.map((e) => {
    if (e.isOfficial) return '✨ ' + e.label + ' (官方)';
    return e.label + ' · ' + e.providerName;
  });
  uni.showActionSheet({
    itemList: items,
    success: (res) => {
      const selected = props.entries[res.tapIndex];
      if (selected && selected.modelId !== props.current) {
        emit('change', selected.modelId);
      }
    },
  });
}
</script>

<template>
  <view
    class="model-picker"
    hover-class="model-picker-hover"
    :hover-stay-time="80"
    @tap="onTap"
  >
    <text v-if="isOfficial" class="model-badge">✨</text>
    <text class="model-label" :class="{ 'model-label-official': isOfficial }">
      {{ label }}
    </text>
    <text v-if="subLabel" class="model-sub">· {{ subLabel }}</text>
    <text class="model-caret">⌄</text>
  </view>
</template>

<style lang="scss" scoped>
.model-picker {
  display: inline-flex;
  align-items: center;
  gap: 8rpx;
  padding: 8rpx 18rpx;
  background: #f1f5f9;
  border-radius: 999rpx;
  max-width: 520rpx;
  box-sizing: border-box;
}
.model-picker-hover {
  background: #e2e8f0;
}
.model-badge {
  font-size: 22rpx;
  line-height: 1;
}
.model-label {
  font-size: 24rpx;
  color: #1e293b;
  font-weight: 600;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.model-label-official {
  color: #7c3aed;
}
.model-sub {
  font-size: 22rpx;
  color: #94a3b8;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  flex-shrink: 1;
  min-width: 0;
}
.model-caret {
  font-size: 22rpx;
  color: #94a3b8;
  margin-top: -8rpx; // 视觉对齐字体 baseline
}
</style>

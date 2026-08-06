<script setup lang="ts">
// 只读 Wiki — 后端 brain 没有跨 project 列页接口, 端点是
// /v1/wiki/projects/{pid}/pages. 小程序端先列 project, 用户点进去
// 再列页 (W5 版做"项目列表 + 顶层概览", 详情页 W6 接入).

import { ref, onMounted } from 'vue';
import { get } from '@/data/api/client';
import { isLoggedIn } from '@/core/token_manager';

interface WikiProject {
  id: string;
  name: string;
  description?: string;
  page_count?: number;
  updated_at?: string;
}

const projects = ref<WikiProject[]>([]);
const loading = ref(false);
const err = ref('');

async function reload() {
  if (!isLoggedIn()) {
    uni.redirectTo({ url: '/pages/me/login' });
    return;
  }
  loading.value = true;
  err.value = '';
  try {
    const r = await get<{ projects: WikiProject[] }>('/v1/wiki/projects?limit=50');
    projects.value = r.projects || [];
  } catch (e: unknown) {
    err.value = e instanceof Error ? e.message : String(e);
  } finally {
    loading.value = false;
  }
}

function openProject(p: WikiProject) {
  // 详情页 W6 加; 现在 toast 占位
  uni.showToast({
    title: '项目 "' + p.name + '" 详情页 W6 上线',
    icon: 'none',
  });
}

onMounted(reload);

function onShareAppMessage() {
  return { title: '在 BiuMind 看 Wiki', path: '/pages/wiki/index' };
}
defineExpose({ onShareAppMessage });
</script>

<template>
  <view class="page">
    <view v-if="loading" class="hint">加载中...</view>
    <view v-else-if="err" class="error">{{ err }}</view>
    <view v-else-if="projects.length === 0" class="hint">
      还没有 Wiki 项目, 去 Web 端创建一个吧
    </view>
    <view v-else>
      <view
        v-for="p in projects"
        :key="p.id"
        class="card"
        @tap="openProject(p)"
      >
        <text class="card-title">{{ p.name }}</text>
        <text class="card-excerpt">{{ p.description || '(无描述)' }}</text>
      </view>
    </view>
  </view>
</template>

<style lang="scss" scoped>
.page {
  padding: 16rpx;
}
.card {
  background: #fff;
  border-radius: 12rpx;
  padding: 24rpx;
  margin-bottom: 16rpx;
}
.card-title {
  display: block;
  font-size: 30rpx;
  font-weight: 600;
  color: #1f2937;
  margin-bottom: 12rpx;
}
.card-excerpt {
  font-size: 26rpx;
  color: #6b7280;
  line-height: 1.5;
}
.hint {
  padding: 96rpx 24rpx;
  text-align: center;
  color: #9ca3af;
  font-size: 28rpx;
}
.error {
  padding: 32rpx;
  color: #ef4444;
  font-size: 26rpx;
}
</style>

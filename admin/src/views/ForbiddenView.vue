<script setup lang="ts">
import { useRoute, useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { ROLE_LABELS } from '@/api/types'

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()

function back() {
  if (window.history.length > 1) router.back()
  else router.replace('/')
}
</script>

<template>
  <el-result icon="warning" title="权限不足" :sub-title="`你的角色 ${ROLE_LABELS[auth.role] ?? auth.role} 无权访问此页面`">
    <template #extra>
      <p v-if="route.query.perm" class="text-muted text-mono">
        需要权限: {{ route.query.perm }}
      </p>
      <p v-if="route.query.roles" class="text-muted text-mono">
        允许角色: {{ route.query.roles }}
      </p>
      <el-button type="primary" @click="back">返回</el-button>
    </template>
  </el-result>
</template>

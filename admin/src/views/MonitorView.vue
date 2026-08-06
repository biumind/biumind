<script setup lang="ts">
import { ref, onMounted, onUnmounted, computed, shallowRef } from 'vue'
import VChart from 'vue-echarts'
import { use } from 'echarts/core'
import { LineChart } from 'echarts/charts'
import {
  GridComponent,
  TooltipComponent,
  LegendComponent,
  DataZoomComponent,
} from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'

import * as api from '@/api/admin'
import { errorMessage } from '@/api/http'
import type { ServiceProbe } from '@/api/types'

use([LineChart, GridComponent, TooltipComponent, LegendComponent, DataZoomComponent, CanvasRenderer])

// ── 实时部分 ──
const services = ref<ServiceProbe[]>([])
const loading = ref(false)
const lastUpdate = ref<Date | null>(null)
const errMsg = ref('')

let timer: ReturnType<typeof setInterval> | null = null

// ── 趋势 tab ──
const activeTab = ref<'live' | 'trend'>('live')
const trendRange = ref<'15m' | '1h' | '6h' | '24h'>('1h')
const promErrors = ref<string[]>([])

// ECharts option 类型多变, 用 any 让 TS 不抠细节.
// eslint-disable-next-line @typescript-eslint/no-explicit-any
const qpsChart = shallowRef<any>(null)
// eslint-disable-next-line @typescript-eslint/no-explicit-any
const errorRateChart = shallowRef<any>(null)
// eslint-disable-next-line @typescript-eslint/no-explicit-any
const latencyChart = shallowRef<any>(null)
// eslint-disable-next-line @typescript-eslint/no-explicit-any
const cpuChart = shallowRef<any>(null)

async function load() {
  loading.value = true
  errMsg.value = ''
  try {
    services.value = await api.listServices()
    lastUpdate.value = new Date()
  } catch (e) {
    errMsg.value = errorMessage(e)
  } finally {
    loading.value = false
  }
}

const rangeSeconds: Record<string, number> = { '15m': 900, '1h': 3600, '6h': 21600, '24h': 86400 }
const rangeStep: Record<string, string> = { '15m': '15s', '1h': '30s', '6h': '2m', '24h': '5m' }

// PromQL 表达式
const QUERIES = {
  qps: 'sum by (service) (rate(biumind_http_requests_total[1m]))',
  errorRate:
    '(sum by (service) (rate(biumind_http_requests_total{status_class="5xx"}[1m]))) / (sum by (service) (rate(biumind_http_requests_total[1m])))',
  latencyP99:
    'histogram_quantile(0.99, sum by (le, service) (rate(biumind_http_request_duration_seconds_bucket[1m])))',
  cpu:
    'sum by (name) (rate(container_cpu_usage_seconds_total{name=~"biu-.+"}[1m]))',
}

interface PromMatrixSeries {
  metric: Record<string, string>
  values: [number, string][]
}

async function loadTrends() {
  const sec = rangeSeconds[trendRange.value]
  const end = Math.floor(Date.now() / 1000)
  const start = end - sec
  const step = rangeStep[trendRange.value]
  promErrors.value = []

  const fetch = async (q: string) => {
    try {
      const r = await api.promQueryRange(q, start, end, step)
      if (r.status !== 'success') {
        promErrors.value.push(`${q} → ${r.error ?? 'error'}`)
        return []
      }
      return r.data.result
    } catch (e) {
      promErrors.value.push(errorMessage(e))
      return []
    }
  }

  const [qps, err, lat, cpu] = await Promise.all([
    fetch(QUERIES.qps),
    fetch(QUERIES.errorRate),
    fetch(QUERIES.latencyP99),
    fetch(QUERIES.cpu),
  ])

  qpsChart.value = buildChart(qps, 'service', 'QPS', (v) => `${parseFloat(v).toFixed(2)} rps`)
  errorRateChart.value = buildChart(err, 'service', '5xx 错误率', (v) =>
    `${(parseFloat(v) * 100).toFixed(2)}%`,
  )
  latencyChart.value = buildChart(lat, 'service', 'P99 延迟', (v) => `${(parseFloat(v) * 1000).toFixed(0)} ms`)
  cpuChart.value = buildChart(cpu, 'name', 'CPU 用量 (容器)', (v) => `${(parseFloat(v) * 100).toFixed(1)}%`)
}

function buildChart(
  series: PromMatrixSeries[],
  labelKey: 'service' | 'name',
  title: string,
  fmt: (raw: string) => string,
) {
  return {
    title: { text: title, textStyle: { fontSize: 14, fontWeight: 600 } },
    tooltip: {
      trigger: 'axis',
      valueFormatter: (v: number | string) => fmt(String(v)),
    },
    legend: { type: 'scroll', bottom: 0 },
    grid: { left: 50, right: 20, top: 30, bottom: 40 },
    xAxis: { type: 'time' },
    yAxis: { type: 'value', axisLabel: { formatter: (v: number) => fmt(String(v)) } },
    dataZoom: [{ type: 'inside' }],
    series: series.map((s) => ({
      type: 'line',
      name: s.metric[labelKey] || 'unknown',
      smooth: true,
      showSymbol: false,
      data: s.values.map(([t, v]) => [t * 1000, parseFloat(v)]),
    })),
  }
}

onMounted(() => {
  load()
  timer = setInterval(load, 5000)
})
onUnmounted(() => {
  if (timer) clearInterval(timer)
})

const updatedAgo = computed(() => {
  if (!lastUpdate.value) return '—'
  const sec = Math.floor((Date.now() - lastUpdate.value.getTime()) / 1000)
  if (sec < 5) return `刚刚`
  return `${sec}s 前`
})

function statusType(s: string): '' | 'success' | 'warning' | 'info' | 'danger' {
  switch (s) {
    case 'healthy': return 'success'
    case 'degraded': return 'warning'
    case 'unhealthy': return 'danger'
    default: return 'info'
  }
}

function statusText(s: string): string {
  return ({ healthy: '健康', degraded: '降级', unhealthy: '异常', unknown: '未知' } as Record<string, string>)[s] ?? s
}

function fmtTime(iso: string) {
  return new Date(iso).toLocaleTimeString('zh-CN', { hour12: false })
}

function onTabChange(t: string | number) {
  activeTab.value = t as 'live' | 'trend'
  if (activeTab.value === 'trend') loadTrends()
}
</script>

<template>
  <div class="monitor">
    <div class="page-header">
      <h1>服务监控</h1>
      <div class="header-info">
        <span class="text-muted">更新于: {{ updatedAgo }}</span>
        <el-button :loading="loading" size="small" @click="load">刷新</el-button>
      </div>
    </div>

    <el-alert v-if="errMsg" :title="errMsg" type="error" show-icon style="margin-bottom: 16px" />

    <el-tabs v-model="activeTab" @tab-change="onTabChange">
      <el-tab-pane label="实时状态" name="live">
        <el-row :gutter="16" class="cards">
          <el-col v-for="s in services" :key="s.name" :xs="24" :sm="12" :md="8" :lg="8">
            <el-card class="svc-card" :class="`status-${s.status}`" shadow="never">
              <div class="svc-row1">
                <div class="svc-name">
                  <el-icon class="svc-icon"><Service /></el-icon>
                  <span>{{ s.name }}</span>
                </div>
                <el-tag size="small" :type="statusType(s.status)">{{ statusText(s.status) }}</el-tag>
              </div>
              <div class="svc-row2">
                <div class="kv">
                  <span class="k">版本</span>
                  <span class="v text-mono">{{ s.version || '—' }}</span>
                </div>
                <div class="kv">
                  <span class="k">延迟</span>
                  <span class="v text-mono">{{ s.latency_ms != null ? `${s.latency_ms}ms` : '—' }}</span>
                </div>
                <div class="kv">
                  <span class="k">HTTP</span>
                  <span class="v text-mono">{{ s.http_status || '—' }}</span>
                </div>
                <div class="kv">
                  <span class="k">检查</span>
                  <span class="v text-mono">{{ fmtTime(s.last_check_at) }}</span>
                </div>
              </div>
              <div v-if="s.probes?.length" class="svc-probes">
                <el-tag
                  v-for="p in s.probes"
                  :key="p.name"
                  size="small"
                  :type="p.status === 'ok' || p.status === 'healthy' ? 'success' : 'danger'"
                  effect="plain"
                >
                  {{ p.name }}: {{ p.status }}
                </el-tag>
              </div>
              <p v-if="s.error" class="svc-error" :title="s.error">⚠ {{ s.error }}</p>
            </el-card>
          </el-col>
        </el-row>

        <el-alert
          v-if="services.length === 0 && !loading"
          title="暂无服务数据"
          description="identity Monitor 探测器还没拿到结果, 等几秒刷新; 或检查 6 个 BiuMind 服务是否在 biu-net 网络内可达."
          type="info"
          :closable="false"
        />
      </el-tab-pane>

      <el-tab-pane label="趋势 (Prometheus)" name="trend">
        <div class="trend-toolbar">
          <el-radio-group v-model="trendRange" @change="loadTrends">
            <el-radio-button value="15m">15 分钟</el-radio-button>
            <el-radio-button value="1h">1 小时</el-radio-button>
            <el-radio-button value="6h">6 小时</el-radio-button>
            <el-radio-button value="24h">24 小时</el-radio-button>
          </el-radio-group>
          <el-button @click="loadTrends">刷新</el-button>
        </div>

        <el-alert
          v-for="(e, i) in promErrors"
          :key="i"
          :title="e"
          type="warning"
          show-icon
          :closable="false"
          style="margin-bottom: 8px"
        />

        <el-row :gutter="16">
          <el-col :xs="24" :md="12">
            <el-card shadow="never" class="chart-card">
              <v-chart v-if="qpsChart" :option="qpsChart" autoresize style="height: 280px" />
              <p v-else class="text-muted" style="text-align: center; padding: 80px 0">加载中…</p>
            </el-card>
          </el-col>
          <el-col :xs="24" :md="12">
            <el-card shadow="never" class="chart-card">
              <v-chart v-if="errorRateChart" :option="errorRateChart" autoresize style="height: 280px" />
              <p v-else class="text-muted" style="text-align: center; padding: 80px 0">加载中…</p>
            </el-card>
          </el-col>
          <el-col :xs="24" :md="12">
            <el-card shadow="never" class="chart-card">
              <v-chart v-if="latencyChart" :option="latencyChart" autoresize style="height: 280px" />
              <p v-else class="text-muted" style="text-align: center; padding: 80px 0">加载中…</p>
            </el-card>
          </el-col>
          <el-col :xs="24" :md="12">
            <el-card shadow="never" class="chart-card">
              <v-chart v-if="cpuChart" :option="cpuChart" autoresize style="height: 280px" />
              <p v-else class="text-muted" style="text-align: center; padding: 80px 0">加载中…</p>
            </el-card>
          </el-col>
        </el-row>
      </el-tab-pane>
    </el-tabs>
  </div>
</template>

<style scoped lang="scss">
.monitor h1 { margin: 0; font-size: 24px; font-weight: 600; }

.page-header {
  display: flex; justify-content: space-between; align-items: center;
  margin-bottom: 16px;
  .header-info {
    display: flex; align-items: center; gap: 12px;
    font-size: 13px;
  }
}

.cards { margin-bottom: 8px; }

.svc-card {
  margin-bottom: 16px;
  border-left: 4px solid #d1d5db;
  &.status-healthy { border-left-color: #67c23a; }
  &.status-degraded { border-left-color: #e6a23c; }
  &.status-unhealthy { border-left-color: #f56c6c; }
  &.status-unknown { border-left-color: #909399; }
}
.svc-row1 { display: flex; justify-content: space-between; align-items: center; margin-bottom: 12px; }
.svc-name { display: flex; align-items: center; gap: 8px; font-weight: 600; }
.svc-icon { color: #6b7280; }

.svc-row2 {
  display: grid; grid-template-columns: 1fr 1fr; gap: 4px 16px; font-size: 13px;
  .kv { display: flex; justify-content: space-between; }
  .k { color: #6b7280; }
  .v { color: #111827; }
}
.svc-probes { margin-top: 12px; display: flex; flex-wrap: wrap; gap: 4px; }
.svc-error {
  margin-top: 8px;
  padding: 6px 10px;
  background: #fef0f0;
  color: #f56c6c;
  border-radius: 4px;
  font-size: 12px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.trend-toolbar {
  display: flex; gap: 12px; align-items: center;
  margin-bottom: 16px;
}
.chart-card { margin-bottom: 16px; }
</style>

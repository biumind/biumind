<script setup lang="ts">
// 业务用量页 — 全部基于 hub 暴露的 metrics:
//   biumind_hub_llm_tokens_total       {model, provider, plan, kind}
//   biumind_hub_llm_cost_millicents_total {model, provider, plan}
//   biumind_hub_llm_requests_total     {model, provider, plan, status}
//   biumind_hub_llm_request_duration_seconds {model, provider}

import { ref, onMounted, shallowRef, computed } from 'vue'
import VChart from 'vue-echarts'
import { use } from 'echarts/core'
import { LineChart, BarChart, PieChart } from 'echarts/charts'
import {
  GridComponent,
  TooltipComponent,
  LegendComponent,
  DataZoomComponent,
  TitleComponent,
} from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'

import * as api from '@/api/admin'
import { errorMessage } from '@/api/http'

use([
  LineChart, BarChart, PieChart,
  GridComponent, TooltipComponent, LegendComponent, DataZoomComponent, TitleComponent,
  CanvasRenderer,
])

// ── 范围选择 ──
const range = ref<'1h' | '6h' | '24h' | '7d' | '30d'>('24h')
const rangeSeconds: Record<string, number> = {
  '1h': 3600, '6h': 21600, '24h': 86400, '7d': 86400 * 7, '30d': 86400 * 30,
}
const rangeStep: Record<string, string> = {
  '1h': '30s', '6h': '2m', '24h': '5m', '7d': '30m', '30d': '2h',
}

// ── KPI ──
const kpiTokens = ref<number>(0)
const kpiCostUSD = ref<number>(0)
const kpiRequests = ref<number>(0)
const kpiAvgLatency = ref<number>(0) // P50

const loading = ref(false)
const errMsgs = ref<string[]>([])

// ── 图表 ──
// eslint-disable-next-line @typescript-eslint/no-explicit-any
const tokensTrend = shallowRef<any>(null)
// eslint-disable-next-line @typescript-eslint/no-explicit-any
const costTrend = shallowRef<any>(null)
// eslint-disable-next-line @typescript-eslint/no-explicit-any
const modelDist = shallowRef<any>(null)
// eslint-disable-next-line @typescript-eslint/no-explicit-any
const providerDist = shallowRef<any>(null)
// eslint-disable-next-line @typescript-eslint/no-explicit-any
const planDist = shallowRef<any>(null)
// eslint-disable-next-line @typescript-eslint/no-explicit-any
const llmLatency = shallowRef<any>(null)

interface PromMatrixSeries {
  metric: Record<string, string>
  values: [number, string][]
}
interface PromVectorPoint {
  metric: Record<string, string>
  value: [number, string]
}

const rangeLabel = computed(() => `过去 ${range.value}`)

async function load() {
  loading.value = true
  errMsgs.value = []

  const sec = rangeSeconds[range.value]
  const end = Math.floor(Date.now() / 1000)
  const start = end - sec
  const step = rangeStep[range.value]
  const lookback = `${sec}s` // PromQL 子查询窗口 (e.g. "86400s")

  const safe = async <T>(p: Promise<T>): Promise<T | null> => {
    try {
      return await p
    } catch (e) {
      errMsgs.value.push(errorMessage(e))
      return null
    }
  }

  // KPI: 一段时间内总和 (instant query 用 increase 函数)
  const [
    totalTokensRes,
    totalCostRes,
    totalReqRes,
    avgLatencyRes,
    tokensRangeRes,
    costRangeRes,
    modelTokensRes,
    providerTokensRes,
    planTokensRes,
    latencyP99Res,
  ] = await Promise.all([
    safe(api.promQuery(`sum(increase(biumind_hub_llm_tokens_total[${lookback}]))`)),
    safe(api.promQuery(`sum(increase(biumind_hub_llm_cost_millicents_total[${lookback}]))`)),
    safe(api.promQuery(`sum(increase(biumind_hub_llm_requests_total[${lookback}]))`)),
    safe(api.promQuery(
      `histogram_quantile(0.50, sum by (le) (rate(biumind_hub_llm_request_duration_seconds_bucket[${lookback}])))`,
    )),
    safe(api.promQueryRange(
      `sum by (kind) (rate(biumind_hub_llm_tokens_total[1m])) * 60`,
      start, end, step,
    )),
    safe(api.promQueryRange(
      `sum (rate(biumind_hub_llm_cost_millicents_total[1m])) * 60`,
      start, end, step,
    )),
    safe(api.promQuery(`sum by (model) (increase(biumind_hub_llm_tokens_total[${lookback}]))`)),
    safe(api.promQuery(`sum by (provider) (increase(biumind_hub_llm_tokens_total[${lookback}]))`)),
    safe(api.promQuery(`sum by (plan) (increase(biumind_hub_llm_tokens_total[${lookback}]))`)),
    safe(api.promQueryRange(
      `histogram_quantile(0.99, sum by (le, model) (rate(biumind_hub_llm_request_duration_seconds_bucket[5m])))`,
      start, end, step,
    )),
  ])

  // KPI 单值
  kpiTokens.value = sumScalar(totalTokensRes)
  kpiCostUSD.value = sumScalar(totalCostRes) / 100_000 // millicents → USD
  kpiRequests.value = sumScalar(totalReqRes)
  kpiAvgLatency.value = sumScalar(avgLatencyRes) // 秒

  // Token 趋势 (按 kind 分线)
  tokensTrend.value = lineChart(
    rangeMatrix(tokensRangeRes), 'kind', 'Token 速率 (per minute)',
    (v) => `${parseFloat(v).toFixed(0)} tok/min`,
  )

  // 成本趋势 ($/min)
  costTrend.value = lineChart(
    rangeMatrix(costRangeRes), '', '成本 ($/min)',
    (v) => `$${(parseFloat(v) / 100_000).toFixed(4)}`,
    (raw: number) => raw / 100_000,
  )

  // model / provider / plan 饼图
  modelDist.value = pieChart(vector(modelTokensRes), 'model', 'Top 模型 (按 token)')
  providerDist.value = pieChart(vector(providerTokensRes), 'provider', 'Provider 分布')
  planDist.value = pieChart(vector(planTokensRes), 'plan', 'Plan 分布')

  // LLM 延迟 P99 by model
  llmLatency.value = lineChart(
    rangeMatrix(latencyP99Res), 'model', 'LLM P99 延迟',
    (v) => `${(parseFloat(v) * 1000).toFixed(0)} ms`,
  )

  loading.value = false
}

function sumScalar(res: { data: { result: PromVectorPoint[] } } | null): number {
  if (!res?.data?.result?.length) return 0
  return res.data.result.reduce((acc, p) => acc + parseFloat(p.value[1] || '0'), 0)
}

function vector(res: { data: { result: PromVectorPoint[] } } | null): PromVectorPoint[] {
  return res?.data?.result ?? []
}

function rangeMatrix(res: { data: { result: PromMatrixSeries[] } } | null): PromMatrixSeries[] {
  return res?.data?.result ?? []
}

function lineChart(
  series: PromMatrixSeries[],
  labelKey: string,
  title: string,
  fmt: (raw: string) => string,
  yTransform?: (raw: number) => number,
) {
  return {
    title: { text: title, textStyle: { fontSize: 14, fontWeight: 600 } },
    tooltip: { trigger: 'axis', valueFormatter: (v: number | string) => fmt(String(v)) },
    legend: { type: 'scroll', bottom: 0 },
    grid: { left: 60, right: 20, top: 30, bottom: 40 },
    xAxis: { type: 'time' },
    yAxis: { type: 'value', axisLabel: { formatter: (v: number) => fmt(String(v)) } },
    dataZoom: [{ type: 'inside' }],
    series: series.map((s) => ({
      type: 'line',
      name: labelKey ? (s.metric[labelKey] || 'unknown') : 'total',
      smooth: true,
      showSymbol: false,
      data: s.values.map(([t, v]) => [
        t * 1000,
        yTransform ? yTransform(parseFloat(v)) : parseFloat(v),
      ]),
    })),
  }
}

function pieChart(points: PromVectorPoint[], labelKey: string, title: string) {
  const data = points
    .map((p) => ({
      name: p.metric[labelKey] || 'unknown',
      value: parseFloat(p.value[1] || '0'),
    }))
    .filter((d) => d.value > 0)
    .sort((a, b) => b.value - a.value)
    .slice(0, 10)

  return {
    title: { text: title, textStyle: { fontSize: 14, fontWeight: 600 } },
    tooltip: { trigger: 'item', formatter: '{b}: {c} ({d}%)' },
    legend: { type: 'scroll', bottom: 0 },
    series: [
      {
        type: 'pie',
        radius: ['40%', '60%'],
        center: ['50%', '50%'],
        data,
        label: { show: true, formatter: '{b}\n{d}%' },
      },
    ],
  }
}

onMounted(load)
</script>

<template>
  <div class="usage">
    <div class="page-header">
      <h1>业务用量</h1>
      <div class="toolbar">
        <el-radio-group v-model="range" @change="load">
          <el-radio-button value="1h">1 小时</el-radio-button>
          <el-radio-button value="6h">6 小时</el-radio-button>
          <el-radio-button value="24h">24 小时</el-radio-button>
          <el-radio-button value="7d">7 天</el-radio-button>
          <el-radio-button value="30d">30 天</el-radio-button>
        </el-radio-group>
        <el-button :loading="loading" @click="load">刷新</el-button>
      </div>
    </div>

    <el-alert
      v-for="(e, i) in errMsgs"
      :key="i"
      :title="e"
      type="warning"
      show-icon
      :closable="false"
      style="margin-bottom: 8px"
    />

    <!-- KPI 卡片 -->
    <el-row :gutter="16" class="kpi-row">
      <el-col :xs="12" :md="6">
        <el-card class="kpi-card" shadow="never">
          <div class="kpi-label">{{ rangeLabel }} · 总 token</div>
          <div class="kpi-value">{{ Math.round(kpiTokens).toLocaleString() }}</div>
        </el-card>
      </el-col>
      <el-col :xs="12" :md="6">
        <el-card class="kpi-card" shadow="never">
          <div class="kpi-label">{{ rangeLabel }} · 总成本</div>
          <div class="kpi-value">${{ kpiCostUSD.toFixed(2) }}</div>
        </el-card>
      </el-col>
      <el-col :xs="12" :md="6">
        <el-card class="kpi-card" shadow="never">
          <div class="kpi-label">{{ rangeLabel }} · LLM 调用次数</div>
          <div class="kpi-value">{{ Math.round(kpiRequests).toLocaleString() }}</div>
        </el-card>
      </el-col>
      <el-col :xs="12" :md="6">
        <el-card class="kpi-card" shadow="never">
          <div class="kpi-label">P50 延迟</div>
          <div class="kpi-value">{{ kpiAvgLatency > 0 ? `${(kpiAvgLatency * 1000).toFixed(0)} ms` : '—' }}</div>
        </el-card>
      </el-col>
    </el-row>

    <!-- 趋势图 -->
    <el-row :gutter="16">
      <el-col :xs="24" :md="12">
        <el-card shadow="never" class="chart-card">
          <v-chart v-if="tokensTrend" :option="tokensTrend" autoresize style="height: 300px" />
          <p v-else class="text-muted" style="text-align: center; padding: 100px 0">加载中…</p>
        </el-card>
      </el-col>
      <el-col :xs="24" :md="12">
        <el-card shadow="never" class="chart-card">
          <v-chart v-if="costTrend" :option="costTrend" autoresize style="height: 300px" />
          <p v-else class="text-muted" style="text-align: center; padding: 100px 0">加载中…</p>
        </el-card>
      </el-col>
    </el-row>

    <!-- 分布饼图 -->
    <el-row :gutter="16">
      <el-col :xs="24" :md="8">
        <el-card shadow="never" class="chart-card">
          <v-chart v-if="modelDist" :option="modelDist" autoresize style="height: 280px" />
          <p v-else class="text-muted" style="text-align: center; padding: 100px 0">加载中…</p>
        </el-card>
      </el-col>
      <el-col :xs="24" :md="8">
        <el-card shadow="never" class="chart-card">
          <v-chart v-if="providerDist" :option="providerDist" autoresize style="height: 280px" />
          <p v-else class="text-muted" style="text-align: center; padding: 100px 0">加载中…</p>
        </el-card>
      </el-col>
      <el-col :xs="24" :md="8">
        <el-card shadow="never" class="chart-card">
          <v-chart v-if="planDist" :option="planDist" autoresize style="height: 280px" />
          <p v-else class="text-muted" style="text-align: center; padding: 100px 0">加载中…</p>
        </el-card>
      </el-col>
    </el-row>

    <!-- LLM 延迟 -->
    <el-row :gutter="16">
      <el-col :span="24">
        <el-card shadow="never" class="chart-card">
          <v-chart v-if="llmLatency" :option="llmLatency" autoresize style="height: 320px" />
          <p v-else class="text-muted" style="text-align: center; padding: 100px 0">加载中…</p>
        </el-card>
      </el-col>
    </el-row>
  </div>
</template>

<style scoped lang="scss">
.usage h1 { margin: 0; font-size: 24px; font-weight: 600; }

.page-header {
  display: flex; justify-content: space-between; align-items: center;
  margin-bottom: 16px;
  flex-wrap: wrap; gap: 12px;
}
.toolbar { display: flex; gap: 12px; align-items: center; }

.kpi-row { margin-bottom: 16px; }
.kpi-card {
  .kpi-label { color: #6b7280; font-size: 13px; margin-bottom: 8px; }
  .kpi-value { font-size: 28px; font-weight: 600; color: #111827; }
}

.chart-card { margin-bottom: 16px; }
</style>

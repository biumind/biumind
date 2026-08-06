// data/api/models.ts — 模型 / provider 列表的动态拉取.
//
// 后端 contract (services/brain/internal/chat/providers/api.go):
//   GET /v1/providers              → { providers: [providerDTO] }
//   GET /v1/providers/{id}/models  → { models: [modelDTO] } (lazy seed builtin)
//
// 客户端把多个启用的 provider 各自的 chat 模型聚合成扁平 ModelEntry 数组,
// picker 直接渲染. Picker 启动时先用 provider_catalog 兜底, 这里 async 拉
// 到后替换.

import { get } from './client';
import {
  BIUMIND_OFFICIAL_MODEL,
  providerDisplayName,
} from '@/lib/provider_catalog';

// ── 后端 DTO ──────────────────────────────────────────────────────

export interface ProviderDTO {
  /** uuid 记录 id (用作 listProviderModels 的参数) */
  id: string;
  /** slug, 'anthropic' / 'openai' / 'custom-foo' / 'official' */
  provider_id: string;
  display_name: string;
  enabled: boolean;
  has_api_key: boolean;
  source: string; // 'official' | 'byok' | 'custom'
  internal: boolean;
}

export interface ModelDTO {
  id: string;
  provider_id: string; // 指向 ProviderDTO.provider_id (slug)
  model_id: string;    // wire id
  display_name: string;
  type: string;        // 'chat' | 'embedding' | etc.
  enabled: boolean;
  context_window?: number;
}

// ── Picker 用的扁平 entry ─────────────────────────────────────────

export interface ModelEntry {
  /** 实际发送时用的 model id (e.g. 'claude-sonnet-4-6' 或 BIUMIND_OFFICIAL_MODEL) */
  modelId: string;
  /** picker 主标签 */
  label: string;
  /** 副标签 (provider 名称, 如 'Anthropic') */
  providerName: string;
  /** 是否官方渠道 — UI 上特殊标记 */
  isOfficial: boolean;
}

// ── API ───────────────────────────────────────────────────────────

export async function listProviders(): Promise<ProviderDTO[]> {
  const r = await get<{ providers?: ProviderDTO[] }>('/v1/providers');
  return r.providers || [];
}

export async function listProviderModels(
  providerRecordId: string,
  type: string = 'chat',
): Promise<ModelDTO[]> {
  const r = await get<{ models?: ModelDTO[] }>(
    '/v1/providers/' + encodeURIComponent(providerRecordId) +
      '/models?type=' + encodeURIComponent(type),
  );
  return r.models || [];
}

// ── model-relay public catalog (P6: global official 模型直读) ──────

export interface RelayModelDTO {
  code: string;
  display_name: string;
  family: string;
  context_window?: number;
  mode: string; // chat / embedding / audio_speech / ...
  min_plan?: string;
  pricing?: {
    currency: string;
    input_per_mtok: number; // markup 后实际计费单价
    output_per_mtok: number;
  };
}

/** GET /v1/me/models?status=active — 全平台 official catalog (global)。 */
export async function listRelayModels(): Promise<RelayModelDTO[]> {
  const r = await get<{ items?: RelayModelDTO[] }>('/v1/me/models?status=active');
  return r.items || [];
}

// ── 聚合: 拉所有可用模型给 picker ─────────────────────────────────
//
// 流程 (与 Flutter _ModelPicker 同):
//   1. listProviders 拿启用的 providers
//   2. official 渠道首先一项 "BiuMind" (model id = BIUMIND_OFFICIAL_MODEL)
//   3. 其余启用的 BYOK provider (有 api key) 各自拉 models, 平铺
//   4. 失败时 fallback 到 builtin catalog (至少能 show 几个常见模型)

export async function loadModelEntries(): Promise<ModelEntry[]> {
  let providers: ProviderDTO[] = [];
  try {
    providers = await listProviders();
  } catch {
    // brain 不可用 → BYOK 空, 但 official (model-relay) 仍试。
  }

  const entries: ModelEntry[] = [];

  // Official — P6: model-relay global catalog (mode=='chat'), 直读跳 brain。
  try {
    const relay = await listRelayModels();
    for (const m of relay) {
      if (m.mode !== 'chat') continue;
      entries.push({
        modelId: m.code,
        label: m.display_name || m.code,
        providerName: 'BiuMind Cloud',
        isOfficial: true,
      });
    }
  } catch {
    // model-relay 不可用 → official 空, 继续 BYOK。
  }

  // BYOK / 其他启用的 providers (brain per-user, custom / 上游 refresh)
  const byok = providers.filter(
    (p) => p.source !== 'official' && p.enabled && p.has_api_key,
  );
  for (const p of byok) {
    let models: ModelDTO[] = [];
    try {
      models = await listProviderModels(p.id);
    } catch {
      continue; // 单 provider 失败不阻塞整体
    }
    for (const m of models) {
      if (!m.enabled) continue;
      if (m.type !== 'chat') continue;
      entries.push({
        modelId: m.model_id,
        label: m.display_name || m.model_id,
        providerName: p.display_name || providerDisplayName(p.provider_id),
        isOfficial: false,
      });
    }
  }

  if (entries.length === 0) return fallbackEntries();
  return entries;
}

// 后端不可用 / 无任何模型时的 fallback — P6 删 catalog 后用 official 哨兵
// 一项 (model-relay/brain 都不可用时至少显一个, 选中走默认路由)。
function fallbackEntries(): ModelEntry[] {
  return [
    {
      modelId: BIUMIND_OFFICIAL_MODEL,
      label: 'BiuMind',
      providerName: 'Official',
      isOfficial: true,
    },
  ];
}

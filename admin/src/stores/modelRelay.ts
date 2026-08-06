// Pinia store for model_relay admin shared state.
//
// Scope: cross-page state only.
//   - displayCurrency  (CNY | USD)  — toggle in the top-right of /models;
//                                     pricing display everywhere uses it
//   - fxRates                       — refreshed on demand, used by
//                                     `convertCurrency()` so UI doesn't
//                                     do its own math
//
// Per-page lists (models / channels / credentials / providers) are
// fetched directly from each view via api/modelRelay.ts. We keep this
// store narrow on purpose — caching list state at this layer creates
// stale-display bugs and we already have a 60s in-memory cache on the
// server side.

import { defineStore } from 'pinia'
import { ref, computed } from 'vue'

import { listFxRates } from '@/api/modelRelay'
import type { Currency, FxRate } from '@/api/modelRelay.types'

const STORAGE_KEY = 'biumind.admin.model_relay.display_currency'

function loadInitialCurrency(): Currency {
  if (typeof localStorage === 'undefined') return 'CNY'
  const saved = localStorage.getItem(STORAGE_KEY)
  return saved === 'USD' || saved === 'CNY' ? saved : 'CNY'
}

export const useModelRelayStore = defineStore('modelRelay', () => {
  // ── Display currency ───────────────────────────────────────────
  const displayCurrency = ref<Currency>(loadInitialCurrency())

  function setDisplayCurrency(c: Currency) {
    displayCurrency.value = c
    if (typeof localStorage !== 'undefined') {
      localStorage.setItem(STORAGE_KEY, c)
    }
  }

  // ── Fx rates ───────────────────────────────────────────────────
  const fxRates = ref<FxRate[]>([])
  const fxStalest = ref<FxRate | undefined>()
  const fxStalestAgeSeconds = ref<number>(0)
  const fxLoadedAt = ref<number>(0)

  async function refreshFxRates(force = false) {
    // 5min freshness — the rates table is admin-edited and changes
    // rarely; over-refreshing is wasted RTT.
    if (!force && fxLoadedAt.value > 0 && Date.now() - fxLoadedAt.value < 5 * 60 * 1000) {
      return
    }
    const env = await listFxRates()
    fxRates.value = env.items
    fxStalest.value = env.stalest
    fxStalestAgeSeconds.value = env.stalest_age_seconds ?? 0
    fxLoadedAt.value = Date.now()
  }

  /**
   * Convert a price in `from` currency to `to` using the cached fx
   * table. self-reflexive returns input unchanged. Missing rate
   * returns the input — caller's choice to flag it; the table seeds
   * USD↔CNY both directions so this only fails on data corruption.
   */
  function convertCurrency(amount: number, from: Currency, to: Currency): number {
    if (from === to) return amount
    const rate = fxRates.value.find((r) => r.from_currency === from && r.to_currency === to)
    if (!rate) return amount
    return amount * rate.rate
  }

  /** True when fxStalestAgeSeconds exceeds the warning threshold (14d). */
  const fxStale = computed(() => fxStalestAgeSeconds.value > 14 * 86_400)

  return {
    displayCurrency,
    setDisplayCurrency,
    fxRates,
    fxStalest,
    fxStalestAgeSeconds,
    fxStale,
    refreshFxRates,
    convertCurrency,
  }
})

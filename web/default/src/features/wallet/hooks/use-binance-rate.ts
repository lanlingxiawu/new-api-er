/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { useState, useEffect, useRef } from 'react'
import { getUSDCNYRate } from '../api'

// ============================================================================
// 模块级缓存：5 分钟内多个组件共用同一份数据，避免重复请求
// ============================================================================

const CACHE_TTL_MS = 5 * 60 * 1000

let cachedRate: number | null = null
let cacheTime = 0
let pendingPromise: Promise<number> | null = null

async function fetchRate(): Promise<number> {
  const now = Date.now()
  if (cachedRate !== null && now - cacheTime < CACHE_TTL_MS) {
    return cachedRate
  }
  if (pendingPromise) {
    return pendingPromise
  }

  pendingPromise = (async () => {
    try {
      const res = await getUSDCNYRate()
      if (res.success && res.data?.rate && res.data.rate > 0) {
        cachedRate = res.data.rate
        cacheTime = Date.now()
        return cachedRate
      }
    } catch {
      // 请求失败时保留上次缓存或返回 0（调用方应处理 0 的情况）
    } finally {
      pendingPromise = null
    }
    return cachedRate ?? 0
  })()

  return pendingPromise
}

// ============================================================================
// Hook
// ============================================================================

/**
 * 获取 Binance P2P 实时 USD/CNY 参考汇率，前端 5 分钟缓存
 * - rate = 0 时表示获取失败，调用方应降级处理（使用系统配置汇率）
 */
export function useBinanceRate() {
  const [rate, setRate] = useState<number>(cachedRate ?? 0)
  const [loading, setLoading] = useState(cachedRate === null)
  const mounted = useRef(true)

  useEffect(() => {
    mounted.current = true
    setLoading(true)
    fetchRate().then((r) => {
      if (mounted.current) {
        setRate(r)
        setLoading(false)
      }
    })
    return () => {
      mounted.current = false
    }
  }, [])

  return { rate, loading }
}

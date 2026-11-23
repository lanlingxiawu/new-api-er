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
import type { CacheGuidanceData } from './cache-guidance'

export function formatBytes(bytes: number | undefined, decimals = 2): string {
  if (bytes === undefined || !Number.isFinite(bytes)) return '\u2014'
  if (bytes === 0) return '0 Bytes'
  if (bytes < 0) return `-${formatBytes(-bytes, decimals)}`
  const k = 1024
  const sizes = ['Bytes', 'KiB', 'MiB', 'GiB', 'TiB']
  const i = Math.floor(Math.log(Math.abs(bytes)) / Math.log(k))
  if (i < 0 || i >= sizes.length) return `${bytes} Bytes`
  return `${Number.parseFloat((bytes / Math.pow(k, i)).toFixed(decimals))} ${
    sizes[i]
  }`
}

export type PerformanceStats = {
  guidance?: CacheGuidanceData
  cache_stats?: {
    body_memory_reserved_bytes: number
    body_capacity_rejects: number
    disk_reserved_bytes: number
    current_disk_usage_bytes: number
    disk_cache_max_bytes: number
    active_disk_files: number
    disk_cache_hits: number
    current_memory_usage_bytes: number
    active_memory_buffers: number
    memory_cache_hits: number
  }
  disk_space_info?: {
    total: number
    free: number
    used: number
    used_percent: number
  }
  memory_stats?: {
    alloc: number
    total_alloc: number
    sys: number
    num_gc: number
    num_goroutine: number
  }
  system_status?: {
    cpu_usage: number
    memory_usage: number
    disk_usage: number
  }
  redis_pool?: {
    enabled: boolean
    pool_size: number
    total_conns: number
    idle_conns: number
    stale_conns: number
    hits: number
    misses: number
    timeouts: number
  }
  disk_cache_info?: {
    path: string
    file_count: number
    total_size: number
  }
  config?: {
    is_running_in_container: boolean
    disk_cache_path: string
  }
}

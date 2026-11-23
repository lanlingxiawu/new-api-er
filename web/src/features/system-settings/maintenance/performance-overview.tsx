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
import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { Progress } from '@/components/ui/progress'

import { formatBytes, type PerformanceStats } from './performance-stats'

function PerformanceMetric({
  label,
  value,
  description,
}: {
  label: string
  value: ReactNode
  description?: string
}) {
  return (
    <div className='min-w-0 space-y-1'>
      <dt className='text-muted-foreground text-xs'>{label}</dt>
      <dd className='text-sm font-medium break-words tabular-nums'>
        {value ?? '\u2014'}
      </dd>
      {description && (
        <p className='text-muted-foreground text-xs leading-relaxed'>
          {description}
        </p>
      )}
    </div>
  )
}

export function PerformanceOverview({ stats }: { stats: PerformanceStats }) {
  const { t } = useTranslation()
  const cache = stats.cache_stats
  const memory = stats.memory_stats
  const disk = stats.disk_space_info
  const redis = stats.redis_pool
  const cachePercent =
    cache && cache.disk_cache_max_bytes > 0
      ? Math.min(
          100,
          Math.max(
            0,
            (cache.current_disk_usage_bytes / cache.disk_cache_max_bytes) * 100
          )
        )
      : undefined
  return (
    <div className='grid gap-4 lg:grid-cols-2'>
      <section className='min-w-0 space-y-4 rounded-xl border p-4 sm:p-5'>
        <h4 className='text-sm font-semibold'>{t('Request body cache')}</h4>
        <div className='space-y-3'>
          <h5 className='text-sm font-medium'>
            {t('Request Body Memory Cache')}
          </h5>
          <dl className='grid grid-cols-2 gap-4'>
            <PerformanceMetric
              label={t('Current Cache Size')}
              value={formatBytes(cache?.current_memory_usage_bytes)}
            />
            <PerformanceMetric
              label={t('Reserved body memory')}
              value={formatBytes(cache?.body_memory_reserved_bytes)}
            />
            <PerformanceMetric
              label={t('Active Cache Count')}
              value={cache?.active_memory_buffers}
            />
            <PerformanceMetric
              label={t('Memory Hits')}
              value={cache?.memory_cache_hits}
            />
          </dl>
          <dl>
            <PerformanceMetric
              label={t('Reservation failures (including successful fallback)')}
              value={cache?.body_capacity_rejects}
            />
          </dl>
        </div>
        <div className='space-y-3 border-t pt-4'>
          <h5 className='text-sm font-medium'>
            {t('Request Body Disk Cache')}
          </h5>
          {cachePercent !== undefined && (
            <Progress
              value={cachePercent}
              aria-label={t('Request Body Disk Cache')}
            />
          )}
          <dl className='grid grid-cols-2 gap-4'>
            <PerformanceMetric
              label={t('Used')}
              value={formatBytes(cache?.current_disk_usage_bytes)}
            />
            <PerformanceMetric
              label={t('Disk cache budget')}
              value={formatBytes(cache?.disk_cache_max_bytes)}
            />
            <PerformanceMetric
              label={t('Active Files')}
              value={cache?.active_disk_files}
            />
            <PerformanceMetric
              label={t('Disk Hits')}
              value={cache?.disk_cache_hits}
            />
          </dl>
        </div>
      </section>
      <section className='min-w-0 space-y-4 rounded-xl border p-4 sm:p-5'>
        <h4 className='text-sm font-semibold'>{t('Go Runtime Memory')}</h4>
        <dl className='grid grid-cols-2 gap-4'>
          <PerformanceMetric
            label={t('Allocated Memory')}
            value={formatBytes(memory?.alloc)}
            description={t(
              'Allocated memory is the live Go heap. It includes the request-body cache and other objects, and it is not the operating-system resident size.'
            )}
          />
          <PerformanceMetric
            label={t('Go Reserved Memory')}
            value={formatBytes(memory?.sys)}
            description={t(
              'Go reserved memory is what the runtime holds from the operating system. It is not the resident set size.'
            )}
          />
          <PerformanceMetric
            label={t('Total Allocated')}
            value={formatBytes(memory?.total_alloc)}
            description={t(
              'Total allocated counts every allocation since the process started. It is not the current usage.'
            )}
          />
          <PerformanceMetric
            label={t('Outside body cache')}
            value={formatBytes(
              memory && cache
                ? Math.max(0, memory.alloc - cache.current_memory_usage_bytes)
                : undefined
            )}
            description={t(
              'This remainder includes parsed requests, billing data and objects waiting for collection. The body-cache budget does not limit it.'
            )}
          />
          <PerformanceMetric label={t('GC Count')} value={memory?.num_gc} />
          <PerformanceMetric label='Goroutines' value={memory?.num_goroutine} />
          <PerformanceMetric
            label={t('Host memory')}
            value={
              stats.system_status
                ? `${stats.system_status.memory_usage.toFixed(1)}%`
                : undefined
            }
          />
        </dl>
      </section>
      <section className='min-w-0 space-y-4 rounded-xl border p-4 sm:p-5'>
        <h4 className='text-sm font-semibold'>
          {t('Cache Directory Disk Space')}
        </h4>
        {disk && disk.total > 0 && (
          <Progress
            value={Math.min(100, Math.max(0, disk.used_percent))}
            aria-label={t('Cache Directory Disk Space')}
          />
        )}
        <dl className='grid grid-cols-2 gap-4 sm:grid-cols-3'>
          <PerformanceMetric
            label={t('Used')}
            value={formatBytes(disk?.used)}
          />
          <PerformanceMetric
            label={t('Available')}
            value={formatBytes(disk?.free)}
          />
          <PerformanceMetric
            label={t('Total')}
            value={formatBytes(disk?.total)}
          />
        </dl>
        <dl className='space-y-4 border-t pt-4'>
          <PerformanceMetric
            label={t('Cache Directory')}
            value={
              <span className='font-mono break-all'>
                {stats.disk_cache_info?.path || '\u2014'}
              </span>
            }
          />
          <div className='grid grid-cols-2 gap-4'>
            <PerformanceMetric
              label={t('Directory File Count')}
              value={stats.disk_cache_info?.file_count}
            />
            <PerformanceMetric
              label={t('Directory Total Size')}
              value={formatBytes(stats.disk_cache_info?.total_size)}
            />
          </div>
        </dl>
      </section>
      <section className='min-w-0 space-y-4 rounded-xl border p-4 sm:p-5'>
        <h4 className='text-sm font-semibold'>{t('Redis connection pool')}</h4>
        {redis?.enabled === false ? (
          <p className='text-muted-foreground text-sm'>
            {t('Redis is not enabled on this node.')}
          </p>
        ) : (
          <>
            <dl className='grid grid-cols-2 gap-4'>
              <PerformanceMetric
                label={t('Configured pool size')}
                value={redis?.pool_size}
              />
              <PerformanceMetric
                label={t('Connections in use')}
                value={
                  redis
                    ? Math.max(0, redis.total_conns - redis.idle_conns)
                    : undefined
                }
              />
              <PerformanceMetric
                label={t('Idle connections')}
                value={redis?.idle_conns}
              />
              <PerformanceMetric
                label={t('Pool wait timeouts')}
                value={redis?.timeouts}
              />
            </dl>
            <p className='text-muted-foreground text-xs leading-relaxed'>
              {t(
                'Pool wait timeouts count every time a caller gave up waiting for a connection since this process started. Set REDIS_POOL_SIZE and restart to change the pool. A larger pool does not help when Redis itself is slow.'
              )}
            </p>
          </>
        )}
      </section>
    </div>
  )
}

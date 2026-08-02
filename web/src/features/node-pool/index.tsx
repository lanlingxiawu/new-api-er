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
// xiugai 添加号池节点功能
import { useEffect, useState, useCallback, useRef, startTransition } from 'react'
import { useTranslation } from 'react-i18next'
import { useRouterState } from '@tanstack/react-router'
import {
  Activity,
  AlertCircle,
  CheckCircle2,
  CircleDashed,
  Cpu,
  Database,
  Loader2,
  MemoryStick,
  Network,
  RefreshCw,
  Server,
  Trash2,
  Users,
  Wallet,
  XCircle,
} from 'lucide-react'
import { cn } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { SectionPageLayout } from '@/components/layout'
import { deleteNode, getNodeAccounts, getNodes } from './api'
import type { Node, NodeAccount, NodeStats } from './types'

function formatNumber(n: number | undefined | null, decimals = 2): string {
  if (n == null) return '–'
  if (n === 0) return '0'
  return n.toFixed(decimals)
}

function StatCard({
  label,
  value,
  icon: Icon,
  variant,
}: {
  label: string
  value: string | number
  icon: React.ElementType
  variant?: 'default' | 'online' | 'offline'
}) {
  return (
    <div
      className={cn(
        'flex min-w-0 flex-1 flex-col gap-1 rounded-lg border bg-card p-3 shadow-sm',
        variant === 'online' && 'border-emerald-200 bg-emerald-50 dark:border-emerald-800 dark:bg-emerald-950/40',
        variant === 'offline' && 'border-red-200 bg-red-50 dark:border-red-800 dark:bg-red-950/40'
      )}
    >
      <div className='flex items-center gap-1.5 text-xs text-muted-foreground'>
        <Icon
          className={cn(
            'size-3.5',
            variant === 'online' && 'text-emerald-600 dark:text-emerald-400',
            variant === 'offline' && 'text-red-600 dark:text-red-400'
          )}
        />
        <span>{label}</span>
      </div>
      <p
        className={cn(
          'text-xl font-bold tabular-nums',
          variant === 'online' && 'text-emerald-600 dark:text-emerald-400',
          variant === 'offline' && 'text-red-600 dark:text-red-400'
        )}
      >
        {value}
      </p>
    </div>
  )
}

function NodeStatusBadge({ status }: { status: string }) {
  const { t } = useTranslation()
  if (status === 'online') {
    return (
      <Badge variant='default' className='gap-1 bg-emerald-600 text-white hover:bg-emerald-600 dark:bg-emerald-500 dark:hover:bg-emerald-500'>
        <CheckCircle2 className='size-3' />
        {t('Online')}
      </Badge>
    )
  }
  return (
    <Badge variant='destructive' className='gap-1'>
      <XCircle className='size-3' />
      {t('Offline')}
    </Badge>
  )
}

function AccountStatusBadge({ status }: { status: string }) {
  const cls =
    status === '正常'
      ? 'bg-emerald-600 text-white hover:bg-emerald-600 dark:bg-emerald-500 dark:hover:bg-emerald-500'
      : 'bg-gray-400 text-white hover:bg-gray-400'
  return (
    <Badge variant='default' className={cn('text-[10px]', cls)}>
      {status}
    </Badge>
  )
}

type AccountStats = { available: number; total: number }

type NodeListItemProps = {
  node: Node
  selected: boolean
  onClick: () => void
  accountStats?: AccountStats
}

function NodeListItem({ node, selected, onClick, accountStats }: NodeListItemProps) {
  return (
    <button
      type='button'
      onClick={onClick}
      className={cn(
        'w-full rounded-lg border p-3 text-left transition-colors hover:bg-muted/60',
        selected && 'border-primary bg-primary/5'
      )}
    >
      <div className='flex items-start justify-between gap-2'>
        <div className='min-w-0 flex-1'>
          <p className='truncate text-sm font-medium'>{node.node_name}</p>
          <p className='truncate text-xs text-muted-foreground'>
            {node.public_ip}:{node.listen_port}
          </p>
        </div>
        <NodeStatusBadge status={node.status} />
      </div>
      <div className='mt-2 grid grid-cols-3 gap-x-2 gap-y-1 text-xs text-muted-foreground'>
        <span className='flex items-center gap-1'>
          <Cpu className='size-3 shrink-0' />
          {formatNumber(node.cpu_usage, 1)}%
        </span>
        <span className='flex items-center gap-1'>
          <MemoryStick className='size-3 shrink-0' />
          {formatNumber(node.mem_usage, 1)}%
        </span>
        <span className='flex items-center gap-1'>
          <Network className='size-3 shrink-0' />
          {formatNumber(node.upload_bandwidth, 0)}↑
        </span>
        {accountStats != null && (
          <span className='col-span-3 mt-0.5 flex items-center gap-1'>
            <Users className='size-3 shrink-0' />
            <span className='text-emerald-600 dark:text-emerald-400'>{accountStats.available}</span>
            <span>/</span>
            <span>{accountStats.total}</span>
          </span>
        )}
      </div>
    </button>
  )
}

type NodeDetailFieldProps = {
  label: string
  value: React.ReactNode
}

function NodeDetailField({ label, value }: NodeDetailFieldProps) {
  return (
    <div className='flex flex-col gap-0.5 rounded-md bg-muted/40 px-3 py-2 text-sm'>
      <span className='text-xs text-muted-foreground'>{label}</span>
      <span className='min-w-0 break-all font-medium'>{value}</span>
    </div>
  )
}

// xiugai 添加号池节点功能 - 离线节点删除按钮
function NodeDetailPanel({
  node,
  onDelete,
  deleting,
}: {
  node: Node
  onDelete: () => void
  deleting: boolean
}) {
  const { t } = useTranslation()
  const [confirming, setConfirming] = useState(false)

  const handleClick = () => {
    if (!confirming) {
      setConfirming(true)
      return
    }
    setConfirming(false)
    onDelete()
  }

  return (
    <div className='shrink-0 rounded-lg border bg-card p-4'>
      <div className='mb-3 flex items-center justify-between'>
        <h3 className='text-sm font-semibold'>{t('Node Details')}</h3>
        {node.status === 'offline' && (
          <Button
            size='sm'
            variant={confirming ? 'destructive' : 'outline'}
            onClick={handleClick}
            disabled={deleting}
            className='h-7 gap-1.5 text-xs'
          >
            {deleting ? (
              <Loader2 className='size-3 animate-spin' />
            ) : (
              <Trash2 className='size-3' />
            )}
            {confirming ? t('Confirm Delete') : t('Delete Node')}
          </Button>
        )}
      </div>
      <div className='grid grid-cols-2 gap-2 sm:grid-cols-4'>
        <NodeDetailField label={t('Node Name')} value={node.node_name} />
        <NodeDetailField label={t('Public IP')} value={node.public_ip} />
        <NodeDetailField label={t('Internal IP')} value={node.internal_ip} />
        <NodeDetailField label={t('Port')} value={node.listen_port} />
        <NodeDetailField label={t('Status')} value={<NodeStatusBadge status={node.status} />} />
        <NodeDetailField label={t('Last Seen')} value={node.last_seen ? new Date(node.last_seen).toLocaleString() : '–'} />
        <NodeDetailField label={t('CPU Usage')} value={`${formatNumber(node.cpu_usage, 1)}%`} />
        <NodeDetailField label={t('Memory Usage')} value={`${formatNumber(node.mem_usage, 1)}%`} />
        <NodeDetailField label={t('Upload Bandwidth')} value={`${formatNumber(node.upload_bandwidth, 1)} Mbps`} />
        <NodeDetailField label={t('Download Bandwidth')} value={`${formatNumber(node.download_bandwidth, 1)} Mbps`} />
        <NodeDetailField label={t('Today Requests')} value={node.today_requests ?? '–'} />
        <NodeDetailField label={t('Total Requests')} value={node.total_requests ?? '–'} />
        <NodeDetailField label={t('Today Consumption')} value={`$${formatNumber(node.today_consumption)}`} />
        <NodeDetailField label={t('Total Consumption')} value={`$${formatNumber(node.total_consumption)}`} />
        <NodeDetailField label={t('Total RPM')} value={formatNumber(node.total_rpm, 1)} />
        <NodeDetailField label={t('Current RPM')} value={formatNumber(node.current_rpm, 1)} />
        <NodeDetailField label={t('Avg Response Time')} value={`${formatNumber(node.avg_response_time, 0)} ms`} />
      </div>
    </div>
  )
}

const PAGE_SIZE_OPTIONS = [10, 20, 50, 100]

type StatusFilter = 'all' | 'online' | 'offline'

function AccountsTable({ accounts, loading }: { accounts: NodeAccount[]; loading: boolean }) {
  const { t } = useTranslation()
  const [page, setPage] = useState(1)
  const [pageSize, setPageSize] = useState(10)
  const [filterId, setFilterId] = useState('')
  const [filterName, setFilterName] = useState('')
  const [filterStatus, setFilterStatus] = useState<StatusFilter>('all')

  useEffect(() => {
    startTransition(() => setPage(1))
  }, [accounts])

  const filtered = accounts.filter((acc) => {
    if (filterId && !acc.id.toLowerCase().includes(filterId.toLowerCase())) return false
    if (filterName && !(acc.name ?? '').toLowerCase().includes(filterName.toLowerCase())) return false
    if (filterStatus === 'online' && acc.status !== '正常') return false
    if (filterStatus === 'offline' && acc.status === '正常') return false
    return true
  })

  const totalPages = Math.ceil(filtered.length / pageSize)
  const pageAccounts = filtered.slice((page - 1) * pageSize, page * pageSize)
  const hasData = !loading && accounts.length > 0 && filtered.length > 0

  return (
    <div className='flex flex-col'>
      {/* 筛选栏 — 固定顶部，不随表格滚动 */}
      {!loading && accounts.length > 0 && (
        <div className='flex shrink-0 flex-wrap items-center gap-2 border-b px-4 py-2.5'>
          <Input
            className='h-7 w-36 text-xs'
            placeholder={t('Filter by ID')}
            value={filterId}
            onChange={(e) => { setFilterId(e.target.value); startTransition(() => setPage(1)) }}
          />
          <Input
            className='h-7 w-36 text-xs'
            placeholder={t('Filter by Name')}
            value={filterName}
            onChange={(e) => { setFilterName(e.target.value); startTransition(() => setPage(1)) }}
          />
          <div className='flex rounded-md border text-xs'>
            {(['all', 'online', 'offline'] as StatusFilter[]).map((s) => (
              <button
                key={s}
                type='button'
                onClick={() => { setFilterStatus(s); startTransition(() => setPage(1)) }}
                className={cn(
                  'px-2.5 py-1 first:rounded-l-md last:rounded-r-md transition-colors',
                  filterStatus === s
                    ? 'bg-primary text-primary-foreground'
                    : 'hover:bg-muted'
                )}
              >
                {s === 'all' ? t('All') : s === 'online' ? t('Online') : t('Offline')}
              </button>
            ))}
          </div>
        </div>
      )}

      {/* 表格区域 — 仅此层滚动 */}
      <div className='overflow-auto px-4' style={{ maxHeight: 'calc(100vh - 440px)', minHeight: 120 }}>
        {loading ? (
          <div className='flex h-24 items-center justify-center text-muted-foreground'>
            <Loader2 className='mr-2 size-4 animate-spin' />
            {t('Loading accounts...')}
          </div>
        ) : accounts.length === 0 ? (
          <div className='flex h-24 items-center justify-center text-sm text-muted-foreground'>
            {t('No accounts')}
          </div>
        ) : filtered.length === 0 ? (
          <div className='flex h-16 items-center justify-center text-sm text-muted-foreground'>
            {t('No matching accounts')}
          </div>
        ) : (
          <table className='w-full text-sm'>
            <thead className='sticky top-0 z-10 bg-card'>
              <tr className='border-b text-left text-xs text-muted-foreground'>
                <th className='pb-2 pr-3 pt-3 font-medium'>{t('ID')}</th>
                <th className='pb-2 pr-3 pt-3 font-medium'>{t('Name')}</th>
                <th className='pb-2 pr-3 pt-3 font-medium'>{t('Status')}</th>
                <th className='pb-2 pr-3 pt-3 font-medium text-right'>{t('Requests')}</th>
                <th className='pb-2 pr-3 pt-3 font-medium text-right'>{t('Token Count')}</th>
                <th className='pb-2 pr-3 pt-3 font-medium text-right'>{t('Account Cost')}</th>
                <th className='pb-2 pr-3 pt-3 font-medium text-right'>{t('User Cost')}</th>
                <th className='pb-2 pt-3 font-medium text-right'>{t('Used / Total')}</th>
              </tr>
            </thead>
            <tbody>
              {pageAccounts.map((acc) => (
                <tr key={acc.id} className='border-b last:border-b-0 hover:bg-muted/40'>
                  <td className='py-2 pr-3 font-mono text-xs'>{acc.id}</td>
                  <td className='py-2 pr-3'>{acc.name || '–'}</td>
                  <td className='py-2 pr-3'>
                    <AccountStatusBadge status={acc.status} />
                  </td>
                  <td className='py-2 pr-3 text-right tabular-nums'>{acc.req ?? 0}</td>
                  <td className='py-2 pr-3 text-right tabular-nums'>{acc.tokens ?? 0}</td>
                  <td className='py-2 pr-3 text-right tabular-nums'>${formatNumber(acc.account_cost)}</td>
                  <td className='py-2 pr-3 text-right tabular-nums'>${formatNumber(acc.user_cost)}</td>
                  <td className='py-2 text-right tabular-nums'>
                    {acc.used_capacity != null || acc.total_capacity != null ? (
                      <div className='flex flex-col items-end gap-1'>
                        <span>{formatNumber(acc.used_capacity)} / {formatNumber(acc.total_capacity)}</span>
                        <div className='h-1 w-16 overflow-hidden rounded-full bg-muted'>
                          <div
                            className='h-full rounded-full bg-primary transition-all'
                            style={{
                              width: `${Math.min(100, acc.total_capacity > 0 ? (acc.used_capacity / acc.total_capacity) * 100 : 0).toFixed(1)}%`,
                            }}
                          />
                        </div>
                      </div>
                    ) : '–'}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      {/* 分页栏 — 固定底部，不随表格滚动 */}
      {hasData && (
        <div className='flex shrink-0 items-center justify-between border-t px-4 py-2.5 text-xs text-muted-foreground'>
          <div className='flex items-center gap-2'>
            <span>
              {(page - 1) * pageSize + 1}–{Math.min(page * pageSize, filtered.length)} / {filtered.length}
            </span>
            <select
              value={pageSize}
              onChange={(e) => { setPageSize(Number(e.target.value)); startTransition(() => setPage(1)) }}
              className='h-6 rounded border bg-background px-1 text-xs'
            >
              {PAGE_SIZE_OPTIONS.map((n) => (
                <option key={n} value={n}>{n} / {t('page')}</option>
              ))}
            </select>
          </div>
          <div className='flex items-center gap-1'>
            <Button
              size='sm'
              variant='outline'
              className='h-6 px-2 text-xs'
              disabled={page <= 1}
              onClick={() => setPage((p) => p - 1)}
            >
              {t('Prev')}
            </Button>
            <span className='px-1 tabular-nums'>{page} / {totalPages}</span>
            <Button
              size='sm'
              variant='outline'
              className='h-6 px-2 text-xs'
              disabled={page >= totalPages}
              onClick={() => setPage((p) => p + 1)}
            >
              {t('Next')}
            </Button>
          </div>
        </div>
      )}
    </div>
  )
}

export function NodePool() {
  const { t } = useTranslation()
  const isNodePoolRoute = useRouterState({
    select: (state) =>
      state.location.pathname === '/node-pool' ||
      state.location.pathname === '/node-pool/',
  })
  const [nodes, setNodes] = useState<Node[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [selectedNode, setSelectedNode] = useState<Node | null>(null)
  const [accounts, setAccounts] = useState<NodeAccount[]>([])
  const [accountsLoading, setAccountsLoading] = useState(false)
  const [accountStatsCache, setAccountStatsCache] = useState<Record<string, AccountStats>>({})
  // xiugai 添加号池节点功能 - 离线节点删除
  const [deleting, setDeleting] = useState(false)
  // end
  const selectedNodeRef = useRef<Node | null>(null)
  // xiugai 添加号池节点功能 - 修复快速切换节点账号列表竞态
  const fetchAccountsSeqRef = useRef(0)
  // end

  // 更新展示账号 + 缓存（带竞态保护）
  const fetchAccounts = useCallback(async (node: Node) => {
    const seq = ++fetchAccountsSeqRef.current
    setAccountsLoading(true)
    setAccounts([])
    try {
      const data = await getNodeAccounts(node.node_name)
      if (seq !== fetchAccountsSeqRef.current) return
      const list = data.accounts ?? []
      setAccounts(list)
      const available = list.filter((a) => a.status === '正常').length
      setAccountStatsCache((prev) => ({ ...prev, [node.node_name]: { available, total: list.length } }))
    } catch {
      if (seq === fetchAccountsSeqRef.current) setAccounts([])
    } finally {
      if (seq === fetchAccountsSeqRef.current) setAccountsLoading(false)
    }
  }, [])

  // 仅更新缓存，不修改展示账号状态
  const fetchAccountsForCache = useCallback(async (node: Node) => {
    try {
      const data = await getNodeAccounts(node.node_name)
      const list = data.accounts ?? []
      const available = list.filter((a) => a.status === '正常').length
      setAccountStatsCache((prev) => ({ ...prev, [node.node_name]: { available, total: list.length } }))
    } catch {
      // 缓存拉取失败静默忽略
    }
  }, [])

  const fetchNodes = useCallback(async (skipBulkCache = false) => {
    if (!isNodePoolRoute) return
    setLoading(true)
    setError(null)
    try {
      const data = await getNodes()
      const list = data.nodes ?? []
      setNodes(list)
      if (list.length === 0) return

      // 保持或自动选中第一个节点
      const cur = selectedNodeRef.current
      let nodeToSelect = cur
        ? list.find((n) => n.public_ip === cur.public_ip && n.node_name === cur.node_name)
        : undefined
      if (!nodeToSelect) nodeToSelect = list[0]
      selectedNodeRef.current = nodeToSelect
      setSelectedNode(nodeToSelect)

      // 选中节点：拉取展示账号（同时更新缓存）
      fetchAccounts(nodeToSelect)
      // 其余节点：并发拉取缓存（删除节点后跳过，避免批量请求所有节点）
      if (!skipBulkCache) {
        list
          .filter((n) => n.node_name !== nodeToSelect!.node_name)
          .forEach((n) => fetchAccountsForCache(n))
      }
    } catch {
      setError(t('Node pool is temporarily unavailable, please try again later'))
    } finally {
      setLoading(false)
    }
  }, [t, isNodePoolRoute, fetchAccounts, fetchAccountsForCache])

  useEffect(() => {
    // Keep the initial node fetch behavior aligned with the classic UI.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    void fetchNodes()
  }, [fetchNodes])

  const handleSelectNode = useCallback(
    (node: Node) => {
      selectedNodeRef.current = node
      setSelectedNode(node)
      fetchAccounts(node)
    },
    [fetchAccounts]
  )

  // xiugai 添加号池节点功能 - 离线节点删除
  const handleDeleteNode = useCallback(async () => {
    if (!selectedNode) return
    setDeleting(true)
    try {
      await deleteNode(selectedNode.node_name)
      selectedNodeRef.current = null
      setSelectedNode(null)
      setAccounts([])
      setAccountStatsCache((prev) => { const n = { ...prev }; delete n[selectedNode.node_name]; return n })
      await fetchNodes(true)
    } catch {
      setError(t('Failed to delete node'))
    } finally {
      setDeleting(false)
    }
  }, [selectedNode, fetchNodes, t])
  // end

  const stats: NodeStats = {
    total: nodes.length,
    online: nodes.filter((n) => n.status === 'online').length,
    offline: nodes.filter((n) => n.status === 'offline').length,
    todayConsumption: nodes.reduce((s, n) => s + (n.today_consumption ?? 0), 0),
    totalConsumption: nodes.reduce((s, n) => s + (n.total_consumption ?? 0), 0),
    totalRpm: nodes.reduce((s, n) => s + (n.total_rpm ?? 0), 0),
  }

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('Node Pool')}</SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        <Button
          size='sm'
          variant='outline'
          onClick={() => fetchNodes()}
          disabled={loading}
        >
          {loading ? (
            <Loader2 className='mr-1.5 size-3.5 animate-spin' />
          ) : (
            <RefreshCw className='mr-1.5 size-3.5' />
          )}
          {t('Refresh')}
        </Button>
      </SectionPageLayout.Actions>
      <SectionPageLayout.Content>
        {/* Summary stats */}
        <div className='mb-4 flex flex-wrap gap-3'>
          <StatCard label={t('Total Nodes')} value={stats.total} icon={Server} />
          <StatCard label={t('Online')} value={stats.online} icon={CheckCircle2} variant='online' />
          <StatCard label={t('Offline')} value={stats.offline} icon={XCircle} variant='offline' />
          <StatCard
            label={t('Today Consumption')}
            value={`$${formatNumber(stats.todayConsumption)}`}
            icon={Activity}
          />
          <StatCard
            label={t('Total Consumption')}
            value={`$${formatNumber(stats.totalConsumption)}`}
            icon={Wallet}
          />
          <StatCard
            label={t('Total RPM')}
            value={formatNumber(stats.totalRpm, 1)}
            icon={Database}
          />
        </div>

        {error && (
          <div className='mb-4 flex items-center gap-2 rounded-lg border border-destructive/30 bg-destructive/10 px-4 py-3 text-sm text-destructive'>
            <AlertCircle className='size-4 shrink-0' />
            {error}
          </div>
        )}

        {loading && !nodes.length ? (
          <div className='flex h-40 items-center justify-center text-muted-foreground'>
            <Loader2 className='mr-2 size-5 animate-spin' />
            {t('Loading nodes...')}
          </div>
        ) : (
          <div className='flex gap-4' style={{ height: 'calc(100vh - 160px)', minHeight: 400 }}>
            {/* Left: Node list */}
            <div className='flex w-72 shrink-0 flex-col gap-2 overflow-y-auto'>
              {nodes.length === 0 ? (
                <div className='flex h-24 items-center justify-center text-sm text-muted-foreground'>
                  <CircleDashed className='mr-2 size-4' />
                  {t('No nodes found')}
                </div>
              ) : (
                nodes.map((node) => (
                  <NodeListItem
                    key={`${node.public_ip}:${node.node_name}`}
                    node={node}
                    selected={
                      selectedNode?.public_ip === node.public_ip &&
                      selectedNode?.node_name === node.node_name
                    }
                    onClick={() => handleSelectNode(node)}
                    accountStats={accountStatsCache[node.node_name]}
                  />
                ))
              )}
            </div>

            {/* Right: Details + Accounts */}
            <div className='flex min-w-0 flex-1 flex-col gap-4 overflow-y-auto pb-4 [&::-webkit-scrollbar]:w-1.5 [&::-webkit-scrollbar-thumb]:rounded-full [&::-webkit-scrollbar-thumb]:bg-border'>
              {selectedNode ? (
                <>
                  {/* Node detail */}
                  <NodeDetailPanel
                    node={selectedNode}
                    onDelete={handleDeleteNode}
                    deleting={deleting}
                  />

                  {/* Account list */}
                  <div className='flex flex-col rounded-lg border bg-card'>
                    <h3 className='shrink-0 border-b px-4 py-3 text-sm font-semibold'>
                      {t('Accounts')}
                      {!accountsLoading && (
                        <span className='ml-2 text-xs font-normal text-muted-foreground'>
                          ({accounts.length})
                        </span>
                      )}
                    </h3>
                    <AccountsTable accounts={accounts} loading={accountsLoading} />
                  </div>
                </>
              ) : (
                <div className='flex h-full flex-col items-center justify-center gap-3 rounded-lg border border-dashed text-muted-foreground'>
                  <Server className='size-10 opacity-30' />
                  <p className='text-sm'>{t('Select a node to view details')}</p>
                </div>
              )}
            </div>
          </div>
        )}
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
// end

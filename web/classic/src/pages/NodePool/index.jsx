/*
Copyright (C) 2025 QuantumNous

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
import React, { useCallback, useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button, Input, Pagination, Spin, Tag } from '@douyinfe/semi-ui';
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
} from 'lucide-react';
import { API } from '../../helpers';
import './index.css';

function formatNumber(value, decimals = 2) {
  if (value == null) return '–';
  const number = Number(value);
  if (!Number.isFinite(number)) return '–';
  if (number === 0) return '0';
  return number.toFixed(decimals);
}

function formatCount(value) {
  if (value == null) return '–';
  const number = Number(value);
  if (!Number.isFinite(number)) return '–';
  return number.toLocaleString();
}

function getNodeKey(node) {
  return `${node.public_ip}:${node.node_name}`;
}

function isSameNode(a, b) {
  return Boolean(
    a && b && a.public_ip === b.public_ip && a.node_name === b.node_name,
  );
}

function StatCard({ label, value, icon: Icon, variant = 'default' }) {
  return (
    <div className={`node-pool-stat-card node-pool-stat-${variant}`}>
      <span className='node-pool-stat-icon'>
        <Icon size={16} />
      </span>
      <span className='node-pool-stat-copy'>
        <span className='node-pool-stat-label'>{label}</span>
        <strong className='node-pool-stat-value'>{value}</strong>
      </span>
    </div>
  );
}

function NodeStatusTag({ status }) {
  const { t } = useTranslation();
  const online = status === 'online';
  const Icon = online ? CheckCircle2 : XCircle;

  return (
    <Tag
      color={online ? 'green' : 'red'}
      size='small'
      className={`node-pool-status-tag ${online ? 'is-online' : 'is-offline'}`}
    >
      <Icon size={12} />
      {online ? t('在线') : t('离线')}
    </Tag>
  );
}

function AccountStatusTag({ status }) {
  const { t } = useTranslation();
  const normal = status === '正常';

  return (
    <Tag
      color={normal ? 'green' : 'grey'}
      size='small'
      className='node-pool-status-tag'
    >
      {status ? t(status) : '–'}
    </Tag>
  );
}

function NodeMetric({ icon: Icon, value }) {
  return (
    <span className='node-pool-node-metric'>
      <Icon size={13} />
      {value}
    </span>
  );
}

function NodeListItem({ node, selected, onClick, accountStats }) {
  return (
    <button
      type='button'
      className={`node-pool-node-card ${selected ? 'is-selected' : ''}`}
      aria-pressed={selected}
      onClick={onClick}
    >
      <span className='node-pool-node-card-header'>
        <span className='node-pool-node-avatar'>
          <Server size={16} />
        </span>
        <span className='node-pool-node-main'>
          <span className='node-pool-node-name'>{node.node_name}</span>
          <span className='node-pool-node-address'>
            {node.public_ip}:{node.listen_port}
          </span>
        </span>
        <NodeStatusTag status={node.status} />
      </span>
      <span className='node-pool-node-metrics'>
        <NodeMetric icon={Cpu} value={`${formatNumber(node.cpu_usage, 1)}%`} />
        <NodeMetric
          icon={MemoryStick}
          value={`${formatNumber(node.mem_usage, 1)}%`}
        />
        <NodeMetric
          icon={Network}
          value={`${formatNumber(node.upload_bandwidth, 0)}↑`}
        />
        {accountStats != null && (
          <NodeMetric
            icon={Users}
            value={
              <span>
                <span style={{ color: 'var(--semi-color-success)' }}>{accountStats.available}</span>
                /{accountStats.total}
              </span>
            }
          />
        )}
      </span>
    </button>
  );
}

function DetailField({ label, value }) {
  return (
    <div className='node-pool-detail-field'>
      <span className='node-pool-detail-label'>{label}</span>
      <span className='node-pool-detail-value'>{value}</span>
    </div>
  );
}

function NodeDetailPanel({ node, onDelete, deleting }) {
  const { t } = useTranslation();
  const [confirming, setConfirming] = useState(false);

  useEffect(() => {
    setConfirming(false);
  }, [node.public_ip, node.node_name]);

  const handleDeleteClick = () => {
    if (!confirming) {
      setConfirming(true);
      return;
    }
    setConfirming(false);
    onDelete();
  };

  return (
    <section className='node-pool-panel'>
      <div className='node-pool-panel-header'>
        <div className='node-pool-panel-title-group'>
          <span className='node-pool-panel-icon'>
            <Server size={16} />
          </span>
          <div>
            <h2 className='node-pool-panel-title'>{t('节点详情')}</h2>
            <p className='node-pool-panel-subtitle'>
              {node.public_ip}:{node.listen_port}
            </p>
          </div>
        </div>
        {node.status === 'offline' && (
          <Button
            size='small'
            type={confirming ? 'danger' : 'tertiary'}
            theme={confirming ? 'solid' : 'light'}
            loading={deleting}
            icon={
              deleting ? (
                <Loader2 size={13} className='node-pool-spin' />
              ) : (
                <Trash2 size={13} />
              )
            }
            disabled={deleting}
            onClick={handleDeleteClick}
          >
            {confirming ? t('确认删除节点') : t('删除节点')}
          </Button>
        )}
      </div>
      <div className='node-pool-detail-grid'>
        <DetailField label={t('节点名')} value={node.node_name} />
        <DetailField label={t('公网IP')} value={node.public_ip} />
        <DetailField label={t('内网IP')} value={node.internal_ip || '–'} />
        <DetailField label={t('监听端口')} value={node.listen_port ?? '–'} />
        <DetailField
          label={t('状态')}
          value={<NodeStatusTag status={node.status} />}
        />
        <DetailField
          label={t('最后心跳')}
          value={node.last_seen ? new Date(node.last_seen).toLocaleString() : '–'}
        />
        <DetailField
          label='CPU'
          value={`${formatNumber(node.cpu_usage, 1)}%`}
        />
        <DetailField
          label={t('内存')}
          value={`${formatNumber(node.mem_usage, 1)}%`}
        />
        <DetailField
          label={t('上行带宽')}
          value={`${formatNumber(node.upload_bandwidth, 1)} Mbps`}
        />
        <DetailField
          label={t('下行带宽')}
          value={`${formatNumber(node.download_bandwidth, 1)} Mbps`}
        />
        <DetailField
          label={t('今日请求')}
          value={formatCount(node.today_requests)}
        />
        <DetailField label={t('总请求')} value={formatCount(node.total_requests)} />
        <DetailField
          label={t('今日消费')}
          value={`$${formatNumber(node.today_consumption)}`}
        />
        <DetailField
          label={t('累计消费')}
          value={`$${formatNumber(node.total_consumption)}`}
        />
        <DetailField label={t('总RPM')} value={formatNumber(node.total_rpm, 1)} />
        <DetailField
          label={t('当前RPM')}
          value={formatNumber(node.current_rpm, 1)}
        />
        <DetailField
          label={t('平均响应时间')}
          value={`${formatNumber(node.avg_response_time, 0)} ms`}
        />
      </div>
    </section>
  );
}

function AccountRows({ accounts }) {
  return accounts.map((account) => (
    <tr key={account.id}>
      <td className='node-pool-account-id'>{account.id}</td>
      <td>{account.name || '–'}</td>
      <td>
        <AccountStatusTag status={account.status} />
      </td>
      <td className='node-pool-number-cell'>{formatCount(account.req ?? 0)}</td>
      <td className='node-pool-number-cell'>
        {formatCount(account.tokens ?? 0)}
      </td>
      <td className='node-pool-number-cell'>
        ${formatNumber(account.account_cost)}
      </td>
      <td className='node-pool-number-cell'>${formatNumber(account.user_cost)}</td>
      <td className='node-pool-number-cell'>
        {account.used_capacity != null || account.total_capacity != null ? (
          <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'flex-end', gap: 4 }}>
            <span>{formatNumber(account.used_capacity)} / {formatNumber(account.total_capacity)}</span>
            <div style={{ width: 64, height: 4, borderRadius: 9999, background: 'var(--semi-color-fill-2)', overflow: 'hidden' }}>
              <div style={{
                height: '100%',
                borderRadius: 9999,
                background: 'var(--semi-color-primary)',
                width: `${Math.min(100, account.total_capacity > 0 ? (account.used_capacity / account.total_capacity) * 100 : 0).toFixed(1)}%`,
                transition: 'width 0.3s',
              }} />
            </div>
          </div>
        ) : '–'}
      </td>
    </tr>
  ));
}

const PAGE_SIZE_OPTIONS = [10, 20, 50, 100];
const STATUS_FILTERS = ['all', 'online', 'offline'];

function AccountsPanel({ accounts, loading }) {
  const { t } = useTranslation();
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(10);
  const [filterId, setFilterId] = useState('');
  const [filterName, setFilterName] = useState('');
  const [filterStatus, setFilterStatus] = useState('all');

  useEffect(() => {
    setPage(1);
  }, [accounts]);

  const filtered = accounts.filter((acc) => {
    if (filterId && !acc.id.toLowerCase().includes(filterId.toLowerCase())) return false;
    if (filterName && !(acc.name ?? '').toLowerCase().includes(filterName.toLowerCase())) return false;
    if (filterStatus === 'online' && acc.status !== '正常') return false;
    if (filterStatus === 'offline' && acc.status === '正常') return false;
    return true;
  });

  const pageAccounts = filtered.slice((page - 1) * pageSize, page * pageSize);

  const statusLabel = { all: t('全部'), online: t('在线'), offline: t('离线') };

  const hasData = !loading && accounts.length > 0 && filtered.length > 0;

  return (
    <section
      className='node-pool-panel'
      style={{ display: 'flex', flexDirection: 'column', flex: 1, minHeight: 0, padding: 0, overflow: 'hidden' }}
    >
      {/* 标题栏 — 固定顶部 */}
      <div
        className='node-pool-panel-header'
        style={{ flexShrink: 0, padding: '14px 16px', marginBottom: 0, borderBottom: '1px solid var(--semi-color-border)' }}
      >
        <div className='node-pool-panel-title-group'>
          <span className='node-pool-panel-icon'>
            <Database size={16} />
          </span>
          <div>
            <h2 className='node-pool-panel-title'>
              {t('账号列表')}
              {!loading && (
                <span className='node-pool-panel-count'>({accounts.length})</span>
              )}
            </h2>
          </div>
        </div>
      </div>

      {/* 筛选栏 — 固定，不随表格滚动 */}
      {!loading && accounts.length > 0 && (
        <div
          style={{
            flexShrink: 0,
            display: 'flex',
            flexWrap: 'wrap',
            gap: 8,
            padding: '10px 16px',
            borderBottom: '1px solid var(--semi-color-border)',
          }}
        >
          <Input
            size='small'
            placeholder={t('筛选 ID')}
            value={filterId}
            onChange={(v) => { setFilterId(v); setPage(1); }}
            style={{ width: 140 }}
            showClear
          />
          <Input
            size='small'
            placeholder={t('筛选账号名')}
            value={filterName}
            onChange={(v) => { setFilterName(v); setPage(1); }}
            style={{ width: 140 }}
            showClear
          />
          <div style={{ display: 'flex', borderRadius: 6, overflow: 'hidden', border: '1px solid var(--semi-color-border)' }}>
            {STATUS_FILTERS.map((s) => (
              <button
                key={s}
                type='button'
                onClick={() => { setFilterStatus(s); setPage(1); }}
                style={{
                  padding: '0 10px',
                  height: 28,
                  fontSize: 12,
                  cursor: 'pointer',
                  border: 'none',
                  borderRight: s !== 'offline' ? '1px solid var(--semi-color-border)' : 'none',
                  background: filterStatus === s ? 'var(--semi-color-primary)' : 'var(--semi-color-bg-2)',
                  color: filterStatus === s ? '#fff' : 'var(--semi-color-text-0)',
                  transition: 'background 0.15s',
                }}
              >
                {statusLabel[s]}
              </button>
            ))}
          </div>
        </div>
      )}

      {/* 表格区域 — 仅此层滚动 */}
      <div style={{ flex: 1, minHeight: 0, overflowY: 'auto', overflowX: 'hidden', padding: '0 16px' }}>
        <Spin spinning={loading}>
          {loading ? (
            <div className='node-pool-empty node-pool-empty-compact'>
              <Loader2 size={18} className='node-pool-spin' />
            </div>
          ) : accounts.length === 0 ? (
            <div className='node-pool-empty node-pool-empty-compact'>
              {t('暂无账号')}
            </div>
          ) : filtered.length === 0 ? (
            <div className='node-pool-empty node-pool-empty-compact'>
              {t('无匹配账号')}
            </div>
          ) : (
            <div className='node-pool-account-table-wrap' style={{ overflowX: 'auto', overflowY: 'visible' }}>
              <table className='node-pool-account-table'>
                <thead style={{ position: 'sticky', top: 0, zIndex: 1, background: 'var(--semi-color-bg-2)' }}>
                  <tr>
                    <th>{t('ID')}</th>
                    <th>{t('名称')}</th>
                    <th>{t('状态')}</th>
                    <th className='node-pool-number-cell'>{t('请求数')}</th>
                    <th className='node-pool-number-cell'>{t('token数')}</th>
                    <th className='node-pool-number-cell'>{t('账号费用')}</th>
                    <th className='node-pool-number-cell'>{t('用户费用')}</th>
                    <th className='node-pool-number-cell'>{t('已用/总量')}</th>
                  </tr>
                </thead>
                <tbody>
                  <AccountRows accounts={pageAccounts} />
                </tbody>
              </table>
            </div>
          )}
        </Spin>
      </div>

      {/* 分页栏 — 固定底部 */}
      {hasData && (
        <div
          style={{
            flexShrink: 0,
            display: 'flex',
            justifyContent: 'space-between',
            alignItems: 'center',
            padding: '10px 16px',
            borderTop: '1px solid var(--semi-color-border)',
          }}
        >
          <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
            <span style={{ fontSize: 12, color: 'var(--semi-color-text-2)' }}>
              {(page - 1) * pageSize + 1}–{Math.min(page * pageSize, filtered.length)} / {filtered.length}
            </span>
            <select
              value={pageSize}
              onChange={(e) => { setPageSize(Number(e.target.value)); setPage(1); }}
              style={{ height: 24, borderRadius: 4, border: '1px solid var(--semi-color-border)', background: 'var(--semi-color-bg-2)', color: 'var(--semi-color-text-0)', fontSize: 12, padding: '0 4px', cursor: 'pointer' }}
            >
              {PAGE_SIZE_OPTIONS.map((n) => (
                <option key={n} value={n}>{n} / {t('页')}</option>
              ))}
            </select>
          </div>
          <Pagination
            currentPage={page}
            total={filtered.length}
            pageSize={pageSize}
            onChange={(p) => setPage(p)}
            size='small'
            showSizeChanger={false}
          />
        </div>
      )}
    </section>
  );
}

export default function NodePool() {
  const { t } = useTranslation();
  const [nodes, setNodes] = useState([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(null);
  const [selectedNode, setSelectedNode] = useState(null);
  const [accounts, setAccounts] = useState([]);
  const [accountsLoading, setAccountsLoading] = useState(false);
  const [accountStatsCache, setAccountStatsCache] = useState({});
  const [deleting, setDeleting] = useState(false);
  const selectedNodeRef = useRef(null);
  const fetchAccountsSeqRef = useRef(0);

  // 更新展示账号 + 缓存（带竞态保护）
  const fetchAccounts = useCallback(async (node) => {
    const seq = ++fetchAccountsSeqRef.current;
    setAccountsLoading(true);
    setAccounts([]);
    try {
      const name = encodeURIComponent(node.node_name);
      const res = await API.get(`/api/node-pool/nodes/${name}/accounts`);
      if (seq !== fetchAccountsSeqRef.current) return;
      const list = res.data?.accounts ?? [];
      setAccounts(list);
      const available = list.filter((a) => a.status === '正常').length;
      setAccountStatsCache((prev) => ({ ...prev, [node.node_name]: { available, total: list.length } }));
    } catch {
      if (seq === fetchAccountsSeqRef.current) setAccounts([]);
    } finally {
      if (seq === fetchAccountsSeqRef.current) setAccountsLoading(false);
    }
  }, []);

  // 仅更新缓存，不修改展示账号状态
  const fetchAccountsForCache = useCallback(async (node) => {
    try {
      const name = encodeURIComponent(node.node_name);
      const res = await API.get(`/api/node-pool/nodes/${name}/accounts`);
      const list = res.data?.accounts ?? [];
      const available = list.filter((a) => a.status === '正常').length;
      setAccountStatsCache((prev) => ({ ...prev, [node.node_name]: { available, total: list.length } }));
    } catch {
      // 缓存拉取失败静默忽略
    }
  }, []);

  const fetchNodes = useCallback(async (skipBulkCache = false) => {
    setLoading(true);
    setError(null);
    try {
      const res = await API.get('/api/node-pool/nodes');
      const list = res.data?.nodes ?? [];
      setNodes(list);
      if (list.length === 0) return;

      // 保持或自动选中第一个节点
      const cur = selectedNodeRef.current;
      let nodeToSelect = cur ? list.find((n) => isSameNode(n, cur)) : undefined;
      if (!nodeToSelect) nodeToSelect = list[0];
      selectedNodeRef.current = nodeToSelect;
      setSelectedNode(nodeToSelect);

      // 选中节点：拉取展示账号（同时更新缓存）
      fetchAccounts(nodeToSelect);
      // 其余节点：并发拉取缓存（删除节点后跳过，避免批量请求所有节点）
      if (!skipBulkCache) {
        list
          .filter((n) => n.node_name !== nodeToSelect.node_name)
          .forEach((n) => fetchAccountsForCache(n));
      }
    } catch (e) {
      setError(e?.message ?? t('获取节点列表失败'));
    } finally {
      setLoading(false);
    }
  }, [t, fetchAccounts, fetchAccountsForCache]);

  useEffect(() => {
    fetchNodes();
  }, [fetchNodes]);

  const handleSelectNode = useCallback(
    (node) => {
      selectedNodeRef.current = node;
      setSelectedNode(node);
      fetchAccounts(node);
    },
    [fetchAccounts],
  );

  const handleDeleteNode = useCallback(async () => {
    if (!selectedNode) return;
    setDeleting(true);
    try {
      const name = encodeURIComponent(selectedNode.node_name);
      await API.delete(`/api/node-pool/nodes/${name}`);
      selectedNodeRef.current = null;
      setSelectedNode(null);
      setAccounts([]);
      setAccountStatsCache((prev) => { const n = { ...prev }; delete n[selectedNode.node_name]; return n; });
      await fetchNodes(true);
    } catch (e) {
      setError(e?.message ?? t('删除节点失败'));
    } finally {
      setDeleting(false);
    }
  }, [selectedNode, fetchNodes, t]);

  const stats = {
    total: nodes.length,
    online: nodes.filter((node) => node.status === 'online').length,
    offline: nodes.filter((node) => node.status === 'offline').length,
    todayConsumption: nodes.reduce(
      (sum, node) => sum + (node.today_consumption ?? 0),
      0,
    ),
    totalConsumption: nodes.reduce(
      (sum, node) => sum + (node.total_consumption ?? 0),
      0,
    ),
    totalRpm: nodes.reduce((sum, node) => sum + (node.total_rpm ?? 0), 0),
  };

  return (
    <div className='node-pool-page'>
      <div className='node-pool-shell'>
        <header className='node-pool-header'>
          <div className='node-pool-title-group'>
            <span className='node-pool-title-icon'>
              <Server size={20} />
            </span>
            <h1 className='node-pool-title'>{t('号池节点')}</h1>
          </div>
          <Button
            size='small'
            theme='light'
            type='tertiary'
            icon={
              loading ? (
                <Loader2 size={14} className='node-pool-spin' />
              ) : (
                <RefreshCw size={14} />
              )
            }
            disabled={loading}
            onClick={fetchNodes}
          >
            {t('刷新')}
          </Button>
        </header>

        <div className='node-pool-stat-grid'>
          <StatCard
            label={t('总节点数')}
            value={stats.total}
            icon={Server}
          />
          <StatCard
            label={t('在线')}
            value={stats.online}
            icon={CheckCircle2}
            variant='online'
          />
          <StatCard
            label={t('离线')}
            value={stats.offline}
            icon={XCircle}
            variant='offline'
          />
          <StatCard
            label={t('今日消费')}
            value={`$${formatNumber(stats.todayConsumption)}`}
            icon={Activity}
          />
          <StatCard
            label={t('累计消费')}
            value={`$${formatNumber(stats.totalConsumption)}`}
            icon={Wallet}
          />
          <StatCard
            label={t('总RPM')}
            value={formatNumber(stats.totalRpm, 1)}
            icon={Database}
          />
        </div>

        {error && (
          <div className='node-pool-error'>
            <AlertCircle size={16} />
            <span>{error}</span>
          </div>
        )}

        {loading && nodes.length === 0 ? (
          <div className='node-pool-loading'>
            <Loader2 size={20} className='node-pool-spin' />
            {t('加载节点中...')}
          </div>
        ) : (
          <div className='node-pool-content'>
            <aside className='node-pool-list'>
              {nodes.length === 0 ? (
                <div className='node-pool-empty node-pool-empty-list'>
                  <CircleDashed size={17} />
                  {t('暂无节点')}
                </div>
              ) : (
                nodes.map((node) => (
                  <NodeListItem
                    key={getNodeKey(node)}
                    node={node}
                    selected={isSameNode(selectedNode, node)}
                    onClick={() => handleSelectNode(node)}
                    accountStats={accountStatsCache[node.node_name]}
                  />
                ))
              )}
            </aside>

            <main className='node-pool-main'>
              {selectedNode ? (
                <>
                  <NodeDetailPanel
                    node={selectedNode}
                    onDelete={handleDeleteNode}
                    deleting={deleting}
                  />
                  <AccountsPanel
                    accounts={accounts}
                    loading={accountsLoading}
                  />
                </>
              ) : (
                <div className='node-pool-empty node-pool-empty-detail'>
                  <Server size={40} />
                  <p>{t('点击左侧节点查看详细信息')}</p>
                </div>
              )}
            </main>
          </div>
        )}
      </div>
    </div>
  );
}
// end

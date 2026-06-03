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
import { Button, Spin, Table, Tag, Typography } from '@douyinfe/semi-ui';
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
  Wallet,
  XCircle,
} from 'lucide-react';
import { API } from '../../helpers';

const { Text } = Typography;

function fmt(n, dec = 2) {
  if (n == null) return '–';
  if (n === 0) return '0';
  return Number(n).toFixed(dec);
}

// ─── 统计卡片 ────────────────────────────────────────────────────────────────

function StatCard({ label, value, icon: Icon, variant }) {
  const borderColor =
    variant === 'online'
      ? 'var(--semi-color-success-light-active)'
      : variant === 'offline'
        ? 'var(--semi-color-danger-light-active)'
        : 'var(--semi-color-border)';
  const bgColor =
    variant === 'online'
      ? 'var(--semi-color-success-light-default)'
      : variant === 'offline'
        ? 'var(--semi-color-danger-light-default)'
        : 'var(--semi-color-bg-2)';
  const iconColor =
    variant === 'online'
      ? 'var(--semi-color-success)'
      : variant === 'offline'
        ? 'var(--semi-color-danger)'
        : 'var(--semi-color-text-2)';
  const valueColor =
    variant === 'online'
      ? 'var(--semi-color-success)'
      : variant === 'offline'
        ? 'var(--semi-color-danger)'
        : 'var(--semi-color-text-0)';

  return (
    <div
      style={{
        flex: '1 1 0',
        minWidth: 0,
        display: 'flex',
        flexDirection: 'column',
        gap: 4,
        borderRadius: 8,
        border: `1px solid ${borderColor}`,
        backgroundColor: bgColor,
        padding: '12px',
        boxShadow: '0 1px 2px rgba(0,0,0,0.04)',
      }}
    >
      <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
        <Icon size={14} color={iconColor} />
        <Text size='small' style={{ color: 'var(--semi-color-text-2)' }}>
          {label}
        </Text>
      </div>
      <p
        style={{
          fontSize: 20,
          fontWeight: 700,
          fontVariantNumeric: 'tabular-nums',
          color: valueColor,
          margin: 0,
        }}
      >
        {value}
      </p>
    </div>
  );
}

// ─── 节点状态标签 ─────────────────────────────────────────────────────────────

function NodeStatusTag({ status }) {
  const { t } = useTranslation();
  if (status === 'online') {
    return (
      <Tag
        color='green'
        size='small'
        style={{ display: 'inline-flex', alignItems: 'center', gap: 4 }}
      >
        <CheckCircle2 size={11} />
        {t('在线')}
      </Tag>
    );
  }
  return (
    <Tag
      color='red'
      size='small'
      style={{ display: 'inline-flex', alignItems: 'center', gap: 4 }}
    >
      <XCircle size={11} />
      {t('离线')}
    </Tag>
  );
}

// ─── 账号状态标签 ─────────────────────────────────────────────────────────────

function AccountStatusTag({ status }) {
  return (
    <Tag color={status === 'online' ? 'green' : 'grey'} size='small'>
      {status}
    </Tag>
  );
}

// ─── 节点列表卡片项 ───────────────────────────────────────────────────────────

function NodeListItem({ node, selected, onClick }) {
  const [hover, setHover] = useState(false);

  const bg = selected
    ? 'var(--semi-color-primary-light-hover)'
    : hover
      ? 'var(--semi-color-fill-0)'
      : 'var(--semi-color-bg-2)';
  const borderColor = selected
    ? 'var(--semi-color-primary)'
    : 'var(--semi-color-border)';

  return (
    <button
      type='button'
      onClick={onClick}
      onMouseEnter={() => setHover(true)}
      onMouseLeave={() => setHover(false)}
      style={{
        width: '100%',
        textAlign: 'left',
        background: bg,
        border: `1px solid ${borderColor}`,
        borderRadius: 8,
        padding: 12,
        cursor: 'pointer',
        transition: 'background 0.15s, border-color 0.15s',
        display: 'block',
      }}
    >
      <div
        style={{
          display: 'flex',
          justifyContent: 'space-between',
          alignItems: 'flex-start',
          gap: 8,
          marginBottom: 8,
        }}
      >
        <div style={{ minWidth: 0, flex: 1 }}>
          <p
            style={{
              margin: 0,
              fontSize: 14,
              fontWeight: 500,
              overflow: 'hidden',
              textOverflow: 'ellipsis',
              whiteSpace: 'nowrap',
              color: 'var(--semi-color-text-0)',
            }}
          >
            {node.node_name}
          </p>
          <p
            style={{
              margin: 0,
              fontSize: 12,
              color: 'var(--semi-color-text-2)',
              overflow: 'hidden',
              textOverflow: 'ellipsis',
              whiteSpace: 'nowrap',
            }}
          >
            {node.public_ip}:{node.listen_port}
          </p>
        </div>
        <NodeStatusTag status={node.status} />
      </div>
      <div
        style={{
          display: 'grid',
          gridTemplateColumns: '1fr 1fr 1fr',
          gap: '2px 8px',
          fontSize: 12,
          color: 'var(--semi-color-text-2)',
        }}
      >
        <span style={{ display: 'flex', alignItems: 'center', gap: 4 }}>
          <Cpu size={12} style={{ flexShrink: 0 }} />
          {fmt(node.cpu_usage, 1)}%
        </span>
        <span style={{ display: 'flex', alignItems: 'center', gap: 4 }}>
          <MemoryStick size={12} style={{ flexShrink: 0 }} />
          {fmt(node.mem_usage, 1)}%
        </span>
        <span style={{ display: 'flex', alignItems: 'center', gap: 4 }}>
          <Network size={12} style={{ flexShrink: 0 }} />
          {fmt(node.upload_bandwidth, 0)}↑
        </span>
      </div>
    </button>
  );
}

// ─── 详情字段行 ───────────────────────────────────────────────────────────────

function DetailField({ label, value }) {
  return (
    <div
      style={{
        display: 'flex',
        justifyContent: 'space-between',
        alignItems: 'center',
        gap: 16,
        borderBottom: '1px solid var(--semi-color-border)',
        padding: '6px 0',
        fontSize: 14,
      }}
    >
      <span style={{ color: 'var(--semi-color-text-2)', flexShrink: 0 }}>
        {label}
      </span>
      <span
        style={{
          fontWeight: 500,
          wordBreak: 'break-all',
          color: 'var(--semi-color-text-0)',
        }}
      >
        {value}
      </span>
    </div>
  );
}

// ─── 节点详情面板 ─────────────────────────────────────────────────────────────

function NodeDetailPanel({ node, onDelete, deleting }) {
  const { t } = useTranslation();
  const [confirming, setConfirming] = useState(false);

  const handleDeleteClick = () => {
    if (!confirming) {
      setConfirming(true);
      return;
    }
    setConfirming(false);
    onDelete();
  };

  return (
    <div
      style={{
        borderRadius: 8,
        border: '1px solid var(--semi-color-border)',
        background: 'var(--semi-color-bg-2)',
        padding: 16,
      }}
    >
      <div
        style={{
          display: 'flex',
          justifyContent: 'space-between',
          alignItems: 'center',
          marginBottom: 12,
        }}
      >
        <span
          style={{
            fontSize: 14,
            fontWeight: 600,
            color: 'var(--semi-color-text-0)',
          }}
        >
          {t('节点详情')}
        </span>
        {node.status === 'offline' && (
          <Button
            size='small'
            type={confirming ? 'danger' : 'tertiary'}
            theme={confirming ? 'solid' : 'light'}
            loading={deleting}
            icon={<Trash2 size={12} />}
            onClick={handleDeleteClick}
          >
            {confirming ? t('确认删除') : t('删除节点')}
          </Button>
        )}
      </div>
      <div>
        <DetailField label={t('节点名称')} value={node.node_name} />
        <DetailField label={t('公网IP')} value={node.public_ip} />
        <DetailField label={t('内网IP')} value={node.internal_ip} />
        <DetailField label={t('端口')} value={node.listen_port} />
        <DetailField
          label={t('状态')}
          value={<NodeStatusTag status={node.status} />}
        />
        <DetailField
          label={t('最后心跳')}
          value={
            node.last_seen
              ? new Date(node.last_seen).toLocaleString()
              : '–'
          }
        />
        <DetailField
          label={t('CPU使用率')}
          value={`${fmt(node.cpu_usage, 1)}%`}
        />
        <DetailField
          label={t('内存使用率')}
          value={`${fmt(node.mem_usage, 1)}%`}
        />
        <DetailField
          label={t('上行带宽')}
          value={`${fmt(node.upload_bandwidth, 1)} Mbps`}
        />
        <DetailField
          label={t('下行带宽')}
          value={`${fmt(node.download_bandwidth, 1)} Mbps`}
        />
        <DetailField label={t('今日请求')} value={node.today_requests ?? '–'} />
        <DetailField label={t('总请求')} value={node.total_requests ?? '–'} />
        <DetailField
          label={t('今日消费')}
          value={`$${fmt(node.today_consumption)}`}
        />
        <DetailField
          label={t('累计消费')}
          value={`$${fmt(node.total_consumption)}`}
        />
        <DetailField label={t('总RPM')} value={fmt(node.total_rpm, 1)} />
        <DetailField label={t('当前RPM')} value={fmt(node.current_rpm, 1)} />
        <DetailField
          label={t('平均响应时间')}
          value={`${fmt(node.avg_response_time, 0)} ms`}
        />
      </div>
    </div>
  );
}

// ─── 账号列表表格 ─────────────────────────────────────────────────────────────

function AccountsTable({ accounts, loading }) {
  const { t } = useTranslation();

  const columns = [
    {
      title: 'ID',
      dataIndex: 'id',
      width: 140,
      render: (v) => (
        <Text size='small' type='tertiary'>
          {v}
        </Text>
      ),
    },
    {
      title: t('名称'),
      dataIndex: 'name',
      width: 100,
      render: (v) => v || '–',
    },
    {
      title: t('状态'),
      dataIndex: 'status',
      width: 72,
      render: (v) => <AccountStatusTag status={v} />,
    },
    {
      title: t('请求数'),
      dataIndex: 'req',
      width: 80,
      align: 'right',
      render: (v) => v ?? 0,
    },
    {
      title: t('令牌数'),
      dataIndex: 'tokens',
      width: 90,
      align: 'right',
      render: (v) => v ?? 0,
    },
    {
      title: t('账号费用'),
      dataIndex: 'account_cost',
      width: 90,
      align: 'right',
      render: (v) => `$${fmt(v)}`,
    },
    {
      title: t('用户费用'),
      dataIndex: 'user_cost',
      width: 90,
      align: 'right',
      render: (v) => `$${fmt(v)}`,
    },
    {
      title: t('容量'),
      dataIndex: 'capacity',
      width: 72,
      align: 'right',
      render: (v) => v ?? '–',
    },
  ];

  return (
    <div
      style={{
        borderRadius: 8,
        border: '1px solid var(--semi-color-border)',
        background: 'var(--semi-color-bg-2)',
        padding: 16,
      }}
    >
      <div
        style={{
          marginBottom: 12,
          fontSize: 14,
          fontWeight: 600,
          color: 'var(--semi-color-text-0)',
        }}
      >
        {t('账号列表')}
        {!loading && (
          <span
            style={{
              marginLeft: 8,
              fontSize: 12,
              fontWeight: 400,
              color: 'var(--semi-color-text-2)',
            }}
          >
            ({accounts.length})
          </span>
        )}
      </div>
      <Spin spinning={loading}>
        {!loading && accounts.length === 0 ? (
          <div
            style={{
              textAlign: 'center',
              padding: '24px 0',
              color: 'var(--semi-color-text-2)',
              fontSize: 14,
            }}
          >
            {t('暂无账号')}
          </div>
        ) : (
          <Table
            size='small'
            dataSource={accounts}
            columns={columns}
            rowKey='id'
            pagination={false}
            scroll={{ x: 760 }}
          />
        )}
      </Spin>
    </div>
  );
}

// ─── 主页面 ───────────────────────────────────────────────────────────────────

export default function NodePool() {
  const { t } = useTranslation();
  const [nodes, setNodes] = useState([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(null);
  const [selected, setSelected] = useState(null);
  const [accounts, setAccounts] = useState([]);
  const [accountsLoading, setAccountsLoading] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const fetchAccountsSeqRef = useRef(0);

  const fetchNodes = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const res = await API.get('/api/node-pool/nodes');
      const list = res.data?.nodes ?? [];
      setNodes(list);
      if (selected) {
        const updated = list.find(
          (n) =>
            n.public_ip === selected.public_ip &&
            n.node_name === selected.node_name,
        );
        if (updated) setSelected(updated);
      }
    } catch (e) {
      setError(e?.message ?? t('获取节点列表失败'));
    } finally {
      setLoading(false);
    }
  }, [selected, t]);

  useEffect(() => {
    fetchNodes();
  }, []);

  const fetchAccounts = useCallback(async (node) => {
    const seq = ++fetchAccountsSeqRef.current;
    setAccountsLoading(true);
    setAccounts([]);
    try {
      const ip = encodeURIComponent(node.public_ip);
      const name = encodeURIComponent(node.node_name);
      const res = await API.get(`/api/node-pool/nodes/${ip}/${name}/accounts`);
      if (seq !== fetchAccountsSeqRef.current) return;
      setAccounts(res.data?.accounts ?? []);
    } catch {
      if (seq === fetchAccountsSeqRef.current) setAccounts([]);
    } finally {
      if (seq === fetchAccountsSeqRef.current) setAccountsLoading(false);
    }
  }, []);

  const handleSelect = useCallback(
    (node) => {
      setSelected(node);
      fetchAccounts(node);
    },
    [fetchAccounts],
  );

  const handleDelete = useCallback(async () => {
    if (!selected) return;
    setDeleting(true);
    try {
      const ip = encodeURIComponent(selected.public_ip);
      const name = encodeURIComponent(selected.node_name);
      await API.delete(`/api/node-pool/nodes/${ip}/${name}`);
      setSelected(null);
      setAccounts([]);
      await fetchNodes();
    } catch (e) {
      setError(e?.message ?? t('删除节点失败'));
    } finally {
      setDeleting(false);
    }
  }, [selected, fetchNodes, t]);

  const stats = {
    total: nodes.length,
    online: nodes.filter((n) => n.status === 'online').length,
    offline: nodes.filter((n) => n.status === 'offline').length,
    todayConsumption: nodes.reduce((s, n) => s + (n.today_consumption ?? 0), 0),
    totalConsumption: nodes.reduce((s, n) => s + (n.total_consumption ?? 0), 0),
    totalRpm: nodes.reduce((s, n) => s + (n.total_rpm ?? 0), 0),
  };

  return (
    <div style={{ marginTop: 60, padding: '0 16px 24px' }}>
      {/* 标题行 */}
      <div
        style={{
          display: 'flex',
          justifyContent: 'space-between',
          alignItems: 'center',
          marginBottom: 16,
        }}
      >
        <span
          style={{
            fontSize: 20,
            fontWeight: 700,
            color: 'var(--semi-color-text-0)',
          }}
        >
          {t('号池节点')}
        </span>
        <Button
          size='small'
          theme='light'
          type='tertiary'
          icon={
            loading ? (
              <Loader2 size={14} className='animate-spin' />
            ) : (
              <RefreshCw size={14} />
            )
          }
          disabled={loading}
          onClick={fetchNodes}
        >
          {t('刷新')}
        </Button>
      </div>

      {/* 统计卡片行 */}
      <div
        style={{ display: 'flex', flexWrap: 'wrap', gap: 12, marginBottom: 16 }}
      >
        <StatCard label={t('总节点数')} value={stats.total} icon={Server} />
        <StatCard
          label={t('在线节点')}
          value={stats.online}
          icon={CheckCircle2}
          variant='online'
        />
        <StatCard
          label={t('离线节点')}
          value={stats.offline}
          icon={XCircle}
          variant='offline'
        />
        <StatCard
          label={t('今日消费')}
          value={`$${fmt(stats.todayConsumption)}`}
          icon={Activity}
        />
        <StatCard
          label={t('累计消费')}
          value={`$${fmt(stats.totalConsumption)}`}
          icon={Wallet}
        />
        <StatCard
          label={t('总RPM')}
          value={fmt(stats.totalRpm, 1)}
          icon={Database}
        />
      </div>

      {/* 错误提示 */}
      {error && (
        <div
          style={{
            display: 'flex',
            alignItems: 'center',
            gap: 8,
            borderRadius: 8,
            border: '1px solid var(--semi-color-danger-light-active)',
            background: 'var(--semi-color-danger-light-default)',
            padding: '12px 16px',
            marginBottom: 16,
            fontSize: 14,
            color: 'var(--semi-color-danger)',
          }}
        >
          <AlertCircle size={16} style={{ flexShrink: 0 }} />
          {error}
        </div>
      )}

      {/* 主体区域 */}
      {loading && nodes.length === 0 ? (
        <div
          style={{
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            height: 160,
            color: 'var(--semi-color-text-2)',
          }}
        >
          <Loader2 size={20} className='animate-spin' style={{ marginRight: 8 }} />
          {t('加载节点中...')}
        </div>
      ) : (
        <div
          style={{
            display: 'flex',
            gap: 16,
            height: 'calc(100vh - 340px)',
            minHeight: 400,
          }}
        >
          {/* 左侧：节点卡片列表 */}
          <div
            style={{
              width: 288,
              flexShrink: 0,
              display: 'flex',
              flexDirection: 'column',
              gap: 8,
              overflowY: 'auto',
            }}
          >
            {nodes.length === 0 ? (
              <div
                style={{
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'center',
                  height: 96,
                  fontSize: 14,
                  color: 'var(--semi-color-text-2)',
                }}
              >
                <CircleDashed size={16} style={{ marginRight: 8 }} />
                {t('暂无节点')}
              </div>
            ) : (
              nodes.map((node) => (
                <NodeListItem
                  key={`${node.public_ip}:${node.node_name}`}
                  node={node}
                  selected={
                    selected?.public_ip === node.public_ip &&
                    selected?.node_name === node.node_name
                  }
                  onClick={() => handleSelect(node)}
                />
              ))
            )}
          </div>

          {/* 右侧：详情 + 账号列表 */}
          <div
            style={{
              flex: 1,
              minWidth: 0,
              display: 'flex',
              flexDirection: 'column',
              gap: 16,
              overflowY: 'auto',
            }}
          >
            {selected ? (
              <>
                <NodeDetailPanel
                  node={selected}
                  onDelete={handleDelete}
                  deleting={deleting}
                />
                <AccountsTable
                  accounts={accounts}
                  loading={accountsLoading}
                />
              </>
            ) : (
              <div
                style={{
                  flex: 1,
                  display: 'flex',
                  flexDirection: 'column',
                  alignItems: 'center',
                  justifyContent: 'center',
                  borderRadius: 8,
                  border: '1px dashed var(--semi-color-border)',
                  color: 'var(--semi-color-text-2)',
                }}
              >
                <Server size={40} style={{ opacity: 0.3, marginBottom: 12 }} />
                <p style={{ fontSize: 14, margin: 0 }}>
                  {t('点击左侧节点查看详细信息')}
                </p>
              </div>
            )}
          </div>
        </div>
      )}
    </div>
  );
}
// end

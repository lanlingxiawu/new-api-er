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

import React, {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from 'react';
import { useTranslation } from 'react-i18next';
import {
  Button,
  Card,
  Col,
  DatePicker,
  Empty,
  Input,
  InputNumber,
  Modal,
  Row,
  Select,
  Space,
  Spin,
  TabPane,
  Tabs,
  Tag,
  Typography,
} from '@douyinfe/semi-ui';
import {
  BadgeDollarSign,
  BriefcaseBusiness,
  Clock,
  DollarSign,
  Pencil,
  Plus,
  RefreshCw,
  Trash2,
  TrendingUp,
  UserRoundCheck,
  Users,
  Wallet,
} from 'lucide-react';
import { API, renderQuota, showError, showSuccess } from '../../helpers';
import CardPro from '../../components/common/ui/CardPro';
import CardTable from '../../components/common/ui/CardTable';
import { createCardProPagination } from '../../helpers/utils';
import { useIsMobile } from '../../hooks/common/useIsMobile';

const { Text } = Typography;
const PAGE_SIZE = 20;

const formatTs = (ts) => {
  if (!ts) return '-';
  return new Date(Number(ts) * 1000).toLocaleString();
};

const toNumber = (value, fallback = 0) => {
  const number = Number(value);
  return Number.isFinite(number) ? number : fallback;
};

const formatPercent = (value) => `${(Number(value || 0) * 100).toFixed(1)}%`;

const buildParams = (params = {}) => {
  const result = {};
  Object.entries(params).forEach(([key, value]) => {
    if (value !== undefined && value !== null && value !== '') {
      result[key] = value;
    }
  });
  return result;
};

function PageShell({ children }) {
  return <div className='mt-[60px] px-2 pb-6'>{children}</div>;
}

function ClassicDescription({
  title,
  description,
  icon: Icon,
  color = 'var(--semi-color-primary)',
}) {
  return (
    <div className='flex flex-col md:flex-row justify-between items-start md:items-center gap-2 w-full'>
      <div className='flex items-center'>
        {Icon ? <Icon size={16} className='mr-2' color={color} /> : null}
        <div>
          <Text strong>{title}</Text>
          {description ? (
            <div>
              <Text type='secondary' size='small'>
                {description}
              </Text>
            </div>
          ) : null}
        </div>
      </div>
    </div>
  );
}

function ClassicPagination({ paged, t }) {
  const isMobile = useIsMobile();
  if (!paged?.paginate) return null;

  return createCardProPagination({
    currentPage: paged.page,
    pageSize: paged.pageSize,
    total: paged.total,
    onPageChange: paged.setPage,
    onPageSizeChange: () => {},
    showSizeChanger: false,
    isMobile,
    t,
  });
}

function ClassicInlinePagination({ paged, t }) {
  const pagination = <ClassicPagination paged={paged} t={t} />;
  if (!paged?.paginate || !paged.total) return null;

  return (
    <div
      className='flex w-full pt-4 mt-4 border-t justify-between items-center'
      style={{ borderColor: 'var(--semi-color-border)' }}
    >
      {pagination}
    </div>
  );
}

function BusinessCard({
  title,
  description,
  icon,
  color,
  actions,
  pagination,
  children,
  t,
  type = 'type1',
}) {
  return (
    <CardPro
      type={type}
      className='mb-4'
      descriptionArea={
        <ClassicDescription
          title={title}
          description={description}
          icon={icon}
          color={color}
        />
      }
      actionsArea={
        actions ? (
          <div className='flex flex-col md:flex-row justify-between items-start md:items-center gap-2 w-full'>
            {actions}
          </div>
        ) : undefined
      }
      paginationArea={pagination}
      t={t}
    >
      {children}
    </CardPro>
  );
}

function ClassicBusinessTable({
  className = '',
  style,
  scroll = { x: '100%' },
  size = 'middle',
  ...props
}) {
  return (
    <div className='w-full'>
      <CardTable
        {...props}
        hidePagination
        scroll={scroll}
        style={{ width: '100%', ...style }}
        className={`rounded-xl overflow-hidden ${className}`.trim()}
        size={size}
      />
    </div>
  );
}

function StatCard({
  title,
  value,
  sub,
  icon: Icon,
  color = 'var(--semi-color-primary)',
}) {
  return (
    <Card
      className='!rounded-2xl border-0'
      bodyStyle={{ padding: 16 }}
      style={{ height: '100%' }}
    >
      <div className='flex items-center justify-between gap-3'>
        <Text type='secondary' size='small'>
          {title}
        </Text>
        {Icon ? <Icon size={18} color={color} /> : null}
      </div>
      <div className='mt-2 text-2xl font-semibold'>{value}</div>
      {sub ? (
        <div
          className='mt-1 text-xs'
          style={{ color: 'var(--semi-color-text-2)' }}
        >
          {sub}
        </div>
      ) : null}
    </Card>
  );
}

function StatusTag({ status }) {
  const { t } = useTranslation();
  return Number(status) === 1 ? (
    <Tag color='green'>{t('启用')}</Tag>
  ) : (
    <Tag color='grey'>{t('禁用')}</Tag>
  );
}

function AmountText({ value, positive }) {
  const amount = Number(value || 0);
  const color =
    positive || amount > 0
      ? 'var(--semi-color-success)'
      : amount < 0
        ? 'var(--semi-color-danger)'
        : undefined;
  return <span style={{ color }}>{renderQuota(amount)}</span>;
}

function Field({ label, children }) {
  return (
    <div className='mb-3'>
      <div className='mb-1 text-sm font-medium'>{label}</div>
      {children}
    </div>
  );
}

function usePagedEndpoint(endpoint, params = {}, options = {}) {
  const { pageSize = PAGE_SIZE, paginate = true } = options;
  const [page, setPage] = useState(1);
  const [items, setItems] = useState([]);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(false);
  const paramsKey = JSON.stringify(params);

  useEffect(() => {
    setPage(1);
  }, [endpoint, paramsKey]);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const res = await API.get(endpoint, {
        params: buildParams({
          ...params,
          page,
          page_size: pageSize,
        }),
        disableDuplicate: true,
      });
      const { success, message, data } = res.data;
      if (success) {
        setItems(data?.items || []);
        setTotal(data?.total || 0);
      } else {
        showError(message);
      }
    } catch (error) {
      showError(error?.message || 'Request failed');
    } finally {
      setLoading(false);
    }
  }, [endpoint, page, paramsKey]);

  useEffect(() => {
    load();
  }, [load]);

  const pagination = paginate
    ? {
        currentPage: page,
        pageSize,
        total,
        showSizeChanger: false,
        onPageChange: setPage,
      }
    : false;

  return {
    page,
    pageSize,
    paginate,
    setPage,
    items,
    total,
    loading,
    load,
    pagination,
  };
}

async function mutateRequest(method, url, data) {
  const res =
    method === 'post'
      ? await API.post(url, data)
      : method === 'put'
        ? await API.put(url, data)
        : method === 'delete'
          ? await API.delete(url)
          : await API.get(url, { params: data });

  const { success, message } = res.data;
  if (!success) {
    throw new Error(message || 'Operation failed');
  }
  return res.data;
}

function UserPicker({ value, onSelect }) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const [keyword, setKeyword] = useState('');
  const [debouncedKeyword, setDebouncedKeyword] = useState('');
  const [selectedLabel, setSelectedLabel] = useState('');
  const [users, setUsers] = useState([]);
  const [loading, setLoading] = useState(false);
  const containerRef = useRef(null);

  useEffect(() => {
    const timer = setTimeout(() => {
      setDebouncedKeyword(keyword.trim());
    }, 300);
    return () => clearTimeout(timer);
  }, [keyword]);

  useEffect(() => {
    if (!open) return undefined;
    const close = (event) => {
      if (!containerRef.current?.contains(event.target)) {
        setOpen(false);
      }
    };
    document.addEventListener('mousedown', close);
    return () => document.removeEventListener('mousedown', close);
  }, [open]);

  useEffect(() => {
    if (!open) return undefined;
    let cancelled = false;
    const loadUsers = async () => {
      setLoading(true);
      try {
        const res = await API.get('/api/user/search', {
          params: buildParams({
            keyword: debouncedKeyword,
            page_size: 20,
          }),
          disableDuplicate: true,
        });
        if (!cancelled) {
          setUsers(res.data?.data?.items || []);
        }
      } catch (error) {
        if (!cancelled) {
          setUsers([]);
        }
      } finally {
        if (!cancelled) {
          setLoading(false);
        }
      }
    };
    loadUsers();
    return () => {
      cancelled = true;
    };
  }, [debouncedKeyword, open]);

  return (
    <div ref={containerRef} style={{ position: 'relative' }}>
      <Input
        value={open ? keyword : selectedLabel}
        placeholder={t('搜索用户名 / 显示名称 / 邮箱')}
        autoComplete='off'
        onChange={(nextValue) => {
          setKeyword(nextValue);
          if (!open) setOpen(true);
        }}
        onFocus={() => {
          setKeyword('');
          setOpen(true);
        }}
      />
      {open ? (
        <div
          style={{
            position: 'absolute',
            top: 'calc(100% + 4px)',
            left: 0,
            right: 0,
            zIndex: 1000,
            maxHeight: 240,
            overflowY: 'auto',
            border: '1px solid var(--semi-color-border)',
            borderRadius: 6,
            background: 'var(--semi-color-bg-2)',
            boxShadow: 'var(--semi-shadow-elevated)',
            padding: 4,
          }}
        >
          {loading ? (
            <div className='px-2 py-6 text-center text-sm text-semi-color-text-2'>
              {t('加载中...')}
            </div>
          ) : users.length === 0 ? (
            <div className='px-2 py-6 text-center text-sm text-semi-color-text-2'>
              {t('未找到用户')}
            </div>
          ) : (
            users.map((user) => (
              <div
                key={user.id}
                role='option'
                aria-selected={value === user.id}
                className='cursor-pointer rounded px-2 py-1.5 text-sm hover:bg-semi-color-fill-0'
                onMouseDown={(event) => {
                  event.preventDefault();
                  const label = `${user.username}${
                    user.display_name ? ` (${user.display_name})` : ''
                  } #${user.id}`;
                  setSelectedLabel(label);
                  onSelect(user.id);
                  setOpen(false);
                }}
              >
                <div className='font-medium'>
                  {user.username}
                  {user.display_name ? ` (${user.display_name})` : ''}
                </div>
                <div className='text-xs text-semi-color-text-2'>
                  #{user.id}
                  {user.email ? ` / ${user.email}` : ''}
                </div>
              </div>
            ))
          )}
        </div>
      ) : null}
    </div>
  );
}

function EmployeeModal({ visible, row, onCancel, onSuccess }) {
  const { t } = useTranslation();
  const isUpdate = Boolean(row);
  const [saving, setSaving] = useState(false);
  const [form, setForm] = useState({
    user_id: 0,
    commission_rate: 0.1,
    target_quota: 0,
    status: 1,
    remark: '',
  });

  useEffect(() => {
    if (!visible) return;
    setForm({
      user_id: row?.user_id || 0,
      commission_rate: row?.commission_rate ?? 0.1,
      target_quota: row?.target_quota || 0,
      status: row?.status || 1,
      remark: row?.remark || '',
    });
  }, [visible, row]);

  const updateField = (key, value) => {
    setForm((prev) => ({ ...prev, [key]: value }));
  };

  const submit = async () => {
    if (!isUpdate && !form.user_id) {
      showError(t('请输入用户 ID'));
      return;
    }

    setSaving(true);
    try {
      if (isUpdate) {
        await mutateRequest('put', `/api/admin/employee/${row.id}`, {
          commission_rate: toNumber(form.commission_rate),
          target_quota: toNumber(form.target_quota),
          status: toNumber(form.status, 1),
          remark: form.remark,
        });
      } else {
        await mutateRequest('post', '/api/admin/employee', {
          user_id: toNumber(form.user_id),
          commission_rate: toNumber(form.commission_rate),
          target_quota: toNumber(form.target_quota),
          remark: form.remark,
        });
      }
      showSuccess(isUpdate ? t('员工已更新') : t('员工已创建'));
      onSuccess();
    } catch (error) {
      showError(error.message);
    } finally {
      setSaving(false);
    }
  };

  return (
    <Modal
      visible={visible}
      title={isUpdate ? t('编辑员工') : t('创建员工')}
      onCancel={onCancel}
      footer={
        <Space>
          <Button onClick={onCancel}>{t('取消')}</Button>
          <Button type='primary' loading={saving} onClick={submit}>
            {t('保存')}
          </Button>
        </Space>
      }
    >
      {!isUpdate ? (
        <Field label={t('用户')}>
          <UserPicker
            value={form.user_id}
            onSelect={(userId) => updateField('user_id', userId)}
          />
          <Text type='secondary' size='small'>
            {t('搜索并选择要设为员工的用户')}
          </Text>
        </Field>
      ) : null}
      <Field label={t('佣金比例')}>
        <InputNumber
          min={0}
          max={1}
          step={0.01}
          value={form.commission_rate}
          onChange={(value) => updateField('commission_rate', toNumber(value))}
          style={{ width: '100%' }}
        />
        <Text type='secondary' size='small'>
          {t('0.1 表示 10%')}
        </Text>
      </Field>
      <Field label={t('业绩目标')}>
        <InputNumber
          min={0}
          value={form.target_quota}
          onChange={(value) => updateField('target_quota', toNumber(value))}
          style={{ width: '100%' }}
        />
      </Field>
      {isUpdate ? (
        <Field label={t('状态')}>
          <Select
            value={form.status}
            onChange={(value) => updateField('status', value)}
            style={{ width: '100%' }}
          >
            <Select.Option value={1}>{t('启用')}</Select.Option>
            <Select.Option value={2}>{t('禁用')}</Select.Option>
          </Select>
        </Field>
      ) : null}
      <Field label={t('备注')}>
        <Input
          value={form.remark}
          onChange={(value) => updateField('remark', value)}
        />
      </Field>
    </Modal>
  );
}

function EmployeesTab() {
  const { t } = useTranslation();
  const employees = usePagedEndpoint(
    '/api/admin/employee',
    {},
    { pageSize: 100, paginate: false },
  );
  const [modalRow, setModalRow] = useState(null);
  const [modalVisible, setModalVisible] = useState(false);

  const openCreate = () => {
    setModalRow(null);
    setModalVisible(true);
  };

  const openEdit = (row) => {
    setModalRow(row);
    setModalVisible(true);
  };

  const closeModal = () => {
    setModalVisible(false);
    setModalRow(null);
  };

  const refreshAfterModal = () => {
    closeModal();
    employees.load();
  };

  const disableEmployee = (row) => {
    Modal.confirm({
      title: t('禁用员工'),
      content: t('禁用后历史佣金记录会保留。'),
      onOk: async () => {
        try {
          await mutateRequest('delete', `/api/admin/employee/${row.id}`);
          showSuccess(t('操作成功'));
          employees.load();
        } catch (error) {
          showError(error.message);
        }
      },
    });
  };

  const columns = [
    { title: 'ID', dataIndex: 'id', width: 80 },
    { title: t('用户 ID'), dataIndex: 'user_id', width: 100 },
    {
      title: t('用户名'),
      dataIndex: 'username',
      render: (value, row) => (
        <div>
          <div>{value || `#${row.user_id}`}</div>
          {row.display_name ? (
            <Text type='secondary' size='small'>
              {row.display_name}
            </Text>
          ) : null}
        </div>
      ),
    },
    {
      title: t('佣金比例'),
      dataIndex: 'commission_rate',
      render: (value) => formatPercent(value),
    },
    {
      title: t('业绩目标'),
      dataIndex: 'target_quota',
      render: (value) => (value ? renderQuota(value) : t('无限制')),
    },
    {
      title: t('状态'),
      dataIndex: 'status',
      render: (value) => <StatusTag status={value} />,
    },
    { title: t('备注'), dataIndex: 'remark', render: (value) => value || '-' },
    {
      title: t('操作'),
      width: 140,
      render: (_, row) => (
        <Space>
          <Button
            size='small'
            icon={<Pencil size={14} />}
            onClick={() => openEdit(row)}
          />
          <Button
            size='small'
            type='danger'
            theme='light'
            icon={<Trash2 size={14} />}
            onClick={() => disableEmployee(row)}
          />
        </Space>
      ),
    },
  ];

  return (
    <>
      <div className='flex justify-end mb-3'>
        <Button type='primary' icon={<Plus size={14} />} onClick={openCreate}>
          {t('添加员工')}
        </Button>
      </div>
      <ClassicBusinessTable
        rowKey='id'
        columns={columns}
        dataSource={employees.items}
        loading={employees.loading}
        empty={<Empty description={t('暂无数据')} />}
      />
      <EmployeeModal
        visible={modalVisible}
        row={modalRow}
        onCancel={closeModal}
        onSuccess={refreshAfterModal}
      />
    </>
  );
}

function CommissionLogsTable({
  endpoint,
  title,
  selfView = false,
  embedded = false,
}) {
  const { t } = useTranslation();
  const logs = usePagedEndpoint(endpoint);
  const columns = [
    { title: t('时间'), dataIndex: 'created_at', render: formatTs, width: 180 },
    ...(selfView
      ? [
          {
            title: t('客户'),
            dataIndex: 'customer_user_id_masked',
            render: (value, row) => value || `#${row.customer_user_id}`,
          },
        ]
      : [
          { title: t('员工 UID'), dataIndex: 'employee_user_id', width: 110 },
          { title: t('客户 UID'), dataIndex: 'customer_user_id', width: 110 },
        ]),
    {
      title: t('模型'),
      dataIndex: 'model_name',
      render: (value) => value || '-',
    },
    {
      title: t('收入'),
      dataIndex: 'revenue_quota',
      render: (value) => renderQuota(value),
    },
    {
      title: t('成本'),
      dataIndex: 'cost_quota',
      render: (value) => renderQuota(value),
    },
    {
      title: t('利润'),
      dataIndex: 'profit_quota',
      render: (value) => <AmountText value={value} />,
    },
    {
      title: t('佣金'),
      dataIndex: 'commission_quota',
      render: (value) => <AmountText value={value} positive />,
    },
    {
      title: t('比例'),
      dataIndex: 'commission_rate',
      render: (value) => formatPercent(value),
    },
  ];

  const content = (
    <>
      <ClassicBusinessTable
        rowKey='id'
        columns={columns}
        dataSource={logs.items}
        loading={logs.loading}
        empty={<Empty description={t('暂无数据')} />}
      />
      {embedded ? <ClassicInlinePagination paged={logs} t={t} /> : null}
    </>
  );

  if (embedded) {
    return content;
  }

  return (
    <BusinessCard
      title={title}
      icon={BadgeDollarSign}
      color='var(--semi-color-success)'
      pagination={<ClassicPagination paged={logs} t={t} />}
      t={t}
    >
      {content}
    </BusinessCard>
  );
}

export function Employees() {
  const { t } = useTranslation();
  return (
    <PageShell>
      <BusinessCard
        title={t('员工管理')}
        description={t('管理员工身份、佣金比例与佣金记录')}
        icon={Users}
        color='var(--semi-color-primary)'
        t={t}
      >
        <Tabs type='line' defaultActiveKey='employees'>
          <TabPane tab={t('员工')} itemKey='employees'>
            <EmployeesTab />
          </TabPane>
          <TabPane tab={t('佣金记录')} itemKey='logs'>
            <CommissionLogsTable
              endpoint='/api/admin/employee/commission'
              title={t('佣金记录')}
              embedded
            />
          </TabPane>
        </Tabs>
      </BusinessCard>
    </PageShell>
  );
}

export function EmployeeConsole() {
  const { t } = useTranslation();
  const [loading, setLoading] = useState(true);
  const [profileData, setProfileData] = useState(null);
  const [summary, setSummary] = useState(null);

  const loadProfile = useCallback(async () => {
    setLoading(true);
    try {
      const profileRes = await API.get('/api/user/employee/profile', {
        skipErrorHandler: true,
        disableDuplicate: true,
      });
      setProfileData(profileRes.data);
      if (profileRes.data.success && profileRes.data.data) {
        const summaryRes = await API.get(
          '/api/user/employee/commission/summary',
          {
            skipErrorHandler: true,
            disableDuplicate: true,
          },
        );
        setSummary(summaryRes.data?.data || profileRes.data.data.extension);
      }
    } catch (error) {
      setProfileData({ success: false });
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    loadProfile();
  }, [loadProfile]);

  const profile = profileData?.data?.profile;
  const extension = summary || profileData?.data?.extension || {};

  return (
    <PageShell>
      <Spin spinning={loading}>
        {!loading && (!profileData?.success || !profile) ? (
          <BusinessCard
            title={t('我的佣金')}
            description={t('查看员工佣金概览和佣金明细')}
            icon={BadgeDollarSign}
            color='var(--semi-color-success)'
            t={t}
          >
            <Empty
              description={t('你还不是员工，请联系管理员开通员工身份。')}
            />
          </BusinessCard>
        ) : (
          <>
            <BusinessCard
              title={t('我的佣金')}
              description={t('查看员工佣金概览和佣金明细')}
              icon={BadgeDollarSign}
              color='var(--semi-color-success)'
              t={t}
            >
              {profile ? (
                <div className='mb-4'>
                  <Space wrap>
                    <Tag color='blue'>
                      {t('佣金比例')}: {formatPercent(profile.commission_rate)}
                    </Tag>
                    <Tag color='grey'>
                      {t('业绩目标')}:{' '}
                      {profile.target_quota
                        ? renderQuota(profile.target_quota)
                        : t('无限制')}
                    </Tag>
                  </Space>
                </div>
              ) : null}
              <Row gutter={[16, 16]}>
                <Col xs={24} md={12} xl={6}>
                  <StatCard
                    title={t('累计佣金')}
                    value={renderQuota(extension.commission_total_quota || 0)}
                    sub={`≈ $${Number(extension.commission_total_usd || 0).toFixed(4)}`}
                    icon={BadgeDollarSign}
                    color='var(--semi-color-success)'
                  />
                </Col>
                <Col xs={24} md={12} xl={6}>
                  <StatCard
                    title={t('待结算')}
                    value={renderQuota(extension.commission_pending_quota || 0)}
                    sub={`≈ $${Number(extension.commission_pending_usd || 0).toFixed(4)}`}
                    icon={Clock}
                    color='var(--semi-color-warning)'
                  />
                </Col>
                <Col xs={24} md={12} xl={6}>
                  <StatCard
                    title={t('客户收入')}
                    value={renderQuota(extension.revenue_total_quota || 0)}
                    sub={`≈ $${Number(extension.revenue_total_usd || 0).toFixed(4)}`}
                    icon={DollarSign}
                    color='var(--semi-color-info)'
                  />
                </Col>
                <Col xs={24} md={12} xl={6}>
                  <StatCard
                    title={t('活跃客户')}
                    value={extension.revenue_customer_count || 0}
                    sub={t('有消费记录的客户数量')}
                    icon={Users}
                  />
                </Col>
              </Row>
              <div
                className='mt-4 pt-4 border-t'
                style={{ borderColor: 'var(--semi-color-border)' }}
              >
                <div className='mb-3'>
                  <Text strong>{t('佣金明细')}</Text>
                </div>
                <CommissionLogsTable
                  endpoint='/api/user/employee/commission'
                  selfView
                  embedded
                />
              </div>
            </BusinessCard>
          </>
        )}
      </Spin>
    </PageShell>
  );
}

function MyCustomerCreateModal({ visible, onCancel, onSuccess }) {
  const { t } = useTranslation();
  const [saving, setSaving] = useState(false);
  const [form, setForm] = useState({
    username: '',
    password: '',
    display_name: '',
    email: '',
    remark: '',
  });

  useEffect(() => {
    if (!visible) {
      setForm({
        username: '',
        password: '',
        display_name: '',
        email: '',
        remark: '',
      });
    }
  }, [visible]);

  const updateField = (key, value) => {
    setForm((prev) => ({ ...prev, [key]: value }));
  };

  const submit = async () => {
    if (!form.username || !form.password) {
      showError(t('请输入用户名和密码'));
      return;
    }
    setSaving(true);
    try {
      await mutateRequest('post', '/api/user/employee/customers', form);
      showSuccess(t('客户已创建'));
      onSuccess();
    } catch (error) {
      showError(error.message);
    } finally {
      setSaving(false);
    }
  };

  return (
    <Modal
      visible={visible}
      title={t('添加客户')}
      onCancel={onCancel}
      footer={
        <Space>
          <Button onClick={onCancel}>{t('取消')}</Button>
          <Button type='primary' loading={saving} onClick={submit}>
            {t('保存')}
          </Button>
        </Space>
      }
    >
      <Field label={t('用户名')}>
        <Input
          value={form.username}
          onChange={(value) => updateField('username', value)}
        />
      </Field>
      <Field label={t('密码')}>
        <Input
          mode='password'
          value={form.password}
          onChange={(value) => updateField('password', value)}
        />
      </Field>
      <Field label={t('显示名称')}>
        <Input
          value={form.display_name}
          onChange={(value) => updateField('display_name', value)}
        />
      </Field>
      <Field label={t('邮箱')}>
        <Input
          value={form.email}
          onChange={(value) => updateField('email', value)}
        />
      </Field>
      <Field label={t('备注')}>
        <Input
          value={form.remark}
          onChange={(value) => updateField('remark', value)}
        />
      </Field>
    </Modal>
  );
}

function MyCustomerEditModal({ visible, row, onCancel, onSuccess }) {
  const { t } = useTranslation();
  const [saving, setSaving] = useState(false);
  const [remark, setRemark] = useState('');

  useEffect(() => {
    if (visible) {
      setRemark(row?.remark || '');
    }
  }, [visible, row]);

  const submit = async () => {
    if (!row) return;
    setSaving(true);
    try {
      await mutateRequest('put', `/api/user/employee/customers/${row.id}`, {
        remark,
      });
      showSuccess(t('客户已更新'));
      onSuccess();
    } catch (error) {
      showError(error.message);
    } finally {
      setSaving(false);
    }
  };

  return (
    <Modal
      visible={visible}
      title={t('编辑客户')}
      onCancel={onCancel}
      footer={
        <Space>
          <Button onClick={onCancel}>{t('取消')}</Button>
          <Button type='primary' loading={saving} onClick={submit}>
            {t('保存')}
          </Button>
        </Space>
      }
    >
      <Field label={t('备注')}>
        <Input value={remark} onChange={setRemark} />
      </Field>
    </Modal>
  );
}

export function CustomerConsole() {
  const { t } = useTranslation();
  const customers = usePagedEndpoint('/api/user/employee/customers');
  const [createVisible, setCreateVisible] = useState(false);
  const [editRow, setEditRow] = useState(null);

  const closeAndRefresh = () => {
    setCreateVisible(false);
    setEditRow(null);
    customers.load();
  };

  const columns = [
    {
      title: t('客户'),
      dataIndex: 'username',
      render: (value, row) => (
        <div>
          <div>{value || `#${row.customer_user_id}`}</div>
          <Text type='secondary' size='small'>
            #{row.customer_user_id}
            {row.email ? ` / ${row.email}` : ''}
          </Text>
        </div>
      ),
    },
    {
      title: t('余额'),
      dataIndex: 'quota',
      render: (value) => renderQuota(value),
    },
    {
      title: t('已用额度'),
      dataIndex: 'used_quota',
      render: (value) => renderQuota(value),
    },
    {
      title: t('佣金'),
      dataIndex: 'commission_quota',
      render: (value) => (value ? <AmountText value={value} positive /> : '-'),
    },
    {
      title: t('状态'),
      dataIndex: 'status',
      render: (value) => <StatusTag status={value} />,
    },
    { title: t('备注'), dataIndex: 'remark', render: (value) => value || '-' },
    {
      title: t('操作'),
      width: 90,
      render: (_, row) => (
        <Button
          size='small'
          icon={<Pencil size={14} />}
          onClick={() => setEditRow(row)}
        />
      ),
    },
  ];

  return (
    <PageShell>
      <BusinessCard
        title={t('我的客户')}
        description={t('创建并维护归属于你的客户账号')}
        icon={UserRoundCheck}
        color='var(--semi-color-primary)'
        actions={
          <Button
            type='primary'
            icon={<Plus size={14} />}
            onClick={() => setCreateVisible(true)}
          >
            {t('添加客户')}
          </Button>
        }
        pagination={<ClassicPagination paged={customers} t={t} />}
        t={t}
      >
        <ClassicBusinessTable
          rowKey='id'
          columns={columns}
          dataSource={customers.items}
          loading={customers.loading}
          empty={<Empty description={t('暂无数据')} />}
        />
      </BusinessCard>
      <MyCustomerCreateModal
        visible={createVisible}
        onCancel={() => setCreateVisible(false)}
        onSuccess={closeAndRefresh}
      />
      <MyCustomerEditModal
        visible={Boolean(editRow)}
        row={editRow}
        onCancel={() => setEditRow(null)}
        onSuccess={closeAndRefresh}
      />
    </PageShell>
  );
}

function getPresetRange(range) {
  if (range === 'all') {
    return { start: null, end: null };
  }
  const days = range === '90d' ? 90 : range === '30d' ? 30 : 7;
  const end = new Date();
  end.setHours(23, 59, 59, 999);
  const start = new Date(end);
  start.setDate(end.getDate() - (days - 1));
  start.setHours(0, 0, 0, 0);
  return { start, end };
}

function rangeToParams(range) {
  if (!range?.start || !range?.end) return {};
  return {
    start_time: Math.floor(range.start.getTime() / 1000),
    end_time: Math.floor(range.end.getTime() / 1000),
  };
}

export function BusinessOverview() {
  const { t } = useTranslation();
  const [range, setRange] = useState('7d');
  const [customRange, setCustomRange] = useState(() => getPresetRange('7d'));
  const [loading, setLoading] = useState(false);
  const [data, setData] = useState(null);
  const selectedRange = useMemo(
    () => (range === 'custom' ? customRange : getPresetRange(range)),
    [customRange, range],
  );
  const params = useMemo(() => rangeToParams(selectedRange), [selectedRange]);
  const datePickerValue = useMemo(
    () =>
      selectedRange.start && selectedRange.end
        ? [selectedRange.start, selectedRange.end]
        : [],
    [selectedRange],
  );

  const loadOverview = useCallback(async () => {
    setLoading(true);
    try {
      const res = await API.get('/api/admin/employee/overview', {
        params,
        disableDuplicate: true,
      });
      const { success, message, data: overview } = res.data;
      if (success) {
        setData(overview);
      } else {
        showError(message);
      }
    } catch (error) {
      showError(error?.message || 'Request failed');
    } finally {
      setLoading(false);
    }
  }, [JSON.stringify(params)]);

  useEffect(() => {
    loadOverview();
  }, [loadOverview]);

  const platform = data?.platform || {};
  const commission = data?.commission || {};
  const rangeButtons = [
    { key: '7d', label: t('近 7 天') },
    { key: '30d', label: t('近 30 天') },
    { key: '90d', label: t('近 90 天') },
    { key: 'all', label: t('全部') },
  ];

  const employeeColumns = [
    {
      title: t('员工'),
      dataIndex: 'username',
      render: (value, row) =>
        value || row.display_name || `#${row.employee_user_id}`,
    },
    {
      title: t('收入'),
      dataIndex: 'total_revenue',
      render: (value) => renderQuota(value),
    },
    {
      title: t('成本'),
      dataIndex: 'total_cost',
      render: (value) => renderQuota(value),
    },
    {
      title: t('利润'),
      dataIndex: 'total_profit',
      render: (value) => <AmountText value={value} />,
    },
    {
      title: t('佣金'),
      dataIndex: 'total_commission',
      render: (value) => <AmountText value={value} positive />,
    },
    { title: t('记录数'), dataIndex: 'record_count' },
  ];

  const channelProfitColumns = [
    {
      title: t('渠道'),
      dataIndex: 'channel_name',
      render: (value, row) => value || `#${row.channel_id}`,
    },
    { title: t('成本比例'), dataIndex: 'cost_ratio' },
    {
      title: t('总消耗'),
      dataIndex: 'consumption_quota',
      render: (value) => renderQuota(value),
    },
    {
      title: t('估算成本'),
      dataIndex: 'est_cost_quota',
      render: (value) => renderQuota(value),
    },
    {
      title: t('估算利润'),
      dataIndex: 'est_profit_quota',
      render: (value) => <AmountText value={value} />,
    },
    {
      title: t('毛利率'),
      dataIndex: 'est_gross_margin',
      render: (value) => formatPercent(value),
    },
  ];

  return (
    <PageShell>
      <BusinessCard
        title={t('业务概览')}
        description={t('查看平台成本、利润、员工佣金和渠道表现')}
        icon={TrendingUp}
        color='var(--semi-color-success)'
        actions={
          <Space wrap>
            {rangeButtons.map((item) => (
              <Button
                key={item.key}
                type={range === item.key ? 'primary' : 'tertiary'}
                theme={range === item.key ? 'solid' : 'light'}
                onClick={() => setRange(item.key)}
              >
                {item.label}
              </Button>
            ))}
            <DatePicker
              type='dateTimeRange'
              value={datePickerValue}
              placeholder={[t('开始时间'), t('结束时间')]}
              style={{ minWidth: 300 }}
              onChange={(value) => {
                const [start, end] = Array.isArray(value) ? value : [];
                if (start && end) {
                  setCustomRange({
                    start: start instanceof Date ? start : new Date(start),
                    end: end instanceof Date ? end : new Date(end),
                  });
                  setRange('custom');
                }
              }}
            />
          </Space>
        }
        t={t}
      >
        <Spin spinning={loading}>
          <div className='flex flex-col gap-4'>
            <Text strong type='secondary'>
              {t('平台范围（所有用户）')}
            </Text>
            <Row gutter={[16, 16]}>
              <Col xs={24} md={12} xl={4}>
                <StatCard
                  title={t('总消耗')}
                  value={renderQuota(platform.total_consumption_quota || 0)}
                  sub={`≈ $${Number(platform.total_consumption_usd || 0).toFixed(4)}`}
                  icon={Wallet}
                  color='var(--semi-color-info)'
                />
              </Col>
              <Col xs={24} md={12} xl={4}>
                <StatCard
                  title={t('估算成本')}
                  value={renderQuota(platform.est_cost_quota || 0)}
                  sub={`≈ $${Number(platform.est_cost_usd || 0).toFixed(4)}`}
                  icon={BriefcaseBusiness}
                  color='var(--semi-color-warning)'
                />
              </Col>
              <Col xs={24} md={12} xl={4}>
                <StatCard
                  title={t('估算利润')}
                  value={renderQuota(platform.est_profit_quota || 0)}
                  sub={`≈ $${Number(platform.est_profit_usd || 0).toFixed(4)}`}
                  icon={TrendingUp}
                  color='var(--semi-color-success)'
                />
              </Col>
              <Col xs={24} md={12} xl={4}>
                <StatCard
                  title={t('平台毛利率')}
                  value={formatPercent(platform.est_gross_margin || 0)}
                  icon={DollarSign}
                />
              </Col>
              <Col xs={24} md={12} xl={4}>
                <StatCard
                  title={t('请求数')}
                  value={platform.request_count || 0}
                  icon={RefreshCw}
                />
              </Col>
              <Col xs={24} md={12} xl={4}>
                <StatCard
                  title={t('Token 数')}
                  value={platform.token_count || 0}
                  icon={Users}
                />
              </Col>
            </Row>
            <Text type='secondary' size='small'>
              {t('成本按交易精确记录。启用此功能前生成的数据没有成本记录。')}
            </Text>

            <Card
              title={t('渠道盈利（全平台）')}
              className='!rounded-2xl border-0'
            >
              <ClassicBusinessTable
                rowKey='channel_id'
                columns={channelProfitColumns}
                dataSource={data?.by_channel_platform || []}
                empty={<Empty description={t('暂无数据')} />}
              />
              <div className='mt-2'>
                <Text type='secondary' size='small'>
                  {t('成本和利润按分组倍率与渠道成本比例估算。')}
                </Text>
              </div>
            </Card>

            <Text strong type='secondary'>
              {t('员工归因流量')}
            </Text>
            <div className='grid grid-cols-1 md:grid-cols-2 xl:grid-cols-5 gap-4'>
              <div>
                <StatCard
                  title={t('员工归因收入')}
                  value={renderQuota(commission.total_revenue_quota || 0)}
                  sub={`≈ $${Number(commission.total_revenue_usd || 0).toFixed(4)}`}
                  icon={DollarSign}
                  color='var(--semi-color-info)'
                />
              </div>
              <div>
                <StatCard
                  title={t('员工归因成本')}
                  value={renderQuota(commission.total_cost_quota || 0)}
                  sub={`≈ $${Number(commission.total_cost_usd || 0).toFixed(4)}`}
                  icon={BriefcaseBusiness}
                  color='var(--semi-color-warning)'
                />
              </div>
              <div>
                <StatCard
                  title={t('员工归因利润')}
                  value={renderQuota(commission.total_profit_quota || 0)}
                  sub={`≈ $${Number(commission.total_profit_usd || 0).toFixed(4)}`}
                  icon={TrendingUp}
                  color='var(--semi-color-success)'
                />
              </div>
              <div>
                <StatCard
                  title={t('佣金总额')}
                  value={renderQuota(commission.total_commission_quota || 0)}
                  sub={`≈ $${Number(commission.total_commission_usd || 0).toFixed(4)}`}
                  icon={BadgeDollarSign}
                />
              </div>
              <div>
                <StatCard
                  title={t('归因毛利率')}
                  value={formatPercent(commission.gross_margin || 0)}
                  sub={`${commission.record_count || 0} ${t('条记录')}`}
                  icon={Wallet}
                />
              </div>
            </div>

            <Card title={t('按员工')} className='!rounded-2xl border-0'>
              <ClassicBusinessTable
                rowKey='employee_user_id'
                columns={employeeColumns}
                dataSource={data?.by_employee || []}
                empty={<Empty description={t('暂无数据')} />}
              />
            </Card>
          </div>
        </Spin>
      </BusinessCard>
    </PageShell>
  );
}

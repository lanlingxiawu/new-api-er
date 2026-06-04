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
  Progress,
  Row,
  Select,
  Space,
  Spin,
  Tag,
  Typography,
} from '@douyinfe/semi-ui';
import {
  IllustrationNoResult,
  IllustrationNoResultDark,
} from '@douyinfe/semi-illustrations';
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
import { getQuotaPerUnit } from '../../helpers/quota';
import CardPro from '../../components/common/ui/CardPro';
import CardTable from '../../components/common/ui/CardTable';
import { createCardProPagination } from '../../helpers/utils';
import { useIsMobile } from '../../hooks/common/useIsMobile';

const { Text } = Typography;
const PAGE_SIZE = 20;
const BUSINESS_AMOUNT_DIGITS = 4;

const formatTs = (ts) => {
  if (!ts) return '-';
  return new Date(Number(ts) * 1000).toLocaleString();
};

const toNumber = (value, fallback = 0) => {
  const number = Number(value);
  return Number.isFinite(number) ? number : fallback;
};

const isSameNumber = (left, right) =>
  Math.abs(Number(left || 0) - Number(right || 0)) < 0.000001;

const resolveSelectedTierId = (row, tiers) => {
  if (!row) return null;

  const currentTierId = Number(row.current_tier_id || 0);
  if (currentTierId > 0) return currentTierId;

  const matchedTier = tiers.find(
    (tier) =>
      isSameNumber(tier.rate, row.commission_rate) &&
      isSameNumber(tier.threshold_usd, row.target_amount),
  );
  return matchedTier?.id || null;
};

const formatPercent = (value) => `${(Number(value || 0) * 100).toFixed(1)}%`;
const formatBusinessAmount = (value) =>
  renderQuota(value || 0, BUSINESS_AMOUNT_DIGITS);
const formatFullNumber = (value) => {
  if (value === undefined || value === null || value === '') return '0';
  const raw = String(value);
  if (!/[eE]/.test(raw)) return raw;
  const number = Number(value);
  if (!Number.isFinite(number)) return raw;

  const [coefficient, exponentPart] = raw.toLowerCase().split('e');
  const exponent = Number(exponentPart);
  if (!Number.isFinite(exponent)) return raw;

  const sign = coefficient.startsWith('-') ? '-' : '';
  const unsigned = sign ? coefficient.slice(1) : coefficient;
  const [integer, fraction = ''] = unsigned.split('.');
  const digits = `${integer}${fraction}`;
  const decimalIndex = integer.length + exponent;

  if (decimalIndex <= 0) {
    return `${sign}0.${'0'.repeat(Math.abs(decimalIndex))}${digits}`;
  }
  if (decimalIndex >= digits.length) {
    return `${sign}${digits}${'0'.repeat(decimalIndex - digits.length)}`;
  }
  return `${sign}${digits.slice(0, decimalIndex)}.${digits.slice(decimalIndex)}`;
};
const formatBusinessUsd = (value) => `≈ $${formatFullNumber(value)}`;
const formatExactUsd = (value) => `$${formatFullNumber(value)}`;
const formatTargetAmount = (value) => {
  const amount = Number(value || 0);
  return Number.isFinite(amount) ? `$${amount.toFixed(2)}` : '$0.00';
};

const renderPerformanceProgress = (value, row, t) => {
  const currentQuota = Number(value || 0);
  const targetAmount = Number(row?.target_amount || 0);

  if (!Number.isFinite(targetAmount) || targetAmount <= 0) {
    return formatBusinessAmount(currentQuota);
  }

  const targetQuota = targetAmount * getQuotaPerUnit();
  const rawPercent = targetQuota > 0 ? (currentQuota / targetQuota) * 100 : 0;
  const percent = Math.max(0, Math.min(100, rawPercent));
  const percentText = `${Number.isFinite(rawPercent) ? rawPercent.toFixed(0) : '0'}%`;

  return (
    <div className='min-w-[180px]'>
      <div className='mb-1 flex items-center justify-between gap-2'>
        <Text size='small'>{formatBusinessAmount(currentQuota)}</Text>
        <Text type='secondary' size='small'>
          {percentText}
        </Text>
      </div>
      <Progress
        percent={percent}
        stroke={
          rawPercent >= 100
            ? 'var(--semi-color-success)'
            : 'var(--semi-color-primary)'
        }
        aria-label='employee performance progress'
        format={() => percentText}
        style={{ marginBottom: 0 }}
      />
      <Text type='secondary' size='small'>
        {t('业绩目标')}: {formatTargetAmount(targetAmount)}
      </Text>
    </div>
  );
};

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
  return <div className='business-page-shell mt-[60px] px-2'>{children}</div>;
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
  searchArea,
  pagination,
  children,
  t,
  type = 'type2',
}) {
  return (
    <CardPro
      type={type}
      statsArea={
        <ClassicDescription
          title={title}
          description={description}
          icon={icon}
          color={color}
        />
      }
      searchArea={
        searchArea ||
        (actions ? (
          <div className='flex flex-col md:flex-row justify-between items-start md:items-center gap-2 w-full'>
            <div />
            <div className='flex flex-wrap justify-end gap-2 w-full md:w-auto'>
              {actions}
            </div>
          </div>
        ) : undefined)
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
  wrapperClassName = '',
  wrapperStyle,
  style,
  scroll = { x: '100%' },
  size = 'middle',
  ...props
}) {
  return (
    <div className={`w-full ${wrapperClassName}`.trim()} style={wrapperStyle}>
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

function BusinessEmpty({ description }) {
  return (
    <Empty
      image={<IllustrationNoResult style={{ width: 150, height: 150 }} />}
      darkModeImage={
        <IllustrationNoResultDark style={{ width: 150, height: 150 }} />
      }
      description={description}
      style={{ padding: 30 }}
    />
  );
}

function BusinessSection({ title, description, children }) {
  return (
    <div
      className='pt-4 border-t'
      style={{ borderColor: 'var(--semi-color-border)' }}
    >
      <div className='mb-3 flex flex-col gap-1'>
        <Text strong>{title}</Text>
        {description ? (
          <Text type='secondary' size='small'>
            {description}
          </Text>
        ) : null}
      </div>
      {children}
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
      <div
        className='mt-1 text-xs'
        style={{
          minHeight: 16,
          color: 'var(--semi-color-text-2)',
          visibility: sub ? 'visible' : 'hidden',
        }}
      >
        {sub || '-'}
      </div>
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

function AmountText({ value }) {
  const amount = Number(value || 0);
  const color =
    amount > 0
      ? 'var(--semi-color-success)'
      : amount < 0
        ? 'var(--semi-color-danger)'
        : undefined;
  return <span style={{ color }}>{formatBusinessAmount(amount)}</span>;
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
  const { pageSize = PAGE_SIZE, paginate = true, enabled = true } = options;
  const [page, setPage] = useState(1);
  const [items, setItems] = useState([]);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(false);
  const paramsKey = JSON.stringify(params);

  useEffect(() => {
    setPage(1);
  }, [endpoint, paramsKey]);

  const load = useCallback(async () => {
    if (!enabled) return;
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
  }, [enabled, endpoint, page, paramsKey]);

  useEffect(() => {
    if (enabled) {
      load();
    }
  }, [enabled, load]);

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
  const [loadingMore, setLoadingMore] = useState(false);
  const [page, setPage] = useState(1);
  const [hasMore, setHasMore] = useState(false);
  const containerRef = useRef(null);

  useEffect(() => {
    if (!value) setSelectedLabel('');
  }, [value]);

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

  const loadUsers = useCallback(
    async (nextPage = 1, replace = false) => {
      if (replace) {
        setLoading(true);
      } else {
        setLoadingMore(true);
      }
      try {
        const res = await API.get('/api/user/search', {
          params: buildParams({
            keyword: debouncedKeyword,
            page_size: 20,
            p: nextPage,
            exclude_employee: true,
          }),
          disableDuplicate: true,
        });
        const data = res.data?.data || {};
        const nextItems = data.items || [];
        const currentPage = Number(data.page || nextPage);
        const pageSize = Number(data.page_size || 20);
        const total = Number(data.total || 0);
        setUsers((prev) => {
          if (replace) return nextItems;
          const existingIds = new Set(prev.map((user) => user.id));
          return [
            ...prev,
            ...nextItems.filter((user) => !existingIds.has(user.id)),
          ];
        });
        setPage(currentPage);
        setHasMore(currentPage * pageSize < total);
      } catch (error) {
        if (replace) {
          setUsers([]);
          setHasMore(false);
        }
      } finally {
        setLoading(false);
        setLoadingMore(false);
      }
    },
    [debouncedKeyword],
  );

  useEffect(() => {
    if (!open) return;
    setUsers([]);
    setPage(1);
    setHasMore(false);
    loadUsers(1, true);
  }, [debouncedKeyword, loadUsers, open]);

  const handleScroll = (event) => {
    const target = event.currentTarget;
    const distanceToBottom =
      target.scrollHeight - target.scrollTop - target.clientHeight;
    if (distanceToBottom > 48 || !hasMore || loadingMore || loading) return;
    loadUsers(page + 1, false);
  };

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
          onScroll={handleScroll}
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
            <>
              {users.map((user) => (
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
              ))}
              {loadingMore ? (
                <div className='px-2 py-3 text-center text-sm text-semi-color-text-2'>
                  {t('加载中...')}
                </div>
              ) : null}
            </>
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
  const [tiers, setTiers] = useState([]);
  const [selectedTierId, setSelectedTierId] = useState(null);
  const [form, setForm] = useState({
    user_id: 0,
    commission_rate: 0.1,
    target_amount: 0,
    status: 1,
    remark: '',
  });

  // 拉取等级列表（供下拉选择）
  useEffect(() => {
    if (!visible) return;
    API.get('/api/admin/employee/tiers', { disableDuplicate: true })
      .then((res) => {
        if (res.data.success) setTiers(res.data.data || []);
      })
      .catch(() => {});
  }, [visible]);

  useEffect(() => {
    if (!visible) return;
    setSelectedTierId(null);
    setForm({
      user_id: row?.user_id || 0,
      commission_rate: row?.commission_rate ?? 0.1,
      target_amount: row?.target_amount || 0,
      status: row?.status || 1,
      remark: row?.remark || '',
    });
  }, [visible, row]);

  useEffect(() => {
    if (!visible || !isUpdate) return;
    setSelectedTierId(resolveSelectedTierId(row, tiers));
  }, [isUpdate, row, tiers, visible]);

  const updateField = (key, value) => {
    setForm((prev) => ({ ...prev, [key]: value }));
  };

  // 选择等级后自动填写比例和业绩目标（可继续手动修改）
  const handleTierSelect = (tierId) => {
    setSelectedTierId(tierId || null);
    if (!tierId) return;
    const tier = tiers.find((t) => Number(t.id) === Number(tierId));
    if (!tier) return;
    setForm((prev) => ({
      ...prev,
      commission_rate: tier.rate,
      target_amount: tier.threshold_usd,
    }));
  };

  const submit = async () => {
    if (!isUpdate && !form.user_id) {
      showError(t('请输入用户 ID'));
      return;
    }

    setSaving(true);
    try {
      let employeeId = row?.id;
      if (isUpdate) {
        await mutateRequest('put', `/api/admin/employee/${row.id}`, {
          commission_rate: toNumber(form.commission_rate),
          target_amount: toNumber(form.target_amount),
          status: toNumber(form.status, 1),
          remark: form.remark,
        });
      } else {
        const created = await mutateRequest('post', '/api/admin/employee', {
          user_id: toNumber(form.user_id),
          commission_rate: toNumber(form.commission_rate),
          target_amount: toNumber(form.target_amount),
          remark: form.remark,
        });
        employeeId = created?.data?.id;
      }
      const tierId = Number(selectedTierId || 0);
      const currentTierId = Number(row?.current_tier_id || 0);
      if (employeeId && tierId > 0 && (!isUpdate || tierId !== currentTierId)) {
        await mutateRequest('post', `/api/admin/employee/${employeeId}/tier`, {
          tier_id: tierId,
          source: 'manual',
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
      {tiers.length > 0 ? (
        <Field label={t('套用等级预设')}>
          <Select
            value={selectedTierId}
            placeholder={t('选择等级自动填写比例和目标（可选）')}
            onChange={handleTierSelect}
            allowClear
            style={{ width: '100%' }}
          >
            {tiers.map((tier) => (
              <Select.Option key={tier.id} value={tier.id}>
                {`等级 ${tier.level}  —  门槛 $${Number(tier.threshold_usd || 0).toFixed(2)}  /  提成 ${(Number(tier.rate || 0) * 100).toFixed(1)}%`}
              </Select.Option>
            ))}
          </Select>
          <Text type='secondary' size='small'>
            {t('选择后自动填入下方数值，仍可手动修改')}
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
      <Field label={t('业绩目标 (USD)')}>
        <InputNumber
          min={0}
          precision={2}
          step={0.01}
          prefix='$'
          value={form.target_amount}
          onChange={(value) => updateField('target_amount', toNumber(value))}
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

function EmployeesTab({
  employees: providedEmployees,
  filters,
  onFiltersChange,
  onReadyToolbar,
}) {
  const { t } = useTranslation();
  const ownEmployees = usePagedEndpoint('/api/admin/employee', filters || {}, {
    enabled: !providedEmployees,
  });
  const employees = providedEmployees || ownEmployees;
  const [modalRow, setModalRow] = useState(null);
  const [modalVisible, setModalVisible] = useState(false);
  const [filterForm, setFilterForm] = useState(() => ({
    user_id: filters?.user_id || undefined,
    keyword: filters?.keyword || '',
    status: filters?.status || 0,
  }));

  useEffect(() => {
    setFilterForm({
      user_id: filters?.user_id || undefined,
      keyword: filters?.keyword || '',
      status: filters?.status || 0,
    });
  }, [filters]);

  const updateFilter = (key, value) => {
    setFilterForm((form) => ({ ...form, [key]: value }));
  };

  const applyFilters = () => {
    onFiltersChange?.(
      buildParams({
        user_id: toNumber(filterForm.user_id),
        keyword: filterForm.keyword?.trim(),
        status: toNumber(filterForm.status),
        sort_by: filters?.sort_by,
        sort_order: filters?.sort_order,
      }),
    );
  };

  const resetFilters = () => {
    setFilterForm({ user_id: undefined, keyword: '', status: 0 });
    onFiltersChange?.({});
  };

  const getSortOrder = (key) => {
    if (filters?.sort_by !== key) return false;
    return filters?.sort_order === 'asc' ? 'ascend' : 'descend';
  };

  const handleTableChange = (_, __, sorter) => {
    const activeSorter = Array.isArray(sorter) ? sorter[0] : sorter;
    const sortKey = activeSorter?.dataIndex || activeSorter?.key;
    const next = { ...(filters || {}) };
    if (sortKey && activeSorter?.sortOrder) {
      next.sort_by =
        sortKey === 'current_tier_level' ? 'current_tier_rate' : sortKey;
      next.sort_order = activeSorter.sortOrder === 'ascend' ? 'asc' : 'desc';
    } else {
      delete next.sort_by;
      delete next.sort_order;
    }
    onFiltersChange?.(next);
  };

  const openCreate = useCallback(() => {
    setModalRow(null);
    setModalVisible(true);
  }, []);

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
    {
      title: t('用户 ID'),
      dataIndex: 'user_id',
      width: 100,
      sorter: true,
      sortOrder: getSortOrder('user_id'),
    },
    {
      title: t('用户名'),
      dataIndex: 'username',
      sorter: true,
      sortOrder: getSortOrder('username'),
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
      title: t('客户总消耗'),
      dataIndex: 'total_consumption_quota',
      width: 130,
      sorter: true,
      sortOrder: getSortOrder('total_consumption_quota'),
      render: (value) => formatBusinessAmount(value),
    },
    {
      title: t('总成本'),
      dataIndex: 'total_cost_quota',
      width: 120,
      sorter: true,
      sortOrder: getSortOrder('total_cost_quota'),
      render: (value) => formatBusinessAmount(value),
    },
    {
      title: t('总利润'),
      dataIndex: 'total_profit_quota',
      width: 120,
      sorter: true,
      sortOrder: getSortOrder('total_profit_quota'),
      render: (value) => formatBusinessAmount(value),
    },
    {
      title: t('总提成'),
      dataIndex: 'total_commission_quota',
      width: 120,
      sorter: true,
      sortOrder: getSortOrder('total_commission_quota'),
      render: (value) => formatBusinessAmount(value),
    },
    {
      title: t('当前等级'),
      dataIndex: 'current_tier_level',
      width: 120,
      sorter: true,
      sortOrder: getSortOrder('current_tier_rate'),
      render: (value, row) => {
        if (!value) return <Text type='secondary'>-</Text>;
        return (
          <div>
            <Tag color='blue'>{`等级 ${value}`}</Tag>
            <div>
              <Text type='secondary' size='small'>
                {formatPercent(row.current_tier_rate)}
              </Text>
            </div>
          </div>
        );
      },
    },
    {
      title: t('佣金比例'),
      dataIndex: 'commission_rate',
      sorter: true,
      sortOrder: getSortOrder('commission_rate'),
      render: (value) => formatPercent(value),
    },
    {
      title: t('业绩目标'),
      dataIndex: 'target_amount',
      sorter: true,
      sortOrder: getSortOrder('target_amount'),
      render: (value) => (value ? formatTargetAmount(value) : t('无限制')),
    },
    {
      title: t('当前业绩'),
      dataIndex: 'current_performance_quota',
      sorter: true,
      sortOrder: getSortOrder('current_performance_quota'),
      render: (value, row) => renderPerformanceProgress(value, row, t),
    },
    {
      title: t('状态'),
      dataIndex: 'status',
      sorter: true,
      sortOrder: getSortOrder('status'),
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

  useEffect(() => {
    if (!onReadyToolbar) return undefined;
    onReadyToolbar(
      <Button
        type='tertiary'
        size='small'
        icon={<Plus size={14} />}
        onClick={openCreate}
      >
        {t('添加员工')}
      </Button>,
    );
    return () => onReadyToolbar(null);
  }, [onReadyToolbar, t]);

  return (
    <>
      {onFiltersChange ? (
        <div
          className='mb-3 flex flex-wrap items-center gap-2'
          style={{ rowGap: 8 }}
        >
          <InputNumber
            size='small'
            min={0}
            hideButtons
            placeholder={t('用户 ID')}
            value={filterForm.user_id}
            onChange={(value) => updateFilter('user_id', value)}
            style={{ width: 120 }}
          />
          <Input
            size='small'
            placeholder={t('搜索用户名 / 显示名称 / 邮箱')}
            value={filterForm.keyword}
            onChange={(value) => updateFilter('keyword', value)}
            style={{ width: 240 }}
          />
          <Select
            size='small'
            value={filterForm.status}
            onChange={(value) => updateFilter('status', value)}
            style={{ width: 120 }}
          >
            <Select.Option value={0}>{t('全部')}</Select.Option>
            <Select.Option value={1}>{t('启用')}</Select.Option>
            <Select.Option value={2}>{t('禁用')}</Select.Option>
          </Select>
          <Button size='small' type='primary' onClick={applyFilters}>
            {t('查询')}
          </Button>
          <Button size='small' type='tertiary' onClick={resetFilters}>
            {t('重置')}
          </Button>
        </div>
      ) : null}
      <ClassicBusinessTable
        rowKey='id'
        columns={columns}
        dataSource={employees.items}
        loading={employees.loading}
        onChange={handleTableChange}
        empty={<BusinessEmpty description={t('搜索无结果')} />}
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
  logs: providedLogs,
  filters,
  onFiltersChange,
  showInlinePagination = embedded,
}) {
  const { t } = useTranslation();
  const ownLogs = usePagedEndpoint(endpoint, filters || {}, {
    enabled: !providedLogs,
  });
  const logs = providedLogs || ownLogs;
  const [filterForm, setFilterForm] = useState(() => ({
    employee_user_id: filters?.employee_user_id || undefined,
    customer_user_id: filters?.customer_user_id || undefined,
    model_name: filters?.model_name || '',
    channel_id: filters?.channel_id || undefined,
    date_range:
      filters?.start_time && filters?.end_time
        ? [
            new Date(Number(filters.start_time) * 1000),
            new Date(Number(filters.end_time) * 1000),
          ]
        : [],
  }));

  useEffect(() => {
    setFilterForm({
      employee_user_id: filters?.employee_user_id || undefined,
      customer_user_id: filters?.customer_user_id || undefined,
      model_name: filters?.model_name || '',
      channel_id: filters?.channel_id || undefined,
      date_range:
        filters?.start_time && filters?.end_time
          ? [
              new Date(Number(filters.start_time) * 1000),
              new Date(Number(filters.end_time) * 1000),
            ]
          : [],
    });
  }, [filters]);

  const updateFilter = (key, value) => {
    setFilterForm((form) => ({ ...form, [key]: value }));
  };

  const applyFilters = () => {
    const [start, end] = Array.isArray(filterForm.date_range)
      ? filterForm.date_range
      : [];
    const nextFilters = buildParams({
      employee_user_id: toNumber(filterForm.employee_user_id),
      customer_user_id: toNumber(filterForm.customer_user_id),
      model_name: filterForm.model_name?.trim(),
      channel_id: toNumber(filterForm.channel_id),
      start_time: start ? Math.floor(new Date(start).getTime() / 1000) : 0,
      end_time: end ? Math.floor(new Date(end).getTime() / 1000) : 0,
    });
    onFiltersChange?.(nextFilters);
  };

  const resetFilters = () => {
    setFilterForm({
      employee_user_id: undefined,
      customer_user_id: undefined,
      model_name: '',
      channel_id: undefined,
      date_range: [],
    });
    onFiltersChange?.({});
  };

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
      render: (value) => formatBusinessAmount(value),
    },
    {
      title: t('成本'),
      dataIndex: 'cost_quota',
      render: (value) => formatBusinessAmount(value),
    },
    {
      title: t('利润'),
      dataIndex: 'profit_quota',
      render: (value) => <AmountText value={value} />,
    },
    {
      title: t('佣金'),
      dataIndex: 'commission_quota',
      render: (value) => <AmountText value={value} />,
    },
    {
      title: t('比例'),
      dataIndex: 'commission_rate',
      render: (value) => formatPercent(value),
    },
  ];

  const content = (
    <>
      {!selfView && onFiltersChange ? (
        <div
          className='mb-3 flex flex-wrap items-center gap-2'
          style={{ rowGap: 8 }}
        >
          <InputNumber
            size='small'
            min={0}
            hideButtons
            placeholder={t('员工 UID')}
            value={filterForm.employee_user_id}
            onChange={(value) => updateFilter('employee_user_id', value)}
            style={{ width: 120 }}
          />
          <InputNumber
            size='small'
            min={0}
            hideButtons
            placeholder={t('客户 UID')}
            value={filterForm.customer_user_id}
            onChange={(value) => updateFilter('customer_user_id', value)}
            style={{ width: 120 }}
          />
          <Input
            size='small'
            placeholder={t('模型名称')}
            value={filterForm.model_name}
            onChange={(value) => updateFilter('model_name', value)}
            style={{ width: 180 }}
          />
          <InputNumber
            size='small'
            min={0}
            hideButtons
            placeholder={t('渠道 ID')}
            value={filterForm.channel_id}
            onChange={(value) => updateFilter('channel_id', value)}
            style={{ width: 110 }}
          />
          <DatePicker
            type='dateTimeRange'
            size='small'
            value={filterForm.date_range}
            placeholder={[t('开始时间'), t('结束时间')]}
            onChange={(value) => updateFilter('date_range', value || [])}
            style={{ width: 300 }}
          />
          <Button size='small' type='primary' onClick={applyFilters}>
            {t('查询')}
          </Button>
          <Button size='small' type='tertiary' onClick={resetFilters}>
            {t('重置')}
          </Button>
        </div>
      ) : null}
      <ClassicBusinessTable
        rowKey='id'
        columns={columns}
        dataSource={logs.items}
        loading={logs.loading}
        empty={<BusinessEmpty description={t('搜索无结果')} />}
      />
      {showInlinePagination ? (
        <ClassicInlinePagination paged={logs} t={t} />
      ) : null}
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

// ============================================================================
// 阶梯提成等级配置
// ============================================================================

function TierModal({ visible, row, onCancel, onSuccess }) {
  const { t } = useTranslation();
  const isUpdate = Boolean(row);
  const [saving, setSaving] = useState(false);
  const [form, setForm] = useState({
    level: 1,
    threshold_usd: 0,
    rate: 0.1,
  });

  useEffect(() => {
    if (!visible) return;
    setForm({
      level: row?.level ?? 1,
      threshold_usd: row?.threshold_usd ?? 0,
      rate: row?.rate ?? 0.1,
    });
  }, [visible, row]);

  const updateField = (key, value) =>
    setForm((prev) => ({ ...prev, [key]: value }));

  const submit = async () => {
    if (!toNumber(form.level, 0) || form.level < 1) {
      showError(t('等级编号必须 ≥ 1'));
      return;
    }
    setSaving(true);
    try {
      const body = {
        level: toNumber(form.level),
        threshold_usd: toNumber(form.threshold_usd),
        rate: toNumber(form.rate),
      };
      if (isUpdate) {
        await mutateRequest('put', `/api/admin/employee/tiers/${row.id}`, body);
      } else {
        await mutateRequest('post', '/api/admin/employee/tiers', body);
      }
      showSuccess(isUpdate ? t('等级已更新') : t('等级已创建'));
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
      title={isUpdate ? t('编辑等级') : t('创建等级')}
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
      <Field label={t('等级编号')}>
        <InputNumber
          min={1}
          step={1}
          precision={0}
          value={form.level}
          onChange={(v) => updateField('level', toNumber(v, 1))}
          style={{ width: '100%' }}
        />
        <Text type='secondary' size='small'>
          {t('正整数，数字越大等级越高（如 1、2、3）')}
        </Text>
      </Field>
      <Field label={t('业绩门槛 (USD)')}>
        <InputNumber
          min={0}
          step={100}
          precision={2}
          prefix='$'
          value={form.threshold_usd}
          onChange={(v) => updateField('threshold_usd', toNumber(v))}
          style={{ width: '100%' }}
        />
        <Text type='secondary' size='small'>
          {t('员工累计利润达到该金额后自动升级')}
        </Text>
      </Field>
      <Field label={t('提成比例')}>
        <InputNumber
          min={0}
          max={1}
          step={0.01}
          value={form.rate}
          onChange={(v) => updateField('rate', toNumber(v))}
          style={{ width: '100%' }}
        />
        <Text type='secondary' size='small'>
          {t('0.1 表示 10%，达到此等级后生效')}
        </Text>
      </Field>
    </Modal>
  );
}

function TiersTab({ onReadyToolbar }) {
  const { t } = useTranslation();
  const [tiers, setTiers] = useState([]);
  const [loading, setLoading] = useState(false);
  const [modalRow, setModalRow] = useState(null);
  const [modalVisible, setModalVisible] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const res = await API.get('/api/admin/employee/tiers', {
        disableDuplicate: true,
      });
      const { success, message, data } = res.data;
      if (success) {
        setTiers(data || []);
      } else {
        showError(message);
      }
    } catch (error) {
      showError(error?.message || 'Request failed');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  const openCreate = useCallback(() => {
    setModalRow(null);
    setModalVisible(true);
  }, []);

  const openEdit = (row) => {
    setModalRow(row);
    setModalVisible(true);
  };

  const closeModal = () => {
    setModalVisible(false);
    setModalRow(null);
  };

  const handleSuccess = () => {
    closeModal();
    load();
  };

  const deleteTier = (row) => {
    Modal.confirm({
      title: t('删除等级'),
      content: t(
        '删除后已处于该等级的员工不会自动降级，但下次升级判断时会使用新配置。',
      ),
      onOk: async () => {
        try {
          await mutateRequest('delete', `/api/admin/employee/tiers/${row.id}`);
          showSuccess(t('等级已删除'));
          load();
        } catch (error) {
          showError(error.message);
        }
      },
    });
  };

  const columns = [
    {
      title: t('等级'),
      dataIndex: 'level',
      width: 80,
      render: (value) => <Tag color='blue'>{`等级 ${value}`}</Tag>,
    },
    {
      title: t('业绩门槛 (USD)'),
      dataIndex: 'threshold_usd',
      render: (value) => (
        <Text strong>{formatExactUsd(Number(value || 0))}</Text>
      ),
    },
    {
      title: t('提成比例'),
      dataIndex: 'rate',
      render: (value) => <Tag color='green'>{formatPercent(value)}</Tag>,
    },
    {
      title: t('操作'),
      width: 120,
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
            onClick={() => deleteTier(row)}
          />
        </Space>
      ),
    },
  ];

  useEffect(() => {
    if (!onReadyToolbar) return undefined;
    onReadyToolbar(
      <Button
        type='tertiary'
        size='small'
        icon={<Plus size={14} />}
        onClick={openCreate}
      >
        {t('添加等级')}
      </Button>,
    );
    return () => onReadyToolbar(null);
  }, [onReadyToolbar, t, openCreate]);

  return (
    <>
      <ClassicBusinessTable
        rowKey='id'
        columns={columns}
        dataSource={tiers}
        loading={loading}
        empty={
          <BusinessEmpty
            description={t('暂无等级配置，点击「添加等级」创建')}
          />
        }
      />
      <TierModal
        visible={modalVisible}
        row={modalRow}
        onCancel={closeModal}
        onSuccess={handleSuccess}
      />
    </>
  );
}

export function Employees() {
  const { t } = useTranslation();
  const [activeTab, setActiveTab] = useState('employees');
  const [tabToolbar, setTabToolbar] = useState(null);
  const [employeeFilters, setEmployeeFilters] = useState({});
  const [commissionLogFilters, setCommissionLogFilters] = useState({});
  const employees = usePagedEndpoint('/api/admin/employee', employeeFilters);
  const commissionLogs = usePagedEndpoint(
    '/api/admin/employee/commission',
    commissionLogFilters,
  );

  const tabs = [
    { key: 'employees', label: t('员工') },
    { key: 'tiers', label: t('提成阶梯') },
    { key: 'logs', label: t('佣金记录') },
  ];

  const pagination =
    activeTab === 'logs' ? (
      <ClassicPagination paged={commissionLogs} t={t} />
    ) : activeTab === 'employees' ? (
      <ClassicPagination paged={employees} t={t} />
    ) : null;

  return (
    <PageShell>
      <BusinessCard
        title={t('员工管理')}
        icon={Users}
        color='var(--semi-color-primary)'
        pagination={pagination}
        searchArea={
          <div className='flex flex-col md:flex-row justify-between items-start md:items-center gap-2 w-full'>
            <div className='flex flex-wrap gap-2'>
              {tabs.map((tab) => (
                <Button
                  key={tab.key}
                  type={activeTab === tab.key ? 'primary' : 'tertiary'}
                  theme={activeTab === tab.key ? 'solid' : 'light'}
                  size='small'
                  onClick={() => setActiveTab(tab.key)}
                >
                  {tab.label}
                </Button>
              ))}
            </div>
            {tabToolbar ? (
              <div className='flex flex-wrap justify-end gap-2 w-full md:w-auto'>
                {tabToolbar}
              </div>
            ) : null}
          </div>
        }
        t={t}
      >
        {activeTab === 'employees' ? (
          <EmployeesTab
            employees={employees}
            filters={employeeFilters}
            onFiltersChange={setEmployeeFilters}
            onReadyToolbar={setTabToolbar}
          />
        ) : activeTab === 'tiers' ? (
          <TiersTab onReadyToolbar={setTabToolbar} />
        ) : (
          <CommissionLogsTable
            endpoint='/api/admin/employee/commission'
            logs={commissionLogs}
            filters={commissionLogFilters}
            onFiltersChange={setCommissionLogFilters}
            title={t('佣金记录')}
            embedded
            showInlinePagination={false}
          />
        )}
      </BusinessCard>
    </PageShell>
  );
}

export function EmployeeConsole() {
  const { t } = useTranslation();
  const [loading, setLoading] = useState(true);
  const [profileData, setProfileData] = useState(null);
  const [summary, setSummary] = useState(null);
  const commissionLogs = usePagedEndpoint(
    '/api/user/employee/commission',
    {},
    {
      enabled: Boolean(profileData?.success && profileData?.data),
    },
  );

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
  const customerTotalConsumptionQuota =
    extension.customer_total_consumption_quota ?? 0;
  const customerTotalConsumptionUsd = extension.customer_total_consumption_usd;
  const totalProfitQuota =
    extension.profit_total_quota ?? extension.total_profit_quota ?? 0;
  const totalProfitUsd =
    extension.profit_total_usd ?? extension.total_profit_usd;
  const totalCommissionQuota =
    extension.total_commission_quota ?? extension.commission_total_quota ?? 0;
  const totalCommissionUsd =
    extension.total_commission_usd ?? extension.commission_total_usd;
  const targetAmount = Number(profile?.target_amount || 0);

  return (
    <PageShell>
      <Spin spinning={loading}>
        {!loading && (!profileData?.success || !profile) ? (
          <BusinessCard
            title={t('我的佣金')}
            icon={BadgeDollarSign}
            color='var(--semi-color-success)'
            t={t}
          >
            <BusinessEmpty
              description={t('你还不是员工，请联系管理员开通员工身份。')}
            />
          </BusinessCard>
        ) : (
          <>
            <BusinessCard
              title={t('我的佣金')}
              icon={BadgeDollarSign}
              color='var(--semi-color-success)'
              pagination={<ClassicPagination paged={commissionLogs} t={t} />}
              t={t}
            >
              <Row gutter={[16, 16]}>
                <Col xs={24} md={12} xl={6}>
                  <StatCard
                    title={t('总消耗')}
                    value={formatBusinessAmount(customerTotalConsumptionQuota)}
                    sub={formatBusinessUsd(customerTotalConsumptionUsd)}
                    icon={DollarSign}
                    color='var(--semi-color-info)'
                  />
                </Col>
                <Col xs={24} md={12} xl={6}>
                  <StatCard
                    title={t('当前业绩')}
                    value={formatBusinessAmount(totalProfitQuota)}
                    sub={formatExactUsd(totalProfitUsd)}
                    icon={TrendingUp}
                    color='var(--semi-color-success)'
                  />
                </Col>
                <Col xs={24} md={12} xl={6}>
                  <StatCard
                    title={t('提成金额')}
                    value={formatBusinessAmount(totalCommissionQuota)}
                    sub={formatBusinessUsd(totalCommissionUsd)}
                    icon={BadgeDollarSign}
                    color='var(--semi-color-success)'
                  />
                </Col>
                <Col xs={24} md={12} xl={6}>
                  <StatCard
                    title={t('提成阶梯')}
                    value={formatPercent(profile?.commission_rate || 0)}
                    sub={
                      targetAmount
                        ? `${t('业绩')}: $${(totalProfitUsd ?? 0).toFixed(2)} / ${formatTargetAmount(targetAmount)}${(totalProfitUsd ?? 0) >= targetAmount ? ' ✓' : ''}`
                        : `${t('业绩目标')}: ${t('无限制')}`
                    }
                    icon={Wallet}
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
                  logs={commissionLogs}
                  embedded
                  showInlinePagination={false}
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
      render: (value) => formatBusinessAmount(value),
    },
    {
      title: t('已用额度'),
      dataIndex: 'used_quota',
      render: (value) => formatBusinessAmount(value),
    },
    {
      title: t('佣金'),
      dataIndex: 'commission_quota',
      render: (value) => (value ? <AmountText value={value} /> : '-'),
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
        icon={UserRoundCheck}
        color='var(--semi-color-primary)'
        actions={
          <Button
            type='tertiary'
            size='small'
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
          empty={<BusinessEmpty description={t('搜索无结果')} />}
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
  const days = { '1d': 1, '7d': 7, '30d': 30, '90d': 90 }[range] || 7;
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
  const [range, setRange] = useState('1d');
  const [customRange, setCustomRange] = useState(() => getPresetRange('1d'));
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
  const channelProfitRows = data?.by_channel_platform || [];
  const employeeRows = (data?.by_employee || []).slice(0, 10);
  const rangeButtons = [
    { key: '1d', label: t('近 1 天') },
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
      render: (value) => formatBusinessAmount(value),
    },
    {
      title: t('成本'),
      dataIndex: 'total_cost',
      render: (value) => formatBusinessAmount(value),
    },
    {
      title: t('利润'),
      dataIndex: 'total_profit',
      render: (value) => <AmountText value={value} />,
    },
    {
      title: t('佣金'),
      dataIndex: 'total_commission',
      render: (value) => <AmountText value={value} />,
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
      render: (value) => formatBusinessAmount(value),
    },
    {
      title: t('估算成本'),
      dataIndex: 'est_cost_quota',
      render: (value) => formatBusinessAmount(value),
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
        icon={TrendingUp}
        color='var(--semi-color-success)'
        actions={
          <Space wrap>
            {rangeButtons.map((item) => (
              <Button
                key={item.key}
                type={range === item.key ? 'primary' : 'tertiary'}
                theme={range === item.key ? 'solid' : 'light'}
                size='small'
                onClick={() => setRange(item.key)}
              >
                {item.label}
              </Button>
            ))}
            <DatePicker
              type='dateTimeRange'
              value={datePickerValue}
              placeholder={[t('开始时间'), t('结束时间')]}
              size='small'
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
                  value={formatBusinessAmount(
                    platform.total_consumption_quota || 0,
                  )}
                  sub={formatBusinessUsd(platform.total_consumption_usd)}
                  icon={Wallet}
                  color='var(--semi-color-info)'
                />
              </Col>
              <Col xs={24} md={12} xl={4}>
                <StatCard
                  title={t('估算成本')}
                  value={formatBusinessAmount(platform.est_cost_quota || 0)}
                  sub={formatBusinessUsd(platform.est_cost_usd)}
                  icon={BriefcaseBusiness}
                  color='var(--semi-color-warning)'
                />
              </Col>
              <Col xs={24} md={12} xl={4}>
                <StatCard
                  title={t('估算利润')}
                  value={formatBusinessAmount(platform.est_profit_quota || 0)}
                  sub={formatBusinessUsd(platform.est_profit_usd)}
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

            <BusinessSection
              title={t('渠道盈利（全平台）')}
              description={t('成本和利润按分组倍率与渠道成本比例估算。')}
            >
              <ClassicBusinessTable
                rowKey='channel_id'
                columns={channelProfitColumns}
                dataSource={channelProfitRows}
                wrapperClassName='business-channel-profit-table pr-1'
                scroll={{ x: '100%', y: 223 }}
                empty={<BusinessEmpty description={t('搜索无结果')} />}
              />
            </BusinessSection>

            <Text strong type='secondary'>
              {t('员工流量')}
            </Text>
            <div className='grid grid-cols-1 md:grid-cols-2 xl:grid-cols-5 gap-4'>
              <div>
                <StatCard
                  title={t('员工收入')}
                  value={formatBusinessAmount(
                    commission.total_revenue_quota || 0,
                  )}
                  sub={formatBusinessUsd(commission.total_revenue_usd)}
                  icon={DollarSign}
                  color='var(--semi-color-info)'
                />
              </div>
              <div>
                <StatCard
                  title={t('员工成本')}
                  value={formatBusinessAmount(commission.total_cost_quota || 0)}
                  sub={formatBusinessUsd(commission.total_cost_usd)}
                  icon={BriefcaseBusiness}
                  color='var(--semi-color-warning)'
                />
              </div>
              <div>
                <StatCard
                  title={t('员工利润')}
                  value={formatBusinessAmount(
                    commission.total_profit_quota || 0,
                  )}
                  sub={formatBusinessUsd(commission.total_profit_usd)}
                  icon={TrendingUp}
                  color='var(--semi-color-success)'
                />
              </div>
              <div>
                <StatCard
                  title={t('佣金总额')}
                  value={formatBusinessAmount(
                    commission.total_commission_quota || 0,
                  )}
                  sub={formatBusinessUsd(commission.total_commission_usd)}
                  icon={BadgeDollarSign}
                />
              </div>
              <div>
                <StatCard
                  title={t('员工毛利率')}
                  value={formatPercent(commission.gross_margin || 0)}
                  sub={`${commission.record_count || 0} ${t('条记录')}`}
                  icon={Wallet}
                />
              </div>
            </div>

            <BusinessSection title={t('按员工')}>
              <ClassicBusinessTable
                rowKey='employee_user_id'
                columns={employeeColumns}
                dataSource={employeeRows}
                wrapperClassName='business-employee-table pr-1'
                scroll={{ x: '100%', y: 223 }}
                empty={<BusinessEmpty description={t('搜索无结果')} />}
              />
            </BusinessSection>
          </div>
        </Spin>
      </BusinessCard>
    </PageShell>
  );
}

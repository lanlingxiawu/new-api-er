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
  Dropdown,
  Empty,
  Input,
  InputNumber,
  Modal,
  Progress,
  Row,
  Select,
  Space,
  Spin,
  Switch,
  Tag,
  TextArea,
  Typography,
} from '@douyinfe/semi-ui';
import {
  IllustrationNoResult,
  IllustrationNoResultDark,
} from '@douyinfe/semi-illustrations';
import {
  BadgeDollarSign,
  BriefcaseBusiness,
  CalendarDays,
  ChevronLeft,
  ChevronRight,
  Clock,
  DollarSign,
  MoreHorizontal,
  Pencil,
  Plus,
  RefreshCw,
  Search,
  Trash2,
  TrendingDown,
  TrendingUp,
  UserRoundCheck,
  UserRoundPlus,
  Users,
  Wallet,
  X,
} from 'lucide-react';
import { API, renderQuota, showError, showSuccess } from '../../helpers';
import { getQuotaPerUnit } from '../../helpers/quota';
import CardPro from '../../components/common/ui/CardPro';
import CardTable from '../../components/common/ui/CardTable';
import CompactModeToggle from '../../components/common/ui/CompactModeToggle';
import { createCardProPagination } from '../../helpers/utils';
import { useIsMobile } from '../../hooks/common/useIsMobile';
import { useTableCompactMode } from '../../hooks/common/useTableCompactMode';

const { Text } = Typography;
const PAGE_SIZE = 10;
const EMPLOYEE_PERFORMANCE_TOP_LIMIT = 10;
const EMPLOYEE_CUSTOMERS_PAGE_SIZE = 8;
const BUSINESS_STATS_BACKFILL_RUNNING_KEY = 'business_stats_backfill_running';
const BUSINESS_AMOUNT_DIGITS = 4;
<<<<<<< Updated upstream
const COMMISSION_RATE_PRESETS = [0.05, 0.08, 0.1, 0.15, 0.2];
const DEFAULT_TIER_GROUP = '通用';
=======
<<<<<<< Updated upstream
=======
const COMMISSION_RATE_PRESETS = [0.05, 0.08, 0.1, 0.15, 0.2];
const DEFAULT_TIER_GROUP = '通用';
const RESET_TIMEZONES = ['Asia/Shanghai', 'Local'];
const RESET_DAY_OPTIONS = Array.from({ length: 31 }, (_, index) => index + 1);

function normalizeResetTimezone(timezone) {
  return timezone === 'Local' ? 'Local' : 'Asia/Shanghai';
}
>>>>>>> Stashed changes

const getTierGroup = (tier) => (tier?.group || '').trim() || DEFAULT_TIER_GROUP;

const compareTierGroupLevel = (a, b) => {
  const groupCompare = getTierGroup(a).localeCompare(
    getTierGroup(b),
    undefined,
    {
      numeric: true,
      sensitivity: 'base',
    },
  );
  if (groupCompare !== 0) return groupCompare;
  return (
    toNumber(a?.level) - toNumber(b?.level) ||
    toNumber(a?.threshold_usd) - toNumber(b?.threshold_usd) ||
    toNumber(a?.id) - toNumber(b?.id)
  );
};

const TIER_GROUP_TAG_COLORS = [
  'blue',
  'green',
  'orange',
  'purple',
  'cyan',
  'pink',
  'teal',
  'violet',
];

const TIER_LEVEL_TAG_COLORS = [
  'grey',
  'indigo',
  'teal',
  'orange',
  'pink',
  'blue',
  'green',
  'yellow',
];

const getTierGroupTagColor = (group) => {
  const text = String(group || DEFAULT_TIER_GROUP);
  let hash = 0;
  for (const char of text) {
    hash = (hash * 31 + char.charCodeAt(0)) >>> 0;
  }
  return TIER_GROUP_TAG_COLORS[hash % TIER_GROUP_TAG_COLORS.length];
};

const getTierLevelTagColor = (level) => {
  const numericLevel = Number(level || 0);
  if (Number.isFinite(numericLevel) && numericLevel > 0) {
    return TIER_LEVEL_TAG_COLORS[
      (Math.floor(numericLevel) - 1) % TIER_LEVEL_TAG_COLORS.length
    ];
  }
  return TIER_LEVEL_TAG_COLORS[0];
};
<<<<<<< Updated upstream
=======
>>>>>>> Stashed changes
>>>>>>> Stashed changes

const formatTs = (ts) => {
  if (!ts) return '-';
  return new Date(Number(ts) * 1000).toLocaleString();
};

const formatDate = (ts) => {
  if (!ts) return '-';
  return new Date(Number(ts) * 1000).toLocaleDateString();
};

const monthValueToTimestamp = (value) => {
  if (!value) return undefined;
  const date = value instanceof Date ? value : new Date(value);
  if (Number.isNaN(date.getTime())) return undefined;
  return Math.floor(
    new Date(date.getFullYear(), date.getMonth(), 1, 0, 0, 0).getTime() / 1000,
  );
};

const currentMonthValue = () => {
  const now = new Date();
  return `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}`;
};

const shiftMonthValue = (value, offset) => {
  const [year, month] = String(value || currentMonthValue())
    .split('-')
    .map(Number);
  const date = new Date(year, (month || 1) - 1 + offset, 1);
  return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}`;
};

const monthValueToRange = (value) => {
  const [year, month] = String(value || currentMonthValue())
    .split('-')
    .map(Number);
  const start = new Date(year, month - 1, 1, 0, 0, 0);
  const end = new Date(year, month, 0, 23, 59, 59);
  return {
    start_time: Math.floor(start.getTime() / 1000),
    end_time: Math.floor(end.getTime() / 1000),
  };
};

const dateKey = (date) =>
  `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}`;

const isCurrentDay = (date) => dateKey(date) === dateKey(new Date());

const buildMonthCalendarCells = (monthValue, days = []) => {
  const [year, month] = String(monthValue || currentMonthValue())
    .split('-')
    .map(Number);
  const first = new Date(year, month - 1, 1);
  const gridStart = new Date(first);
  gridStart.setDate(first.getDate() - first.getDay());
  const dayMap = new Map(days.map((day) => [day.date, day]));
  return Array.from({ length: 42 }, (_, index) => {
    const date = new Date(gridStart);
    date.setDate(gridStart.getDate() + index);
    const key = dateKey(date);
    return {
      key,
      date,
      inMonth: date.getMonth() === month - 1,
      stat: dayMap.get(key),
    };
  });
};

const monthLabel = (value) => {
  const [year, month] = String(value || currentMonthValue())
    .split('-')
    .map(Number);
  return new Date(year, month - 1, 1).toLocaleDateString(undefined, {
    year: 'numeric',
    month: 'long',
  });
};

const toNumber = (value, fallback = 0) => {
  const number = Number(value);
  return Number.isFinite(number) ? number : fallback;
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

const getPerformanceTargetAmount = (row) => {
  const nextTierTarget = Number(row?.next_tier_threshold_usd || 0);
  if (Number.isFinite(nextTierTarget) && nextTierTarget > 0) {
    return nextTierTarget;
  }
  const profileTarget = Number(row?.target_amount || 0);
  return Number.isFinite(profileTarget) && profileTarget > 0
    ? profileTarget
    : 0;
};

const renderPerformanceProgress = (value, row, t) => {
  const currentQuota = Number(value || 0);
  const targetAmount = getPerformanceTargetAmount(row);

  if (!Number.isFinite(targetAmount) || targetAmount <= 0) {
    return formatBusinessAmount(currentQuota);
  }

  const targetQuota = targetAmount * getQuotaPerUnit();
  const rawPercent = targetQuota > 0 ? (currentQuota / targetQuota) * 100 : 0;
  const percent = Math.max(0, Math.min(100, rawPercent));
  const percentText = `${Number.isFinite(rawPercent) ? rawPercent.toFixed(0) : '0'}%`;

  return (
    <div className='min-w-[120px]'>
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
    onPageSizeChange: paged.setPageSize,
    showSizeChanger: Boolean(paged.setPageSize),
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
  hasMore = false,
  onLoadMore,
  ...props
}) {
  const handleScroll = useCallback(
    (event) => {
      if (!hasMore || !onLoadMore) return;
      const target = event.currentTarget;
      const distanceToBottom =
        target.scrollHeight - target.scrollTop - target.clientHeight;
      if (distanceToBottom <= 24) {
        onLoadMore();
      }
    },
    [hasMore, onLoadMore],
  );

  return (
    <div
      className={`w-full ${wrapperClassName}`.trim()}
      style={wrapperStyle}
      onScroll={handleScroll}
    >
      <CardTable
        {...props}
        hidePagination
        scroll={scroll}
        onScroll={handleScroll}
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

function SummaryPanel({ children, danger = false, className = '' }) {
  return (
    <div
      className={`rounded-lg border px-3 py-2 ${className}`.trim()}
      style={{
        borderColor: danger
          ? 'var(--semi-color-danger-light-default)'
          : 'var(--semi-color-border)',
        background: danger
          ? 'var(--semi-color-danger-light-default)'
          : 'var(--semi-color-fill-0)',
      }}
    >
      {children}
    </div>
  );
}

function SummaryItem({ label, value }) {
  return (
    <div className='flex min-w-0 items-center justify-between gap-3 py-0.5'>
      <Text type='secondary' size='small' className='shrink-0'>
        {label}
      </Text>
      <Text strong size='small' ellipsis>
        {value ?? '-'}
      </Text>
    </div>
  );
}

function RowActionDropdown({ label, actions }) {
  return (
    <Dropdown trigger='click' position='bottomRight' menu={actions}>
      <Button type='tertiary' size='small' icon={<MoreHorizontal size={14} />}>
        {label}
      </Button>
    </Dropdown>
  );
}

function usePagedEndpoint(endpoint, params = {}, options = {}) {
  const {
    pageSize: initialPageSize = PAGE_SIZE,
    paginate = true,
    enabled = true,
  } = options;
  const [page, setPage] = useState(1);
  const [pageSize, setPageSizeState] = useState(initialPageSize);
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
  }, [enabled, endpoint, page, pageSize, paramsKey]);

  useEffect(() => {
    if (enabled) {
      load();
    }
  }, [enabled, load]);

  const setPageSize = useCallback((nextPageSize) => {
    setPageSizeState(Number(nextPageSize) || PAGE_SIZE);
    setPage(1);
  }, []);

  const pagination = paginate
    ? {
        currentPage: page,
        pageSize,
        total,
        showSizeChanger: true,
        onPageChange: setPage,
        onPageSizeChange: setPageSize,
      }
    : false;

  return {
    page,
    pageSize,
    paginate,
    setPage,
    setPageSize,
    items,
    total,
    loading,
    load,
    pagination,
  };
}

function useEndpointData(endpoint, params = {}, options = {}) {
  const { enabled = true } = options;
  const [data, setData] = useState(null);
  const [loading, setLoading] = useState(false);
  const paramsKey = JSON.stringify(params);

  const load = useCallback(async () => {
    if (!enabled) return;
    setLoading(true);
    try {
      const res = await API.get(endpoint, {
        params: buildParams(params),
        disableDuplicate: true,
      });
      const { success, message, data: payload } = res.data;
      if (success) {
        setData(payload || null);
      } else {
        showError(message);
      }
    } catch (error) {
      showError(error?.message || 'Request failed');
    } finally {
      setLoading(false);
    }
  }, [enabled, endpoint, paramsKey]);

  useEffect(() => {
    if (enabled) {
      load();
    }
  }, [enabled, load]);

  return { data, loading, reload: load };
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

function UserPicker({
  value,
  onSelect,
  onClear,
  multiple = false,
  selectedUsers = [],
  onToggle,
  excludeEmployee = true,
  excludeAdmin = false,
  excludeAssignedCustomer = false,
  employeeUserId,
}) {
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
  const selectedIds = useMemo(
    () => new Set(selectedUsers.map((user) => user.id)),
    [selectedUsers],
  );

  useEffect(() => {
    if (!value || multiple) setSelectedLabel('');
  }, [multiple, value]);

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
            exclude_employee: excludeEmployee,
            exclude_admin: excludeAdmin,
            exclude_assigned_customer: excludeAssignedCustomer,
          }),
          disableDuplicate: true,
        });
        const data = res.data?.data || {};
        const nextItems = data.items || [];
        const currentPage = Number(data.page || nextPage);
        const pageSize = Number(data.page_size || 20);
        const total = Number(data.total || 0);
        const sortByAssigned = (arr) =>
          [...arr].sort((a, b) => {
            if (a.is_assigned_customer === b.is_assigned_customer) return 0;
            return a.is_assigned_customer ? 1 : -1;
          });
        setUsers((prev) => {
          if (replace) return sortByAssigned(nextItems);
          const existingIds = new Set(prev.map((user) => user.id));
          return sortByAssigned([
            ...prev,
            ...nextItems.filter((user) => !existingIds.has(user.id)),
          ]);
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
    [debouncedKeyword, excludeAdmin, excludeAssignedCustomer, excludeEmployee],
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

  const clearSelection = () => {
    setSelectedLabel('');
    setKeyword('');
    setDebouncedKeyword('');
    onClear?.();
    setOpen(true);
  };

  return (
    <div ref={containerRef} style={{ position: 'relative' }}>
      <Input
        value={multiple ? keyword : open ? keyword : selectedLabel}
        placeholder={t('搜索用户名 / 显示名称 / 邮箱')}
        autoComplete='off'
        onChange={(nextValue) => {
          setKeyword(nextValue);
          if (!open) setOpen(true);
        }}
        onFocus={() => {
          if (!multiple) setKeyword('');
          setOpen(true);
        }}
      />
      {!multiple && value && !open ? (
        <Button
          type='tertiary'
          theme='borderless'
          size='small'
          icon={<X size={14} />}
          aria-label={t('清空')}
          title={t('清空')}
          onMouseDown={(event) => event.preventDefault()}
          onClick={clearSelection}
          style={{
            position: 'absolute',
            right: 4,
            top: 4,
            zIndex: 1,
          }}
        />
      ) : null}
      {open ? (
        <div
          style={{
            position: 'absolute',
            top: 'calc(100% + 8px)',
            left: 0,
            right: 0,
            zIndex: 1200,
            maxHeight: 224,
            overflowY: 'auto',
            border: '1px solid var(--semi-color-border)',
            borderRadius: 10,
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
              {users.map((user) => {
                const isCurrentEmployeeCustomer =
                  user.is_assigned_customer &&
                  user.assigned_employee_user_id === employeeUserId;
                const isSelected = multiple
                  ? selectedIds.has(user.id)
                  : value === user.id;
                const canSelect = !multiple || !isCurrentEmployeeCustomer;
                return (
                  <div
                    key={user.id}
                    role='option'
                    aria-selected={isSelected}
                    className={`rounded px-3 py-2 text-sm hover:bg-semi-color-fill-1 ${
                      canSelect ? 'cursor-pointer' : 'cursor-default'
                    }`}
                    style={{
                      background: isSelected
                        ? 'var(--semi-color-fill-1)'
                        : user.is_assigned_customer
                          ? 'var(--semi-color-warning-light-default)'
                          : undefined,
                    }}
                    onMouseDown={(event) => {
                      event.preventDefault();
                      if (!canSelect) return;
                      const label = `${user.username}${
                        user.display_name ? ` (${user.display_name})` : ''
                      } #${user.id}`;
                      if (multiple) {
                        onToggle?.(user);
                        setOpen(true);
                      } else {
                        setSelectedLabel(label);
                        onSelect(user);
                        setOpen(false);
                      }
                    }}
                  >
                    <div className='flex min-w-0 items-center justify-between gap-3'>
                      <span className='truncate font-medium'>
                        {user.username}
                        {user.display_name ? ` (${user.display_name})` : ''}
                      </span>
                      <div className='flex items-center gap-1.5 shrink-0'>
                        {multiple && isSelected ? (
                          <Tag color='blue' size='small'>
                            {t('已选择')}
                          </Tag>
                        ) : null}
                        {user.is_assigned_customer ? (
                          <Tag color='orange' size='small'>
                            {user.assigned_employee_user_id === employeeUserId
                              ? t('当前员工')
                              : t('已分配')}
                          </Tag>
                        ) : null}
                        <span className='text-xs text-semi-color-text-2'>
                          #{user.id}
                        </span>
                      </div>
                    </div>
                    {user.is_assigned_customer ? (
                      <div
                        className='mt-0.5 truncate text-xs'
                        style={{ color: 'var(--semi-color-warning)' }}
                      >
                        {user.assigned_employee_name
                          ? `${t('已分配给')}: ${user.assigned_employee_name}`
                          : t('已分配给其他员工')}
                      </div>
                    ) : user.email ? (
                      <div className='mt-0.5 truncate text-xs text-semi-color-text-2'>
                        {user.email}
                      </div>
                    ) : null}
                  </div>
                );
              })}
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

function RemoveCustomersModal({ visible, row, onCancel, onRefresh }) {
  const { t } = useTranslation();
  const [keyword, setKeyword] = useState('');
  const [debouncedKeyword, setDebouncedKeyword] = useState('');
  const [customers, setCustomers] = useState([]);
  const [loading, setLoading] = useState(false);
  const [page, setPage] = useState(1);
  const [total, setTotal] = useState(0);
  const [customerToRemove, setCustomerToRemove] = useState(null);
  const [removing, setRemoving] = useState(false);

  useEffect(() => {
    const timer = setTimeout(() => {
      setDebouncedKeyword(keyword.trim());
    }, 300);
    return () => clearTimeout(timer);
  }, [keyword]);

  useEffect(() => {
    if (!visible) {
      setKeyword('');
      setDebouncedKeyword('');
      setCustomers([]);
      setTotal(0);
      setPage(1);
      setCustomerToRemove(null);
    }
  }, [visible]);

  useEffect(() => {
    setPage(1);
  }, [debouncedKeyword, row?.id]);

  const loadCustomers = useCallback(
    async (nextPage = page) => {
      if (!visible || !row?.id) return;
      setLoading(true);
      try {
        const res = await API.get(`/api/admin/employee/${row.id}/customers`, {
          params: buildParams({
            keyword: debouncedKeyword,
            page_size: EMPLOYEE_CUSTOMERS_PAGE_SIZE,
            page: nextPage,
          }),
          disableDuplicate: true,
        });
        const data = res.data?.data || {};
        const nextItems = data.items || [];
        const currentPage = Number(data.page || nextPage);
        const nextTotal = Number(data.total || 0);
        setCustomers(nextItems);
        setPage(currentPage);
        setTotal(nextTotal);
      } catch (error) {
        setCustomers([]);
        setTotal(0);
      } finally {
        setLoading(false);
      }
    },
    [debouncedKeyword, page, row?.id, visible],
  );

  useEffect(() => {
    if (!visible || !row?.id) return;
    loadCustomers(page);
  }, [loadCustomers, page, row?.id, visible]);

  const getCustomerLabel = (customer) =>
    `${customer.username || '-'}${
      customer.display_name ? ` (${customer.display_name})` : ''
    }`;
  const getCustomerUserId = (customer) =>
    customer.customer_user_id || customer.id;
  const pageCount = Math.max(
    1,
    Math.ceil(total / EMPLOYEE_CUSTOMERS_PAGE_SIZE),
  );
  const pageStart =
    total === 0 ? 0 : (page - 1) * EMPLOYEE_CUSTOMERS_PAGE_SIZE + 1;
  const pageEnd = Math.min(total, page * EMPLOYEE_CUSTOMERS_PAGE_SIZE);
  const employeeLabel =
    row?.username ||
    row?.display_name ||
    (row?.user_id ? `#${row.user_id}` : '-');

  const performRemoveCustomer = async () => {
    if (!customerToRemove || !row?.id) return;
    const customerUserId = getCustomerUserId(customerToRemove);
    setRemoving(true);
    try {
      await mutateRequest(
        'delete',
        `/api/admin/employee/${row.id}/customer/${customerUserId}`,
      );
      showSuccess(t('客户已从员工移除'));
      setCustomerToRemove(null);
      onRefresh?.();
      if (customers.length === 1 && page > 1) {
        setPage((current) => Math.max(1, current - 1));
      } else {
        await loadCustomers(page);
      }
    } catch (error) {
      showError(error.message);
    } finally {
      setRemoving(false);
    }
  };

  return (
    <>
      <Modal
        visible={visible}
        title={t('员工客户列表')}
        onCancel={onCancel}
        width={760}
        bodyStyle={{ height: 560, overflow: 'hidden' }}
        footer={<Button onClick={onCancel}>{t('关闭')}</Button>}
      >
        <div className='flex h-full min-h-0 flex-col gap-3'>
          <div
            className='rounded-lg border px-3 py-2'
            style={{
              borderColor: 'var(--semi-color-border)',
              background: 'var(--semi-color-fill-0)',
            }}
          >
            <Text type='secondary' size='small'>
              {t('员工')}
            </Text>
            <div className='mt-1 flex min-w-0 items-center gap-2'>
              <Text strong ellipsis>
                {row?.username || `#${row?.user_id}`}
              </Text>
              {row?.display_name ? (
                <Text type='secondary' ellipsis>
                  {row.display_name}
                </Text>
              ) : null}
              <Tag size='small' color='white' className='ml-auto shrink-0'>
                #{row?.user_id}
              </Tag>
            </div>
          </div>
          <Text type='secondary' size='small'>
            {t('查看分配给该员工的客户。需要时可在列表中移除客户。')}
          </Text>
          <div className='flex flex-col gap-2 md:flex-row md:items-center md:justify-between'>
            <div className='min-w-0 flex-1'>
              <Input
                prefix={<Search size={14} />}
                suffix={
                  keyword ? (
                    <Button
                      type='tertiary'
                      theme='borderless'
                      size='small'
                      icon={<X size={14} />}
                      aria-label={t('清空')}
                      onClick={() => {
                        setKeyword('');
                        setDebouncedKeyword('');
                      }}
                    />
                  ) : null
                }
                value={keyword}
                placeholder={t('搜索当前客户')}
                onChange={setKeyword}
              />
            </div>
            <Space spacing={8}>
              <Tag size='small' color='white'>
                {t('共 {{count}} 个客户', { count: total })}
              </Tag>
              <Button
                type='tertiary'
                theme='light'
                size='small'
                icon={<RefreshCw size={14} />}
                loading={loading}
                onClick={() => loadCustomers(page)}
              >
                {t('刷新')}
              </Button>
            </Space>
          </div>
          <div
            className='flex min-h-0 flex-1 flex-col overflow-hidden rounded-lg border'
            style={{
              borderColor: 'var(--semi-color-border)',
            }}
          >
            <div
              className='grid grid-cols-[minmax(0,1fr)_96px_96px_88px] gap-4 border-b px-4 py-2 text-xs font-medium'
              style={{
                background: 'var(--semi-color-fill-0)',
                color: 'var(--semi-color-text-2)',
                borderColor: 'var(--semi-color-border)',
              }}
            >
              <span>{t('客户')}</span>
              <span className='text-right'>{t('已用额度')}</span>
              <span className='text-right'>{t('提成')}</span>
              <span className='text-right'>{t('操作')}</span>
            </div>
            <div className='min-h-0 flex-1 overflow-y-auto'>
              {loading ? (
                <div className='flex h-full min-h-[260px] items-center justify-center text-sm text-semi-color-text-2'>
                  {t('加载中...')}
                </div>
              ) : customers.length === 0 ? (
                <div className='flex h-full min-h-[260px] items-center justify-center px-6 text-center text-sm text-semi-color-text-2'>
                  {debouncedKeyword
                    ? t('未找到客户')
                    : t('该员工暂无已分配客户')}
                </div>
              ) : (
                customers.map((customer, index) => {
                  const customerUserId = getCustomerUserId(customer);
                  return (
                    <div
                      key={customerUserId}
                      className='grid min-h-14 grid-cols-[minmax(0,1fr)_96px_96px_88px] items-center gap-4 px-4 py-2.5 text-sm hover:bg-semi-color-fill-1'
                      style={{
                        borderBottom:
                          index < customers.length - 1
                            ? '1px solid var(--semi-color-fill-1)'
                            : undefined,
                      }}
                    >
                      <div className='min-w-0'>
                        <div className='flex min-w-0 items-center gap-2'>
                          <span className='truncate font-medium'>
                            {customer.username || '-'}
                            {customer.remark ? ` (${customer.remark})` : ''}
                          </span>
                          {customer.customer_employee_status === 2 ? (
                            <Tag color='grey' size='small'>
                              {t('员工身份已禁用')}
                            </Tag>
                          ) : null}
                        </div>
                        <div className='truncate text-xs text-semi-color-text-2'>
                          #{customerUserId}
                          {customer.email ? ` / ${customer.email}` : ''}
                        </div>
                      </div>
                      <div className='text-right font-medium tabular-nums'>
                        {formatBusinessAmount(customer.used_quota || 0)}
                      </div>
                      <div className='text-right font-medium tabular-nums'>
                        {formatBusinessAmount(customer.commission_quota || 0)}
                      </div>
                      <div className='flex justify-end'>
                        <Button
                          size='small'
                          type='danger'
                          theme='borderless'
                          icon={<X size={14} />}
                          onClick={() => setCustomerToRemove(customer)}
                        >
                          {t('移除')}
                        </Button>
                      </div>
                    </div>
                  );
                })
              )}
            </div>
          </div>
          <div className='flex flex-col gap-2 pt-3 md:flex-row md:items-center md:justify-between'>
            <Text type='secondary' size='small'>
              {t('显示 {{start}}-{{end}} / {{total}} 个客户', {
                start: pageStart,
                end: pageEnd,
                total,
              })}
            </Text>
            <Space>
              <Button
                size='small'
                type='tertiary'
                theme='light'
                disabled={page <= 1 || loading}
                onClick={() => setPage((current) => Math.max(1, current - 1))}
              >
                {t('上一页')}
              </Button>
              <Tag color='white' size='small'>
                {t('第 {{current}} / {{total}} 页', {
                  current: page,
                  total: pageCount,
                })}
              </Tag>
              <Button
                size='small'
                type='tertiary'
                theme='light'
                disabled={page >= pageCount || loading}
                onClick={() =>
                  setPage((current) => Math.min(pageCount, current + 1))
                }
              >
                {t('下一页')}
              </Button>
            </Space>
          </div>
        </div>
      </Modal>
      <Modal
        visible={Boolean(customerToRemove)}
        title={t('确认移除客户')}
        onCancel={() => {
          if (!removing) setCustomerToRemove(null);
        }}
        width={460}
        footer={
          <Space>
            <Button
              disabled={removing}
              onClick={() => setCustomerToRemove(null)}
            >
              {t('取消')}
            </Button>
            <Button
              type='danger'
              loading={removing}
              onClick={performRemoveCustomer}
            >
              {t('移除客户')}
            </Button>
          </Space>
        }
      >
        <div className='space-y-3'>
          <Text>{t('移除后，该客户后续消费将不再计入该员工提成。')}</Text>
          {customerToRemove ? (
            <SummaryPanel danger>
              <SummaryItem
                label={t('客户')}
                value={getCustomerLabel(customerToRemove)}
              />
              <SummaryItem
                label={t('用户 ID')}
                value={`#${getCustomerUserId(customerToRemove)}`}
              />
              <SummaryItem label={t('目标员工')} value={employeeLabel} />
            </SummaryPanel>
          ) : null}
        </div>
      </Modal>
    </>
  );
}

function AssignCustomerModal({ visible, row, onCancel, onSuccess }) {
  const { t } = useTranslation();
  const [saving, setSaving] = useState(false);
  const [selectedCustomers, setSelectedCustomers] = useState([]);

  useEffect(() => {
    if (!visible) {
      setSelectedCustomers([]);
    }
  }, [visible]);

  const employeeLabel =
    row?.username ||
    row?.display_name ||
    (row?.user_id ? `#${row.user_id}` : '-');
  const selectedCount = selectedCustomers.length;
  const reassignmentCustomers = selectedCustomers.filter(
    (customer) =>
      customer.is_assigned_customer &&
      customer.assigned_employee_user_id !== row?.user_id,
  );
  const isReassignment = reassignmentCustomers.length > 0;

  const getCustomerLabel = (customer) =>
    `${customer.username || '-'}${
      customer.display_name ? ` (${customer.display_name})` : ''
    }`;

  const toggleCustomer = (customer) => {
    setSelectedCustomers((prev) => {
      if (prev.some((item) => item.id === customer.id)) {
        return prev.filter((item) => item.id !== customer.id);
      }
      return [...prev, customer];
    });
  };

  const removeSelectedCustomer = (customerId) => {
    setSelectedCustomers((prev) =>
      prev.filter((customer) => customer.id !== customerId),
    );
  };

  const performAssign = async () => {
    setSaving(true);
    try {
      for (const customer of selectedCustomers) {
        await mutateRequest(
          'post',
          `/api/admin/employee/${row.id}/assign-customer`,
          {
            user_id: toNumber(customer.id),
          },
        );
      }
      showSuccess(
        isReassignment
          ? t('已成功重新分共 {{count}} 个客户', {
              count: selectedCount,
            })
          : t('已成功分共 {{count}} 个客户', { count: selectedCount }),
      );
      onSuccess();
    } catch (error) {
      showError(error.message);
    } finally {
      setSaving(false);
    }
  };

  const submit = async () => {
    if (selectedCount === 0) {
      showError(t('请选择至少一个客户'));
      return;
    }
    if (isReassignment) {
      Modal.confirm({
        title: t('确认重新分配'),
        content: (
          <div className='space-y-3'>
            <Text>
              {t(
                '选中的客户中包含已分配给其他员工的客户，重新分配会更新后续提成归属。',
              )}
            </Text>
            <SummaryPanel danger>
              <SummaryItem label={t('已选择客户')} value={selectedCount} />
              <SummaryItem
                label={t('需要重新分配')}
                value={reassignmentCustomers.length}
              />
              <SummaryItem label={t('目标员工')} value={employeeLabel} />
              <div className='mt-3 flex flex-wrap gap-2'>
                {selectedCustomers.slice(0, 6).map((customer) => (
                  <Tag key={customer.id} color='white' size='small'>
                    {getCustomerLabel(customer)} #{customer.id}
                  </Tag>
                ))}
                {selectedCustomers.length > 6 ? (
                  <Tag color='white' size='small'>
                    +{selectedCustomers.length - 6}
                  </Tag>
                ) : null}
              </div>
            </SummaryPanel>
          </div>
        ),
        okText: t('重新分配选中客户'),
        cancelText: t('取消'),
        okType: 'danger',
        onOk: performAssign,
      });
      return;
    }
    await performAssign();
  };

  const submitLabel = isReassignment
    ? t('重新分配选中客户')
    : t('分配选中客户');

  return (
    <Modal
      visible={visible}
      title={t('分配客户')}
      onCancel={onCancel}
      width={560}
      bodyStyle={{ height: 500, overflow: 'visible' }}
      footer={
        <Space>
          <Button onClick={onCancel}>{t('取消')}</Button>
          <Button
            type='primary'
            loading={saving}
            disabled={selectedCount === 0}
            onClick={submit}
          >
            {submitLabel}
          </Button>
        </Space>
      }
    >
      <div
        className='mb-4 rounded-lg border px-3 py-2'
        style={{
          borderColor: 'var(--semi-color-border)',
          background: 'var(--semi-color-fill-0)',
        }}
      >
        <Text type='secondary' size='small'>
          {t('员工')}
        </Text>
        <div className='mt-1 flex min-w-0 items-center gap-2'>
          <Text strong ellipsis>
            {row?.username || `#${row?.user_id}`}
          </Text>
          {row?.display_name ? (
            <Text type='secondary' ellipsis>
              {row.display_name}
            </Text>
          ) : null}
          <Tag size='small' color='white' className='ml-auto shrink-0'>
            #{row?.user_id}
          </Tag>
        </div>
      </div>
      <Field label={t('添加客户')}>
        <UserPicker
          multiple
          selectedUsers={selectedCustomers}
          onToggle={toggleCustomer}
          excludeEmployee
          excludeAdmin
          employeeUserId={row?.user_id}
        />
        {selectedCustomers.length > 0 ? (
          <SummaryPanel danger={isReassignment} className='mt-3'>
            <SummaryItem label={t('已选择客户')} value={selectedCount} />
            <SummaryItem label={t('目标员工')} value={employeeLabel} />
            {isReassignment ? (
              <SummaryItem
                label={t('需要重新分配')}
                value={reassignmentCustomers.length}
              />
            ) : null}
            <div className='mt-3 flex flex-wrap gap-2'>
              {selectedCustomers.map((customer) => (
                <Tag
                  key={customer.id}
                  color='white'
                  size='small'
                  closable
                  onClose={() => removeSelectedCustomer(customer.id)}
                >
                  {getCustomerLabel(customer)} #{customer.id}
                </Tag>
              ))}
            </div>
            {isReassignment ? (
              <Text type='warning' size='small' className='mt-2 block'>
                {t(
                  '选中的客户中包含已分配给其他员工的客户，重新分配会更新后续提成归属。',
                )}
              </Text>
            ) : null}
          </SummaryPanel>
        ) : null}
        <Text
          type='secondary'
          size='small'
          className='mt-2 block leading-relaxed'
        >
          {t('将该用户的受邀人设置为此员工，其消费将为员工产生提成')}
        </Text>
      </Field>
    </Modal>
  );
}

function EmployeeModal({ visible, row, onCancel, onSuccess }) {
  const { t } = useTranslation();
  const isUpdate = Boolean(row);
  const [saving, setSaving] = useState(false);
  const [tiers, setTiers] = useState([]);
  const [form, setForm] = useState({
    user_id: 0,
    tier_id: null,
    status: 1,
    remark: '',
  });

  const tierGroups = useMemo(() => {
    const map = new Map();
    tiers.forEach((tier) => {
      const group = getTierGroup(tier);
      if (!map.has(group)) map.set(group, []);
      map.get(group).push(tier);
    });
    return Array.from(map.entries())
      .sort((a, b) =>
        a[0].localeCompare(b[0], undefined, {
          numeric: true,
          sensitivity: 'base',
        }),
      )
      .map(([group, ts]) => [
        group,
        [...ts].sort(
          (a, b) =>
            toNumber(a.level) - toNumber(b.level) ||
            toNumber(a.threshold_usd) - toNumber(b.threshold_usd) ||
            toNumber(a.id) - toNumber(b.id),
        ),
      ]);
  }, [tiers]);

  const currentTier = tiers.find((t) => t.id === form.tier_id);
  const employeeLabel =
    row?.username ||
    row?.display_name ||
    (row?.user_id ? `#${row.user_id}` : '-');

  const [activeGroup, setActiveGroup] = useState(null);

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
    setForm({
      user_id: row?.user_id || 0,
      tier_id: row?.current_tier_id || null,
      status: row?.status || 1,
      remark: row?.remark || '',
    });
    setActiveGroup(null);
  }, [visible, row]);

  useEffect(() => {
    if (!visible || tiers.length === 0) return;
    const initialTierId = row?.current_tier_id || null;
    if (initialTierId) {
      const found = tiers.find((t) => t.id === initialTierId);
      if (found) setActiveGroup(getTierGroup(found));
    } else if (!row) {
      const sorted = [...tiers].sort(compareTierGroupLevel);
      const defaultTier =
        sorted.find((t) => getTierGroup(t) === DEFAULT_TIER_GROUP) ?? sorted[0];
      if (defaultTier) {
        setForm((prev) => {
          if (prev.tier_id) return prev;
          return { ...prev, tier_id: defaultTier.id };
        });
        setActiveGroup(getTierGroup(defaultTier));
      }
    }
  }, [visible, row, tiers]);

  useEffect(() => {
    if (!visible || currentTier || tierGroups.length === 0) return;
    if (!tierGroups.some(([group]) => group === activeGroup)) {
      setActiveGroup(tierGroups[0][0]);
    }
  }, [activeGroup, currentTier, tierGroups, visible]);

  const updateField = (key, value) => {
    setForm((prev) => ({ ...prev, [key]: value }));
  };

  const selectGroup = (group) => {
    setActiveGroup(group);
    const groupTiers = tierGroups.find(([g]) => g === group)?.[1] ?? [];
    updateField('tier_id', groupTiers[0]?.id ?? null);
  };

  const handleTierSelect = (tierId) => {
    updateField('tier_id', tierId || null);
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
          tier_id: toNumber(form.tier_id),
          status: toNumber(form.status, 1),
          remark: form.remark,
        });
      } else {
        await mutateRequest('post', '/api/admin/employee', {
          user_id: toNumber(form.user_id),
          tier_id: toNumber(form.tier_id),
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
      width={560}
      bodyStyle={{ height: 540, overflow: 'visible' }}
      footer={
        <Space>
          <Button onClick={onCancel}>{t('取消')}</Button>
          <Button
            type='primary'
            loading={saving}
            disabled={!isUpdate && !form.user_id}
            onClick={submit}
          >
            {t('保存')}
          </Button>
        </Space>
      }
    >
      {isUpdate ? (
        <SummaryPanel className='mb-4'>
          <SummaryItem label={t('员工')} value={employeeLabel} />
          <SummaryItem
            label={t('员工 UID')}
            value={`#${row?.user_id || '-'}`}
          />
          <SummaryItem label={t('客户数量')} value={row?.customer_count ?? 0} />
          <SummaryItem
            label={t('当前等级')}
            value={
              row?.current_tier_level
                ? `${t('等级')} ${row.current_tier_level} / ${
                    row.current_tier_group || t('通用')
                  }`
                : '-'
            }
          />
        </SummaryPanel>
      ) : null}
      {!isUpdate ? (
        <Field label={t('用户')}>
          <UserPicker
            value={form.user_id}
            onSelect={(user) => {
              updateField('user_id', user.id);
              updateField('remark', user.remark || '');
            }}
            onClear={() => {
              updateField('user_id', 0);
              updateField('remark', '');
            }}
            excludeEmployee
            excludeAssignedCustomer
          />
          <Text type='secondary' size='small'>
            {t('搜索并选择要设为员工的用户')}
          </Text>
        </Field>
      ) : null}
      <Field label={t('提成等级')}>
        {tiers.length === 0 ? (
          <Text type='secondary' size='small'>
            {t('暂无等级配置')}
          </Text>
        ) : (
          <>
            <div
              style={{
                display: 'flex',
                flexWrap: 'wrap',
                gap: 8,
                marginBottom: 8,
              }}
            >
              {tierGroups.map(([group]) => (
                <Tag
                  key={group}
                  size='large'
                  color={getTierGroupTagColor(group)}
                  type={activeGroup === group ? 'light' : 'ghost'}
                  style={{ cursor: 'pointer', userSelect: 'none' }}
                  onClick={() => selectGroup(group)}
                >
                  {group}
                </Tag>
              ))}
            </div>
            {activeGroup !== null &&
              (() => {
                const groupTiers =
                  tierGroups.find(([group]) => group === activeGroup)?.[1] ??
                  [];
                if (groupTiers.length === 0) return null;
                return (
                  <div
                    style={{
                      display: 'flex',
                      flexWrap: 'wrap',
                      gap: 8,
                      marginBottom: 8,
                    }}
                  >
                    {groupTiers.map((tier) => (
                      <Tag
                        key={tier.id}
                        size='large'
                        color={getTierLevelTagColor(tier.level)}
                        type={form.tier_id === tier.id ? 'light' : 'ghost'}
                        style={{ cursor: 'pointer', userSelect: 'none' }}
                        onClick={() => handleTierSelect(tier.id)}
                      >
                        {`等级 ${tier.level} ${(Number(tier.rate || 0) * 100).toFixed(1)}%`}
                      </Tag>
                    ))}
                  </div>
                );
              })()}
            {/* 选中信息 */}
            {currentTier && (
              <div
                style={{
                  background: 'var(--semi-color-fill-0)',
                  borderRadius: 6,
                  padding: '6px 10px',
                  fontSize: 12,
                  color: 'var(--semi-color-text-1)',
                  marginBottom: 4,
                }}
              >
                <Tag color={getTierLevelTagColor(currentTier.level)}>
                  {`等级 ${currentTier.level}`}
                </Tag>
                <Tag
                  color={getTierGroupTagColor(
                    currentTier.group || DEFAULT_TIER_GROUP,
                  )}
                >
                  {currentTier.group || DEFAULT_TIER_GROUP}
                </Tag>
                <Text size='small'>{`  ·  门槛 $${Number(currentTier.threshold_usd || 0).toFixed(2)}  ·  提成 ${(Number(currentTier.rate || 0) * 100).toFixed(1)}%`}</Text>
              </div>
            )}
          </>
        )}
        <Text type='secondary' size='small'>
          {t('提成比例和业绩目标由等级决定，请在「提成阶梯」中配置')}
        </Text>
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
        <TextArea
          value={form.remark}
          onChange={(value) => updateField('remark', value)}
          autosize={{ minRows: 3, maxRows: 5 }}
          placeholder={t('请输入备注（仅管理员可见）')}
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
  const [compactMode, setCompactMode] = useTableCompactMode('employees');
  const [modalRow, setModalRow] = useState(null);
  const [modalVisible, setModalVisible] = useState(false);
  const [assignModalRow, setAssignModalRow] = useState(null);
  const [assignModalVisible, setAssignModalVisible] = useState(false);
  const [customerListModalRow, setCustomerListModalRow] = useState(null);
  const [customerListModalVisible, setCustomerListModalVisible] =
    useState(false);
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
        user_id: filterForm.user_id ? toNumber(filterForm.user_id) : undefined,
        keyword: filterForm.keyword?.trim(),
        status: toNumber(filterForm.status) || undefined,
        sort_by: filters?.sort_by,
        sort_order: filters?.sort_order,
      }),
    );
  };

  const handleFilterSubmit = (event) => {
    event?.preventDefault();
    applyFilters();
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

  const openAssign = (row) => {
    setAssignModalRow(row);
    setAssignModalVisible(true);
  };

  const openCustomerList = (row) => {
    setCustomerListModalRow(row);
    setCustomerListModalVisible(true);
  };

  const closeModal = () => {
    setModalVisible(false);
    setModalRow(null);
  };

  const closeAssignModal = () => {
    setAssignModalVisible(false);
    setAssignModalRow(null);
  };

  const closeCustomerListModal = () => {
    setCustomerListModalVisible(false);
    setCustomerListModalRow(null);
  };

  const refreshAfterModal = () => {
    closeModal();
    employees.load();
  };

  const disableEmployee = (row) => {
    const employeeLabel =
      row?.username ||
      row?.display_name ||
      (row?.user_id ? `#${row.user_id}` : '-');
    Modal.confirm({
      title: t('确认禁用'),
      content: (
        <div className='space-y-3'>
          <Text>{t('禁用后历史提成记录会保留。')}</Text>
          <SummaryPanel danger>
            <SummaryItem label={t('员工')} value={employeeLabel} />
            <SummaryItem
              label={t('员工 UID')}
              value={`#${row?.user_id || '-'}`}
            />
            <SummaryItem
              label={t('客户数量')}
              value={row?.customer_count ?? 0}
            />
          </SummaryPanel>
        </div>
      ),
      okText: t('确认禁用'),
      cancelText: t('取消'),
      okType: 'danger',
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

  const filterStatusOptions = [
    { value: 0, label: t('全部') },
    { value: 1, label: t('启用') },
    { value: 2, label: t('禁用') },
  ];

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
      title: <span className='whitespace-nowrap'>{t('客户数量')}</span>,
      dataIndex: 'customer_count',
      width: 112,
      sorter: true,
      sortOrder: getSortOrder('customer_count'),
      render: (value, row) => (
        <Button
          size='small'
          type='tertiary'
          theme='borderless'
          icon={<Users size={14} />}
          className='inline-flex min-w-[48px] flex-nowrap items-center justify-center whitespace-nowrap tabular-nums'
          aria-label={t('查看该员工的客户')}
          onClick={(event) => {
            event?.stopPropagation?.();
            openCustomerList(row);
          }}
        >
          {value || 0}
        </Button>
      ),
    },
    {
      title: t('客户总消费'),
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
            <Space>
              <Tag color={getTierLevelTagColor(value)}>{`等级 ${value}`}</Tag>
              {row.current_tier_group ? (
                <Tag color={getTierGroupTagColor(row.current_tier_group)}>
                  {row.current_tier_group}
                </Tag>
              ) : null}
            </Space>
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
      title: t('业绩目标'),
      dataIndex: 'next_tier_threshold_usd',
      render: (value) => (value ? formatTargetAmount(value) : t('无限制')),
    },
    {
      title: t('当前业绩'),
      dataIndex: 'current_performance_quota',
      width: 130,
      sorter: true,
      sortOrder: getSortOrder('current_performance_quota'),
      render: (value, row) => renderPerformanceProgress(value, row, t),
    },
    {
      title: t('本期提成'),
      dataIndex: 'current_commission_quota',
      width: 130,
      sorter: true,
      sortOrder: getSortOrder('current_commission_quota'),
      render: (value, row) => (
        <div>
          <AmountText value={value || 0} />
          <div>
            <Text type='secondary' size='small'>
              {formatBusinessUsd(row.current_commission_usd)}
            </Text>
          </div>
        </div>
      ),
    },
    {
      title: t('状态'),
      dataIndex: 'status',
      sorter: true,
      sortOrder: getSortOrder('status'),
      render: (value) => <StatusTag status={value} />,
    },
    {
      title: t('备注'),
      dataIndex: 'remark',
      render: (value) => value || '-',
    },
    {
      title: t('操作'),
      key: 'operate',
      width: 150,
      fixed: 'right',
      render: (_, row) => (
        <Space spacing={4} className='flex-nowrap'>
          {Number(row.status) === 1 ? (
            <Button
              size='small'
              type='tertiary'
              theme='borderless'
              icon={<UserRoundPlus size={14} />}
              title={t('分配客户')}
              aria-label={t('分配客户')}
              onClick={() => openAssign(row)}
            />
          ) : null}
          <Button
            size='small'
            type='tertiary'
            theme='borderless'
            icon={<Users size={14} />}
            title={t('查看客户')}
            aria-label={t('查看客户')}
            onClick={() => openCustomerList(row)}
          />
          <Button
            size='small'
            type='tertiary'
            theme='borderless'
            icon={<Pencil size={14} />}
            title={t('编辑员工')}
            aria-label={t('编辑员工')}
            onClick={() => openEdit(row)}
          />
          {Number(row.status) === 1 ? (
            <Button
              size='small'
              type='danger'
              theme='borderless'
              icon={<Trash2 size={14} />}
              title={t('禁用员工')}
              aria-label={t('禁用员工')}
              onClick={() => disableEmployee(row)}
            />
          ) : null}
        </Space>
      ),
    },
  ];

  const tableColumns = useMemo(() => {
    if (!compactMode) return columns;
    return columns.map((col) => {
      if (col.key === 'operate' || col.dataIndex === 'operate') {
        const { fixed, ...rest } = col;
        return rest;
      }
      return col;
    });
  }, [compactMode, columns]);

  useEffect(() => {
    if (!onReadyToolbar) return undefined;
    onReadyToolbar(
      <>
        <CompactModeToggle
          compactMode={compactMode}
          setCompactMode={setCompactMode}
          t={t}
        />
        <Button
          type='tertiary'
          size='small'
          icon={<Plus size={14} />}
          onClick={openCreate}
        >
          {t('添加员工')}
        </Button>
      </>,
    );
    return () => onReadyToolbar(null);
  }, [compactMode, onReadyToolbar, openCreate, setCompactMode, t]);

  return (
    <>
      {onFiltersChange ? (
        <form
          className='mb-3 flex flex-wrap items-center gap-2'
          style={{ rowGap: 8 }}
          onSubmit={handleFilterSubmit}
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
          <div
            className='flex rounded-md border p-0.5'
            style={{ borderColor: 'var(--semi-color-border)' }}
          >
            {filterStatusOptions.map((option) => (
              <Button
                key={option.value}
                htmlType='button'
                size='small'
                type={
                  Number(filterForm.status) === option.value
                    ? 'primary'
                    : 'tertiary'
                }
                theme={
                  Number(filterForm.status) === option.value
                    ? 'solid'
                    : 'borderless'
                }
                onClick={() => updateFilter('status', option.value)}
              >
                {option.label}
              </Button>
            ))}
          </div>
          <Button
            size='small'
            type='primary'
            htmlType='submit'
            icon={<Search size={14} />}
          >
            {t('查询')}
          </Button>
          <Button
            size='small'
            type='tertiary'
            htmlType='button'
            icon={<X size={14} />}
            onClick={resetFilters}
          >
            {t('重置')}
          </Button>
          <Text type='secondary' size='small'>
            {t('共 {{count}} 名员工', { count: employees.total || 0 })}
          </Text>
        </form>
      ) : null}
      <ClassicBusinessTable
        rowKey='id'
        columns={tableColumns}
        dataSource={employees.items}
        loading={employees.loading}
        onChange={handleTableChange}
        rowClassName={(record) =>
          Number(record.status) === 2 ? 'opacity-60' : ''
        }
        scroll={compactMode ? null : { x: 'max-content' }}
        empty={<BusinessEmpty description={t('搜索无结果')} />}
      />
      <EmployeeModal
        visible={modalVisible}
        row={modalRow}
        onCancel={closeModal}
        onSuccess={refreshAfterModal}
      />
      <AssignCustomerModal
        visible={assignModalVisible}
        row={assignModalRow}
        onCancel={closeAssignModal}
        onSuccess={() => {
          closeAssignModal();
          employees.load();
        }}
      />
      <RemoveCustomersModal
        visible={customerListModalVisible}
        row={customerListModalRow}
        onCancel={closeCustomerListModal}
        onRefresh={() => employees.load()}
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
    {
      title: t('时间'),
      dataIndex: 'created_at',
      render: formatTs,
      width: 180,
    },
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
      title: selfView ? t('消耗') : t('收入'),
      dataIndex: 'revenue_quota',
      render: (value) => formatBusinessAmount(value),
    },
    ...(!selfView
      ? [
          {
            title: t('成本'),
            dataIndex: 'cost_quota',
            render: (value) => formatBusinessAmount(value),
          },
        ]
      : []),
    {
      title: selfView ? t('业绩') : t('利润'),
      dataIndex: 'profit_quota',
      render: (value) => <AmountText value={value} />,
    },
    {
      title: t('提成'),
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
      {onFiltersChange ? (
        <div
          className='mb-3 flex flex-wrap items-center gap-2'
          style={{ rowGap: 8 }}
        >
          {!selfView ? (
            <InputNumber
              size='small'
              min={0}
              hideButtons
              placeholder={t('员工 UID')}
              value={filterForm.employee_user_id}
              onChange={(value) => updateFilter('employee_user_id', value)}
              style={{ width: 120 }}
            />
          ) : null}
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

<<<<<<< Updated upstream
function TierModal({ visible, row, onCancel, onSuccess, tiers = [] }) {
=======
<<<<<<< Updated upstream
function TierModal({ visible, row, onCancel, onSuccess }) {
=======
function CommissionMonthlyCalendar({
  endpoint,
  selfView = false,
  embedded = false,
}) {
  const { t } = useTranslation();
  const [month, setMonth] = useState(currentMonthValue);
  const [employeeInput, setEmployeeInput] = useState(undefined);
  const [employeeUserId, setEmployeeUserId] = useState(undefined);
  const [selectedDate, setSelectedDate] = useState(undefined);
  const range = useMemo(() => monthValueToRange(month), [month]);
  const params = useMemo(
    () =>
      buildParams({
        ...range,
        page: 1,
        page_size: 100,
        employee_user_id: selfView ? undefined : employeeUserId,
      }),
    [employeeUserId, range, selfView],
  );
  const stats = useEndpointData(endpoint, params);

  const applyFilters = () => {
    const next = toNumber(employeeInput);
    setEmployeeUserId(Number.isFinite(next) && next > 0 ? next : undefined);
  };

  const resetFilters = () => {
    setEmployeeInput(undefined);
    setEmployeeUserId(undefined);
  };

  const periodItems = stats.data?.items || [];
  const summary = useMemo(
    () =>
      periodItems.reduce(
        (next, item) => ({
          revenue_quota: next.revenue_quota + (item.revenue_quota || 0),
          cost_quota: next.cost_quota + (item.cost_quota || 0),
          profit_quota: next.profit_quota + (item.profit_quota || 0),
          commission_quota:
            next.commission_quota + (item.commission_quota || 0),
          record_count: next.record_count + (item.record_count || 0),
        }),
        {
          revenue_quota: 0,
          cost_quota: 0,
          profit_quota: 0,
          commission_quota: 0,
          record_count: 0,
        },
      ),
    [periodItems],
  );

  const content = (
    <div className='space-y-4'>
      <div className='flex flex-wrap items-center justify-between gap-3 rounded bg-[var(--semi-color-fill-0)] p-3'>
        <Space wrap>
          <Button
            size='small'
            type='tertiary'
            icon={<ChevronLeft size={14} />}
            onClick={() => setMonth(shiftMonthValue(month, -1))}
          />
          <DatePicker
            type='month'
            size='small'
            value={new Date(`${month}-01T00:00:00`)}
            placeholder={t('统计月份')}
            onChange={(value) => {
              const date = value instanceof Date ? value : new Date(value);
              if (!Number.isNaN(date.getTime())) {
                setMonth(
                  `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}`,
                );
              }
            }}
            style={{ width: 150 }}
          />
          <Button
            size='small'
            type='tertiary'
            icon={<ChevronRight size={14} />}
            onClick={() => setMonth(shiftMonthValue(month, 1))}
          />
          <Button
            size='small'
            type={month === currentMonthValue() ? 'secondary' : 'tertiary'}
            onClick={() => setMonth(currentMonthValue())}
          >
            {t('今日')}
          </Button>
        </Space>
        {!selfView ? (
          <Space wrap>
            <InputNumber
              size='small'
              min={0}
              hideButtons
              placeholder={t('Employee UID')}
              value={employeeInput}
              onChange={setEmployeeInput}
              onKeyDown={(event) => {
                if (event.key === 'Enter') applyFilters();
              }}
              style={{ width: 140 }}
            />
            <Button size='small' type='primary' onClick={applyFilters}>
              {t('Search')}
            </Button>
            <Button size='small' type='tertiary' onClick={resetFilters}>
              {t('Reset')}
            </Button>
          </Space>
        ) : null}
      </div>
      <Row gutter={[12, 12]}>
        <Col xs={24} md={12}>
          <Card bodyStyle={{ minHeight: 112, padding: 16 }}>
            <div className='flex items-center gap-2'>
              <CalendarDays size={16} color='var(--semi-color-success)' />
              <Text type='secondary'>{monthLabel(month)}</Text>
            </div>
            <div className='mt-2 text-2xl font-semibold'>
              {stats.loading ? (
                <Spin size='small' />
              ) : (
                formatBusinessAmount(summary.commission_quota || 0)
              )}
            </div>
            <Text type='secondary' size='small'>
              {t('Monthly Commission')}
            </Text>
          </Card>
        </Col>
        <Col xs={12} md={6}>
          <Card bodyStyle={{ minHeight: 112, padding: 16 }}>
            <Text type='secondary' size='small'>
              {t('Monthly Performance')}
            </Text>
            <div className='mt-2 font-semibold'>
              {formatBusinessAmount(summary.profit_quota || 0)}
            </div>
          </Card>
        </Col>
        <Col xs={12} md={6}>
          <Card bodyStyle={{ minHeight: 112, padding: 16 }}>
            <Text type='secondary' size='small'>
              {t('Records')}
            </Text>
            <div className='mt-2 font-semibold'>
              {summary.record_count || 0}
            </div>
          </Card>
        </Col>
      </Row>
      <Row gutter={[12, 12]}>
        {periodItems.map((item) => (
          <Col xs={24} lg={12} key={`${item.period_start_at}-${item.employee_user_id}`}>
            <Card bodyStyle={{ minHeight: 156, padding: 16 }}>
              <div className='flex flex-wrap justify-between gap-3'>
                <div>
                  <Text strong>{item.period_key || t('Period')}</Text>
                  <div className='mt-1 text-xs text-[var(--semi-color-text-2)]'>
                    {formatTs(item.period_start_at)} - {formatTs(item.period_end_at)}
                  </div>
                </div>
                <div className='text-right'>
                  <div className='text-lg font-semibold'>
                    {formatBusinessAmount(item.commission_quota || 0)}
                  </div>
                  <Text type='secondary' size='small'>
                    {t('Commission')}
                  </Text>
                </div>
              </div>
              <Row gutter={[12, 12]} className='mt-3'>
                <Col span={6}>
                  <Text type='secondary' size='small'>{t('Profit')}</Text>
                  <div className='font-medium'>{formatBusinessAmount(item.profit_quota || 0)}</div>
                </Col>
                <Col span={6}>
                  <Text type='secondary' size='small'>{t('Revenue')}</Text>
                  <div className='font-medium'>{formatBusinessAmount(item.revenue_quota || 0)}</div>
                </Col>
                <Col span={6}>
                  <Text type='secondary' size='small'>{t('Cost')}</Text>
                  <div className='font-medium'>{formatBusinessAmount(item.cost_quota || 0)}</div>
                </Col>
                <Col span={6}>
                  <Text type='secondary' size='small'>{t('Records')}</Text>
                  <div className='font-medium'>{item.record_count || 0}</div>
                </Col>
              </Row>
              {!selfView ? (
                <Text type='tertiary' size='small' className='mt-3 block'>
                  {t('Employee UID')}: {item.employee_user_id}
                </Text>
              ) : null}
            </Card>
          </Col>
        ))}
      </Row>
      {!stats.loading && periodItems.length === 0 ? (
        <BusinessEmpty description={t('暂无数据')} />
      ) : null}
    </div>
  );

  if (embedded) {
    return content;
  }

  return (
    <BusinessCard
      title={t('月度统计')}
      icon={BadgeDollarSign}
      color='var(--semi-color-success)'
      t={t}
    >
      {content}
    </BusinessCard>
  );
}

function TierModal({ visible, row, onCancel, onSuccess, tiers = [] }) {
>>>>>>> Stashed changes
>>>>>>> Stashed changes
  const { t } = useTranslation();
  const isUpdate = Boolean(row);
  const [saving, setSaving] = useState(false);
  const [form, setForm] = useState({
    level: 1,
    group: '通用',
    threshold_usd: 0,
    rate: 0.1,
  });
  const [addingGroup, setAddingGroup] = useState(false);
  const [newGroupText, setNewGroupText] = useState('');
  const newGroupInputRef = useRef(null);

  const baseGroups = useMemo(() => {
    const seen = new Set(['通用']);
    tiers.forEach((tier) => {
      if (tier.group) seen.add(tier.group);
    });
    return Array.from(seen);
  }, [tiers]);

  const [extraGroups, setExtraGroups] = useState([]);

  const allGroups = useMemo(() => {
    const seen = new Set(baseGroups);
    extraGroups.forEach((g) => seen.add(g));
    if (form.group && !seen.has(form.group)) seen.add(form.group);
    return Array.from(seen);
  }, [baseGroups, extraGroups, form.group]);
  const normalizedGroup = (form.group || '').trim() || '通用';
  const commissionRate = toNumber(form.rate);
  const duplicateTier = useMemo(() => {
    const level = toNumber(form.level, 0);
    if (!level) return null;
    return tiers.find(
      (tier) =>
        Number(tier.id) !== Number(row?.id) &&
        toNumber(tier.level) === level &&
        ((tier.group || '通用').trim() || '通用') === normalizedGroup,
    );
  }, [form.level, normalizedGroup, row?.id, tiers]);

  useEffect(() => {
    if (!visible) return;
    setExtraGroups([]);
    setAddingGroup(false);
    setNewGroupText('');
    setForm({
      level: row?.level ?? 1,
      group: row?.group || '通用',
      threshold_usd: row?.threshold_usd ?? 0,
      rate: row?.rate ?? 0.1,
    });
  }, [visible, row]);

  useEffect(() => {
    if (addingGroup && newGroupInputRef.current) {
      newGroupInputRef.current.focus();
    }
  }, [addingGroup]);

  const confirmNewGroup = () => {
    const g = newGroupText.trim();
    if (g && !allGroups.includes(g)) {
      setExtraGroups((prev) => [...prev, g]);
    }
    if (g) {
      setForm((prev) => ({ ...prev, group: g }));
    }
    setAddingGroup(false);
    setNewGroupText('');
  };

  const updateField = (key, value) =>
    setForm((prev) => ({ ...prev, [key]: value }));

  const submit = async () => {
    if (!toNumber(form.level, 0) || form.level < 1) {
      showError(t('等级编号必须 >= 1'));
      return;
    }
    if (duplicateTier) {
      showError(t('已存在相同等级和分组的阶梯'));
      return;
    }
    if (commissionRate < 0 || commissionRate > 1) {
      showError(t('提成比例必须在 0 到 1 之间'));
      return;
    }
    setSaving(true);
    try {
      const body = {
        level: toNumber(form.level),
        group: normalizedGroup,
        threshold_usd: toNumber(form.threshold_usd),
        rate: commissionRate,
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
      width={560}
      bodyStyle={{ height: 560, overflowY: 'auto' }}
      footer={
        <Space>
          <Button onClick={onCancel}>{t('取消')}</Button>
          <Button
            type='primary'
            loading={saving}
            disabled={Boolean(duplicateTier)}
            onClick={submit}
          >
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
      <Field label={t('分组')}>
        <div
          style={{
            display: 'flex',
            flexWrap: 'wrap',
            gap: 8,
            alignItems: 'center',
          }}
        >
          {allGroups.map((g) => (
            <Tag
              key={g}
              size='large'
              color={getTierGroupTagColor(g)}
              type={form.group === g ? 'light' : 'ghost'}
              style={{ cursor: 'pointer', userSelect: 'none' }}
              onClick={() => updateField('group', g)}
            >
              {g}
            </Tag>
          ))}
          {addingGroup ? (
            <Input
              ref={newGroupInputRef}
              size='default'
              value={newGroupText}
              onChange={(v) => setNewGroupText(v)}
              onEnterPress={confirmNewGroup}
              onBlur={confirmNewGroup}
              placeholder={t('输入分组名')}
              style={{ width: 110 }}
            />
          ) : (
            <Tag
              size='large'
              color='blue'
              type='ghost'
              style={{ cursor: 'pointer', userSelect: 'none' }}
              onClick={() => setAddingGroup(true)}
            >
              + {t('新增')}
            </Tag>
          )}
        </div>
        <Text type='secondary' size='small' style={{ marginTop: 4 }}>
          {t(
            '同一等级可设置多个分组（如 "通用"、"VIP"），各分组提成比例独立，默认 "通用"',
          )}
        </Text>
        {duplicateTier ? (
          <Text type='danger' size='small' className='mt-2 block'>
            {t('已存在相同等级和分组的阶梯')}
          </Text>
        ) : null}
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
        <div className='mt-2 flex flex-wrap items-center gap-2'>
          <Text type='secondary' size='small'>
            {t('快速选择')}
          </Text>
          {COMMISSION_RATE_PRESETS.map((rate) => (
            <Tag
              key={rate}
              color={commissionRate === rate ? 'green' : 'grey'}
              style={{ cursor: 'pointer', userSelect: 'none' }}
              onClick={() => updateField('rate', rate)}
            >
              {formatPercent(rate)}
            </Tag>
          ))}
        </div>
      </Field>
      <Field label={t('生效预览')}>
        <SummaryPanel>
          <SummaryItem
            label={t('等级')}
            value={
              <Tag color={getTierLevelTagColor(form.level)}>
                {`${t('等级')} ${toNumber(form.level, 1)}`}
              </Tag>
            }
          />
          <SummaryItem
            label={t('分组')}
            value={
              <Tag color={getTierGroupTagColor(normalizedGroup)}>
                {normalizedGroup}
              </Tag>
            }
          />
          <SummaryItem
            label={t('业绩门槛 (USD)')}
            value={formatExactUsd(toNumber(form.threshold_usd))}
          />
          <SummaryItem
            label={t('提成比例')}
            value={formatPercent(commissionRate)}
          />
        </SummaryPanel>
      </Field>
    </Modal>
  );
}

<<<<<<< Updated upstream
function TiersTab({ tiersPaged, onReadyToolbar }) {
=======
<<<<<<< Updated upstream
function TiersTab({ onReadyToolbar }) {
=======
function TierResetSettingsCard({ onResetSuccess }) {
  const { t } = useTranslation();
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [resetting, setResetting] = useState(false);
  const [settingsVisible, setSettingsVisible] = useState(false);
  const [config, setConfig] = useState(null);
  const [form, setForm] = useState({
    enabled: false,
    reset_day: 10,
    timezone: 'Asia/Shanghai',
  });

  const loadConfig = useCallback(async () => {
    setLoading(true);
    try {
      const res = await API.get('/api/admin/employee/tiers/reset-config', {
        disableDuplicate: true,
      });
      const { success, message, data } = res.data;
      if (!success) {
        showError(message);
        return;
      }
      const nextConfig = data || {};
      setConfig(nextConfig);
      setForm({
        enabled: Boolean(nextConfig.enabled),
        reset_day: Number(nextConfig.reset_day || 10),
        timezone: normalizeResetTimezone(nextConfig.timezone),
      });
    } catch (error) {
      showError(error?.message || 'Request failed');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    loadConfig();
  }, [loadConfig]);

  const updateForm = (key, value) => {
    setForm((current) => ({
      ...current,
      [key]: value,
    }));
  };

  const isDirty =
    !!config &&
    (form.enabled !== Boolean(config.enabled) ||
      form.reset_day !== Number(config.reset_day || 10) ||
      form.timezone !== normalizeResetTimezone(config.timezone));

  const saveConfig = async () => {
    if (!config) return;
    setSaving(true);
    try {
      const updates = [
        ['commission_tier_reset_setting.enabled', String(form.enabled)],
        ['commission_tier_reset_setting.reset_day', String(form.reset_day)],
        ['commission_tier_reset_setting.reset_hour', '0'],
        ['commission_tier_reset_setting.reset_minute', '0'],
        ['commission_tier_reset_setting.reset_second', '0'],
        ['commission_tier_reset_setting.timezone', form.timezone],
      ];
      for (const [key, value] of updates) {
        const res = await API.put('/api/option/', { key, value });
        const { success, message } = res.data;
        if (!success) {
          throw new Error(message || 'Operation failed');
        }
      }
      showSuccess(t('设置已更新'));
      await loadConfig();
      setSettingsVisible(false);
    } catch (error) {
      showError(error?.message || 'Operation failed');
    } finally {
      setSaving(false);
    }
  };

  const resetNow = () => {
    Modal.confirm({
      title: t('确认立即重置'),
      content: t('将所有员工重置到最低等级？'),
      okText: t('立即重置'),
      cancelText: t('取消'),
      okType: 'danger',
      onOk: async () => {
        setResetting(true);
        try {
          const res = await API.post('/api/admin/employee/tiers/reset-now');
          const { success, message, data } = res.data;
          if (!success) {
            throw new Error(message || 'Operation failed');
          }
          showSuccess(
            t('重置完成，已处理 {{count}} 名员工', {
              count: data?.processed || 0,
            }),
          );
          await loadConfig();
          onResetSuccess?.();
        } catch (error) {
          showError(error?.message || 'Operation failed');
        } finally {
          setResetting(false);
        }
      },
    });
  };

  return (
    <>
      <Button
        type='tertiary'
        size='small'
        icon={<RefreshCw size={14} />}
        disabled={loading}
        onClick={() => setSettingsVisible(true)}
      >
        {t('月度重置')}
      </Button>

      <Modal
        title={t('等级与业绩月度重置设置')}
        visible={settingsVisible}
        onCancel={() => setSettingsVisible(false)}
        footer={
          <Space>
            <Button
              type='tertiary'
              icon={<RefreshCw size={14} />}
              loading={resetting}
              onClick={resetNow}
            >
              {t('立即重置')}
            </Button>
            <Button onClick={() => setSettingsVisible(false)}>
              {t('取消')}
            </Button>
            <Button
              type='primary'
              loading={saving}
              disabled={!isDirty}
              onClick={saveConfig}
            >
              {t('保存')}
            </Button>
          </Space>
        }
      >
        <div className='space-y-6 py-1'>
          <div className='flex flex-wrap items-center justify-between gap-3 pb-2'>
            <Text strong>{t('启用月度自动重置')}</Text>
            <Switch
              checked={form.enabled}
              onChange={(checked) => updateForm('enabled', checked)}
              disabled={loading || saving}
            />
          </div>

          <Row gutter={[12, 12]}>
            <Col xs={24} sm={12}>
              <Field label={t('每月重置日')}>
                <Select
                  value={form.reset_day}
                  onChange={(value) =>
                    updateForm('reset_day', Math.trunc(Number(value) || 1))
                  }
                  className='w-full'
                >
                  {RESET_DAY_OPTIONS.map((day) => (
                    <Select.Option key={day} value={day}>
                      {day}
                    </Select.Option>
                  ))}
                </Select>
              </Field>
            </Col>
            <Col xs={24} sm={12}>
              <Field label={t('时区')}>
                <Select
                  value={form.timezone}
                  onChange={(value) => updateForm('timezone', value || 'Local')}
                  className='w-full'
                >
                  {RESET_TIMEZONES.map((timezone) => (
                    <Select.Option key={timezone} value={timezone}>
                      {timezone === 'Local' ? t('服务器时区') : t('中国时区')}
                    </Select.Option>
                  ))}
                </Select>
              </Field>
            </Col>
          </Row>
          <Row gutter={[12, 12]}>
            <Col xs={24} sm={12}>
              <Text type='secondary'>
                {t('上次重置时间')}: {formatTs(config?.last_reset_at)}
              </Text>
            </Col>
            <Col xs={24} sm={12}>
              <Text type='secondary'>
                {t('下次重置时间')}: {formatTs(config?.next_reset_at)}
              </Text>
            </Col>
          </Row>
        </div>
      </Modal>
    </>
  );
}

function TiersTab({ tiersPaged, onReadyToolbar }) {
>>>>>>> Stashed changes
>>>>>>> Stashed changes
  const { t } = useTranslation();
  const [allTiers, setAllTiers] = useState([]);
  const [modalRow, setModalRow] = useState(null);
  const [modalVisible, setModalVisible] = useState(false);

  const loadAllTiers = useCallback(async () => {
    try {
      const res = await API.get('/api/admin/employee/tiers', {
        disableDuplicate: true,
      });
      const { success, message, data } = res.data;
      if (success) {
        setAllTiers(data || []);
      } else {
        showError(message);
      }
    } catch (error) {
      showError(error?.message || 'Request failed');
    }
  }, []);

  useEffect(() => {
    loadAllTiers();
  }, [loadAllTiers]);

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
    tiersPaged.load();
    loadAllTiers();
  };

  const deleteTier = (row) => {
    Modal.confirm({
      title: t('确认删除'),
      content: (
        <div className='space-y-3'>
          <Text>
            {t(
              '删除后已处于该等级的员工不会自动降级，但下次升级判断时会使用新配置。',
            )}
          </Text>
          <SummaryPanel danger>
            <SummaryItem
              label={t('等级')}
              value={
                <Tag color={getTierLevelTagColor(row.level)}>
                  {`${t('等级')} ${row.level}`}
                </Tag>
              }
            />
            <SummaryItem
              label={t('分组')}
              value={
                <Tag
                  color={getTierGroupTagColor(row.group || DEFAULT_TIER_GROUP)}
                >
                  {row.group || DEFAULT_TIER_GROUP}
                </Tag>
              }
            />
            <SummaryItem
              label={t('业绩门槛 (USD)')}
              value={formatExactUsd(Number(row.threshold_usd || 0))}
            />
            <SummaryItem
              label={t('提成比例')}
              value={formatPercent(row.rate)}
            />
          </SummaryPanel>
        </div>
      ),
      okText: t('确认删除'),
      cancelText: t('取消'),
      okType: 'danger',
      onOk: async () => {
        try {
          await mutateRequest('delete', `/api/admin/employee/tiers/${row.id}`);
          showSuccess(t('等级已删除'));
          tiersPaged.load();
          loadAllTiers();
        } catch (error) {
          showError(error.message);
        }
      },
    });
  };

  const columns = [
    {
      title: t('分组'),
      dataIndex: 'group',
      width: 100,
      render: (value) => (
        <Tag color={getTierGroupTagColor(value || DEFAULT_TIER_GROUP)}>
          {value || DEFAULT_TIER_GROUP}
        </Tag>
      ),
    },
    {
      title: t('等级'),
      dataIndex: 'level',
      width: 120,
      render: (value) => (
        <Tag color={getTierLevelTagColor(value)}>{`等级 ${value}`}</Tag>
      ),
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
      key: 'operate',
      width: 90,
      fixed: 'right',
      render: (_, row) => (
        <Space spacing={4} className='flex-nowrap'>
          <Button
            size='small'
            type='tertiary'
            theme='borderless'
            icon={<Pencil size={14} />}
            title={t('编辑等级')}
            aria-label={t('编辑等级')}
            onClick={() => openEdit(row)}
          />
          <Button
            size='small'
            type='danger'
            theme='borderless'
            icon={<Trash2 size={14} />}
            title={t('删除等级')}
            aria-label={t('删除等级')}
            onClick={() => deleteTier(row)}
          />
        </Space>
      ),
    },
  ];

  const handleTierResetSuccess = useCallback(() => {
    tiersPaged.load();
    loadAllTiers();
  }, [tiersPaged.load, loadAllTiers]);

  useEffect(() => {
    if (!onReadyToolbar) return undefined;
    onReadyToolbar(
      <Space>
        <TierResetSettingsCard onResetSuccess={handleTierResetSuccess} />
        <Button
          type='tertiary'
          size='small'
          icon={<Plus size={14} />}
          onClick={openCreate}
        >
          {t('添加等级')}
        </Button>
      </Space>,
    );
    return () => onReadyToolbar(null);
  }, [onReadyToolbar, t, openCreate, handleTierResetSuccess]);

  return (
    <>
      <ClassicBusinessTable
        rowKey='id'
        columns={columns}
        dataSource={tiersPaged.items}
        loading={tiersPaged.loading}
        empty={
          <BusinessEmpty
            description={t('暂无等级配置，点击「添加等级」创建')}
          />
        }
      />
      <TierModal
        visible={modalVisible}
        row={modalRow}
        tiers={allTiers}
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
  const tiers = usePagedEndpoint(
    '/api/admin/employee/tiers',
    {},
    { enabled: activeTab === 'tiers' },
  );
  const commissionLogs = usePagedEndpoint(
    '/api/admin/employee/commission',
    commissionLogFilters,
    { enabled: activeTab === 'logs' },
  );
  const tabs = [
    { key: 'monthly', label: t('月度统计') },
    { key: 'employees', label: t('员工') },
    { key: 'tiers', label: t('提成阶梯') },
    { key: 'logs', label: t('提成记录') },
  ];

  const pagination =
    activeTab === 'logs' ? (
      <ClassicPagination paged={commissionLogs} t={t} />
    ) : activeTab === 'employees' ? (
      <ClassicPagination paged={employees} t={t} />
    ) : activeTab === 'tiers' ? (
      <ClassicPagination paged={tiers} t={t} />
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
<<<<<<< Updated upstream
          <TiersTab tiersPaged={tiers} onReadyToolbar={setTabToolbar} />
=======
<<<<<<< Updated upstream
          <TiersTab onReadyToolbar={setTabToolbar} />
=======
          <TiersTab tiersPaged={tiers} onReadyToolbar={setTabToolbar} />
        ) : activeTab === 'monthly' ? (
          <CommissionMonthlyCalendar
            endpoint='/api/admin/employee/commission/monthly'
            embedded
          />
>>>>>>> Stashed changes
>>>>>>> Stashed changes
        ) : (
          <CommissionLogsTable
            endpoint='/api/admin/employee/commission'
            logs={commissionLogs}
            filters={commissionLogFilters}
            onFiltersChange={setCommissionLogFilters}
            title={t('提成记录')}
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
<<<<<<< Updated upstream
  const [commissionLogFilters, setCommissionLogFilters] = useState({});
=======
<<<<<<< Updated upstream
=======
  const [commissionLogFilters, setCommissionLogFilters] = useState({});
  const [activeConsoleTab, setActiveConsoleTab] = useState('details');
>>>>>>> Stashed changes
>>>>>>> Stashed changes
  const commissionLogs = usePagedEndpoint(
    '/api/user/employee/commission',
    commissionLogFilters,
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
<<<<<<< Updated upstream
  const tierInfo = profileData?.data?.tier;
  const effectiveRate = tierInfo?.tier_rate ?? profile?.commission_rate ?? 0;
  const tierGroup = tierInfo?.tier_group || '';
=======
<<<<<<< Updated upstream
=======
  const tierInfo = profileData?.data?.tier;
  const effectiveRate = tierInfo?.tier_rate ?? 0;
  const tierGroup = tierInfo?.tier_group || '';
>>>>>>> Stashed changes
>>>>>>> Stashed changes
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
  const targetAmount = Number(
    tierInfo?.tier_threshold_usd || profile?.target_amount || 0,
  );

  return (
    <PageShell>
      <Spin spinning={loading}>
        {!loading && (!profileData?.success || !profile) ? (
          <BusinessCard
            title={t('我的提成')}
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
              title={t('我的提成')}
              icon={BadgeDollarSign}
              color='var(--semi-color-success)'
              pagination={
                activeConsoleTab === 'details' ? (
                  <ClassicPagination paged={commissionLogs} t={t} />
                ) : null
              }
              searchArea={
                <div className='flex flex-col md:flex-row justify-between items-start md:items-center gap-2 w-full'>
                  <div className='flex flex-wrap gap-2'>
                    {[
                      ['monthly', t('月度统计')],
                      ['details', t('提成明细')],
                    ].map(([key, label]) => (
                      <Button
                        key={key}
                        size='small'
                        type={activeConsoleTab === key ? 'primary' : 'tertiary'}
                        theme={activeConsoleTab === key ? 'solid' : 'light'}
                        onClick={() => setActiveConsoleTab(key)}
                      >
                        {label}
                      </Button>
                    ))}
                  </div>
                  <div />
                </div>
              }
              t={t}
            >
<<<<<<< Updated upstream
              <Row gutter={[16, 16]}>
                <Col xs={24} md={12} xl={6}>
                  <StatCard
                    title={t('总消费')}
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
                    value={
                      <span>
                        {formatPercent(effectiveRate)}
                        {tierGroup ? (
                          <Tag size='small' style={{ marginLeft: 6 }}>
                            {tierGroup}
                          </Tag>
                        ) : null}
                      </span>
                    }
                    sub={
                      targetAmount
                        ? `${t('业绩')}: $${(totalProfitUsd ?? 0).toFixed(2)} / ${formatTargetAmount(targetAmount)}${(totalProfitUsd ?? 0) >= targetAmount ? ' done' : ''}`
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
                  <Text strong>{t('提成明细')}</Text>
                </div>
                <CommissionLogsTable
                  endpoint='/api/user/employee/commission'
                  selfView
                  logs={commissionLogs}
<<<<<<< Updated upstream
                  filters={commissionLogFilters}
                  onFiltersChange={setCommissionLogFilters}
=======
=======
              {activeConsoleTab === 'monthly' ? (
                <CommissionMonthlyCalendar
                  endpoint='/api/user/employee/commission/monthly'
                  selfView
>>>>>>> Stashed changes
>>>>>>> Stashed changes
                  embedded
                />
              ) : (
                <div className='space-y-4'>
                  <Row gutter={[16, 16]}>
                    <Col xs={24} md={12} xl={6}>
                      <StatCard
                        title={t('总消费')}
                        value={formatBusinessAmount(
                          customerTotalConsumptionQuota,
                        )}
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
                        value={
                          <span>
                            {formatPercent(effectiveRate)}
                            {tierGroup ? (
                              <Tag size='small' style={{ marginLeft: 6 }}>
                                {tierGroup}
                              </Tag>
                            ) : null}
                          </span>
                        }
                        sub={
                          targetAmount
                            ? `${t('业绩')}: $${(totalProfitUsd ?? 0).toFixed(2)} / ${formatTargetAmount(targetAmount)}${(totalProfitUsd ?? 0) >= targetAmount ? ' done' : ''}`
                            : `${t('业绩目标')}: ${t('无限制')}`
                        }
                        icon={Wallet}
                      />
                    </Col>
                  </Row>
                  <CommissionLogsTable
                    endpoint='/api/user/employee/commission'
                    selfView
                    logs={commissionLogs}
                    filters={commissionLogFilters}
                    onFiltersChange={setCommissionLogFilters}
                    embedded
                    showInlinePagination={false}
                  />
                </div>
              )}
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
  const [customerFilters, setCustomerFilters] = useState({});
  const [filterForm, setFilterForm] = useState({
    customer_user_id: undefined,
    keyword: '',
    status: undefined,
  });
  const customers = usePagedEndpoint(
    '/api/user/employee/customers',
    customerFilters,
  );
  const [createVisible, setCreateVisible] = useState(false);
  const [editRow, setEditRow] = useState(null);

  const updateFilter = (key, value) => {
    setFilterForm((form) => ({ ...form, [key]: value }));
  };

  const applyFilters = () => {
    setCustomerFilters(
      buildParams({
        customer_user_id: toNumber(filterForm.customer_user_id),
        keyword: filterForm.keyword?.trim(),
        status: toNumber(filterForm.status),
      }),
    );
  };

  const resetFilters = () => {
    setFilterForm({
      customer_user_id: undefined,
      keyword: '',
      status: undefined,
    });
    setCustomerFilters({});
  };

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
      title: t('提成'),
      dataIndex: 'commission_quota',
      render: (value) => (value ? <AmountText value={value} /> : '-'),
    },
    {
      title: t('状态'),
      dataIndex: 'status',
      render: (value) => <StatusTag status={value} />,
    },
    {
      title: t('备注'),
      dataIndex: 'remark',
      render: (value) => value || '-',
    },
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
          <div className='flex flex-wrap justify-end gap-2'>
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
              placeholder={t('用户名/ 邮箱 / 备注')}
              value={filterForm.keyword}
              onChange={(value) => updateFilter('keyword', value)}
              style={{ width: 180 }}
            />
            <Select
              size='small'
              placeholder={t('全部状态')}
              value={filterForm.status}
              onChange={(value) => updateFilter('status', value)}
              style={{ width: 110 }}
            >
              <Select.Option value={1}>{t('启用')}</Select.Option>
              <Select.Option value={2}>{t('禁用')}</Select.Option>
            </Select>
            <Button size='small' type='primary' onClick={applyFilters}>
              {t('查询')}
            </Button>
            <Button size='small' type='tertiary' onClick={resetFilters}>
              {t('重置')}
            </Button>
            <Button
              type='tertiary'
              size='small'
              icon={<Plus size={14} />}
              onClick={() => setCreateVisible(true)}
            >
              {t('添加客户')}
            </Button>
          </div>
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
  const [channelPage, setChannelPage] = useState(1);
  const [employeePage, setEmployeePage] = useState(1);
  const [channelNameFilter, setChannelNameFilter] = useState('');
  const [loading, setLoading] = useState(false);
  const [data, setData] = useState(null);
  const [loadedChannelRows, setLoadedChannelRows] = useState([]);
  const [loadedEmployeeRows, setLoadedEmployeeRows] = useState([]);
  const channelRowsByPageRef = useRef(new Map());
  const employeeRowsByPageRef = useRef(new Map());
  const selectedRange = useMemo(
    () => (range === 'custom' ? customRange : getPresetRange(range)),
    [customRange, range],
  );
  const rangeKey = useMemo(
    () =>
      [
        range,
        selectedRange.start?.getTime?.() || '',
        selectedRange.end?.getTime?.() || '',
      ].join('|'),
    [range, selectedRange],
  );
  const channelMergeScope = useMemo(
    () => [rangeKey, channelNameFilter.trim()].join('|'),
    [channelNameFilter, rangeKey],
  );
  const employeeMergeScope = rangeKey;
  const params = useMemo(
    () => ({
      ...rangeToParams(selectedRange),
      channel_page: channelPage,
      channel_page_size: PAGE_SIZE,
      employee_page: employeePage,
      employee_page_size: EMPLOYEE_PERFORMANCE_TOP_LIMIT,
      ...(channelNameFilter.trim()
        ? { channel_keyword: channelNameFilter.trim() }
        : {}),
    }),
    [selectedRange, channelPage, employeePage, channelNameFilter],
  );
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

  useEffect(() => {
    setChannelPage(1);
    channelRowsByPageRef.current.clear();
    setLoadedChannelRows([]);
  }, [channelNameFilter, selectedRange]);

  useEffect(() => {
    setEmployeePage(1);
    employeeRowsByPageRef.current.clear();
    setLoadedEmployeeRows([]);
  }, [selectedRange]);

  const platform = data?.platform || {};
  const commission = data?.commission || {};
  const channelProfitRows = data?.by_channel_platform || [];
  const [backfilling, setBackfilling] = useState(false);
  const [backfillStarted, setBackfillStarted] = useState(() => {
    if (typeof window === 'undefined') return false;
    return (
      window.localStorage.getItem(BUSINESS_STATS_BACKFILL_RUNNING_KEY) ===
      'true'
    );
  });
  const showBackfill = Boolean(data?.needs_backfill);
  const showBackfillRunning = showBackfill && (backfilling || backfillStarted);

  useEffect(() => {
    if (!data || data.needs_backfill) return;
    setBackfillStarted(false);
    if (typeof window !== 'undefined') {
      window.localStorage.removeItem(BUSINESS_STATS_BACKFILL_RUNNING_KEY);
    }
  }, [data?.needs_backfill]);

  const handleBackfill = useCallback(async () => {
    setBackfilling(true);
    try {
      await API.post('/api/admin/employee/overview/backfill');
      setBackfillStarted(true);
      if (typeof window !== 'undefined') {
        window.localStorage.setItem(
          BUSINESS_STATS_BACKFILL_RUNNING_KEY,
          'true',
        );
      }
      showSuccess(t('正在回填历史数据'));
    } catch (error) {
      setBackfillStarted(false);
      if (typeof window !== 'undefined') {
        window.localStorage.removeItem(BUSINESS_STATS_BACKFILL_RUNNING_KEY);
      }
      showError(error?.message || 'Request failed');
    } finally {
      setBackfilling(false);
    }
  }, [t]);
  const employeeRows = data?.by_employee || [];
  const channelTotal =
    data?.by_channel_platform_total || channelProfitRows.length;
  const hasMoreChannelRows = loadedChannelRows.length < channelTotal;

  useEffect(() => {
    if (!data) return;
    const fetchedChannelPage = data.by_channel_platform_page || channelPage;
    channelRowsByPageRef.current.set(
      `${channelMergeScope}|${fetchedChannelPage}`,
      channelProfitRows,
    );
    const nextRows = [];
    for (let page = 1; ; page += 1) {
      const rows = channelRowsByPageRef.current.get(
        `${channelMergeScope}|${page}`,
      );
      if (!rows) break;
      nextRows.push(...rows);
    }
    setLoadedChannelRows(nextRows);
  }, [channelMergeScope, channelPage, channelProfitRows, data]);

  useEffect(() => {
    if (!data) return;
    const fetchedEmployeePage = data.by_employee_page || employeePage;
    employeeRowsByPageRef.current.set(
      `${employeeMergeScope}|${fetchedEmployeePage}`,
      employeeRows,
    );
    const nextRows = [];
    for (let page = 1; ; page += 1) {
      const rows = employeeRowsByPageRef.current.get(
        `${employeeMergeScope}|${page}`,
      );
      if (!rows) break;
      nextRows.push(...rows);
    }
    setLoadedEmployeeRows(nextRows);
  }, [employeeMergeScope, employeePage, employeeRows, data]);

  const loadMoreChannels = useCallback(() => {
    if (loading || !hasMoreChannelRows) return;
    setChannelPage((page) => page + 1);
  }, [hasMoreChannelRows, loading]);

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
        row.display_name || value || `#${row.employee_user_id}`,
    },
    {
      title: t('客户消费'),
      dataIndex: 'total_revenue',
      render: (value) => formatBusinessAmount(value),
    },
    {
      title: t('客户成本'),
      dataIndex: 'total_cost',
      render: (value) => formatBusinessAmount(value),
    },
    {
      title: t('客户利润'),
      dataIndex: 'total_profit',
      render: (value) => <AmountText value={value} />,
    },
    {
      title: t('提成'),
      dataIndex: 'total_commission',
      render: (value) => <AmountText value={value} />,
    },
    { title: t('记录数'), dataIndex: 'record_count' },
  ];

  const channelProfitColumns = [
    {
      title: t('渠道'),
      dataIndex: 'channel_name',
      sorter: (a, b) =>
        (a.channel_name ?? '').localeCompare(b.channel_name ?? ''),
      render: (value, row) =>
        value || t('已删除渠道 #{{id}}', { id: row.channel_id }),
    },
    {
      title: t('成本比例'),
      dataIndex: 'cost_ratio',
      sorter: (a, b) => a.cost_ratio - b.cost_ratio,
    },
    {
      title: t('总消费'),
      dataIndex: 'consumption_quota',
      sorter: (a, b) => a.consumption_quota - b.consumption_quota,
      render: (value) => formatBusinessAmount(value),
    },
    {
      title: t('估算成本'),
      dataIndex: 'est_cost_quota',
      sorter: (a, b) => a.est_cost_quota - b.est_cost_quota,
      render: (value) => formatBusinessAmount(value),
    },
    {
      title: t('估算利润'),
      dataIndex: 'est_profit_quota',
      sorter: (a, b) => a.est_profit_quota - b.est_profit_quota,
      render: (value) => <AmountText value={value} />,
    },
    {
      title: t('毛利率'),
      dataIndex: 'est_gross_margin',
      sorter: (a, b) => a.est_gross_margin - b.est_gross_margin,
      render: (value) => formatPercent(value),
    },
  ];

  return (
    <div className='business-overview-shell mt-[60px] px-2'>
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
            <Button
              size='small'
              type='tertiary'
              icon={<RefreshCw size={14} />}
              loading={loading}
              onClick={loadOverview}
            >
              {t('刷新')}
            </Button>
          </Space>
        }
        t={t}
      >
        <Spin spinning={loading}>
          <div className='flex flex-col gap-4'>
            {showBackfill ? (
              <div
                className='flex items-center gap-3 rounded-xl px-4 py-3'
                style={{
                  border: '1px solid var(--semi-color-warning)',
                  background: 'var(--semi-color-warning-light-default)',
                }}
              >
                <Text className='flex-1' size='small'>
                  {showBackfillRunning
                    ? t('正在回填历史数据')
                    : t(
                        '检测到历史成本数据尚未迁移，迁移后可获得更准确的统计。此操作仅需执行一次。',
                      )}
                </Text>
                {!showBackfillRunning ? (
                  <Button
                    size='small'
                    type='warning'
                    theme='solid'
                    onClick={handleBackfill}
                  >
                    {t('立即迁移')}
                  </Button>
                ) : null}
              </div>
            ) : null}
            <Text strong type='secondary'>
              {t('平台范围（所有用户）')}
            </Text>
            <Row gutter={[16, 16]}>
              <Col xs={24} md={12} xl={4}>
                <StatCard
                  title={t('总消费')}
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
                <Card
                  className='!rounded-2xl border-0'
                  bodyStyle={{ padding: 16 }}
                  style={{ height: '100%' }}
                >
                  <div
                    style={{
                      display: 'grid',
                      gridTemplateColumns: '1fr 1fr',
                      gap: 8,
                    }}
                  >
                    <div>
                      <div className='flex items-center justify-between gap-3'>
                        <Text type='secondary' size='small'>
                          {t('盈利渠道')}
                        </Text>
                        <TrendingUp
                          size={18}
                          color='var(--semi-color-success)'
                        />
                      </div>
                      <div className='mt-2 text-2xl font-semibold'>
                        {platform.profitable_channel_count || 0}
                      </div>
                      <div
                        className='mt-1 text-xs'
                        style={{ minHeight: 16, visibility: 'hidden' }}
                      >
                        -
                      </div>
                    </div>
                    <div>
                      <div className='flex items-center justify-between gap-3'>
                        <Text type='secondary' size='small'>
                          {t('亏损渠道')}
                        </Text>
                        <TrendingDown
                          size={18}
                          color='var(--semi-color-danger)'
                        />
                      </div>
                      <div className='mt-2 text-2xl font-semibold'>
                        {platform.loss_channel_count || 0}
                      </div>
                      <div
                        className='mt-1 text-xs'
                        style={{ minHeight: 16, visibility: 'hidden' }}
                      >
                        -
                      </div>
                    </div>
                  </div>
                </Card>
              </Col>
            </Row>
            <Text type='secondary' size='small'>
              {t('成本按交易精确记录。启用此功能前生成的数据没有成本记录。')}
            </Text>

            <BusinessSection
              title={t('渠道盈利（全平台）')}
              description={t('成本和利润按分组倍率与渠道成本比例估算。')}
            >
              <div className='mb-3 flex items-center gap-2'>
                <Input
                  size='small'
                  prefix={<Search size={14} />}
                  placeholder={t('搜索渠道名称')}
                  value={channelNameFilter}
                  onChange={setChannelNameFilter}
                  style={{ width: 200 }}
                  showClear
                />
              </div>
              <ClassicBusinessTable
                rowKey='channel_id'
                columns={channelProfitColumns}
                dataSource={loadedChannelRows}
                wrapperClassName='business-channel-profit-table pr-1'
                scroll={{ x: '100%', y: 223 }}
                hasMore={hasMoreChannelRows}
                onLoadMore={loadMoreChannels}
                empty={<BusinessEmpty description={t('搜索无结果')} />}
              />
            </BusinessSection>

            <Text strong type='secondary'>
              {t('员工归属业绩')}
            </Text>
            <div className='grid grid-cols-1 md:grid-cols-2 xl:grid-cols-5 gap-4'>
              <div>
                <StatCard
                  title={t('客户消费')}
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
                  title={t('客户成本')}
                  value={formatBusinessAmount(commission.total_cost_quota || 0)}
                  sub={formatBusinessUsd(commission.total_cost_usd)}
                  icon={BriefcaseBusiness}
                  color='var(--semi-color-warning)'
                />
              </div>
              <div>
                <StatCard
                  title={t('客户利润')}
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
                  title={t('提成总额')}
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

            <BusinessSection title={t('员工业绩 Top 10')}>
              <ClassicBusinessTable
                rowKey='employee_user_id'
                columns={employeeColumns}
                dataSource={loadedEmployeeRows}
                wrapperClassName='business-employee-table pr-1'
                scroll={{ x: '100%', y: 223 }}
                empty={<BusinessEmpty description={t('搜索无结果')} />}
              />
            </BusinessSection>
          </div>
        </Spin>
      </BusinessCard>
    </div>
  );
}

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
import React, { useState, useEffect, useMemo } from 'react';
import {
  Modal,
  Table,
  Badge,
  Typography,
  Toast,
  Empty,
  Button,
  Input,
  Tag,
  Select,
  DatePicker,
} from '@douyinfe/semi-ui';
import {
  IllustrationNoResult,
  IllustrationNoResultDark,
} from '@douyinfe/semi-illustrations';
import { Coins } from 'lucide-react';
import { IconSearch, IconDownload, IconRefresh } from '@douyinfe/semi-icons';
import {
  API,
  createCardProPagination,
  renderQuota,
  timestamp2string,
} from '../../../helpers';
import { isAdmin } from '../../../helpers/utils';
import { useIsMobile } from '../../../hooks/common/useIsMobile';
const { Text } = Typography;

const STATUS_CONFIG = {
  success: { type: 'success', key: '成功' },
  pending: { type: 'warning', key: '待支付' },
  failed: { type: 'danger', key: '失败' },
  expired: { type: 'danger', key: '已过期' },
};

const PAYMENT_METHOD_MAP = {
  stripe: 'Stripe',
  creem: 'Creem',
  waffo: 'Waffo',
  waffo_pancake: 'Waffo',
  alipay: '支付宝',
  alipay_official: '支付宝',
  wxpay: '微信',
  wechat_official: '微信',
  balance: '余额',
  infini: 'Infini',
};

const PAYMENT_METHOD_FILTER_OPTIONS = [
  'stripe',
  'creem',
  'waffo',
  'alipay_official',
  'wechat_official',
  'infini',
];

const CURRENCY_SYMBOLS = {
  USD: '$',
  CNY: '¥',
  EUR: '€',
  GBP: '£',
  JPY: '¥',
  HKD: 'HK$',
  AUD: 'A$',
  CAD: 'C$',
  SGD: 'S$',
};

const EMPTY_FILTERS = {
  keyword: '',
  startTime: 0,
  endTime: 0,
  status: '',
  paymentMethod: '',
  userId: '',
};

function normalizeFilters(filters) {
  return {
    keyword: (filters.keyword || '').trim(),
    startTime: Number(filters.startTime) || 0,
    endTime: Number(filters.endTime) || 0,
    status: filters.status || '',
    paymentMethod: filters.paymentMethod || '',
    userId: String(filters.userId || '').trim(),
  };
}

function hasFilters(filters) {
  const normalized = normalizeFilters(filters);
  return Boolean(
    normalized.keyword ||
      normalized.startTime ||
      normalized.endTime ||
      normalized.status ||
      normalized.paymentMethod ||
      normalized.userId
  );
}

const TopupHistoryModal = ({
  visible,
  onCancel,
  t,
  enabledPaymentMethods,
  userExportEnabled = true,
}) => {
  const [loading, setLoading] = useState(false);
  const [exporting, setExporting] = useState(false);
  const [topups, setTopups] = useState([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(10);
  const [keyword, setKeyword] = useState('');
  const [startTime, setStartTime] = useState(0);
  const [endTime, setEndTime] = useState(0);
  const [status, setStatus] = useState('');
  const [paymentMethod, setPaymentMethod] = useState('');
  const [userId, setUserId] = useState('');
  const [appliedFilters, setAppliedFilters] = useState(EMPTY_FILTERS);
  const isMobile = useIsMobile();
  const userIsAdmin = useMemo(() => isAdmin(), []);

  const paymentMethodOptions = Array.isArray(enabledPaymentMethods)
    ? enabledPaymentMethods
    : PAYMENT_METHOD_FILTER_OPTIONS;

  // Build query params shared by list loading and export.
  const buildFilterQuery = (source = appliedFilters) => {
    const normalized = normalizeFilters(source);
    const params = new URLSearchParams();
    if (normalized.keyword) params.append('keyword', normalized.keyword);
    if (normalized.startTime) {
      params.append('start_time', String(normalized.startTime));
    }
    if (normalized.endTime) params.append('end_time', String(normalized.endTime));
    if (normalized.status) params.append('status', normalized.status);
    if (normalized.paymentMethod) {
      params.append('payment_method', normalized.paymentMethod);
    }
    if (userIsAdmin && normalized.userId) {
      params.append('user_id', String(normalized.userId));
    }
    return params;
  };

  const fetchTopupPage = async (currentPage, currentPageSize) => {
    const base = userIsAdmin ? '/api/user/topup' : '/api/user/topup/self';
    const params = buildFilterQuery();
    params.append('page_size', String(currentPageSize));
    params.append('p', String(currentPage));
    return API.get(`${base}?${params.toString()}`);
  };

  const loadTopups = async (currentPage, currentPageSize) => {
    setLoading(true);
    try {
      const res = await fetchTopupPage(currentPage, currentPageSize);
      const { success, message, data } = res.data;
      if (success) {
        setTopups(data.items || []);
        setTotal(data.total || 0);
      } else {
        Toast.error({ content: message || t('加载失败') });
      }
    } catch (error) {
      Toast.error({ content: t('加载账单失败') });
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    if (visible) {
      loadTopups(page, pageSize);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [
    visible,
    page,
    pageSize,
    appliedFilters.keyword,
    appliedFilters.startTime,
    appliedFilters.endTime,
    appliedFilters.status,
    appliedFilters.paymentMethod,
    appliedFilters.userId,
  ]);

  const handlePageChange = (currentPage) => {
    setPage(currentPage);
  };

  const handlePageSizeChange = (currentPageSize) => {
    setPageSize(currentPageSize);
    setPage(1);
  };

  const handleKeywordChange = (value) => {
    setKeyword(value);
  };

  const handleDateRangeChange = (range) => {
    // Semi dateTimeRange returns [Date, Date] or null.
    if (Array.isArray(range) && range.length === 2 && range[0] && range[1]) {
      setStartTime(Math.floor(new Date(range[0]).getTime() / 1000));
      setEndTime(Math.floor(new Date(range[1]).getTime() / 1000));
    } else {
      setStartTime(0);
      setEndTime(0);
    }
  };

  const handleApplyFilters = () => {
    setAppliedFilters(
      normalizeFilters({
        keyword,
        startTime,
        endTime,
        status,
        paymentMethod,
        userId,
      }),
    );
    setPage(1);
  };

  const handleResetFilters = () => {
    setKeyword('');
    setStartTime(0);
    setEndTime(0);
    setStatus('');
    setPaymentMethod('');
    setUserId('');
    setAppliedFilters(EMPTY_FILTERS);
    setPage(1);
  };

  const draftFilters = {
    keyword,
    startTime,
    endTime,
    status,
    paymentMethod,
    userId,
  };
  const hasActiveFilters = hasFilters(draftFilters);
  const hasAppliedFilters = hasFilters(appliedFilters);
  const hasPendingFilterChanges =
    JSON.stringify(normalizeFilters(draftFilters)) !==
    JSON.stringify(normalizeFilters(appliedFilters));
  const canShowExport = userIsAdmin || userExportEnabled;
  const canExport = total > 0;

  const handleExport = async () => {
    if (exporting) return;
    if (total <= 0) return;
    setExporting(true);
    try {
      const base = userIsAdmin
        ? '/api/user/topup/export'
        : '/api/user/topup/self/export';
      const qs = buildFilterQuery(appliedFilters).toString();
      const url = qs ? `${base}?${qs}` : base;

      const res = await API.get(url, {
        responseType: 'blob',
        skipErrorHandler: true,
      });
      const blob = res.data;

      const contentType = (res.headers?.['content-type'] || '').toString();
      if (contentType.includes('application/json')) {
        const text = await blob.text();
        let message = '';
        try {
          message = JSON.parse(text)?.message || '';
        } catch (e) {
          /* ignore */
        }
        Toast.error({ content: message || t('Export failed') });
        return;
      }

      const disposition = (
        res.headers?.['content-disposition'] || ''
      ).toString();
      const match = /filename="?([^"]+)"?/i.exec(disposition);
      const filename = match?.[1] || 'topup-export.csv';

      const objectUrl = window.URL.createObjectURL(blob);
      const link = document.createElement('a');
      link.href = objectUrl;
      link.download = filename;
      document.body.appendChild(link);
      link.click();
      link.remove();
      window.URL.revokeObjectURL(objectUrl);

      const truncated =
        (res.headers?.['x-export-truncated'] || '').toString() === 'true';
      if (truncated) {
        const maxRows = res.headers?.['x-export-max-rows'] || '';
        Toast.warning({
          content: t('Export reached the {{count}}-row limit. Some records were not exported. Narrow the time range to export the rest.', { count: maxRows }),
        });
      } else {
        Toast.success({ content: t('Export started') });
      }
    } catch (error) {
      if (error?.response?.status === 429) {
        Toast.error({ content: t('Export request too frequent, please wait 5 minutes.') });
      } else {
        Toast.error({ content: t('Export failed') });
      }
    } finally {
      setExporting(false);
    }
  };

  const handleAdminComplete = async (tradeNo) => {
    try {
      const res = await API.post('/api/user/topup/complete', {
        trade_no: tradeNo,
      });
      const { success, message } = res.data;
      if (success) {
        Toast.success({ content: t('补单成功') });
        await loadTopups(page, pageSize);
      } else {
        Toast.error({ content: message || t('补单失败') });
      }
    } catch (e) {
      Toast.error({ content: t('补单失败') });
    }
  };

  const confirmAdminComplete = (tradeNo) => {
    Modal.confirm({
      title: t('确认补单'),
      content: t('是否将该订单标记为成功并为用户入账？'),
      onOk: () => handleAdminComplete(tradeNo),
    });
  };

  const renderStatusBadge = (status) => {
    const config = STATUS_CONFIG[status] || { type: 'primary', key: status };
    return (
      <span className='flex items-center gap-2'>
        <Badge dot type={config.type} />
        <span>{t(config.key)}</span>
      </span>
    );
  };

  // Render payment method label.
  const renderPaymentMethod = (pm) => {
    const displayName = PAYMENT_METHOD_MAP[pm];
    return <Text>{displayName ? t(displayName) : pm || '-'}</Text>;
  };

  const isSubscriptionTopup = (record) => {
    const tradeNo = (record?.trade_no || '').toLowerCase();
    return Number(record?.amount || 0) === 0 && tradeNo.startsWith('sub');
  };

  const isRawQuotaTopup = (record) =>
    record?.payment_provider === 'infini' || record?.payment_method === 'infini';

  const renderMoney = (money, record) => {
    const value = Number(money) || 0;
    if (isRawQuotaTopup(record)) {
      const code = (record?.payment_currency || '').trim().toUpperCase();
      if (code) {
        const sym = CURRENCY_SYMBOLS[code];
        return sym
          ? `${sym}${value.toFixed(2)}`
          : `${value.toFixed(2)} ${code}`;
      }
    }
    return `¥${value.toFixed(2)}`;
  };

  const columns = useMemo(() => {
    const baseColumns = [
      ...(userIsAdmin
        ? [
            {
              title: t('用户ID'),
              dataIndex: 'user_id',
              key: 'user_id',
              render: (userId) => <Text>{userId ?? '-'}</Text>,
            },
          ]
        : []),
      {
        title: t('订单号'),
        dataIndex: 'trade_no',
        key: 'trade_no',
        render: (text) => <Text copyable>{text}</Text>,
      },
      {
        title: t('支付方式'),
        dataIndex: 'payment_method',
        key: 'payment_method',
        render: renderPaymentMethod,
      },
      {
        title: t('充值额度'),
        dataIndex: 'amount',
        key: 'amount',
        render: (amount, record) => {
          if (isSubscriptionTopup(record)) {
            return (
              <Tag color='purple' shape='circle' size='small'>
                {t('订阅套餐')}
              </Tag>
            );
          }
          return (
            <span className='flex items-center gap-1'>
              <Coins size={16} />
              <Text>{isRawQuotaTopup(record) ? renderQuota(amount) : amount}</Text>
            </span>
          );
        },
      },
      {
        title: t('支付金额'),
        dataIndex: 'money',
        key: 'money',
        render: (money, record) => (
          <Text type='danger'>{renderMoney(money, record)}</Text>
        ),
      },
      {
        title: t('状态'),
        dataIndex: 'status',
        key: 'status',
        render: renderStatusBadge,
      },
    ];

    if (userIsAdmin) {
      baseColumns.push({
        title: t('操作'),
        key: 'action',
        render: (_, record) => {
          const actions = [];
          if (record.status === 'pending') {
            actions.push(
              <Button
                key="complete"
                size='small'
                type='primary'
                theme='outline'
                onClick={() => confirmAdminComplete(record.trade_no)}
              >
                {t('补单')}
              </Button>
            );
          }
          return actions.length > 0 ? <>{actions}</> : null;
        },
      });
    }

    baseColumns.push({
      title: t('创建时间'),
      dataIndex: 'create_time',
      key: 'create_time',
      render: (time) => timestamp2string(time),
    });

    return baseColumns;
  }, [t, userIsAdmin]);

  return (
    <Modal
      title={t('充值账单')}
      visible={visible}
      onCancel={onCancel}
      footer={null}
      size={isMobile ? 'full-width' : undefined}
      width={isMobile ? undefined : '90vw'}
      style={isMobile ? undefined : { maxWidth: 1100 }}
      bodyStyle={{
        height: isMobile ? 'calc(100vh - 140px)' : 720,
        display: 'flex',
        flexDirection: 'column',
        overflow: 'hidden',
      }}
    >
      {/* Filter and export toolbar */}
      <div className='mb-3 flex flex-wrap items-center gap-2'>
        <Input
          prefix={<IconSearch />}
          placeholder={t('订单号')}
          value={keyword}
          onChange={handleKeywordChange}
          showClear
          style={{ width: isMobile ? '100%' : 200 }}
        />
        <DatePicker
          type='dateTimeRange'
          placeholder={[t('开始时间'), t('结束时间')]}
          value={startTime && endTime ? [startTime * 1000, endTime * 1000] : []}
          onChange={handleDateRangeChange}
          style={{ width: isMobile ? '100%' : 360 }}
        />
        <Select
          placeholder={t('状态')}
          value={status || ''}
          onChange={(val) => {
            setStatus(val || '');
          }}
          style={{ width: isMobile ? '48%' : 120 }}
        >
          <Select.Option value=''>{t('全部状态')}</Select.Option>
          {Object.keys(STATUS_CONFIG).map((key) => (
            <Select.Option key={key} value={key}>
              {t(STATUS_CONFIG[key].key)}
            </Select.Option>
          ))}
        </Select>
        <Select
          placeholder={t('支付方式')}
          value={paymentMethod || ''}
          onChange={(val) => {
            setPaymentMethod(val || '');
          }}
          style={{ width: isMobile ? '48%' : 140 }}
        >
          <Select.Option value=''>{t('全部支付方式')}</Select.Option>
          {paymentMethodOptions.map((pm) => (
            <Select.Option key={pm} value={pm}>
              {t(PAYMENT_METHOD_MAP[pm] || pm)}
            </Select.Option>
          ))}
        </Select>
        {userIsAdmin && (
          <Input
            type='number'
            placeholder={t('用户ID')}
            value={userId}
            onChange={(val) => {
              setUserId(val);
            }}
            showClear
            style={{ width: isMobile ? '48%' : 110 }}
          />
        )}
        <Button
          theme='light'
          type='primary'
          icon={<IconSearch />}
          disabled={!hasPendingFilterChanges}
          onClick={handleApplyFilters}
        >
          {t('Filter')}
        </Button>
        <Button
          theme='borderless'
          icon={<IconRefresh />}
          disabled={!hasActiveFilters && !hasAppliedFilters}
          onClick={handleResetFilters}
        >
          {t('Reset')}
        </Button>
        {canShowExport &&
          <Button
            theme='light'
            type='primary'
            icon={<IconDownload />}
            loading={exporting}
            disabled={!canExport}
            onClick={handleExport}
          >
            {t('Export')}
          </Button>}
      </div>
      <div style={{ flex: 1, minHeight: 0, overflowY: 'auto' }}>
        <Table
        columns={columns}
        dataSource={topups}
        loading={loading}
        rowKey='id'
        tableLayout='fixed'
        scroll={{ x: '100%' }}
        pagination={false}
        size='small'
        empty={
          <Empty
            image={<IllustrationNoResult style={{ width: 150, height: 150 }} />}
            darkModeImage={
              <IllustrationNoResultDark style={{ width: 150, height: 150 }} />
            }
            description={t('暂无充值记录')}
            style={{ padding: 30 }}
          />
        }
        />
      </div>
      {total > 0 && (
        <div
          className='mt-3 mb-2 flex flex-wrap items-center justify-between gap-3'
          style={{ flexShrink: 0 }}
        >
          {createCardProPagination({
            currentPage: page,
            pageSize,
            total,
            pageSizeOpts: [10, 20, 50, 100],
            showSizeChanger: true,
            onPageSizeChange: handlePageSizeChange,
            onPageChange: handlePageChange,
            t,
          })}
        </div>
      )}
    </Modal>
  );
};

export default TopupHistoryModal;

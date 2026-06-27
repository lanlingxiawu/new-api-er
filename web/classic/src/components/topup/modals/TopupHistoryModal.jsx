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
  Dropdown,
} from '@douyinfe/semi-ui';
import {
  IllustrationNoResult,
  IllustrationNoResultDark,
} from '@douyinfe/semi-illustrations';
import { Coins } from 'lucide-react';
import { IconSearch, IconDownload, IconRefresh } from '@douyinfe/semi-icons';
import { API, renderQuota, timestamp2string } from '../../../helpers';
import { isAdmin } from '../../../helpers/utils';
import { useIsMobile } from '../../../hooks/common/useIsMobile';
const { Text } = Typography;

// 状态映射配置
const STATUS_CONFIG = {
  success: { type: 'success', key: '成功' },
  pending: { type: 'warning', key: '待支付' },
  failed: { type: 'danger', key: '失败' },
  expired: { type: 'danger', key: '已过期' },
};

// 支付方式映射（键为后端存储的 payment_method 原始值，见 model/topup.go）
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

// 支付方式筛选选项
const PAYMENT_METHOD_FILTER_OPTIONS = [
  'stripe',
  'creem',
  'waffo',
  'alipay_official',
  'wechat_official',
  'infini',
];

// 常见结算币种符号（Infini 多币种）
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

const TopupHistoryModal = ({ visible, onCancel, t, enabledPaymentMethods }) => {
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
  const isMobile = useIsMobile();
  const userIsAdmin = useMemo(() => isAdmin(), []);

  // 支付方式筛选项：仅显示已开启的支付方式（由父组件传入）；未传入时回退全部。
  const paymentMethodOptions = Array.isArray(enabledPaymentMethods)
    ? enabledPaymentMethods
    : PAYMENT_METHOD_FILTER_OPTIONS;

  // 构造筛选查询参数（列表与导出共用）
  const buildFilterQuery = () => {
    const params = new URLSearchParams();
    if (keyword) params.append('keyword', keyword);
    if (startTime) params.append('start_time', String(startTime));
    if (endTime) params.append('end_time', String(endTime));
    if (status) params.append('status', status);
    if (paymentMethod) params.append('payment_method', paymentMethod);
    if (userIsAdmin && userId) params.append('user_id', String(userId));
    return params;
  };

  const fetchTopupPage = async (currentPage, currentPageSize) => {
    const base = userIsAdmin ? '/api/user/topup' : '/api/user/topup/self';
    const params = buildFilterQuery();
    params.append('page_size', String(currentPageSize));
    params.append('p', String(currentPage));
    return API.get(`${base}?${params.toString()}`);
  };

  // 偏移分页：跳到任意页只发一次请求，不再从第 1 页顺序补抓游标（避免线性请求风暴）。
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
  }, [visible, page, pageSize, keyword, startTime, endTime, status, paymentMethod, userId]);

  const handlePageChange = (currentPage) => {
    setPage(currentPage);
  };

  const handlePageSizeChange = (currentPageSize) => {
    setPageSize(currentPageSize);
    setPage(1);
  };

  const handleKeywordChange = (value) => {
    setKeyword(value);
    setPage(1);
  };

  const handleDateRangeChange = (range) => {
    // Semi dateTimeRange 返回 [Date, Date] 或 null
    if (Array.isArray(range) && range.length === 2 && range[0] && range[1]) {
      setStartTime(Math.floor(new Date(range[0]).getTime() / 1000));
      setEndTime(Math.floor(new Date(range[1]).getTime() / 1000));
    } else {
      setStartTime(0);
      setEndTime(0);
    }
    setPage(1);
  };

  const handleResetFilters = () => {
    setKeyword('');
    setStartTime(0);
    setEndTime(0);
    setStatus('');
    setPaymentMethod('');
    setUserId('');
    setPage(1);
  };

  const hasActiveFilters =
    keyword || startTime || endTime || status || paymentMethod || userId;

  // CSV 导出。useFilters=true 按当前筛选导出；false 导出全部。
  const handleExport = async (useFilters) => {
    if (exporting) return;
    setExporting(true);
    try {
      const base = userIsAdmin
        ? '/api/user/topup/export'
        : '/api/user/topup/self/export';
      let url = base;
      if (useFilters) {
        const qs = buildFilterQuery().toString();
        if (qs) url = `${base}?${qs}`;
      }

      // skipErrorHandler：自行处理错误（含 429 频控），避免全局拦截器再弹一次通用提示。
      const res = await API.get(url, {
        responseType: 'blob',
        skipErrorHandler: true,
      });
      const blob = res.data;

      // 后端以 JSON 返回业务错误（而非 CSV 流）时，解析并提示
      const contentType = (res.headers?.['content-type'] || '').toString();
      if (contentType.includes('application/json')) {
        const text = await blob.text();
        let message = '';
        try {
          message = JSON.parse(text)?.message || '';
        } catch (e) {
          /* ignore */
        }
        Toast.error({ content: message || t('导出失败') });
        return;
      }

      // 解析文件名
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
          content: t(
            '已达到 {{count}} 行导出上限，部分记录未导出，请缩小时间范围以导出其余记录。',
            { count: maxRows },
          ),
        });
      } else {
        Toast.success({ content: t('导出已开始') });
      }
    } catch (error) {
      if (error?.response?.status === 429) {
        Toast.error({ content: t('导出请求过于频繁，请稍后再试') });
      } else {
        Toast.error({ content: t('导出失败') });
      }
    } finally {
      setExporting(false);
    }
  };

  // 管理员补单
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

  // 渲染状态徽章
  const renderStatusBadge = (status) => {
    const config = STATUS_CONFIG[status] || { type: 'primary', key: status };
    return (
      <span className='flex items-center gap-2'>
        <Badge dot type={config.type} />
        <span>{t(config.key)}</span>
      </span>
    );
  };

  // 渲染支付方式
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

  // 渲染支付金额（Infini 等多币种按 payment_currency 显示正确单位，其余沿用 ¥）
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

    // 管理员才显示操作列
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

  const exportMenu = [
    {
      node: 'item',
      name: t('按当前筛选导出'),
      onClick: () => handleExport(true),
    },
    {
      node: 'item',
      name: t('导出全部'),
      onClick: () => handleExport(false),
    },
  ];

  return (
    <Modal
      title={t('充值账单')}
      visible={visible}
      onCancel={onCancel}
      footer={null}
      size={isMobile ? 'full-width' : undefined}
      width={isMobile ? undefined : '90vw'}
      style={isMobile ? undefined : { maxWidth: 1100 }}
      bodyStyle={{ maxHeight: '72vh', overflowY: 'auto' }}
    >
      {/* 筛选与导出工具栏 */}
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
            setPage(1);
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
            setPage(1);
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
              setPage(1);
            }}
            showClear
            style={{ width: isMobile ? '48%' : 110 }}
          />
        )}
        {hasActiveFilters && (
          <Button
            theme='borderless'
            icon={<IconRefresh />}
            onClick={handleResetFilters}
          >
            {t('重置')}
          </Button>
        )}
        <Dropdown trigger='click' position='bottomRight' menu={exportMenu}>
          <Button
            theme='light'
            type='primary'
            icon={<IconDownload />}
            loading={exporting}
          >
            {t('导出')}
          </Button>
        </Dropdown>
      </div>
      <Table
        columns={columns}
        dataSource={topups}
        loading={loading}
        rowKey='id'
        tableLayout='fixed'
        scroll={{ x: '100%' }}
        pagination={{
          currentPage: page,
          pageSize: pageSize,
          total: total,
          showSizeChanger: true,
          pageSizeOpts: [10, 20, 50, 100],
          onPageChange: handlePageChange,
          onPageSizeChange: handlePageSizeChange,
        }}
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
    </Modal>
  );
};

export default TopupHistoryModal;

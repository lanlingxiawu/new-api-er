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

import React, { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  Button,
  Empty,
  Input,
  SideSheet,
  Space,
  Spin,
  Tag,
  Typography,
} from '@douyinfe/semi-ui';
import {
  IllustrationNoResult,
  IllustrationNoResultDark,
} from '@douyinfe/semi-illustrations';
import CardPro from '../../components/common/ui/CardPro';
import CardTable from '../../components/common/ui/CardTable';
import { IconCopy } from '@douyinfe/semi-icons';
import {
  API,
  showError,
  showSuccess,
  copy,
  timestamp2string,
  createCardProPagination,
} from '../../helpers';
import { useIsMobile } from '../../hooks/common/useIsMobile';

const { Text, Title } = Typography;

function formatBytes(bytes) {
  if (!bytes || bytes <= 0) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB'];
  let value = bytes;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit += 1;
  }
  return `${value.toFixed(unit === 0 ? 0 : 1)} ${units[unit]}`;
}

function prettify(raw) {
  if (!raw) return '';
  const trimmed = String(raw).trim();
  if (
    (trimmed.startsWith('{') && trimmed.endsWith('}')) ||
    (trimmed.startsWith('[') && trimmed.endsWith(']'))
  ) {
    try {
      return JSON.stringify(JSON.parse(trimmed), null, 2);
    } catch (e) {
      return raw;
    }
  }
  return raw;
}

const RequestLog = () => {
  const { t } = useTranslation();
  const isMobile = useIsMobile();
  const [logs, setLogs] = useState([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(20);
  const [loading, setLoading] = useState(false);
  const [filters, setFilters] = useState({
    username: '',
    model_name: '',
    request_id: '',
  });
  const [detail, setDetail] = useState(null);
  const [detailVisible, setDetailVisible] = useState(false);
  const [detailLoading, setDetailLoading] = useState(false);

  const loadLogs = async (targetPage = page, targetPageSize = pageSize) => {
    setLoading(true);
    try {
      const params = new URLSearchParams({
        p: targetPage,
        page_size: targetPageSize,
      });
      if (filters.username) params.append('username', filters.username);
      if (filters.model_name) params.append('model_name', filters.model_name);
      if (filters.request_id) params.append('request_id', filters.request_id);
      const res = await API.get(`/api/request-log/?${params.toString()}`);
      const { success, message, data } = res.data;
      if (success) {
        setLogs((data.items || []).map((item) => ({ ...item, key: item.id })));
        setTotal(data.total || 0);
      } else {
        showError(message);
      }
    } catch (error) {
      showError(error.message);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    loadLogs(1, pageSize);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const handleSearch = () => {
    setPage(1);
    loadLogs(1, pageSize);
  };

  const handlePageChange = (newPage) => {
    setPage(newPage);
    loadLogs(newPage, pageSize);
  };

  const handlePageSizeChange = (size) => {
    setPageSize(size);
    setPage(1);
    loadLogs(1, size);
  };

  const openDetail = async (id) => {
    setDetailLoading(true);
    try {
      const res = await API.get(`/api/request-log/${id}`);
      const { success, message, data } = res.data;
      if (success) {
        setDetail(data);
        setDetailVisible(true);
      } else {
        showError(message);
      }
    } catch (error) {
      showError(error.message);
    } finally {
      setDetailLoading(false);
    }
  };

  const columns = [
    {
      title: t('时间'),
      dataIndex: 'created_at',
      render: (text) => timestamp2string(text),
    },
    { title: t('用户名'), dataIndex: 'username' },
    { title: t('模型'), dataIndex: 'model_name' },
    { title: t('渠道'), dataIndex: 'channel_id' },
    { title: t('方法'), dataIndex: 'method' },
    {
      title: t('路径'),
      dataIndex: 'url',
      render: (text) => (
        <Text ellipsis={{ showTooltip: true }} style={{ maxWidth: 260 }}>
          {text}
        </Text>
      ),
    },
    {
      title: t('状态'),
      dataIndex: 'status_code',
      render: (code, record) => (
        <Space>
          <Tag color={code >= 200 && code < 300 ? 'green' : 'red'}>{code}</Tag>
          {record.is_stream && <Tag color='blue'>{t('流式')}</Tag>}
        </Space>
      ),
    },
    {
      title: t('请求体大小'),
      dataIndex: 'request_body_size',
      render: (size) => formatBytes(size),
    },
    {
      title: t('返回体大小'),
      dataIndex: 'response_body_size',
      render: (size) => formatBytes(size),
    },
    {
      title: t('操作'),
      dataIndex: 'operate',
      render: (text, record) => (
        <Button size='small' onClick={() => openDetail(record.id)}>
          {t('查看')}
        </Button>
      ),
    },
  ];

  const handleCopyContent = async (content) => {
    if (!content) return;
    const success = await copy(prettify(content));
    if (success) {
      showSuccess(t('已复制到剪贴板'));
    } else {
      showError(t('复制失败'));
    }
  };

  const renderBlock = (title, content) => (
    <div style={{ marginBottom: 16 }}>
      <div
        style={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
        }}
      >
        <Title heading={6}>{title}</Title>
        <Button
          theme='borderless'
          type='tertiary'
          size='small'
          icon={<IconCopy />}
          disabled={!content}
          onClick={() => handleCopyContent(content)}
        >
          {t('复制')}
        </Button>
      </div>
      <pre
        style={{
          background: 'var(--semi-color-fill-0)',
          border: '1px solid var(--semi-color-border)',
          padding: 12,
          borderRadius: 8,
          maxHeight: 280,
          overflow: 'auto',
          whiteSpace: 'pre-wrap',
          wordBreak: 'break-all',
          fontSize: 12,
          lineHeight: 1.6,
          fontFamily:
            'ui-monospace, SFMono-Regular, Menlo, Consolas, monospace',
        }}
      >
        {content ? prettify(content) : '-'}
      </pre>
    </div>
  );

  const searchArea = (
    <Space wrap>
      <Input
        placeholder={t('用户名')}
        value={filters.username}
        onChange={(v) => setFilters({ ...filters, username: v })}
        style={{ width: 160 }}
        showClear
      />
      <Input
        placeholder={t('模型')}
        value={filters.model_name}
        onChange={(v) => setFilters({ ...filters, model_name: v })}
        style={{ width: 160 }}
        showClear
      />
      <Input
        placeholder={t('请求 ID')}
        value={filters.request_id}
        onChange={(v) => setFilters({ ...filters, request_id: v })}
        style={{ width: 220 }}
        showClear
      />
      <Button theme='solid' onClick={handleSearch}>
        {t('查询')}
      </Button>
    </Space>
  );

  return (
    <div className='mt-[60px] px-2'>
      {detailLoading && (
        <div
          style={{
            position: 'fixed',
            inset: 0,
            zIndex: 1100,
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            background: 'rgba(0, 0, 0, 0.35)',
          }}
        >
          <Spin size='large' tip={t('加载中...')} />
        </div>
      )}
      <CardPro
        type='type2'
        statsArea={
          <Title heading={4} style={{ margin: '4px 0' }}>
            {t('请求日志')}
          </Title>
        }
        searchArea={searchArea}
        paginationArea={createCardProPagination({
          currentPage: page,
          pageSize: pageSize,
          total: total,
          onPageChange: handlePageChange,
          onPageSizeChange: handlePageSizeChange,
          isMobile: isMobile,
          t: t,
        })}
        t={t}
      >
        <CardTable
          columns={columns}
          dataSource={logs}
          rowKey='key'
          loading={loading}
          scroll={{ x: 'max-content' }}
          className='rounded-xl overflow-hidden'
          size='small'
          hidePagination={true}
          empty={
            <Empty
              image={
                <IllustrationNoResult style={{ width: 150, height: 150 }} />
              }
              darkModeImage={
                <IllustrationNoResultDark style={{ width: 150, height: 150 }} />
              }
              description={t('搜索无结果')}
              style={{ padding: 30 }}
            />
          }
        />
      </CardPro>

      <SideSheet
        title={t('请求日志详情')}
        visible={detailVisible}
        onCancel={() => setDetailVisible(false)}
        width={720}
      >
        {detail && (
          <div>
            <Text
              type='tertiary'
              style={{
                fontFamily:
                  'ui-monospace, SFMono-Regular, Menlo, Consolas, monospace',
                fontSize: 12,
                wordBreak: 'break-all',
              }}
            >
              Request ID: {detail.request_id || '-'}
            </Text>
            <div style={{ marginTop: 16 }}>
              {renderBlock(t('请求头'), detail.request_headers)}
              {renderBlock(
                `${t('请求体')} (${formatBytes(detail.request_body_size)})`,
                detail.request_body,
              )}
              {renderBlock(t('返回头'), detail.response_headers)}
              {renderBlock(
                `${t('返回体')} (${formatBytes(detail.response_body_size)})`,
                detail.response_body,
              )}
            </div>
          </div>
        )}
      </SideSheet>
    </div>
  );
};

export default RequestLog;

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

import React, { useContext, useEffect, useState } from 'react';
import { Button, Card, Col, Form, Row, Switch, Typography } from '@douyinfe/semi-ui';
import { useTranslation } from 'react-i18next';
import { API, showError, showSuccess } from '../../../helpers';
import { StatusContext } from '../../../context/Status';

const { Text } = Typography;

const DEFAULT_HEADER_MODULES = {
  home: true,
  console: true,
  pricing: {
    enabled: true,
    requireAuth: false,
  },
  docs: true,
  about: true,
};

const cloneDefaults = () => ({
  ...DEFAULT_HEADER_MODULES,
  pricing: { ...DEFAULT_HEADER_MODULES.pricing },
});

const normalizeAccessModule = (value, fallback) => {
  if (typeof value === 'boolean') {
    return {
      enabled: value,
      requireAuth: fallback.requireAuth,
    };
  }
  if (value && typeof value === 'object') {
    return {
      enabled: value.enabled !== false,
      requireAuth: value.requireAuth === true,
    };
  }
  return { ...fallback };
};

const normalizeHeaderModules = (raw) => {
  const defaults = cloneDefaults();
  if (!raw) return defaults;

  try {
    const parsed = typeof raw === 'string' ? JSON.parse(raw) : raw;
    return {
      ...defaults,
      ...parsed,
      pricing: normalizeAccessModule(parsed?.pricing, defaults.pricing),
    };
  } catch (error) {
    return defaults;
  }
};

export default function SettingsHeaderNavModules(props) {
  const { t } = useTranslation();
  const [loading, setLoading] = useState(false);
  const [statusState, statusDispatch] = useContext(StatusContext);
  const [headerNavModules, setHeaderNavModules] = useState(cloneDefaults);

  useEffect(() => {
    if (props.options?.HeaderNavModules) {
      setHeaderNavModules(normalizeHeaderModules(props.options.HeaderNavModules));
    }
  }, [props.options]);

  const handleHeaderNavModuleChange = (moduleKey) => (checked) => {
    setHeaderNavModules((prev) => {
      const next = { ...prev };
      if (moduleKey === 'pricing') {
        next[moduleKey] = {
          ...normalizeAccessModule(prev[moduleKey], DEFAULT_HEADER_MODULES[moduleKey]),
          enabled: checked,
        };
      } else {
        next[moduleKey] = checked;
      }
      return next;
    });
  };

  const handleAccessAuthChange = (moduleKey) => (checked) => {
    setHeaderNavModules((prev) => ({
      ...prev,
      [moduleKey]: {
        ...normalizeAccessModule(prev[moduleKey], DEFAULT_HEADER_MODULES[moduleKey]),
        requireAuth: checked,
      },
    }));
  };

  const resetHeaderNavModules = () => {
    setHeaderNavModules(cloneDefaults());
    showSuccess(t('已重置为默认配置'));
  };

  const onSubmit = async () => {
    setLoading(true);
    try {
      const value = JSON.stringify(headerNavModules);
      const res = await API.put('/api/option/', {
        key: 'HeaderNavModules',
        value,
      });
      const { success, message } = res.data;
      if (success) {
        showSuccess(t('保存成功'));
        statusDispatch({
          type: 'set',
          payload: {
            ...statusState.status,
            HeaderNavModules: value,
          },
        });
        if (props.refresh) {
          await props.refresh();
        }
      } else {
        showError(message);
      }
    } catch (error) {
      showError(t('保存失败，请重试'));
    } finally {
      setLoading(false);
    }
  };

  const moduleConfigs = [
    {
      key: 'home',
      title: t('首页'),
      description: t('用户主页，展示系统信息'),
    },
    {
      key: 'console',
      title: t('控制台'),
      description: t('用户控制面板，管理账户'),
    },
    {
      key: 'pricing',
      title: t('模型广场'),
      description: t('模型定价与可用模型展示'),
      hasAccessConfig: true,
      accessTitle: t('需要登录访问'),
      accessDescription: t('开启后未登录用户无法访问模型广场'),
    },
    {
      key: 'docs',
      title: t('文档'),
      description: t('系统文档和帮助信息'),
    },
    {
      key: 'about',
      title: t('关于'),
      description: t('关于系统的详细信息'),
    },
  ];

  const isEnabled = (module) =>
    module.hasAccessConfig
      ? headerNavModules[module.key]?.enabled !== false
      : headerNavModules[module.key] === true;

  return (
    <Card>
      <Form.Section
        text={t('顶部栏管理')}
        extraText={t('控制顶部栏模块显示状态，全局生效')}
      >
        <Row gutter={[16, 16]} style={{ marginBottom: '24px' }}>
          {moduleConfigs.map((module) => (
            <Col key={module.key} xs={24} sm={12} md={8} lg={8} xl={8}>
              <Card
                style={{
                  borderRadius: '8px',
                  border: '1px solid var(--semi-color-border)',
                  transition: 'all 0.2s ease',
                  background: 'var(--semi-color-bg-1)',
                  minHeight: '80px',
                }}
                bodyStyle={{ padding: '16px' }}
                hoverable
              >
                <div
                  style={{
                    display: 'flex',
                    justifyContent: 'space-between',
                    alignItems: 'center',
                    height: '100%',
                  }}
                >
                  <div style={{ flex: 1, textAlign: 'left' }}>
                    <div
                      style={{
                        fontWeight: '600',
                        fontSize: '14px',
                        color: 'var(--semi-color-text-0)',
                        marginBottom: '4px',
                      }}
                    >
                      {module.title}
                    </div>
                    <Text
                      type='secondary'
                      size='small'
                      style={{
                        fontSize: '12px',
                        color: 'var(--semi-color-text-2)',
                        lineHeight: '1.4',
                        display: 'block',
                      }}
                    >
                      {module.description}
                    </Text>
                  </div>
                  <div style={{ marginLeft: '16px' }}>
                    <Switch
                      checked={isEnabled(module)}
                      onChange={handleHeaderNavModuleChange(module.key)}
                      size='default'
                    />
                  </div>
                </div>

                {module.hasAccessConfig && isEnabled(module) ? (
                  <div
                    style={{
                      borderTop: '1px solid var(--semi-color-border)',
                      marginTop: '12px',
                      paddingTop: '12px',
                    }}
                  >
                    <div
                      style={{
                        display: 'flex',
                        justifyContent: 'space-between',
                        alignItems: 'center',
                      }}
                    >
                      <div style={{ flex: 1, textAlign: 'left' }}>
                        <div
                          style={{
                            fontWeight: '500',
                            fontSize: '12px',
                            color: 'var(--semi-color-text-1)',
                            marginBottom: '2px',
                          }}
                        >
                          {module.accessTitle}
                        </div>
                        <Text
                          type='secondary'
                          size='small'
                          style={{
                            fontSize: '11px',
                            color: 'var(--semi-color-text-2)',
                            lineHeight: '1.4',
                            display: 'block',
                          }}
                        >
                          {module.accessDescription}
                        </Text>
                      </div>
                      <div style={{ marginLeft: '16px' }}>
                        <Switch
                          checked={headerNavModules[module.key]?.requireAuth || false}
                          onChange={handleAccessAuthChange(module.key)}
                          size='default'
                        />
                      </div>
                    </div>
                  </div>
                ) : null}
              </Card>
            </Col>
          ))}
        </Row>

        <div
          style={{
            display: 'flex',
            gap: '12px',
            justifyContent: 'flex-start',
            alignItems: 'center',
            paddingTop: '8px',
            borderTop: '1px solid var(--semi-color-border)',
          }}
        >
          <Button
            size='default'
            type='tertiary'
            onClick={resetHeaderNavModules}
            style={{
              borderRadius: '6px',
              fontWeight: '500',
            }}
          >
            {t('重置为默认')}
          </Button>
          <Button
            size='default'
            type='primary'
            onClick={onSubmit}
            loading={loading}
            style={{
              borderRadius: '6px',
              fontWeight: '500',
              minWidth: '100px',
            }}
          >
            {t('保存设置')}
          </Button>
        </div>
      </Form.Section>
    </Card>
  );
}

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
import {
  Button,
  Card,
  Col,
  Form,
  Row,
  Switch,
  Typography,
} from '@douyinfe/semi-ui';
import { useTranslation } from 'react-i18next';
import { API, showError, showSuccess } from '../../../helpers';
import { StatusContext } from '../../../context/Status';
import {
  DEFAULT_ADMIN_CONFIG,
  mergeAdminConfig,
} from '../../../hooks/common/useSidebar';

const { Text } = Typography;

const cloneDefaultConfig = () =>
  JSON.parse(JSON.stringify(DEFAULT_ADMIN_CONFIG));

export default function SettingsSidebarModulesAdmin(props) {
  const { t } = useTranslation();
  const [loading, setLoading] = useState(false);
  const [statusState, statusDispatch] = useContext(StatusContext);
  const [sidebarModulesAdmin, setSidebarModulesAdmin] =
    useState(cloneDefaultConfig);

  useEffect(() => {
    if (!props.options?.SidebarModulesAdmin) {
      setSidebarModulesAdmin(cloneDefaultConfig());
      return;
    }

    try {
      setSidebarModulesAdmin(
        mergeAdminConfig(JSON.parse(props.options.SidebarModulesAdmin)),
      );
    } catch (error) {
      setSidebarModulesAdmin(cloneDefaultConfig());
    }
  }, [props.options]);

  const handleSectionChange = (sectionKey) => (checked) => {
    setSidebarModulesAdmin((prev) => ({
      ...prev,
      [sectionKey]: {
        ...prev[sectionKey],
        enabled: checked,
      },
    }));
  };

  const handleModuleChange = (sectionKey, moduleKey) => (checked) => {
    setSidebarModulesAdmin((prev) => ({
      ...prev,
      [sectionKey]: {
        ...prev[sectionKey],
        [moduleKey]: checked,
      },
    }));
  };

  const resetSidebarModules = () => {
    setSidebarModulesAdmin(cloneDefaultConfig());
    showSuccess(t('已重置为默认配置'));
  };

  const onSubmit = async () => {
    setLoading(true);
    try {
      const value = JSON.stringify(sidebarModulesAdmin);
      const res = await API.put('/api/option/', {
        key: 'SidebarModulesAdmin',
        value,
      });
      const { success, message } = res.data;
      if (success) {
        showSuccess(t('保存成功'));
        statusDispatch({
          type: 'set',
          payload: {
            ...statusState.status,
            SidebarModulesAdmin: value,
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

  const sectionConfigs = [
    {
      key: 'chat',
      title: t('聊天区域'),
      description: t('操练场和聊天功能'),
      modules: [
        {
          key: 'playground',
          title: t('操练场'),
          description: t('AI 模型测试环境'),
        },
        {
          key: 'chat',
          title: t('聊天'),
          description: t('聊天会话管理'),
        },
      ],
    },
    {
      key: 'console',
      title: t('控制台区域'),
      description: t('数据管理和日志查看'),
      modules: [
        {
          key: 'detail',
          title: t('数据看板'),
          description: t('系统数据统计'),
        },
        {
          key: 'token',
          title: t('令牌管理'),
          description: t('API 令牌管理'),
        },
        {
          key: 'log',
          title: t('使用日志'),
          description: t('API 使用记录'),
        },
        {
          key: 'midjourney',
          title: t('绘图日志'),
          description: t('绘图任务记录'),
        },
        {
          key: 'task',
          title: t('任务日志'),
          description: t('系统任务记录'),
        },
      ],
    },
    {
      key: 'personal',
      title: t('个人中心区域'),
      description: t('用户个人功能'),
      modules: [
        {
          key: 'topup',
          title: t('钱包管理'),
          description: t('余额充值管理'),
        },
        {
          key: 'personal',
          title: t('个人设置'),
          description: t('个人信息设置'),
        },
        {
          key: 'commission',
          title: t('我的提成'),
          description: t('员工自助提成看板'),
        },
        {
          key: 'customerConsole',
          title: t('我的客户'),
          description: t('员工客户列表和维护工具'),
        },
      ],
    },
    {
      key: 'admin',
      title: t('管理员区域'),
      description: t('系统管理功能'),
      modules: [
        {
          key: 'channel',
          title: t('渠道管理'),
          description: t('API 渠道配置'),
        },
        {
          key: 'models',
          title: t('模型管理'),
          description: t('AI 模型配置'),
        },
        {
          key: 'deployment',
          title: t('模型部署'),
          description: t('模型部署管理'),
        },
        {
          key: 'subscription',
          title: t('订阅管理'),
          description: t('订阅套餐管理'),
        },
        {
          key: 'employee',
          title: t('员工管理'),
          description: t('管理员工与提成比例'),
        },
        {
          key: 'businessOverview',
          title: t('业务概览'),
          description: t('查看消耗、成本、利润与提成统计'),
        },
        {
          key: 'redemption',
          title: t('兑换码管理'),
          description: t('兑换码生成管理'),
        },
        {
          key: 'user',
          title: t('用户管理'),
          description: t('用户账户管理'),
        },
        {
          key: 'setting',
          title: t('系统设置'),
          description: t('系统参数配置'),
        },
      ],
    },
  ];

  return (
    <Card>
      <Form.Section
        text={t('侧边栏管理（全局控制）')}
        extraText={t(
          '全局控制侧边栏区域和功能显示，管理员隐藏的功能用户无法启用',
        )}
      >
        {sectionConfigs.map((section) => (
          <div key={section.key} style={{ marginBottom: '32px' }}>
            <div
              style={{
                display: 'flex',
                justifyContent: 'space-between',
                alignItems: 'center',
                marginBottom: '16px',
                padding: '12px 16px',
                backgroundColor: 'var(--semi-color-fill-0)',
                borderRadius: '8px',
                border: '1px solid var(--semi-color-border)',
              }}
            >
              <div>
                <div
                  style={{
                    fontWeight: '600',
                    fontSize: '16px',
                    color: 'var(--semi-color-text-0)',
                    marginBottom: '4px',
                  }}
                >
                  {section.title}
                </div>
                <Text
                  type='secondary'
                  size='small'
                  style={{
                    fontSize: '12px',
                    color: 'var(--semi-color-text-2)',
                    lineHeight: '1.4',
                  }}
                >
                  {section.description}
                </Text>
              </div>
              <Switch
                checked={sidebarModulesAdmin[section.key]?.enabled}
                onChange={handleSectionChange(section.key)}
                size='default'
              />
            </div>

            <Row gutter={[16, 16]}>
              {section.modules.map((module) => (
                <Col key={module.key} xs={24} sm={12} md={8} lg={6} xl={6}>
                  <Card
                    bodyStyle={{ padding: '16px' }}
                    hoverable
                    style={{
                      opacity: sidebarModulesAdmin[section.key]?.enabled
                        ? 1
                        : 0.5,
                      transition: 'opacity 0.2s',
                    }}
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
                          checked={
                            sidebarModulesAdmin[section.key]?.[module.key]
                          }
                          onChange={handleModuleChange(section.key, module.key)}
                          size='default'
                          disabled={!sidebarModulesAdmin[section.key]?.enabled}
                        />
                      </div>
                    </div>
                  </Card>
                </Col>
              ))}
            </Row>
          </div>
        ))}

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
            onClick={resetSidebarModules}
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

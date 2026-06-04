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

import React, { useContext, useEffect, useMemo, useState } from 'react';
import {
  Avatar,
  Button,
  Card,
  Col,
  Row,
  Switch,
  Typography,
} from '@douyinfe/semi-ui';
import { Settings } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { API, showError, showSuccess } from '../../../helpers';
import { StatusContext } from '../../../context/Status';
import { useUserPermissions } from '../../../hooks/common/useUserPermissions';
import {
  DEFAULT_ADMIN_CONFIG,
  mergeAdminConfig,
  useSidebar,
} from '../../../hooks/common/useSidebar';

const { Text } = Typography;

const cloneDefaultConfig = () => JSON.parse(JSON.stringify(DEFAULT_ADMIN_CONFIG));

export default function SettingsSidebarModulesUser() {
  const { t } = useTranslation();
  const [loading, setLoading] = useState(false);
  const [statusState] = useContext(StatusContext);
  const [adminConfig, setAdminConfig] = useState(null);
  const [sidebarModulesUser, setSidebarModulesUser] = useState({});

  const {
    loading: permissionsLoading,
    hasSidebarSettingsPermission,
    isSidebarSectionAllowed,
    isSidebarModuleAllowed,
  } = useUserPermissions();
  const { refreshUserConfig } = useSidebar();

  const sectionConfigs = useMemo(
    () => [
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
            title: t('我的佣金'),
            description: t('员工自助佣金看板'),
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
            description: t('管理员工与佣金比例'),
          },
          {
            key: 'businessOverview',
            title: t('业务概览'),
            description: t('查看消耗、成本、利润与佣金统计'),
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
    ],
    [t],
  );

  const generateDefaultConfig = () => {
    const defaults = cloneDefaultConfig();
    const next = {};

    sectionConfigs.forEach((section) => {
      if (!isSidebarSectionAllowed(section.key)) return;
      next[section.key] = { enabled: true };
      section.modules.forEach((module) => {
        next[section.key][module.key] =
          defaults[section.key]?.[module.key] !== false &&
          isSidebarModuleAllowed(section.key, module.key);
      });
    });

    return next;
  };

  useEffect(() => {
    const loadConfigs = async () => {
      try {
        const mergedAdminConf = statusState?.status?.SidebarModulesAdmin
          ? mergeAdminConfig(JSON.parse(statusState.status.SidebarModulesAdmin))
          : mergeAdminConfig(null);
        setAdminConfig(mergedAdminConf);

        const userRes = await API.get('/api/user/self');
        if (userRes.data.success && userRes.data.data.sidebar_modules) {
          const raw = userRes.data.data.sidebar_modules;
          const userConf = typeof raw === 'string' ? JSON.parse(raw) : raw;
          setSidebarModulesUser({ ...generateDefaultConfig(), ...userConf });
        } else {
          setSidebarModulesUser(generateDefaultConfig());
        }
      } catch (error) {
        setAdminConfig(mergeAdminConfig(null));
        setSidebarModulesUser(generateDefaultConfig());
      }
    };

    if (!permissionsLoading && hasSidebarSettingsPermission()) {
      loadConfigs();
    }
  }, [
    statusState,
    permissionsLoading,
    hasSidebarSettingsPermission,
    isSidebarSectionAllowed,
    isSidebarModuleAllowed,
    sectionConfigs,
  ]);

  if (!permissionsLoading && !hasSidebarSettingsPermission()) {
    return null;
  }

  if (permissionsLoading) {
    return null;
  }

  const isAllowedByAdmin = (sectionKey, moduleKey = null) => {
    if (!adminConfig) return true;
    if (moduleKey) {
      return (
        adminConfig[sectionKey]?.enabled && adminConfig[sectionKey]?.[moduleKey]
      );
    }
    return adminConfig[sectionKey]?.enabled;
  };

  const visibleSections = sectionConfigs
    .filter((section) => isSidebarSectionAllowed(section.key))
    .map((section) => ({
      ...section,
      modules: section.modules.filter(
        (module) =>
          isSidebarModuleAllowed(section.key, module.key) &&
          isAllowedByAdmin(section.key, module.key),
      ),
    }))
    .filter(
      (section) => section.modules.length > 0 && isAllowedByAdmin(section.key),
    );

  const handleSectionChange = (sectionKey) => (checked) => {
    setSidebarModulesUser((prev) => ({
      ...prev,
      [sectionKey]: {
        ...prev[sectionKey],
        enabled: checked,
      },
    }));
  };

  const handleModuleChange = (sectionKey, moduleKey) => (checked) => {
    setSidebarModulesUser((prev) => ({
      ...prev,
      [sectionKey]: {
        ...prev[sectionKey],
        [moduleKey]: checked,
      },
    }));
  };

  const resetSidebarModules = () => {
    setSidebarModulesUser(generateDefaultConfig());
    showSuccess(t('已重置为默认配置'));
  };

  const onSubmit = async () => {
    setLoading(true);
    try {
      const res = await API.put('/api/user/self', {
        sidebar_modules: JSON.stringify(sidebarModulesUser),
      });
      const { success, message } = res.data;
      if (success) {
        showSuccess(t('保存成功'));
        await refreshUserConfig();
      } else {
        showError(message);
      }
    } catch (error) {
      showError(t('保存失败，请重试'));
    } finally {
      setLoading(false);
    }
  };

  return (
    <Card className='!rounded-2xl shadow-sm border-0'>
      <div className='flex items-center mb-4'>
        <Avatar size='small' color='purple' className='mr-3 shadow-md'>
          <Settings size={16} />
        </Avatar>
        <div>
          <Typography.Text className='text-lg font-medium'>
            {t('左侧边栏个人设置')}
          </Typography.Text>
          <div className='text-xs text-gray-600'>
            {t('个性化设置左侧边栏的显示内容')}
          </div>
        </div>
      </div>

      <div className='mb-4'>
        <Text type='secondary' className='text-sm text-gray-600'>
          {t('您可以个性化设置侧边栏的要显示功能')}
        </Text>
      </div>

      {visibleSections.map((section) => (
        <div key={section.key} className='mb-6'>
          <div className='flex justify-between items-center mb-4 p-4 bg-gray-50 rounded-xl border border-gray-200'>
            <div>
              <div className='font-semibold text-base text-gray-900 mb-1'>
                {section.title}
              </div>
              <Text className='text-xs text-gray-600'>{section.description}</Text>
            </div>
            <Switch
              checked={sidebarModulesUser[section.key]?.enabled !== false}
              onChange={handleSectionChange(section.key)}
              size='default'
            />
          </div>

          <Row gutter={[12, 12]}>
            {section.modules.map((module) => (
              <Col key={module.key} xs={24} sm={12} md={8} lg={6} xl={6}>
                <Card
                  className={`!rounded-xl border border-gray-200 hover:border-blue-300 transition-all duration-200 ${
                    sidebarModulesUser[section.key]?.enabled !== false
                      ? ''
                      : 'opacity-50'
                  }`}
                  bodyStyle={{ padding: '16px' }}
                  hoverable
                >
                  <div className='flex justify-between items-center h-full'>
                    <div className='flex-1 text-left'>
                      <div className='font-semibold text-sm text-gray-900 mb-1'>
                        {module.title}
                      </div>
                      <Text className='text-xs text-gray-600 leading-relaxed block'>
                        {module.description}
                      </Text>
                    </div>
                    <div className='ml-4'>
                      <Switch
                        checked={
                          sidebarModulesUser[section.key]?.[module.key] !==
                          false
                        }
                        onChange={handleModuleChange(section.key, module.key)}
                        size='default'
                        disabled={
                          sidebarModulesUser[section.key]?.enabled === false
                        }
                      />
                    </div>
                  </div>
                </Card>
              </Col>
            ))}
          </Row>
        </div>
      ))}

      <div className='flex justify-end gap-3 mt-6 pt-4 border-t border-gray-200'>
        <Button type='tertiary' onClick={resetSidebarModules} className='!rounded-lg'>
          {t('重置为默认')}
        </Button>
        <Button
          type='primary'
          onClick={onSubmit}
          loading={loading}
          className='!rounded-lg'
        >
          {t('保存设置')}
        </Button>
      </div>
    </Card>
  );
}

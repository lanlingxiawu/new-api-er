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

import React, { useEffect, useState, useMemo } from 'react';
import { Card, Spin, Modal } from '@douyinfe/semi-ui';
import { API, showError, showSuccess, toBoolean } from '../../helpers';
import SettingsAPIInfo from '../../pages/Setting/Dashboard/SettingsAPIInfo';
import SettingsAnnouncements from '../../pages/Setting/Dashboard/SettingsAnnouncements';
import SettingsFAQ from '../../pages/Setting/Dashboard/SettingsFAQ';
import SettingsUptimeKuma from '../../pages/Setting/Dashboard/SettingsUptimeKuma';
import SettingsDataDashboard from '../../pages/Setting/Dashboard/SettingsDataDashboard';
import { dashboardContentDefaults } from '../../pages/Setting/Dashboard/dashboardDefaults';

const DashboardSetting = () => {
  let [inputs, setInputs] = useState(dashboardContentDefaults);
  let [loading, setLoading] = useState(false);
  const [showMigrateModal, setShowMigrateModal] = useState(false);

  const getOptions = async () => {
    const res = await API.get('/api/option/');
    const { success, message, data } = res.data;
    if (success) {
      let newInputs = {};
      data.forEach((item) => {
        if (item.key in inputs) {
          newInputs[item.key] = item.value;
        }
        if (item.key.endsWith('Enabled') && item.key === 'DataExportEnabled') {
          newInputs[item.key] = toBoolean(item.value);
        }
      });
      setInputs(newInputs);
    } else {
      showError(message);
    }
  };

  async function onRefresh() {
    try {
      setLoading(true);
      await getOptions();
    } catch (error) {
      showError('刷新失败');
      console.error(error);
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    onRefresh();
  }, []);

  const hasLegacyData = useMemo(() => {
    const legacyKeys = [
      'ApiInfo',
      'Announcements',
      'FAQ',
      'UptimeKumaUrl',
      'UptimeKumaSlug',
    ];
    return legacyKeys.some((k) => inputs[k]);
  }, [inputs]);

  useEffect(() => {
    if (hasLegacyData) {
      setShowMigrateModal(true);
    }
  }, [hasLegacyData]);

  const handleMigrate = async () => {
    try {
      setLoading(true);
      await API.post('/api/option/migrate_console_setting');
      showSuccess('迁移完成');
      await onRefresh();
      setShowMigrateModal(false);
    } catch (err) {
      console.error(err);
      showError('迁移失败：' + (err.message || '未知错误'));
    } finally {
      setLoading(false);
    }
  };

  return (
    <>
      <Spin spinning={loading} size='large'>
        <Modal
          title='迁移设置'
          visible={showMigrateModal}
          onOk={handleMigrate}
          onCancel={() => setShowMigrateModal(false)}
          confirmLoading={loading}
          okText='开始迁移'
          cancelText='取消'
        >
          <p>检测到旧版数据看板内容设置，是否迁移到新格式？</p>
          <p style={{ color: '#f57c00', marginTop: '10px' }}>
            <strong>说明：</strong>迁移会把旧配置改写为新结构并删除旧键，如有需要请先备份原配置。
          </p>
        </Modal>

        <Card style={{ marginTop: '10px' }}>
          <SettingsDataDashboard options={inputs} refresh={onRefresh} />
        </Card>
        <Card style={{ marginTop: '10px' }}>
          <SettingsAnnouncements options={inputs} refresh={onRefresh} />
        </Card>
        <Card style={{ marginTop: '10px' }}>
          <SettingsAPIInfo options={inputs} refresh={onRefresh} />
        </Card>
        <Card style={{ marginTop: '10px' }}>
          <SettingsFAQ options={inputs} refresh={onRefresh} />
        </Card>
        <Card style={{ marginTop: '10px' }}>
          <SettingsUptimeKuma options={inputs} refresh={onRefresh} />
        </Card>
      </Spin>
    </>
  );
};

export default DashboardSetting;

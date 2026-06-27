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
import { Card, Spin } from '@douyinfe/semi-ui';
import SettingsBusinessStatsGuard from '../../pages/Setting/Operation/SettingsBusinessStatsGuard';
import SettingsLedgerPipeline from '../../pages/Setting/Operation/SettingsLedgerPipeline';
import SettingsExport from '../../pages/Setting/Operation/SettingsExport';
import SettingsLedgerDetail from '../../pages/Setting/Operation/SettingsLedgerDetail';
import SettingsFallbackBackfill from '../../pages/Setting/Operation/SettingsFallbackBackfill';
import { systemTuningDefaults } from '../../pages/Setting/Operation/systemTuningDefaults';
import { API, showError, toBoolean } from '../../helpers';

const SystemTuningSetting = () => {
  let [inputs, setInputs] = useState(systemTuningDefaults);
  let [loading, setLoading] = useState(false);

  const getOptions = async () => {
    const res = await API.get('/api/option/');
    const { success, message, data } = res.data;
    if (success) {
      let newInputs = {};
      data.forEach((item) => {
        if (typeof inputs[item.key] === 'boolean') {
          newInputs[item.key] = toBoolean(item.value);
        } else {
          newInputs[item.key] = item.value;
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
      showError('鍒锋柊澶辫触');
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    onRefresh();
  }, []);

  return (
    <>
      <Spin spinning={loading} size='large'>
        <Card style={{ marginTop: '10px' }}>
          <SettingsBusinessStatsGuard options={inputs} refresh={onRefresh} />
        </Card>
        <Card style={{ marginTop: '10px' }}>
          <SettingsLedgerPipeline options={inputs} refresh={onRefresh} />
        </Card>
        <Card style={{ marginTop: '10px' }}>
          <SettingsExport options={inputs} refresh={onRefresh} />
        </Card>
        <Card style={{ marginTop: '10px' }}>
          <SettingsLedgerDetail options={inputs} refresh={onRefresh} />
        </Card>
        <Card style={{ marginTop: '10px' }}>
          <SettingsFallbackBackfill options={inputs} refresh={onRefresh} />
        </Card>
      </Spin>
    </>
  );
};

export default SystemTuningSetting;

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

import React, { useEffect, useRef, useState } from 'react';
import {
  Banner,
  Button,
  Col,
  Form,
  Row,
  Spin,
  Typography,
} from '@douyinfe/semi-ui';
import { useTranslation } from 'react-i18next';
import {
  API,
  compareObjects,
  showError,
  showSuccess,
  showWarning,
} from '../../../helpers';

const { Text } = Typography;

const defaultInputs = {
  'business_stats_circuit_breaker_setting.enabled': true,
  'business_stats_circuit_breaker_setting.manual_disabled': false,
  'business_stats_circuit_breaker_setting.failure_threshold': 3,
  'business_stats_circuit_breaker_setting.initial_cooldown_seconds': 60,
  'business_stats_circuit_breaker_setting.max_cooldown_seconds': 3600,
  'business_stats_circuit_breaker_setting.side_effect_db_timeout_ms': 800,
};

const numericFields = [
  {
    field: 'business_stats_circuit_breaker_setting.failure_threshold',
    label: '失败阈值',
    extraText: '结算后副逻辑连续失败达到该次数后打开熔断。',
    min: 1,
    max: 1000,
    suffix: '次',
  },
  {
    field: 'business_stats_circuit_breaker_setting.initial_cooldown_seconds',
    label: '初始冷却时间',
    extraText: '第一次自动熔断打开时持续的时间。',
    min: 1,
    max: 86400,
    suffix: '秒',
  },
  {
    field: 'business_stats_circuit_breaker_setting.max_cooldown_seconds',
    label: '最大冷却时间',
    extraText: '重复失败后自动冷却时间会翻倍，并在此值停止增长。',
    min: 1,
    max: 604800,
    suffix: '秒',
  },
  {
    field: 'business_stats_circuit_breaker_setting.side_effect_db_timeout_ms',
    label: 'DB 短超时',
    extraText: '结算后查询使用的短超时时间，超时后记录为兜底失败。',
    min: 50,
    max: 30000,
    suffix: 'ms',
  },
];

export default function SettingsBusinessStatsGuard(props) {
  const { t } = useTranslation();
  const [loading, setLoading] = useState(false);
  const [inputs, setInputs] = useState(defaultInputs);
  const [inputsRow, setInputsRow] = useState(defaultInputs);
  const [status, setStatus] = useState(null);
  const refForm = useRef();

  function updateInput(key, value) {
    setInputs((origin) => ({
      ...origin,
      [key]: value,
    }));
  }

  function validateInputs() {
    for (const item of numericFields) {
      const value = Number(inputs[item.field]);
      if (!Number.isInteger(value) || value < item.min || value > item.max) {
        showError(
          t('{{label}}必须是 {{min}} 到 {{max}} 之间的整数', {
            label: t(item.label),
            min: item.min,
            max: item.max,
          }),
        );
        return false;
      }
    }
    if (
      Number(
        inputs['business_stats_circuit_breaker_setting.max_cooldown_seconds'],
      ) <
      Number(
        inputs[
          'business_stats_circuit_breaker_setting.initial_cooldown_seconds'
        ],
      )
    ) {
      showError(t('最大冷却时间必须大于或等于初始冷却时间'));
      return false;
    }
    return true;
  }

  function onSubmit() {
    const updateArray = compareObjects(inputs, inputsRow);
    if (!updateArray.length) {
      return showWarning(t('你似乎并没有修改什么'));
    }
    if (!validateInputs()) return;

    const requestQueue = updateArray.map((item) => {
      const value =
        typeof inputs[item.key] === 'boolean'
          ? String(inputs[item.key])
          : String(inputs[item.key]);
      return API.put('/api/option/', {
        key: item.key,
        value,
      });
    });

    setLoading(true);
    Promise.all(requestQueue)
      .then((res) => {
        if (res.includes(undefined)) {
          return showError(t('部分保存失败，请重试'));
        }
        showSuccess(t('保存成功'));
        props.refresh();
      })
      .catch(() => {
        showError(t('保存失败，请重试'));
      })
      .finally(() => {
        setLoading(false);
      });
  }

  async function loadCircuitBreakerStatus() {
    try {
      const res = await API.get(
        '/api/option/business-stats-circuit-breaker/status',
        { disableDuplicate: true },
      );
      const { success, data } = res.data;
      if (success) {
        setStatus(data);
      }
    } catch (err) {}
  }

  useEffect(() => {
    const currentInputs = { ...defaultInputs };
    for (const key in props.options) {
      if (Object.keys(defaultInputs).includes(key)) {
        currentInputs[key] = props.options[key];
      }
    }
    setInputs(currentInputs);
    setInputsRow(structuredClone(currentInputs));
    refForm.current?.setValues(currentInputs);
  }, [props.options]);

  useEffect(() => {
    loadCircuitBreakerStatus();
    const timer = setInterval(loadCircuitBreakerStatus, 10000);
    return () => clearInterval(timer);
  }, []);

  const enabled = inputs['business_stats_circuit_breaker_setting.enabled'];
  const manualDisabled =
    inputs['business_stats_circuit_breaker_setting.manual_disabled'];

  return (
    <Spin spinning={loading}>
      <Form
        values={inputs}
        getFormApi={(formAPI) => (refForm.current = formAPI)}
        style={{ marginBottom: 15 }}
      >
        <Form.Section text={t('结算后成本与提成保护')}>
          <Banner
            type={status?.hard_disabled ? 'danger' : 'info'}
            fullMode={false}
            closeIcon={null}
            title={
              <Text strong style={{ fontSize: 14 }}>
                {t('保护结算不受副逻辑影响')}
              </Text>
            }
            description={
              <div style={{ fontSize: 12, lineHeight: '20px' }}>
                <div>
                  {t(
                    '这些设置只影响结算后的成本台账、提成日志和业务统计。主额度结算保持隔离。',
                  )}
                </div>
                {status && (
                  <div
                    style={{
                      display: 'flex',
                      flexWrap: 'wrap',
                      columnGap: 12,
                      rowGap: 2,
                      marginTop: 4,
                    }}
                  >
                    <span>
                      {status.hard_disabled
                        ? t('运行状态：已硬熔断')
                        : status.open
                          ? t('运行状态：正在绕过副逻辑')
                          : t('运行状态：健康')}
                    </span>
                    <span>
                      {t('连续失败次数')}：{status.consecutive_failures}
                    </span>
                    {status.disabled_until > 0 && (
                      <span>
                        {t('自动熔断打开至')}：
                        {new Date(
                          status.disabled_until * 1000,
                        ).toLocaleString()}
                      </span>
                    )}
                    {status.last_reason && (
                      <span style={{ wordBreak: 'break-word' }}>
                        {t('最近错误')}：{status.last_reason}
                      </span>
                    )}
                  </div>
                )}
              </div>
            }
            style={{ marginBottom: 16 }}
          />

          <Row gutter={16}>
            <Col xs={24} sm={12} md={12} lg={12} xl={12}>
              <Form.Switch
                field={'business_stats_circuit_breaker_setting.enabled'}
                label={t('启用自动熔断')}
                size='default'
                checkedText='｜'
                uncheckedText='〇'
                onChange={(value) =>
                  updateInput(
                    'business_stats_circuit_breaker_setting.enabled',
                    value,
                  )
                }
              />
              <Text
                type='tertiary'
                size='small'
                style={{ display: 'block', marginTop: 4, marginBottom: 8 }}
              >
                {t(
                  '启用后，DB、Redis、内存或文件兜底连续失败时，会临时跳过结算后副逻辑。',
                )}
              </Text>
            </Col>
            <Col xs={24} sm={12} md={12} lg={12} xl={12}>
              <Form.Switch
                field={'business_stats_circuit_breaker_setting.manual_disabled'}
                label={t('手动禁用结算后副逻辑')}
                size='default'
                checkedText='｜'
                uncheckedText='〇'
                onChange={(value) =>
                  updateInput(
                    'business_stats_circuit_breaker_setting.manual_disabled',
                    value,
                  )
                }
              />
              <Text
                type='tertiary'
                size='small'
                style={{ display: 'block', marginTop: 4, marginBottom: 8 }}
              >
                {t(
                  '启用后，结算会立即跳过成本和提成副逻辑，并将计算后的成本/提成兜底记录写入日志文件。',
                )}
              </Text>
            </Col>
          </Row>

          {(manualDisabled || !enabled) && (
            <Banner
              type='warning'
              fullMode={false}
              closeIcon={null}
              title={t('副逻辑当前已被绕过')}
              description={
                manualDisabled
                  ? t(
                      '手动禁用已开启。新的结算后成本和提成将以计算后的兜底记录写入日志文件。',
                    )
                  : t('自动熔断已关闭，副逻辑将按配置被绕过。')
              }
              style={{ marginBottom: 16 }}
            />
          )}

          <Row gutter={16}>
            {numericFields.map((item) => (
              <Col xs={24} sm={12} md={6} lg={6} xl={6} key={item.field}>
                <Form.InputNumber
                  field={item.field}
                  label={t(item.label)}
                  min={item.min}
                  max={item.max}
                  step={1}
                  suffix={t(item.suffix)}
                  extraText={t(item.extraText)}
                  onChange={(value) => {
                    updateInput(item.field, parseInt(value));
                  }}
                />
              </Col>
            ))}
          </Row>

          <Row>
            <Button size='default' onClick={onSubmit}>
              {t('保存结算保护设置')}
            </Button>
          </Row>
        </Form.Section>
      </Form>
    </Spin>
  );
}

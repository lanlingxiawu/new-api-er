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
import { Button, Col, Form, Row, Spin, Typography } from '@douyinfe/semi-ui';
import { useTranslation } from 'react-i18next';
import {
  API,
  compareObjects,
  showError,
  showSuccess,
  showWarning,
} from '../../../helpers';
import { fallbackBackfillDefaults } from './systemTuningDefaults';

const { Text } = Typography;

const defaultInputs = fallbackBackfillDefaults;

const numericFields = [
  {
    field: 'business_stats_fallback_backfill_setting.status_cache_seconds',
    label: '状态缓存 TTL',
    extraText: '文件状态的内存缓存秒数，用于减少频繁轮询。',
    min: 1,
    suffix: '秒',
  },
  {
    field: 'business_stats_fallback_backfill_setting.max_read_line_bytes',
    label: '单行最大字节数',
    extraText: '单条 JSON 日志行最大字节数，超出后中止任务并保留文件重试。',
    min: 1,
    suffix: '字节',
  },
  {
    field: 'business_stats_fallback_backfill_setting.write_batch_size',
    label: '写入批次大小',
    extraText: '每次批量写入前缓存多少条，也是内存缓冲上限。',
    min: 1,
    suffix: '条',
  },
  {
    field: 'business_stats_fallback_backfill_setting.flush_interval_sec',
    label: '刷盘间隔',
    extraText: '两次刷盘的最小间隔秒数，设为 0 时仅按批次大小刷盘。',
    min: 0,
    suffix: '秒',
  },
  {
    field: 'business_stats_fallback_backfill_setting.batch_sleep_ms',
    label: '批次间隔睡眠',
    extraText: '每次刷盘后休眠毫秒数，用于让出 CPU 并降低瞬时数据库写入压力。',
    min: 0,
    suffix: '毫秒',
  },
];

export default function SettingsFallbackBackfill(props) {
  const { t } = useTranslation();
  const [loading, setLoading] = useState(false);
  const [inputs, setInputs] = useState(defaultInputs);
  const [inputsRow, setInputsRow] = useState(defaultInputs);
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
      if (!Number.isInteger(value) || value < item.min) {
        showError(
          t('{{label}}必须是大于等于 {{min}} 的整数', {
            label: t(item.label),
            min: item.min,
          }),
        );
        return false;
      }
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
      return API.put('/api/option/', {
        key: item.key,
        value: String(inputs[item.key]),
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

  useEffect(() => {
    const currentInputs = { ...defaultInputs };
    for (const key in props.options) {
      if (Object.keys(defaultInputs).includes(key)) {
        if (typeof defaultInputs[key] === 'boolean') {
          currentInputs[key] =
            props.options[key] === true ||
            props.options[key] === 'true' ||
            props.options[key] === '1';
        } else if (typeof defaultInputs[key] === 'number') {
          const parsed = Number(props.options[key]);
          if (!Number.isNaN(parsed)) currentInputs[key] = parsed;
        } else {
          currentInputs[key] = props.options[key];
        }
      }
    }
    setInputs(currentInputs);
    setInputsRow(structuredClone(currentInputs));
    refForm.current?.setValues(currentInputs);
  }, [props.options]);

  return (
    <Spin spinning={loading}>
      <Form
        values={inputs}
        getFormApi={(formAPI) => (refForm.current = formAPI)}
        style={{ marginBottom: 15 }}
      >
        <Form.Section text={t('兜底回填')}>
          <Row gutter={16}>
            <Col xs={24} sm={12} md={12} lg={12} xl={12}>
              <Form.Switch
                field={'business_stats_fallback_backfill_setting.enabled'}
                label={t('启用兜底回填')}
                size='default'
                onChange={(value) =>
                  updateInput(
                    'business_stats_fallback_backfill_setting.enabled',
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
                  '总开关，关闭后隐藏横幅、状态不返回文件且不能触发回填。',
                )}
              </Text>
            </Col>
          </Row>

          <Row gutter={16}>
            <Col xs={24} sm={12} md={12} lg={12} xl={12}>
              <Form.Switch
                field={'business_stats_fallback_backfill_setting.use_separate_fallback_dir'}
                label={t('使用独立兜底目录')}
                size='default'
                checkedText='｜'
                uncheckedText='〇'
                onChange={(value) =>
                  updateInput('business_stats_fallback_backfill_setting.use_separate_fallback_dir', value)
                }
              />
              <Text
                type='tertiary'
                size='small'
                style={{ display: 'block', marginTop: 4, marginBottom: 8 }}
              >
                {t('开启后兜底文件写入日志目录下的 fallback/ 子目录，与应用日志分离；关闭（默认）则共用日志目录。')}
              </Text>
            </Col>
          </Row>

          <Row gutter={16}>
            {numericFields.map((item) => (
              <Col xs={24} sm={12} md={8} lg={8} xl={8} key={item.field}>
                <Form.InputNumber
                  hideButtons
                  field={item.field}
                  label={t(item.label)}
                  min={item.min}
                  step={1}
                  suffix={item.suffix ? t(item.suffix) : undefined}
                  extraText={t(item.extraText)}
                  onChange={(value) => {
                    updateInput(item.field, Number.parseInt(value, 10));
                  }}
                />
              </Col>
            ))}
          </Row>

          <Row>
            <Button size='default' onClick={onSubmit}>
              {t('保存兜底回填设置')}
            </Button>
          </Row>
        </Form.Section>
      </Form>
    </Spin>
  );
}

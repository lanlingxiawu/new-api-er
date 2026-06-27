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
import { ledgerPipelineDefaults } from './systemTuningDefaults';

const { Text } = Typography;

const defaultInputs = ledgerPipelineDefaults;

const numericFields = [
  {
    field: 'ledger_pipeline_setting.flush_interval_sec',
    label: '刷盘间隔',
    extraText: '每轮刷盘的间隔秒数。',
    min: 1,
    suffix: '秒',
  },
  {
    field: 'ledger_pipeline_setting.outer_batch_size',
    label: '外层批次大小',
    extraText: '每轮外层循环处理的记录数。',
    min: 1,
  },
  {
    field: 'ledger_pipeline_setting.inner_batch_size',
    label: '内层批次大小',
    extraText: '每条 SQL INSERT 写入的行数。',
    min: 1,
    suffix: '行',
  },
  {
    field: 'ledger_pipeline_setting.paired_flush_max_per_cycle',
    label: '每周期最大配对数',
    extraText: '每轮最多刷出的配对记录数，完整排空时忽略。',
    min: 1,
  },
  {
    field: 'ledger_pipeline_setting.buf_max_entries',
    label: '缓冲区上限',
    extraText: '每个队列的内存缓冲上限，超出后丢弃最旧记录。',
    min: 1,
  },
  {
    field: 'ledger_pipeline_setting.dedup_mem_max_entries',
    label: '去重集合上限',
    extraText: '提成日志 ID 去重集合上限，超出后重建，DB ON CONFLICT 仍保证幂等。',
    min: 1,
  },
  {
    field: 'ledger_pipeline_setting.dedup_redis_ttl_sec',
    label: 'Redis 去重键有效期',
    extraText:
      'Redis 去重键有效期，覆盖重试窗口即可；越大越占内存，仅在启用 Redis 去重时生效。',
    min: 1,
    suffix: '秒',
  },
  {
    field: 'ledger_pipeline_setting.flush_db_timeout_sec',
    label: '刷盘写库超时',
    extraText:
      '单次刷盘写库的超时上限，超时按失败处理并退避重试，避免单条卡死的 SQL 拖垮整个刷盘循环。',
    min: 1,
    suffix: '秒',
  },
];

export default function SettingsLedgerPipeline(props) {
  const { t } = useTranslation();
  const [loading, setLoading] = useState(false);
  const [inputs, setInputs] = useState(defaultInputs);
  const [inputsRow, setInputsRow] = useState(defaultInputs);
  const [targetRpm, setTargetRpm] = useState(112500);
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
          if (!isNaN(parsed)) currentInputs[key] = parsed;
        } else {
          currentInputs[key] = props.options[key];
        }
      }
    }
    setInputs(currentInputs);
    setInputsRow(structuredClone(currentInputs));
    refForm.current?.setValues(currentInputs);
    if (
      Number(currentInputs['ledger_pipeline_setting.flush_interval_sec']) > 0 &&
      Number(currentInputs['ledger_pipeline_setting.paired_flush_max_per_cycle']) > 0
    ) {
      setTargetRpm(
        Math.round(
          (Number(currentInputs['ledger_pipeline_setting.paired_flush_max_per_cycle']) *
            60) /
            Number(currentInputs['ledger_pipeline_setting.flush_interval_sec']),
        ),
      );
    }
  }, [props.options]);

  useEffect(() => {
    let cancelled = false;

    async function fetchStatus() {
      try {
        const res = await API.get('/api/admin/system/ledger-pipeline/status');
        if (!cancelled && res.data?.success) {
          setStatus(res.data.data);
        }
      } catch {
        if (!cancelled) {
          setStatus(null);
        }
      }
    }

    fetchStatus();
    const timer = setInterval(fetchStatus, 10000);
    return () => {
      cancelled = true;
      clearInterval(timer);
    };
  }, []);

  const flushIntervalSec =
    Number(inputs['ledger_pipeline_setting.flush_interval_sec']) || 0;
  const recommendedPairedFlushMaxPerCycle =
    flushIntervalSec > 0 ? Math.ceil((targetRpm / 60) * flushIntervalSec) : 0;

  return (
    <Spin spinning={loading}>
      <Form
        values={inputs}
        getFormApi={(formAPI) => (refForm.current = formAPI)}
        style={{ marginBottom: 15 }}
      >
        <Form.Section text={t('台账流水线')}>
          {status && (
            <>
              <Row gutter={16} style={{ marginBottom: 12 }}>
                {[
                  {
                    label: '成本队列',
                    backlog: status.cost_backlog,
                    dropped: status.cost_dropped_total,
                    lastFlushItems: status.last_cost_flush_items,
                    lastFlushTookMs: status.last_cost_flush_took_ms,
                  },
                  {
                    label: '成对队列',
                    backlog: status.pair_backlog,
                    dropped: status.pair_dropped_total,
                    lastFlushItems: status.last_pair_flush_items,
                    lastFlushTookMs: status.last_pair_flush_took_ms,
                  },
                  {
                    label: '提成队列',
                    backlog: status.commission_backlog,
                    dropped: status.commission_dropped_total,
                    lastFlushItems: status.last_commission_flush_items,
                    lastFlushTookMs: status.last_commission_flush_took_ms,
                  },
                ].map(({ label, backlog, dropped, lastFlushItems, lastFlushTookMs }) => (
                  <Col xs={24} sm={12} md={8} lg={8} xl={8} key={label}>
                    <div
                      style={{
                        border: '1px solid var(--semi-color-border)',
                        borderRadius: 12,
                        padding: 12,
                      }}
                    >
                      <div style={{ fontWeight: 600, marginBottom: 6 }}>{label}</div>
                      <Text type='tertiary' size='small' style={{ display: 'block' }}>
                        积压: {backlog ?? 0}
                      </Text>
                      <Text type='tertiary' size='small' style={{ display: 'block' }}>
                        丢弃: {dropped ?? 0}
                      </Text>
                      <Text type='tertiary' size='small' style={{ display: 'block' }}>
                        最近刷盘: {lastFlushItems ?? 0}
                      </Text>
                      <Text type='tertiary' size='small' style={{ display: 'block' }}>
                        刷盘耗时: {lastFlushTookMs ?? 0} ms
                      </Text>
                    </div>
                  </Col>
                ))}
              </Row>
              <Row gutter={16} style={{ marginBottom: 12 }}>
                <Col span={24}>
                  <Text style={{ display: 'block' }}>
                    理论可承载约 {Math.round(status.theoretical_pair_rpm ?? 0)} 条/分钟。
                  </Text>
                  <Text type='tertiary' size='small' style={{ display: 'block', marginTop: 4 }}>
                    仅代表该账本刷盘链路，全链路 RPM 还会受其他环节影响。
                  </Text>
                </Col>
              </Row>
            </>
          )}
          <Row gutter={16}>
            <Col xs={24} sm={12} md={12} lg={12} xl={12}>
              <Form.Switch
                field={'ledger_pipeline_setting.full_drain'}
                label={t('完整排空')}
                size='default'
                checkedText='｜'
                uncheckedText='〇'
                onChange={(value) =>
                  updateInput('ledger_pipeline_setting.full_drain', value)
                }
              />
              <Text
                type='tertiary'
                size='small'
                style={{ display: 'block', marginTop: 4, marginBottom: 8 }}
              >
                {t('启用后每轮排空全部缓冲，忽略每周期最大配对数。')}
              </Text>
            </Col>
            <Col xs={24} sm={12} md={12} lg={12} xl={12}>
              <Form.Switch
                field={'ledger_pipeline_setting.dedup_use_redis'}
                label={t('使用 Redis 去重')}
                size='default'
                checkedText='｜'
                uncheckedText='〇'
                onChange={(value) =>
                  updateInput('ledger_pipeline_setting.dedup_use_redis', value)
                }
              />
              <Text
                type='tertiary'
                size='small'
                style={{ display: 'block', marginTop: 4, marginBottom: 8 }}
              >
                {t(
                  '多实例请开启共享 Redis 去重，避免提成重复统计；单实例可关闭。',
                )}
              </Text>
            </Col>
          </Row>

          <Row gutter={16}>
            <Col xs={24} sm={12} md={8} lg={8} xl={8}>
              <Form.InputNumber
                hideButtons
                field={'ledger_pipeline_setting.target_rpm_preview'}
                label='目标 RPM（参考）'
                min={1}
                step={1000}
                extraText={`推荐每周期配对上限：${recommendedPairedFlushMaxPerCycle}`}
                value={targetRpm}
                onChange={(value) => {
                  const parsed = parseInt(value);
                  if (!isNaN(parsed) && parsed > 0) {
                    setTargetRpm(parsed);
                  }
                }}
              />
              <Text
                type='tertiary'
                size='small'
                style={{ display: 'block', marginTop: 4 }}
              >
                修改建议：优先调大“每周期最大配对数”；仍不够再调小“刷盘间隔”。开启“完整排空”后，该推荐值仅作参考。
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
                    updateInput(item.field, parseInt(value));
                  }}
                />
              </Col>
            ))}
          </Row>

          <Row>
            <Button size='default' onClick={onSubmit}>
              {t('保存台账流水线设置')}
            </Button>
          </Row>
        </Form.Section>
      </Form>
    </Spin>
  );
}

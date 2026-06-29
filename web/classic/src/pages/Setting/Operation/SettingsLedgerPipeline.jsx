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
  // ── 吞吐量控制 ──────────────────────────────────────────
  {
    field: 'ledger_pipeline_setting.flush_interval_sec',
    label: '刷盘间隔',
    extraText: '每轮刷盘的间隔秒数。',
    min: 1,
    suffix: '秒',
  },
  // ── 批次大小 ────────────────────────────────────────────
  {
    field: 'ledger_pipeline_setting.outer_batch_size',
    label: '批次大小（结算队列）',
    extraText: '结算队列每轮外层循环处理的记录数，也是事务失败的回滚粒度。',
    min: 1,
  },
  {
    field: 'ledger_pipeline_setting.inner_batch_size',
    label: '内层批次大小（结算队列）',
    extraText: '每条 SQL INSERT 写入的行数。',
    min: 1,
    suffix: '行',
  },
  // ── 成本队列专属配置 ────────────────────────────────────
  {
    field: 'ledger_pipeline_setting.cost_outer_batch_size',
    label: '批次大小（成本队列）',
    extraText: '成本队列批量插入数据库的条数，0 时继承结算队列批次大小。',
    min: 0,
  },
  // ── 每轮上限（结算队列 / 成本队列）──────────────────────
  {
    field: 'ledger_pipeline_setting.settlement_flush_max_per_cycle',
    label: '结算队列每轮上限',
    extraText: '结算队列（成本+提成配对）每轮最多刷出条数，完整排空开关优先。',
    min: 1,
  },
  {
    field: 'ledger_pipeline_setting.cost_flush_max_per_cycle',
    label: '成本队列每轮上限',
    extraText: '成本队列（纯成本记录）每轮最多刷出条数，0 表示不限（每轮排空全部，默认）。可防止 DB 恢复时集中冲击。完整排空开关优先。',
    min: 0,
  },
  // ── 缓冲与去重 ──────────────────────────────────────────
  {
    field: 'ledger_pipeline_setting.buf_max_entries',
    label: '缓冲区上限',
    extraText: '内存缓冲上限，超出时最旧记录写入 fallback 文件（仅 fallback 队列满时永久丢失）。',
    min: 1,
  },
  {
    field: 'ledger_pipeline_setting.dedup_mem_max_entries',
    label: '去重集合上限',
    extraText: '去重集合上限，超出后重建；ON CONFLICT 保证幂等。',
    min: 1,
  },
  {
    field: 'ledger_pipeline_setting.dedup_redis_ttl_sec',
    label: 'Redis 去重键有效期',
    extraText: '覆盖重试窗口即可；越大越占内存，仅 Redis 去重时生效。',
    min: 1,
    suffix: '秒',
  },
  // ── 超时与安全边界 ──────────────────────────────────────
  {
    field: 'ledger_pipeline_setting.flush_db_timeout_sec',
    label: '刷盘写库超时',
    extraText: '超时按失败处理，防止卡死 SQL 阻塞整个刷盘循环。',
    min: 1,
    suffix: '秒',
  },
  {
    field: 'ledger_pipeline_setting.fallback_queue_capacity',
    label: '文件写入队列容量',
    extraText: '超出后拒绝入队并触发熔断器永久禁用。',
    min: 1,
  },
  {
    field: 'ledger_pipeline_setting.shutdown_timeout_sec',
    label: '停止等待超时',
    extraText: '含 goroutine 停止、最终刷盘、fallback drain；须小于 HTTP shutdown 超时（30 秒）。',
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
    const allFields = [
      ...numericFields,
      { field: 'ledger_retry_setting.retry_flush_interval_sec', label: '重试刷盘间隔', min: 1 },
      { field: 'ledger_retry_setting.stat_upsert_max_retries', label: '最大重试次数', min: 1 },
      { field: 'ledger_retry_setting.retry_queue_max_entries', label: '重试队列容量', min: 1 },
    ];
    for (const item of allFields) {
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
      Number(currentInputs['ledger_pipeline_setting.settlement_flush_max_per_cycle']) > 0
    ) {
      setTargetRpm(
        Math.round(
          (Number(currentInputs['ledger_pipeline_setting.settlement_flush_max_per_cycle']) *
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

  // Core values used for dynamic hints
  const flushIntervalSec = Math.max(1, Number(inputs['ledger_pipeline_setting.flush_interval_sec']) || 8);
  const outerBatch = Math.max(1, Number(inputs['ledger_pipeline_setting.outer_batch_size']) || 2000);
  const innerBatch = Number(inputs['ledger_pipeline_setting.inner_batch_size']) || 500;
  const settlementMax = Number(inputs['ledger_pipeline_setting.settlement_flush_max_per_cycle']) || 15000;
  const dbTimeout = Number(inputs['ledger_pipeline_setting.flush_db_timeout_sec']) || 30;
  const shutdownSec = Number(inputs['ledger_pipeline_setting.shutdown_timeout_sec']) || 25;
  const costOuterBatch = Number(inputs['ledger_pipeline_setting.cost_outer_batch_size']) || 0;
  const effectiveCostOuterBatch = costOuterBatch > 0 ? costOuterBatch : outerBatch;
  const costFlushMax = Number(inputs['ledger_pipeline_setting.cost_flush_max_per_cycle']) || 0;
  const bufMax = Number(inputs['ledger_pipeline_setting.buf_max_entries']) || 100000;
  const dedupMem = Number(inputs['ledger_pipeline_setting.dedup_mem_max_entries']) || 200000;
  const dedupTtl = Number(inputs['ledger_pipeline_setting.dedup_redis_ttl_sec']) || 21600;
  const retryInterval = Math.max(1, Number(inputs['ledger_retry_setting.retry_flush_interval_sec']) || 2);
  const maxRetries = Number(inputs['ledger_retry_setting.stat_upsert_max_retries']) || 10;
  // Minimum dedup TTL: from record entry (T=0) to last retry attempt.
  // First flush fires within flushIntervalSec; then maxRetries retry cycles at retryInterval.
  const minDedupRedisTtl = flushIntervalSec + maxRetries * retryInterval;

  const recommendedPairedFlushMaxPerCycle =
    flushIntervalSec > 0 ? Math.ceil((targetRpm / 60) * flushIntervalSec) : 0;
  const recommendedRetryIntervalSec = Math.max(1, Math.floor(flushIntervalSec / 4));

  // Dynamic extra text per field, derived from backend execution logic
  const dynamicExtraText = {
    'ledger_pipeline_setting.flush_interval_sec':
      `每轮刷盘的间隔秒数。当前配置理论吞吐 ${Math.round((settlementMax * 60) / flushIntervalSec)} 条/分钟。`,
    'ledger_pipeline_setting.outer_batch_size':
      `结算队列每轮外层循环处理的记录数，内层批次应 ≤ 此值。`,
    'ledger_pipeline_setting.inner_batch_size': innerBatch > outerBatch
      ? `⚠ 须 ≤ 结算队列批次（${outerBatch}）。`
      : `每条 SQL INSERT 写入的行数，也是事务失败的回滚粒度。推荐约批次大小 ÷ 4 = ${Math.max(1, Math.floor(outerBatch / 4))} 行。`,
    // settlement_flush_max_per_cycle 不需要是 outerBatch 整数倍：
    // 刷盘代码先取 buf[:pairedMax]，再以 outerBatch 步长循环，末批直接处理剩余记录，无害。
    'ledger_pipeline_setting.settlement_flush_max_per_cycle': settlementMax % outerBatch !== 0
      ? `结算队列（成本+提成配对）每轮上限，完整排空时忽略。约 ${Math.ceil(settlementMax / outerBatch)} 批（末批 ${settlementMax % outerBatch} 条）。`
      : `结算队列（成本+提成配对）每轮上限，完整排空时忽略。= ${settlementMax / outerBatch} 批/轮。`,
    'ledger_pipeline_setting.cost_outer_batch_size': costOuterBatch === 0
      ? `成本队列批量插入数据库的条数，0 时继承结算队列批次大小。（当前 ${outerBatch} 条）。`
      : `成本队列专属批次大小，与结算队列独立；结算队列批次仍为 ${outerBatch} 条。`,
    'ledger_pipeline_setting.cost_flush_max_per_cycle': costFlushMax === 0
      ? `成本队列（纯成本记录）设为 0 时每轮排空全部积压（当前行为）。设正整数后分批写入，余量留至下一轮。`
      : `成本队列（纯成本记录）每轮最多 ${costFlushMax} 条；完整排空开关优先。`,
    'ledger_pipeline_setting.buf_max_entries':
      `内存缓冲上限，超出时最旧记录写入 fallback 文件。当前为结算批次的 ${Math.floor(bufMax / outerBatch)} 倍。`,
    'ledger_pipeline_setting.dedup_mem_max_entries': dedupMem <= bufMax
      ? `去重集合上限，超出后重建；ON CONFLICT 保证幂等。⚠ 推荐 > 缓冲区上限（${bufMax.toLocaleString()}），否则频繁重建。`
      : `去重集合上限，超出后重建；ON CONFLICT 保证幂等。已覆盖缓冲区上限（${bufMax.toLocaleString()}）✓`,
    // 公式来源：T=0 入队（键写入）→ flushIntervalSec 内首次刷盘 → maxRetries 次重试（每次 retryInterval）。
    'ledger_pipeline_setting.dedup_redis_ttl_sec':
      `重试生命周期：${flushIntervalSec}s 刷盘 + ${maxRetries} × ${retryInterval}s = ${minDedupRedisTtl}s${dedupTtl < minDedupRedisTtl ? `；⚠ 当前值偏低，建议 ≥ ${minDedupRedisTtl}` : ' ✓'}。仅 Redis 去重时生效。`,
    'ledger_pipeline_setting.flush_db_timeout_sec': dbTimeout >= flushIntervalSec
      ? `推荐 < 刷盘间隔（${flushIntervalSec}s）——超时阻塞期间后续刷盘周期也无法启动。推荐 ${Math.max(1, flushIntervalSec - 2)}s。`
      : `推荐 ≤ ${Math.max(1, flushIntervalSec - 2)}s（刷盘间隔 ${flushIntervalSec}s - 2）。`,
    'ledger_pipeline_setting.fallback_queue_capacity':
      `超出后拒绝入队并触发熔断器永久禁用。`,
    'ledger_pipeline_setting.shutdown_timeout_sec': shutdownSec >= 30
      ? `⚠ 须 < HTTP shutdown 超时（30s）。`
      : `含 goroutine 停止 + 最终刷盘 + fallback drain。推荐 20–25s，为 HTTP shutdown（30s）留出余量。`,
  };

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
                    desc: '无提成请求成本记录',
                    backlog: status.cost_backlog,
                    capacity: status.buf_max_entries,
                    dropped: status.cost_dropped_total,
                    lastFlushItems: status.last_cost_flush_items,
                    lastFlushTookMs: status.last_cost_flush_took_ms,
                  },
                  {
                    label: '结算队列',
                    desc: '含提成请求的成本+提成配对',
                    backlog: status.pair_backlog,
                    capacity: status.buf_max_entries,
                    dropped: status.pair_dropped_total,
                    lastFlushItems: status.last_pair_flush_items,
                    lastFlushTookMs: status.last_pair_flush_took_ms,
                  },
                  {
                    label: '成本重试队列',
                    desc: '成本记录刷盘失败重试',
                    backlog: status.cost_retry_backlog,
                    capacity: status.retry_queue_max_entries,
                    dropped: null,
                    lastFlushItems: status.last_cost_retry_flush_items,
                    lastFlushTookMs: status.last_cost_retry_flush_took_ms,
                    maxRetries: status.stat_upsert_max_retries,
                  },
                  {
                    label: '结算重试队列',
                    desc: '结算配对刷盘失败重试',
                    backlog: status.pair_retry_backlog,
                    capacity: status.retry_queue_max_entries,
                    dropped: null,
                    lastFlushItems: status.last_pair_retry_flush_items,
                    lastFlushTookMs: status.last_pair_retry_flush_took_ms,
                    maxRetries: status.stat_upsert_max_retries,
                  },
                ].map(({ label, desc, backlog, capacity, dropped, lastFlushItems, lastFlushTookMs, maxRetries }) => (
                  <Col xs={24} sm={12} md={12} lg={12} xl={12} key={label}>
                    <div
                      style={{
                        border: '1px solid var(--semi-color-border)',
                        borderRadius: 12,
                        padding: 12,
                      }}
                    >
                      <div style={{ fontWeight: 600, marginBottom: 2 }}>{label}</div>
                      <Text type='quaternary' size='small' style={{ display: 'block', marginBottom: 6 }}>{desc}</Text>
                      <Text type='tertiary' size='small' style={{ display: 'block' }}>
                        积压: {(backlog ?? 0).toLocaleString()}{capacity != null ? ` / ${capacity.toLocaleString()}` : ''}
                      </Text>
                      {dropped !== null && (
                        <Text type='tertiary' size='small' style={{ display: 'block' }}>
                          丢弃: {(dropped ?? 0).toLocaleString()}
                        </Text>
                      )}
                      <Text type='tertiary' size='small' style={{ display: 'block' }}>
                        最近刷盘: {(lastFlushItems ?? 0).toLocaleString()}
                      </Text>
                      <Text type='tertiary' size='small' style={{ display: 'block' }}>
                        刷盘耗时: {lastFlushTookMs ?? 0} ms
                      </Text>
                      {maxRetries != null && (
                        <Text type='tertiary' size='small' style={{ display: 'block' }}>
                          最大重试: {maxRetries} 次
                        </Text>
                      )}
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
                优先调大”每周期最大配对数”，不够再缩短”刷盘间隔”；开启”完整排空”后此值仅供参考。
              </Text>
            </Col>
          </Row>

          <Row gutter={16}>
            {numericFields.map((item) => (
              <Col xs={24} sm={12} md={12} lg={12} xl={12} key={item.field}>
                <Form.InputNumber
                  hideButtons
                  field={item.field}
                  label={t(item.label)}
                  min={item.min}
                  step={1}
                  suffix={item.suffix ? t(item.suffix) : undefined}
                  extraText={dynamicExtraText[item.field] ?? t(item.extraText)}
                  onChange={(value) => {
                    updateInput(item.field, parseInt(value));
                  }}
                />
              </Col>
            ))}
          </Row>

          <div style={{
            borderTop: '1px solid var(--semi-color-border)',
            margin: '16px 0 12px',
            paddingTop: 16,
          }}>
            <Text style={{ fontWeight: 600, fontSize: 14 }}>{t('重试队列配置')}</Text>
            <Text type='tertiary' size='small' style={{ display: 'block', marginTop: 2, marginBottom: 12 }}>
              {t('独立 goroutine，独立容量配置，与主刷盘通过 TryLock 互斥（默认）或并发写库（开启允许并发后）。')}
            </Text>
          </div>
          <Row gutter={16} style={{ marginBottom: 8 }}>
            <Col xs={24} sm={12} md={12} lg={12} xl={12}>
              <Form.Switch
                field={'ledger_retry_setting.allow_concurrent_flush'}
                label={t('允许并发写库')}
                size='default'
                checkedText='｜'
                uncheckedText='〇'
                onChange={(value) =>
                  updateInput('ledger_retry_setting.allow_concurrent_flush', value)
                }
              />
              <Text
                type='tertiary'
                size='small'
                style={{ display: 'block', marginTop: 4, marginBottom: 8 }}
              >
                {t('关闭（默认）：重试通过 TryLock 与主刷盘互斥，主刷盘执行期间跳过本轮，避免竞争同一 DB 表的连接池。开启：重试与主刷盘并发写库，重试吞吐更高，但在写库压力大时可能争抢连接。')}
              </Text>
            </Col>
          </Row>
          <Row gutter={16} style={{ marginBottom: 12 }}>
            <Col xs={24} sm={12} md={12} lg={12} xl={12}>
              <Form.InputNumber
                hideButtons
                field={'ledger_retry_setting.retry_queue_max_entries'}
                label={t('重试队列容量')}
                min={1}
                step={1000}
                extraText={`成本重试队列和结算重试队列各自独立计算上限。超出后写入 fallback 文件。当前约为外层批次的 ${Math.floor((Number(inputs['ledger_retry_setting.retry_queue_max_entries']) || 50000) / outerBatch)} 倍。`}
                onChange={(value) => {
                  updateInput('ledger_retry_setting.retry_queue_max_entries', parseInt(value));
                }}
              />
            </Col>
            <Col xs={24} sm={12} md={12} lg={12} xl={12}>
              <Form.InputNumber
                hideButtons
                field={'ledger_retry_setting.retry_flush_interval_sec'}
                label={t('重试刷盘间隔')}
                min={1}
                step={1}
                suffix={t('秒')}
                extraText={`推荐 ≤ 刷盘间隔 ÷ 4 = ${recommendedRetryIntervalSec}s。关闭并发写库时，主刷盘持锁期间重试跳过，实际触发间隔可能略长。`}
                onChange={(value) => {
                  updateInput('ledger_retry_setting.retry_flush_interval_sec', parseInt(value));
                }}
              />
            </Col>
            <Col xs={24} sm={12} md={12} lg={12} xl={12}>
              <Form.InputNumber
                hideButtons
                field={'ledger_retry_setting.stat_upsert_max_retries'}
                label={t('最大重试次数')}
                min={1}
                step={1}
                extraText={`超出后写入 fallback 文件等待人工补录。影响 Redis 去重 TTL：${flushIntervalSec}s 刷盘 + ${maxRetries} × ${retryInterval}s = ${minDedupRedisTtl}s。`}
                onChange={(value) => {
                  updateInput('ledger_retry_setting.stat_upsert_max_retries', parseInt(value));
                }}
              />
            </Col>
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

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
import { Button, Col, Form, Row, Spin } from '@douyinfe/semi-ui';
import { useTranslation } from 'react-i18next';
import { API, compareObjects, showError, showSuccess, showWarning } from '../../../helpers';
import { ledgerDetailDefaults } from './systemTuningDefaults';

const defaultInputs = ledgerDetailDefaults;

const exportFields = [
  {
    field: 'ledger_detail_setting.export_user_cooldown_sec',
    label: '导出用户冷却时间',
    extraText: '同一用户两次导出至少间隔这么多秒，冷却内会被拒绝。',
    min: 1,
    suffix: '秒',
  },
  {
    field: 'ledger_detail_setting.export_batch_size',
    label: '导出每批读取行数',
    extraText: '每批读取的行数，会影响内存和数据库压力。',
    min: 1,
  },
  {
    field: 'ledger_detail_setting.export_batch_sleep_ms',
    label: '批次间休眠时间',
    extraText: '每批后休眠毫秒数，用于降低数据库压力。',
    min: 0,
    suffix: '毫秒',
  },
  {
    field: 'ledger_detail_setting.export_rows_per_file',
    label: '每文件最大行数',
    extraText: '达到该行数后自动分片新文件。',
    min: 1,
  },
  {
    field: 'ledger_detail_setting.export_max_range_sec',
    label: '最大导出时间跨度',
    extraText: '单次导出允许的最大时间范围。',
    min: 1,
    suffix: '秒',
  },
  {
    field: 'ledger_detail_setting.export_timeout_sec',
    label: '导出任务超时时间',
    extraText: '导出任务的最长运行时间，超时即失败。',
    min: 1,
    suffix: '秒',
  },
];

const listFields = [
  {
    field: 'ledger_detail_setting.list_max_range_sec',
    label: '列表最大时间跨度',
    extraText: '列表查询允许的最大时间范围。',
    min: 1,
    suffix: '秒',
  },
  {
    field: 'ledger_detail_setting.list_default_range_sec',
    label: '列表默认时间跨度',
    extraText: '未传时间范围时默认查这么多秒。',
    min: 1,
    suffix: '秒',
  },
  {
    field: 'ledger_detail_setting.list_default_limit',
    label: '列表默认每页行数',
    extraText: '未传 limit 时使用的默认页大小。',
    min: 1,
  },
  {
    field: 'ledger_detail_setting.list_max_limit',
    label: '列表每页最大行数',
    extraText: '单次列表请求允许的最大 limit。',
    min: 1,
  },
  {
    field: 'ledger_detail_setting.list_scan_batch_size',
    label: '列表扫描批次大小',
    extraText: '内存 tag 过滤时每批从 DB 拉取行数。',
    min: 1,
  },
  {
    field: 'ledger_detail_setting.list_scan_rows_per_req',
    label: '列表最大扫描行数',
    extraText: '单次列表最多扫描的行数，超出后返回截断结果。',
    min: 1,
  },
];

export default function SettingsLedgerDetail(props) {
  const { t } = useTranslation();
  const [loading, setLoading] = useState(false);
  const [inputs, setInputs] = useState(defaultInputs);
  const [inputsRow, setInputsRow] = useState(defaultInputs);
  const refForm = useRef();

  function updateInput(key, value) {
    setInputs((origin) => ({ ...origin, [key]: value }));
  }

  function validateInputs(fields) {
    for (const item of fields) {
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
    if (!updateArray.length) return showWarning(t('你似乎并没有修改什么'));
    if (!validateInputs([...exportFields, ...listFields])) return;

    const requestQueue = updateArray.map((item) =>
      API.put('/api/option/', { key: item.key, value: String(inputs[item.key]) }),
    );

    setLoading(true);
    Promise.all(requestQueue)
      .then((res) => {
        if (res.includes(undefined)) return showError(t('部分保存失败，请重试'));
        showSuccess(t('保存成功'));
        props.refresh();
      })
      .catch(() => showError(t('保存失败，请重试')))
      .finally(() => setLoading(false));
  }

  useEffect(() => {
    const currentInputs = { ...defaultInputs };
    for (const key in props.options) {
      if (Object.keys(defaultInputs).includes(key)) {
        const parsed = Number(props.options[key]);
        if (!isNaN(parsed)) currentInputs[key] = parsed;
      }
    }
    setInputs(currentInputs);
    setInputsRow(structuredClone(currentInputs));
    refForm.current?.setValues(currentInputs);
  }, [props.options]);

  const renderFields = (fields) => (
    <Row gutter={16}>
      {fields.map((item) => (
        <Col xs={24} sm={12} md={8} lg={8} xl={8} key={item.field}>
          <Form.InputNumber
            hideButtons
            field={item.field}
            label={t(item.label)}
            min={item.min}
            step={1}
            suffix={item.suffix ? t(item.suffix) : undefined}
            extraText={t(item.extraText)}
            onChange={(value) => updateInput(item.field, parseInt(value))}
          />
        </Col>
      ))}
    </Row>
  );

  return (
    <Spin spinning={loading}>
      <Form
        values={inputs}
        getFormApi={(formAPI) => (refForm.current = formAPI)}
        style={{ marginBottom: 15 }}
      >
        <Form.Section text={t('台账明细导出设置')}>
          {renderFields(exportFields)}
        </Form.Section>
        <Form.Section text={t('台账明细列表设置')}>
          {renderFields(listFields)}
        </Form.Section>
        <Row style={{ marginTop: 8 }}>
          <Button size='default' onClick={onSubmit}>
            {t('保存台账明细设置')}
          </Button>
        </Row>
      </Form>
    </Spin>
  );
}

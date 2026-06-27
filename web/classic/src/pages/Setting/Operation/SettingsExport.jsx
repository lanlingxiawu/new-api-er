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
import { API, compareObjects, showError, showSuccess, showWarning } from '../../../helpers';
import { exportSettingsDefaults } from './systemTuningDefaults';

const defaultInputs = exportSettingsDefaults;
const { Text } = Typography;

const numericFields = [
  {
    field: 'payment_setting.user_export_max_rows',
    label: '用户导出行数上限',
    extraText: '普通用户单次最多可导出多少行，管理员仍受硬上限约束。',
    min: 1,
    step: 1000,
  },
  {
    field: 'export_setting.rate_limit_cooldown_sec',
    label: '账单导出限流冷却时间',
    extraText: '同一用户两次导出至少间隔这么多秒，冷却内返回 429。',
    min: 1,
    suffix: '秒',
  },
  {
    field: 'export_setting.hard_ceiling_rows',
    label: '导出硬上限行数',
    extraText: '所有导出的绝对行数上限，防止异常请求打满 DB。',
    min: 1,
  },
];

const switchFields = [
  {
    field: 'export_setting.user_export_enabled',
    label: '允许普通用户导出账单',
    extraText: '关闭后普通用户不可导出，管理员导出仍可用。',
  },
];

export default function SettingsExport(props) {
  const { t } = useTranslation();
  const [loading, setLoading] = useState(false);
  const [inputs, setInputs] = useState(defaultInputs);
  const [inputsRow, setInputsRow] = useState(defaultInputs);
  const refForm = useRef();

  function updateInput(key, value) {
    setInputs((origin) => ({ ...origin, [key]: value }));
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
    if (!updateArray.length) return showWarning(t('你似乎并没有修改什么'));
    if (!validateInputs()) return;

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
        if (typeof defaultInputs[key] === 'boolean') {
          currentInputs[key] =
            props.options[key] === true ||
            props.options[key] === 'true' ||
            props.options[key] === '1';
        } else {
          const parsed = Number(props.options[key]);
          if (!isNaN(parsed)) currentInputs[key] = parsed;
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
        <Form.Section text={t('账单导出设置')}>
          <Row gutter={16}>
            {switchFields.map((item) => (
              <Col xs={24} sm={12} md={12} lg={12} xl={12} key={item.field}>
                <Form.Switch
                  field={item.field}
                  label={t(item.label)}
                  size='default'
                  checkedText='开'
                  uncheckedText='关'
                  onChange={(value) => updateInput(item.field, value)}
                />
                <Text
                  type='tertiary'
                  size='small'
                  style={{ display: 'block', marginTop: 4, marginBottom: 8 }}
                >
                  {t(item.extraText)}
                </Text>
              </Col>
            ))}
          </Row>
          <Row gutter={16}>
            {numericFields.map((item) => (
              <Col xs={24} sm={12} md={8} lg={8} xl={8} key={item.field}>
                <Form.InputNumber
                  hideButtons
                  field={item.field}
                  label={t(item.label)}
                  min={item.min}
                  step={item.step ?? 1}
                  suffix={item.suffix ? t(item.suffix) : undefined}
                  extraText={t(item.extraText)}
                  onChange={(value) => updateInput(item.field, parseInt(value))}
                />
              </Col>
            ))}
          </Row>
          <Row style={{ marginTop: 8 }}>
            <Button size='default' onClick={onSubmit}>
              {t('保存账单导出设置')}
            </Button>
          </Row>
        </Form.Section>
      </Form>
    </Spin>
  );
}

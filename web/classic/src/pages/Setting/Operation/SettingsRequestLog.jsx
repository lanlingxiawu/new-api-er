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

import React, { useEffect, useState, useRef } from 'react';
import { Button, Col, Form, Row, Spin, Typography } from '@douyinfe/semi-ui';
import { useTranslation } from 'react-i18next';
import {
  compareObjects,
  API,
  showError,
  showSuccess,
  showWarning,
} from '../../../helpers';

const { Text } = Typography;

export default function SettingsRequestLog(props) {
  const { t } = useTranslation();
  const [loading, setLoading] = useState(false);
  const [inputs, setInputs] = useState({
    RequestLogEnabled: false,
    RequestLogUsername: '',
    RequestLogMaxBodyKB: '64',
    RequestLogMinCount: '1000',
    RequestLogMaxCount: '5000',
  });
  const refForm = useRef();
  const [inputsRow, setInputsRow] = useState(inputs);

  function onSubmit() {
    const updateArray = compareObjects(inputs, inputsRow);
    if (!updateArray.length) return showWarning(t('你似乎并没有修改什么'));
    const requestQueue = updateArray.map((item) => {
      let value = '';
      if (typeof inputs[item.key] === 'boolean') {
        value = String(inputs[item.key]);
      } else {
        value = String(inputs[item.key]);
      }
      return API.put('/api/option/', {
        key: item.key,
        value,
      });
    });
    setLoading(true);
    Promise.all(requestQueue)
      .then((res) => {
        if (requestQueue.length === 1) {
          if (res.includes(undefined)) return;
        } else if (requestQueue.length > 1) {
          if (res.includes(undefined))
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
    const currentInputs = {};
    for (let key in props.options) {
      if (Object.keys(inputs).includes(key)) {
        currentInputs[key] = props.options[key];
      }
    }
    setInputs(Object.assign(inputs, currentInputs));
    setInputsRow(structuredClone(currentInputs));
    refForm.current.setValues(currentInputs);
  }, [props.options]);

  return (
    <>
      <Spin spinning={loading}>
        <Form
          values={inputs}
          getFormApi={(formAPI) => (refForm.current = formAPI)}
          style={{ marginBottom: 15 }}
        >
          <Form.Section text={t('请求日志设置')}>
            <Row gutter={16}>
              <Col xs={24} sm={12} md={8} lg={8} xl={8}>
                <Form.Switch
                  field={'RequestLogEnabled'}
                  label={t('请求记录开关')}
                  size='default'
                  checkedText='｜'
                  uncheckedText='〇'
                  onChange={(value) => {
                    setInputs({
                      ...inputs,
                      RequestLogEnabled: value,
                    });
                  }}
                />
                <Text
                  type='tertiary'
                  size='small'
                  style={{ display: 'block', marginTop: 4, marginBottom: 8 }}
                >
                  {t('开启后记录中转请求的请求头/请求体与返回头/返回体')}
                </Text>
              </Col>
              <Col xs={24} sm={12} md={8} lg={8} xl={8}>
                <Form.Input
                  field={'RequestLogUsername'}
                  label={t('记录用户名')}
                  placeholder={t('留空记录所有用户')}
                  onChange={(value) => {
                    setInputs({
                      ...inputs,
                      RequestLogUsername: value,
                    });
                  }}
                />
                <Text
                  type='tertiary'
                  size='small'
                  style={{ display: 'block', marginTop: 4, marginBottom: 8 }}
                >
                  {t('为空则记录所有用户的请求和返回；填写则仅记录该用户名')}
                </Text>
              </Col>
              <Col xs={24} sm={12} md={8} lg={8} xl={8}>
                <Form.Input
                  field={'RequestLogMaxBodyKB'}
                  label={t('单条最大体积(KB)')}
                  placeholder={'64'}
                  onChange={(value) => {
                    setInputs({
                      ...inputs,
                      RequestLogMaxBodyKB: value,
                    });
                  }}
                />
                <Text
                  type='tertiary'
                  size='small'
                  style={{ display: 'block', marginTop: 4, marginBottom: 8 }}
                >
                  {t('每个字段按此大小截断，超出部分丢弃')}
                </Text>
              </Col>
              <Col xs={24} sm={12} md={8} lg={8} xl={8}>
                <Form.Input
                  field={'RequestLogMinCount'}
                  label={t('最小日志量')}
                  placeholder={'1000'}
                  onChange={(value) => {
                    setInputs({
                      ...inputs,
                      RequestLogMinCount: value,
                    });
                  }}
                />
                <Text
                  type='tertiary'
                  size='small'
                  style={{ display: 'block', marginTop: 4, marginBottom: 8 }}
                >
                  {t('清理后保留的最新日志条数（仅存于 Redis）')}
                </Text>
              </Col>
              <Col xs={24} sm={12} md={8} lg={8} xl={8}>
                <Form.Input
                  field={'RequestLogMaxCount'}
                  label={t('最大日志量')}
                  placeholder={'5000'}
                  onChange={(value) => {
                    setInputs({
                      ...inputs,
                      RequestLogMaxCount: value,
                    });
                  }}
                />
                <Text
                  type='tertiary'
                  size='small'
                  style={{ display: 'block', marginTop: 4, marginBottom: 8 }}
                >
                  {t('日志条数超过该值时触发清理')}
                </Text>
              </Col>
            </Row>
            <Row>
              <Button size='default' onClick={onSubmit}>
                {t('保存请求日志设置')}
              </Button>
            </Row>
          </Form.Section>
        </Form>
      </Spin>
    </>
  );
}

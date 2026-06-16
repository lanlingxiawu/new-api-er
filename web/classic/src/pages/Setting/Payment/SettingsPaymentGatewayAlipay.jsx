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
import { Banner, Button, Form, Row, Col, Spin } from '@douyinfe/semi-ui';
import {
  API,
  removeTrailingSlash,
  showError,
  showSuccess,
} from '../../../helpers';
import { useTranslation } from 'react-i18next';
import { BookOpen } from 'lucide-react';

export default function SettingsPaymentGatewayAlipay(props) {
  const { t } = useTranslation();
  const sectionTitle = props.hideSectionTitle ? undefined : t('支付宝设置');
  const [loading, setLoading] = useState(false);
  const [inputs, setInputs] = useState({
    AlipayEnabled: false,
    AlipaySandbox: false,
    AlipayAppId: '',
    AlipayPrivateKey: '',
    AlipayPublicKey: '',
    AlipayMinTopUp: 1,
    AlipayNotifyUrl: '',
    AlipayReturnUrl: '',
  });
  const [originInputs, setOriginInputs] = useState({});
  const formApiRef = useRef(null);

  useEffect(() => {
    if (props.options && formApiRef.current) {
      const currentInputs = {
        AlipayEnabled:
          props.options.AlipayEnabled !== undefined
            ? props.options.AlipayEnabled
            : false,
        AlipaySandbox:
          props.options.AlipaySandbox !== undefined
            ? props.options.AlipaySandbox
            : false,
        AlipayAppId: props.options.AlipayAppId || '',
        AlipayPrivateKey: '',
        AlipayPublicKey: '',
        AlipayMinTopUp:
          props.options.AlipayMinTopUp !== undefined
            ? parseFloat(props.options.AlipayMinTopUp)
            : 1,
        AlipayNotifyUrl: props.options.AlipayNotifyUrl || '',
        AlipayReturnUrl: props.options.AlipayReturnUrl || '',
      };
      setInputs(currentInputs);
      setOriginInputs({ ...currentInputs });
      formApiRef.current.setValues(currentInputs);
    }
  }, [props.options]);

  const handleFormChange = (values) => {
    setInputs(values);
  };

  const submitAlipaySetting = async () => {
    setLoading(true);
    try {
      const options = [];

      if (
        originInputs['AlipayEnabled'] !== inputs.AlipayEnabled &&
        inputs.AlipayEnabled !== undefined
      ) {
        options.push({
          key: 'AlipayEnabled',
          value: inputs.AlipayEnabled ? 'true' : 'false',
        });
      }
      if (
        originInputs['AlipaySandbox'] !== inputs.AlipaySandbox &&
        inputs.AlipaySandbox !== undefined
      ) {
        options.push({
          key: 'AlipaySandbox',
          value: inputs.AlipaySandbox ? 'true' : 'false',
        });
      }
      if (inputs.AlipayAppId !== '') {
        options.push({ key: 'AlipayAppId', value: inputs.AlipayAppId });
      }
      if (inputs.AlipayPrivateKey && inputs.AlipayPrivateKey.trim() !== '') {
        options.push({
          key: 'AlipayPrivateKey',
          value: inputs.AlipayPrivateKey,
        });
      }
      if (inputs.AlipayPublicKey && inputs.AlipayPublicKey.trim() !== '') {
        options.push({
          key: 'AlipayPublicKey',
          value: inputs.AlipayPublicKey,
        });
      }
      if (
        inputs.AlipayMinTopUp !== undefined &&
        inputs.AlipayMinTopUp !== null
      ) {
        options.push({
          key: 'AlipayMinTopUp',
          value: inputs.AlipayMinTopUp.toString(),
        });
      }
      if (inputs.AlipayNotifyUrl !== '') {
        options.push({
          key: 'AlipayNotifyUrl',
          value: inputs.AlipayNotifyUrl,
        });
      }
      if (inputs.AlipayReturnUrl !== '') {
        options.push({
          key: 'AlipayReturnUrl',
          value: inputs.AlipayReturnUrl,
        });
      }

      if (options.length === 0) {
        showSuccess(t('更新成功'));
        return;
      }

      const results = await Promise.all(
        options.map((opt) =>
          API.put('/api/option/', { key: opt.key, value: opt.value }),
        ),
      );

      const errorResults = results.filter((res) => !res.data.success);
      if (errorResults.length > 0) {
        errorResults.forEach((res) => showError(res.data.message));
      } else {
        showSuccess(t('更新成功'));
        setOriginInputs({ ...inputs, AlipayPrivateKey: '', AlipayPublicKey: '' });
        props.refresh?.();
      }
    } catch (error) {
      showError(t('更新失败'));
    } finally {
      setLoading(false);
    }
  };

  return (
    <Spin spinning={loading}>
      <Form
        initValues={inputs}
        onValueChange={handleFormChange}
        getFormApi={(api) => (formApiRef.current = api)}
      >
        <Form.Section text={sectionTitle}>
          <Banner
            type='info'
            icon={<BookOpen size={16} />}
            description={
              <>
                {t(
                  '请在支付宝开放平台获取 App ID、应用私钥和支付宝公钥（公钥模式），并在下方填写。异步通知和同步跳转地址留空则使用系统默认值。',
                )}
                <br />
                {t('默认异步通知地址')}：
                {props.options.ServerAddress
                  ? removeTrailingSlash(props.options.ServerAddress)
                  : t('网站地址')}
                /api/alipay/notify
              </>
            }
            style={{ marginBottom: 12 }}
          />
          <Row gutter={{ xs: 8, sm: 16, md: 24, lg: 24, xl: 24, xxl: 24 }}>
            <Col xs={24} sm={24} md={8} lg={8} xl={8}>
              <Form.Switch
                field='AlipayEnabled'
                size='default'
                checkedText='｜'
                uncheckedText='〇'
                label={t('启用支付宝官方支付')}
              />
            </Col>
            <Col xs={24} sm={24} md={8} lg={8} xl={8}>
              <Form.Switch
                field='AlipaySandbox'
                size='default'
                checkedText='｜'
                uncheckedText='〇'
                label={t('沙箱环境')}
              />
            </Col>
            <Col xs={24} sm={24} md={8} lg={8} xl={8}>
              <Form.InputNumber
                field='AlipayMinTopUp'
                label={t('最低充值数量')}
                placeholder={t('例如：1')}
                extraText={t('用户单次最少可充值的数量')}
                min={1}
                step={1}
                style={{ width: '100%' }}
              />
            </Col>
          </Row>
          <Row
            gutter={{ xs: 8, sm: 16, md: 24, lg: 24, xl: 24, xxl: 24 }}
            style={{ marginTop: 16 }}
          >
            <Col xs={24} sm={24} md={12} lg={12} xl={12}>
              <Form.Input
                field='AlipayAppId'
                label={t('App ID')}
                placeholder={t('支付宝开放平台应用 App ID')}
              />
            </Col>
            <Col xs={24} sm={24} md={12} lg={12} xl={12}>
              <Form.Input
                field='AlipayNotifyUrl'
                label={t('异步通知地址')}
                placeholder={t('留空则使用系统默认地址')}
              />
            </Col>
          </Row>
          <Row
            gutter={{ xs: 8, sm: 16, md: 24, lg: 24, xl: 24, xxl: 24 }}
            style={{ marginTop: 16 }}
          >
            <Col xs={24} sm={24} md={12} lg={12} xl={12}>
              <Form.TextArea
                field='AlipayPrivateKey'
                label={t('应用私钥')}
                placeholder={t('填写后覆盖当前私钥，留空表示保持当前不变')}
                extraText={t('应用私钥（RSA2），保存后不会回显')}
                type='password'
                autosize={{ minRows: 3, maxRows: 6 }}
              />
            </Col>
            <Col xs={24} sm={24} md={12} lg={12} xl={12}>
              <Form.TextArea
                field='AlipayPublicKey'
                label={t('支付宝公钥')}
                placeholder={t('填写后覆盖当前公钥，留空表示保持当前不变')}
                extraText={t('支付宝公钥（公钥模式），保存后不会回显')}
                type='password'
                autosize={{ minRows: 3, maxRows: 6 }}
              />
            </Col>
          </Row>
          <Row
            gutter={{ xs: 8, sm: 16, md: 24, lg: 24, xl: 24, xxl: 24 }}
            style={{ marginTop: 16 }}
          >
            <Col xs={24}>
              <Form.Input
                field='AlipayReturnUrl'
                label={t('同步跳转地址')}
                placeholder={t(
                  '留空则使用系统默认地址（例如：https://example.com/console/topup）',
                )}
              />
            </Col>
          </Row>
          <Button onClick={submitAlipaySetting}>
            {t('更新支付宝设置')}
          </Button>
        </Form.Section>
      </Form>
    </Spin>
  );
}

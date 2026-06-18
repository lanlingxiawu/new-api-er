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

const toBoolean = (value) => value === true || value === 'true';

export default function SettingsPaymentGatewayInfini(props) {
  const { t } = useTranslation();
  const sectionTitle = props.hideSectionTitle ? undefined : t('Infini 支付设置');
  const [loading, setLoading] = useState(false);
  const [inputs, setInputs] = useState({
    InfiniEnabled: false,
    InfiniApiKey: '',
    InfiniApiSecret: '',
    InfiniWebhookSecret: '',
    InfiniSandbox: false,
    InfiniNotifyUrl: '',
    InfiniReturnUrl: '',
    InfiniFailUrl: '',
    InfiniUnitPrice: 1.0,
    InfiniMinTopUp: 1,
    InfiniCurrency: 'USD',
    InfiniCurrencies: '',
    InfiniPayMethods: '',
  });
  const [originInputs, setOriginInputs] = useState({});
  const formApiRef = useRef(null);

  useEffect(() => {
    if (props.options && formApiRef.current) {
      const currentInputs = {
        InfiniEnabled: toBoolean(props.options.InfiniEnabled),
        InfiniApiKey: props.options.InfiniApiKey || '',
        InfiniApiSecret: '',
        InfiniWebhookSecret: '',
        InfiniSandbox: toBoolean(props.options.InfiniSandbox),
        InfiniNotifyUrl: props.options.InfiniNotifyUrl || '',
        InfiniReturnUrl: props.options.InfiniReturnUrl || '',
        InfiniFailUrl: props.options.InfiniFailUrl || '',
        InfiniUnitPrice: parseFloat(props.options.InfiniUnitPrice) || 1.0,
        InfiniMinTopUp: parseInt(props.options.InfiniMinTopUp) || 1,
        InfiniCurrency: props.options.InfiniCurrency || 'USD',
        InfiniCurrencies: props.options.InfiniCurrencies || '',
        InfiniPayMethods: props.options.InfiniPayMethods || '',
      };
      setInputs(currentInputs);
      setOriginInputs({ ...currentInputs });
      formApiRef.current.setValues(currentInputs);
    }
  }, [props.options]);

  const handleFormChange = (values) => {
    setInputs(values);
  };

  const submitInfiniSetting = async () => {
    setLoading(true);
    try {
      const options = [];

      options.push({
        key: 'InfiniEnabled',
        value: inputs.InfiniEnabled ? 'true' : 'false',
      });
      options.push({
        key: 'InfiniSandbox',
        value: inputs.InfiniSandbox ? 'true' : 'false',
      });

      if (inputs.InfiniApiKey !== '') {
        options.push({ key: 'InfiniApiKey', value: inputs.InfiniApiKey });
      }
      if (inputs.InfiniApiSecret && inputs.InfiniApiSecret.trim() !== '') {
        options.push({ key: 'InfiniApiSecret', value: inputs.InfiniApiSecret });
      }
      if (inputs.InfiniWebhookSecret && inputs.InfiniWebhookSecret.trim() !== '') {
        options.push({ key: 'InfiniWebhookSecret', value: inputs.InfiniWebhookSecret });
      }

      options.push({ key: 'InfiniNotifyUrl', value: inputs.InfiniNotifyUrl || '' });
      options.push({ key: 'InfiniReturnUrl', value: inputs.InfiniReturnUrl || '' });
      options.push({ key: 'InfiniFailUrl', value: inputs.InfiniFailUrl || '' });
      options.push({ key: 'InfiniUnitPrice', value: String(inputs.InfiniUnitPrice || 1.0) });
      options.push({ key: 'InfiniMinTopUp', value: String(inputs.InfiniMinTopUp || 1) });
      options.push({ key: 'InfiniCurrency', value: inputs.InfiniCurrency || 'USD' });
      options.push({ key: 'InfiniCurrencies', value: inputs.InfiniCurrencies || '' });
      options.push({ key: 'InfiniPayMethods', value: inputs.InfiniPayMethods || '' });

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
        setOriginInputs({
          ...inputs,
          InfiniApiSecret: '',
          InfiniWebhookSecret: '',
        });
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
                  '在 business.infini.money 注册商户账号，在开发者页面创建 API Key 并复制 keyId 和 Secret，配置 Webhook 端点。所有密钥仅保存在服务端。',
                )}
                <br />
                {t('默认 Webhook 回调地址')}：
                {props.options?.ServerAddress
                  ? removeTrailingSlash(props.options.ServerAddress)
                  : t('网站地址')}
                /api/infini/webhook
              </>
            }
            style={{ marginBottom: 12 }}
          />

          <Row gutter={{ xs: 8, sm: 16, md: 24 }}>
            <Col xs={24} sm={12} md={6}>
              <Form.Switch
                field='InfiniEnabled'
                size='default'
                checkedText='｜'
                uncheckedText='〇'
                label={t('启用 Infini 支付')}
              />
            </Col>
            <Col xs={24} sm={12} md={6}>
              <Form.Switch
                field='InfiniSandbox'
                size='default'
                checkedText='｜'
                uncheckedText='〇'
                label={t('沙箱模式')}
              />
            </Col>
            <Col xs={24} sm={12} md={6}>
              <Form.InputNumber
                field='InfiniMinTopUp'
                label={t('最低充值数量')}
                placeholder='1'
                min={1}
                step={1}
                style={{ width: '100%' }}
              />
            </Col>
            <Col xs={24} sm={12} md={6}>
              <Form.InputNumber
                field='InfiniUnitPrice'
                label={t('单价（每单位 USD）')}
                placeholder='1.0'
                min={0}
                step={0.01}
                precision={4}
                style={{ width: '100%' }}
              />
            </Col>
          </Row>

          <Row gutter={{ xs: 8, sm: 16, md: 24 }} style={{ marginTop: 16 }}>
            <Col xs={24} sm={12} md={8}>
              <Form.Input
                field='InfiniApiKey'
                label={t('API Key（keyId）')}
                placeholder={t('Infini keyId')}
              />
            </Col>
            <Col xs={24} sm={12} md={8}>
              <Form.Input
                field='InfiniApiSecret'
                label={t('API Secret（签名密钥）')}
                placeholder={t('填写后覆盖当前密钥，留空表示保持当前不变')}
                extraText={t('HMAC-SHA256 签名密钥，保存后不会回显')}
                type='password'
              />
            </Col>
            <Col xs={24} sm={12} md={8}>
              <Form.Input
                field='InfiniWebhookSecret'
                label={t('Webhook 密钥')}
                placeholder={t('填写后覆盖当前密钥，留空表示保持当前不变')}
                extraText={t('Webhook 验签密钥，保存后不会回显')}
                type='password'
              />
            </Col>
          </Row>

          <Row gutter={{ xs: 8, sm: 16, md: 24 }} style={{ marginTop: 16 }}>
            <Col xs={24} sm={12} md={4}>
              <Form.Input
                field='InfiniCurrency'
                label={t('结算币种')}
                placeholder='USD'
              />
            </Col>
            <Col xs={24} sm={12} md={7}>
              <Form.Input
                field='InfiniNotifyUrl'
                label={t('Webhook 回调地址')}
                placeholder={t('留空则使用系统默认地址')}
              />
            </Col>
            <Col xs={24} sm={12} md={7}>
              <Form.Input
                field='InfiniReturnUrl'
                label={t('支付成功跳转地址')}
                placeholder={t('留空则使用系统默认地址')}
              />
            </Col>
            <Col xs={24} sm={12} md={6}>
              <Form.Input
                field='InfiniFailUrl'
                label={t('支付失败跳转地址')}
                placeholder={t('留空则使用系统默认地址')}
              />
            </Col>
          </Row>

          <Row gutter={{ xs: 8, sm: 16, md: 24 }} style={{ marginTop: 16 }}>
            <Col xs={24}>
              <Form.TextArea
                field='InfiniCurrencies'
                label={t('多币种配置（JSON）')}
                placeholder='[{"currency":"USD","unit_price":1.0,"min_topup":1},{"currency":"EUR","unit_price":0.92,"min_topup":1}]'
                extraText={t('JSON 数组，每项配置一个可供用户选择的结算币种，配置后覆盖上方单币种设置。字段说明：currency = 大写币种代码（支持 USD/EUR/GBP/SGD/AUD/HKD/JPY/KRW）；unit_price = 每个额度单位对应的该币种金额（如 $1/单位 填 1.0，€0.92/单位 填 0.92）；min_topup = 该币种最低充值单位数（JPY/KRW 为零小数位币种，建议填整数）')}
                autosize={{ minRows: 3, maxRows: 6 }}
                style={{ fontFamily: 'monospace', fontSize: 12 }}
              />
            </Col>
          </Row>

          <Row gutter={{ xs: 8, sm: 16, md: 24 }} style={{ marginTop: 16 }}>
            <Col xs={24}>
              <Form.Input
                field='InfiniPayMethods'
                label={t('限定支付方式（JSON）')}
                placeholder='[1,2]'
                extraText={t('1=加密货币, 2=银行卡, 3=Binance Pay, 5=Apple Pay, 6=Google Pay；留空则使用商户控制台默认配置')}
                style={{ fontFamily: 'monospace' }}
              />
            </Col>
          </Row>

          <Button
            onClick={submitInfiniSetting}
            style={{ marginTop: 16 }}
          >
            {t('更新 Infini 设置')}
          </Button>
        </Form.Section>
      </Form>
    </Spin>
  );
}

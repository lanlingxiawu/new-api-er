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

export default function SettingsPaymentGatewayWechat(props) {
  const { t } = useTranslation();
  const sectionTitle = props.hideSectionTitle ? undefined : t('微信支付设置');
  const [loading, setLoading] = useState(false);
  const [inputs, setInputs] = useState({
    WechatEnabled: false,
    WechatAppId: '',
    WechatMchId: '',
    WechatApiV3Key: '',
    WechatMchPrivateKey: '',
    WechatMchCertSerialNo: '',
    WechatMinTopUp: 1,
    WechatNotifyUrl: '',
  });
  const [originInputs, setOriginInputs] = useState({});
  const formApiRef = useRef(null);

  useEffect(() => {
    if (props.options && formApiRef.current) {
      const currentInputs = {
        WechatEnabled:
          props.options.WechatEnabled !== undefined
            ? props.options.WechatEnabled
            : false,
        WechatAppId: props.options.WechatAppId || '',
        WechatMchId: props.options.WechatMchId || '',
        WechatApiV3Key: '',
        WechatMchPrivateKey: '',
        WechatMchCertSerialNo: props.options.WechatMchCertSerialNo || '',
        WechatMinTopUp:
          props.options.WechatMinTopUp !== undefined
            ? parseFloat(props.options.WechatMinTopUp)
            : 1,
        WechatNotifyUrl: props.options.WechatNotifyUrl || '',
      };
      setInputs(currentInputs);
      setOriginInputs({ ...currentInputs });
      formApiRef.current.setValues(currentInputs);
    }
  }, [props.options]);

  const handleFormChange = (values) => {
    setInputs(values);
  };

  const submitWechatSetting = async () => {
    setLoading(true);
    try {
      const options = [];

      if (
        originInputs['WechatEnabled'] !== inputs.WechatEnabled &&
        inputs.WechatEnabled !== undefined
      ) {
        options.push({
          key: 'WechatEnabled',
          value: inputs.WechatEnabled ? 'true' : 'false',
        });
      }
      if (inputs.WechatAppId !== '') {
        options.push({ key: 'WechatAppId', value: inputs.WechatAppId });
      }
      if (inputs.WechatMchId !== '') {
        options.push({ key: 'WechatMchId', value: inputs.WechatMchId });
      }
      if (inputs.WechatApiV3Key && inputs.WechatApiV3Key.trim() !== '') {
        options.push({
          key: 'WechatApiV3Key',
          value: inputs.WechatApiV3Key,
        });
      }
      if (
        inputs.WechatMchPrivateKey &&
        inputs.WechatMchPrivateKey.trim() !== ''
      ) {
        options.push({
          key: 'WechatMchPrivateKey',
          value: inputs.WechatMchPrivateKey,
        });
      }
      if (inputs.WechatMchCertSerialNo !== '') {
        options.push({
          key: 'WechatMchCertSerialNo',
          value: inputs.WechatMchCertSerialNo,
        });
      }
      if (
        inputs.WechatMinTopUp !== undefined &&
        inputs.WechatMinTopUp !== null
      ) {
        options.push({
          key: 'WechatMinTopUp',
          value: inputs.WechatMinTopUp.toString(),
        });
      }
      if (inputs.WechatNotifyUrl !== '') {
        options.push({
          key: 'WechatNotifyUrl',
          value: inputs.WechatNotifyUrl,
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
        setOriginInputs({
          ...inputs,
          WechatApiV3Key: '',
          WechatMchPrivateKey: '',
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
                  '请在微信支付商户平台获取 AppID、商户号、APIv3 密钥以及商户 API 证书序列号和私钥，并在下方填写。异步通知地址留空则使用系统默认值。',
                )}
                <br />
                {t('默认异步通知地址')}：
                {props.options.ServerAddress
                  ? removeTrailingSlash(props.options.ServerAddress)
                  : t('网站地址')}
                /api/wechat/notify
              </>
            }
            style={{ marginBottom: 12 }}
          />
          <Row gutter={{ xs: 8, sm: 16, md: 24, lg: 24, xl: 24, xxl: 24 }}>
            <Col xs={24} sm={24} md={8} lg={8} xl={8}>
              <Form.Switch
                field='WechatEnabled'
                size='default'
                checkedText='｜'
                uncheckedText='〇'
                label={t('启用微信官方支付')}
              />
            </Col>
            <Col xs={24} sm={24} md={8} lg={8} xl={8}>
              <Form.InputNumber
                field='WechatMinTopUp'
                label={t('最低充值数量')}
                placeholder={t('例如：1')}
                extraText={t('用户单次最少可充值的数量')}
                min={1}
                step={1}
                style={{ width: '100%' }}
              />
            </Col>
            <Col xs={24} sm={24} md={8} lg={8} xl={8}>
              <Form.Input
                field='WechatNotifyUrl'
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
              <Form.Input
                field='WechatAppId'
                label={t('App ID')}
                placeholder={t('公众号 / 小程序 / APP AppID')}
              />
            </Col>
            <Col xs={24} sm={24} md={12} lg={12} xl={12}>
              <Form.Input
                field='WechatMchId'
                label={t('商户号')}
                placeholder={t('微信支付商户号')}
              />
            </Col>
          </Row>
          <Row
            gutter={{ xs: 8, sm: 16, md: 24, lg: 24, xl: 24, xxl: 24 }}
            style={{ marginTop: 16 }}
          >
            <Col xs={24} sm={24} md={12} lg={12} xl={12}>
              <Form.Input
                field='WechatApiV3Key'
                label={t('APIv3 密钥')}
                placeholder={t('填写后覆盖当前密钥，留空表示保持当前不变')}
                extraText={t('32 字节 APIv3 密钥，保存后不会回显')}
                type='password'
              />
            </Col>
            <Col xs={24} sm={24} md={12} lg={12} xl={12}>
              <Form.Input
                field='WechatMchCertSerialNo'
                label={t('商户 API 证书序列号')}
                placeholder={t('例如：1DEDFA********')}
              />
            </Col>
          </Row>
          <Row
            gutter={{ xs: 8, sm: 16, md: 24, lg: 24, xl: 24, xxl: 24 }}
            style={{ marginTop: 16 }}
          >
            <Col xs={24}>
              <Form.TextArea
                field='WechatMchPrivateKey'
                label={t('商户 API 私钥')}
                placeholder={t('填写后覆盖当前私钥，留空表示保持当前不变')}
                extraText={t('商户 API 私钥（PEM 格式），保存后不会回显')}
                type='password'
                autosize={{ minRows: 3, maxRows: 6 }}
              />
            </Col>
          </Row>
          <Button onClick={submitWechatSetting}>
            {t('更新微信支付设置')}
          </Button>
        </Form.Section>
      </Form>
    </Spin>
  );
}

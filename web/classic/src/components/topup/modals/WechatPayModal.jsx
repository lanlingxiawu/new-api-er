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

import React, { useEffect, useRef } from 'react';
import { Modal, Typography } from '@douyinfe/semi-ui';
import { QRCodeSVG } from 'qrcode.react';
import { CheckCircle2, Loader2, XCircle } from 'lucide-react';
import { SiWechat } from 'react-icons/si';

const { Text } = Typography;

const POLL_INTERVAL_MS = 3000;

const WechatPayModal = ({
  t,
  visible,
  onCancel,
  codeUrl,
  status,
  renderAmount,
  onCheckStatus,
  onSuccess,
}) => {
  const onCheckStatusRef = useRef(onCheckStatus);
  onCheckStatusRef.current = onCheckStatus;
  const onSuccessRef = useRef(onSuccess);
  onSuccessRef.current = onSuccess;

  // 在弹窗打开且订单处于待支付状态时，定时轮询订单状态
  useEffect(() => {
    if (!visible || status !== 'pending') {
      return;
    }
    const timer = setInterval(async () => {
      const result = await onCheckStatusRef.current();
      if (result === 'success') {
        onSuccessRef.current();
      }
    }, POLL_INTERVAL_MS);
    return () => clearInterval(timer);
  }, [visible, status]);

  return (
    <Modal
      title={
        <div className='flex items-center'>
          <SiWechat className='mr-2' size={18} color='#07C160' />
          {t('微信支付')}
        </div>
      }
      visible={visible}
      onCancel={onCancel}
      footer={null}
      maskClosable={false}
      centered
      size='small'
    >
      <div className='flex flex-col items-center gap-4 py-4'>
        {status === 'success' ? (
          <div className='flex flex-col items-center gap-2 py-6'>
            <CheckCircle2 size={48} className='text-green-600' />
            <Text strong>{t('支付成功')}</Text>
          </div>
        ) : status === 'failed' ? (
          <div className='flex flex-col items-center gap-2 py-6'>
            <XCircle size={48} className='text-red-500' />
            <Text strong>{t('支付失败')}</Text>
          </div>
        ) : codeUrl ? (
          <>
            <div className='flex justify-center rounded-lg bg-white p-4'>
              <QRCodeSVG value={codeUrl} size={200} />
            </div>
            <div className='flex items-center gap-2 text-sm text-gray-500'>
              <Loader2 size={16} className='animate-spin' />
              <span>{t('等待支付...')}</span>
            </div>
            <Text type='secondary'>
              {t('实付金额：')}
              <span style={{ color: 'red' }}>{renderAmount()}</span>
            </Text>
          </>
        ) : (
          <Loader2 size={32} className='animate-spin' />
        )}
      </div>
    </Modal>
  );
};

export default WechatPayModal;

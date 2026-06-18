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

import React from 'react';
import { Modal, Typography, Card, Skeleton } from '@douyinfe/semi-ui';
import { SiAlipay, SiWechat, SiStripe } from 'react-icons/si';
import { CreditCard } from 'lucide-react';

const { Text } = Typography;

const PaymentConfirmModal = ({
  t,
  open,
  onlineTopUp,
  handleCancel,
  confirmLoading,
  topUpCount,
  renderQuotaWithAmount,
  amountLoading,
  renderAmount,
  payWay,
  payMethods,
  // 新增：用于显示折扣明细
  amountNumber,
  discountRate,
  // Binance P2P 实时汇率
  binanceRate,
  // 系统充值比例 = operation_setting.Price（CNY/额度单位），后台可配置
  priceRatio,
}) => {
  const hasDiscount =
    discountRate && discountRate > 0 && discountRate < 1 && amountNumber > 0;
  const originalAmount = hasDiscount ? amountNumber / discountRate : 0;
  const discountAmount = hasDiscount ? originalAmount - amountNumber : 0;

  const isInfini = payWay === 'infini' || (payWay && payWay.startsWith('infini:'));
  const normalizedPayWay = isInfini ? 'infini' : payWay;
  const infiniCurrency = isInfini && payWay.startsWith('infini:') ? payWay.split(':')[1] : 'USD';
  // Binance 实时汇率
  const effectiveRate = binanceRate > 0 ? binanceRate : 0;
  const safePrice = priceRatio > 0 ? priceRatio : 1;
  const quotaPerUnit = Number(localStorage.getItem('quota_per_unit') || 500000);
  // 实际到账按 raw quota 快照计算，再换回展示金额，和后端精度一致。
  const expectedCreditUnits = isInfini && amountNumber > 0 && effectiveRate > 0
    ? Math.round(amountNumber * effectiveRate * quotaPerUnit / safePrice) / quotaPerUnit
    : 0;
  // CNY 总等值
  const cnyEquivalent = isInfini && effectiveRate > 0 && amountNumber > 0
    ? amountNumber * effectiveRate
    : 0;
  return (
    <Modal
      title={
        <div className='flex items-center'>
          <CreditCard className='mr-2' size={18} />
          {t('充值确认')}
        </div>
      }
      visible={open}
      onOk={onlineTopUp}
      onCancel={handleCancel}
      maskClosable={false}
      size='small'
      centered
      confirmLoading={confirmLoading}
    >
      <div className='space-y-4'>
        <Card className='!rounded-xl !border-0 bg-slate-50 dark:bg-slate-800'>
          <div className='space-y-3'>
            <div className='flex justify-between items-center'>
              <Text strong className='text-slate-700 dark:text-slate-200'>
                {t('充值数量')}：
              </Text>
              <Text className='text-slate-900 dark:text-slate-100'>
                {renderQuotaWithAmount(topUpCount)}
              </Text>
            </div>
            <div className='flex justify-between items-center'>
              <Text strong className='text-slate-700 dark:text-slate-200'>
                {t('实付金额')}：
              </Text>
              {amountLoading ? (
                <Skeleton.Title style={{ width: '60px', height: '16px' }} />
              ) : (
                <div className='flex items-baseline space-x-2'>
                  <Text strong className='font-bold' style={{ color: 'red' }}>
                    {payWay && (payWay === 'infini' || payWay.startsWith('infini:'))
                      ? `${amountNumber} ${payWay.startsWith('infini:') ? payWay.split(':')[1] : 'USD'}`
                      : renderAmount()}
                  </Text>
                  {hasDiscount && (
                    <Text size='small' className='text-rose-500'>
                      {Math.round(discountRate * 100)}%
                    </Text>
                  )}
                </div>
              )}
            </div>
            {/* Infini：到账额度（系统配置单位）+ 充值比例 + CNY 换算 */}
            {isInfini && !amountLoading && topUpCount > 0 && (
              <div
                className='rounded-lg px-3 py-3 space-y-3'
                style={{ background: 'var(--semi-color-fill-0)' }}
              >
                {/* 行1：实际到账 ≈ paymentUSD × binanceRate / Price（浮动汇率，约等于） */}
                <div className='flex justify-between items-center'>
                  <Text size='small' className='text-slate-500 dark:text-slate-400'>
                    {t('实际到账')}
                  </Text>
                  <Text
                    strong
                    style={{
                      color: 'var(--semi-color-success)',
                      fontSize: '18px',
                      lineHeight: 1.4,
                    }}
                  >
                    {expectedCreditUnits > 0
                      ? `≈ ${renderQuotaWithAmount(expectedCreditUnits)}`
                      : '—'}
                  </Text>
                </div>
                {/* 行2：充值比例 1 ¥ = X额度（1/Price，后台配置决定） */}
                <div className='flex justify-between items-center'>
                  <Text size='small' className='text-slate-500'>
                    {t('充值比例')}
                  </Text>
                  <Text size='small' className='text-slate-500'>
                    {`1 ¥ = ${renderQuotaWithAmount(1 / safePrice)}`}
                  </Text>
                </div>
                {/* 行3：汇率 1 USD ≈ ¥X（Binance 实时） */}
                {effectiveRate > 0 && (
                  <div className='flex justify-between items-center'>
                    <Text size='small' className='text-slate-500'>
                      {t('实时汇率')}
                    </Text>
                    <Text size='small' className='text-slate-500' strong>
                      {`1 ${infiniCurrency} ≈ ¥${effectiveRate.toFixed(2)}`}
                    </Text>
                  </div>
                )}
              </div>
            )}
            {hasDiscount && !amountLoading && (
              <>
                <div className='flex justify-between items-center'>
                  <Text className='text-slate-500 dark:text-slate-400'>
                    {t('原价')}：
                  </Text>
                  <Text delete className='text-slate-500 dark:text-slate-400'>
                    {`${originalAmount.toFixed(2)} ${t('元')}`}
                  </Text>
                </div>
                <div className='flex justify-between items-center'>
                  <Text className='text-slate-500 dark:text-slate-400'>
                    {t('优惠')}：
                  </Text>
                  <Text className='text-emerald-600 dark:text-emerald-400'>
                    {`- ${discountAmount.toFixed(2)} ${t('元')}`}
                  </Text>
                </div>
              </>
            )}
            <div className='flex justify-between items-center'>
              <Text strong className='text-slate-700 dark:text-slate-200'>
                {t('支付方式')}：
              </Text>
              <div className='flex items-center'>
                {(() => {
                  const payMethod = payMethods.find(
                    (method) => method.type === normalizedPayWay,
                  );
                  if (payMethod) {
                    return (
                      <>
                        {payMethod.type === 'alipay' ||
                        payMethod.type === 'alipay_official' ? (
                          <SiAlipay
                            className='mr-2'
                            size={16}
                            color='#1677FF'
                          />
                        ) : payMethod.type === 'wxpay' ||
                          payMethod.type === 'wechat_official' ? (
                          <SiWechat
                            className='mr-2'
                            size={16}
                            color='#07C160'
                          />
                        ) : payMethod.type === 'stripe' ? (
                          <SiStripe
                            className='mr-2'
                            size={16}
                            color='#635BFF'
                          />
                        ) : payMethod.type === 'infini' ? (
                          <img
                            src='/infini-logo.png'
                            alt='Infini'
                            className='mr-2'
                            style={{ width: 16, height: 16, objectFit: 'contain' }}
                          />
                        ) : payMethod.icon ? (
                          <img
                            src={payMethod.icon}
                            alt={payMethod.name}
                            className='mr-2'
                            style={{
                              width: 16,
                              height: 16,
                              objectFit: 'contain',
                            }}
                          />
                        ) : (
                          <CreditCard
                            className='mr-2'
                            size={16}
                            color={
                              payMethod.color || 'var(--semi-color-text-2)'
                            }
                          />
                        )}
                        <Text className='text-slate-900 dark:text-slate-100'>
                          {payMethod.name}
                        </Text>
                      </>
                    );
                  } else {
                    // 默认充值方式
                    if (payWay === 'alipay') {
                      return (
                        <>
                          <SiAlipay
                            className='mr-2'
                            size={16}
                            color='#1677FF'
                          />
                          <Text className='text-slate-900 dark:text-slate-100'>
                            {t('支付宝')}
                          </Text>
                        </>
                      );
                    } else if (payWay === 'stripe') {
                      return (
                        <>
                          <SiStripe
                            className='mr-2'
                            size={16}
                            color='#635BFF'
                          />
                          <Text className='text-slate-900 dark:text-slate-100'>
                            Stripe
                          </Text>
                        </>
                      );
                    } else if (isInfini) {
                      return (
                        <>
                          <img
                            src='/infini-logo.png'
                            alt='Infini'
                            className='mr-2'
                            style={{
                              width: 16,
                              height: 16,
                              objectFit: 'contain',
                            }}
                          />
                          <Text className='text-slate-900 dark:text-slate-100'>
                            Infini
                          </Text>
                        </>
                      );
                    } else {
                      return (
                        <>
                          <SiWechat
                            className='mr-2'
                            size={16}
                            color='#07C160'
                          />
                          <Text className='text-slate-900 dark:text-slate-100'>
                            {t('微信')}
                          </Text>
                        </>
                      );
                    }
                  }
                })()}
              </div>
            </div>
          </div>
        </Card>
      </div>
    </Modal>
  );
};

export default PaymentConfirmModal;

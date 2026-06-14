import fs from 'fs';

const files = [
  'src/components/topup/RechargeCard.jsx',
  'src/components/topup/modals/PaymentConfirmModal.jsx',
  'src/components/topup/modals/WechatPayModal.jsx',
  'src/components/topup/index.jsx',
  'src/pages/Setting/Payment/SettingsPaymentGatewayAlipay.jsx',
  'src/pages/Setting/Payment/SettingsPaymentGatewayWechat.jsx',
  'src/components/settings/PaymentSetting.jsx',
];

const zhcn = JSON.parse(fs.readFileSync('src/i18n/locales/zh-CN.json', 'utf-8')).translation;

const reSingle = /\bt\(\s*'((?:[^'\\]|\\.)*)'\s*,?\s*\)/g;
const reBacktick = /\bt\(\s*`((?:[^`\\]|\\.)*)`\s*,?\s*\)/g;

const found = new Set();
for (const f of files) {
  const content = fs.readFileSync(f, 'utf-8');
  let m;
  while ((m = reSingle.exec(content))) found.add(m[1]);
  while ((m = reBacktick.exec(content))) found.add(m[1]);
}

const missing = [...found].filter((k) => !(k in zhcn));
console.log('TOTAL found:', found.size);
console.log('MISSING (' + missing.length + '):');
missing.forEach((k) => console.log(JSON.stringify(k)));

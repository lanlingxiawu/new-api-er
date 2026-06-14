import fs from 'fs';

// New i18n keys introduced by the Alipay/WeChat official payment UI work.
// zh-CN is the source language (identity mapping); other locales are
// human-quality translations of the Chinese source string.
const translations = {
  '微信支付': {
    'zh-CN': '微信支付',
    'zh-TW': '微信支付',
    en: 'WeChat Pay',
    fr: 'WeChat Pay',
    ru: 'WeChat Pay',
    ja: 'WeChat Pay',
    vi: 'WeChat Pay',
  },
  '支付成功': {
    'zh-CN': '支付成功',
    'zh-TW': '付款成功',
    en: 'Payment successful',
    fr: 'Paiement réussi',
    ru: 'Платёж выполнен успешно',
    ja: '支払いが完了しました',
    vi: 'Thanh toán thành công',
  },
  '等待支付...': {
    'zh-CN': '等待支付...',
    'zh-TW': '等待付款...',
    en: 'Waiting for payment...',
    fr: 'En attente du paiement...',
    ru: 'Ожидание оплаты...',
    ja: '支払い待ち...',
    vi: 'Đang chờ thanh toán...',
  },
  '管理员未开启 Waffo Pancake 充值！': {
    'zh-CN': '管理员未开启 Waffo Pancake 充值！',
    'zh-TW': '管理員未開啟 Waffo Pancake 儲值！',
    en: 'The administrator has not enabled Waffo Pancake top-up!',
    fr: "L'administrateur n'a pas activé la recharge Waffo Pancake !",
    ru: 'Администратор не включил пополнение через Waffo Pancake!',
    ja: '管理者は Waffo Pancake チャージを有効にしていません！',
    vi: 'Quản trị viên chưa bật nạp tiền Waffo Pancake!',
  },
  '管理员未开启 Waffo 充值！': {
    'zh-CN': '管理员未开启 Waffo 充值！',
    'zh-TW': '管理員未開啟 Waffo 儲值！',
    en: 'The administrator has not enabled Waffo top-up!',
    fr: "L'administrateur n'a pas activé la recharge Waffo !",
    ru: 'Администратор не включил пополнение через Waffo!',
    ja: '管理者は Waffo チャージを有効にしていません！',
    vi: 'Quản trị viên chưa bật nạp tiền Waffo!',
  },
  '管理员未开启支付宝充值！': {
    'zh-CN': '管理员未开启支付宝充值！',
    'zh-TW': '管理員未開啟支付寶儲值！',
    en: 'The administrator has not enabled Alipay top-up!',
    fr: "L'administrateur n'a pas activé la recharge Alipay !",
    ru: 'Администратор не включил пополнение через Alipay!',
    ja: '管理者は Alipay チャージを有効にしていません！',
    vi: 'Quản trị viên chưa bật nạp tiền Alipay!',
  },
  '管理员未开启微信支付充值！': {
    'zh-CN': '管理员未开启微信支付充值！',
    'zh-TW': '管理員未開啟微信支付儲值！',
    en: 'The administrator has not enabled WeChat Pay top-up!',
    fr: "L'administrateur n'a pas activé la recharge WeChat Pay !",
    ru: 'Администратор не включил пополнение через WeChat Pay!',
    ja: '管理者は WeChat Pay チャージを有効にしていません！',
    vi: 'Quản trị viên chưa bật nạp tiền WeChat Pay!',
  },
  '支付跳转地址不安全': {
    'zh-CN': '支付跳转地址不安全',
    'zh-TW': '付款跳轉位址不安全',
    en: 'The payment redirect URL is not safe',
    fr: "L'URL de redirection de paiement n'est pas sécurisée",
    ru: 'Адрес перенаправления для оплаты небезопасен',
    ja: '決済リダイレクト先のURLが安全ではありません',
    vi: 'Địa chỉ chuyển hướng thanh toán không an toàn',
  },
  '支付宝设置': {
    'zh-CN': '支付宝设置',
    'zh-TW': '支付寶設定',
    en: 'Alipay Settings',
    fr: 'Paramètres Alipay',
    ru: 'Настройки Alipay',
    ja: 'Alipay 設定',
    vi: 'Cài đặt Alipay',
  },
  '请在支付宝开放平台获取 App ID、应用私钥和支付宝公钥（公钥模式），并在下方填写。异步通知和同步跳转地址留空则使用系统默认值。': {
    'zh-CN':
      '请在支付宝开放平台获取 App ID、应用私钥和支付宝公钥（公钥模式），并在下方填写。异步通知和同步跳转地址留空则使用系统默认值。',
    'zh-TW':
      '請在支付寶開放平台取得 App ID、應用私鑰與支付寶公鑰（公鑰模式），並在下方填寫。非同步通知與同步跳轉位址留空則使用系統預設值。',
    en: 'Get the App ID, app private key, and Alipay public key (public key mode) from the Alipay Open Platform, then fill them in below. Leave the notification and return URLs blank to use the system defaults.',
    fr: "Récupérez l'App ID, la clé privée de l'application et la clé publique Alipay (mode clé publique) depuis la plateforme ouverte Alipay, puis renseignez-les ci-dessous. Laissez les URL de notification et de retour vides pour utiliser les valeurs par défaut du système.",
    ru: 'Получите App ID, приватный ключ приложения и публичный ключ Alipay (режим публичного ключа) на платформе Alipay Open Platform и заполните их ниже. Оставьте адреса уведомления и возврата пустыми, чтобы использовать значения по умолчанию.',
    ja: 'Alipay オープンプラットフォームで App ID、アプリ秘密鍵、Alipay 公開鍵（公開鍵モード）を取得し、以下に入力してください。非同期通知先と同期リダイレクト先を空にするとシステムのデフォルト値が使用されます。',
    vi: 'Lấy App ID, khóa riêng tư của ứng dụng và khóa công khai Alipay (chế độ khóa công khai) từ Alipay Open Platform, sau đó điền vào bên dưới. Để trống địa chỉ thông báo và địa chỉ chuyển hướng để dùng giá trị mặc định của hệ thống.',
  },
  '默认异步通知地址': {
    'zh-CN': '默认异步通知地址',
    'zh-TW': '預設非同步通知位址',
    en: 'Default notification URL',
    fr: 'URL de notification par défaut',
    ru: 'Адрес уведомления по умолчанию',
    ja: 'デフォルトの通知URL',
    vi: 'Địa chỉ thông báo mặc định',
  },
  '启用支付宝官方支付': {
    'zh-CN': '启用支付宝官方支付',
    'zh-TW': '啟用支付寶官方支付',
    en: 'Enable official Alipay payment',
    fr: 'Activer le paiement officiel Alipay',
    ru: 'Включить официальную оплату Alipay',
    ja: 'Alipay 公式決済を有効にする',
    vi: 'Bật thanh toán Alipay chính thức',
  },
  '沙箱环境': {
    'zh-CN': '沙箱环境',
    'zh-TW': '沙盒環境',
    en: 'Sandbox environment',
    fr: 'Environnement sandbox',
    ru: 'Песочница (тестовая среда)',
    ja: 'サンドボックス環境',
    vi: 'Môi trường sandbox',
  },
  '最低充值数量': {
    'zh-CN': '最低充值数量',
    'zh-TW': '最低儲值數量',
    en: 'Minimum top-up amount',
    fr: 'Montant minimum de recharge',
    ru: 'Минимальная сумма пополнения',
    ja: '最低チャージ額',
    vi: 'Số tiền nạp tối thiểu',
  },
  '例如：1': {
    'zh-CN': '例如：1',
    'zh-TW': '例如：1',
    en: 'e.g., 1',
    fr: 'ex. : 1',
    ru: 'например: 1',
    ja: '例：1',
    vi: 'ví dụ: 1',
  },
  '用户单次最少可充值的数量': {
    'zh-CN': '用户单次最少可充值的数量',
    'zh-TW': '使用者單次最少可儲值的數量',
    en: 'The minimum amount a user can top up in a single transaction',
    fr: "Le montant minimum qu'un utilisateur peut recharger en une seule fois",
    ru: 'Минимальная сумма, которую пользователь может пополнить за одну операцию',
    ja: 'ユーザーが一度にチャージできる最小額',
    vi: 'Số tiền tối thiểu người dùng có thể nạp trong một lần',
  },
  'App ID': {
    'zh-CN': 'App ID',
    'zh-TW': 'App ID',
    en: 'App ID',
    fr: 'App ID',
    ru: 'App ID',
    ja: 'App ID',
    vi: 'App ID',
  },
  '支付宝开放平台应用 App ID': {
    'zh-CN': '支付宝开放平台应用 App ID',
    'zh-TW': '支付寶開放平台應用 App ID',
    en: 'The App ID of your Alipay Open Platform application',
    fr: "L'App ID de votre application sur la plateforme ouverte Alipay",
    ru: 'App ID вашего приложения на платформе Alipay Open Platform',
    ja: 'Alipay オープンプラットフォームアプリの App ID',
    vi: 'App ID của ứng dụng trên Alipay Open Platform',
  },
  '异步通知地址': {
    'zh-CN': '异步通知地址',
    'zh-TW': '非同步通知位址',
    en: 'Async notification URL',
    fr: 'URL de notification asynchrone',
    ru: 'Адрес асинхронного уведомления',
    ja: '非同期通知URL',
    vi: 'Địa chỉ thông báo bất đồng bộ',
  },
  '留空则使用系统默认地址': {
    'zh-CN': '留空则使用系统默认地址',
    'zh-TW': '留空則使用系統預設位址',
    en: 'Leave blank to use the system default address',
    fr: "Laissez vide pour utiliser l'adresse par défaut du système",
    ru: 'Оставьте пустым, чтобы использовать адрес по умолчанию',
    ja: '空欄の場合はシステムのデフォルトアドレスを使用します',
    vi: 'Để trống để dùng địa chỉ mặc định của hệ thống',
  },
  '应用私钥': {
    'zh-CN': '应用私钥',
    'zh-TW': '應用私鑰',
    en: 'App private key',
    fr: "Clé privée de l'application",
    ru: 'Приватный ключ приложения',
    ja: 'アプリ秘密鍵',
    vi: 'Khóa riêng tư ứng dụng',
  },
  '填写后覆盖当前私钥，留空表示保持当前不变': {
    'zh-CN': '填写后覆盖当前私钥，留空表示保持当前不变',
    'zh-TW': '填寫後將覆蓋目前私鑰，留空表示保持不變',
    en: 'Filling this in will overwrite the current private key; leave blank to keep it unchanged',
    fr: 'Le remplir remplacera la clé privée actuelle ; laissez vide pour ne pas la modifier',
    ru: 'Заполнение перезапишет текущий приватный ключ; оставьте пустым, чтобы не менять его',
    ja: '入力すると現在の秘密鍵が上書きされます。変更しない場合は空欄のままにしてください',
    vi: 'Điền vào sẽ ghi đè khóa riêng tư hiện tại; để trống để giữ nguyên',
  },
  '应用私钥（RSA2），保存后不会回显': {
    'zh-CN': '应用私钥（RSA2），保存后不会回显',
    'zh-TW': '應用私鑰（RSA2），儲存後不會回顯',
    en: 'App private key (RSA2); not displayed again after saving',
    fr: "Clé privée de l'application (RSA2) ; non réaffichée après l'enregistrement",
    ru: 'Приватный ключ приложения (RSA2); не отображается повторно после сохранения',
    ja: 'アプリ秘密鍵（RSA2）。保存後は再表示されません',
    vi: 'Khóa riêng tư ứng dụng (RSA2); không hiển thị lại sau khi lưu',
  },
  '支付宝公钥': {
    'zh-CN': '支付宝公钥',
    'zh-TW': '支付寶公鑰',
    en: 'Alipay public key',
    fr: 'Clé publique Alipay',
    ru: 'Публичный ключ Alipay',
    ja: 'Alipay 公開鍵',
    vi: 'Khóa công khai Alipay',
  },
  '填写后覆盖当前公钥，留空表示保持当前不变': {
    'zh-CN': '填写后覆盖当前公钥，留空表示保持当前不变',
    'zh-TW': '填寫後將覆蓋目前公鑰，留空表示保持不變',
    en: 'Filling this in will overwrite the current public key; leave blank to keep it unchanged',
    fr: 'Le remplir remplacera la clé publique actuelle ; laissez vide pour ne pas la modifier',
    ru: 'Заполнение перезапишет текущий публичный ключ; оставьте пустым, чтобы не менять его',
    ja: '入力すると現在の公開鍵が上書きされます。変更しない場合は空欄のままにしてください',
    vi: 'Điền vào sẽ ghi đè khóa công khai hiện tại; để trống để giữ nguyên',
  },
  '支付宝公钥（公钥模式），保存后不会回显': {
    'zh-CN': '支付宝公钥（公钥模式），保存后不会回显',
    'zh-TW': '支付寶公鑰（公鑰模式），儲存後不會回顯',
    en: 'Alipay public key (public key mode); not displayed again after saving',
    fr: "Clé publique Alipay (mode clé publique) ; non réaffichée après l'enregistrement",
    ru: 'Публичный ключ Alipay (режим публичного ключа); не отображается повторно после сохранения',
    ja: 'Alipay 公開鍵（公開鍵モード）。保存後は再表示されません',
    vi: 'Khóa công khai Alipay (chế độ khóa công khai); không hiển thị lại sau khi lưu',
  },
  '同步跳转地址': {
    'zh-CN': '同步跳转地址',
    'zh-TW': '同步跳轉位址',
    en: 'Return URL',
    fr: 'URL de retour',
    ru: 'Адрес возврата',
    ja: '同期リダイレクトURL',
    vi: 'Địa chỉ chuyển hướng đồng bộ',
  },
  '留空则使用系统默认地址（例如：https://example.com/console/topup）': {
    'zh-CN': '留空则使用系统默认地址（例如：https://example.com/console/topup）',
    'zh-TW': '留空則使用系統預設位址（例如：https://example.com/console/topup）',
    en: 'Leave blank to use the system default address (e.g., https://example.com/console/topup)',
    fr: "Laissez vide pour utiliser l'adresse par défaut du système (ex. : https://example.com/console/topup)",
    ru: 'Оставьте пустым, чтобы использовать адрес по умолчанию (например, https://example.com/console/topup)',
    ja: '空欄の場合はシステムのデフォルトアドレスを使用します（例：https://example.com/console/topup）',
    vi: 'Để trống để dùng địa chỉ mặc định của hệ thống (ví dụ: https://example.com/console/topup)',
  },
  '更新支付宝设置': {
    'zh-CN': '更新支付宝设置',
    'zh-TW': '更新支付寶設定',
    en: 'Update Alipay settings',
    fr: 'Mettre à jour les paramètres Alipay',
    ru: 'Обновить настройки Alipay',
    ja: 'Alipay 設定を更新',
    vi: 'Cập nhật cài đặt Alipay',
  },
  '微信支付设置': {
    'zh-CN': '微信支付设置',
    'zh-TW': '微信支付設定',
    en: 'WeChat Pay Settings',
    fr: 'Paramètres WeChat Pay',
    ru: 'Настройки WeChat Pay',
    ja: 'WeChat Pay 設定',
    vi: 'Cài đặt WeChat Pay',
  },
  '请在微信支付商户平台获取 AppID、商户号、APIv3 密钥以及商户 API 证书序列号和私钥，并在下方填写。异步通知地址留空则使用系统默认值。': {
    'zh-CN':
      '请在微信支付商户平台获取 AppID、商户号、APIv3 密钥以及商户 API 证书序列号和私钥，并在下方填写。异步通知地址留空则使用系统默认值。',
    'zh-TW':
      '請在微信支付商戶平台取得 AppID、商戶號、APIv3 金鑰以及商戶 API 憑證序號和私鑰，並在下方填寫。非同步通知位址留空則使用系統預設值。',
    en: 'Get the AppID, merchant ID, APIv3 key, and merchant API certificate serial number and private key from the WeChat Pay merchant platform, then fill them in below. Leave the notification URL blank to use the system default.',
    fr: "Récupérez l'AppID, l'ID marchand, la clé APIv3 ainsi que le numéro de série du certificat API marchand et la clé privée depuis la plateforme marchande WeChat Pay, puis renseignez-les ci-dessous. Laissez l'URL de notification vide pour utiliser la valeur par défaut du système.",
    ru: 'Получите AppID, ID мерчанта, ключ APIv3, а также серийный номер сертификата API мерчанта и приватный ключ на платформе мерчанта WeChat Pay и заполните их ниже. Оставьте адрес уведомления пустым, чтобы использовать значение по умолчанию.',
    ja: 'WeChat Pay 加盟店プラットフォームで AppID、商戶号、APIv3 キー、商戶 API 証明書シリアル番号と秘密鍵を取得し、以下に入力してください。通知URLを空にするとシステムのデフォルト値が使用されます。',
    vi: 'Lấy AppID, mã số merchant, khóa APIv3, số sê-ri chứng chỉ API merchant và khóa riêng tư từ nền tảng merchant WeChat Pay, sau đó điền vào bên dưới. Để trống địa chỉ thông báo để dùng giá trị mặc định của hệ thống.',
  },
  '启用微信官方支付': {
    'zh-CN': '启用微信官方支付',
    'zh-TW': '啟用微信官方支付',
    en: 'Enable official WeChat payment',
    fr: 'Activer le paiement officiel WeChat',
    ru: 'Включить официальную оплату WeChat',
    ja: 'WeChat 公式決済を有効にする',
    vi: 'Bật thanh toán WeChat chính thức',
  },
  '公众号 / 小程序 / APP AppID': {
    'zh-CN': '公众号 / 小程序 / APP AppID',
    'zh-TW': '公眾號 / 小程式 / APP AppID',
    en: 'Official Account / Mini Program / App AppID',
    fr: 'AppID Compte officiel / Mini-programme / App',
    ru: 'AppID официального аккаунта / мини-программы / приложения',
    ja: '公式アカウント / ミニプログラム / アプリ AppID',
    vi: 'AppID Tài khoản chính thức / Mini Program / App',
  },
  '商户号': {
    'zh-CN': '商户号',
    'zh-TW': '商戶號',
    en: 'Merchant ID',
    fr: 'ID marchand',
    ru: 'ID мерчанта',
    ja: '加盟店番号',
    vi: 'Mã số merchant',
  },
  '微信支付商户号': {
    'zh-CN': '微信支付商户号',
    'zh-TW': '微信支付商戶號',
    en: 'WeChat Pay merchant ID',
    fr: 'ID marchand WeChat Pay',
    ru: 'ID мерчанта WeChat Pay',
    ja: 'WeChat Pay 加盟店番号',
    vi: 'Mã số merchant WeChat Pay',
  },
  'APIv3 密钥': {
    'zh-CN': 'APIv3 密钥',
    'zh-TW': 'APIv3 金鑰',
    en: 'APIv3 key',
    fr: 'Clé APIv3',
    ru: 'Ключ APIv3',
    ja: 'APIv3 キー',
    vi: 'Khóa APIv3',
  },
  '填写后覆盖当前密钥，留空表示保持当前不变': {
    'zh-CN': '填写后覆盖当前密钥，留空表示保持当前不变',
    'zh-TW': '填寫後將覆蓋目前金鑰，留空表示保持不變',
    en: 'Filling this in will overwrite the current key; leave blank to keep it unchanged',
    fr: 'Le remplir remplacera la clé actuelle ; laissez vide pour ne pas la modifier',
    ru: 'Заполнение перезапишет текущий ключ; оставьте пустым, чтобы не менять его',
    ja: '入力すると現在のキーが上書きされます。変更しない場合は空欄のままにしてください',
    vi: 'Điền vào sẽ ghi đè khóa hiện tại; để trống để giữ nguyên',
  },
  '32 字节 APIv3 密钥，保存后不会回显': {
    'zh-CN': '32 字节 APIv3 密钥，保存后不会回显',
    'zh-TW': '32 位元組 APIv3 金鑰，儲存後不會回顯',
    en: '32-byte APIv3 key; not displayed again after saving',
    fr: 'Clé APIv3 de 32 octets ; non réaffichée après l\'enregistrement',
    ru: '32-байтовый ключ APIv3; не отображается повторно после сохранения',
    ja: '32バイトの APIv3 キー。保存後は再表示されません',
    vi: 'Khóa APIv3 32 byte; không hiển thị lại sau khi lưu',
  },
  '商户 API 证书序列号': {
    'zh-CN': '商户 API 证书序列号',
    'zh-TW': '商戶 API 憑證序號',
    en: 'Merchant API certificate serial number',
    fr: 'Numéro de série du certificat API marchand',
    ru: 'Серийный номер сертификата API мерчанта',
    ja: '商戶 API 証明書シリアル番号',
    vi: 'Số sê-ri chứng chỉ API merchant',
  },
  '例如：1DEDFA********': {
    'zh-CN': '例如：1DEDFA********',
    'zh-TW': '例如：1DEDFA********',
    en: 'e.g., 1DEDFA********',
    fr: 'ex. : 1DEDFA********',
    ru: 'например: 1DEDFA********',
    ja: '例：1DEDFA********',
    vi: 'ví dụ: 1DEDFA********',
  },
  '商户 API 私钥': {
    'zh-CN': '商户 API 私钥',
    'zh-TW': '商戶 API 私鑰',
    en: 'Merchant API private key',
    fr: 'Clé privée API marchand',
    ru: 'Приватный ключ API мерчанта',
    ja: '商戶 API 秘密鍵',
    vi: 'Khóa riêng tư API merchant',
  },
  '商户 API 私钥（PEM 格式），保存后不会回显': {
    'zh-CN': '商户 API 私钥（PEM 格式），保存后不会回显',
    'zh-TW': '商戶 API 私鑰（PEM 格式），儲存後不會回顯',
    en: 'Merchant API private key (PEM format); not displayed again after saving',
    fr: "Clé privée API marchand (format PEM) ; non réaffichée après l'enregistrement",
    ru: 'Приватный ключ API мерчанта (формат PEM); не отображается повторно после сохранения',
    ja: '商戶 API 秘密鍵（PEM 形式）。保存後は再表示されません',
    vi: 'Khóa riêng tư API merchant (định dạng PEM); không hiển thị lại sau khi lưu',
  },
  '更新微信支付设置': {
    'zh-CN': '更新微信支付设置',
    'zh-TW': '更新微信支付設定',
    en: 'Update WeChat Pay settings',
    fr: 'Mettre à jour les paramètres WeChat Pay',
    ru: 'Обновить настройки WeChat Pay',
    ja: 'WeChat Pay 設定を更新',
    vi: 'Cập nhật cài đặt WeChat Pay',
  },
};

const locales = ['zh-CN', 'zh-TW', 'en', 'fr', 'ru', 'ja', 'vi'];

// Surgical text-append: avoid JSON.parse + JSON.stringify on the whole file,
// since several locale files contain pre-existing duplicate keys that would
// be silently collapsed (and possibly change which value wins) on re-serialize.
const suffix = '\n  }\n}\n';

for (const locale of locales) {
  const filePath = `src/i18n/locales/${locale}.json`;
  const content = fs.readFileSync(filePath, 'utf-8');
  const data = JSON.parse(content);

  if (!content.endsWith(suffix)) {
    throw new Error(`${locale}: unexpected file ending, aborting`);
  }

  const newLines = [];
  for (const [key, byLocale] of Object.entries(translations)) {
    if (!(key in data.translation)) {
      newLines.push(`    ${JSON.stringify(key)}: ${JSON.stringify(byLocale[locale])}`);
    }
  }

  if (newLines.length === 0) {
    console.log(`${locale}: nothing to add`);
    continue;
  }

  const body = content.slice(0, -suffix.length);
  const newContent = body + ',\n' + newLines.join(',\n') + suffix;

  // Sanity check: must still be valid JSON with the expected number of new keys.
  const parsed = JSON.parse(newContent);
  if (Object.keys(parsed.translation).length !== Object.keys(data.translation).length + newLines.length) {
    throw new Error(`${locale}: key count mismatch after merge, aborting`);
  }

  fs.writeFileSync(filePath, newContent, 'utf-8');
  console.log(`${locale}: added ${newLines.length} keys`);
}

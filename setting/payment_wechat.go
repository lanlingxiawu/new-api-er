/*
Copyright (C) 2023-2026 QuantumNous

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
package setting

var (
	WechatEnabled         bool
	WechatAppId           string // 公众号/小程序/APP AppID（Native 下单必填）
	WechatMchId           string // 微信支付商户号
	WechatApiV3Key        string // APIv3 密钥（32 字节）
	WechatMchPrivateKey   string // 商户 API 私钥（PEM 格式）
	WechatMchCertSerialNo string // 商户 API 证书序列号
	WechatMinTopUp        int    = 1
	WechatNotifyUrl       string // 异步通知地址，留空则使用系统默认回调地址
)

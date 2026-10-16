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
import i18next from 'i18next'
import { toast } from 'sonner'
import { afterEach, expect, test, vi } from 'vitest'

import zh from '@/i18n/locales/zh.json'

import * as channelApi from '../../api'
import { handleUpdateChannelAccountBalance } from '../channel-actions'

afterEach(async () => {
  vi.restoreAllMocks()
  await i18next.changeLanguage('en')
})

test('渠道余额更新成功和网络失败均显示中文提示', async () => {
  i18next.addResourceBundle('zh', 'translation', zh.translation)
  await i18next.changeLanguage('zh')
  const success = vi.spyOn(toast, 'success')
  const failure = vi.spyOn(toast, 'error')
  const update = vi
    .spyOn(channelApi, 'updateChannelAccountBalance')
    .mockResolvedValueOnce({ success: true, message: '' })
    .mockRejectedValueOnce(new Error('Network failure'))
  await handleUpdateChannelAccountBalance(7)
  expect(success).toHaveBeenCalledWith(
    zh.translation['Balance updated successfully']
  )
  await handleUpdateChannelAccountBalance(7)
  expect(failure).toHaveBeenCalledWith(
    zh.translation['Failed to update balance']
  )
  expect(update).toHaveBeenCalledTimes(2)
})

test('渠道余额更新业务失败使用结构化翻译而非原始英文 message', async () => {
  i18next.addResourceBundle('zh', 'translation', zh.translation)
  await i18next.changeLanguage('zh')
  const failure = vi.spyOn(toast, 'error')
  vi.spyOn(channelApi, 'updateChannelAccountBalance').mockResolvedValue({
    success: false,
    message: 'Raw untranslated message',
    message_key: 'Failed to update balance',
  } as Awaited<ReturnType<typeof channelApi.updateChannelAccountBalance>>)
  await handleUpdateChannelAccountBalance(7)
  expect(failure).toHaveBeenCalledWith(
    zh.translation['Failed to update balance']
  )
})

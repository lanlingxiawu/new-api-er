import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import { isValidElement, type ReactElement } from 'react'

import {
  linkifyAuthRecoveryMessage,
  isAuthSessionLimitError,
} from './server-error-toast'

describe('authentication recovery error toast', () => {
  test('recognizes the HTTP 409 Axios error payload returned by login', () => {
    assert.equal(
      isAuthSessionLimitError({
        response: {
          status: 409,
          data: { success: false, code: 'AUTH_SESSION_LIMIT' },
        },
      }),
      true
    )
  })

  test('turns both recovery routes into same-tab links', () => {
    const content = linkifyAuthRecoveryMessage(
      '已登录设备：进入“个人资料（/profile）→ 登录会话”\n无法访问已登录设备：进入“忘记密码（/forgot-password）”'
    )
    const links = content.filter(isValidElement) as ReactElement<{
      href: string
      target?: string
    }>[]

    assert.deepEqual(
      links.map((link) => link.props.href),
      ['/profile', '/forgot-password']
    )
    assert.ok(links.every((link) => link.props.target === undefined))
  })

  test('keeps surrounding translated copy and line breaks', () => {
    const content = linkifyAuthRecoveryMessage(
      'first /profile\nsecond /forgot-password'
    )

    const text = content.map((part) => {
      if (typeof part === 'string') return part
      if (isValidElement<{ children: string }>(part)) {
        return part.props.children
      }
      return ''
    })

    assert.equal(
      text.join(''),
      'first /profile\nsecond /forgot-password'
    )
  })
})

import i18next from 'i18next'
import type { ReactNode } from 'react'
import { toast } from 'sonner'

import { getServerErrorMessageKey } from '@/lib/server-error-message'

const sessionLimitMessageKey =
  'Too many active login sessions. On a device where you are already signed in, open Login sessions and use “Sign out other sessions” to revoke them. If you cannot access a signed-in device, reset your password to sign out all sessions.'
const recoveryRoutePattern = /\/(?:profile|forgot-password)/g

export function linkifyAuthRecoveryMessage(message: string): ReactNode[] {
  const content: ReactNode[] = []
  let cursor = 0

  for (const match of message.matchAll(recoveryRoutePattern)) {
    const route = match[0]
    const index = match.index
    content.push(message.slice(cursor, index))
    content.push(
      <a
        key={`${route}-${index}`}
        href={route}
        className='font-medium underline underline-offset-2'
      >
        {route}
      </a>
    )
    cursor = index + route.length
  }

  content.push(message.slice(cursor))
  return content
}

export function isAuthSessionLimitError(value: unknown): boolean {
  return getServerErrorMessageKey(value) === sessionLimitMessageKey
}

export function showAuthServerErrorToast(value: unknown): boolean {
  if (!isAuthSessionLimitError(value)) return false

  toast.error(
    <div className='whitespace-pre-line'>
      {linkifyAuthRecoveryMessage(i18next.t(sessionLimitMessageKey))}
    </div>,
    { duration: 15000 }
  )
  return true
}

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
import { create } from 'zustand'

import type { AdminCapabilities } from '@/lib/admin-permissions'

export type UserPermissions = {
  sidebar_settings?: boolean
  sidebar_modules?: Record<string, unknown>
  admin_permissions?: AdminCapabilities
}

export interface AuthUser {
  id: number
  username: string
  display_name?: string
  email?: string
  role: number
  status?: number
  group?: string
  quota?: number
  used_quota?: number
  request_count?: number
  aff_code?: string
  aff_count?: number
  aff_quota?: number
  aff_history_quota?: number
  inviter_id?: number
  github_id?: string
  oidc_id?: string
  wechat_id?: string
  telegram_id?: string
  linux_do_id?: string
  setting?: Record<string, unknown> | string
  stripe_customer?: string
  sidebar_modules?: string
  permissions?: UserPermissions
  is_employee?: boolean
}

/** One browser login session, as reported by the backend. */
export interface LoginSession {
  sid: string
  current: boolean
  login_method: string
  ip: string
  user_agent: string
  created_at: number
  last_active_at: number
  expires_at: number
}

/** Everything the backend hands back on login / token refresh. */
export interface AuthBundle {
  access_token: string
  token_type: 'Bearer' | string
  access_expires_at: number
  user: AuthUser
  session: LoginSession
}

export type AuthBootstrapState = 'idle' | 'checking' | 'complete'

interface AuthState {
  auth: {
    user: AuthUser | null
    /** Kept in memory only — the httpOnly refresh cookie is what survives a reload. */
    accessToken: string | null
    accessExpiresAt: number | null
    session: LoginSession | null
    bootstrapState: AuthBootstrapState
    setBundle: (bundle: AuthBundle) => void
    setUser: (user: AuthUser | null) => void
    setBootstrapState: (bootstrapState: AuthBootstrapState) => void
    reset: (bootstrapState?: AuthBootstrapState) => void
  }
}

export const useAuthStore = create<AuthState>()((set) => {
  // Restore user info from localStorage
  const initUser = (() => {
    try {
      if (typeof window !== 'undefined') {
        const saved = window.localStorage.getItem('user')
        return saved ? JSON.parse(saved) : null
      }
    } catch {
      // Clear dirty data when parsing fails
      if (typeof window !== 'undefined') {
        window.localStorage.removeItem('user')
      }
    }
    return null
  })()

  // 只持久化 user，用来避免刷新时先闪一下未登录状态；
  // access token 只留在内存里，页面重载后由 httpOnly refresh cookie 换新的。
  const persistUser = (user: AuthUser | null) => {
    if (typeof window === 'undefined') return
    if (user) {
      window.localStorage.setItem('user', JSON.stringify(user))
    } else {
      window.localStorage.removeItem('user')
    }
  }

  return {
    auth: {
      user: initUser,
      accessToken: null,
      accessExpiresAt: null,
      session: null,
      // 有持久化的 user 但没有 token，说明需要先用 refresh cookie 换 token 才算就绪。
      bootstrapState: 'idle',
      setBundle: (bundle) =>
        set((state) => {
          persistUser(bundle.user)
          return {
            ...state,
            auth: {
              ...state.auth,
              user: bundle.user,
              accessToken: bundle.access_token,
              accessExpiresAt: bundle.access_expires_at,
              session: bundle.session,
              bootstrapState: 'complete',
            },
          }
        }),
      setUser: (user) =>
        set((state) => {
          persistUser(user)
          return { ...state, auth: { ...state.auth, user } }
        }),
      setBootstrapState: (bootstrapState) =>
        set((state) => ({
          ...state,
          auth: { ...state.auth, bootstrapState },
        })),
      reset: (bootstrapState = 'complete') =>
        set((state) => {
          persistUser(null)
          return {
            ...state,
            auth: {
              ...state.auth,
              user: null,
              accessToken: null,
              accessExpiresAt: null,
              session: null,
              bootstrapState,
            },
          }
        }),
    },
  }
})

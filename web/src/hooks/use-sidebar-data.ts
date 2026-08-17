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
import {
  Activity,
  BadgeDollarSign,
  Box,
  CreditCard,
  FileSearch,
  FileText,
  FlaskConical,
  Key,
  LayoutDashboard,
  LineChart,
  ListTodo,
  MessageSquare,
  Radio,
  Server,
  ServerCog,
  Settings,
  Ticket,
  User,
  UserCog,
  UserRoundCheck,
  Users,
  Wallet,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'

import type { SidebarData } from '@/components/layout/types'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

/**
 * Root navigation groups for the application sidebar.
 *
 * These are shown when the URL does not match any nested sidebar view
 * registered in `layout/lib/sidebar-view-registry.ts`.
 */
export function useSidebarData(): SidebarData {
  const { t } = useTranslation()
  const isEmployee = useAuthStore((s) => Boolean(s.auth.user?.is_employee))

  return {
    navGroups: [
      {
        id: 'chat',
        title: t('Chat'),
        items: [
          {
            title: t('Playground'),
            url: '/playground',
            icon: FlaskConical,
          },
          {
            title: t('Chat'),
            icon: MessageSquare,
            type: 'chat-presets',
          },
        ],
      },
      {
        id: 'general',
        title: t('General'),
        items: [
          {
            title: t('Overview'),
            url: '/dashboard/overview',
            icon: Activity,
          },
          {
            title: t('Dashboard'),
            url: '/dashboard/models',
            icon: LayoutDashboard,
          },
          {
            title: t('API Keys'),
            url: '/keys',
            icon: Key,
          },
          {
            title: t('Usage Logs'),
            url: '/usage-logs/common',
            icon: FileText,
          },
          {
            title: t('Task Logs'),
            url: '/usage-logs/task',
            activeUrls: ['/usage-logs/drawing'],
            configUrls: ['/usage-logs/drawing', '/usage-logs/task'],
            icon: ListTodo,
          },
        ],
      },
      {
        id: 'personal',
        title: t('Personal'),
        items: [
          {
            title: t('Wallet'),
            url: '/wallet',
            icon: Wallet,
          },
          {
            title: t('Profile'),
            url: '/profile',
            icon: User,
          },
          // 仅员工可见
          ...(isEmployee
            ? [
                {
                  title: t('My Commission'),
                  url: '/commission',
                  icon: BadgeDollarSign,
                },
                {
                  title: t('My Customers'),
                  url: '/customer-console',
                  icon: UserRoundCheck,
                },
              ]
            : []),
        ],
      },
      {
        id: 'admin',
        title: t('Admin'),
        items: [
          {
            title: t('Channels'),
            url: '/channels',
            icon: Radio,
          },
          {
            title: t('Models'),
            url: '/models/metadata',
            icon: Box,
          },
          {
            title: t('Users'),
            url: '/users',
            icon: Users,
          },
          {
            title: t('Redemption Codes'),
            url: '/redemption-codes',
            icon: Ticket,
          },
          {
            title: t('Subscriptions'),
            url: '/subscriptions',
            icon: CreditCard,
          },
          {
            title: t('Authenticity Detection'),
            url: '/channels/detection',
            icon: Activity,
          },
          {
            title: t('Price monitor'),
            url: '/price-monitor',
            icon: BadgeDollarSign,
          },
          {
            title: t('Node Pool'),
            url: '/node-pool',
            icon: Server,
            requiredRole: ROLE.SUPER_ADMIN,
          },
          {
            title: t('Employee Management'),
            url: '/employees',
            icon: UserCog,
          },
          {
            title: t('Business Overview'),
            url: '/commission-overview',
            icon: LineChart,
          },
          // 请求日志和系统信息：可见性由 admin_menu.request_logs /
          // admin_menu.system_info 权限决定（见 useSidebarView 里的
          // adminMenuFromUrl 过滤），默认对普通管理员关闭，由超级管理员按人授予。
          {
            title: t('Request Logs'),
            url: '/request-logs',
            icon: FileSearch,
          },
          {
            title: t('System Info'),
            url: '/system-info',
            icon: ServerCog,
          },
          {
            title: t('System Settings'),
            url: '/system-settings/site',
            activeUrls: ['/system-settings'],
            icon: Settings,
          },
        ],
      },
    ],
  }
}

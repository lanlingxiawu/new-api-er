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
import { useTranslation } from 'react-i18next'

import { SectionPageLayout } from '@/components/layout'
import { canViewSystemSettingsScope } from '@/lib/admin-permissions'
import { useAuthStore } from '@/stores/auth-store'

import { SystemInstancesPanel } from './components/system-instances-panel'
import { SystemTasksPanel } from './components/system-tasks-panel'

export function SystemInfo() {
  const { t } = useTranslation()
  const currentUser = useAuthStore((state) => state.auth.user)
  // 系统任务面板复用日志维护分区的查看权限：拥有该分区权限的管理员才需要看到
  // 后台任务执行状态，避免把无关的运维细节暴露给所有能进入本页的管理员。
  const canViewSystemTasks = canViewSystemSettingsScope(
    currentUser,
    'operations.logs'
  )

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('System Info')}</SectionPageLayout.Title>
      <SectionPageLayout.Content>
        <div className='space-y-4'>
          <SystemInstancesPanel />
          {canViewSystemTasks && <SystemTasksPanel />}
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}

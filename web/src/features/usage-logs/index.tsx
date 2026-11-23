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
import { getRouteApi, Link, useNavigate } from '@tanstack/react-router'
import { ArrowLeft, ArrowUpRight } from 'lucide-react'
import { Activity, useCallback, useEffect, useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import { SectionPageLayout } from '@/components/layout'
import type { NavGroup } from '@/components/layout/types'
import { Button } from '@/components/ui/button'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { CacheStatsDialog } from '@/features/system-settings/general/channel-affinity/cache-stats-dialog'
import { useSidebarConfig } from '@/hooks/use-sidebar-config'

import { UserInfoDialog } from './components/dialogs/user-info-dialog'
import {
  type LogsViewScope,
  UsageLogsProvider,
  useLogsViewScope,
  useUsageLogsContext,
} from './components/usage-logs-provider'
import { UsageLogsTable } from './components/usage-logs-table'
import { LogExportCenter } from './export'
import {
  isUsageLogsSectionId,
  USAGE_LOGS_DEFAULT_SECTION,
  type UsageLogsSectionId,
} from './section-registry'
import { UpstreamLogsPage } from './upstream-log/upstream-logs-page'

const route = getRouteApi('/_authenticated/usage-logs/$section')
const TASK_LOG_SECTIONS = ['drawing', 'task'] as const
type LocalUsageLogsSectionId = Exclude<UsageLogsSectionId, 'upstream'>

const SECTION_META: Record<UsageLogsSectionId, { titleKey: string }> = {
  common: {
    titleKey: 'Common Logs',
  },
  drawing: {
    titleKey: 'Drawing Logs',
  },
  task: {
    titleKey: 'Task Logs',
  },
  export: {
    titleKey: 'Export Center',
  },
  upstream: {
    titleKey: 'Upstream Logs',
  },
}

function UsageLogsContent() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const params = route.useParams()
  const searchParams = route.useSearch()
  const activeCategory: UsageLogsSectionId =
    params.section && isUsageLogsSectionId(params.section)
      ? params.section
      : USAGE_LOGS_DEFAULT_SECTION
  const {
    selectedUserId,
    userInfoDialogOpen,
    setUserInfoDialogOpen,
    affinityTarget,
    affinityDialogOpen,
    setAffinityDialogOpen,
  } = useUsageLogsContext()
  const { canManageScope, viewScope, setViewScope, isAdminView } =
    useLogsViewScope()
  const tabNavGroups = useMemo<NavGroup[]>(
    () => [
      {
        title: 'Task Logs',
        items: TASK_LOG_SECTIONS.map((section) => ({
          title: SECTION_META[section].titleKey,
          url: `/usage-logs/${section}`,
        })),
      },
    ],
    []
  )
  const filteredTabGroups = useSidebarConfig(tabNavGroups)
  const visibleSections = useMemo(
    () =>
      (filteredTabGroups[0]?.items ?? [])
        .map((item) => {
          if (!('url' in item) || typeof item.url !== 'string') return null
          return item.url.split('/').pop() ?? null
        })
        .filter((section): section is UsageLogsSectionId =>
          Boolean(section && isUsageLogsSectionId(section))
        ),
    [filteredTabGroups]
  )

  const isUpstreamSection = activeCategory === 'upstream'
  const savedLocalSection: LocalUsageLogsSectionId =
    searchParams.localSection ?? 'common'
  const localSection: LocalUsageLogsSectionId = isUpstreamSection
    ? savedLocalSection
    : activeCategory

  const handleHeaderTabChange = useCallback(
    (tab: string) => {
      if (tab !== 'all' && tab !== 'self') return
      setViewScope(tab as LogsViewScope)
      const targetSection = isUpstreamSection ? savedLocalSection : localSection
      void navigate({
        to: '/usage-logs/$section',
        params: { section: targetSection },
        search: {
          ...searchParams,
          page: 1,
          localSection: targetSection,
        },
        replace: true,
      })
    },
    [
      isUpstreamSection,
      localSection,
      navigate,
      savedLocalSection,
      searchParams,
      setViewScope,
    ]
  )

  useEffect(() => {
    if (!isUpstreamSection || canManageScope) return
    void navigate({
      to: '/usage-logs/$section',
      params: { section: 'common' },
      search: {
        ...searchParams,
        upstreamPage: undefined,
        upstreamPageSize: undefined,
        upstreamType: undefined,
        upstreamModel: undefined,
        upstreamToken: undefined,
        upstreamChannel: undefined,
        upstreamGroup: undefined,
        upstreamUsername: undefined,
        upstreamLocalRequestId: undefined,
        upstreamFilterRequestId: undefined,
        upstreamStartTime: undefined,
        upstreamEndTime: undefined,
        upstreamFilterKeyIndex: undefined,
      },
      replace: true,
    })
  }, [canManageScope, isUpstreamSection, navigate, searchParams])

  const isExportSection = localSection === 'export'
  const isCommonView = localSection === 'common' || localSection === 'export'
  let pageMeta = SECTION_META.task
  if (isUpstreamSection && canManageScope) {
    pageMeta = SECTION_META.upstream
  } else if (isExportSection) {
    pageMeta = SECTION_META.export
  } else if (isCommonView) {
    pageMeta = SECTION_META.common
  }
  const showTaskSwitcher =
    localSection !== 'common' && !isExportSection && visibleSections.length > 1

  const logCategory =
    localSection === 'drawing' || localSection === 'task'
      ? localSection
      : 'common'
  let localPageContent = (
    <UsageLogsTable
      logCategory={logCategory}
      isWorkspaceActive={!isUpstreamSection}
    />
  )
  if (isExportSection) {
    localPageContent = <LogExportCenter self={!isAdminView} />
  }

  return (
    <>
      <SectionPageLayout fixedContent>
        <SectionPageLayout.Title>
          {t(pageMeta.titleKey)}
        </SectionPageLayout.Title>
        <SectionPageLayout.Actions>
          {canManageScope && (
            <div className='flex flex-wrap items-center gap-2'>
              {!isUpstreamSection && (
                <ToggleGroup
                  value={[viewScope]}
                  size='sm'
                  variant='outline'
                  aria-label={t('Log scope')}
                  onValueChange={(values) => {
                    const next = values.find((value) => value !== viewScope)
                    if (next) handleHeaderTabChange(next)
                  }}
                >
                  <ToggleGroupItem value='all'>{t('All')}</ToggleGroupItem>
                  <ToggleGroupItem value='self'>
                    {t('Only Mine')}
                  </ToggleGroupItem>
                </ToggleGroup>
              )}
              <Button
                variant='outline'
                size='sm'
                nativeButton={false}
                render={
                  <Link
                    to='/usage-logs/$section'
                    params={{
                      section: isUpstreamSection
                        ? savedLocalSection
                        : 'upstream',
                    }}
                    search={{ ...searchParams, localSection }}
                  />
                }
              >
                {isUpstreamSection ? <ArrowLeft /> : <ArrowUpRight />}
                {isUpstreamSection ? t('Back to logs') : t('Upstream Logs')}
              </Button>
            </div>
          )}
        </SectionPageLayout.Actions>
        <SectionPageLayout.Content>
          <div className='flex h-full min-h-0 flex-col gap-4'>
            <Activity mode={isUpstreamSection ? 'hidden' : 'visible'}>
              <div className='flex h-full min-h-0 flex-col gap-4'>
                {showTaskSwitcher && (
                  <nav
                    className='flex flex-wrap items-center gap-2'
                    aria-label={t('Task Logs')}
                  >
                    {visibleSections.map((section) => (
                      <Button
                        key={section}
                        size='sm'
                        variant={
                          section === localSection ? 'secondary' : 'ghost'
                        }
                        nativeButton={false}
                        render={
                          <Link
                            to='/usage-logs/$section'
                            params={{ section }}
                            search={{
                              ...searchParams,
                              page: 1,
                              filter: undefined,
                              localSection: section as LocalUsageLogsSectionId,
                            }}
                            aria-current={
                              section === localSection ? 'page' : undefined
                            }
                          />
                        }
                      >
                        {t(SECTION_META[section].titleKey)}
                      </Button>
                    ))}
                  </nav>
                )}
                {isExportSection && (
                  <nav aria-label={t('Back to logs')}>
                    <Button
                      size='sm'
                      variant='ghost'
                      nativeButton={false}
                      render={
                        <Link
                          to='/usage-logs/$section'
                          params={{ section: 'common' }}
                          search={{ ...searchParams, localSection: 'common' }}
                        />
                      }
                    >
                      <ArrowLeft />
                      {t('Common Logs')}
                    </Button>
                  </nav>
                )}
                <div className='min-h-0 flex-1 overflow-y-auto'>
                  {localPageContent}
                </div>
              </div>
            </Activity>
            {canManageScope && (
              <Activity mode={isUpstreamSection ? 'visible' : 'hidden'}>
                <div className='h-full min-h-0 overflow-y-auto'>
                  <UpstreamLogsPage isWorkspaceActive={isUpstreamSection} />
                </div>
              </Activity>
            )}
          </div>
        </SectionPageLayout.Content>
      </SectionPageLayout>

      <UserInfoDialog
        userId={selectedUserId}
        open={userInfoDialogOpen}
        onOpenChange={setUserInfoDialogOpen}
      />

      <CacheStatsDialog
        open={affinityDialogOpen}
        onOpenChange={setAffinityDialogOpen}
        target={
          affinityTarget
            ? {
                rule_name: affinityTarget.rule_name || '',
                using_group:
                  affinityTarget.using_group ||
                  affinityTarget.selected_group ||
                  '',
                key_hint: affinityTarget.key_hint || '',
                key_fp: affinityTarget.key_fp || '',
              }
            : null
        }
      />
    </>
  )
}

export function UsageLogs() {
  return (
    <UsageLogsProvider>
      <UsageLogsContent />
    </UsageLogsProvider>
  )
}

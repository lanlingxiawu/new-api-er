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

import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { getUserModelsWithAvailability, getGroupStatuses } from '@/lib/api'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { Skeleton } from '@/components/ui/skeleton'
import { AlertCircle, CheckCircle2, Wrench } from 'lucide-react'
import { Alert, AlertDescription } from '@/components/ui/alert'

interface ModelWithAvailability {
  name: string
  display_name?: string
  owner?: string
  available: boolean
  reason?: string
  last_checked_time: number
}

interface GroupStatus {
  user_group: string
  available_models: number
  total_models: number
  availability_rate: number
  last_test_time: number
}

export function ModelMarketplace() {
  const { t } = useTranslation()

  // Load user models with availability status.
  const modelsQuery = useQuery({
    queryKey: ['marketplace', 'user-models-with-availability'],
    queryFn: async () => {
      const result = await getUserModelsWithAvailability()
      return result.success ? result.data ?? {} : {}
    },
    staleTime: 5 * 60 * 1000,
    refetchInterval: 10 * 60 * 1000,
  })

  // Load per-group availability summary.
  const groupStatusQuery = useQuery({
    queryKey: ['marketplace', 'group-statuses'],
    queryFn: async () => {
      const result = await getGroupStatuses()
      if (result.success && result.data?.groups) {
        const statusMap: Record<string, GroupStatus> = {}
        result.data.groups.forEach((status: GroupStatus) => {
          statusMap[status.user_group] = status
        })
        return statusMap
      }
      return {}
    },
    staleTime: 5 * 60 * 1000,
    refetchInterval: 10 * 60 * 1000,
  })

  const modelsData = modelsQuery.data || {}
  const groupStatuses = groupStatusQuery.data || {}

  const isLoading = modelsQuery.isLoading || groupStatusQuery.isLoading

  const formatTime = (timestamp: number) => {
    if (!timestamp) return t('Never tested')
    const date = new Date(timestamp * 1000)
    return date.toLocaleString()
  }

  // 鑾峰彇鍙敤鎬х櫨鍒嗘瘮棰滆壊
  const getAvailabilityColor = (rate: number) => {
    if (rate === 100) return 'bg-green-100 text-green-800'
    if (rate >= 75) return 'bg-blue-100 text-blue-800'
    if (rate >= 50) return 'bg-yellow-100 text-yellow-800'
    return 'bg-red-100 text-red-800'
  }

  return (
    <div className="w-full space-y-6">
      {/* 椤甸潰鏍囬 */}
      <div>
        <h1 className="text-3xl font-bold tracking-tight">{t('Model Marketplace')}</h1>
        <p className="text-muted-foreground mt-2">
          {t('View all available models and their status')}
        </p>
      </div>

      {/* 鏃犳ā鍨嬫彁绀?*/}
      {Object.keys(modelsData).length === 0 && !isLoading && (
        <Alert>
          <AlertCircle className="h-4 w-4" />
          <AlertDescription>
            {t('No models available. Please configure your groups.')}
          </AlertDescription>
        </Alert>
      )}

      {/* 鎸夊垎缁勬樉绀烘ā鍨?*/}
      <div className="grid gap-6">
        {Object.entries(modelsData).map(([groupName, groupData]: [string, any]) => {
          const status = groupStatuses[groupName]
          const availableCount = status?.available_models || 0
          const totalCount = status?.total_models || groupData.total_count
          const availabilityRate = status?.availability_rate || 0

          return (
            <Card key={groupName} className="overflow-hidden">
              <CardHeader className="pb-3">
                <div className="flex items-center justify-between">
                  <div>
                    <CardTitle className="text-xl">{groupName}</CardTitle>
                    <CardDescription>
                      {t('Group')} 鈥?{t('Models')}: {availableCount}/{totalCount}
                    </CardDescription>
                  </div>
                  <div className="text-right">
                    <Badge className={getAvailabilityColor(availabilityRate)}>
                      {availabilityRate.toFixed(0)}% {t('Available')}
                    </Badge>
                    <p className="text-xs text-muted-foreground mt-2">
                      {t('Last tested')}: {formatTime(status?.last_test_time)}
                    </p>
                  </div>
                </div>
              </CardHeader>

              <CardContent>
                {isLoading ? (
                  // 鍔犺浇楠ㄦ灦
                  <div className="space-y-3">
                    {[...Array(3)].map((_, i) => (
                      <Skeleton key={i} className="h-12 w-full" />
                    ))}
                  </div>
                ) : (
                  // 妯″瀷鍒楄〃
                  <div className="space-y-2">
                    {groupData.models.map((model: ModelWithAvailability) => (
                      <div
                        key={model.name}
                        className="flex items-center justify-between p-3 rounded-lg border hover:bg-muted/50 transition-colors"
                      >
                        <div className="flex items-center gap-3 flex-1">
                          {/* 鍙敤鎬у浘鏍囧拰鏂囨湰 */}
                          {model.available ? (
                            <div className="flex items-center gap-2">
                              <CheckCircle2 className="h-5 w-5 text-green-600 flex-shrink-0" />
                              <div>
                                <p className="font-medium text-sm">{model.name}</p>
                                {model.owner && (
                                  <p className="text-xs text-muted-foreground">{model.owner}</p>
                                )}
                              </div>
                            </div>
                          ) : (
                            <div className="flex items-center gap-2">
                              <Wrench className="h-5 w-5 text-orange-500 flex-shrink-0" />
                              <div>
                                <p className="font-medium text-sm line-through text-muted-foreground">
                                  {model.name}
                                </p>
                                {model.owner && (
                                  <p className="text-xs text-muted-foreground">{model.owner}</p>
                                )}
                              </div>
                            </div>
                          )}
                        </div>

                        {/* 鐘舵€佹爣绛?*/}
                        <div className="flex items-center gap-2 ml-4 flex-shrink-0">
                          {model.available ? (
                            <Badge variant="outline" className="inline-flex items-center gap-1 bg-green-50 text-green-700 border-green-200">
                              <CheckCircle2 className="h-3.5 w-3.5" />
                              {t('Available')}
                            </Badge>
                          ) : (
                            <Badge variant="outline" className="inline-flex items-center gap-1 bg-orange-50 text-orange-700 border-orange-200">
                              <Wrench className="h-3.5 w-3.5" />
                              {model.reason || t('Repairing')}
                            </Badge>
                          )}
                        </div>
                      </div>
                    ))}
                  </div>
                )}
              </CardContent>
            </Card>
          )
        })}
      </div>

      {/* 鍒锋柊鎻愮ず */}
      <p className="text-xs text-muted-foreground text-center">
        {t('Last updated')}: {new Date().toLocaleString()}
        <br />
        {t('Updates every 30 minutes')}
      </p>
    </div>
  )
}

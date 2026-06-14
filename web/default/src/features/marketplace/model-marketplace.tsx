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
import { getUserModels } from '@/lib/api'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { AlertCircle } from 'lucide-react'
import { Alert, AlertDescription } from '@/components/ui/alert'

export function ModelMarketplace() {
  const { t } = useTranslation()

  const modelsQuery = useQuery({
    queryKey: ['marketplace', 'user-models'],
    queryFn: async () => {
      const result = await getUserModels()
      return result.success ? (result.data ?? []) : []
    },
    staleTime: 5 * 60 * 1000,
  })

  const models = modelsQuery.data ?? []
  const isLoading = modelsQuery.isLoading

  return (
    <div className='w-full space-y-6'>
      <div>
        <h1 className='text-3xl font-bold tracking-tight'>{t('Model Marketplace')}</h1>
        <p className='text-muted-foreground mt-2'>
          {t('View all available models and their status')}
        </p>
      </div>

      {!isLoading && models.length === 0 && (
        <Alert>
          <AlertCircle className='h-4 w-4' />
          <AlertDescription>
            {t('No models available. Please configure your groups.')}
          </AlertDescription>
        </Alert>
      )}

      <Card>
        <CardHeader className='pb-3'>
          <CardTitle>{t('Available Models')}</CardTitle>
        </CardHeader>
        <CardContent>
          {isLoading ? (
            <div className='space-y-3'>
              {[...Array(5)].map((_, i) => (
                <Skeleton key={i} className='h-8 w-full' />
              ))}
            </div>
          ) : (
            <div className='grid grid-cols-1 sm:grid-cols-2 md:grid-cols-3 gap-2'>
              {models.map((name: string) => (
                <div
                  key={name}
                  className='rounded-md border px-3 py-2 text-sm font-mono'
                >
                  {name}
                </div>
              ))}
            </div>
          )}
        </CardContent>
      </Card>
    </div>
  )
}

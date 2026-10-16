import { Link, useLocation } from '@tanstack/react-router'
import { useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { FormNavigationGuard } from '@/features/system-settings/components/form-navigation-guard'

import { ModelPricingPanel } from './model-pricing-panel'

export function ModelPricingWorkspace({ children }: { children: ReactNode }) {
  const { t } = useTranslation()
  const search = useLocation({ select: (location) => location.searchStr })
  const [dirty, setDirty] = useState(false)
  const model = new URLSearchParams(search).get('model')
  if (!model) return children
  return (
    <div className='flex min-h-0 flex-1 flex-col gap-3'>
      <div>
        <Button
          role='link'
          variant='outline'
          size='sm'
          render={
            <Link
              to='/system-settings/billing/$section'
              params={{ section: 'model-pricing' }}
              search={{ model: undefined }}
            />
          }
        >
          {t('All Models')}
        </Button>
      </div>
      <FormNavigationGuard when={dirty} />
      <ModelPricingPanel
        key={model}
        modelName={model}
        onDirtyChange={setDirty}
      />
    </div>
  )
}

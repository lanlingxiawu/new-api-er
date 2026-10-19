import { useTranslation } from 'react-i18next'

import {
  Field,
  FieldGroup,
  FieldLabel,
  FieldLegend,
  FieldSet,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'

import {
  parseGuardDraft,
  type GuardDraft,
  type GuardStatus,
} from './group-retry-guard'

export function GroupRetryGuardFields(props: {
  id: number
  value: GuardDraft
  disabled: boolean
  active: boolean
  available: boolean
  status?: GuardStatus
  onChange: (value: GuardDraft) => void
}) {
  const { t } = useTranslation()
  const fields: {
    key:
      | 'window'
      | 'rpm'
      | 'latency'
      | 'count'
      | 'cooldown'
      | 'multiplier'
      | 'maximum'
      | 'reset'
    label: string
    min: number
    max: number
    step?: number
  }[] = [
    {
      key: 'window',
      label: t('Observation window (seconds)'),
      min: 10,
      max: 3600,
    },
    { key: 'rpm', label: t('RPM threshold'), min: 1, max: 10000000 },
    {
      key: 'latency',
      label: t('Response delay threshold (seconds)'),
      min: 0.001,
      max: 3600,
      step: 0.001,
    },
    { key: 'count', label: t('Slow response count'), min: 1, max: 10000000 },
    {
      key: 'cooldown',
      label: t('Initial cooldown (seconds)'),
      min: 1,
      max: 86400,
    },
  ]
  if (props.value.mode === 'progressive') {
    fields.push(
      { key: 'multiplier', label: t('Cooldown multiplier'), min: 2, max: 10 },
      {
        key: 'maximum',
        label: t('Maximum cooldown (seconds)'),
        min: 1,
        max: 86400,
      },
      {
        key: 'reset',
        label: t('Reset after stable recovery (seconds)'),
        min: 10,
        max: 86400,
      }
    )
  }
  let statusText = t('Collecting samples')
  if (!props.available) {
    statusText = t(
      'Monitoring unavailable. Existing retry settings remain active.'
    )
  } else if (
    props.status &&
    props.status.retry_disabled_until > Date.now() / 1000
  ) {
    statusText = t(
      'Retries paused until {{time}} (cooldown {{seconds}}s, level {{level}})',
      {
        time: new Date(
          props.status.retry_disabled_until * 1000
        ).toLocaleTimeString(),
        seconds: props.status.current_cooldown_seconds,
        level: props.status.cooldown_level,
      }
    )
  } else if (props.status?.ready) statusText = t('Normal')

  const rule = parseGuardDraft(props.value)
  const cooldownSteps: number[] = []
  let lastCooldown = rule?.cooldown_seconds ?? 0
  const maximumCooldown = rule?.max_cooldown_seconds ?? lastCooldown
  if (rule) {
    cooldownSteps.push(rule.cooldown_seconds)
    if (rule.cooldown_mode === 'progressive') {
      while (cooldownSteps.length < 4 && lastCooldown < maximumCooldown) {
        lastCooldown = Math.min(
          maximumCooldown,
          lastCooldown * (rule.cooldown_multiplier ?? 1)
        )
        cooldownSteps.push(lastCooldown)
      }
    }
  }
  const cooldownPreview =
    cooldownSteps.join(' → ') +
    (rule?.cooldown_mode === 'progressive' && lastCooldown < maximumCooldown
      ? ' → …'
      : '')
  const sections = [
    {
      key: 'trigger',
      title: t('Trigger conditions'),
      fields: fields.slice(0, 4),
    },
    {
      key: 'cooldown',
      title: t('Cooldown and recovery'),
      fields: fields.slice(4),
    },
  ]

  return (
    <div className='flex flex-col gap-4 border-t pt-3'>
      <div className='flex items-center gap-3'>
        <Switch
          id={`retry-guard-${props.id}`}
          checked={props.value.enabled}
          aria-controls={`retry-guard-${props.id}-parameters`}
          aria-expanded={props.value.enabled}
          disabled={props.disabled}
          onCheckedChange={(enabled) =>
            props.onChange({ ...props.value, enabled })
          }
        />
        <Label htmlFor={`retry-guard-${props.id}`}>
          {t('Enable protection for this group')}
        </Label>
      </div>
      {props.value.enabled && (
        <>
          <div
            id={`retry-guard-${props.id}-parameters`}
            className='bg-muted/20 flex flex-col gap-5 rounded-lg p-4'
          >
            {sections.map((section) => (
              <FieldSet key={section.key} className='min-w-0 gap-3'>
                <FieldLegend
                  variant='label'
                  className='mb-0 flex w-full flex-wrap items-center justify-between gap-3'
                >
                  <span>{section.title}</span>
                  {section.key === 'cooldown' && (
                    <ToggleGroup
                      variant='outline'
                      size='sm'
                      className='w-full sm:w-auto'
                      aria-label={t('Cooldown mode')}
                      value={[props.value.mode]}
                      disabled={props.disabled}
                      onValueChange={(values) => {
                        const mode = values[0]
                        if (mode === 'fixed' || mode === 'progressive') {
                          props.onChange({ ...props.value, mode })
                        }
                      }}
                    >
                      <ToggleGroupItem value='fixed' className='min-w-0 flex-1'>
                        {t('Fixed cooldown')}
                      </ToggleGroupItem>
                      <ToggleGroupItem
                        value='progressive'
                        className='min-w-0 flex-1'
                      >
                        {t('Progressive cooldown')}
                      </ToggleGroupItem>
                    </ToggleGroup>
                  )}
                </FieldLegend>
                <FieldGroup className='grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-4'>
                  {section.fields.map((field) => (
                    <Field key={field.key}>
                      <FieldLabel
                        htmlFor={`retry-guard-${props.id}-${field.key}`}
                      >
                        {field.label}
                      </FieldLabel>
                      <Input
                        id={`retry-guard-${props.id}-${field.key}`}
                        type='number'
                        min={field.min}
                        max={field.max}
                        step={field.step ?? 1}
                        disabled={props.disabled}
                        value={props.value[field.key]}
                        onChange={(event) =>
                          props.onChange({
                            ...props.value,
                            [field.key]: event.target.value,
                          })
                        }
                      />
                    </Field>
                  ))}
                </FieldGroup>
                {rule && (
                  <p
                    className='text-muted-foreground text-xs leading-relaxed'
                    aria-live='polite'
                  >
                    {section.key === 'trigger'
                      ? t(
                          'Within {{window}}s: RPM ≥ {{rpm}} and at least {{count}} responses taking ≥ {{delay}}s.',
                          {
                            window: rule.window_seconds,
                            rpm: rule.rpm_threshold,
                            count: rule.slow_count_threshold,
                            delay: rule.latency_threshold_ms / 1000,
                          }
                        )
                      : t('Cooldown sequence: {{sequence}} seconds', {
                          sequence: cooldownPreview,
                        })}
                  </p>
                )}
              </FieldSet>
            ))}
          </div>
          {!rule && (
            <p className='text-destructive text-xs'>
              {t(
                'Check the protection limits. Maximum cooldown must be at least the initial cooldown.'
              )}
            </p>
          )}
          {props.active && props.value.enabled && (
            <div
              className='text-muted-foreground space-y-1 text-xs'
              aria-live='polite'
            >
              <p>{statusText}</p>
              {props.available && props.status && (
                <p>
                  {t(
                    'Last {{seconds}}s: {{rpm}} RPM, {{count}} slow responses',
                    {
                      seconds: props.status.window_seconds,
                      rpm: props.status.rpm.toFixed(1),
                      count: props.status.slow_count,
                    }
                  )}
                </p>
              )}
            </div>
          )}
        </>
      )}
    </div>
  )
}

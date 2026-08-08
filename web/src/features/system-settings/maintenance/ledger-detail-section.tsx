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
import * as z from 'zod'
import { useEffect, useMemo, useRef } from 'react'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Separator } from '@/components/ui/separator'
import { SettingsForm } from '../components/settings-form-layout'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { useSettingsSaveConfirmation } from '../components/settings-save-confirmation'
import { SettingsSection } from '../components/settings-section'
import { useUpdateOption } from '../hooks/use-update-option'
import {
  numberInputNoSpinnerClassName,
  safeNumberFieldProps,
} from '../utils/numeric-field'

/**
 * react-hook-form 7 interprets dotted `name` strings as nested paths.
 * We model the form with nested objects and flatten before persisting.
 */
const schema = z.object({
  ledger_detail_setting: z.object({
    export_user_cooldown_sec: z.number().int().min(1),
    export_batch_size: z.number().int().min(1),
    export_batch_sleep_ms: z.number().int().min(0),
    export_rows_per_file: z.number().int().min(1),
    export_max_range_sec: z.number().int().min(1),
    export_timeout_sec: z.number().int().min(1),
    list_max_range_sec: z.number().int().min(1),
    list_default_range_sec: z.number().int().min(1),
    list_default_limit: z.number().int().min(1),
    list_max_limit: z.number().int().min(1),
    list_scan_batch_size: z.number().int().min(1),
    list_scan_rows_per_req: z.number().int().min(1),
  }),
})

type FormValues = z.infer<typeof schema>

type FlatDefaults = {
  'ledger_detail_setting.export_user_cooldown_sec': number
  'ledger_detail_setting.export_batch_size': number
  'ledger_detail_setting.export_batch_sleep_ms': number
  'ledger_detail_setting.export_rows_per_file': number
  'ledger_detail_setting.export_max_range_sec': number
  'ledger_detail_setting.export_timeout_sec': number
  'ledger_detail_setting.list_max_range_sec': number
  'ledger_detail_setting.list_default_range_sec': number
  'ledger_detail_setting.list_default_limit': number
  'ledger_detail_setting.list_max_limit': number
  'ledger_detail_setting.list_scan_batch_size': number
  'ledger_detail_setting.list_scan_rows_per_req': number
}

type LedgerDetailSectionProps = { defaultValues: FlatDefaults }

function buildFormDefaults(d: FlatDefaults): FormValues {
  return {
    ledger_detail_setting: {
      export_user_cooldown_sec: d['ledger_detail_setting.export_user_cooldown_sec'],
      export_batch_size: d['ledger_detail_setting.export_batch_size'],
      export_batch_sleep_ms: d['ledger_detail_setting.export_batch_sleep_ms'],
      export_rows_per_file: d['ledger_detail_setting.export_rows_per_file'],
      export_max_range_sec: d['ledger_detail_setting.export_max_range_sec'],
      export_timeout_sec: d['ledger_detail_setting.export_timeout_sec'],
      list_max_range_sec: d['ledger_detail_setting.list_max_range_sec'],
      list_default_range_sec: d['ledger_detail_setting.list_default_range_sec'],
      list_default_limit: d['ledger_detail_setting.list_default_limit'],
      list_max_limit: d['ledger_detail_setting.list_max_limit'],
      list_scan_batch_size: d['ledger_detail_setting.list_scan_batch_size'],
      list_scan_rows_per_req: d['ledger_detail_setting.list_scan_rows_per_req'],
    },
  }
}

function normalizeFormValues(v: FormValues): FlatDefaults {
  const s = v.ledger_detail_setting
  return {
    'ledger_detail_setting.export_user_cooldown_sec': s.export_user_cooldown_sec,
    'ledger_detail_setting.export_batch_size': s.export_batch_size,
    'ledger_detail_setting.export_batch_sleep_ms': s.export_batch_sleep_ms,
    'ledger_detail_setting.export_rows_per_file': s.export_rows_per_file,
    'ledger_detail_setting.export_max_range_sec': s.export_max_range_sec,
    'ledger_detail_setting.export_timeout_sec': s.export_timeout_sec,
    'ledger_detail_setting.list_max_range_sec': s.list_max_range_sec,
    'ledger_detail_setting.list_default_range_sec': s.list_default_range_sec,
    'ledger_detail_setting.list_default_limit': s.list_default_limit,
    'ledger_detail_setting.list_max_limit': s.list_max_limit,
    'ledger_detail_setting.list_scan_batch_size': s.list_scan_batch_size,
    'ledger_detail_setting.list_scan_rows_per_req': s.list_scan_rows_per_req,
  }
}

const exportFields: Array<{
  name: keyof FormValues['ledger_detail_setting']
  label: string
  description: string
  min: number
}> = [
  {
    name: 'export_user_cooldown_sec',
    label: 'Export User Cooldown',
    description: 'Minimum seconds between two exports per user. Requests during cooldown are rejected.',
    min: 1,
  },
  {
    name: 'export_batch_size',
    label: 'Export Batch Size',
    description: 'Rows fetched per DB batch. Affects memory usage and DB load.',
    min: 1,
  },
  {
    name: 'export_batch_sleep_ms',
    label: 'Batch Sleep (ms)',
    description: 'Milliseconds to sleep after each batch to throttle DB load.',
    min: 0,
  },
  {
    name: 'export_rows_per_file',
    label: 'Rows Per File',
    description: 'Rows before a new shard file is created automatically.',
    min: 1,
  },
  {
    name: 'export_max_range_sec',
    label: 'Max Export Time Range (s)',
    description: 'Maximum allowed time range for a single export (seconds).',
    min: 1,
  },
  {
    name: 'export_timeout_sec',
    label: 'Export Job Timeout (s)',
    description: 'Max runtime for an export goroutine (seconds). Job is marked failed on timeout.',
    min: 1,
  },
]

const listFields: Array<{
  name: keyof FormValues['ledger_detail_setting']
  label: string
  description: string
  min: number
}> = [
  {
    name: 'list_max_range_sec',
    label: 'List Max Time Range (s)',
    description: 'Maximum allowed time range for list queries (seconds).',
    min: 1,
  },
  {
    name: 'list_default_range_sec',
    label: 'List Default Time Range (s)',
    description: 'Default query range when no time range is provided (seconds).',
    min: 1,
  },
  {
    name: 'list_default_limit',
    label: 'List Default Page Size',
    description: 'Default page size when no limit parameter is given.',
    min: 1,
  },
  {
    name: 'list_max_limit',
    label: 'List Max Page Size',
    description: 'Maximum limit value allowed per list request.',
    min: 1,
  },
  {
    name: 'list_scan_batch_size',
    label: 'List Scan Batch Size',
    description: 'Rows pulled per DB batch during in-memory tag filtering.',
    min: 1,
  },
  {
    name: 'list_scan_rows_per_req',
    label: 'List Max Scan Rows',
    description: 'Max rows scanned per list request. Returns truncated results when exceeded.',
    min: 1,
  },
]

export function LedgerDetailSection({ defaultValues }: LedgerDetailSectionProps) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()
  const requestSaveConfirmation = useSettingsSaveConfirmation()

  const formDefaults = useMemo(() => buildFormDefaults(defaultValues), [defaultValues])

  const form = useForm<FormValues>({
    resolver: zodResolver(schema),
    defaultValues: formDefaults,
  })

  const baselineRef = useRef<FlatDefaults>(defaultValues)
  const baselineSerializedRef = useRef<string>(JSON.stringify(defaultValues))

  useEffect(() => {
    const serialized = JSON.stringify(defaultValues)
    if (serialized === baselineSerializedRef.current) return
    baselineRef.current = defaultValues
    baselineSerializedRef.current = serialized
    form.reset(buildFormDefaults(defaultValues))
  }, [defaultValues, form])

  const onSubmit = async (values: FormValues) => {
    const normalized = normalizeFormValues(values)
    const changedKeys = (Object.keys(normalized) as Array<keyof FlatDefaults>).filter(
      (key) => normalized[key] !== baselineRef.current[key],
    )
    if (changedKeys.length === 0) {
      toast.info(t('No changes to save'))
      return
    }
    await requestSaveConfirmation(async () => {
      for (const key of changedKeys) {
        await updateOption.mutateAsync({ key, value: normalized[key] })
      }
      baselineRef.current = normalized
      baselineSerializedRef.current = JSON.stringify(normalized)
      form.reset(buildFormDefaults(normalized))
    })
  }

  const renderFields = (fields: typeof exportFields) =>
    fields.map((item) => (
      <FormField
        key={item.name}
        control={form.control}
        name={`ledger_detail_setting.${item.name}`}
        render={({ field }) => (
          <FormItem>
            <FormLabel>{t(item.label)}</FormLabel>
            <FormControl>
              <Input
                className={numberInputNoSpinnerClassName}
                type='number'
                inputMode='numeric'
                min={item.min}
                step={1}
                {...safeNumberFieldProps(field)}
              />
            </FormControl>
            <FormDescription>{t(item.description)}</FormDescription>
            <FormMessage />
          </FormItem>
        )}
      />
    ))

  return (
    <SettingsSection title={t('Ledger Detail Settings')}>
      <Form {...form}>
        <SettingsForm onSubmit={form.handleSubmit(onSubmit)}>
          <SettingsPageFormActions
            onSave={form.handleSubmit(onSubmit)}
            isSaving={updateOption.isPending}
            saveLabel='Save ledger detail settings'
          />

          <div>
            <h4 className='text-sm font-medium'>{t('Ledger Detail Export')}</h4>
          </div>
          {renderFields(exportFields)}

          <Separator />

          <div>
            <h4 className='text-sm font-medium'>{t('Ledger Detail List')}</h4>
          </div>
          {renderFields(listFields)}
        </SettingsForm>
      </Form>
    </SettingsSection>
  )
}

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Download, RefreshCw, X } from 'lucide-react'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Combobox } from '@/components/ui/combobox'
import { Field, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Label } from '@/components/ui/label'
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { CompactDateTimeRangePicker } from '@/features/usage-logs/components/compact-date-time-range-picker'
import { useDebounce } from '@/hooks'
import { useAuthStore } from '@/stores/auth-store'

import {
  EMPLOYEE_EXPORT_BASE as BASE,
  employeeTemplateName,
  exportRequest,
  type EmployeeExportCapabilities,
} from '../employee-api'
import { EmployeeExportJobs } from './employee-export-jobs'
import { ExportFilterSelect } from './export-filter-select'

const FIELD_LABELS: Record<string, string> = {
  date: 'Date',
  username: 'User',
  model_name: 'Model',
  token_name: 'Token',
  group: 'Group',
}
const MODE_LABELS = {
  detail: 'Detail',
  summary: 'Summary',
  both: 'Detail + summary',
}

interface EmployeeExportPrefill {
  start?: Date
  end?: Date
  customerId?: number
}

export function EmployeeCustomerExport(props: {
  prefill?: EmployeeExportPrefill
}) {
  const { t } = useTranslation()
  const userId = useAuthStore((state) => state.auth.user?.id)
  const [open, setOpen] = useState(false)
  const [tab, setTab] = useState('new')
  const capabilities = useQuery({
    queryKey: ['employee-export-capabilities', userId],
    queryFn: () =>
      exportRequest<EmployeeExportCapabilities>(
        'get',
        `${BASE}/capabilities`,
        undefined,
        { quiet: true }
      ),
    enabled: Boolean(userId),
    refetchOnWindowFocus: true,
  })
  if (capabilities.isError) {
    return (
      <Button variant='outline' onClick={() => void capabilities.refetch()}>
        <RefreshCw />
        {t('Retry export access')}
      </Button>
    )
  }
  if (!capabilities.data?.enabled) return null
  return (
    <>
      <Button
        variant='outline'
        onClick={() => {
          setTab('new')
          setOpen(true)
          void capabilities.refetch()
        }}
      >
        <Download />
        {t('Export customer data')}
      </Button>
      <Sheet open={open} onOpenChange={setOpen}>
        <SheetContent
          side='right'
          className='flex w-full flex-col gap-0 sm:max-w-3xl'
        >
          <SheetHeader>
            <SheetTitle>{t('Export customer data')}</SheetTitle>
            <SheetDescription>
              {t(
                'Exports run in the background and are throttled to protect live traffic.'
              )}
            </SheetDescription>
          </SheetHeader>
          <Tabs
            value={tab}
            onValueChange={(value) => setTab(String(value))}
            className='min-h-0 flex-1 gap-0'
          >
            <TabsList className='mx-4 mb-2 shrink-0'>
              <TabsTrigger value='new'>{t('New Export')}</TabsTrigger>
              <TabsTrigger value='history'>{t('Export history')}</TabsTrigger>
            </TabsList>
            <TabsContent value='new' className='min-h-0 flex-1 overflow-hidden'>
              {open && (
                <EmployeeExportForm
                  capabilities={capabilities.data}
                  prefill={props.prefill}
                  onClose={() => setOpen(false)}
                  onCreated={() => setTab('history')}
                />
              )}
            </TabsContent>
            <TabsContent
              value='history'
              className='min-h-0 flex-1 overflow-y-auto px-4 py-2'
            >
              {open && tab === 'history' && <EmployeeExportJobs />}
            </TabsContent>
          </Tabs>
        </SheetContent>
      </Sheet>
    </>
  )
}

function EmployeeExportForm(props: {
  capabilities: EmployeeExportCapabilities
  prefill?: EmployeeExportPrefill
  onClose: () => void
  onCreated: () => void
}) {
  const { t } = useTranslation()
  const templateInputId = useId()
  const userId = useAuthStore((state) => state.auth.user?.id)
  const client = useQueryClient()
  const [templateId, setTemplateId] = useState(
    () => props.capabilities.templates[0]?.key ?? ''
  )
  const [all, setAll] = useState(false)
  const [customers, setCustomers] = useState<number[]>(() =>
    props.prefill?.customerId ? [props.prefill.customerId] : []
  )
  const [customerLabels, setCustomerLabels] = useState<Record<string, string>>(
    {}
  )
  const [range, setRange] = useState<{ start?: Date; end?: Date }>(() => ({
    start: props.prefill?.start ?? new Date(Date.now() - 86400000),
    end: props.prefill?.end ?? new Date(),
  }))
  const [filters, setFilters] = useState<Record<string, string>>({})
  const template = props.capabilities.templates.find(
    (item) => item.key === templateId
  )
  const start = Math.floor((range.start?.getTime() ?? 0) / 1000)
  const end = Math.floor((range.end?.getTime() ?? 0) / 1000)
  const validRange =
    start > 0 && end >= start && end - start <= props.capabilities.max_range_sec
  const payload = {
    template_id: templateId,
    // Used only by templates without a pinned timezone (the builtin ones).
    timezone: Intl.DateTimeFormat().resolvedOptions().timeZone,
    customer_ids: all ? [] : customers,
    all_customers: all,
    start_timestamp: start,
    end_timestamp: end,
    filters,
  }
  const serialized = JSON.stringify(payload)
  const estimatePayload = useDebounce(serialized, 400)
  const ready = Boolean(template && validRange && (all || customers.length))
  const estimate = useQuery({
    queryKey: ['employee-export-estimate', userId, estimatePayload],
    queryFn: () =>
      exportRequest<{ rows: number; capped: boolean; available: boolean }>(
        'post',
        `${BASE}/estimate`,
        JSON.parse(estimatePayload),
        { quiet: true }
      ),
    // Never enable a request with the previous, potentially empty selection.
    enabled: ready && estimatePayload === serialized,
    refetchOnWindowFocus: false,
    staleTime: 30_000,
    retry: false,
  })
  const create = useMutation({
    mutationFn: (request: typeof payload) =>
      exportRequest('post', `${BASE}/jobs`, request),
    onSuccess: () => {
      toast.success(t('Export started'))
      void client.invalidateQueries({ queryKey: ['employee-export-jobs'] })
      props.onCreated()
    },
    // The HTTP client has already shown the error; refresh access in case it changed.
    onError: () => {
      void client.invalidateQueries({
        queryKey: ['employee-export-capabilities', userId],
      })
    },
  })
  return (
    <form
      className='flex h-full min-h-0 flex-col'
      onSubmit={(event) => {
        event.preventDefault()
        if (ready && !create.isPending) create.mutate(payload)
      }}
    >
      <div className='min-h-0 flex-1 overflow-y-auto px-4 py-2'>
        <fieldset disabled={create.isPending} className='space-y-5'>
          <FieldGroup className='gap-4'>
            <Field>
              <FieldLabel htmlFor={templateInputId}>{t('Template')}</FieldLabel>
              <Combobox
                id={templateInputId}
                options={props.capabilities.templates.map((tpl) => ({
                  value: tpl.key,
                  label: employeeTemplateName(tpl, t),
                }))}
                value={templateId}
                onValueChange={(value) => {
                  setTemplateId(value ?? '')
                  setFilters({})
                }}
                placeholder={t('Select template')}
              />
              <p className='text-muted-foreground text-xs'>
                {t('Export templates are managed by your administrator.')}
              </p>
            </Field>
            <Field>
              <FieldLabel>{t('Date Range')}</FieldLabel>
              <CompactDateTimeRangePicker
                start={range.start}
                end={range.end}
                onChange={setRange}
                disabled={create.isPending}
              />
              {!validRange && (
                <p className='text-destructive text-sm' role='alert'>
                  {t('Choose a time range within {{days}} days.', {
                    days: Math.floor(props.capabilities.max_range_sec / 86400),
                  })}
                </p>
              )}
            </Field>
            <Field>
              <FieldLabel>{t('Customers')}</FieldLabel>
              <Label className='flex items-center gap-2'>
                <Checkbox
                  checked={all}
                  onCheckedChange={(checked) => {
                    setAll(Boolean(checked))
                    setCustomers([])
                    setFilters({})
                  }}
                />
                {t('All my customers')}
              </Label>
              {!all && (
                <>
                  <ExportFilterSelect
                    base={BASE}
                    field='customer'
                    label={t('Customer')}
                    value=''
                    disabled={
                      create.isPending ||
                      customers.length >= props.capabilities.max_customers
                    }
                    onSelect={(option) =>
                      setCustomerLabels((current) => ({
                        ...current,
                        [option.value]: option.label,
                      }))
                    }
                    onChange={(value) => {
                      const id = Number(value)
                      if (
                        id &&
                        !customers.includes(id) &&
                        customers.length < props.capabilities.max_customers
                      ) {
                        setCustomers([...customers, id])
                        setFilters({})
                      }
                    }}
                  />
                  <div className='flex flex-wrap gap-2'>
                    {customers.map((id) => (
                      <Badge
                        key={id}
                        variant='secondary'
                        className='max-w-full gap-1'
                      >
                        <span className='truncate'>
                          {customerLabels[id] ?? `#${id}`}
                        </span>
                        <Button
                          type='button'
                          size='icon'
                          variant='ghost'
                          className='size-5 shrink-0'
                          aria-label={t('Remove')}
                          onClick={() => {
                            setCustomers(
                              customers.filter((item) => item !== id)
                            )
                            setFilters({})
                          }}
                        >
                          <X className='size-3' />
                        </Button>
                      </Badge>
                    ))}
                  </div>
                </>
              )}
              <p className='text-muted-foreground text-xs'>
                {t('Up to {{count}} customers per export', {
                  count: props.capabilities.max_customers,
                })}
              </p>
            </Field>
            {template && (all || customers.length > 0) && (
              <div className='grid gap-3 sm:grid-cols-2'>
                {template.allowed_filters.map((field) => (
                  <Field key={field}>
                    <FieldLabel>{t(FIELD_LABELS[field] ?? field)}</FieldLabel>
                    <ExportFilterSelect
                      base={BASE}
                      field={field}
                      label={t(FIELD_LABELS[field] ?? field)}
                      value={filters[field] ?? ''}
                      start={start}
                      end={end}
                      customerIds={all ? undefined : customers}
                      templateId={template.key}
                      disabled={create.isPending || !validRange}
                      onChange={(value) =>
                        setFilters({ ...filters, [field]: value })
                      }
                    />
                  </Field>
                ))}
              </div>
            )}
          </FieldGroup>
          {template && (
            <section className='space-y-3 rounded-md border p-3'>
              <div className='flex flex-wrap items-center justify-between gap-2'>
                <h3 className='text-sm font-medium'>{t('Output')}</h3>
                <div className='flex gap-2'>
                  <Badge variant='outline'>
                    {template.format === 'xlsx' ? 'Excel' : 'CSV'}
                  </Badge>
                  <Badge variant='secondary'>
                    {t(MODE_LABELS[template.mode])}
                  </Badge>
                </div>
              </div>
              {template.mode !== 'summary' && (
                <details>
                  <summary className='cursor-pointer text-sm'>
                    {t('Export columns')} · {template.columns.length}
                  </summary>
                  <div className='mt-2 flex max-h-40 flex-wrap gap-1.5 overflow-y-auto'>
                    {template.columns.map((key) => (
                      <Badge key={key} variant='outline'>
                        {t(
                          props.capabilities.columns.find(
                            (column) => column.key === key
                          )?.label ?? key
                        )}
                      </Badge>
                    ))}
                  </div>
                </details>
              )}
              {template.mode !== 'detail' && (
                <div className='space-y-2'>
                  <p className='text-muted-foreground text-xs'>
                    {t('Group by')}
                  </p>
                  <div className='flex flex-wrap gap-1.5'>
                    {template.summary_dims.map((key) => (
                      <Badge key={key} variant='outline'>
                        {t(FIELD_LABELS[key] ?? key)}
                      </Badge>
                    ))}
                  </div>
                  <p className='text-muted-foreground text-xs'>
                    {template.columns.includes('quota')
                      ? t(
                          'Summary includes request counts, tokens, quota, and cost.'
                        )
                      : t('Summary includes request counts, tokens, and cost.')}
                  </p>
                </div>
              )}
            </section>
          )}
        </fieldset>
      </div>
      <SheetFooter className='shrink-0 border-t'>
        <div className='flex w-full flex-wrap items-center justify-between gap-3'>
          <div className='text-muted-foreground text-xs' aria-live='polite'>
            {ready &&
              estimatePayload === serialized &&
              estimate.data?.available &&
              t('Scans about {{rows}} rows', {
                rows: `${estimate.data.rows.toLocaleString()}${estimate.data.capped ? '+' : ''}`,
              })}
          </div>
          <div className='flex gap-2'>
            <Button
              type='button'
              variant='outline'
              disabled={create.isPending}
              onClick={props.onClose}
            >
              {t('Cancel')}
            </Button>
            <Button type='submit' disabled={!ready || create.isPending}>
              {create.isPending ? t('Loading...') : t('Start Export')}
            </Button>
          </div>
        </div>
      </SheetFooter>
    </form>
  )
}

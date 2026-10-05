import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { FileText, Plus } from 'lucide-react'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Combobox } from '@/components/ui/combobox'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from '@/components/ui/dialog'
import {
  Empty,
  EmptyHeader,
  EmptyTitle,
  EmptyDescription,
} from '@/components/ui/empty'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
  SheetDescription,
  SheetFooter,
} from '@/components/ui/sheet'
import { Switch } from '@/components/ui/switch'

import {
  EMPLOYEE_TEMPLATE_BASE,
  employeeTemplateName,
  exportRequest,
  getEmployeeTemplates,
  getEmployeeExportColumns,
  type EmployeeExportCatalog,
  type EmployeeExportTemplate,
} from '../employee-api'
import { TROUBLESHOOTING_FILTERS } from '../employee-filters'
import type { ExportFormat, ExportMode, SummaryDimension } from '../types'
import { ColumnPicker } from './column-picker'

const DIMENSIONS: { value: SummaryDimension; label: string }[] = [
  { value: 'date', label: 'Date' },
  { value: 'username', label: 'User' },
  { value: 'model_name', label: 'Model' },
  { value: 'token_name', label: 'Token' },
  { value: 'group', label: 'Group' },
]
const FILTERS = [
  ...DIMENSIONS.filter((item) =>
    ['model_name', 'token_name', 'group'].includes(item.value)
  ),
  ...TROUBLESHOOTING_FILTERS,
]
const MODE_LABELS = {
  detail: 'Detail',
  summary: 'Summary',
  both: 'Detail + summary',
}

export function EmployeeTemplateManager() {
  const { t } = useTranslation()
  const client = useQueryClient()
  const [open, setOpen] = useState(false)
  const [search, setSearch] = useState('')
  const [editing, setEditing] = useState<EmployeeExportTemplate>()
  const [deleting, setDeleting] = useState<EmployeeExportTemplate>()
  const templates = useQuery({
    queryKey: ['employee-export-templates'],
    queryFn: getEmployeeTemplates,
    enabled: open,
  })
  const catalog = useQuery({
    queryKey: ['employee-export-columns'],
    queryFn: getEmployeeExportColumns,
    enabled: open,
  })
  const matches = (tpl: EmployeeExportTemplate) =>
    employeeTemplateName(tpl, t).toLowerCase().includes(search.toLowerCase())
  const builtin = templates.data?.builtin.filter(matches) ?? []
  const custom = templates.data?.custom.filter(matches) ?? []
  const remove = useMutation({
    mutationFn: () =>
      exportRequest('delete', `${EMPLOYEE_TEMPLATE_BASE}/${deleting?.id}`),
    onSuccess: () => {
      setDeleting(undefined)
      void client.invalidateQueries({ queryKey: ['employee-export-templates'] })
    },
    // The HTTP client has already shown the error; the list may be stale.
    onError: () =>
      void client.invalidateQueries({
        queryKey: ['employee-export-templates'],
      }),
  })
  return (
    <>
      <Button variant='outline' onClick={() => setOpen(true)}>
        <FileText />
        {t('Employee export templates')}
      </Button>
      <Dialog open={open && !editing && !deleting} onOpenChange={setOpen}>
        <DialogContent className='max-h-[90vh] overflow-y-auto sm:max-w-3xl'>
          <DialogHeader>
            <DialogTitle>{t('Employee export templates')}</DialogTitle>
            <DialogDescription>
              {t(
                'When employee customer exports are on, every employee can use the built-in templates and all enabled custom templates.'
              )}
            </DialogDescription>
          </DialogHeader>
          <div className='flex gap-2'>
            <Input
              value={search}
              onChange={(event) => setSearch(event.target.value)}
              placeholder={t('Search')}
            />
            <Button
              disabled={!catalog.data}
              onClick={() =>
                setEditing({
                  id: 0,
                  key: '',
                  builtin: false,
                  name: '',
                  enabled: true,
                  version: 0,
                  format: 'xlsx',
                  mode: 'detail',
                  columns: catalog.data?.default_columns ?? [],
                  summary_dims: ['date'],
                  options: {
                    csv_bom: true,
                    header: true,
                    timezone: Intl.DateTimeFormat().resolvedOptions().timeZone,
                  },
                  allowed_filters: ['model_name', 'token_name', 'group'],
                })
              }
            >
              <Plus />
              {t('Add')}
            </Button>
          </div>
          {templates.isLoading && <p>{t('Loading...')}</p>}
          {(templates.isError || catalog.isError) && (
            <Button
              variant='outline'
              onClick={() => {
                void templates.refetch()
                void catalog.refetch()
              }}
            >
              {t('Retry')}
            </Button>
          )}
          {templates.data && (
            <section className='space-y-2'>
              <h3 className='text-sm font-medium'>{t('Built-in templates')}</h3>
              {builtin.map((tpl) => (
                <div
                  key={tpl.key}
                  className='flex items-center justify-between gap-2 rounded-md border p-3'
                >
                  <div className='min-w-0 flex-1'>
                    <p
                      className='truncate font-medium'
                      title={employeeTemplateName(tpl, t)}
                    >
                      {employeeTemplateName(tpl, t)}
                    </p>
                    <div className='mt-1 flex flex-wrap items-center gap-1.5'>
                      <Badge variant='secondary'>{t('Built-in')}</Badge>
                      <span className='text-muted-foreground text-xs'>
                        {tpl.format === 'xlsx' ? 'Excel' : 'CSV'} ·{' '}
                        {t(MODE_LABELS[tpl.mode])}
                      </span>
                    </div>
                  </div>
                </div>
              ))}
            </section>
          )}
          {templates.data && (
            <section className='space-y-2'>
              <h3 className='text-sm font-medium'>{t('Custom templates')}</h3>
              {custom.map((tpl) => (
                <div
                  key={tpl.id}
                  className='flex items-center justify-between gap-2 rounded-md border p-3'
                >
                  <div className='min-w-0 flex-1'>
                    <p className='truncate font-medium' title={tpl.name}>
                      {tpl.name}
                    </p>
                    <div className='mt-1 flex flex-wrap items-center gap-1.5'>
                      <Badge variant={tpl.enabled ? 'secondary' : 'outline'}>
                        {tpl.enabled ? t('Enabled') : t('Disabled')}
                      </Badge>
                      <span className='text-muted-foreground text-xs'>
                        {tpl.format === 'xlsx' ? 'Excel' : 'CSV'} ·{' '}
                        {t(MODE_LABELS[tpl.mode])}
                      </span>
                    </div>
                  </div>
                  <div className='flex shrink-0 gap-2'>
                    <Button
                      variant='outline'
                      size='sm'
                      disabled={!catalog.data}
                      onClick={() => setEditing(tpl)}
                    >
                      {t('Edit')}
                    </Button>
                    <Button
                      variant='outline'
                      size='sm'
                      onClick={() => setDeleting(tpl)}
                    >
                      {t('Delete')}
                    </Button>
                  </div>
                </div>
              ))}
              {templates.data.custom.length === 0 && (
                <Empty>
                  <EmptyHeader>
                    <EmptyTitle>{t('No custom templates yet')}</EmptyTitle>
                    <EmptyDescription>
                      {t(
                        'Add a template when employees need fields beyond the built-in reconciliation templates.'
                      )}
                    </EmptyDescription>
                  </EmptyHeader>
                </Empty>
              )}
              {templates.data.custom.length > 0 && custom.length === 0 && (
                <p className='text-muted-foreground py-6 text-center text-sm'>
                  {t('No results found')}
                </p>
              )}
            </section>
          )}
        </DialogContent>
      </Dialog>
      {editing && catalog.data && (
        <EmployeeTemplateEditor
          key={`${editing.id}:${editing.version}`}
          template={editing}
          columns={catalog.data.columns}
          maxColumns={catalog.data.max_columns}
          onClose={() => setEditing(undefined)}
        />
      )}
      <Dialog
        open={Boolean(deleting)}
        onOpenChange={(value) => {
          if (!value) setDeleting(undefined)
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t('Delete template')}</DialogTitle>
          </DialogHeader>
          <p className='truncate font-medium' title={deleting?.name}>
            {deleting?.name}
          </p>
          <p className='text-muted-foreground text-sm'>
            {t(
              'Employees can no longer use this template. Unfinished exports and download links that use it will stop working.'
            )}
          </p>
          <DialogFooter>
            <Button variant='outline' onClick={() => setDeleting(undefined)}>
              {t('Cancel')}
            </Button>
            <Button
              variant='destructive'
              disabled={remove.isPending}
              onClick={() => remove.mutate()}
            >
              {t('Delete')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  )
}

function EmployeeTemplateEditor(props: {
  template: EmployeeExportTemplate
  columns: EmployeeExportCatalog['columns']
  maxColumns: number
  onClose: () => void
}) {
  const { t } = useTranslation()
  const client = useQueryClient()
  const id = useId()
  const [draft, setDraft] = useState(props.template)
  const save = useMutation({
    mutationFn: () =>
      exportRequest(
        draft.id ? 'put' : 'post',
        draft.id
          ? `${EMPLOYEE_TEMPLATE_BASE}/${draft.id}`
          : EMPLOYEE_TEMPLATE_BASE,
        draft
      ),
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: ['employee-export-templates'] })
      toast.success(t('Template saved'))
      props.onClose()
    },
    // The HTTP client has already shown the error (a version conflict asks to reopen the latest version).
    onError: () =>
      void client.invalidateQueries({
        queryKey: ['employee-export-templates'],
      }),
  })
  return (
    <Sheet
      open
      onOpenChange={(value) => {
        if (!value) props.onClose()
      }}
    >
      <SheetContent
        side='right'
        className='flex w-full flex-col gap-0 sm:max-w-3xl'
      >
        <SheetHeader>
          <SheetTitle>
            {draft.id ? t('Edit export template') : t('New export template')}
          </SheetTitle>
          <SheetDescription>
            {t(
              'Choose the fields and output employees can use when exporting their customers.'
            )}
          </SheetDescription>
        </SheetHeader>
        <div className='min-h-0 flex-1 overflow-y-auto px-4 py-2'>
          <fieldset disabled={save.isPending} className='space-y-4'>
            <div className='space-y-1'>
              <Label htmlFor={`${id}-name`}>{t('Template name')}</Label>
              <Input
                id={`${id}-name`}
                maxLength={64}
                value={draft.name}
                onChange={(event) =>
                  setDraft({ ...draft, name: event.target.value })
                }
              />
            </div>
            <Label className='flex items-center gap-2'>
              <Switch
                checked={draft.enabled}
                onCheckedChange={(enabled) => setDraft({ ...draft, enabled })}
              />
              {t('Enabled')}
            </Label>
            <div className='grid gap-3 sm:grid-cols-2'>
              <div>
                <Label htmlFor={`${id}-format`}>{t('Format')}</Label>
                <Combobox
                  id={`${id}-format`}
                  options={[
                    { value: 'xlsx', label: 'Excel (.xlsx)' },
                    { value: 'csv_gz', label: 'CSV (.csv.gz)' },
                  ]}
                  value={draft.format}
                  onValueChange={(format) => {
                    if (format) {
                      setDraft({ ...draft, format: format as ExportFormat })
                    }
                  }}
                />
              </div>
              <div>
                <Label htmlFor={`${id}-mode`}>{t('Output')}</Label>
                <Combobox
                  id={`${id}-mode`}
                  options={[
                    { value: 'detail', label: t('Detail') },
                    { value: 'summary', label: t('Summary') },
                    { value: 'both', label: t('Detail + summary') },
                  ]}
                  value={draft.mode}
                  onValueChange={(mode) => {
                    if (mode) setDraft({ ...draft, mode: mode as ExportMode })
                  }}
                />
              </div>
            </div>
            {draft.mode !== 'detail' && (
              <section className='space-y-2'>
                <h3 className='text-sm font-medium'>{t('Group by')}</h3>
                <div className='flex flex-wrap gap-3'>
                  {DIMENSIONS.map((dim) => (
                    <Label key={dim.value} className='flex items-center gap-2'>
                      <Checkbox
                        checked={draft.summary_dims?.includes(dim.value)}
                        onCheckedChange={(checked) =>
                          setDraft({
                            ...draft,
                            summary_dims: checked
                              ? [...draft.summary_dims, dim.value]
                              : draft.summary_dims.filter(
                                  (value) => value !== dim.value
                                ),
                          })
                        }
                      />
                      {t(dim.label)}
                    </Label>
                  ))}
                </div>
                {/* Same switch as the Quota export column: the summary follows the template. */}
                <Label className='flex items-center gap-2'>
                  <Checkbox
                    checked={draft.columns.includes('quota')}
                    onCheckedChange={(checked) =>
                      setDraft({
                        ...draft,
                        columns: checked
                          ? [...draft.columns, 'quota']
                          : draft.columns.filter((key) => key !== 'quota'),
                      })
                    }
                  />
                  {t('Include quota in summary')}
                </Label>
                <p className='text-muted-foreground text-xs'>
                  {draft.columns.includes('quota')
                    ? t(
                        'Summary includes request counts, tokens, quota, and cost.'
                      )
                    : t('Summary includes request counts, tokens, and cost.')}
                </p>
              </section>
            )}
            <Label>{t('Allowed export filters')}</Label>
            <div className='flex flex-wrap gap-3'>
              {FILTERS.map((filter) => (
                <Label key={filter.value} className='flex items-center gap-2'>
                  <Checkbox
                    checked={draft.allowed_filters?.includes(filter.value)}
                    onCheckedChange={(checked) =>
                      setDraft({
                        ...draft,
                        allowed_filters: checked
                          ? [...draft.allowed_filters, filter.value]
                          : draft.allowed_filters.filter(
                              (value) => value !== filter.value
                            ),
                      })
                    }
                  />
                  {t(filter.label)}
                </Label>
              ))}
            </div>
            <div className='space-y-1'>
              <Label htmlFor={`${id}-timezone`}>{t('Timezone')}</Label>
              <Input
                id={`${id}-timezone`}
                value={draft.options.timezone}
                onChange={(event) =>
                  setDraft({
                    ...draft,
                    options: { ...draft.options, timezone: event.target.value },
                  })
                }
              />
            </div>
            <div className='flex gap-4'>
              {(['header', 'csv_bom'] as const)
                .filter(
                  (field) => field !== 'csv_bom' || draft.format === 'csv_gz'
                )
                .map((field) => (
                  <Label key={field} className='flex items-center gap-2'>
                    <Checkbox
                      checked={draft.options[field]}
                      onCheckedChange={(checked) =>
                        setDraft({
                          ...draft,
                          options: {
                            ...draft.options,
                            [field]: Boolean(checked),
                          },
                        })
                      }
                    />
                    {field === 'header' ? t('Include headers') : t('CSV BOM')}
                  </Label>
                ))}
            </div>
            {draft.mode !== 'summary' && (
              <section className='space-y-2'>
                <h3 className='text-sm font-medium'>{t('Export columns')}</h3>
                <ColumnPicker
                  columns={props.columns}
                  selected={draft.columns}
                  maxColumns={props.maxColumns}
                  onChange={(columns) => setDraft({ ...draft, columns })}
                />
              </section>
            )}
          </fieldset>
        </div>
        <SheetFooter className='shrink-0 border-t'>
          <div className='flex justify-end gap-2'>
            <Button
              variant='outline'
              disabled={save.isPending}
              onClick={props.onClose}
            >
              {t('Cancel')}
            </Button>
            <Button
              disabled={
                save.isPending ||
                !draft.name.trim() ||
                !draft.columns.length ||
                (draft.mode !== 'detail' && !draft.summary_dims.length)
              }
              onClick={() => save.mutate()}
            >
              {save.isPending ? t('Saving...') : t('Save')}
            </Button>
          </div>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  )
}

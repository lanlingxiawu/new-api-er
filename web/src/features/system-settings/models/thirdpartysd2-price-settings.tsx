import { Code2, Copy, Eye, Plus, Trash2 } from 'lucide-react'
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
import { memo, useCallback, useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { Textarea } from '@/components/ui/textarea'

import { useSettingsSaveConfirmation } from '../components/settings-save-confirmation'
import { useUpdateOption } from '../hooks/use-update-option'

const OPTION_KEY = 'thirdpartysd2_pricing.matrix'

// A price entry omits a price the built-in matrix still supplies, and is null
// when the resolution is removed from the model. Both are meaningful to the
// server, so an empty input must never be sent as 0.
type ThirdPartySD2PriceEntry = {
  no_video?: number | null
  with_video?: number | null
}

type ThirdPartySD2PricingMatrix = Record<
  string,
  Record<string, ThirdPartySD2PriceEntry | null>
>

type ThirdPartySD2DefaultMatrix = Record<
  string,
  Record<string, { no_video: number; with_video: number }>
>

// Mirrors the built-in matrix the server merges overrides onto.
const DEFAULT_MATRIX: ThirdPartySD2DefaultMatrix = {
  'dreamina-seedance-2-0-260128': {
    '480p': { no_video: 7.0, with_video: 4.3 },
    '720p': { no_video: 7.0, with_video: 4.3 },
    '1080p': { no_video: 7.7, with_video: 4.7 },
    '4k': { no_video: 4.0, with_video: 2.4 },
  },
  'dreamina-seedance-2-0-fast-260128': {
    '480p': { no_video: 5.6, with_video: 3.3 },
    '720p': { no_video: 5.6, with_video: 3.3 },
  },
}

const SUPPORTED_RESOLUTIONS = ['480p', '720p', '1080p', '4k'] as const

type ThirdPartySD2PriceRow = {
  id: number
  model: string
  resolution: string
  noVideo: number | null
  withVideo: number | null
}

function classifyResolution(height: number): string {
  if (height >= 2160) return '4k'
  if (height >= 1080) return '1080p'
  if (height >= 720) return '720p'
  if (height > 0) return '480p'
  return ''
}

function parseWholeNumber(value: string): number | null {
  if (!/^[+-]?\d+$/.test(value)) return null
  return Number(value)
}

// Mirrors the server's resolution normalization so the table shows the same
// tiers the server prices.
function normalizeResolution(raw: string): string {
  const value = raw.trim().toLowerCase().replaceAll(' ', '')
  if ((SUPPORTED_RESOLUTIONS as readonly string[]).includes(value)) return value
  if (value === '2160p') return '4k'
  if (value.endsWith('p')) {
    const height = parseWholeNumber(value.slice(0, -1))
    if (height !== null) return classifyResolution(height)
  }
  const separator = value.indexOf('x')
  if (separator < 0) return ''
  const width = parseWholeNumber(value.slice(0, separator))
  const height = parseWholeNumber(value.slice(separator + 1))
  if (width === null || height === null || width <= 0 || height <= 0) return ''
  return classifyResolution(Math.min(width, height))
}

function resolutionRank(resolution: string): number {
  const rank = (SUPPORTED_RESOLUTIONS as readonly string[]).indexOf(resolution)
  return rank < 0 ? SUPPORTED_RESOLUTIONS.length : rank
}

function pickPrice(
  value: number | null | undefined,
  fallback: number | null
): number | null {
  if (typeof value === 'number' && Number.isFinite(value)) return value
  return fallback
}

// matrixToRows applies the saved overrides to the built-in matrix exactly the
// way the server does: an absent price keeps the built-in one, and a null
// resolution removes the row.
function matrixToRows(
  matrix: ThirdPartySD2PricingMatrix
): ThirdPartySD2PriceRow[] {
  const merged = new Map<
    string,
    Map<string, { noVideo: number | null; withVideo: number | null }>
  >()
  for (const [model, resolutions] of Object.entries(DEFAULT_MATRIX)) {
    const bucket = new Map<
      string,
      { noVideo: number | null; withVideo: number | null }
    >()
    for (const [resolution, pricing] of Object.entries(resolutions)) {
      bucket.set(resolution, {
        noVideo: pricing.no_video,
        withVideo: pricing.with_video,
      })
    }
    merged.set(model, bucket)
  }

  for (const [rawModel, resolutions] of Object.entries(matrix ?? {})) {
    const model = rawModel.trim()
    if (!model) continue
    let bucket = merged.get(model)
    if (!bucket) {
      bucket = new Map()
      merged.set(model, bucket)
    }
    for (const [rawResolution, pricing] of Object.entries(resolutions ?? {})) {
      // an unrecognized key is kept as typed so it can be corrected
      const resolution =
        normalizeResolution(rawResolution) || rawResolution.trim()
      if (!resolution) continue
      if (pricing === null) {
        bucket.delete(resolution)
        continue
      }
      const base = bucket.get(resolution)
      bucket.set(resolution, {
        noVideo: pickPrice(pricing?.no_video, base?.noVideo ?? null),
        withVideo: pickPrice(pricing?.with_video, base?.withVideo ?? null),
      })
    }
  }

  let nextId = 1
  const rows: ThirdPartySD2PriceRow[] = []
  for (const [model, bucket] of merged) {
    const resolutions = [...bucket.entries()].sort(
      ([a], [b]) => resolutionRank(a) - resolutionRank(b)
    )
    for (const [resolution, pricing] of resolutions) {
      rows.push({
        id: nextId++,
        model,
        resolution,
        noVideo: pricing.noVideo,
        withVideo: pricing.withVideo,
      })
    }
  }
  return rows
}

// rowsToMatrix writes every row with both prices, and writes null for a
// built-in row the administrator removed — without that tombstone the server
// would merge the built-in price back in and the row would reappear.
function rowsToMatrix(
  rows: ThirdPartySD2PriceRow[]
): ThirdPartySD2PricingMatrix {
  const matrix: ThirdPartySD2PricingMatrix = {}
  const kept = new Set<string>()
  for (const row of rows) {
    const model = row.model.trim()
    const resolution = row.resolution.trim()
    if (!model || !resolution) continue
    if (row.noVideo === null || row.withVideo === null) continue
    if (!matrix[model]) {
      matrix[model] = {}
    }
    matrix[model][resolution] = {
      no_video: row.noVideo,
      with_video: row.withVideo,
    }
    kept.add(`${model}\u0000${normalizeResolution(resolution) || resolution}`)
  }
  for (const [model, resolutions] of Object.entries(DEFAULT_MATRIX)) {
    for (const resolution of Object.keys(resolutions)) {
      if (kept.has(`${model}\u0000${resolution}`)) continue
      if (!matrix[model]) {
        matrix[model] = {}
      }
      matrix[model][resolution] = null
    }
  }
  return matrix
}

function findDuplicateModelResolution(
  rows: ThirdPartySD2PriceRow[]
): { model: string; resolution: string } | null {
  const seen = new Set<string>()
  for (const row of rows) {
    const model = row.model.trim()
    const resolution = row.resolution.trim()
    if (!model || !resolution) continue
    const key = `${model}\u0000${normalizeResolution(resolution) || resolution}`
    if (seen.has(key)) {
      return { model, resolution }
    }
    seen.add(key)
  }
  return null
}

function parseInitialMatrix(
  rawValue: string | undefined
): ThirdPartySD2PricingMatrix {
  if (!rawValue) return {}
  try {
    const parsed = JSON.parse(rawValue) as unknown
    if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
      return parsed as ThirdPartySD2PricingMatrix
    }
  } catch {
    // fall through to the built-in matrix
  }
  return {}
}

type ThirdPartySD2PriceSettingsProps = {
  defaultValue: string
}

export const ThirdPartySD2PriceSettings = memo(
  function ThirdPartySD2PriceSettings({
    defaultValue,
  }: ThirdPartySD2PriceSettingsProps) {
    const { t } = useTranslation()
    const updateOption = useUpdateOption()
    const requestSaveConfirmation = useSettingsSaveConfirmation()
    const [editMode, setEditMode] = useState<'visual' | 'json'>('visual')
    const [rows, setRows] = useState<ThirdPartySD2PriceRow[]>([])
    const [jsonText, setJsonText] = useState('')
    const [jsonError, setJsonError] = useState('')
    const [nextRowId, setNextRowId] = useState(1)

    useEffect(() => {
      const initialRows = matrixToRows(parseInitialMatrix(defaultValue))
      setRows(initialRows)
      setJsonText(JSON.stringify(rowsToMatrix(initialRows), null, 2))
      setJsonError('')
      setNextRowId(initialRows.length + 1)
    }, [defaultValue])

    const currentMatrix = useMemo(() => rowsToMatrix(rows), [rows])
    const savedMatrix = useMemo(
      () =>
        JSON.stringify(
          rowsToMatrix(matrixToRows(parseInitialMatrix(defaultValue)))
        ),
      [defaultValue]
    )

    const syncFromRows = useCallback((nextRows: ThirdPartySD2PriceRow[]) => {
      setRows(nextRows)
      setJsonText(JSON.stringify(rowsToMatrix(nextRows), null, 2))
      setJsonError('')
    }, [])

    const handleJsonChange = useCallback(
      (text: string) => {
        setJsonText(text)
        try {
          const parsed = JSON.parse(text) as unknown
          if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
            setJsonError(
              t(
                'JSON must be an object of model -> resolution -> price entries'
              )
            )
            return
          }
          const nextRows = matrixToRows(parsed as ThirdPartySD2PricingMatrix)
          setRows(nextRows)
          setNextRowId(nextRows.length + 1)
          setJsonError('')
        } catch (error) {
          setJsonError(
            error instanceof Error ? error.message : t('Invalid JSON')
          )
        }
      },
      [t]
    )

    const updateRow = useCallback(
      (
        id: number,
        field: 'model' | 'resolution' | 'noVideo' | 'withVideo',
        value: string | number | null
      ) => {
        syncFromRows(
          rows.map((row) => (row.id === id ? { ...row, [field]: value } : row))
        )
      },
      [rows, syncFromRows]
    )

    const addRow = useCallback(() => {
      const newRow: ThirdPartySD2PriceRow = {
        id: nextRowId,
        model: '',
        resolution: '720p',
        noVideo: null,
        withVideo: null,
      }
      setNextRowId((prev) => prev + 1)
      syncFromRows([...rows, newRow])
    }, [nextRowId, rows, syncFromRows])

    const removeRow = useCallback(
      (id: number) => {
        syncFromRows(rows.filter((row) => row.id !== id))
      },
      [rows, syncFromRows]
    )

    const resetToDefault = useCallback(() => {
      const initialRows = matrixToRows({})
      setRows(initialRows)
      setJsonText(JSON.stringify(rowsToMatrix(initialRows), null, 2))
      setJsonError('')
      setNextRowId(initialRows.length + 1)
    }, [])

    const handleCopyJson = useCallback(async () => {
      try {
        await navigator.clipboard.writeText(jsonText)
        toast.success(t('Copied to clipboard'))
      } catch {
        toast.error(t('Failed to copy'))
      }
    }, [jsonText, t])

    const findRowIssue = useCallback(
      (candidates: ThirdPartySD2PriceRow[]) => {
        for (const row of candidates) {
          const model = row.model.trim()
          const resolution = row.resolution.trim()
          if (!model || !resolution) {
            return t(
              'Enter a model and a resolution for every price row, or remove the row.'
            )
          }
          if (!normalizeResolution(resolution)) {
            return t(
              'Resolution "{{resolution}}" is not supported. Use 480p, 720p, 1080p or 4k.',
              { resolution }
            )
          }
          if (row.noVideo === null || row.withVideo === null) {
            return t(
              'Enter both prices for model {{model}} at {{resolution}}, or remove the row.',
              { model, resolution }
            )
          }
          if (row.noVideo < 0 || row.withVideo < 0) {
            return t(
              'Prices for model {{model}} at {{resolution}} cannot be negative.',
              { model, resolution }
            )
          }
        }
        return ''
      },
      [t]
    )

    const handleSave = useCallback(async () => {
      if (editMode === 'json' && jsonError) {
        toast.error(t('Please fix JSON errors before saving'))
        return
      }
      const rowIssue = findRowIssue(rows)
      if (rowIssue) {
        toast.error(rowIssue)
        return
      }
      const duplicate = findDuplicateModelResolution(rows)
      if (duplicate) {
        toast.error(
          t(
            'A duplicate configuration already exists for model {{model}} and resolution {{resolution}}. Saving is blocked.',
            {
              model: duplicate.model,
              resolution: duplicate.resolution,
            }
          )
        )
        return
      }
      const serializedMatrix = JSON.stringify(currentMatrix)
      if (serializedMatrix === savedMatrix) {
        toast.info(t('No changes to save'))
        return
      }
      await requestSaveConfirmation(async () => {
        await updateOption.mutateAsync({
          key: OPTION_KEY,
          value: serializedMatrix,
        })
      })
    }, [
      currentMatrix,
      editMode,
      findRowIssue,
      jsonError,
      requestSaveConfirmation,
      rows,
      savedMatrix,
      t,
      updateOption,
    ])

    const toggleEditMode = useCallback(() => {
      setEditMode((prev) => (prev === 'visual' ? 'json' : 'visual'))
    }, [])

    return (
      <div className='space-y-4'>
        <Alert>
          <AlertDescription className='space-y-1 text-sm'>
            <div>
              {t(
                'Configure token prices ($/1M tokens) for third-party SD2 models by resolution and whether the request includes video input.'
              )}
            </div>
            <div>
              {t(
                'These prices override the generic model ratio only for the third-party SD2 task channel.'
              )}
            </div>
            <div>
              {t(
                'A model accepts exactly the resolutions listed here: deleting a row makes that resolution unavailable for the model.'
              )}
            </div>
          </AlertDescription>
        </Alert>

        <div className='flex flex-wrap items-center justify-between gap-2'>
          <div className='flex flex-wrap items-center gap-2'>
            {editMode === 'visual' ? (
              <>
                <Button variant='outline' size='sm' onClick={addRow}>
                  <Plus className='mr-2 h-4 w-4' />
                  {t('Add')}
                </Button>
                <Button variant='ghost' size='sm' onClick={resetToDefault}>
                  {t('Restore defaults')}
                </Button>
              </>
            ) : (
              <>
                <Button variant='ghost' size='sm' onClick={handleCopyJson}>
                  <Copy className='mr-2 h-4 w-4' />
                  {t('Copy')}
                </Button>
                <Button variant='ghost' size='sm' onClick={resetToDefault}>
                  {t('Restore defaults')}
                </Button>
              </>
            )}
          </div>
          <Button variant='outline' size='sm' onClick={toggleEditMode}>
            {editMode === 'visual' ? (
              <>
                <Code2 className='mr-2 h-4 w-4' />
                {t('Switch to JSON')}
              </>
            ) : (
              <>
                <Eye className='mr-2 h-4 w-4' />
                {t('Switch to Visual')}
              </>
            )}
          </Button>
        </div>

        {editMode === 'visual' ? (
          <div className='overflow-hidden rounded-md border'>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t('Model')}</TableHead>
                  <TableHead>{t('Resolution')}</TableHead>
                  <TableHead className='w-[220px]'>
                    {t('Price without video input ($/1M tokens)')}
                  </TableHead>
                  <TableHead className='w-[220px]'>
                    {t('Price with video input ($/1M tokens)')}
                  </TableHead>
                  <TableHead className='w-[80px] text-right'>
                    {t('Actions')}
                  </TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {rows.length === 0 ? (
                  <TableRow>
                    <TableCell
                      colSpan={5}
                      className='text-muted-foreground py-8 text-center'
                    >
                      {t('No third-party SD2 prices configured')}
                    </TableCell>
                  </TableRow>
                ) : (
                  rows.map((row) => (
                    <TableRow key={row.id}>
                      <TableCell>
                        <Input
                          value={row.model}
                          placeholder='dreamina-seedance-2-0-260128'
                          onChange={(e) =>
                            updateRow(row.id, 'model', e.target.value)
                          }
                        />
                      </TableCell>
                      <TableCell>
                        <Input
                          value={row.resolution}
                          placeholder='720p'
                          onChange={(e) =>
                            updateRow(row.id, 'resolution', e.target.value)
                          }
                        />
                      </TableCell>
                      <TableCell>
                        <Input
                          type='number'
                          min={0}
                          step={0.1}
                          value={row.noVideo ?? ''}
                          onChange={(e) =>
                            updateRow(
                              row.id,
                              'noVideo',
                              e.target.value === ''
                                ? null
                                : Number(e.target.value)
                            )
                          }
                        />
                      </TableCell>
                      <TableCell>
                        <Input
                          type='number'
                          min={0}
                          step={0.1}
                          value={row.withVideo ?? ''}
                          onChange={(e) =>
                            updateRow(
                              row.id,
                              'withVideo',
                              e.target.value === ''
                                ? null
                                : Number(e.target.value)
                            )
                          }
                        />
                      </TableCell>
                      <TableCell className='text-right'>
                        <Button
                          variant='ghost'
                          size='icon'
                          onClick={() => removeRow(row.id)}
                          aria-label={t('Delete')}
                        >
                          <Trash2 className='text-destructive h-4 w-4' />
                        </Button>
                      </TableCell>
                    </TableRow>
                  ))
                )}
              </TableBody>
            </Table>
          </div>
        ) : (
          <div className='space-y-2'>
            <Textarea
              value={jsonText}
              onChange={(e) => handleJsonChange(e.target.value)}
              className='font-mono text-sm'
              rows={14}
              spellCheck={false}
            />
            {jsonError && (
              <p className='text-destructive text-sm'>{jsonError}</p>
            )}
          </div>
        )}

        <div className='flex justify-end'>
          <Button
            onClick={handleSave}
            disabled={
              updateOption.isPending || (editMode === 'json' && !!jsonError)
            }
          >
            {t('Save third-party SD2 prices')}
          </Button>
        </div>
      </div>
    )
  }
)

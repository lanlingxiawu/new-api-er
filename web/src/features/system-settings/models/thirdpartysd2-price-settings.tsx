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

type ThirdPartySD2PriceEntry = {
  no_video: number
  with_video: number
}

type ThirdPartySD2PricingMatrix = Record<
  string,
  Record<string, ThirdPartySD2PriceEntry>
>

const DEFAULT_MATRIX: ThirdPartySD2PricingMatrix = {
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

type ThirdPartySD2PriceRow = {
  id: number
  model: string
  resolution: string
  noVideo: number
  withVideo: number
}

function matrixToRows(
  matrix: ThirdPartySD2PricingMatrix
): ThirdPartySD2PriceRow[] {
  let nextId = 1
  const rows: ThirdPartySD2PriceRow[] = []
  for (const [model, resolutions] of Object.entries(matrix)) {
    for (const [resolution, pricing] of Object.entries(resolutions ?? {})) {
      rows.push({
        id: nextId++,
        model,
        resolution,
        noVideo: Number(pricing?.no_video) || 0,
        withVideo: Number(pricing?.with_video) || 0,
      })
    }
  }
  return rows
}

function rowsToMatrix(
  rows: ThirdPartySD2PriceRow[]
): ThirdPartySD2PricingMatrix {
  const matrix: ThirdPartySD2PricingMatrix = {}
  for (const row of rows) {
    const model = row.model.trim()
    const resolution = row.resolution.trim()
    if (!model || !resolution) continue
    if (!matrix[model]) {
      matrix[model] = {}
    }
    matrix[model][resolution] = {
      no_video: Number(row.noVideo) || 0,
      with_video: Number(row.withVideo) || 0,
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
    const key = `${model}\u0000${resolution}`
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
  if (!rawValue) return { ...DEFAULT_MATRIX }
  try {
    const parsed = JSON.parse(rawValue) as unknown
    if (
      parsed &&
      typeof parsed === 'object' &&
      !Array.isArray(parsed) &&
      Object.keys(parsed as object).length > 0
    ) {
      return parsed as ThirdPartySD2PricingMatrix
    }
  } catch {
    // fall through to defaults
  }
  return { ...DEFAULT_MATRIX }
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
      const matrix = parseInitialMatrix(defaultValue)
      const initialRows = matrixToRows(matrix)
      setRows(initialRows)
      setJsonText(JSON.stringify(matrix, null, 2))
      setJsonError('')
      setNextRowId(initialRows.length + 1)
    }, [defaultValue])

    const currentMatrix = useMemo(() => rowsToMatrix(rows), [rows])
    const savedMatrix = useMemo(
      () => JSON.stringify(parseInitialMatrix(defaultValue)),
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
        value: string | number
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
        noVideo: 0,
        withVideo: 0,
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
      const initialRows = matrixToRows(DEFAULT_MATRIX)
      setRows(initialRows)
      setJsonText(JSON.stringify(DEFAULT_MATRIX, null, 2))
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

    const handleSave = useCallback(async () => {
      if (editMode === 'json' && jsonError) {
        toast.error(t('Please fix JSON errors before saving'))
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
                          value={row.noVideo}
                          onChange={(e) =>
                            updateRow(
                              row.id,
                              'noVideo',
                              Number(e.target.value) || 0
                            )
                          }
                        />
                      </TableCell>
                      <TableCell>
                        <Input
                          type='number'
                          min={0}
                          step={0.1}
                          value={row.withVideo}
                          onChange={(e) =>
                            updateRow(
                              row.id,
                              'withVideo',
                              Number(e.target.value) || 0
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

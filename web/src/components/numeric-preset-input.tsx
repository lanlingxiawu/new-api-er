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
import * as React from 'react'
import { useTranslation } from 'react-i18next'

import { ComboboxInput } from '@/components/ui/combobox-input'
import { cn } from '@/lib/utils'

/**
 * A number with a special meaning (0 = inherit, -1 = no retry, …).
 * `label` is an i18n key; the component translates it.
 */
export type NumericPreset = { value: number; label: string }

export type NumericPresetInputProps = {
  value: number | string | null | undefined
  /**
   * Receives what the field shows on every edit: an accepted value (a preset or
   * a number in range), or — for rejected text — the out-of-range number, or NaN
   * when the text is not a complete number. The caller's own save validation
   * must reject those, so a half-typed or invalid entry can never be saved as a
   * different number.
   */
  onChange: (value: number) => void
  onBlur?: () => void
  presets: readonly NumericPreset[]
  /** Lower bound of the ordinary range (presets are accepted regardless). */
  min: number
  max?: number
  /** Accept decimals; otherwise only whole numbers can be typed. */
  decimals?: boolean
  placeholder?: string
  disabled?: boolean
  id?: string
  className?: string
  // Injected by the shared FormControl so labels and form errors stay linked.
  'aria-invalid'?: boolean | 'true' | 'false'
  'aria-describedby'?: string
  'data-form-root'?: string
}

type Draft = {
  /** What the admin typed. */
  text: string
  /** The value prop the draft belongs to; a different value (form reset) discards the draft. */
  base: string
  /** Set on blur when the text is not an accepted value. */
  invalid: boolean
}

/** Canonical text of a value; anything that is not a finite number (NaN, "NaN") is ''. */
function formatValue(value: NumericPresetInputProps['value']): string {
  if (typeof value === 'number') {
    return Number.isFinite(value) ? String(value) : ''
  }
  const trimmed = typeof value === 'string' ? value.trim() : ''
  return /^-?\d+(\.\d+)?$/.test(trimmed) ? trimmed : ''
}

/** Returns null for text that is not a complete number. "1." counts as 1. */
function parseNumber(text: string, decimals: boolean): number | null {
  const trimmed = text.trim()
  const pattern = decimals ? /^-?\d+(\.\d*)?$/ : /^-?\d+$/
  if (!pattern.test(trimmed)) return null
  const parsed = Number(trimmed)
  if (!Number.isFinite(parsed)) return null
  if (!decimals && !Number.isSafeInteger(parsed)) return null
  return parsed
}

function isAccepted(
  value: number | null,
  props: NumericPresetInputProps
): value is number {
  if (value === null) return false
  if (props.presets.some((preset) => preset.value === value)) return true
  return value >= props.min && (props.max === undefined || value <= props.max)
}

/** The value handed to the caller for text: the number it shows, or NaN. */
function valueOfText(text: string, decimals: boolean): number {
  return parseNumber(text, decimals) ?? Number.NaN
}

/**
 * Numeric input whose special values (0 = inherit, -1 = unlimited, …) are offered
 * as labelled options in a dropdown, while any number in `[min, max]` can still be
 * typed. Built on the shared `ComboboxInput`, so dropdown styling and keyboard
 * handling (arrows, Enter, Escape) match the other comboboxes.
 *
 * `ComboboxInput` runs in selector mode (`allowCustomValue={false}`): opening it
 * starts from an empty search so every preset is listed even when the current
 * value is an ordinary number. Typed text is read from the bubbling change event
 * and forwarded on every edit (see `onChange`), so the form state always matches
 * the field and the caller's existing validation blocks saving rejected text,
 * even when Save is clicked or Enter pressed straight after typing. The inline
 * error waits for blur so half-typed text ("-" on the way to "-1") is not
 * flagged mid-keystroke.
 */
export function NumericPresetInput(props: NumericPresetInputProps) {
  const { t } = useTranslation()
  const decimals = props.decimals === true
  const [draft, setDraft] = React.useState<Draft | null>(null)
  // Remounting is the only way to close ComboboxInput's dropdown from outside;
  // it does not close on Tab-out by itself.
  const [comboboxKey, setComboboxKey] = React.useState(0)
  // ComboboxInput's own search text: empty when the list opens, then what the
  // admin types. Presets are filtered on this, not on the field's value.
  const [search, setSearch] = React.useState('')
  const containerRef = React.useRef<HTMLDivElement>(null)
  const focusSnapshotRef = React.useRef<{
    draft: Draft | null
    value: string
  } | null>(null)
  const generatedId = React.useId()
  const inputId =
    props.id ?? `numeric-preset-${generatedId.replaceAll(':', '')}`
  const errorId = `${inputId}-error`

  const currentValue = formatValue(props.value)
  const activeDraft = draft && draft.base === currentValue ? draft : null
  const text = activeDraft ? activeDraft.text : currentValue
  const showError = activeDraft?.invalid === true

  const allowNegative =
    props.min < 0 || props.presets.some((preset) => preset.value < 0)
  const allowedChars = `0123456789${allowNegative ? '-' : ''}${decimals ? '.' : ''}`

  // While a number is typed, list only presets whose value starts with it:
  // matching label text would offer "Default 300 seconds (0)" for "300".
  const typed = search.trim()
  const typedNumber = typed !== '' && /^-?[\d.]*$/.test(typed)
  const options = props.presets
    .filter((preset) => !typedNumber || String(preset.value).startsWith(typed))
    .map((preset) => ({
      value: String(preset.value),
      label: t('{{label}} ({{value}})', {
        label: t(preset.label),
        value: preset.value,
      }),
    }))

  const rangeHint =
    props.max === undefined
      ? t('Or enter a number of at least {{min}}', { min: props.min })
      : t('Or enter a number from {{min}} to {{max}}', {
          min: props.min,
          max: props.max,
        })
  const errorMessage =
    props.max === undefined
      ? t(
          'Choose an option from the list or enter a number of at least {{min}}',
          {
            min: props.min,
          }
        )
      : t(
          'Choose an option from the list or enter a number from {{min}} to {{max}}',
          { min: props.min, max: props.max }
        )

  const typedValue = typedNumber ? parseNumber(typed, decimals) : null
  const emptyText =
    typedValue !== null && isAccepted(typedValue, props)
      ? t('Custom value: {{value}}', { value: typedValue })
      : 'No matching preset'

  // Inside a form that already reports this field's error, do not repeat it.
  const formReportsError =
    props['aria-invalid'] === true || props['aria-invalid'] === 'true'
  const showInlineError = showError && !formReportsError
  const describedBy =
    [props['aria-describedby'], showInlineError ? errorId : undefined]
      .filter(Boolean)
      .join(' ') || undefined
  const ariaInvalid = showError || formReportsError
  const formRoot = props['data-form-root']

  // ComboboxInput does not forward aria/inputmode/data props, so they are set on
  // its input directly. React never manages these attributes there, so they
  // stick. `data-form-root` lets the form focus this input on a failed submit.
  React.useEffect(() => {
    const input = containerRef.current?.querySelector('input')
    if (!input) return
    input.setAttribute('aria-invalid', String(ariaInvalid))
    if (describedBy) {
      input.setAttribute('aria-describedby', describedBy)
    } else {
      input.removeAttribute('aria-describedby')
    }
    // Mobile numeric keypads have no minus key, so fall back to text there.
    if (allowNegative) {
      input.removeAttribute('inputmode')
    } else {
      input.setAttribute('inputmode', decimals ? 'decimal' : 'numeric')
    }
    if (formRoot) {
      input.setAttribute('data-form-root', formRoot)
    } else {
      input.removeAttribute('data-form-root')
    }
  }, [ariaInvalid, describedBy, allowNegative, decimals, formRoot, comboboxKey])

  const commitText = (nextText: string) => {
    const next = valueOfText(nextText, decimals)
    setDraft({ text: nextText, base: formatValue(next), invalid: false })
    if (!Object.is(next, valueOfText(currentValue, decimals))) {
      props.onChange(next)
    }
  }

  const isDropdownOpen = () =>
    containerRef.current?.querySelector('[role="listbox"]') != null

  // After a preset is picked the input keeps focus and shows its label; select
  // the label so the next keystroke or paste replaces it instead of appending.
  const selectShownPresetLabel = (input: HTMLInputElement) => {
    if (options.some((option) => option.label === input.value)) input.select()
  }

  return (
    <div className={cn('space-y-1', props.className)}>
      <div className='flex items-center gap-2'>
        <div
          ref={containerRef}
          className='min-w-0 flex-1'
          onFocus={(event) => {
            if (containerRef.current?.contains(event.relatedTarget as Node)) {
              return
            }
            focusSnapshotRef.current = {
              draft: activeDraft,
              value: currentValue,
            }
          }}
          onBlur={(event) => {
            if (containerRef.current?.contains(event.relatedTarget as Node)) {
              return
            }
            if (activeDraft) {
              const parsed = parseNumber(activeDraft.text, decimals)
              setDraft(
                isAccepted(parsed, props)
                  ? null
                  : { ...activeDraft, invalid: true }
              )
            }
            if (isDropdownOpen()) {
              setComboboxKey((key) => key + 1)
              setSearch('')
            }
            props.onBlur?.()
          }}
          onChange={(event) => {
            const target = event.target as HTMLInputElement
            commitText(target.value)
          }}
          onKeyDownCapture={(event) => {
            const printable =
              event.key.length === 1 &&
              !event.ctrlKey &&
              !event.metaKey &&
              !event.altKey
            if (printable && !allowedChars.includes(event.key)) {
              event.preventDefault()
              return
            }
            if (
              printable ||
              event.key === 'Backspace' ||
              event.key === 'Delete'
            ) {
              selectShownPresetLabel(event.target as HTMLInputElement)
            }
          }}
          onKeyDown={(event) => {
            // Escape while the list is open restores the value from before the
            // field was focused (even one outside the current range), matching
            // ComboboxInput's free-text mode.
            if (event.key !== 'Escape') return
            const snapshot = focusSnapshotRef.current
            if (!snapshot || !isDropdownOpen()) return
            setDraft(snapshot.draft)
            if (snapshot.value !== currentValue) {
              props.onChange(valueOfText(snapshot.value, decimals))
            }
          }}
          onPasteCapture={(event) => {
            const pasted = event.clipboardData.getData('text').trim()
            if ([...pasted].some((char) => !allowedChars.includes(char))) {
              event.preventDefault()
              return
            }
            selectShownPresetLabel(event.target as HTMLInputElement)
          }}
        >
          <ComboboxInput
            key={comboboxKey}
            id={inputId}
            options={options}
            value={text}
            onValueChange={(selected) => {
              setDraft(null)
              const parsed = parseNumber(selected, decimals)
              if (parsed !== null && selected !== currentValue) {
                props.onChange(parsed)
              }
            }}
            placeholder={text || props.placeholder || ''}
            onSearchValueChange={setSearch}
            emptyText={emptyText}
            disabled={props.disabled}
            dropdownFooter={
              <div
                className='text-muted-foreground border-t px-2 py-1.5 text-xs'
                onMouseDown={(event) => event.preventDefault()}
              >
                {rangeHint}
              </div>
            }
          />
        </div>
      </div>
      {showInlineError && (
        <p id={errorId} className='text-destructive text-sm'>
          {errorMessage}
        </p>
      )}
    </div>
  )
}

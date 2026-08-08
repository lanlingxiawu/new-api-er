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
import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useRef,
  useState,
  type ReactNode,
} from 'react'
import { useTranslation } from 'react-i18next'

import { ConfirmDialog } from '@/components/confirm-dialog'

export type SettingsSaveConfirmationOptions = {
  title?: ReactNode
  description?: React.JSX.Element | string
  confirmText?: ReactNode
}

type SaveAction = () => void | Promise<void>
type RequestSaveConfirmation = (
  action: SaveAction,
  options?: SettingsSaveConfirmationOptions
) => Promise<boolean>

type PendingConfirmation = {
  action: SaveAction
  resolve: (confirmed: boolean) => void
  reject: (reason: unknown) => void
}

type DialogState = {
  open: boolean
  options: SettingsSaveConfirmationOptions
}

const SettingsSaveConfirmationContext =
  createContext<RequestSaveConfirmation | null>(null)

export function SettingsSaveConfirmationProvider({
  children,
}: {
  children: ReactNode
}) {
  const { t } = useTranslation()
  const pendingRef = useRef<PendingConfirmation | null>(null)
  const executingRef = useRef(false)
  const mountedRef = useRef(true)
  const [isExecuting, setIsExecuting] = useState(false)
  const [dialogState, setDialogState] = useState<DialogState>({
    open: false,
    options: {},
  })

  const requestSaveConfirmation = useCallback<RequestSaveConfirmation>(
    (action, options = {}) => {
      if (pendingRef.current) {
        return Promise.resolve(false)
      }

      return new Promise<boolean>((resolve, reject) => {
        pendingRef.current = { action, resolve, reject }
        setDialogState({ open: true, options })
      })
    },
    []
  )

  const cancelPendingConfirmation = useCallback(() => {
    if (executingRef.current) return

    const pending = pendingRef.current
    pendingRef.current = null
    setDialogState({ open: false, options: {} })
    pending?.resolve(false)
  }, [])

  const handleConfirm = useCallback(async () => {
    const pending = pendingRef.current
    if (!pending || executingRef.current) return

    executingRef.current = true
    setIsExecuting(true)

    try {
      await pending.action()
      pending.resolve(true)
    } catch (error) {
      pending.reject(error)
    } finally {
      if (pendingRef.current === pending) {
        pendingRef.current = null
      }
      executingRef.current = false
      if (mountedRef.current) {
        setIsExecuting(false)
        setDialogState({ open: false, options: {} })
      }
    }
  }, [])

  useEffect(() => {
    mountedRef.current = true

    return () => {
      mountedRef.current = false
      pendingRef.current?.resolve(false)
      pendingRef.current = null
    }
  }, [])

  const { options } = dialogState

  return (
    <SettingsSaveConfirmationContext.Provider value={requestSaveConfirmation}>
      {children}
      <ConfirmDialog
        open={dialogState.open}
        onOpenChange={(open) => {
          if (!open) cancelPendingConfirmation()
        }}
        title={options.title ?? t('Confirm Changes')}
        desc={
          options.description ??
          t('Are you sure you want to save these changes?')
        }
        confirmText={
          isExecuting
            ? t('Saving...')
            : (options.confirmText ?? t('Save Changes'))
        }
        isLoading={isExecuting}
        handleConfirm={handleConfirm}
      />
    </SettingsSaveConfirmationContext.Provider>
  )
}

// eslint-disable-next-line react-refresh/only-export-components
export function useSettingsSaveConfirmation() {
  const requestSaveConfirmation = useContext(SettingsSaveConfirmationContext)

  if (!requestSaveConfirmation) {
    throw new Error(
      'useSettingsSaveConfirmation must be used inside SettingsSaveConfirmationProvider'
    )
  }

  return requestSaveConfirmation
}

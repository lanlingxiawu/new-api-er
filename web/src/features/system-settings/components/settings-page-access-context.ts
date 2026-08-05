import { createContext, useContext } from 'react'

export type SettingsPageContextValue = {
  actionsContainer: HTMLDivElement | null
  titleStatusContainer: HTMLSpanElement | null
  suppressSectionHeader: boolean
  scope: string
  canEdit: boolean
}

export const SettingsPageContext = createContext<SettingsPageContextValue>({
  actionsContainer: null,
  titleStatusContainer: null,
  suppressSectionHeader: false,
  scope: '',
  canEdit: false,
})

export function useSettingsPageAccess() {
  const { scope, canEdit } = useContext(SettingsPageContext)
  return { scope, canEdit }
}

import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from '@testing-library/react'
import { toast } from 'sonner'
import { afterEach, expect, it, vi } from 'vitest'

import { SettingsForm } from './settings-form-layout'
import {
  SettingsPageFormActions,
  SettingsPageProvider,
} from './settings-page-context'
import { useSettingsSaveConfirmation } from './settings-save-confirmation'

function ConfirmedSettingsSave({ save }: { save: () => Promise<void> }) {
  const confirm = useSettingsSaveConfirmation()
  return (
    <button type='button' onClick={() => void confirm(save)}>
      Review changes
    </button>
  )
}

afterEach(() => {
  cleanup()
  document.body.replaceChildren()
  vi.restoreAllMocks()
})

it('does not submit a settings form after edit access is revoked', () => {
  const save = vi.fn()
  render(
    <SettingsPageProvider
      actionsContainer={null}
      scope='auth.oauth'
      canEdit={false}
    >
      <SettingsForm aria-label='settings' onSubmit={save}>
        <input aria-label='draft' />
      </SettingsForm>
    </SettingsPageProvider>
  )
  fireEvent.submit(screen.getByRole('form', { name: 'settings' }))
  expect(save).not.toHaveBeenCalled()
})

it.each([
  { scope: 'auth.oauth', canEdit: false },
  { scope: 'site.system-info', canEdit: true },
])(
  'cancels a pending save when settings access changes to $scope/$canEdit',
  async (next) => {
    const save = vi.fn().mockResolvedValue(undefined)
    const { rerender } = render(
      <SettingsPageProvider actionsContainer={null} scope='auth.oauth' canEdit>
        <ConfirmedSettingsSave save={save} />
      </SettingsPageProvider>
    )
    fireEvent.click(screen.getByRole('button', { name: 'Review changes' }))
    expect(await screen.findByRole('alertdialog')).toBeVisible()
    rerender(
      <SettingsPageProvider actionsContainer={null} {...next}>
        <ConfirmedSettingsSave save={save} />
      </SettingsPageProvider>
    )
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: 'Save Changes' })
    ).not.toBeInTheDocument()
    expect(save).not.toHaveBeenCalled()
  }
)

it.each(['page action', 'form submit'])(
  'handles a rejected %s without losing the draft and allows retry',
  async (entry) => {
    const error = vi.spyOn(toast, 'error').mockImplementation(() => 'error')
    const save = vi
      .fn()
      .mockRejectedValueOnce(new Error('Network Error'))
      .mockResolvedValue(undefined)
    const actions = document.createElement('div')
    document.body.append(actions)
    render(
      <SettingsPageProvider
        actionsContainer={actions}
        scope='site.system-info'
        canEdit
      >
        <SettingsPageFormActions onSave={save} />
        <SettingsForm
          aria-label='settings'
          onSubmit={async (event) => {
            event.preventDefault()
            await save()
          }}
        >
          <input aria-label='draft' defaultValue='original' />
        </SettingsForm>
      </SettingsPageProvider>
    )
    fireEvent.change(screen.getByLabelText('draft'), {
      target: { value: 'unsaved' },
    })
    for (let attempt = 1; attempt <= 2; attempt++) {
      if (entry === 'page action') {
        fireEvent.click(screen.getByRole('button', { name: 'Save Changes' }))
      } else {
        fireEvent.submit(screen.getByRole('form', { name: 'settings' }))
      }
      await waitFor(() => expect(save).toHaveBeenCalledTimes(attempt))
      await waitFor(() => expect(error).toHaveBeenCalledTimes(1))
      expect(screen.getByLabelText('draft')).toHaveValue('unsaved')
    }
  }
)

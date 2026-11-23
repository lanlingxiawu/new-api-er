import { QueryClientProvider } from '@tanstack/react-query'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { toast } from 'sonner'
import { afterEach, expect, it, vi } from 'vitest'

import { createAppQueryClient } from '@/lib/query-client'

import type { EmployeeExportTemplate } from '../employee-api'
import { EmployeeTemplateManager } from './employee-template-manager'

const request = vi.hoisted(() => vi.fn())
vi.mock('@/lib/api', () => ({ api: { request } }))

afterEach(() => {
  cleanup()
  request.mockReset()
  vi.restoreAllMocks()
})

it.each(['save', 'delete'] as const)(
  'shows a rejected template %s and keeps the dialog available for retry',
  async (action) => {
    const client = createAppQueryClient()
    const user = userEvent.setup()
    const notify = vi.spyOn(toast, 'error').mockReturnValue('template-error')
    const success = vi
      .spyOn(toast, 'success')
      .mockReturnValue('template-success')
    const template: EmployeeExportTemplate = {
      id: 12,
      key: '12',
      builtin: false,
      name: 'Customer daily summary',
      enabled: true,
      version: 1,
      columns: ['model_name', 'quota'],
      format: 'csv_gz',
      mode: 'both',
      summary_dims: ['date', 'model_name', 'token_name', 'group'],
      options: { csv_bom: true, header: true, timezone: 'Asia/Shanghai' },
      allowed_filters: ['model_name'],
    }
    const message =
      action === 'save'
        ? 'The template has changed. Reopen it before saving.'
        : 'Access denied. Please check your template permissions.'
    let rejected = true
    request.mockImplementation(async ({ method, url }) => {
      if (method === 'get' && url.includes('/templates?')) {
        return {
          data: {
            success: true,
            data: { builtin: [], items: [template], next_cursor: 0 },
          },
        }
      }
      if (method === 'get' && url.endsWith('/columns')) {
        return {
          data: {
            success: true,
            data: {
              columns: [
                {
                  key: 'model_name',
                  label: 'Model',
                  group: 'Basic',
                  audience: 'customer',
                },
                {
                  key: 'quota',
                  label: 'Quota',
                  group: 'Basic',
                  audience: 'customer',
                },
              ],
              default_columns: ['model_name', 'quota'],
              max_columns: 150,
            },
          },
        }
      }
      return {
        data: {
          success: !rejected,
          message: rejected ? message : '',
          data: template,
        },
      }
    })
    render(
      <QueryClientProvider client={client}>
        <EmployeeTemplateManager />
      </QueryClientProvider>
    )
    await user.click(
      screen.getByRole('button', { name: 'Employee export templates' })
    )
    await user.click(
      await screen.findByRole('button', {
        name: action === 'save' ? 'Edit' : 'Delete',
      })
    )
    const title = action === 'save' ? 'Edit export template' : 'Delete template'
    const button = action === 'save' ? 'Save' : 'Delete'
    if (action === 'save') {
      const name = screen.getByRole('textbox', { name: 'Template name' })
      await user.clear(name)
      await user.type(name, 'Revised customer summary')
    }
    await user.click(screen.getByRole('button', { name: button }))
    await waitFor(() => expect(notify).toHaveBeenCalledWith(message))
    expect(notify).toHaveBeenCalledTimes(1)
    expect(success).not.toHaveBeenCalled()
    expect(screen.getByRole('dialog', { name: title })).toBeVisible()
    if (action === 'save') {
      expect(
        screen.getByRole('textbox', { name: 'Template name' })
      ).toHaveValue('Revised customer summary')
    }
    const method = action === 'save' ? 'put' : 'delete'
    const attempted = request.mock.calls.find(
      ([config]) => config.method === method
    )?.[0]
    rejected = false
    await user.click(screen.getByRole('button', { name: button }))
    await waitFor(() =>
      expect(
        screen.queryByRole('dialog', { name: title })
      ).not.toBeInTheDocument()
    )
    expect(
      request.mock.calls
        .filter(([config]) => config.method === method)
        .map(([config]) => config)
    ).toEqual([attempted, attempted])
    expect(notify).toHaveBeenCalledTimes(1)
    client.clear()
  }
)

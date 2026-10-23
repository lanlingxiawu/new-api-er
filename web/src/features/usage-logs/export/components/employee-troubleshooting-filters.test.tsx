import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { expect, it } from 'vitest'

import {
  EmployeeTroubleshootingFilters,
  type EmployeeTroubleshootingFilters as Filters,
} from './employee-troubleshooting-filters'

function FilterForm() {
  const [value, onChange] = useState<Filters>({})
  return (
    <EmployeeTroubleshootingFilters
      allowed={[
        'anomaly_only',
        'anomaly_kinds',
        'charged',
        'completion_tokens_max',
      ]}
      value={value}
      onChange={onChange}
      anomalyKinds={['settlement_unsettled', 'retried']}
      quotaPerUnit={500000}
      maxFilterValues={20}
      disabled={false}
    />
  )
}

it('exposes the quick filter selection and keeps charge and zero output controls in sync', async () => {
  const user = userEvent.setup()
  render(<FilterForm />)
  await user.click(screen.getByText('Troubleshooting filters'))
  const quick = screen.getByRole('button', { name: 'Charged with zero output' })
  expect(quick).toHaveAttribute('aria-pressed', 'false')
  await user.click(quick)
  expect(quick).toHaveAttribute('aria-pressed', 'true')
  expect(
    screen.getByRole('button', { name: 'Charged (cost > 0)' })
  ).toHaveAttribute('aria-pressed', 'true')
  expect(screen.getByLabelText('Max output tokens')).toHaveValue(0)
  await user.click(screen.getByRole('button', { name: 'Free (cost = 0)' }))
  expect(quick).toHaveAttribute('aria-pressed', 'false')
  expect(
    screen.getByRole('button', { name: 'Free (cost = 0)' })
  ).toHaveAttribute('aria-pressed', 'true')
})

it('supports multiple anomaly selections and makes any anomaly mutually exclusive', async () => {
  const user = userEvent.setup()
  render(<FilterForm />)
  await user.click(screen.getByText('Troubleshooting filters'))
  const settlement = screen.getByRole('button', {
    name: 'Settlement incomplete',
  })
  const retried = screen.getByRole('button', {
    name: 'Retried across channels',
  })
  const any = screen.getByRole('button', { name: 'Any anomaly' })
  await user.click(settlement)
  await user.click(retried)
  expect(settlement).toHaveAttribute('aria-pressed', 'true')
  expect(retried).toHaveAttribute('aria-pressed', 'true')
  await user.click(any)
  expect(any).toHaveAttribute('aria-pressed', 'true')
  expect(settlement).toHaveAttribute('aria-pressed', 'false')
  expect(retried).toHaveAttribute('aria-pressed', 'false')
})

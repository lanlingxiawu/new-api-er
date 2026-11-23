import { render, screen } from '@testing-library/react'
import { expect, it } from 'vitest'

import { MaintenanceError } from './maintenance-error'

it('explains maintenance without offering a nonfunctional action', () => {
  render(<MaintenanceError />)
  expect(screen.getByRole('heading', { name: '503' })).toBeInTheDocument()
  expect(
    screen.queryByRole('button', { name: 'Learn more' })
  ).not.toBeInTheDocument()
})

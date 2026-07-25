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
import { useEffect, useState } from 'react'
import { Check, ChevronsUpDown, Loader2, X } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from '@/components/ui/command'
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from '@/components/ui/popover'
import {
  assignCustomerToEmployee,
  getEmployees,
  unassignCustomerFromEmployee,
} from '@/features/employees/api'
import { type EmployeeProfile } from '@/features/employees/types'

type SelectedEmployee = {
  /** employee_profiles.id — used by the assign/unassign endpoints */
  id: number
  /** the employee's user id — matches the customer's inviter_id */
  userId: number
  label: string
  remark: string
}

type EmployeeAssignFieldProps = {
  /** the user (customer) being edited */
  userId: number
  /** the user's current inviter_id (== assigned employee's user id, 0 if none) */
  inviterId?: number
  /** called after a successful assign/unassign so the caller can refresh */
  onChanged?: () => void
}

function employeeLabel(emp: EmployeeProfile): string {
  const name =
    emp.display_name?.trim() || emp.username?.trim() || `#${emp.user_id}`
  return `${name} (ID: ${emp.user_id})`
}

function employeeRemark(emp: EmployeeProfile): string {
  return emp.remark?.trim() || ''
}

export function EmployeeAssignField({
  userId,
  inviterId,
  onChanged,
}: EmployeeAssignFieldProps) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const [search, setSearch] = useState('')
  const [debounced, setDebounced] = useState('')
  const [options, setOptions] = useState<EmployeeProfile[]>([])
  const [loading, setLoading] = useState(false)
  const [saving, setSaving] = useState(false)
  const [selected, setSelected] = useState<SelectedEmployee | null>(null)

  // Debounce the search input.
  useEffect(() => {
    const timer = setTimeout(() => setDebounced(search.trim()), 300)
    return () => clearTimeout(timer)
  }, [search])

  // Resolve the currently assigned employee (independent of dropdown paging).
  useEffect(() => {
    let cancelled = false
    async function loadCurrent() {
      if (!inviterId || inviterId <= 0) {
        if (!cancelled) setSelected(null)
        return
      }
      try {
        const res = await getEmployees(1, 1, { user_id: inviterId })
        const emp = res?.data?.items?.[0]
        if (!cancelled && emp) {
          setSelected({
            id: emp.id,
            userId: emp.user_id,
            label: employeeLabel(emp),
            remark: employeeRemark(emp),
          })
        }
      } catch {
        /* ignore — leave unresolved */
      }
    }
    loadCurrent()
    return () => {
      cancelled = true
    }
  }, [inviterId])

  // Load employee options (server-side search) whenever the popover is open.
  useEffect(() => {
    if (!open) return
    let cancelled = false
    setLoading(true)
    getEmployees(1, 20, { keyword: debounced || undefined, status: 1 })
      .then((res) => {
        if (!cancelled) setOptions(res?.data?.items || [])
      })
      .catch(() => {
        if (!cancelled) setOptions([])
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [open, debounced])

  const handleAssign = async (emp: EmployeeProfile) => {
    if (selected?.id === emp.id) {
      setOpen(false)
      return
    }
    setSaving(true)
    try {
      const res = await assignCustomerToEmployee(emp.id, userId)
      if (res.success) {
        setSelected({
          id: emp.id,
          userId: emp.user_id,
          label: employeeLabel(emp),
          remark: employeeRemark(emp),
        })
        toast.success(t('Employee assigned successfully'))
        onChanged?.()
      } else {
        toast.error(res.message || t('Failed to assign employee'))
      }
    } catch {
      toast.error(t('Failed to assign employee'))
    } finally {
      setSaving(false)
      setOpen(false)
      setSearch('')
    }
  }

  const handleUnassign = async () => {
    if (!selected) return
    setSaving(true)
    try {
      const res = await unassignCustomerFromEmployee(selected.id, userId)
      if (res.success) {
        setSelected(null)
        toast.success(t('Employee unassigned successfully'))
        onChanged?.()
      } else {
        toast.error(res.message || t('Failed to unassign employee'))
      }
    } catch {
      toast.error(t('Failed to unassign employee'))
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className='flex items-center gap-2'>
      <Popover open={open} onOpenChange={setOpen}>
        <PopoverTrigger
          render={
            <Button
              type='button'
              variant='outline'
              role='combobox'
              aria-expanded={open}
              disabled={saving}
              className='w-full flex-1 justify-between font-normal'
            />
          }
        >
          <span className='flex min-w-0 flex-1 flex-col items-start'>
            <span
              className={cn(
                'max-w-full truncate',
                !selected && 'text-muted-foreground'
              )}
            >
              {selected ? selected.label : t('Not assigned')}
            </span>
            {selected?.remark && (
              <span className='text-muted-foreground max-w-full truncate text-xs'>
                {selected.remark}
              </span>
            )}
          </span>
          {saving ? (
            <Loader2 className='h-4 w-4 shrink-0 animate-spin opacity-50' />
          ) : (
            <ChevronsUpDown className='h-4 w-4 shrink-0 opacity-50' />
          )}
        </PopoverTrigger>
        <PopoverContent
          className='w-(--anchor-width) overflow-hidden rounded-xl p-0 shadow-lg'
          onWheel={(event) => event.stopPropagation()}
          onTouchMove={(event) => event.stopPropagation()}
          onPointerDown={(event) => event.stopPropagation()}
        >
          <Command shouldFilter={false}>
            <CommandInput
              placeholder={t('Search by name, email or remark...')}
              value={search}
              onValueChange={setSearch}
            />
            <CommandList className='max-h-80'>
              {loading ? (
                <div className='text-muted-foreground flex items-center justify-center gap-2 py-6 text-sm'>
                  <Loader2 className='h-4 w-4 animate-spin' />
                  {t('Loading...')}
                </div>
              ) : (
                <>
                  <CommandEmpty>{t('No employee found.')}</CommandEmpty>
                  <CommandGroup>
                    {options.map((emp) => (
                      <CommandItem
                        key={emp.id}
                        value={String(emp.id)}
                        onSelect={() => handleAssign(emp)}
                        className='data-[selected=true]:bg-muted items-start gap-2 rounded-lg px-3 py-2'
                      >
                        <Check
                          className={cn(
                            'mt-0.5 h-4 w-4 shrink-0 self-start',
                            selected?.id === emp.id ? 'opacity-100' : 'opacity-0'
                          )}
                        />
                        <span className='flex min-w-0 flex-1 flex-col'>
                          <span className='truncate'>{employeeLabel(emp)}</span>
                          {employeeRemark(emp) && (
                            <span className='text-muted-foreground truncate text-xs'>
                              {employeeRemark(emp)}
                            </span>
                          )}
                        </span>
                      </CommandItem>
                    ))}
                  </CommandGroup>
                </>
              )}
            </CommandList>
          </Command>
        </PopoverContent>
      </Popover>
      {selected && (
        <Button
          type='button'
          variant='ghost'
          size='icon'
          onClick={handleUnassign}
          disabled={saving}
          aria-label={t('Unassign employee')}
        >
          <X className='h-4 w-4' />
        </Button>
      )}
    </div>
  )
}

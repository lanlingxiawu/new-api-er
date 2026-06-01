import { useState } from 'react'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { upsertChannelCost } from '../api'
import type { ChannelCostConfig } from '../types'

const schema = z.object({
  channel_id: z.number().positive('Must be positive'),
  cost_ratio: z.number().min(0, 'Min 0').max(10, 'Max 10'),
  remark: z.string().max(255).optional(),
})
type FormValues = z.infer<typeof schema>

interface Props {
  open: boolean
  onOpenChange: (open: boolean) => void
  currentRow?: ChannelCostConfig
  onSuccess?: () => void
}

export function ChannelCostFormDialog({
  open,
  onOpenChange,
  currentRow,
  onSuccess,
}: Props) {
  const { t } = useTranslation()
  const [isSubmitting, setIsSubmitting] = useState(false)

  const form = useForm<FormValues>({
    resolver: zodResolver(schema),
    defaultValues: {
      channel_id: currentRow?.channel_id ?? 0,
      cost_ratio: currentRow?.cost_ratio ?? 1.0,
      remark: currentRow?.remark ?? '',
    },
  })

  const onSubmit = async (values: FormValues) => {
    setIsSubmitting(true)
    try {
      const res = await upsertChannelCost(values)
      if (!res.success) throw new Error(res.message ?? 'Failed')
      toast.success(t('Channel cost config saved'))
      onOpenChange(false)
      onSuccess?.()
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : t('Operation failed'))
    } finally {
      setIsSubmitting(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className='sm:max-w-[420px]'>
        <DialogHeader>
          <DialogTitle>{t('Set Channel Cost Ratio')}</DialogTitle>
        </DialogHeader>
        <Form {...form}>
          <form onSubmit={form.handleSubmit(onSubmit)} className='space-y-4'>
            <FormField
              control={form.control}
              name='channel_id'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Channel ID')}</FormLabel>
                  <FormControl>
                    <Input
                      type='number'
                      placeholder='e.g. 1'
                      disabled={!!currentRow}
                      {...field}
                      onChange={(e) =>
                        field.onChange(parseInt(e.target.value) || 0)
                      }
                    />
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
            <FormField
              control={form.control}
              name='cost_ratio'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Cost Ratio')}</FormLabel>
                  <FormControl>
                    <Input
                      type='number'
                      step='0.01'
                      min='0'
                      max='10'
                      {...field}
                      onChange={(e) =>
                        field.onChange(parseFloat(e.target.value) || 0)
                      }
                    />
                  </FormControl>
                  <FormDescription>
                    {t('1.0 = full cost, 0.6 = 60% of standard price')}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
            <FormField
              control={form.control}
              name='remark'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Remark')}</FormLabel>
                  <FormControl>
                    <Input placeholder={t('Optional')} {...field} />
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
            <DialogFooter>
              <Button
                type='button'
                variant='outline'
                onClick={() => onOpenChange(false)}
              >
                {t('Cancel')}
              </Button>
              <Button type='submit' disabled={isSubmitting}>
                {isSubmitting ? t('Saving...') : t('Save')}
              </Button>
            </DialogFooter>
          </form>
        </Form>
      </DialogContent>
    </Dialog>
  )
}

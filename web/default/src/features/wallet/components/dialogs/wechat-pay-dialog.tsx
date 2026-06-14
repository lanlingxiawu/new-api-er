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
import { useEffect, useRef } from 'react'
import { CheckCircle2, Loader2, XCircle } from 'lucide-react'
import { QRCodeSVG } from 'qrcode.react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { formatCurrency } from '../../lib'

interface WechatPayDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  codeUrl: string | null
  status: string | null
  paymentAmount: number
  onCheckStatus: () => Promise<string | null>
  onSuccess: () => void
}

const POLL_INTERVAL_MS = 3000

export function WechatPayDialog({
  open,
  onOpenChange,
  codeUrl,
  status,
  paymentAmount,
  onCheckStatus,
  onSuccess,
}: WechatPayDialogProps) {
  const { t } = useTranslation()
  const onCheckStatusRef = useRef(onCheckStatus)
  onCheckStatusRef.current = onCheckStatus
  const onSuccessRef = useRef(onSuccess)
  onSuccessRef.current = onSuccess

  // Poll the order status while the dialog is open and the order is pending
  useEffect(() => {
    if (!open || status !== 'pending') {
      return
    }

    const timer = setInterval(async () => {
      const result = await onCheckStatusRef.current()
      if (result === 'success') {
        onSuccessRef.current()
      }
    }, POLL_INTERVAL_MS)

    return () => clearInterval(timer)
  }, [open, status])

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className='max-sm:w-[calc(100vw-1.5rem)] sm:max-w-sm'>
        <DialogHeader>
          <DialogTitle>{t('WeChat Pay')}</DialogTitle>
          <DialogDescription>
            {status === 'success'
              ? t('Your payment has been received.')
              : status === 'failed'
                ? t('This payment could not be completed.')
                : t('Scan the QR code with WeChat to complete payment')}
          </DialogDescription>
        </DialogHeader>

        <div className='flex flex-col items-center gap-4 py-4'>
          {status === 'success' ? (
            <div className='flex flex-col items-center gap-2 py-6'>
              <CheckCircle2 className='h-12 w-12 text-green-600' />
              <p className='text-sm font-medium'>{t('Payment successful')}</p>
            </div>
          ) : status === 'failed' ? (
            <div className='flex flex-col items-center gap-2 py-6'>
              <XCircle className='text-destructive h-12 w-12' />
              <p className='text-sm font-medium'>{t('Payment failed')}</p>
            </div>
          ) : codeUrl ? (
            <>
              <div className='flex justify-center rounded-lg bg-white p-4'>
                <QRCodeSVG value={codeUrl} size={200} />
              </div>
              <div className='text-muted-foreground flex items-center gap-2 text-sm'>
                <Loader2 className='h-4 w-4 animate-spin' />
                <span>{t('Waiting for payment...')}</span>
              </div>
              <p className='text-sm'>
                {t('Amount to pay:')}{' '}
                <span className='font-semibold'>
                  {formatCurrency(paymentAmount)}
                </span>
              </p>
            </>
          ) : (
            <Loader2 className='h-8 w-8 animate-spin' />
          )}
        </div>

        <Button
          variant='outline'
          onClick={() => onOpenChange(false)}
          className='w-full'
        >
          {status === 'success' ? t('Close') : t('Cancel')}
        </Button>
      </DialogContent>
    </Dialog>
  )
}

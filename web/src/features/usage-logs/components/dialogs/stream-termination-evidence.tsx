import { useTranslation } from 'react-i18next'

import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'

interface TerminationObservation {
  side: string
  operation: string
  error_class: string
  after_ms: number
  timeout_kind?: string
  timeout_seconds?: number
  ws_close_code?: number
  response_index?: number
}

/** Private observations at this gateway, not attribution to a remote actor. */
export interface StreamTermination {
  current_response?: number
  first_observed?: TerminationObservation
  upstream_read?: TerminationObservation
  upstream_read_after_close?: boolean
  upstream_close?: TerminationObservation
}

function TerminationObservationRow(props: {
  label: string
  observation: TerminationObservation
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const sides: Record<string, string> = {
    downstream: t('Downstream'),
    upstream: t('Upstream'),
    gateway: t('Gateway'),
    unknown: t('Unknown'),
  }
  const operations: Record<string, string> = {
    request_cancel: t('Request cancellation'),
    read: t('Read'),
    write: t('Write'),
    flush: t('Flush'),
    ping: t('Ping'),
    protocol: t('Protocol validation'),
    panic: t('Panic'),
    timeout: t('Timeout'),
    close: t('Close'),
  }
  const side = Object.hasOwn(sides, props.observation.side)
    ? sides[props.observation.side]
    : undefined
  const operation = Object.hasOwn(operations, props.observation.operation)
    ? operations[props.observation.operation]
    : props.observation.operation

  return (
    <div
      role='group'
      aria-label={props.label}
      className='bg-background/60 flex flex-col gap-1 rounded border p-2 text-xs'
    >
      <p className='font-medium'>{props.label}</p>
      {(props.observation.response_index ?? 0) > 0 && (
        <p className='text-muted-foreground'>
          {t('HTTP response #{{index}}', {
            index: formatNumber(props.observation.response_index ?? 0, locale),
          })}
        </p>
      )}
      <p className='flex flex-wrap items-baseline gap-x-2 gap-y-1'>
        <span>{side ?? t('Unknown')}</span>
        {!side && <code className='break-all'>{props.observation.side}</code>}
        <span>{operation}</span>
        <span className='text-muted-foreground'>
          {t('After {{milliseconds}} ms', {
            milliseconds: formatNumber(props.observation.after_ms, locale),
          })}
        </span>
      </p>
      <p className='break-all'>
        {t('Error class')}
        {': '}
        <code>{props.observation.error_class}</code>
      </p>
      {props.observation.timeout_kind !== undefined && (
        <p className='break-all'>
          {t('Timeout type')}
          {': '}
          <code>{props.observation.timeout_kind}</code>
        </p>
      )}
      {props.observation.timeout_seconds !== undefined && (
        <p>
          {t('Timeout duration')}
          {': '}
          <span>
            {t('{{duration}} s', {
              duration: formatNumber(props.observation.timeout_seconds, locale),
            })}
          </span>
        </p>
      )}
      {props.observation.ws_close_code !== undefined && (
        <p>
          {t('WebSocket close code')}
          {': '}
          <code>{props.observation.ws_close_code}</code>
        </p>
      )}
    </div>
  )
}

export function StreamTerminationEvidence(props: {
  termination?: StreamTermination
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  return (
    <div className='flex min-w-0 flex-col gap-2'>
      <p className='text-xs font-semibold'>
        {t('Gateway-observed termination source')}
      </p>
      {!props.termination ? (
        <p className='text-muted-foreground text-xs'>
          {t('Termination observations were not recorded for this request.')}
        </p>
      ) : (
        <>
          {(props.termination.current_response ?? 0) > 0 && (
            <p className='text-muted-foreground text-xs'>
              {t('Current HTTP response: #{{index}}', {
                index: formatNumber(
                  props.termination.current_response ?? 0,
                  locale
                ),
              })}
            </p>
          )}
          {props.termination.first_observed ? (
            <TerminationObservationRow
              label={t('First termination signal')}
              observation={props.termination.first_observed}
            />
          ) : (
            <p className='text-muted-foreground text-xs'>
              {t('No abnormal termination signal was recorded.')}
            </p>
          )}
          {props.termination.upstream_read && (
            <>
              <TerminationObservationRow
                label={t('Upstream read ended')}
                observation={props.termination.upstream_read}
              />
              <p className='text-muted-foreground text-xs'>
                {t(
                  'An upstream read ending does not by itself indicate an incomplete response.'
                )}
              </p>
              {props.termination.upstream_read_after_close === true && (
                <p className='text-muted-foreground text-xs'>
                  {t(
                    'This read result was observed after the gateway started closing upstream.'
                  )}
                </p>
              )}
            </>
          )}
          {props.termination.upstream_close && (
            <TerminationObservationRow
              label={t('Gateway started closing upstream')}
              observation={props.termination.upstream_close}
            />
          )}
          <p className='text-muted-foreground text-xs'>
            {t(
              'These observations do not establish whether the client, SDK, or a proxy initiated the disconnect.'
            )}
          </p>
        </>
      )}
    </div>
  )
}

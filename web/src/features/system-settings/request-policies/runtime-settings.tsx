import { useContext } from 'react'
import { useTranslation } from 'react-i18next'

import { SettingsPageContext } from '../components/settings-page-access-context'
import { ConfigGroupSection } from '../system-tuning/hot-config-sections'
import type { RequestPolicySettings } from './defaults'

export function PolicyRuntimeSettings(props: {
  kind: 'probe' | 'daily'
  settings: RequestPolicySettings
}) {
  const { t } = useTranslation()
  const context = useContext(SettingsPageContext)
  return (
    <SettingsPageContext.Provider
      value={{ ...context, suppressSectionHeader: false }}
    >
      {props.kind === 'probe' ? (
        <ConfigGroupSection
          title={t('Maximum input length for probe detection')}
          module='probe_routing_setting'
          defaults={{
            max_input_chars:
              props.settings['probe_routing_setting.max_input_chars'],
          }}
          fields={[
            {
              key: 'max_input_chars',
              label: 'Maximum input length for probe detection',
              min: 1,
              max: 1024,
              description:
                'These requests skip channels with probe blocking enabled and use another eligible channel in the same group. A higher limit widens detection and may reroute more normal short questions; a lower limit narrows it.',
            },
          ]}
        />
      ) : (
        <ConfigGroupSection
          title={t('Enable daily amount limit')}
          module='channel_daily_limit_setting'
          description={t(
            'Per-channel limits are configured on each channel. These settings control how the feature runs globally.'
          )}
          defaults={{
            enabled: props.settings['channel_daily_limit_setting.enabled'],
            timezone: props.settings['channel_daily_limit_setting.timezone'],
            retention_days:
              props.settings['channel_daily_limit_setting.retention_days'],
          }}
          fields={[
            {
              key: 'enabled',
              label: 'Enable daily amount limit',
              type: 'switch',
              description:
                'When off, usage is no longer accumulated and channels are neither disabled nor recovered. Channels already disabled stay disabled and must be enabled manually; re-enabling this switch does not recover them retroactively.',
            },
            { key: 'timezone', label: 'Daily reset timezone', type: 'text' },
            {
              key: 'retention_days',
              label: 'Usage history retention (days)',
              min: 7,
              max: 3650,
            },
          ]}
        />
      )}
    </SettingsPageContext.Provider>
  )
}

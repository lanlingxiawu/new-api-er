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
import {
  useLayoutEffect,
  useEffect,
  useMemo,
  useRef,
  useState,
  type CSSProperties,
  type ReactNode,
} from 'react'
import { Link } from '@tanstack/react-router'
import {
  ChevronDown,
  ChevronRight,
  Code2,
  Gift,
  Headset,
  Layers3,
  Link2,
  Mail,
  Percent,
  Send,
  ShieldCheck,
  Waypoints,
  Zap,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import {
  INTERFACE_LANGUAGE_OPTIONS,
  normalizeInterfaceLanguage,
} from '@/i18n/languages'
import { cn } from '@/lib/utils'
import { useAuthStore } from '@/stores/auth-store'
import { Markdown } from '@/components/ui/markdown'
import { useStatus } from '@/hooks/use-status'
import { useSystemConfig } from '@/hooks/use-system-config'
import { useHomePageContent } from './hooks'

type TopLink = {
  labelKey: string
  to?: string
  href?: string
  external?: boolean
  hasDropdown?: boolean
}

type FooterLink = {
  labelKey: string
  to?: string
  href?: string
  external?: boolean
}

type FooterColumn = {
  titleKey: string
  links: FooterLink[]
}

type FeatureCard = {
  titleKey: string
  descriptionKey: string
  boxColor: string
  icon: 'api' | 'economy' | 'docs'
}

type WhyFeature = {
  titleKey: string
  descriptionKey: string
  icon: ReactNode
}

type GlanceCard = {
  labelKey: string
  value: string
  delta?: string
  deltaKey?: string
  noteKey: string
}

const TOP_LINKS: TopLink[] = [
  { labelKey: 'home.nav.models', hasDropdown: true },
  { labelKey: 'home.nav.pricing' },
  { labelKey: 'home.nav.docs', hasDropdown: true },
  { labelKey: 'home.nav.console' },
  { labelKey: 'home.nav.developers', hasDropdown: true },
]

const HERO_STATS = [
  { value: '200+', labelKey: 'home.hero.stats.availableModels' },
  { value: '99.95%', labelKey: 'home.hero.stats.gatewayUptime' },
  { value: '~38ms', labelKey: 'home.hero.stats.routingOverhead' },
  { value: '1 行', labelKey: 'home.hero.stats.migrationCost' },
]

const PROVIDERS = [
  { name: 'OpenAI', key: 'openai' },
  { name: 'Anthropic', key: 'anthropic' },
  { name: 'Google', key: 'google' },
  { name: 'ByteDance', key: 'bytedance' },
  { name: 'Qwen', key: 'qwen' },
  { name: 'Kimi', key: 'kimi' },
  { name: 'Minimax', key: 'minimax' },
]

const FEATURE_CARDS: FeatureCard[] = [
  {
    titleKey: 'home.features.cards.unifiedAccess.title',
    descriptionKey: 'home.features.cards.unifiedAccess.description',
    boxColor: '#e3ede6',
    icon: 'api',
  },
  {
    titleKey: 'home.features.cards.smartRouting.title',
    descriptionKey: 'home.features.cards.smartRouting.description',
    boxColor: '#d7e6dc',
    icon: 'economy',
  },
  {
    titleKey: 'home.features.cards.developerDocs.title',
    descriptionKey: 'home.features.cards.developerDocs.description',
    boxColor: '#ecf3ee',
    icon: 'docs',
  },
]

const WHY_FEATURES: WhyFeature[] = [
  {
    titleKey: 'home.why.features.ecosystemBridging.title',
    descriptionKey: 'home.why.features.ecosystemBridging.description',
    icon: <Link2 className='size-5' />,
  },
  {
    titleKey: 'home.why.features.migrationPath.title',
    descriptionKey: 'home.why.features.migrationPath.description',
    icon: <Zap className='size-5' />,
  },
  {
    titleKey: 'home.why.features.costControl.title',
    descriptionKey: 'home.why.features.costControl.description',
    icon: <Percent className='size-5' />,
  },
  {
    titleKey: 'home.why.features.controlSurface.title',
    descriptionKey: 'home.why.features.controlSurface.description',
    icon: <Layers3 className='size-5' />,
  },
  {
    titleKey: 'home.why.features.reliability.title',
    descriptionKey: 'home.why.features.reliability.description',
    icon: <ShieldCheck className='size-5' />,
  },
  {
    titleKey: 'home.why.features.multiTeam.title',
    descriptionKey: 'home.why.features.multiTeam.description',
    icon: <Headset className='size-5' />,
  },
]

const GLANCE_CARDS: GlanceCard[] = [
  {
    labelKey: 'home.dashboard.glance.totalRequests',
    value: '143.2K',
    delta: '+18.7%',
    noteKey: 'home.dashboard.glance.totalRequests.note',
  },
  {
    labelKey: 'home.dashboard.glance.totalCost',
    value: '$1,247',
    delta: '-12.3%',
    noteKey: 'home.dashboard.glance.totalCost.note',
  },
  {
    labelKey: 'home.dashboard.glance.cacheHitRate',
    value: '90%',
    delta: '+4.2%',
    noteKey: 'home.dashboard.glance.cacheHitRate.note',
  },
  {
    labelKey: 'home.dashboard.glance.availability',
    value: '99.97%',
    deltaKey: 'home.dashboard.glance.availability.delta',
    noteKey: 'home.dashboard.glance.availability.note',
  },
]

const SIDEBAR_GROUPS = [
  {
    titleKey: 'home.dashboard.sidebar.overview',
    items: [
      'home.dashboard.sidebar.dashboard',
      'home.dashboard.sidebar.usage',
      'home.dashboard.sidebar.routing',
    ],
  },
  {
    titleKey: 'home.dashboard.sidebar.operations',
    items: [
      'home.dashboard.sidebar.channels',
      'home.dashboard.sidebar.keys',
      'home.dashboard.sidebar.billing',
    ],
  },
  {
    titleKey: 'home.dashboard.sidebar.insights',
    items: [
      'home.dashboard.sidebar.latency',
      'home.dashboard.sidebar.caching',
      'home.dashboard.sidebar.costByModel',
    ],
  },
]

const LATENCY_ROWS = ['<50ms', '<100ms', '<150ms', '<200ms', '<250ms', '<300ms']
const LATENCY_COLS = 24
const COST_TABS = [
  'home.dashboard.cost.tabs.7days',
  'home.dashboard.cost.tabs.30days',
  'home.dashboard.cost.tabs.90days',
]
const COST_DAYS = [
  'home.dashboard.cost.days.mon',
  'home.dashboard.cost.days.tue',
  'home.dashboard.cost.days.wed',
  'home.dashboard.cost.days.thu',
  'home.dashboard.cost.days.fri',
  'home.dashboard.cost.days.sat',
  'home.dashboard.cost.days.sun',
]
const COST_MODELS = [
  { name: 'Claude Opus 4.7', color: '#4ba97e' },
  { name: 'Claude Opus 4.6', color: '#2e6b52' },
  { name: 'GPT 5.5', color: '#6fae93' },
  { name: 'Gemini 3.1 Pro', color: '#c79a3e' },
  { name: 'Gemini 3.5 Flash', color: '#a6745a' },
]
const COST_SERIES = [
  [28, 22, 16, 10, 6],
  [34, 26, 18, 12, 8],
  [24, 20, 15, 9, 6],
  [40, 30, 22, 14, 9],
  [32, 25, 18, 11, 7],
  [20, 16, 12, 8, 5],
  [30, 24, 17, 10, 7],
]
const LOGS = [
  { t: '12:04:51', model: 'Claude Opus 4.7', ms: '412ms', cost: '$0.182', ok: true },
  { t: '12:04:48', model: 'GPT 5.5', ms: '388ms', cost: '$0.094', ok: true },
  { t: '12:04:45', model: 'Gemini 3.1 Pro', ms: '274ms', cost: '$0.041', ok: true },
  { t: '12:04:43', model: 'Claude Opus 4.6', ms: '451ms', cost: '$0.157', ok: true },
  { t: '12:04:41', model: 'DeepSeek V4', ms: '203ms', cost: '$0.008', ok: true },
  { t: '12:04:38', model: 'GPT 5.5', ms: '—', cost: '$0.000', ok: false },
  { t: '12:04:36', model: 'Gemini 3.5 Flash', ms: '121ms', cost: '$0.003', ok: true },
]
const MODELS = [
  { name: 'Claude Opus 4.7', pct: 34, calls: '48.9K', cost: '$426' },
  { name: 'Claude Opus 4.6', pct: 26, calls: '36.5K', cost: '$318' },
  { name: 'GPT 5.5', pct: 20, calls: '28.1K', cost: '$244' },
  { name: 'Gemini 3.1 Pro', pct: 13, calls: '18.2K', cost: '$159' },
  { name: 'Gemini 3.5 Flash', pct: 7, calls: '11.5K', cost: '$100' },
]
const VIEW_LABELS = [
  'home.dashboard.sidebar.dashboard',
  'home.dashboard.sidebar.usage',
  'home.dashboard.sidebar.costByModel',
] as const
const WAYPOINTS: { left: number; top: number; view: number; click?: boolean }[] = [
  { left: 15, top: 33, view: 0, click: true },
  { left: 62, top: 62, view: 0, click: true },
  { left: 15, top: 38, view: 1, click: true },
  { left: 58, top: 50, view: 1, click: true },
  { left: 15, top: 56, view: 2, click: true },
  { left: 60, top: 48, view: 2, click: true },
]
const CURSOR_TRAVEL = 780
const DASHBOARD_CARD = 'rounded-xl border border-[#e6e9e3] bg-white'

const LANGUAGE_SHORT_LABELS: Record<string, string> = {
  zh: 'CN',
  en: 'EN',
  fr: 'FR',
  ru: 'RU',
  ja: 'JP',
  vi: 'VI',
}

const FOOTER_COLUMNS = (docsUrl: string): FooterColumn[] => [
  {
    titleKey: 'home.footer.columns.product',
    links: [
      { labelKey: 'home.footer.links.modelDirectory', to: '/pricing' },
      { labelKey: 'home.footer.links.console', to: '/dashboard' },
      { labelKey: 'home.footer.links.about', to: '/about' },
    ],
  },
  {
    titleKey: 'home.footer.columns.developers',
    links: [
      { labelKey: 'home.footer.links.getStarted', to: '/sign-up' },
      { labelKey: 'home.footer.links.apiDocs', href: docsUrl, external: true },
      {
        labelKey: 'home.footer.links.deploymentGuide',
        href: `${docsUrl.replace(/\/$/, '')}/installation/`,
        external: true,
      },
    ],
  },
  {
    titleKey: 'home.footer.columns.resources',
    links: [
      { labelKey: 'home.footer.links.github', href: 'https://github.com/QuantumNous/new-api', external: true },
      { labelKey: 'home.footer.links.documentation', href: docsUrl, external: true },
      { labelKey: 'home.footer.links.privacyPolicy', to: '/privacy-policy' },
      { labelKey: 'home.footer.links.userAgreement', to: '/user-agreement' },
    ],
  },
]

const CTA_SNIPPET = `from openai import OpenAI

client = OpenAI(
    base_url="https://n123.ai/v1",
    api_key="<your-key>",
)

response = client.chat.completions.create(
    model="gpt-5.5",
    messages=[
        {"role": "system", "content": "You are a helpful assistant."},
        {"role": "user", "content": "用一句话介绍 N123"},
    ],
    stream=True,
)

for chunk in response:
    print(chunk.choices[0].delta.content or "", end="")`

function useReducedMotion() {
  const [reduced, setReduced] = useState(false)

  useEffect(() => {
    const media = window.matchMedia('(prefers-reduced-motion: reduce)')
    const update = () => setReduced(media.matches)
    update()
    media.addEventListener('change', update)
    return () => media.removeEventListener('change', update)
  }, [])

  return reduced
}

function useReveal(threshold = 0.12) {
  const ref = useRef<HTMLDivElement | null>(null)
  const reducedMotion = useReducedMotion()
  const [visible, setVisible] = useState(reducedMotion)

  useEffect(() => {
    if (reducedMotion) {
      setVisible(true)
      return
    }
    const node = ref.current
    if (!node) return

    const observer = new IntersectionObserver(
      ([entry]) => {
        if (entry.isIntersecting) {
          setVisible(true)
          observer.disconnect()
        }
      },
      { threshold }
    )

    observer.observe(node)
    return () => observer.disconnect()
  }, [reducedMotion, threshold])

  return { ref, visible }
}

function Reveal({
  children,
  className,
  fast = true,
}: {
  children: ReactNode
  className?: string
  fast?: boolean
}) {
  const { ref, visible } = useReveal()

  return (
    <div
      ref={ref}
      className={cn(
        'fade-in-up-element',
        fast && 'fade-in-up-fast',
        visible && 'animate-fade-in-up',
        className
      )}
    >
      {children}
    </div>
  )
}

function ScrollProgress() {
  const [width, setWidth] = useState(0)

  useEffect(() => {
    const handleScroll = () => {
      const root = document.documentElement
      const max = root.scrollHeight - root.clientHeight
      setWidth(max > 0 ? (window.scrollY / max) * 100 : 0)
    }

    handleScroll()
    window.addEventListener('scroll', handleScroll, { passive: true })
    return () => window.removeEventListener('scroll', handleScroll)
  }, [])

  return (
    <div className='pointer-events-none fixed inset-x-0 top-0 z-[60] h-[3px]'>
      <div
        className='h-full bg-gradient-to-r from-[#102e24] via-[#4ba97e] to-[#2e6b52] transition-[width] duration-150 ease-out'
        style={{ width: `${width}%` }}
      />
    </div>
  )
}

function WavyText({ text, staggerDelay = 0.05 }: { text: string; staggerDelay?: number }) {
  const chars = Array.from(text)

  return (
    <span className='wavy-text' style={{ '--stagger-delay': `${staggerDelay}s` } as CSSProperties}>
      <span aria-hidden className='span-mother'>
        {chars.map((char, index) => (
          <span key={`top-${index}`} style={{ ['--i' as string]: index } as CSSProperties}>
            {char === ' ' ? '\u00A0' : char}
          </span>
        ))}
      </span>
      <span aria-hidden className='span-mother2'>
        {chars.map((char, index) => (
          <span key={`bottom-${index}`} style={{ ['--i' as string]: index } as CSSProperties}>
            {char === ' ' ? '\u00A0' : char}
          </span>
        ))}
      </span>
      <span className='sr-only'>{text}</span>
    </span>
  )
}

function brand(path: ReactNode) {
  return function BrandIcon(props: { className?: string }) {
    return (
      <svg viewBox='0 0 24 24' fill='currentColor' fillRule='evenodd' className={props.className}>
        {path}
      </svg>
    )
  }
}

const OpenAIIcon = brand(
  <path d='M21.55 10.004a5.416 5.416 0 00-.478-4.501c-1.217-2.09-3.662-3.166-6.05-2.66A5.59 5.59 0 0010.831 1C8.39.995 6.224 2.546 5.473 4.838A5.553 5.553 0 001.76 7.496a5.487 5.487 0 00.691 6.5 5.416 5.416 0 00.477 4.502c1.217 2.09 3.662 3.165 6.05 2.66A5.586 5.586 0 0013.168 23c2.443.006 4.61-1.546 5.361-3.84a5.553 5.553 0 003.715-2.66 5.488 5.488 0 00-.693-6.497v.001zm-8.381 11.558a4.199 4.199 0 01-2.675-.954c.034-.018.093-.05.132-.074l4.44-2.53a.71.71 0 00.364-.623v-6.176l1.877 1.069c.02.01.033.029.036.05v5.115c-.003 2.274-1.87 4.118-4.174 4.123zM4.192 17.78a4.059 4.059 0 01-.498-2.763c.032.02.09.055.131.078l4.44 2.53c.225.13.504.13.73 0l5.42-3.088v2.138a.068.068 0 01-.027.057L9.9 19.288c-1.999 1.136-4.552.46-5.707-1.51h-.001zM3.023 8.216A4.15 4.15 0 015.198 6.41l-.002.151v5.06a.711.711 0 00.364.624l5.42 3.087-1.876 1.07a.067.067 0 01-.063.005l-4.489-2.559c-1.995-1.14-2.679-3.658-1.53-5.63h.001zm15.417 3.54l-5.42-3.088L14.896 7.6a.067.067 0 01.063-.006l4.489 2.557c1.998 1.14 2.683 3.662 1.529 5.633a4.163 4.163 0 01-2.174 1.807V12.38a.71.71 0 00-.363-.623zm1.867-2.773a6.04 6.04 0 00-.132-.078l-4.44-2.53a.731.731 0 00-.729 0l-5.42 3.088V7.325a.068.068 0 01.027-.057L14.1 4.713c2-1.137 4.555-.46 5.707 1.513.487.833.664 1.809.499 2.757h.001zm-11.741 3.81l-1.877-1.068a.065.065 0 01-.036-.051V6.559c.001-2.277 1.873-4.122 4.181-4.12.976 0 1.92.338 2.671.954-.034.018-.092.05-.131.073l-4.44 2.53a.71.71 0 00-.365.623l-.003 6.173v.002zm1.02-2.168L12 9.25l2.414 1.375v2.75L12 14.75l-2.415-1.375v-2.75z' />
)
const AnthropicIcon = brand(
  <path d='M13.827 3.52h3.603L24 20h-3.603l-6.57-16.48zm-7.258 0h3.767L16.906 20h-3.674l-1.343-3.461H5.017l-1.344 3.46H0L6.57 3.522zm4.132 9.959L8.453 7.687 6.205 13.48H10.7z' />
)
const GoogleIcon = brand(
  <>
    <path d='M23 12.245c0-.905-.075-1.565-.236-2.25h-10.54v4.083h6.186c-.124 1.014-.797 2.542-2.294 3.569l-.021.136 3.332 2.53.23.022C21.779 18.417 23 15.593 23 12.245z' />
    <path d='M12.225 23c3.03 0 5.574-.978 7.433-2.665l-3.542-2.688c-.948.648-2.22 1.1-3.891 1.1a6.745 6.745 0 01-6.386-4.572l-.132.011-3.465 2.628-.045.124C4.043 20.531 7.835 23 12.225 23z' />
    <path d='M5.84 14.175A6.65 6.65 0 015.463 12c0-.758.138-1.491.361-2.175l-.006-.147-3.508-2.67-.115.054A10.831 10.831 0 001 12c0 1.772.436 3.447 1.197 4.938l3.642-2.763z' />
    <path d='M12.225 5.253c2.108 0 3.529.892 4.34 1.638l3.167-3.031C17.787 2.088 15.255 1 12.225 1 7.834 1 4.043 3.469 2.197 7.062l3.63 2.763a6.77 6.77 0 016.398-4.572z' />
  </>
)
const ByteDanceIcon = brand(
  <>
    <path d='M14.944 18.587l-1.704-.445V10.01l1.824-.462c1-.254 1.84-.461 1.88-.453.032 0 .056 2.235.056 4.972v4.973l-.176-.008c-.104 0-.952-.207-1.88-.446z' />
    <path d='M7 16.542c0-2.736.024-4.98.064-4.98.032-.008.872.2 1.88.454l1.816.461-.016 4.05-.024 4.049-1.632.422c-.896.23-1.736.445-1.856.469L7 21.523v-4.98z' />
    <path d='M19.24 12.477c0-9.03.008-9.515.144-9.475.072.024.784.207 1.576.406.792.207 1.576.405 1.744.445l.296.08-.016 8.56-.024 8.568-1.624.414c-.888.23-1.728.437-1.856.47l-.24.055v-9.523z' />
    <path d='M1 12.509c0-4.678.024-8.505.064-8.505.032 0 .872.207 1.872.454l1.824.461v7.582c0 4.16-.016 7.574-.032 7.574-.024 0-.872.215-1.88.47L1 21.013v-8.505z' />
  </>
)
const QwenIcon = brand(
  <path d='M12.604 1.34c.393.69.784 1.382 1.174 2.075a.18.18 0 00.157.091h5.552c.174 0 .322.11.446.327l1.454 2.57c.19.337.24.478.024.837-.26.43-.513.864-.76 1.3l-.367.658c-.106.196-.223.28-.04.512l2.652 4.637c.172.301.111.494-.043.77-.437.785-.882 1.564-1.335 2.34-.159.272-.352.375-.68.37-.777-.016-1.552-.01-2.327.016a.099.099 0 00-.081.05 575.097 575.097 0 01-2.705 4.74c-.169.293-.38.363-.725.364-.997.003-2.002.004-3.017.002a.537.537 0 01-.465-.271l-1.335-2.323a.09.09 0 00-.083-.049H4.982c-.285.03-.553-.001-.805-.092l-1.603-2.77a.543.543 0 01-.002-.54l1.207-2.12a.198.198 0 000-.197 550.951 550.951 0 01-1.875-3.272l-.79-1.395c-.16-.31-.173-.496.095-.965.465-.813.927-1.625 1.387-2.436.132-.234.304-.334.584-.335a338.3 338.3 0 012.589-.001.124.124 0 00.107-.063l2.806-4.895a.488.488 0 01.422-.246c.524-.001 1.053 0 1.583-.006L11.704 1c.341-.003.724.032.9.34zm-3.432.403a.06.06 0 00-.052.03L6.254 6.788a.157.157 0 01-.135.078H3.253c-.056 0-.07.025-.041.074l5.81 10.156c.025.042.013.062-.034.063l-2.795.015a.218.218 0 00-.2.116l-1.32 2.31c-.044.078-.021.118.068.118l5.716.008c.046 0 .08.02.104.061l1.403 2.454c.046.081.092.082.139 0l5.006-8.76.783-1.382a.055.055 0 01.096 0l1.424 2.53a.122.122 0 00.107.062l2.763-.02a.04.04 0 00.035-.02.041.041 0 000-.04l-2.9-5.086a.108.108 0 010-.113l.293-.507 1.12-1.977c.024-.041.012-.062-.035-.062H9.2c-.059 0-.073-.026-.043-.077l1.434-2.505a.107.107 0 000-.114L9.225 1.774a.06.06 0 00-.053-.031z' />
)
const MinimaxIcon = brand(
  <path d='M16.278 2c1.156 0 2.093.927 2.093 2.07v12.501a.74.74 0 00.744.709.74.74 0 00.743-.709V9.099a2.06 2.06 0 012.071-2.049A2.06 2.06 0 0124 9.1v6.561a.649.649 0 01-.652.645.649.649 0 01-.653-.645V9.1a.762.762 0 00-.766-.758.762.762 0 00-.766.758v7.472a2.037 2.037 0 01-2.048 2.026 2.037 2.037 0 01-2.048-2.026v-12.5a.785.785 0 00-.788-.753.785.785 0 00-.789.752l-.001 15.904A2.037 2.037 0 0113.441 22a2.037 2.037 0 01-2.048-2.026V18.04c0-.356.292-.645.652-.645.36 0 .652.289.652.645v1.934c0 .263.142.506.372.638.23.131.514.131.744 0a.734.734 0 00.372-.638V4.07c0-1.143.937-2.07 2.093-2.07zm-5.674 0c1.156 0 2.093.927 2.093 2.07v11.523a.648.648 0 01-.652.645.648.648 0 01-.652-.645V4.07a.785.785 0 00-.789-.78.785.785 0 00-.789.78v14.013a2.06 2.06 0 01-2.07 2.048 2.06 2.06 0 01-2.071-2.048V9.1a.762.762 0 00-.766-.758.762.762 0 00-.766.758v3.8a2.06 2.06 0 01-2.071 2.049A2.06 2.06 0 010 12.9v-1.378c0-.357.292-.646.652-.646.36 0 .653.29.653.646V12.9c0 .418.343.757.766.757s.766-.339.766-.757V9.099a2.06 2.06 0 012.07-2.048 2.06 2.06 0 012.071 2.048v8.984c0 .419.343.758.767.758.423 0 .766-.339.766-.758V4.07c0-1.143.937-2.07 2.093-2.07z' />
)

function ProviderIcon({ providerKey, name }: { providerKey: string; name: string }) {
  const icons: Record<string, (props: { className?: string }) => ReactNode> = {
    openai: OpenAIIcon,
    anthropic: AnthropicIcon,
    google: GoogleIcon,
    bytedance: ByteDanceIcon,
    qwen: QwenIcon,
    minimax: MinimaxIcon,
  }
  const Icon = icons[providerKey]
  if (Icon) return <Icon className='size-6 text-[#4ba97e]' />
  return <span className='text-sm font-semibold text-[#4ba97e]'>{name.charAt(0)}</span>
}

function NavTarget({
  link,
  className,
  children,
  onClick,
}: {
  link: TopLink | FooterLink
  className?: string
  children: ReactNode
  onClick?: () => void
}) {
  if (link.href) {
    return (
      <a
        href={link.href}
        target={link.external ? '_blank' : undefined}
        rel={link.external ? 'noreferrer' : undefined}
        className={className}
        onClick={onClick}
      >
        {children}
      </a>
    )
  }

  return (
    <Link to={(link.to || '/') as never} className={className} onClick={onClick}>
      {children}
    </Link>
  )
}

function MobileMenu({
  links,
  open,
  onClose,
  isAuthenticated,
  loginTarget,
  primaryTarget,
}: {
  links: TopLink[]
  open: boolean
  onClose: () => void
  isAuthenticated: boolean
  loginTarget: string
  primaryTarget: string
}) {
  const { t } = useTranslation()

  return (
    <div
      className={cn(
        'fixed inset-0 z-[100] flex flex-col bg-[#eaf1ec] transition-transform duration-300 ease-in-out lg:hidden',
        open ? 'translate-x-0' : 'translate-x-full'
      )}
      aria-hidden={!open}
    >
      <div className='flex items-center justify-between px-[16px] py-4'>
        <span className='flex items-center gap-[10px]'>
          <img src='/n123-logo.svg' alt='N123' className='h-[26px] w-auto' />
          <span className='text-[18px] font-semibold text-[#14201a]'>N123</span>
        </span>
        <button
          type='button'
          aria-label={t('home.menu.close')}
          onClick={onClose}
          className='relative h-[24px] w-[24px]'
        >
          <span className='absolute left-0 top-1/2 block h-[1.5px] w-full rotate-45 bg-[#14201a]' />
          <span className='absolute left-0 top-1/2 block h-[1.5px] w-full -rotate-45 bg-[#14201a]' />
        </button>
      </div>

      <nav className='mt-6 flex flex-col px-[20px]'>
        {links.map((link) => (
          <NavTarget
            key={link.labelKey}
            link={link}
            onClick={onClose}
            className='flex items-center justify-between border-b border-black/10 py-[16px] text-[20px] font-medium text-black'
          >
            <>
              {t(link.labelKey)}
              {link.hasDropdown && <ChevronDown className='h-[18px] w-[18px] opacity-50' />}
            </>
          </NavTarget>
        ))}
      </nav>

      <div className='mt-auto flex flex-col gap-[12px] p-[20px]'>
        <Link
          to={loginTarget as never}
          onClick={onClose}
          className='inline-flex h-[48px] items-center justify-center rounded-full border border-[#4ba97e]/20 bg-white text-[15px] font-medium text-[#14201a]'
        >
          {isAuthenticated ? t('home.actions.goToConsole') : t('Sign in')}
        </Link>
        <Link
          to={primaryTarget as never}
          onClick={onClose}
          className='inline-flex h-[48px] items-center justify-center rounded-full bg-[#4ba97e] text-[15px] font-semibold text-white'
        >
          {t('home.actions.freeStart')}
        </Link>
      </div>
    </div>
  )
}

function LandingNavbar({
  docsUrl,
  isAuthenticated,
}: {
  docsUrl: string
  isAuthenticated: boolean
}) {
  const { t, i18n } = useTranslation()
  const [menuOpen, setMenuOpen] = useState(false)
  const [scrolled, setScrolled] = useState(false)
  const loginTarget = isAuthenticated ? '/dashboard' : '/sign-in'
  const primaryTarget = isAuthenticated ? '/dashboard' : '/sign-up'
  const currentLanguage = normalizeInterfaceLanguage(i18n.language)
  const currentLanguageLabel =
    LANGUAGE_SHORT_LABELS[currentLanguage] ?? currentLanguage.toUpperCase()

  const links = useMemo<TopLink[]>(
    () =>
      TOP_LINKS.map((link) => {
        if (link.labelKey === 'home.nav.docs') {
          return { ...link, href: docsUrl, external: true }
        }
        return link
      }),
    [docsUrl]
  )

  useEffect(() => {
    const onScroll = () => setScrolled(window.scrollY > 30)
    onScroll()
    window.addEventListener('scroll', onScroll, { passive: true })
    return () => window.removeEventListener('scroll', onScroll)
  }, [])

  useEffect(() => {
    document.body.style.overflow = menuOpen ? 'hidden' : ''
    return () => {
      document.body.style.overflow = ''
    }
  }, [menuOpen])

  return (
    <>
      <header
        className={cn(
          'fixed left-0 right-0 top-0 z-50 px-[16px] py-4 transition-colors duration-300 md:px-[26px] md:py-5',
          scrolled
            ? 'border-b border-[#e6e9e3]/70 bg-[#fbfbf9]/80 backdrop-blur-md'
            : 'bg-transparent'
        )}
      >
        <div className='mx-auto flex max-w-[1420px] items-center justify-between'>
          <div className='flex items-center gap-[36px]'>
            <a href='#top' aria-label='N123' className='flex items-center gap-[8px]'>
              <img src='/n123-logo.svg' alt='N123' className='h-[26px] w-auto' />
              <span className='text-[19px] font-semibold tracking-tight text-[#14201a]'>N123</span>
            </a>

            <nav className='hidden items-center gap-[22px] lg:flex'>
              {links.map((link) => (
                <NavTarget
                  key={link.labelKey}
                  link={link}
                  className='group flex items-center gap-[4px] whitespace-nowrap text-[15px] font-medium text-[#14201a]/85 transition-colors hover:text-[#14201a]'
                >
                  <>
                    <span className='decoration-[#4ba97e] underline-offset-[5px] group-hover:underline'>
                      {t(link.labelKey)}
                    </span>
                    {link.hasDropdown && <ChevronDown className='h-[14px] w-[14px] opacity-60' />}
                  </>
                </NavTarget>
              ))}
            </nav>
          </div>

          <div className='flex items-center gap-[12px] md:gap-[16px]'>
            <label className='relative hidden cursor-pointer items-center gap-[4px] text-[15px] font-medium text-[#14201a]/85 transition-colors hover:text-[#14201a] sm:flex'>
              <span>{currentLanguageLabel}</span>
              <ChevronDown className='h-[14px] w-[14px] opacity-60' />
              <span className='sr-only'>{t('Change language')}</span>
              <select
                aria-label={t('Change language')}
                className='absolute inset-0 cursor-pointer opacity-0'
                value={currentLanguage}
                onChange={(event) => {
                  void i18n.changeLanguage(event.target.value)
                }}
              >
                {INTERFACE_LANGUAGE_OPTIONS.map((language) => (
                  <option key={language.code} value={language.code}>
                    {language.label}
                  </option>
                ))}
              </select>
            </label>

            <Link
              to={loginTarget as never}
              className='group hidden h-[34px] items-center rounded-[10px] border border-[#4ba97e]/15 bg-white px-[16px] text-[14px] font-medium text-[#14201a] transition-colors hover:border-[#4ba97e]/35 sm:inline-flex'
            >
              <span className='decoration-[#4ba97e] underline-offset-[5px] group-hover:underline'>
                {isAuthenticated ? t('Dashboard') : t('Sign in')}
              </span>
            </Link>

            <Link
              to={primaryTarget as never}
              className='inline-flex h-[34px] items-center rounded-[10px] bg-[#4ba97e] px-[16px] text-[14px] font-semibold text-white shadow-[0_4px_14px_rgba(75,169,126,0.3)] transition-colors hover:bg-[#143c2f]'
            >
              <WavyText text={isAuthenticated ? t('home.actions.openConsole') : t('home.actions.freeStart')} />
            </Link>

            <button
              type='button'
              aria-label={t('home.menu.open')}
              onClick={() => setMenuOpen(true)}
              className='flex h-[24px] w-[24px] flex-col justify-center gap-[5px] lg:hidden'
            >
              <span className='block h-[1.5px] w-full bg-[#14201a]' />
              <span className='block h-[1.5px] w-full bg-[#14201a]' />
            </button>
          </div>
        </div>
      </header>

      <MobileMenu
        links={links}
        open={menuOpen}
        onClose={() => setMenuOpen(false)}
        isAuthenticated={isAuthenticated}
        loginTarget={loginTarget}
        primaryTarget={primaryTarget}
      />
    </>
  )
}

function ProviderMarquee() {
  const { t } = useTranslation()
  const half = [...PROVIDERS, ...PROVIDERS, ...PROVIDERS]
  const track = [...half, ...half]

  return (
    <section className='border-y border-[#e6e9e3] bg-[#fbfbf9] py-7'>
      <p className='mb-5 text-center text-[13px] tracking-wide text-[#5b6b62]'>
        {t('home.providers.title')}
      </p>
      <div className='marquee-pause fade-x mx-auto max-w-7xl overflow-hidden px-4'>
        <div className='flex w-max animate-marquee items-center gap-12'>
          {track.map((provider, index) => (
            <div
              key={`${provider.key}-${index}`}
              className='flex shrink-0 items-center gap-2 text-[#5b6b62]'
            >
              <ProviderIcon providerKey={provider.key} name={provider.name} />
              <span className='text-base font-medium text-[#14201a]/80'>{provider.name}</span>
            </div>
          ))}
        </div>
      </div>
    </section>
  )
}

function DashboardShell({ children }: { children: ReactNode }) {
  return (
    <div className='overflow-hidden rounded-[24px] border border-[#e6e9e3] bg-[#fbfbf9] text-left shadow-[0_24px_60px_-20px_rgba(75,169,126,0.25)]'>
      {children}
    </div>
  )
}

function useCountUpText(value: string, active: boolean) {
  const [display, setDisplay] = useState(active ? value : value.replace(/[\d.]+/, '0'))

  useEffect(() => {
    if (!active) {
      setDisplay(value.replace(/[\d.]+/, '0'))
      return
    }

    const match = value.match(/^(.*?)(\d[\d,.]*)(.*)$/)
    if (!match) {
      setDisplay(value)
      return
    }

    const [, prefix, rawNumber, suffix] = match
    const numericValue = Number(rawNumber.replace(/,/g, ''))
    if (!Number.isFinite(numericValue)) {
      setDisplay(value)
      return
    }

    const hasDecimal = rawNumber.includes('.')
    const decimalPlaces = hasDecimal ? rawNumber.split('.')[1]?.length ?? 0 : 0
    const duration = 900
    const startedAt = performance.now()
    let frame = 0

    const formatValue = (current: number) => {
      const formatted = hasDecimal
        ? current.toFixed(decimalPlaces)
        : Math.round(current).toLocaleString('en-US')
      return `${prefix}${formatted}${suffix}`
    }

    const tick = (now: number) => {
      const progress = Math.min((now - startedAt) / duration, 1)
      const eased = 1 - Math.pow(1 - progress, 3)
      setDisplay(formatValue(numericValue * eased))
      if (progress < 1) frame = requestAnimationFrame(tick)
      else setDisplay(value)
    }

    frame = requestAnimationFrame(tick)
    return () => cancelAnimationFrame(frame)
  }, [active, value])

  return display
}

function GlanceCard({
  labelKey,
  value,
  delta,
  deltaKey,
  noteKey,
  active,
}: GlanceCard & { active: boolean }) {
  const { t } = useTranslation()
  const display = useCountUpText(value, active)

  return (
    <div className='h-full rounded-lg border border-[#e6e9e3] bg-white p-3'>
      <span className='text-[11px] text-[#5b6b62]'>{t(labelKey)}</span>
      <div className='mt-1 flex items-baseline gap-2'>
        <span className='tabular-nums text-xl font-bold tracking-tight text-[#14201a]'>
          {display}
        </span>
        {(delta || deltaKey) && (
          <span className='text-[11px] font-medium text-[#2e6b52]'>
            {deltaKey ? t(deltaKey) : delta}
          </span>
        )}
      </div>
      <p className='mt-1.5 line-clamp-2 text-[10px] leading-snug text-[#5b6b62]/80'>
        {t(noteKey)}
      </p>
    </div>
  )
}

const HEATMAP_RGB: [number, number, number][] = [
  [75, 169, 126],
  [46, 107, 82],
  [111, 174, 147],
  [191, 207, 90],
  [216, 169, 58],
  [196, 127, 58],
]

function heatmapRand(row: number, col: number) {
  const seed = Math.sin(row * 127.1 + col * 311.7) * 43758.5453
  return seed - Math.floor(seed)
}

function heatmapVolume(row: number, col: number) {
  const rowWeight = Math.max(0.1, 1 - row * 0.15)
  const colBump = 0.12 * Math.exp(-((col - 13) ** 2) / 110)
  return Math.min(1, Math.max(0.05, rowWeight * 0.5 + heatmapRand(row, col) * 0.55 + colBump))
}

function heatmapColor(row: number, vol: number) {
  const [r, g, b] = HEATMAP_RGB[Math.min(row, HEATMAP_RGB.length - 1)]
  return `rgba(${r},${g},${b},${(0.4 + vol * 0.6).toFixed(2)})`
}

function GlanceDashboard({ active }: { active: boolean }) {
  const { t } = useTranslation()
  const maxTotal = Math.max(...COST_SERIES.map((day) => day.reduce((sum, item) => sum + item, 0)))
  const cacheRate = useCountUpText('90%', active)

  return (
    <div className='grid grid-cols-12 gap-2'>
      {GLANCE_CARDS.map((card) => (
        <div key={card.labelKey} className='col-span-6 sm:col-span-3'>
          <GlanceCard {...card} active={active} />
        </div>
      ))}

      <div className='col-span-12 flex flex-col rounded-lg border border-[#e6e9e3] bg-white lg:col-span-7'>
        <div className='flex items-center justify-between border-b border-[#e6e9e3] px-3 py-2'>
          <div className='flex items-center gap-2'>
            <Waypoints className='h-3.5 w-3.5 text-[#5b6b62]' />
            <span className='text-xs font-semibold text-[#14201a]'>{t('home.dashboard.latency.title')}</span>
          </div>
          <span className='inline-flex items-center gap-1 rounded-full bg-[#eaf1ec] px-2 py-0.5 text-[10px] font-semibold text-[#4ba97e]'>
            <span className='h-1.5 w-1.5 rounded-full bg-[#4ba97e]' />
            {t('home.dashboard.latency.subtitle')}
          </span>
        </div>
        <div className='flex-1 p-3'>
          <div className='flex gap-2'>
            <div className='flex shrink-0 flex-col justify-between py-0.5 text-right text-[9px] text-[#5b6b62]/70'>
              {LATENCY_ROWS.map((label) => (
                <span key={label}>{label}</span>
              ))}
            </div>
            <div className='flex-1'>
              <div className='flex flex-col gap-1'>
                {LATENCY_ROWS.map((rowLabel, rowIndex) => (
                  <div key={rowLabel} className='flex gap-1'>
                    {Array.from({ length: LATENCY_COLS }).map((_, colIndex) => {
                      if (rowIndex >= 3 && heatmapRand(colIndex, rowIndex) < (rowIndex - 2) * 0.22) {
                        return (
                          <div
                            key={`${rowLabel}-${colIndex}`}
                            className='aspect-square flex-1 rounded-[2px] bg-[#f1f3ef]'
                            style={{ opacity: active ? 1 : 0, transition: 'opacity 0.4s ease' }}
                          />
                        )
                      }

                      const intensity = heatmapVolume(rowIndex, colIndex)
                      return (
                        <div
                          key={`${rowLabel}-${colIndex}`}
                          className='aspect-square flex-1 rounded-[2px]'
                          style={{
                            backgroundColor: heatmapColor(rowIndex, intensity),
                            opacity: active ? 1 : 0,
                            transform: active ? 'scale(1)' : 'scale(0.7)',
                            transition: 'opacity 0.4s ease, transform 0.4s ease',
                          }}
                        />
                      )
                    })}
                  </div>
                ))}
              </div>
              <div className='mt-2 flex justify-between text-[9px] text-[#5b6b62]/60'>
                {['00:00', '06:00', '12:00', '18:00', '24:00'].map((tick) => (
                  <span key={tick}>{tick}</span>
                ))}
              </div>
            </div>
          </div>
        </div>
      </div>

      <div className='col-span-12 flex flex-col rounded-lg border border-[#e6e9e3] bg-white p-3 lg:col-span-5'>
        <div className='mb-2 flex items-center gap-2'>
          <Zap className='h-3.5 w-3.5 text-[#5b6b62]' />
          <span className='text-xs font-semibold text-[#14201a]'>{t('home.dashboard.cache.title')}</span>
        </div>
        <div className='flex flex-1 items-center justify-center'>
          <div className='relative h-[130px] w-[130px]'>
            <svg className='h-full w-full -rotate-90' viewBox='0 0 120 120'>
              <circle cx='60' cy='60' r={52} fill='none' stroke='#eaf1ec' strokeWidth='10' />
              <circle
                cx='60'
                cy='60'
                r={52}
                fill='none'
                stroke='#4ba97e'
                strokeWidth='10'
                strokeLinecap='round'
                strokeDasharray={`${(90 / 100) * (2 * Math.PI * 52)} ${2 * Math.PI * 52}`}
                strokeDashoffset={active ? 0 : (90 / 100) * (2 * Math.PI * 52)}
                style={{ transition: 'stroke-dashoffset 1.2s cubic-bezier(0.22,1,0.36,1)' }}
              />
            </svg>
            <div className='absolute inset-0 flex flex-col items-center justify-center'>
              <span className='tabular-nums text-2xl font-bold text-[#4ba97e]'>{cacheRate}</span>
              <span className='text-[10px] text-[#5b6b62]'>{t('home.dashboard.glance.cacheHitRate')}</span>
            </div>
          </div>
        </div>
        <div className='mt-1 grid grid-cols-3 gap-2'>
          {[
            ['127.4K', 'home.dashboard.cache.hits'],
            ['14.2K', 'home.dashboard.cache.misses'],
            ['$1,180', 'home.dashboard.cache.saved'],
          ].map(([label, key]) => (
            <div key={key} className='text-center'>
              <div className='text-sm font-semibold text-[#14201a]'>{label}</div>
              <div className='text-[10px] text-[#5b6b62]/80'>{t(key)}</div>
            </div>
          ))}
        </div>
      </div>

      <div className='col-span-12 flex flex-col rounded-lg border border-[#e6e9e3] bg-white lg:col-span-7'>
        <div className='flex items-center justify-between border-b border-[#e6e9e3] px-3 py-2'>
          <div className='flex items-center gap-2'>
            <Code2 className='h-3.5 w-3.5 text-[#5b6b62]' />
            <span className='text-xs font-semibold text-[#14201a]'>{t('home.dashboard.dailyCost.title')}</span>
          </div>
          <div className='flex gap-1'>
            {COST_TABS.map((tab, index) => (
              <span
                key={tab}
                className={cn(
                  'rounded px-1.5 py-0.5 text-[10px]',
                  index === 0 ? 'bg-[#eaf1ec] text-[#4ba97e]' : 'text-[#5b6b62]/70'
                )}
              >
                {t(tab)}
              </span>
            ))}
          </div>
        </div>
        <div className='flex flex-1 flex-col p-3'>
          <div className='flex min-h-[120px] flex-1 items-end justify-between gap-2'>
            {COST_SERIES.map((day, dayIndex) => (
              <div key={COST_DAYS[dayIndex]} className='flex flex-1 flex-col items-center gap-1.5'>
                <div
                  className='flex w-full flex-col-reverse overflow-hidden rounded-t'
                  style={{
                    height: '120px',
                    transform: active ? 'scaleY(1)' : 'scaleY(0)',
                    transformOrigin: 'bottom',
                    transition: 'transform 0.7s cubic-bezier(0.22,1,0.36,1)',
                    transitionDelay: `${dayIndex * 70}ms`,
                  }}
                >
                  {day.map((segment, segmentIndex) => {
                    const model = COST_MODELS[segmentIndex]
                    return (
                      <div
                        key={`${COST_DAYS[dayIndex]}-${model.name}`}
                        style={{
                          height: `${(segment / maxTotal) * 100}%`,
                          backgroundColor: model.color,
                          opacity: 0.92 - segmentIndex * 0.08,
                        }}
                      />
                    )
                  })}
                </div>
                <span className='text-[9px] text-[#5b6b62]/70'>{t(COST_DAYS[dayIndex])}</span>
              </div>
            ))}
          </div>
          <div className='mt-3 flex flex-wrap gap-x-3 gap-y-1'>
            {COST_MODELS.map((model) => (
              <span key={model.name} className='flex items-center gap-1 text-[9px] text-[#5b6b62]'>
                <span className='h-2 w-2 rounded-full' style={{ backgroundColor: model.color }} />
                {model.name}
              </span>
            ))}
          </div>
        </div>
      </div>

      <div className='col-span-12 flex flex-col rounded-lg border border-[#e6e9e3] bg-white p-3 lg:col-span-5'>
        <div className='mb-3 flex items-center gap-2'>
          <Layers3 className='h-3.5 w-3.5 text-[#5b6b62]' />
          <span className='text-xs font-semibold text-[#14201a]'>{t('home.dashboard.cost.title')}</span>
        </div>
        <div className='flex-1 space-y-2.5'>
          {MODELS.map((model, index) => (
            <div key={model.name} className='flex flex-col gap-1'>
              <div className='flex items-center justify-between text-[11px]'>
                <span className='text-[#14201a]/80'>{model.name}</span>
                <span className='text-[#5b6b62]'>
                  <span className='text-[#2e6b52]'>{model.cost}</span> · {model.pct}%
                </span>
              </div>
              <div className='h-1.5 overflow-hidden rounded-full bg-[#eaf1ec]'>
                <div
                  className='h-full rounded-full'
                  style={{
                    width: active ? `${model.pct}%` : '0%',
                    backgroundColor: COST_MODELS[index]?.color ?? '#4ba97e',
                    transition: 'width 0.9s cubic-bezier(0.22,1,0.36,1)',
                    transitionDelay: `${index * 80}ms`,
                  }}
                />
              </div>
            </div>
          ))}
        </div>
        <div className='mt-3 flex items-center justify-between border-t border-[#e6e9e3] pt-2 text-[11px]'>
          <span className='text-[#5b6b62]'>143.2K requests</span>
          <span className='font-semibold text-[#14201a]'>$1,247</span>
        </div>
      </div>
    </div>
  )
}

function HeroAura() {
  const ref = useRef<HTMLDivElement | null>(null)

  useEffect(() => {
    const node = ref.current
    if (!node) return

    let raf = 0
    const handleMove = (event: MouseEvent) => {
      cancelAnimationFrame(raf)
      raf = requestAnimationFrame(() => {
        const x = event.clientX / window.innerWidth - 0.5
        const y = event.clientY / window.innerHeight - 0.5
        node.style.setProperty('--px', `${x * 34}px`)
        node.style.setProperty('--py', `${y * 34}px`)
      })
    }

    window.addEventListener('mousemove', handleMove)
    return () => {
      window.removeEventListener('mousemove', handleMove)
      cancelAnimationFrame(raf)
    }
  }, [])

  return (
    <div
      ref={ref}
      aria-hidden
      className='pointer-events-none absolute inset-0 overflow-hidden transition-transform duration-300 ease-out'
      style={{ transform: 'translate(var(--px, 0px), var(--py, 0px))' }}
    >
      <span
        aria-hidden
        className='absolute left-[12%] top-[4%] h-[460px] w-[460px] rounded-full opacity-60 blur-[100px]'
        style={{
          background: 'radial-gradient(circle, rgba(75,169,126,0.34), transparent 70%)',
          animation: 'aura-float 16s ease-in-out infinite',
        }}
      />
      <span
        aria-hidden
        className='absolute right-[8%] top-0 h-[420px] w-[420px] rounded-full opacity-50 blur-[100px]'
        style={{
          background: 'radial-gradient(circle, rgba(111,174,147,0.30), transparent 70%)',
          animation: 'aura-float-alt 20s ease-in-out infinite',
        }}
      />
      <span
        aria-hidden
        className='absolute bottom-[2%] left-[38%] h-[520px] w-[520px] rounded-full opacity-50 blur-[100px]'
        style={{
          background: 'radial-gradient(circle, rgba(46,107,82,0.26), transparent 70%)',
          animation: 'aura-float 22s ease-in-out infinite',
        }}
      />
    </div>
  )
}

function HeroDashboard() {
  const { t } = useTranslation()
  const [step, setStep] = useState(0)
  const [view, setView] = useState(0)
  const [clicked, setClicked] = useState(false)

  useEffect(() => {
    const interval = setInterval(() => setStep((value) => (value + 1) % WAYPOINTS.length), 2400)
    return () => clearInterval(interval)
  }, [])

  useEffect(() => {
    setClicked(false)
    const timer = setTimeout(() => {
      setView(WAYPOINTS[step].view)
      if (WAYPOINTS[step].click) setClicked(true)
    }, CURSOR_TRAVEL)
    return () => clearTimeout(timer)
  }, [step])

  const waypoint = WAYPOINTS[step]

  return (
    <DashboardShell>
      <div className='flex items-center gap-2 border-b border-[#e6e9e3] bg-[#f1f3ef] px-4 py-3'>
        <span className='h-3 w-3 rounded-full bg-rose-400' />
        <span className='h-3 w-3 rounded-full bg-amber-400' />
        <span className='h-3 w-3 rounded-full bg-emerald-400' />
        <div className='ml-3 max-w-md flex-1 rounded-md border border-[#e6e9e3] bg-white px-3 py-1 font-mono text-[11px] text-[#5b6b62]'>
          {`https://n123.ai/${['analytics', 'logs', 'models'][view]}`}
        </div>
      </div>

      <div className='flex'>
        <aside className='hidden w-52 shrink-0 flex-col gap-3 border-r border-[#e6e9e3] bg-[#fbfbf9] p-3 md:flex'>
          <div className='flex items-center gap-2 px-1 py-1'>
            <img src='/n123-logo.svg' alt='N123' className='h-5 w-auto' />
            <span className='text-xs font-semibold text-[#14201a]'>N123</span>
          </div>
          <div className='rounded-xl border border-[#e6e9e3] bg-white px-2.5 py-2'>
            <div className='text-[9px] text-[#5b6b62]'>{t('Balance')}</div>
            <div className='text-sm font-semibold text-[#14201a]'>$4,182.14</div>
            <button className='mt-1 text-[10px] font-medium text-[#4ba97e]'>
              + {t('Recharge')}
            </button>
          </div>
          {SIDEBAR_GROUPS.map((group) => (
            <div key={group.titleKey} className='flex flex-col gap-0.5'>
              <span className='mb-0.5 px-1 text-[9px] uppercase tracking-wider text-[#9aa39d]'>
                {t(group.titleKey)}
              </span>
              {group.items.map((item) => {
                const isActive = item === VIEW_LABELS[view]
                return (
                  <span
                    key={item}
                    className={cn(
                      'rounded px-2 py-1.5 text-[11px] transition-colors',
                      isActive ? 'bg-[#eaf1ec] font-medium text-[#4ba97e]' : 'text-[#5b6b62]'
                    )}
                  >
                    {t(item)}
                  </span>
                )
              })}
            </div>
          ))}
          <div className='mt-auto flex items-center gap-2 border-t border-[#e6e9e3] px-1 pt-2'>
            <span className='flex h-6 w-6 items-center justify-center rounded-full bg-[#2e6b52]'>
              <img src='/n123-logo.svg' alt='N123' className='h-3 w-auto brightness-0 invert' />
            </span>
            <span className='text-[11px] text-[#5b6b62]'>N123</span>
          </div>
        </aside>

        <div className='flex-1 bg-white p-5'>
          <div className='mb-4 flex items-center justify-between'>
            <h3 className='text-sm font-semibold text-[#14201a]'>
              {t(VIEW_LABELS[view])}
            </h3>
            <div className='flex gap-1.5'>
              {COST_TABS.map((tab, index) => (
                <span
                  key={tab}
                  className={cn(
                    'rounded-md px-2 py-1 text-[10px]',
                    index === 0 ? 'bg-[#4ba97e] text-white' : 'bg-[#f1f3ef] text-[#5b6b62]'
                  )}
                >
                  {t(tab)}
                </span>
              ))}
            </div>
          </div>

          <div className='relative h-[520px]'>
            {[
              <div key='analytics' className='flex h-full flex-col gap-4'>
                <div className='grid grid-cols-3 gap-3'>
                  {GLANCE_CARDS.map((card) => (
                    <div key={card.labelKey} className={`${DASHBOARD_CARD} px-3 py-2.5`}>
                      <div className='text-[10px] text-[#5b6b62]'>{t(card.labelKey)}</div>
                      <div className='text-lg font-bold text-[#14201a]'>{card.value}</div>
                    </div>
                  ))}
                </div>

                <div className='grid flex-1 grid-cols-2 gap-3'>
                  <div className={`${DASHBOARD_CARD} p-4`}>
                    <div className='mb-2.5 flex items-center justify-between'>
                      <span className='text-[11px] text-[#5b6b62]'>
                        {t('home.dashboard.cost.title')}
                      </span>
                      <span className='text-sm font-semibold text-[#14201a]'>$445.95</span>
                    </div>
                    <div className='flex h-[132px] items-end gap-1.5'>
                      <div className='flex-1 rounded-sm bg-gradient-to-t from-[#4ba97e] to-[#6fae93]' style={{ height: '30%' }} />
                      <div className='flex-1 rounded-sm bg-gradient-to-t from-[#4ba97e] to-[#6fae93]' style={{ height: '55%' }} />
                      <div className='flex-1 rounded-sm bg-gradient-to-t from-[#4ba97e] to-[#6fae93]' style={{ height: '40%' }} />
                      <div className='flex-1 rounded-sm bg-gradient-to-t from-[#4ba97e] to-[#6fae93]' style={{ height: '70%' }} />
                      <div className='flex-1 rounded-sm bg-gradient-to-t from-[#4ba97e] to-[#6fae93]' style={{ height: '45%' }} />
                      <div className='flex-1 rounded-sm bg-gradient-to-t from-[#4ba97e] to-[#6fae93]' style={{ height: '85%' }} />
                      <div className='flex-1 rounded-sm bg-gradient-to-t from-[#4ba97e] to-[#6fae93]' style={{ height: '60%' }} />
                      <div className='flex-1 rounded-sm bg-gradient-to-t from-[#4ba97e] to-[#6fae93]' style={{ height: '75%' }} />
                    </div>
                  </div>

                  <div className={`${DASHBOARD_CARD} p-4`}>
                    <div className='mb-2.5 flex items-center justify-between'>
                      <span className='text-[11px] text-[#5b6b62]'>
                        {t('home.dashboard.glance.totalRequests')}
                      </span>
                      <span className='text-sm font-semibold text-[#14201a]'>123,200</span>
                    </div>
                    <div className='flex h-[132px] items-end gap-1.5'>
                      <div className='flex-1 rounded-sm bg-gradient-to-t from-[#2e6b52] to-[#8fd9b4]' style={{ height: '50%' }} />
                      <div className='flex-1 rounded-sm bg-gradient-to-t from-[#2e6b52] to-[#8fd9b4]' style={{ height: '35%' }} />
                      <div className='flex-1 rounded-sm bg-gradient-to-t from-[#2e6b52] to-[#8fd9b4]' style={{ height: '65%' }} />
                      <div className='flex-1 rounded-sm bg-gradient-to-t from-[#2e6b52] to-[#8fd9b4]' style={{ height: '45%' }} />
                      <div className='flex-1 rounded-sm bg-gradient-to-t from-[#2e6b52] to-[#8fd9b4]' style={{ height: '80%' }} />
                      <div className='flex-1 rounded-sm bg-gradient-to-t from-[#2e6b52] to-[#8fd9b4]' style={{ height: '55%' }} />
                      <div className='flex-1 rounded-sm bg-gradient-to-t from-[#2e6b52] to-[#8fd9b4]' style={{ height: '70%' }} />
                      <div className='flex-1 rounded-sm bg-gradient-to-t from-[#2e6b52] to-[#8fd9b4]' style={{ height: '90%' }} />
                    </div>
                  </div>
                </div>

                <div className='grid flex-1 grid-cols-2 gap-3'>
                  <div className={`${DASHBOARD_CARD} p-4`}>
                    <div className='mb-2.5 flex items-center justify-between'>
                      <span className='text-[11px] text-[#5b6b62]'>
                        {t('home.dashboard.latency.title')}
                      </span>
                      <span className='text-sm font-semibold text-[#14201a]'>
                        {t('home.dashboard.realTime')}
                      </span>
                    </div>
                    <div className='grid h-[132px] grid-cols-[52px_1fr] gap-3'>
                      <div className='grid grid-rows-6 gap-1.5 text-right text-[9px] leading-none text-[#9aa39d]'>
                        {LATENCY_ROWS.map((rowLabel) => (
                          <span key={rowLabel} className='flex items-center justify-end'>
                            {rowLabel}
                          </span>
                        ))}
                      </div>
                      <div
                        className='grid h-full gap-1.5'
                        style={{ gridTemplateColumns: `repeat(${LATENCY_COLS}, minmax(0, 1fr))` }}
                      >
                        {Array.from({ length: LATENCY_COLS }).map((_, colIndex) => (
                          <div key={colIndex} className='flex h-full flex-col gap-1.5'>
                            {LATENCY_ROWS.map((rowLabel, rowIndex) => {
                              const opacity =
                                0.12 + ((rowIndex + (colIndex % LATENCY_ROWS.length)) % 6) * 0.12
                              return (
                                <div
                                  key={`${colIndex}-${rowLabel}`}
                                  className='flex-1 rounded-[2px]'
                                  style={{
                                    backgroundColor: `rgba(75,169,126,${opacity.toFixed(2)})`,
                                  }}
                                />
                              )
                            })}
                          </div>
                        ))}
                      </div>
                    </div>
                  </div>

                  <div className={`${DASHBOARD_CARD} p-4`}>
                    <div className='mb-2.5 flex items-center justify-between'>
                      <span className='text-[11px] text-[#5b6b62]'>
                        {t('home.dashboard.cache.title')}
                      </span>
                      <span className='text-sm font-semibold text-[#14201a]'>90%</span>
                    </div>
                    <div className='mb-4 h-2 overflow-hidden rounded-full bg-[#eaf1ec]'>
                      <div className='h-full rounded-full bg-gradient-to-r from-[#4ba97e] to-[#6fae93]' style={{ width: '90%' }} />
                    </div>
                    <div className='grid grid-cols-3 gap-3 text-center'>
                      {[
                        ['127.4K', 'home.dashboard.cache.hits'],
                        ['14.2K', 'home.dashboard.cache.misses'],
                        ['$1,180', 'home.dashboard.cache.saved'],
                      ].map(([value, label]) => (
                        <div key={label} className='rounded-[14px] bg-[#fbfbf9] px-3 py-3'>
                          <div className='text-[15px] font-semibold text-[#14201a]'>{value}</div>
                          <div className='mt-1 text-[11px] text-[#5b6b62]'>{t(label)}</div>
                        </div>
                      ))}
                    </div>
                  </div>
                </div>

                <div className={`${DASHBOARD_CARD} p-4`}>
                  <div className='mb-4 flex items-center justify-between'>
                    <div className='text-[15px] font-semibold text-[#14201a]'>
                      {t('home.dashboard.dailyCost.title')}
                    </div>
                    <div className='rounded-full border border-[#e6e9e3] bg-[#fbfbf9] px-3 py-1 text-[11px] text-[#5b6b62]'>
                      {t('home.dashboard.updated')}
                    </div>
                  </div>

                  <div className='rounded-[18px] border border-[#e6e9e3] bg-[#fbfbf9] p-4'>
                    <div className='mb-6 grid grid-cols-7 gap-3 text-center text-[11px] text-[#5b6b62]'>
                      {COST_DAYS.map((day) => (
                        <div key={day}>{t(day)}</div>
                      ))}
                    </div>
                    <div className='grid grid-cols-7 items-end gap-3'>
                      {COST_SERIES.map((group, index) => {
                        const total = group.reduce((sum, item) => sum + item, 0)
                        return (
                          <div key={COST_DAYS[index]} className='flex h-[220px] flex-col justify-end gap-[6px]'>
                            {group
                              .slice()
                              .reverse()
                              .map((value, stackIndex) => {
                                const model = COST_MODELS[group.length - stackIndex - 1]
                                return (
                                  <div
                                    key={`${COST_DAYS[index]}-${model.name}`}
                                    className='rounded-[8px]'
                                    style={{
                                      height: `${Math.max(14, value * 3)}px`,
                                      backgroundColor: model.color,
                                      opacity: 0.92 - stackIndex * 0.08,
                                    }}
                                    title={`${model.name}: ${value}`}
                                  />
                                )
                              })}
                            <div className='pt-1 text-center text-[11px] text-[#5b6b62]'>${total}</div>
                          </div>
                        )
                      })}
                    </div>
                  </div>
                </div>
              </div>,
              <div key='logs' className='flex h-full flex-col'>
                <div className='grid grid-cols-[80px_1fr_64px_56px] gap-2 border-b border-[#e6e9e3] pb-2 text-[10px] uppercase tracking-wider text-[#9aa39d]'>
                  <span>{t('Time')}</span>
                  <span>{t('Model')}</span>
                  <span className='text-right'>{t('Latency')}</span>
                  <span className='text-right'>{t('Cost')}</span>
                </div>
                <div className='divide-y divide-[#eef0ec]'>
                  {LOGS.map((log) => (
                    <div key={log.t} className='grid grid-cols-[80px_1fr_64px_56px] items-center gap-2 py-2.5 text-[11px]'>
                      <span className='font-mono text-[#5b6b62]'>{log.t}</span>
                      <span className='flex items-center gap-2 text-[#14201a]'>
                        <span className={cn('h-1.5 w-1.5 rounded-full', log.ok ? 'bg-[#2e6b52]' : 'bg-rose-500')} />
                        {log.model}
                      </span>
                      <span className='text-right font-mono text-[#5b6b62]'>{log.ms}</span>
                      <span className='text-right font-mono text-[#4ba97e]'>{log.cost}</span>
                    </div>
                  ))}
                </div>
              </div>,
              <div key='models' className='flex h-full flex-col gap-3'>
                {MODELS.map((model) => (
                  <div key={model.name} className={`${DASHBOARD_CARD} px-3.5 py-3`}>
                    <div className='flex items-center justify-between text-[12px]'>
                      <span className='font-medium text-[#14201a]'>{model.name}</span>
                      <span className='text-[#5b6b62]'>
                        <span className='text-[#4ba97e]'>{model.cost}</span> · {model.calls}
                      </span>
                    </div>
                    <div className='mt-2 h-1.5 overflow-hidden rounded-full bg-[#eaf1ec]'>
                      <div className='h-full rounded-full bg-gradient-to-r from-[#2e6b52] to-[#6fae93]' style={{ width: `${model.pct * 2.6}%` }} />
                    </div>
                  </div>
                ))}
              </div>,
            ].map((node, index) => (
              <div
                key={index}
                className='absolute inset-0 transition-all duration-500 ease-in-out'
                style={{
                  opacity: index === view ? 1 : 0,
                  transform: index === view ? 'translateX(0)' : 'translateX(24px)',
                  pointerEvents: index === view ? 'auto' : 'none',
                }}
              >
                {node}
              </div>
            ))}
          </div>
        </div>
      </div>

      <div
        className='pointer-events-none absolute z-50 transition-all duration-[750ms] ease-out'
        style={{ left: `${waypoint.left}%`, top: `${waypoint.top}%` }}
      >
        {clicked && waypoint.click && (
          <span
            key={step}
            className='absolute left-0 top-0 h-8 w-8 rounded-full bg-[#4ba97e]/25'
            style={{ animation: 'cursor-ripple 0.7s ease-out forwards' }}
          />
        )}
        <svg width='20' height='24' viewBox='0 0 20 24' fill='none' style={{ filter: 'drop-shadow(rgba(0,0,0,0.25) 0px 2px 6px)' }}>
          <path
            d='M1 1L1 18L5.5 13.5L9.5 22L12.5 20.5L8.5 12L14 11L1 1Z'
            fill='#14201a'
            stroke='white'
            strokeWidth='1.5'
            strokeLinejoin='round'
          />
        </svg>
      </div>
    </DashboardShell>
  )
}

function HeroSection({
  docsUrl,
  isAuthenticated,
}: {
  docsUrl: string
  isAuthenticated: boolean
}) {
  const { t } = useTranslation()
  const primaryTarget = isAuthenticated ? '/dashboard' : '/sign-up'
  const secondaryTarget = isAuthenticated ? '/dashboard' : '/sign-in'

  return (
    <section className='relative flex flex-1 items-center overflow-hidden'>
      <HeroAura />
      <div className='relative z-10 mx-auto w-full max-w-[1360px] px-[24px] pb-[64px] pt-[120px]'>
        <div className='grid items-center gap-12 lg:grid-cols-[0.9fr_1.1fr] lg:gap-14'>
          <div className='flex flex-col items-center text-center lg:items-start lg:text-left'>
            <a
              href='#models'
              className='hero-fade-in-up group mb-[24px] inline-flex items-center gap-[6px] rounded-full border border-[#4ba97e]/15 bg-[#eaf1ec] px-[16px] py-[8px] text-[12px] leading-normal text-[#4ba97e] transition-all duration-300 hover:border-[#4ba97e]/30 hover:shadow-sm md:text-[13px]'
            >
              <Gift className='h-[14px] w-[14px] opacity-70 transition-opacity group-hover:opacity-100' />
              <span className='font-semibold text-[#102e24]'>{t('home.hero.badge.count')}</span>
              <span>{t('home.hero.badge.connected')}</span>
              <span>·</span>
              <span>{t('home.hero.badge.modalities')}</span>
              <ChevronRight className='h-[14px] w-[14px] opacity-50 transition-transform group-hover:translate-x-[2px]' />
            </a>

            <h1 className='hero-fade-in-up text-[38px] font-semibold leading-[1.08] tracking-tight text-[#14201a] md:text-[54px] lg:text-[58px] font-kefaiii-bold'>
              {t('home.hero.title')}
              <br />
              <span className='text-[#4ba97e]'>{t('home.hero.subtitle')}</span>
            </h1>

            <p className='hero-fade-in-up mt-[20px] max-w-[520px] text-[16px] leading-[1.7] text-[#5b6b62] md:text-[18px]'>
              {t('home.hero.description')}
            </p>

            <div className='hero-fade-in-up mt-[30px] flex flex-col items-center gap-[12px] sm:flex-row'>
              <Link
                to={primaryTarget as never}
                className='inline-flex h-[40px] items-center justify-center rounded-[10px] bg-[#4ba97e] px-[28px] text-[15px] font-semibold text-[#fbfbf9] shadow-[0_8px_24px_rgba(75,169,126,0.28)] transition-colors hover:bg-[#143c2f]'
              >
                {isAuthenticated ? t('home.actions.openConsole') : t('home.actions.freeStart')}
              </Link>
              <a
                href={isAuthenticated ? secondaryTarget : docsUrl}
                target={isAuthenticated ? undefined : '_blank'}
                rel={isAuthenticated ? undefined : 'noreferrer'}
                className='inline-flex h-[40px] items-center justify-center rounded-[10px] border border-[#4ba97e]/20 bg-white px-[28px] text-[15px] font-medium text-[#14201a] transition-colors hover:border-[#4ba97e]/40'
              >
                {isAuthenticated ? t('home.actions.goToDashboard') : t('home.actions.readDocs')}
              </a>
            </div>

            <dl className='hero-fade-in-up mt-[36px] grid w-full max-w-[520px] grid-cols-4 gap-x-4 gap-y-2'>
              {HERO_STATS.map((stat) => (
                <div key={stat.labelKey} className='text-center lg:text-left'>
                  <dd className='text-[22px] font-semibold text-[#4ba97e] md:text-[24px]'>
                    {stat.value}
                  </dd>
                  <dt className='text-[12px] text-[#5b6b62]'>{t(stat.labelKey)}</dt>
                </div>
              ))}
            </dl>
          </div>

          <div className='hero-fade-in-up w-full lg:-mr-[340px] xl:-mr-[560px]'>
            <HeroDashboard />
          </div>
        </div>
      </div>
    </section>
  )
}

function GlanceSection() {
  const { t } = useTranslation()
  const { ref, visible } = useReveal(0.2)

  return (
    <section id='dashboard' className='bg-[#fbfbf9] py-16 sm:py-20 lg:py-24'>
      <div className='mx-auto max-w-7xl px-6 lg:px-8'>
        <div className='mb-8 text-center'>
          <h2 className='text-3xl font-bold tracking-tight text-[#14201a] sm:text-4xl font-kefaiii-bold'>
            {t('home.glance.title')}
          </h2>
          <p className='mx-auto mt-3 max-w-xl text-[#5b6b62]'>
            {t('home.glance.description')}
          </p>
        </div>
        <div ref={ref}>
          <GlanceDashboard active={visible} />
        </div>
      </div>
    </section>
  )
}

function FeatureIcon({ icon }: { icon: FeatureCard['icon'] }) {
  const Icon = icon === 'api' ? Layers3 : icon === 'economy' ? Waypoints : Code2
  return <Icon className='h-[60px] w-[60px] text-[#4ba97e] md:h-[64px] md:w-[64px]' strokeWidth={1.5} />
}

function FeaturesSection() {
  const { t } = useTranslation()

  return (
    <section className='relative w-full overflow-hidden'>
      <div className='relative z-10 w-full bg-[#eaf1ec] py-[80px] md:py-[110px]'>
        <div className='features-mesh-bg pointer-events-none absolute inset-0' />
        <Reveal className='relative z-10 mx-auto w-full max-w-[1400px] px-[20px] md:px-[24px]'>
          <div className='text-center md:text-start'>
            <p className='mb-3 text-[13px] tracking-wide text-[#5b6b62]'>
              {t('home.features.kicker')}
            </p>
            <h2 className='text-[28px] font-semibold leading-[1.1] text-[#14201a] md:text-[36px] lg:text-[44px] font-kefaiii-bold'>
              {t('home.features.title')}
            </h2>
          </div>

          <div className='mb-[64px] mt-6 hidden h-px w-full bg-[#4ba97e]/15 md:block min-[1600px]:mb-[100px]' />

          <div className='grid grid-cols-1 gap-10 md:flex md:items-stretch md:gap-0'>
            {FEATURE_CARDS.map((card, index) => (
              <div
                key={card.titleKey}
                className={cn('flex-1 md:px-6', index > 0 && 'md:border-l md:border-[#4ba97e]/15')}
              >
                <div className='transition-transform duration-300 ease-[cubic-bezier(0.22,1,0.36,1)] hover:-translate-y-[6px]'>
                  <div
                    className='flex h-[174px] items-center justify-center rounded-[18px] shadow-[0_10px_30px_-12px_rgba(75,169,126,0.35)]'
                    style={{ backgroundColor: card.boxColor }}
                  >
                    <FeatureIcon icon={card.icon} />
                  </div>
                  <h3 className='mt-[20px] text-[20px] font-semibold leading-[30px] text-[#14201a]'>
                    {t(card.titleKey)}
                  </h3>
                  <p className='mt-[8px] text-[17px] leading-[27px] text-[#5b6b62]'>
                    {t(card.descriptionKey)}
                  </p>
                </div>
              </div>
            ))}
          </div>
        </Reveal>
      </div>
    </section>
  )
}

function MapCtaSection() {
  const { t } = useTranslation()

  return (
    <section className='relative w-full bg-[#fbfbf9] py-16 sm:py-20 lg:py-24'>
      <div className='mx-auto max-w-3xl px-4 text-center'>
        <p className='text-[13px] tracking-wide text-[#5b6b62]'>{t('home.map.kicker')}</p>
        <h2 className='mt-2 text-3xl font-bold tracking-tight text-[#14201a] sm:text-4xl font-kefaiii-bold'>
          {t('home.map.title')}
        </h2>
        <p className='mx-auto mt-3 max-w-xl text-[#5b6b62]'>
          {t('home.map.description')}
        </p>
      </div>

      <div className='relative mx-auto mt-10 w-full max-w-[1920px] px-[16px]'>
        <div className='relative aspect-[3/1] w-full overflow-hidden'>
          <img
            src='/images/world-map.svg'
            alt='world map'
            className='pointer-events-none absolute inset-0 h-full w-full select-none object-cover object-top'
          />

          <svg
            viewBox='0 0 800 400'
            preserveAspectRatio='xMidYMin slice'
            className='pointer-events-none absolute inset-0 h-full w-full select-none'
          >
            <defs>
              <linearGradient id='arc-gradient' x1='0%' y1='0%' x2='100%' y2='0%'>
                <stop offset='0%' stopColor='#4ba97e' stopOpacity='0' />
                <stop offset='18%' stopColor='#4ba97e' stopOpacity='1' />
                <stop offset='82%' stopColor='#2e6b52' stopOpacity='1' />
                <stop offset='100%' stopColor='#2e6b52' stopOpacity='0' />
              </linearGradient>
              <filter id='arc-glow' x='-80%' y='-80%' width='260%' height='260%'>
                <feGaussianBlur stdDeviation='2.6' result='b' />
                <feMerge>
                  <feMergeNode in='b' />
                  <feMergeNode in='SourceGraphic' />
                </feMerge>
              </filter>
            </defs>

            {[
              'M 67.79 57.33 Q 102.51 7.33 137.24 124.33',
              'M 67.79 57.33 Q 180.68 7.33 293.57 235.11',
              'M 293.57 235.11 Q 336.63 63.95 379.69 113.95',
              'M 399.72 85.54 Q 485.65 35.54 571.58 136.41',
              'M 571.58 136.41 Q 632.36 54.15 693.14 104.15',
              'M 571.58 136.41 Q 526.7 86.41 481.83 202.87',
            ].map((path, index) => (
              <path
                key={`base-${index}`}
                d={path}
                fill='none'
                stroke='#4ba97e'
                strokeOpacity={0.25}
                strokeWidth={0.8}
                strokeLinecap='round'
              />
            ))}

            {[
              'M 67.79 57.33 Q 102.51 7.33 137.24 124.33',
              'M 67.79 57.33 Q 180.68 7.33 293.57 235.11',
              'M 293.57 235.11 Q 336.63 63.95 379.69 113.95',
              'M 399.72 85.54 Q 485.65 35.54 571.58 136.41',
              'M 571.58 136.41 Q 632.36 54.15 693.14 104.15',
              'M 571.58 136.41 Q 526.7 86.41 481.83 202.87',
            ].map((path, index) => (
              <path
                key={`flow-${index}`}
                d={path}
                fill='none'
                stroke='url(#arc-gradient)'
                strokeWidth={1.4}
                strokeLinecap='round'
                className='arc-flow'
                style={{ animationDelay: `${index * 0.15}s` }}
              />
            ))}

            {[ 
              [67.79, 57.33],
              [137.24, 124.33],
              [293.57, 235.11],
              [379.69, 113.95],
              [399.72, 85.54],
              [571.58, 136.41],
              [693.14, 104.15],
              [481.83, 202.87],
            ].map(([cx, cy], index) => (
              <circle key={index} cx={cx} cy={cy} r={2.6} fill='#4ba97e' />
            ))}

            {[
              'M 67.79 57.33 Q 102.51 7.33 137.24 124.33',
              'M 67.79 57.33 Q 180.68 7.33 293.57 235.11',
              'M 293.57 235.11 Q 336.63 63.95 379.69 113.95',
              'M 399.72 85.54 Q 485.65 35.54 571.58 136.41',
              'M 571.58 136.41 Q 632.36 54.15 693.14 104.15',
              'M 571.58 136.41 Q 526.7 86.41 481.83 202.87',
            ].map((path, index) => (
              <g key={`comet-${index}`} filter='url(#arc-glow)'>
                <circle r={4.4} fill='#34c772' fillOpacity={0.35}>
                  <animateMotion
                    dur={`${2.6 + index * 0.45}s`}
                    begin={`${index * 0.5}s`}
                    repeatCount='indefinite'
                    path={path}
                    keyPoints='0;1'
                    keyTimes='0;1'
                    calcMode='spline'
                    keySplines='0.4 0 0.2 1'
                  />
                </circle>
                <circle r={2.4} fill='#4ba97e'>
                  <animateMotion
                    dur={`${2.6 + index * 0.45}s`}
                    begin={`${index * 0.5}s`}
                    repeatCount='indefinite'
                    path={path}
                    keyPoints='0;1'
                    keyTimes='0;1'
                    calcMode='spline'
                    keySplines='0.4 0 0.2 1'
                  />
                </circle>
              </g>
            ))}
          </svg>

          <div className='absolute left-1/2 top-[44%] hidden -translate-x-1/2 flex-col items-center md:flex'>
            <img src='/n123-logo.svg' alt='N123' className='h-[50px] w-auto' />
            <span className='h-[54px] w-px bg-[#4ba97e]/40' />
            <span className='-mt-[3px] h-[6px] w-[6px] rounded-full bg-[#4ba97e] shadow-[0_0_8px_2px_rgba(75,169,126,0.55)]' />
          </div>
        </div>
      </div>
    </section>
  )
}

function WhyChooseSection() {
  const { t } = useTranslation()

  return (
    <section className='bg-[#fbfbf9] py-16 sm:py-20 lg:py-24'>
      <div className='mx-auto max-w-7xl px-4 sm:px-6 lg:px-8'>
        <div className='text-center'>
          <h2 className='text-3xl font-bold tracking-tight text-[#14201a] sm:text-4xl font-kefaiii-bold'>
            {t('home.why.title')}
          </h2>
          <p className='mx-auto mt-4 max-w-[640px] text-[#5b6b62]'>
            {t('home.why.description')}
          </p>
        </div>

        <div className='mt-14 grid gap-x-8 gap-y-10 sm:grid-cols-2 lg:grid-cols-3'>
          {WHY_FEATURES.map((feature) => (
            <Reveal key={feature.titleKey}>
              <div className='flex gap-4'>
                <span className='flex size-10 shrink-0 items-center justify-center rounded-lg bg-[#eaf1ec] text-[#4ba97e]'>
                  {feature.icon}
                </span>
                <div>
                  <h3 className='font-semibold text-[#14201a]'>{t(feature.titleKey)}</h3>
                  <p className='mt-1 text-sm leading-relaxed text-[#5b6b62]'>
                    {t(feature.descriptionKey)}
                  </p>
                </div>
              </div>
            </Reveal>
          ))}
        </div>
      </div>
    </section>
  )
}

function tokenizePython(code: string) {
  const tokenColors = {
    keyword: '#ff8fb0',
    string: '#c3e88d',
    fn: '#82c7ff',
    number: '#f7a072',
    comment: '#7fa594',
    punct: '#a7d8c1',
    text: '#dcefe5',
  } as const
  const keywords = new Set([
    'from',
    'import',
    'for',
    'in',
    'or',
    'as',
    'def',
    'return',
    'None',
    'True',
    'False',
    'with',
    'if',
    'else',
    'and',
    'not',
    'while',
    'class',
  ])

  type Token = { text: string; color: string }
  const tokens: Token[] = []
  const re =
    /(#[^\n]*)|("(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*')|(\d+(?:\.\d+)?)|([A-Za-z_]\w*)|(\s+)|([^\sA-Za-z_]+)/g
  let match: RegExpExecArray | null

  while ((match = re.exec(code))) {
    if (match[1]) tokens.push({ text: match[1], color: tokenColors.comment })
    else if (match[2]) tokens.push({ text: match[2], color: tokenColors.string })
    else if (match[3]) tokens.push({ text: match[3], color: tokenColors.number })
    else if (match[4]) {
      const next = code[re.lastIndex]
      const color = keywords.has(match[4])
        ? tokenColors.keyword
        : next === '('
          ? tokenColors.fn
          : tokenColors.text
      tokens.push({ text: match[4], color })
    } else if (match[5]) {
      tokens.push({ text: match[5], color: tokenColors.text })
    } else if (match[6]) {
      tokens.push({ text: match[6], color: tokenColors.punct })
    }
  }

  return tokens
}

function TypewriterCode({ code, speed = 42 }: { code: string; speed?: number }) {
  const [count, setCount] = useState(0)
  const reducedMotion = useReducedMotion()
  const tokens = useMemo(() => tokenizePython(code), [code])

  useEffect(() => {
    if (reducedMotion) {
      setCount(code.length)
      return
    }

    let index = 0
    let timer: ReturnType<typeof setTimeout>
    const tick = () => {
      index += 1
      setCount(index)
      if (index < code.length) {
        timer = setTimeout(tick, code[index] === '\n' ? speed * 4 : speed)
      } else {
        timer = setTimeout(() => {
          index = 0
          setCount(0)
          timer = setTimeout(tick, speed)
        }, 2600)
      }
    }

    timer = setTimeout(tick, 500)
    return () => clearTimeout(timer)
  }, [code, reducedMotion, speed])

  const rendered: ReactNode[] = []
  let remaining = count
  for (let index = 0; index < tokens.length && remaining > 0; index += 1) {
    const slice = tokens[index].text.slice(0, remaining)
    rendered.push(
      <span key={index} style={{ color: tokens[index].color }}>
        {slice}
      </span>
    )
    remaining -= slice.length
  }

  return (
    <pre className='pointer-events-none absolute inset-0 m-0 overflow-hidden p-6 text-left font-mono text-[12px] leading-relaxed whitespace-pre opacity-50 select-none sm:p-10 sm:text-[13px]'>
      {rendered}
      <span className='caret-blink text-[#a7d8c1]'>▋</span>
    </pre>
  )
}

function CtaSection({
  docsUrl,
  isAuthenticated,
}: {
  docsUrl: string
  isAuthenticated: boolean
}) {
  const { t } = useTranslation()
  const primaryTarget = isAuthenticated ? '/dashboard' : '/sign-up'
  const secondaryTarget = isAuthenticated ? '/dashboard' : docsUrl

  return (
    <section className='bg-[#fbfbf9] px-[16px] pb-16 sm:pb-20 lg:pb-24'>
      <div className='relative mx-auto max-w-6xl overflow-hidden rounded-[24px] bg-[#102e24] px-6 py-16 text-center sm:py-20'>
        <TypewriterCode code={CTA_SNIPPET} />

        <div
          aria-hidden
          className='absolute inset-0 bg-[radial-gradient(ellipse_60%_60%_at_50%_50%,rgba(16,46,36,0.82),rgba(16,46,36,0.42))]'
        />
        <div
          aria-hidden
          className='pointer-events-none absolute left-1/2 top-[-33%] h-[420px] w-[420px] -translate-x-1/2 rounded-full opacity-50 blur-[90px]'
          style={{
            background: 'radial-gradient(circle, rgba(111,174,147,0.45), transparent 70%)',
          }}
        />

        <div className='relative z-10'>
          <h2 className='mx-auto max-w-[640px] text-3xl font-bold tracking-tight text-white drop-shadow-[0_2px_12px_rgba(0,0,0,0.35)] sm:text-4xl font-kefaiii-bold'>
            {t('home.cta.title')}
          </h2>
          <p className='mt-4 text-white/80 drop-shadow-[0_1px_8px_rgba(0,0,0,0.3)]'>
            {t('home.cta.description')}
          </p>
          <div className='mt-9 flex flex-col items-center justify-center gap-3 sm:flex-row'>
            <Link
              to={primaryTarget as never}
              className='inline-flex h-[40px] items-center justify-center rounded-[10px] bg-white px-7 text-[15px] font-semibold text-[#102e24] transition-colors hover:bg-[#eef2ee]'
            >
              {isAuthenticated ? t('home.actions.openConsole') : t('home.actions.createAccount')}
            </Link>
            {isAuthenticated ? (
              <Link
                to={secondaryTarget as never}
                className='inline-flex h-[40px] items-center justify-center rounded-[10px] border border-white/40 px-7 text-[15px] font-medium text-white backdrop-blur-sm transition-colors hover:border-white/70'
              >
                {t('home.actions.viewDashboard')}
              </Link>
            ) : (
              <a
                href={secondaryTarget}
                target='_blank'
                rel='noreferrer'
                className='inline-flex h-[40px] items-center justify-center rounded-[10px] border border-white/40 px-7 text-[15px] font-medium text-white backdrop-blur-sm transition-colors hover:border-white/70'
              >
                {t('home.actions.readDocs')}
              </a>
            )}
          </div>
        </div>
      </div>
    </section>
  )
}

function LandingFooter({
  docsUrl,
  siteName,
}: {
  docsUrl: string
  siteName: string
}) {
  const { t } = useTranslation()
  const year = new Date().getFullYear()
  const footerColumns = useMemo(() => FOOTER_COLUMNS(docsUrl), [docsUrl])

  return (
    <footer className='mx-[10px] mb-[10px] rounded-[16px] bg-[#102e24] text-[#eef2ee]'>
      <div className='mx-auto max-w-[1420px] px-[24px] pb-[30px] pt-[60px] md:px-[50px] md:pt-[80px]'>
        <div className='grid grid-cols-2 gap-x-[24px] gap-y-[40px] md:grid-cols-5'>
          <div className='col-span-2'>
            <div className='flex items-center gap-[10px]'>
              <img src='/n123-logo.svg' alt='N123' className='h-[26px] w-auto' />
              <span className='text-[19px] font-semibold font-kefaiii-bold'>N123</span>
            </div>
            <p className='mt-[16px] max-w-[280px] text-[14px] leading-[1.7] text-[#eef2ee]/65'>
              {t('home.footer.description')}
            </p>
          </div>

          {footerColumns.map((column) => (
            <div key={column.titleKey}>
              <p className='mb-[22px] text-[12px] font-medium text-[#eef2ee]/55'>{t(column.titleKey)}</p>
              <ul className='flex flex-col gap-[16px]'>
                {column.links.map((link) => (
                  <li key={link.labelKey}>
                    <NavTarget
                      link={link}
                      className='text-[15px] font-medium text-[#eef2ee]/85 transition-colors hover:text-white'
                    >
                      {t(link.labelKey)}
                    </NavTarget>
                  </li>
                ))}
              </ul>
            </div>
          ))}
        </div>

        <div className='mt-[50px] h-px w-full bg-[#eef2ee]/10 md:mt-[70px]' />

        <div className='mt-[24px] flex flex-col items-center justify-between gap-[16px] md:flex-row'>
          <p className='text-[13px] text-[#eef2ee]/50'>
            {t('home.footer.copyright', { year, siteName })}
          </p>
          <div className='flex items-center gap-[12px]'>
            {[
              { label: 'Telegram', icon: <Send className='h-[15px] w-[15px]' /> },
              { label: 'X', icon: <span className='text-[15px] font-semibold'>X</span> },
              { label: 'Email', icon: <Mail className='h-[15px] w-[15px]' /> },
            ].map(({ label, icon }) => (
              <a
                key={label}
                href='#'
                aria-label={label}
                className='flex h-[34px] w-[34px] items-center justify-center rounded-full bg-[#eef2ee]/10 text-[#eef2ee]/85 transition-colors hover:bg-[#eef2ee]/20 hover:text-white'
              >
                {icon}
              </a>
            ))}
          </div>
        </div>
      </div>
    </footer>
  )
}

function LandingPage({
  docsUrl,
  isAuthenticated,
  siteName,
}: {
  docsUrl: string
  isAuthenticated: boolean
  siteName: string
}) {
  return (
    <>
      <div className='landing-home-root landing-home-shell'>
        <ScrollProgress />
        <LandingNavbar docsUrl={docsUrl} isAuthenticated={isAuthenticated} />
        <main id='top'>
          <div className='flex min-h-svh flex-col'>
            <HeroSection docsUrl={docsUrl} isAuthenticated={isAuthenticated} />
            <ProviderMarquee />
          </div>
          <GlanceSection />
          <FeaturesSection />
          <MapCtaSection />
          <WhyChooseSection />
          <CtaSection docsUrl={docsUrl} isAuthenticated={isAuthenticated} />
        </main>
        <LandingFooter docsUrl={docsUrl} siteName={siteName} />
      </div>
    </>
  )
}

export function Home() {
  const { auth } = useAuthStore()
  const { status } = useStatus()
  const { systemName } = useSystemConfig()
  const { content, isLoaded, isUrl } = useHomePageContent()
  const { t } = useTranslation()

  const docsUrl = (status?.docs_link as string | undefined) || 'https://docs.newapi.pro'
  const isAuthenticated = !!auth.user

  useLayoutEffect(() => {
    document.body.classList.add('landing-home-page')
    document.body.classList.add('landing-home-page--nexaxis')
    document.documentElement.classList.add('landing-home-page')
    document.documentElement.classList.add('landing-home-page--nexaxis')
    return () => {
      document.body.classList.remove('landing-home-page')
      document.body.classList.remove('landing-home-page--nexaxis')
      document.documentElement.classList.remove('landing-home-page')
      document.documentElement.classList.remove('landing-home-page--nexaxis')
    }
  }, [])

  if (!isLoaded) {
    return (
      <main className='landing-home-root landing-home-shell flex min-h-screen items-center justify-center bg-[#fbfbf9]'>
        <div className='text-[#5b6b62]'>{t('Loading...')}</div>
      </main>
    )
  }

  if (content) {
    return (
      <main className='landing-home-root landing-home-shell overflow-x-hidden'>
        {isUrl ? (
          <iframe
            src={content}
            className='h-screen w-full border-none'
            title={t('home.customPage.title')}
          />
        ) : (
          <div className='landing-home-content container mx-auto py-8'>
            <Markdown className='custom-home-content'>{content}</Markdown>
          </div>
        )}
      </main>
    )
  }

  return <LandingPage docsUrl={docsUrl} isAuthenticated={isAuthenticated} siteName={systemName || 'N123'} />
}


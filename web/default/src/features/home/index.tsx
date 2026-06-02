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
import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import {
  INTERFACE_LANGUAGE_OPTIONS,
  normalizeInterfaceLanguage,
} from '@/i18n/languages'
import { ArrowRight, ChevronDown, ChevronRight, Menu, X } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import heroOrbitImage from '@/assets/home/Ellipse 6.png'
import homeOrbitDot from '@/assets/home/Frame 37.png'
import featureGlobalAccess from '@/assets/home/Global Model Access.png'
import featureSecureReliable from '@/assets/home/Secure & Reliable.png'
import featureStableFast from '@/assets/home/Stable & Fast.png'
import routeLineOne from '@/assets/home/Vector 1.png'
import routeLineTwo from '@/assets/home/Vector 2.png'
import routeLineFour from '@/assets/home/Vector 4.png'
import routeLineFive from '@/assets/home/Vector 5.png'
import routeLineSix from '@/assets/home/Vector 6.png'
import routeLineSeven from '@/assets/home/Vector 7.png'
import homeSearchIcon from '@/assets/home/home_icon_search.png'
import homeMapBg from '@/assets/home/home_mapbg.png'
import statModelsIcon from '@/assets/home/home_page01_icon_01.png'
import statRegionsIcon from '@/assets/home/home_page01_icon_02.png'
import statUptimeIcon from '@/assets/home/home_page01_icon_03.png'
import statDevelopersIcon from '@/assets/home/home_page01_icon_04.png'
import heroGlobeImage from '@/assets/home/image 6.png'
import { useStatus } from '@/hooks/use-status'
import { Markdown } from '@/components/ui/markdown'
import { useHomePageContent } from './hooks'

const EMBEDDED_INITIAL_PROMPT_KEY = '__NEW_API_NEXTCHAT_INITIAL_PROMPT__'
const HOME_PRIMARY_LOGO = '/logo.png'
const HOME_ACCENT_LOGO = '/logo1.png'

const setEmbeddedInitialPrompt = (prompt: string) => {
  if (typeof window === 'undefined') return
  ;(window as unknown as Record<string, string>)[EMBEDDED_INITIAL_PROMPT_KEY] =
    prompt.trim()
}

const routeLines = [
  {
    src: routeLineOne,
    className: 'figma-home-route-1',
    direction: 'figma-home-route-left-to-right',
  },
  {
    src: routeLineTwo,
    className: 'figma-home-route-2',
    direction: 'figma-home-route-left-to-right',
  },
  {
    src: routeLineFour,
    className: 'figma-home-route-4',
    direction: 'figma-home-route-left-to-right',
  },
  {
    src: routeLineSix,
    className: 'figma-home-route-6',
    direction: 'figma-home-route-right-to-left',
  },
  {
    src: routeLineFive,
    className: 'figma-home-route-5',
    direction: 'figma-home-route-left-to-right',
  },
  {
    src: routeLineSeven,
    className: 'figma-home-route-7',
    direction: 'figma-home-route-left-to-right',
  },
]

const mapDots = [
  'figma-home-map-dot-1',
  'figma-home-map-dot-2',
  'figma-home-map-dot-3',
  'figma-home-map-dot-4',
  'figma-home-map-dot-5',
  'figma-home-map-dot-6',
  'figma-home-map-dot-7',
  'figma-home-map-dot-8',
]

const featureCards = [
  {
    title: '全球模型接入',
    description:
      '在一个统一平台中接入 OpenAI、Anthropic、Google、Meta 及更多顶级模型。',
    icon: featureGlobalAccess,
  },
  {
    title: '稳定高速',
    description: '全球多节点 AI 路由网络，提供极速响应与 99.9% 可用性保障。',
    icon: featureStableFast,
  },
  {
    title: '安全可靠',
    description: '企业级安全能力，支持端到端加密、零数据留存，并满足合规要求。',
    icon: featureSecureReliable,
  },
]

const stats = [
  { value: '200+', label: '可接入模型', icon: statModelsIcon },
  { value: '50+', label: '覆盖国家 / 地区', icon: statRegionsIcon },
  { value: '99.9%', label: '可用性保障', icon: statUptimeIcon },
  { value: '100K+', label: '开发者信赖', icon: statDevelopersIcon },
]

type FigmaHomeNavChild = {
  label?: string
  to?: string
  target?: '_self' | '_blank'
  hidden?: boolean
  key?: string
  fullLabel?: string
  active?: boolean
}

type NavItem = {
  label: string
  to?: string
  target?: '_self' | '_blank'
  dropdown?: boolean
  value?: string
  children?: FigmaHomeNavChild[]
}

const figmaHomeNavItems: NavItem[] = [
  {
    label: 'LLM服务',
    to: '/playground',
    dropdown: true,
    children: [
      { label: '聊天', to: '/playground' },
      { label: '绘图', to: 'https://nano.nexaxis.ai/textCreate/' ,target: '_blank'},
      // { label: '视频', to: '/console/chat?tool=video' },
    ],
  },
  { label: '控制台', to: '/dashboard' },
  { label: '模型广场', to: '/pricing' },
]

const HOME_DASHBOARD_PATH = '/dashboard'
const HOME_PLAYGROUND_PATH = '/playground'
const HOME_BLOG_URL = 'https://github.com/QuantumNous/new-api'

const resolvedFigmaHomeNavItems: NavItem[] = figmaHomeNavItems.map((item) => {
  // if (item.to === '/console/chat?tool=chat') {
  //   return {
  //     ...item,
  //     to: HOME_PLAYGROUND_PATH,
  //     children: item.children?.map((child) => {
  //       if (
  //         child.to === '/console/chat?tool=chat' ||
  //         child.to === '/chat/image' ||
  //         child.to === '/console/chat?tool=video'
  //       ) {
  //         return {
  //           ...child,
  //           to: HOME_PLAYGROUND_PATH,
  //         }
  //       }
  //       return child
  //     }),
  //   }
  // }

  // if (item.to === '/console') {
  //   return {
  //     ...item,
  //     to: HOME_DASHBOARD_PATH,
  //   }
  // }

  // if (item.to === '/articles') {
  //   return {
  //     ...item,
  //     to: HOME_BLOG_URL,
  //     target: '_blank',
  //   }
  // }

  return item
})

const getVisibleChildren = (children: NavItem['children'] = []) =>
  children.filter((child) => !child.hidden)

type FigmaFooterLink = {
  id: string
  labelKey: string
  url: string
  target?: '_self' | '_blank'
}

type FigmaFooterGroup = {
  id: string
  titleKey: string
  links: FigmaFooterLink[]
}

type FigmaFooterConfig = {
  version: 1
  groups: FigmaFooterGroup[]
}

const DEFAULT_FIGMA_FOOTER_CONFIG: FigmaFooterConfig = {
  version: 1,
  groups: [
    {
      id: 'about',
      titleKey: '关于',
      links: [
        {
          id: 'about-com',
          labelKey: '关于项目',
          url: 'https://docs.nexaxis.ai/docs',
          target: '_blank',
        },
      ],
    },
    {
      id: 'work',
      titleKey: '文档',
      links: [
        {
          id: 'browse-models',
          labelKey: 'API 文档',
          url: 'https://docs.nexaxis.ai/docs/models-list',
          target: '_blank',
        },
        {
          id: 'how-it-works',
          labelKey: '帮助',
          url: 'https://docs.nexaxis.ai/docs/cc-switch',
          target: '_blank',
        },
      ],
    },
    {
      id: 'socials',
      titleKey: '社交',
      links: [
        {
          id: 'twitter-x',
          labelKey: 'Twitter / X',
          url: 'https://x.com/NexaxisAI',
          target: '_blank',
        },
        {
          id: 'telegram',
          labelKey: 'Telegram',
          url: 'https://t.me/nexaxis',
          target: '_blank',
        },
      ],
    },
    {
      id: 'legal',
      titleKey: '法律',
      links: [
        {
          id: 'privacy-policy',
          labelKey: '隐私政策',
          url: '/privacy-policy',
          target: '_self',
        },
      ],
    },
  ],
}

const createFooterId = (prefix: string, index: number) =>
  `${prefix}-${index + 1}`

function isPresent<T>(value: T | null | undefined): value is T {
  return value != null
}

function normalizeFigmaFooterConfig(config: unknown): FigmaFooterConfig | null {
  if (!config || typeof config !== 'object' || Array.isArray(config))
    return null

  const input = config as {
    groups?: unknown
  }
  const groups = Array.isArray(input.groups)
    ? input.groups
        .map((group, groupIndex) => {
          if (!group || typeof group !== 'object' || Array.isArray(group)) {
            return null
          }

          const groupInput = group as {
            id?: unknown
            titleKey?: unknown
            title?: unknown
            links?: unknown
          }
          const titleKey = String(
            groupInput.titleKey ?? groupInput.title ?? ''
          ).trim()
          if (!titleKey) return null

          const links = Array.isArray(groupInput.links)
            ? groupInput.links
                .map((link, linkIndex) => {
                  if (
                    !link ||
                    typeof link !== 'object' ||
                    Array.isArray(link)
                  ) {
                    return null
                  }

                  const linkInput = link as {
                    id?: unknown
                    labelKey?: unknown
                    label?: unknown
                    url?: unknown
                    target?: unknown
                  }
                  const labelKey = String(
                    linkInput.labelKey ?? linkInput.label ?? ''
                  ).trim()
                  const url = String(linkInput.url ?? '').trim()
                  if (!labelKey || !url) return null

                  const rawTarget = String(linkInput.target ?? '_blank').trim()
                  const target = rawTarget === '_self' ? '_self' : '_blank'

                  return {
                    id: String(
                      linkInput.id ?? createFooterId('link', linkIndex)
                    ).trim(),
                    labelKey,
                    url,
                    target,
                  } satisfies FigmaFooterLink
                })
                .filter(isPresent)
            : []

          return {
            id: String(
              groupInput.id ?? createFooterId('group', groupIndex)
            ).trim(),
            titleKey,
            links,
          } satisfies FigmaFooterGroup
        })
        .filter(isPresent)
    : []

  const groupIds = groups.map((group) => group.id).join(',')
  if (groupIds === 'about,docs,related-projects,friendly-links') {
    return DEFAULT_FIGMA_FOOTER_CONFIG
  }

  return {
    version: 1,
    groups,
  }
}

function parseFigmaFooterConfig(rawValue: unknown): FigmaFooterConfig | null {
  if (!rawValue || typeof rawValue !== 'string') return null

  try {
    return normalizeFigmaFooterConfig(JSON.parse(rawValue))
  } catch {
    return null
  }
}

const LogoMark = ({
  className = '',
  src = HOME_PRIMARY_LOGO,
}: {
  className?: string
  src?: string
}) => (
  <span className={`figma-home-logo ${className}`} aria-hidden='true'>
    <img src={src} alt='' />
  </span>
)

const FigmaFooterLogo = ({ className = '' }: { className?: string }) => (
  <LogoMark
    className={`figma-home-footer-logo ${className}`}
    src={HOME_ACCENT_LOGO}
  />
)

function FigmaFooter({
  footerConfig,
  copyrightText = 'Copyright ©2026 Nexaxis. All rights reserved.',
  bottomExtra = null,
}: {
  footerConfig?: FigmaFooterConfig | null
  copyrightText?: string
  bottomExtra?: React.ReactNode
}) {
  const { t } = useTranslation()
  const groups = Array.isArray(footerConfig?.groups) ? footerConfig.groups : []

  return (
    <footer className='figma-home-footer'>
      <div className='figma-home-footer-inner'>
        <FigmaFooterLogo />

        <div className='figma-home-footer-columns'>
          {groups.map((group) => (
            <div key={group.id} className='figma-home-footer-column'>
              <h3>{t(group.titleKey)}</h3>
              {group.links.map((link) => {
                const label = t(link.labelKey)
                const isInternal =
                  link.target === '_self' && link.url.startsWith('/')

                return (
                  <a
                    key={link.id}
                    href={link.url}
                    target={isInternal ? undefined : link.target || '_blank'}
                    rel={
                      !isInternal && link.target === '_blank'
                        ? 'noopener noreferrer'
                        : undefined
                    }
                  >
                    {label}
                  </a>
                )
              })}
            </div>
          ))}
        </div>

        <div className='figma-home-footer-bottom'>
          <p className='figma-home-copyright'>{copyrightText}</p>
          {bottomExtra ? (
            <div className='figma-home-footer-extra'>{bottomExtra}</div>
          ) : null}
        </div>
      </div>
    </footer>
  )
}

const HeroGlobe = () => (
  <div className='figma-home-globe' aria-hidden='true'>
    <img className='figma-home-globe-main' src={heroGlobeImage} alt='' />
    <img className='figma-home-globe-orbit' src={heroOrbitImage} alt='' />
    <img
      className='figma-home-globe-dot figma-home-globe-dot-east'
      src={homeOrbitDot}
      alt=''
    />
    <img
      className='figma-home-globe-dot figma-home-globe-dot-west'
      src={homeOrbitDot}
      alt=''
    />
    <img
      className='figma-home-globe-dot figma-home-globe-dot-southwest'
      src={homeOrbitDot}
      alt=''
    />
    <img
      className='figma-home-globe-dot figma-home-globe-dot-northeast'
      src={homeOrbitDot}
      alt=''
    />
  </div>
)

const GlobalMap = () => (
  <div className='figma-home-map' aria-hidden='true'>
    <img src={homeMapBg} alt='' />
  </div>
)

function TypewriterTitle({ text }: { text: string }) {
  const characters = useMemo(() => Array.from(text), [text])
  const [visibleLength, setVisibleLength] = useState(0)
  const [isDeleting, setIsDeleting] = useState(false)

  useEffect(() => {
    setVisibleLength(0)
    setIsDeleting(false)
  }, [text])

  useEffect(() => {
    if (!characters.length) return undefined

    const prefersReducedMotion =
      typeof window !== 'undefined' &&
      window.matchMedia?.('(prefers-reduced-motion: reduce)').matches

    if (prefersReducedMotion) {
      setVisibleLength(characters.length)
      return undefined
    }

    let delay = isDeleting ? 45 : 90

    if (!isDeleting && visibleLength === characters.length) {
      delay = 1400
    } else if (isDeleting && visibleLength === 0) {
      delay = 500
    }

    const timer = window.setTimeout(() => {
      if (!isDeleting && visibleLength === characters.length) {
        setIsDeleting(true)
        return
      }

      if (isDeleting && visibleLength === 0) {
        setIsDeleting(false)
        return
      }

      setVisibleLength((currentLength) =>
        isDeleting
          ? Math.max(currentLength - 1, 0)
          : Math.min(currentLength + 1, characters.length)
      )
    }, delay)

    return () => window.clearTimeout(timer)
  }, [characters, isDeleting, visibleLength])

  return (
    <span className='figma-home-typewriter' aria-hidden='true'>
      <span className='figma-home-typewriter-text'>
        {characters.slice(0, visibleLength).join('')}
      </span>
    </span>
  )
}

function FigmaHomeHeader() {
  const { t, i18n } = useTranslation()
  const [mobileMenuOpen, setMobileMenuOpen] = useState(false)
  const [mobileExpandedMenu, setMobileExpandedMenu] = useState<string | null>(
    null
  )
  const getStartedPath = HOME_DASHBOARD_PATH

  const languageOptions = useMemo(
    () =>
      INTERFACE_LANGUAGE_OPTIONS.map((item) => ({
        key: item.code,
        fullLabel: item.label,
        shortLabel:
          item.code === 'zh'
            ? '中文'
            : item.code === 'ja'
              ? '日本語'
              : item.code.toUpperCase(),
      })),
    []
  )

  const currentLanguage =
    languageOptions.find(
      (item) =>
        normalizeInterfaceLanguage(item.key) ===
        normalizeInterfaceLanguage(i18n.language)
    ) || languageOptions[0]

  const handleLanguageSelect = useCallback(
    (languageKey: string) => {
      const nextLanguage = normalizeInterfaceLanguage(languageKey)
      setMobileMenuOpen(false)
      setMobileExpandedMenu(null)
      i18n.changeLanguage(nextLanguage)
      localStorage.setItem('i18nextLng', nextLanguage)
    },
    [i18n]
  )

  const mobileMenuItems: NavItem[] = useMemo(
    () => [
      ...resolvedFigmaHomeNavItems,
      {
        label: '语言',
        value: currentLanguage.shortLabel,
        children: languageOptions.map((item) => ({
          ...item,
          active:
            normalizeInterfaceLanguage(item.key) ===
            normalizeInterfaceLanguage(i18n.language),
        })),
      },
    ],
    [currentLanguage.shortLabel, i18n.language, languageOptions]
  )

  const closeMobileMenu = () => {
    setMobileMenuOpen(false)
    setMobileExpandedMenu(null)
  }

  return (
    <>
      <header className='figma-home-header'>
        <a href='/' className='figma-home-brand' aria-label={t('首页')}>
          <LogoMark />
        </a>

        <a href={HOME_DASHBOARD_PATH} className='figma-home-mobile-console'>
          {t('控制台')}
        </a>

        <nav className='figma-home-nav' aria-label={t('主导航')}>
          {resolvedFigmaHomeNavItems.map((item) => {
            const visibleChildren = getVisibleChildren(item.children)
            const hasDesktopDropdown = item.dropdown || visibleChildren.length

            return (
              <div
                key={item.label}
                className={
                  hasDesktopDropdown
                    ? 'figma-home-nav-item has-dropdown'
                    : 'figma-home-nav-item'
                }
              >
                {hasDesktopDropdown ? (
                  <>
                    <button type='button' className='figma-home-nav-trigger'>
                      {t(item.label)}
                      <ChevronDown size={14} />
                    </button>
                    <div className='figma-home-nav-menu'>
                      {visibleChildren.map((child) => (
                        <a
                          key={child.label}
                          href={child.to}
                          target={child.target}
                          rel={
                            child.target === '_blank'
                              ? 'noopener noreferrer'
                              : undefined
                          }
                        >
                          {t(child.label || '')}
                        </a>
                      ))}
                    </div>
                  </>
                ) : (
                  <a
                    href={item.to}
                    target={item.target}
                    rel={
                      item.target === '_blank'
                        ? 'noopener noreferrer'
                        : undefined
                    }
                  >
                    {t(item.label)}
                  </a>
                )}
              </div>
            )
          })}
        </nav>

        <div className='figma-home-actions'>
          <div className='figma-home-language'>
            <button type='button'>
              {currentLanguage.shortLabel}
              <ChevronDown size={14} />
            </button>
            <div className='figma-home-language-menu'>
              {languageOptions.map((item) => (
                <button
                  key={item.key}
                  type='button'
                  onClick={() => handleLanguageSelect(item.key)}
                >
                  {item.fullLabel}
                </button>
              ))}
            </div>
          </div>
          <a href={getStartedPath} className='figma-home-header-cta'>
            {t('开始使用')}
          </a>
          <button
            className='figma-home-menu-button'
            type='button'
            aria-label={t('切换菜单')}
            aria-expanded={mobileMenuOpen}
            onClick={() => setMobileMenuOpen((open) => !open)}
          >
            {mobileMenuOpen ? <X size={18} /> : <Menu size={18} />}
          </button>
        </div>
      </header>

      <div
        className={`figma-home-mobile-panel${mobileMenuOpen ? 'is-open' : ''}`}
      >
        <div className='figma-home-mobile-panel-top'>
          <img
            className='figma-home-mobile-logo-image'
            src={HOME_PRIMARY_LOGO}
            alt=''
          />
          <button
            type='button'
            className='figma-home-mobile-close'
            aria-label={t('关闭菜单')}
            onClick={closeMobileMenu}
          >
            <X size={28} strokeWidth={1.8} />
          </button>
        </div>

        <div className='figma-home-mobile-links'>
          {mobileMenuItems.map((item) => {
            const isExpanded = mobileExpandedMenu === item.label
            const visibleChildren = getVisibleChildren(item.children)
            if (item.to && !visibleChildren.length) {
              return (
                <div key={item.label} className='figma-home-mobile-menu-item'>
                  <a
                    href={item.to}
                    target={item.target}
                    rel={
                      item.target === '_blank'
                        ? 'noopener noreferrer'
                        : undefined
                    }
                    className='figma-home-mobile-link'
                    onClick={closeMobileMenu}
                  >
                    <span>{t(item.label)}</span>
                  </a>
                </div>
              )
            }

            return (
              <div
                key={item.label}
                className={`figma-home-mobile-menu-item${
                  isExpanded ? 'is-expanded' : ''
                }`}
              >
                <button
                  type='button'
                  className='figma-home-mobile-link'
                  aria-expanded={isExpanded}
                  onClick={() =>
                    setMobileExpandedMenu((current) =>
                      current === item.label ? null : item.label
                    )
                  }
                >
                  <span>{t(item.label)}</span>
                  <span className='figma-home-mobile-link-meta'>
                    {item.value ? <span>{item.value}</span> : null}
                    <ChevronDown size={24} strokeWidth={2} />
                  </span>
                </button>

                {isExpanded ? (
                  <div className='figma-home-mobile-submenu'>
                    {visibleChildren.map((child) =>
                      child.to ? (
                        <a
                          key={child.label}
                          href={child.to}
                          target={child.target}
                          rel={
                            child.target === '_blank'
                              ? 'noopener noreferrer'
                              : undefined
                          }
                          onClick={closeMobileMenu}
                        >
                          {t(child.label || '')}
                        </a>
                      ) : (
                        <button
                          key={child.key}
                          type='button'
                          className={child.active ? 'is-active' : ''}
                          onClick={() => handleLanguageSelect(child.key || '')}
                        >
                          {child.fullLabel}
                        </button>
                      )
                    )}
                  </div>
                ) : null}
              </div>
            )
          })}
        </div>
      </div>
    </>
  )
}

export function Home() {
  const { t, i18n } = useTranslation()
  const { status } = useStatus()
  const { content, isLoaded, isUrl } = useHomePageContent()
  const [heroPrompt, setHeroPrompt] = useState('')
  const [isRoutingActive, setIsRoutingActive] = useState(false)
  const routingSectionRef = useRef<HTMLElement | null>(null)
  const heroSearchRef = useRef<HTMLDivElement | null>(null)
  const heroSearchInputRef = useRef<HTMLInputElement | null>(null)

  const getStartedPath = HOME_DASHBOARD_PATH
  const heroTitle = t('一个 API 接入所有 LLM')

  const promoEnabled = status?.home_promo_enabled !== false
  const promoTextZh = status?.home_promo_text_zh as string | undefined
  const promoTextEn = status?.home_promo_text_en as string | undefined
  const isEnglish = (i18n.language || '').toLowerCase().startsWith('en')
  const fallbackText = '限时，1:1 充值赠送，最高可获 {{$100}} 免费额度！'
  const promoTextRaw = isEnglish
    ? promoTextEn || promoTextZh || fallbackText
    : promoTextZh || promoTextEn || fallbackText
  const promoLink =
    (status?.home_promo_link as string | undefined) || getStartedPath
  const promoIsExternal = /^https?:\/\//i.test(promoLink)

  const promoSegments = useMemo(() => {
    if (!promoTextRaw) return []
    const segments: Array<{ highlight: boolean; text: string }> = []
    const regex = /\{\{([\s\S]+?)\}\}/g
    let lastIndex = 0
    let match: RegExpExecArray | null
    while ((match = regex.exec(promoTextRaw)) !== null) {
      if (match.index > lastIndex) {
        segments.push({
          highlight: false,
          text: promoTextRaw.slice(lastIndex, match.index),
        })
      }
      segments.push({ highlight: true, text: match[1] })
      lastIndex = regex.lastIndex
    }
    if (lastIndex < promoTextRaw.length) {
      segments.push({
        highlight: false,
        text: promoTextRaw.slice(lastIndex),
      })
    }
    return segments
  }, [promoTextRaw])

  const showPromo = promoEnabled && promoTextRaw && promoTextRaw.trim() !== ''
  const footerConfig = useMemo(
    () =>
      parseFigmaFooterConfig(status?.footer_html) ||
      parseFigmaFooterConfig(localStorage.getItem('footer_html')) ||
      DEFAULT_FIGMA_FOOTER_CONFIG,
    [status?.footer_html]
  )

  useEffect(() => {
    if (!isLoaded || content !== '') {
      return undefined
    }

    const handleOutsidePointerDown = (event: PointerEvent) => {
      const searchContainer = heroSearchRef.current
      const searchInput = heroSearchInputRef.current

      if (
        !searchContainer ||
        !searchInput ||
        document.activeElement !== searchInput ||
        searchContainer.contains(event.target as Node)
      ) {
        return
      }

      searchInput.blur()
    }

    document.addEventListener('pointerdown', handleOutsidePointerDown, true)

    return () => {
      document.removeEventListener(
        'pointerdown',
        handleOutsidePointerDown,
        true
      )
    }
  }, [content, isLoaded])

  useEffect(() => {
    if (!isLoaded || content !== '') {
      return undefined
    }

    const routingSection = routingSectionRef.current
    if (!routingSection) return undefined

    let triggered = false
    const activate = () => {
      if (triggered) return
      triggered = true
      setIsRoutingActive(true)
    }

    const isInView = () => {
      const rect = routingSection.getBoundingClientRect()
      const viewportH =
        window.innerHeight || document.documentElement.clientHeight
      return rect.top < viewportH - 80 && rect.bottom > 0
    }

    const observer = new IntersectionObserver(
      ([entry]) => {
        if (entry.isIntersecting) {
          activate()
          observer.disconnect()
        }
      },
      {
        root: null,
        threshold: 0.05,
        rootMargin: '0px 0px -10% 0px',
      }
    )
    observer.observe(routingSection)

    const scrollContainer = document.querySelector('.app-layout-scroll')
    const scrollTargets = [window, scrollContainer].filter(Boolean) as Array<
      Window | Element
    >

    const handleScroll = () => {
      if (isInView()) {
        activate()
        scrollTargets.forEach((target) =>
          target.removeEventListener('scroll', handleScroll)
        )
        window.removeEventListener('resize', handleScroll)
        observer.disconnect()
      }
    }

    scrollTargets.forEach((target) =>
      target.addEventListener('scroll', handleScroll, { passive: true })
    )
    window.addEventListener('resize', handleScroll)
    handleScroll()

    return () => {
      observer.disconnect()
      scrollTargets.forEach((target) =>
        target.removeEventListener('scroll', handleScroll)
      )
      window.removeEventListener('resize', handleScroll)
    }
  }, [content, isLoaded])

  const handleHeroSearchSubmit = (
    event?: React.FormEvent | React.MouseEvent
  ) => {
    event?.preventDefault()
    const prompt = heroPrompt.trim()
    const chatPath = HOME_PLAYGROUND_PATH

    if (prompt) {
      setEmbeddedInitialPrompt(prompt)
    }

    window.location.href = chatPath
  }

  const handleHeroSearchKeyDown = (
    event: React.KeyboardEvent<HTMLInputElement>
  ) => {
    if (event.key !== 'Enter') return
    handleHeroSearchSubmit(event)
  }

  if (!isLoaded) {
    return (
      <div className='home-logo-loading flex min-h-screen items-center justify-center'>
        <LogoMark />
      </div>
    )
  }

  if (content !== '') {
    return isUrl ? (
      <iframe
        src={content}
        title='Home Page Content'
        className='figma-home-custom-frame'
      />
    ) : (
      <div className='figma-home-custom-content markdown-body'>
        <Markdown>{content}</Markdown>
      </div>
    )
  }

  return (
    <main className='figma-home'>
      <FigmaHomeHeader />

      <section className='figma-home-hero'>
        {showPromo &&
          (promoIsExternal ? (
            <a
              href={promoLink}
              className='figma-home-promo'
              target='_blank'
              rel='noopener noreferrer'
            >
              <span className='figma-home-promo-text'>
                {promoSegments.map((seg, idx) =>
                  seg.highlight ? (
                    <span key={idx} className='figma-home-promo-highlight'>
                      {seg.text}
                    </span>
                  ) : (
                    <React.Fragment key={idx}>{seg.text}</React.Fragment>
                  )
                )}
              </span>
              <ChevronRight size={24} />
            </a>
          ) : (
            <a href={promoLink} className='figma-home-promo'>
              <span className='figma-home-promo-text'>
                {promoSegments.map((seg, idx) =>
                  seg.highlight ? (
                    <span key={idx} className='figma-home-promo-highlight'>
                      {seg.text}
                    </span>
                  ) : (
                    <React.Fragment key={idx}>{seg.text}</React.Fragment>
                  )
                )}
              </span>
              <ChevronRight size={24} />
            </a>
          ))}

        <h1 className='figma-home-hero-title' aria-label={heroTitle}>
          <TypewriterTitle text={heroTitle} />
          <span className='figma-home-typewriter-measure' aria-hidden='true'>
            {heroTitle}
          </span>
        </h1>

        <div className='figma-home-hero-art' aria-hidden='true'>
          <HeroGlobe />
          <LogoMark className='figma-home-hero-logo' />
        </div>

        <div ref={heroSearchRef} className='figma-home-search' role='search'>
          <img className='figma-home-search-icon' src={homeSearchIcon} alt='' />
          <input
            ref={heroSearchInputRef}
            value={heroPrompt}
            onChange={(event) => setHeroPrompt(event.target.value)}
            onKeyDown={handleHeroSearchKeyDown}
            placeholder={t('你想了解什么？')}
          />
          <button
            type='button'
            aria-label={t('开始对话')}
            onClick={handleHeroSearchSubmit}
          >
            <LogoMark />
          </button>
        </div>
      </section>

      <section className='figma-home-feature-section'>
        <div className='figma-home-feature-grid'>
          {featureCards.map((card) => (
            <article key={card.title} className='figma-home-feature-card'>
              <div className='figma-home-feature-icon'>
                <img src={card.icon} alt='' />
              </div>
              <h2>{t(card.title)}</h2>
              <p>{t(card.description)}</p>
            </article>
          ))}
        </div>

        <div className='figma-home-stats'>
          {stats.map((stat) => (
            <div key={stat.label} className='figma-home-stat'>
              <img src={stat.icon} alt='' />
              <div>
                <strong>{stat.value}</strong>
                <span>{t(stat.label)}</span>
              </div>
            </div>
          ))}
        </div>
      </section>

      <section
        ref={routingSectionRef}
        className={`figma-home-routing${
          isRoutingActive ? ' is-route-active' : ''
        }`}
      >
        <div className='figma-home-routing-header'>
          <h2>{t('智能路由，全球覆盖')}</h2>
          <p>
            {t('AI 驱动的路由会自动选择最优路径，降低延迟并提升你的体验。')}
          </p>
        </div>

        <div className='figma-home-map-wrap'>
          <GlobalMap />
          <div className='figma-home-map-routes' aria-hidden='true'>
            {routeLines.map((line) => (
              <img
                key={line.className}
                className={`${line.className} ${line.direction}`}
                src={line.src}
                alt=''
              />
            ))}
          </div>
          {mapDots.map((dotClass) => (
            <span key={dotClass} className={`figma-home-map-dot ${dotClass}`} />
          ))}
          <div className='figma-home-region figma-home-region-us'>
            <strong>{t('美国西部')}</strong>
            <span>120ms</span>
          </div>
          <div className='figma-home-region figma-home-region-sa'>
            <strong>{t('南美洲')}</strong>
            <span>150ms</span>
          </div>
          <div className='figma-home-region figma-home-region-ap'>
            <strong>{t('亚太地区')}</strong>
            <span>60ms</span>
          </div>
          <LogoMark className='figma-home-map-logo' />
        </div>

        <a href={getStartedPath} className='figma-home-routing-cta'>
          {t('立即开始')}
          <ArrowRight size={18} />
        </a>
      </section>
      <FigmaFooter footerConfig={footerConfig} />
    </main>
  )
}

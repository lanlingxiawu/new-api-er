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
import React, { useCallback, useEffect, useMemo, useState } from 'react'
import {
  ArrowDown,
  ArrowRight,
  ArrowUpRight,
  Boxes,
  Check,
  ChevronDown,
  ChevronRight,
  Code2,
  Languages,
  Menu,
  X,
  Zap,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import {
  INTERFACE_LANGUAGE_OPTIONS,
  normalizeInterfaceLanguage,
} from '@/i18n/languages'
import { useStatus } from '@/hooks/use-status'
import { useAuthStore } from '@/stores/auth-store'
import { api } from '@/lib/api'
import { cn } from '@/lib/utils'
import { Markdown } from '@/components/ui/markdown'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { useHomePageContent } from './hooks'

// ---- Brand + navigation ----
const BRAND_NAME = '巨量词元'
const BRAND_ROMAN = 'JULIANG CIYUAN'
const HOME_CONSOLE_PATH = '/dashboard'
const HOME_PRICING_PATH = '/pricing'
const HOME_PRIVACY_PATH = '/privacy-policy'
const HOME_DOCS_URL = 'https://docs.juliang.io/docs'
const HOME_ABOUT_URL = 'https://juliang.io'
const HOME_GITHUB_URL = 'https://github.com/QuantumNous/new-api'
const HOME_TWITTER_URL = 'https://x.com/NexaxisAI'
const HOME_DISCORD_URL = 'https://discord.com'
const HOME_SUPPORT_MAIL = 'mailto:support@juliang.io'

const HOME_PROMO_FALLBACK_ZH =
  '限时，1:1 充值赠送，最高可获 {{$100}} 免费额度！'
const HOME_PROMO_FALLBACK_EN =
  'Limited time — 1:1 top-up bonus, up to {{$100}} in free credit!'

// Brand logo asset (served from the public root). One file drives every logo
// surface: header, hero, loading, footer, favicon, and PWA icon.
const BRAND_LOGO_SRC = '/logo.png'

// ---- Footer columns (backend-overridable via status.footer_html) ----
type FooterLink = {
  id?: string
  label: string
  href: string
  internal?: boolean
}

type FooterGroup = {
  id: string
  title: string
  links: FooterLink[]
}

const HOME_FOOTER_GROUPS: FooterGroup[] = [
  {
    id: 'product',
    title: '产品',
    links: [
      { label: '能力', href: '#jl-capabilities' },
      { label: '模型', href: HOME_PRICING_PATH, internal: true },
      { label: '定价', href: HOME_PRICING_PATH, internal: true },
    ],
  },
  {
    id: 'developer',
    title: '开发者',
    links: [
      { label: '文档', href: HOME_DOCS_URL },
      { label: 'API 参考', href: HOME_DOCS_URL },
      { label: '状态', href: HOME_DOCS_URL },
    ],
  },
  {
    id: 'company',
    title: '公司',
    links: [
      { label: '关于我们', href: HOME_ABOUT_URL },
      { label: '更新日志', href: HOME_DOCS_URL },
      { label: '隐私政策', href: HOME_PRIVACY_PATH, internal: true },
    ],
  },
  {
    id: 'contact',
    title: '联系',
    links: [
      { label: 'GitHub', href: HOME_GITHUB_URL },
      { label: 'X (Twitter)', href: HOME_TWITTER_URL },
      { label: 'Discord', href: HOME_DISCORD_URL },
      { label: '邮箱支持', href: HOME_SUPPORT_MAIL },
    ],
  },
]

const capabilityCards = [
  {
    icon: Boxes,
    tone: 'is-violet',
    title: '全球模型统一接入',
    description: '聚合全球主流大模型，一个接口，全部调用。',
  },
  {
    icon: Zap,
    tone: 'is-blue',
    title: '极致性能',
    description: '智能路由，急速均衡，高并发支持，稳定可靠。',
  },
  {
    icon: Code2,
    tone: 'is-teal',
    title: '开发者友好',
    description: '完全兼容 OpenAI API，快速集成，即插即用。',
  },
] as const

// Shared interface-language state for the landing header/drawer. Mirrors the
// in-app LanguageSwitcher: switch immediately (i18next caches to localStorage)
// and best-effort persist to the signed-in user's profile.
function useHomeLanguage() {
  const { i18n } = useTranslation()
  const user = useAuthStore((s) => s.auth.user)
  const currentLanguage = normalizeInterfaceLanguage(i18n.language)

  const changeLanguage = useCallback(
    async (code: string) => {
      if (code === currentLanguage) return
      await i18n.changeLanguage(code)
      if (user) {
        try {
          await api.put('/api/user/self', { language: code })
        } catch {
          // Best-effort persistence; don't block the UI on failure
        }
      }
    },
    [i18n, user, currentLanguage]
  )

  return { currentLanguage, changeLanguage }
}

// Desktop header language menu (replaces the old GitHub link).
function HomeLangSwitcher() {
  const { t } = useTranslation()
  const { currentLanguage, changeLanguage } = useHomeLanguage()
  const [open, setOpen] = useState(false)
  const currentLabel =
    INTERFACE_LANGUAGE_OPTIONS.find((lang) => lang.code === currentLanguage)
      ?.label ?? 'Language'

  // Auto-close on scroll (outside-click and selection already close it).
  useEffect(() => {
    if (!open) return
    const close = () => setOpen(false)
    window.addEventListener('scroll', close, { passive: true })
    return () => window.removeEventListener('scroll', close)
  }, [open])

  return (
    <DropdownMenu modal={false} open={open} onOpenChange={setOpen}>
      <DropdownMenuTrigger
        render={
          <button
            type='button'
            className='jl-lang'
            aria-label={t('Change language')}
          />
        }
      >
        <Languages size={16} />
        <span>{currentLabel}</span>
        <ChevronDown size={14} className='jl-lang-caret' />
      </DropdownMenuTrigger>
      <DropdownMenuContent
        align='end'
        sideOffset={10}
        className='w-auto min-w-44 p-1.5'
      >
        {INTERFACE_LANGUAGE_OPTIONS.map((lang) => (
          <DropdownMenuItem
            key={lang.code}
            onClick={() => changeLanguage(lang.code)}
            className={cn(
              'justify-between gap-8 px-2.5 py-1.5',
              currentLanguage === lang.code && 'text-primary font-medium'
            )}
          >
            {lang.label}
            <Check
              size={14}
              className={cn(
                'text-primary',
                currentLanguage !== lang.code && 'invisible'
              )}
            />
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

// Parse the backend footer config (best-effort) into normalized groups.
function parseFooterGroups(raw: unknown): FooterGroup[] | null {
  if (!raw || typeof raw !== 'string') return null
  let parsed: {
    groups?: Array<Record<string, unknown> | undefined>
  }
  try {
    parsed = JSON.parse(raw)
  } catch {
    return null
  }
  if (!parsed || !Array.isArray(parsed.groups) || !parsed.groups.length) {
    return null
  }
  const groups: FooterGroup[] = []
  for (const rawGroup of parsed.groups) {
    const group = (rawGroup ?? {}) as Record<string, unknown>
    const title = String(group.title ?? group.titleKey ?? '').trim()
    if (!title) continue
    const rawLinks = Array.isArray(group.links) ? group.links : []
    const links: FooterLink[] = []
    for (const rawLink of rawLinks) {
      const link = (rawLink ?? {}) as Record<string, unknown>
      const label = String(link.label ?? link.labelKey ?? '').trim()
      const href = String(link.href ?? link.url ?? '').trim()
      if (!label || !href) continue
      links.push({
        label,
        href,
        internal: link.target === '_self' && href.startsWith('/'),
      })
    }
    groups.push({
      id: String(group.id ?? `group-${groups.length}`),
      title,
      links,
    })
  }
  return groups.length ? groups : null
}

const scrollToCapabilities = (event?: React.MouseEvent) => {
  const target = document.getElementById('jl-capabilities')
  if (!target) return
  event?.preventDefault()
  target.scrollIntoView({ behavior: 'smooth', block: 'start' })
}

function BrandMark() {
  return (
    <>
      <img className='jl-brand-logo' src={BRAND_LOGO_SRC} alt='' aria-hidden='true' />
      <span className='jl-brand-name'>
        <strong>{BRAND_NAME}</strong>
        <span>{BRAND_ROMAN}</span>
      </span>
    </>
  )
}

function HeroArt() {
  return (
    <div className='jl-hero-art jl-reveal' aria-hidden='true'>
      <div className='jl-hero-tilt'>
        <div className='jl-hero-stage'>
          <img className='jl-hero-logo' src={BRAND_LOGO_SRC} alt='' />
        </div>
      </div>
    </div>
  )
}

function FigmaHomeHeader() {
  const { t } = useTranslation()
  const { currentLanguage, changeLanguage } = useHomeLanguage()
  const [scrolled, setScrolled] = useState(false)
  const [drawerOpen, setDrawerOpen] = useState(false)

  useEffect(() => {
    const onScroll = () => setScrolled(window.scrollY > 8)
    onScroll()
    window.addEventListener('scroll', onScroll, { passive: true })
    return () => window.removeEventListener('scroll', onScroll)
  }, [])

  useEffect(() => {
    document.body.style.overflow = drawerOpen ? 'hidden' : ''
    return () => {
      document.body.style.overflow = ''
    }
  }, [drawerOpen])

  const closeDrawer = () => setDrawerOpen(false)

  const navLinks = (
    <>
      <a href='/' className='is-active'>
        {t('首页')}
      </a>
      <a href='#jl-capabilities' onClick={scrollToCapabilities}>
        {t('能力')}
      </a>
      <a href={HOME_DOCS_URL} target='_blank' rel='noopener noreferrer'>
        {t('文档')}
      </a>
      <a href={HOME_ABOUT_URL} target='_blank' rel='noopener noreferrer'>
        {t('关于')}
      </a>
    </>
  )

  return (
    <>
      <header className={`jl-header${scrolled ? ' is-scrolled' : ''}`}>
        <a href='/' className='jl-brand' aria-label={t('首页')}>
          <BrandMark />
        </a>

        <nav className='jl-nav' aria-label={t('主导航')}>
          {navLinks}
        </nav>

        <div className='jl-header-actions'>
          <HomeLangSwitcher />
          <a href={HOME_CONSOLE_PATH} className='jl-btn jl-btn-dark'>
            {t('控制台')}
            <ArrowRight size={16} className='jl-arrow' />
          </a>
          <button
            type='button'
            className='jl-header-menu-btn'
            aria-label={t('切换菜单')}
            aria-expanded={drawerOpen ? 'true' : 'false'}
            onClick={() => setDrawerOpen((open) => !open)}
          >
            <Menu size={20} />
          </button>
        </div>
      </header>

      <div className={`jl-drawer${drawerOpen ? ' is-open' : ''}`}>
        <div className='jl-drawer-top'>
          <span className='jl-brand'>
            <BrandMark />
          </span>
          <button
            type='button'
            className='jl-drawer-close'
            aria-label={t('关闭菜单')}
            onClick={closeDrawer}
          >
            <X size={24} />
          </button>
        </div>
        <nav className='jl-drawer-links' onClick={closeDrawer}>
          {navLinks}
        </nav>
        <div className='jl-drawer-lang' role='group' aria-label={t('Change language')}>
          {INTERFACE_LANGUAGE_OPTIONS.map((lang) => (
            <button
              key={lang.code}
              type='button'
              className={`jl-drawer-lang-btn${
                currentLanguage === lang.code ? ' is-active' : ''
              }`}
              onClick={() => {
                changeLanguage(lang.code)
                closeDrawer()
              }}
            >
              {lang.label}
            </button>
          ))}
        </div>
        <div className='jl-drawer-cta'>
          <a
            href={HOME_CONSOLE_PATH}
            className='jl-btn jl-btn-dark'
            onClick={closeDrawer}
          >
            {t('控制台')}
            <ArrowRight size={16} className='jl-arrow' />
          </a>
        </div>
      </div>
    </>
  )
}

function FigmaFooter({
  groups,
  copyrightText,
}: {
  groups: FooterGroup[]
  copyrightText: string
}) {
  const { t } = useTranslation()

  const renderLink = (link: FooterLink, index: number) => {
    const label = t(link.label)
    const key = link.id ?? `${link.label}-${index}`
    if (link.href.startsWith('#')) {
      return (
        <a
          key={key}
          href={link.href}
          onClick={(event) => {
            const target = document.getElementById(link.href.slice(1))
            if (!target) return
            event.preventDefault()
            target.scrollIntoView({ behavior: 'smooth', block: 'start' })
          }}
        >
          {label}
        </a>
      )
    }
    if (link.internal) {
      return (
        <a key={key} href={link.href}>
          {label}
        </a>
      )
    }
    return (
      <a key={key} href={link.href} target='_blank' rel='noopener noreferrer'>
        {label}
      </a>
    )
  }

  return (
    <footer className='jl-footer'>
      <div className='jl-footer-inner'>
        <div className='jl-footer-top'>
          <div className='jl-footer-brand'>
            <span className='jl-brand'>
              <BrandMark />
            </span>
            <p className='jl-footer-tagline'>
              {t('连接全球 AI，让智能无处不在。')}
            </p>
            <div className='jl-footer-copy'>{copyrightText}</div>
          </div>

          {groups.map((group) => (
            <div key={group.id} className='jl-footer-col'>
              <h4>{t(group.title)}</h4>
              {group.links.map((link, index) => renderLink(link, index))}
            </div>
          ))}
        </div>
      </div>
    </footer>
  )
}

export function Home() {
  const { t, i18n } = useTranslation()
  const { status } = useStatus()
  const { content, isLoaded, isUrl } = useHomePageContent()

  const promoEnabled = status?.home_promo_enabled !== false
  const promoTextZh = status?.home_promo_text_zh as string | undefined
  const promoTextEn = status?.home_promo_text_en as string | undefined
  const isEnglish = normalizeInterfaceLanguage(i18n.language) === 'en'
  const fallbackText = isEnglish ? HOME_PROMO_FALLBACK_EN : HOME_PROMO_FALLBACK_ZH
  const promoTextRaw = isEnglish
    ? promoTextEn || promoTextZh || fallbackText
    : promoTextZh || promoTextEn || fallbackText
  const promoLink =
    (status?.home_promo_link as string | undefined) || HOME_CONSOLE_PATH
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
      segments.push({ highlight: false, text: promoTextRaw.slice(lastIndex) })
    }
    return segments
  }, [promoTextRaw])

  const showPromo = promoEnabled && promoTextRaw && promoTextRaw.trim() !== ''

  const footerGroups = useMemo(
    () =>
      parseFooterGroups(status?.footer_html) ||
      parseFooterGroups(
        typeof window !== 'undefined'
          ? localStorage.getItem('footer_html')
          : null
      ) ||
      HOME_FOOTER_GROUPS,
    [status?.footer_html]
  )

  const copyrightText = `© ${new Date().getFullYear()} ${BRAND_NAME}. All rights reserved.`

  // Scroll-reveal entrance for landing sections.
  useEffect(() => {
    if (!isLoaded || content !== '') return undefined
    const reveals = Array.from(
      document.querySelectorAll<HTMLElement>('.figma-home .jl-reveal')
    )
    if (!reveals.length) return undefined

    const prefersReduced = window.matchMedia?.(
      '(prefers-reduced-motion: reduce)'
    ).matches
    if (prefersReduced || typeof IntersectionObserver === 'undefined') {
      reveals.forEach((el) => el.classList.add('is-visible'))
      return undefined
    }

    const observer = new IntersectionObserver(
      (entries) => {
        entries.forEach((entry) => {
          if (entry.isIntersecting) {
            entry.target.classList.add('is-visible')
            observer.unobserve(entry.target)
          }
        })
      },
      { threshold: 0.14, rootMargin: '0px 0px -8% 0px' }
    )
    reveals.forEach((el) => observer.observe(el))
    return () => observer.disconnect()
  }, [isLoaded, content])

  // Pointer-driven 3D tilt on the hero artwork (desktop, motion-allowed only).
  useEffect(() => {
    if (!isLoaded || content !== '') return undefined
    const hero = document.querySelector<HTMLElement>('.jl-hero')
    const tilt = document.querySelector<HTMLElement>('.jl-hero-tilt')
    if (!hero || !tilt) return undefined

    const finePointer = window.matchMedia?.('(pointer: fine)').matches
    const prefersReduced = window.matchMedia?.(
      '(prefers-reduced-motion: reduce)'
    ).matches
    if (!finePointer || prefersReduced) return undefined

    let frame = 0
    const onMove = (event: PointerEvent) => {
      const rect = hero.getBoundingClientRect()
      const px = (event.clientX - rect.left) / rect.width - 0.5
      const py = (event.clientY - rect.top) / rect.height - 0.5
      if (frame) cancelAnimationFrame(frame)
      frame = requestAnimationFrame(() => {
        tilt.style.setProperty('--jl-ry', `${(px * 16).toFixed(2)}deg`)
        tilt.style.setProperty('--jl-rx', `${(-py * 13).toFixed(2)}deg`)
      })
    }
    const onLeave = () => {
      if (frame) cancelAnimationFrame(frame)
      tilt.style.setProperty('--jl-ry', '0deg')
      tilt.style.setProperty('--jl-rx', '0deg')
    }

    hero.addEventListener('pointermove', onMove)
    hero.addEventListener('pointerleave', onLeave)
    return () => {
      hero.removeEventListener('pointermove', onMove)
      hero.removeEventListener('pointerleave', onLeave)
      if (frame) cancelAnimationFrame(frame)
    }
  }, [isLoaded, content])

  // PPT-style full-screen flip between screen 1 (hero) and screen 2 (capabilities).
  // Everything from screen 2 onward scrolls normally. Desktop wheel, motion-allowed only.
  useEffect(() => {
    if (!isLoaded || content !== '') return undefined
    const finePointer = window.matchMedia?.('(pointer: fine)').matches
    const prefersReduced = window.matchMedia?.(
      '(prefers-reduced-motion: reduce)'
    ).matches
    if (!finePointer || prefersReduced) return undefined

    let animating = false
    let timer = 0
    const animateTo = (top: number) => {
      animating = true
      window.scrollTo({ top, behavior: 'smooth' })
      window.clearTimeout(timer)
      timer = window.setTimeout(() => {
        // Land exactly on target so a short/undershot smooth scroll can't
        // leave us mid-way (which would re-trigger the flip and trap scroll 2).
        window.scrollTo({ top })
        animating = false
      }, 820)
    }

    const onWheel = (event: WheelEvent) => {
      const caps = document.getElementById('jl-capabilities')
      if (!caps) return
      if (animating) {
        event.preventDefault()
        return
      }
      const y = window.scrollY
      // Absolute Y where screen 2 starts (stable ≈ hero height).
      const capsAbsTop = Math.round(y + caps.getBoundingClientRect().top)
      const TOL = 6
      if (event.deltaY > 0 && y < capsAbsTop - TOL) {
        // Screen 1, scrolling down -> flip to screen 2.
        event.preventDefault()
        animateTo(capsAbsTop)
      } else if (event.deltaY < 0 && y > TOL && y < capsAbsTop + TOL) {
        // Near the screen 1/2 boundary, scrolling up -> flip back to screen 1.
        event.preventDefault()
        animateTo(0)
      }
      // Otherwise (screen 2 content and below): let the browser scroll normally.
    }

    window.addEventListener('wheel', onWheel, { passive: false })
    return () => {
      window.removeEventListener('wheel', onWheel)
      window.clearTimeout(timer)
    }
  }, [isLoaded, content])

  if (!isLoaded) {
    return (
      <div className='home-logo-loading'>
        <img className='jl-brand-logo' src={BRAND_LOGO_SRC} alt='' />
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

      <section className='jl-hero'>
        <div className='jl-hero-copy'>
          {showPromo &&
            (promoIsExternal ? (
              <a
                href={promoLink}
                className='jl-promo jl-reveal'
                target='_blank'
                rel='noopener noreferrer'
              >
                <span>
                  {promoSegments.map((seg, idx) =>
                    seg.highlight ? (
                      <span key={idx} className='jl-promo-highlight'>
                        {seg.text}
                      </span>
                    ) : (
                      <React.Fragment key={idx}>{seg.text}</React.Fragment>
                    )
                  )}
                </span>
                <ChevronRight size={16} />
              </a>
            ) : (
              <a href={promoLink} className='jl-promo jl-reveal'>
                <span>
                  {promoSegments.map((seg, idx) =>
                    seg.highlight ? (
                      <span key={idx} className='jl-promo-highlight'>
                        {seg.text}
                      </span>
                    ) : (
                      <React.Fragment key={idx}>{seg.text}</React.Fragment>
                    )
                  )}
                </span>
                <ChevronRight size={16} />
              </a>
            ))}

          <h1 className='jl-hero-title jl-reveal'>
            {BRAND_NAME}
            <br />
            {t('连接全球')} <span className='jl-grad'>AI</span>
          </h1>
          <p className='jl-hero-sub jl-reveal'>
            {t('一个 API，即可调用世界领先的大模型。稳定、快速、无限可能。')}
          </p>
          <div className='jl-hero-cta jl-reveal'>
            <a href={HOME_CONSOLE_PATH} className='jl-btn jl-btn-dark'>
              {t('立即开始')}
              <ArrowRight size={17} className='jl-arrow' />
            </a>
            <a
              href={HOME_DOCS_URL}
              target='_blank'
              rel='noopener noreferrer'
              className='jl-btn jl-btn-ghost'
            >
              {t('查看文档')}
            </a>
          </div>
        </div>

        <HeroArt />

        <button
          type='button'
          className='jl-scroll jl-reveal'
          onClick={scrollToCapabilities}
        >
          <ArrowDown size={15} />
          {t('向下探索')}
        </button>
      </section>

      <section id='jl-capabilities' className='jl-capabilities'>
        <span className='jl-eyebrow jl-reveal'>{t('核心能力')}</span>
        <h2 className='jl-section-title jl-reveal'>
          {t('为开发者打造的 AI 基础设施')}
        </h2>
        <div className='jl-cards'>
          {capabilityCards.map((card) => {
            const Icon = card.icon
            return (
              <article key={card.title} className='jl-card jl-reveal'>
                <span className={`jl-card-icon ${card.tone}`}>
                  <Icon size={26} strokeWidth={2} />
                </span>
                <h3>{t(card.title)}</h3>
                <p>{t(card.description)}</p>
                <span className='jl-card-more'>
                  <ArrowUpRight size={20} />
                </span>
              </article>
            )
          })}
        </div>
      </section>

      <section className='jl-cta-wrap'>
        <div className='jl-cta jl-reveal'>
          <div className='jl-cta-globe' aria-hidden='true' />
          <h2 className='jl-cta-title'>{t('开始连接全球 AI')}</h2>
          <a href={HOME_CONSOLE_PATH} className='jl-btn jl-btn-dark'>
            {t('立即开始')}
            <ArrowRight size={17} className='jl-arrow' />
          </a>
        </div>
      </section>

      <FigmaFooter groups={footerGroups} copyrightText={copyrightText} />
    </main>
  )
}

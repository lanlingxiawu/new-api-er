/*
Copyright (C) 2025 QuantumNous

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

import React from 'react';

// ---- Brand wordmark ----
export const BRAND_NAME = '巨量词元';
export const BRAND_ROMAN = 'JULIANG CIYUAN';

// Brand logo asset (served from the public root). One file drives every logo
// surface: header, hero, loading, footer, favicon, and PWA icon.
export const BRAND_LOGO_SRC = '/logo.png';

// ---- Navigation / link targets ----
// Console is an internal SPA route (classic mounts the app under /console).
// External docs targets mirror the default frontend so both homepages point
// to the same pages.
export const HOME_CONSOLE_PATH = '/console';
export const HOME_PRICING_PATH = '/pricing';
export const HOME_PRIVACY_PATH = '/privacy-policy';
export const HOME_DOCS_URL = 'https://docs.juliang.io/docs/';
export const HOME_DOCS_QUICKSTART_URL = 'https://docs.juliang.io/docs/quickstart/';
export const HOME_DOCS_API_URL = 'https://docs.juliang.io/docs/api-overview/';
export const HOME_DOCS_PRICING_URL = 'https://docs.juliang.io/docs/official-pricing/';
export const HOME_ABOUT_URL = 'https://docs.juliang.io/docs/';
export const HOME_GITHUB_URL = 'https://github.com/QuantumNous/new-api';
export const HOME_TWITTER_URL = 'https://x.com/NexaxisAI';
export const HOME_DISCORD_URL = 'https://discord.com';
export const HOME_SUPPORT_MAIL = 'mailto:support@juliang.io';

/**
 * Self-contained GitHub mark (lucide dropped its brand icons). Keeps both
 * frontends free of an external icon dependency for this glyph.
 */
export const GithubIcon = ({ size = 17 }) => (
  <svg
    width={size}
    height={size}
    viewBox='0 0 24 24'
    fill='currentColor'
    aria-hidden='true'
  >
    <path d='M12 .5C5.73.5.5 5.73.5 12c0 5.08 3.29 9.39 7.86 10.91.58.11.79-.25.79-.56 0-.28-.01-1.02-.02-2-3.2.69-3.88-1.54-3.88-1.54-.52-1.33-1.28-1.69-1.28-1.69-1.05-.72.08-.7.08-.7 1.16.08 1.77 1.19 1.77 1.19 1.03 1.77 2.7 1.26 3.36.96.1-.75.4-1.26.72-1.55-2.55-.29-5.24-1.28-5.24-5.69 0-1.26.45-2.29 1.19-3.1-.12-.29-.52-1.46.11-3.05 0 0 .97-.31 3.18 1.18a11.1 11.1 0 0 1 2.9-.39c.98 0 1.97.13 2.9.39 2.2-1.49 3.17-1.18 3.17-1.18.63 1.59.23 2.76.11 3.05.74.81 1.19 1.84 1.19 3.1 0 4.42-2.69 5.39-5.25 5.68.41.36.78 1.06.78 2.14 0 1.55-.01 2.8-.01 3.18 0 .31.21.68.8.56A11.51 11.51 0 0 0 23.5 12C23.5 5.73 18.27.5 12 .5z' />
  </svg>
)

/**
 * The brand glyph + wordmark. Renders the inner content only; wrap the caller
 * in a `.jl-brand` element (a Link, span, etc.).
 */
export const BrandMark = () => (
  <>
    <img className='jl-brand-logo' src={BRAND_LOGO_SRC} alt='' aria-hidden='true' />
    <span className='jl-brand-name'>
      <strong>{BRAND_NAME}</strong>
      <span>{BRAND_ROMAN}</span>
    </span>
  </>
);

/**
 * Hero artwork — the real brand logo (/logo.png) rendered large, floating over
 * a glass panel with a pointer-driven 3D tilt.
 */
export const HeroArt = () => (
  <div className='jl-hero-art jl-reveal' aria-hidden='true'>
    <div className='jl-hero-tilt'>
      <div className='jl-hero-stage'>
        <img className='jl-hero-logo' src={BRAND_LOGO_SRC} alt='' />
      </div>
    </div>
  </div>
);

/**
 * Default footer columns matching the reference design. Labels are natural
 * (Chinese) i18n keys; the footer component runs them through `t()`.
 */
export const HOME_FOOTER_GROUPS = [
  {
    id: 'product',
    title: '产品',
    links: [
      { label: '能力', href: '#jl-capabilities' },
      { label: '模型', href: HOME_PRICING_PATH, internal: true },
      { label: '定价', href: HOME_DOCS_PRICING_URL },
    ],
  },
  {
    id: 'developer',
    title: '开发者',
    links: [
      { label: '文档', href: HOME_DOCS_URL },
      { label: 'API 参考', href: HOME_DOCS_API_URL },
    ],
  },
  {
    id: 'company',
    title: '公司',
    links: [
      { label: '关于我们', href: HOME_ABOUT_URL },
      { label: '隐私政策', href: HOME_PRIVACY_PATH, internal: true },
    ],
  },
];

// Footer contact methods, admin-configurable via 设置 → 个性化设置 → 联系方式.
// Each field only appears when it has a value. Mirrors the default frontend.
const HOME_CONTACT_DEFS = [
  { key: 'contact_email', label: '邮箱', scheme: 'mailto' },
  { key: 'contact_phone', label: '电话', scheme: 'tel' },
  { key: 'contact_wechat', label: '微信' },
  { key: 'contact_qq', label: 'QQ' },
  { key: 'contact_telegram', label: 'Telegram', scheme: 'url' },
  { key: 'contact_discord', label: 'Discord', scheme: 'url' },
];

export const buildContactGroup = (status, t) => {
  if (!status) return null;
  const tr = typeof t === 'function' ? t : (s) => s;
  const links = [];
  for (const def of HOME_CONTACT_DEFS) {
    const raw = status[def.key];
    const value = typeof raw === 'string' ? raw.trim() : '';
    if (!value) continue;
    const name = tr(def.label);
    if (def.scheme === 'mailto') {
      links.push({ id: def.key, label: `${name}：${value}`, href: `mailto:${value}` });
    } else if (def.scheme === 'tel') {
      links.push({ id: def.key, label: `${name}：${value}`, href: `tel:${value}` });
    } else if (def.scheme === 'url' && /^https?:\/\//i.test(value)) {
      links.push({ id: def.key, label: name, href: value });
    } else {
      links.push({ id: def.key, label: `${name}：${value}`, plain: true });
    }
  }
  if (!links.length) return null;
  return { id: 'contact', title: '联系', links };
};

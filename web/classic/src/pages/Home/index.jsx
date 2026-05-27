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
import React, { useContext, useEffect, useMemo, useRef, useState } from 'react';
import { Button, Typography, Input, ScrollList, ScrollItem } from '@douyinfe/semi-ui';
import { Link, useLocation } from 'react-router-dom';
import { API, copy, showError, showSuccess, getSystemName, getLogo, getFooterHTML } from '../../helpers';
import { useIsMobile } from '../../hooks/common/useIsMobile';
import { API_ENDPOINTS } from '../../constants/common.constant';
import { StatusContext } from '../../context/Status';
import { useActualTheme } from '../../context/Theme';
import { marked } from 'marked';
import { useTranslation } from 'react-i18next';
import { IconGithubLogo, IconPlay, IconFile, IconCopy } from '@douyinfe/semi-icons';
import NoticeModal from '../../components/layout/NoticeModal';

const { Text } = Typography;

const TOP_LINKS = [
  { label: 'Models', to: '/pricing', hasDropdown: true },
  { label: 'Pricing', to: '/pricing' },
  { label: 'Docs', hasDropdown: true },
  { label: 'Console', to: '/console' },
  { label: 'Developers', hasDropdown: true },
];

const HERO_STATS = [
  { value: '200+', label: 'Available models' },
  { value: '99.95%', label: 'Gateway uptime' },
  { value: '~38ms', label: 'Routing overhead' },
  { value: '1 line', label: 'Migration cost' },
];

const PROVIDERS = [
  { name: 'OpenAI' },
  { name: 'Anthropic' },
  { name: 'Google' },
  { name: 'ByteDance' },
  { name: 'Qwen' },
  { name: 'Kimi' },
  { name: 'Minimax' },
];

const FEATURE_CARDS = [
  {
    title: 'Unified access layer',
    description:
      'Chat, image, audio, and video models are all exposed through one OpenAI-compatible entrypoint, so adding or swapping providers does not force application rewrites.',
    boxColor: '#e3ede6',
  },
  {
    title: 'Smart routing',
    description:
      'Route by latency, price, region, or availability. When one upstream drifts or fails, traffic can fail over automatically without breaking your product path.',
    boxColor: '#d7e6dc',
  },
  {
    title: 'Developer docs',
    description:
      'From first request to production rollout, the full integration path is documented. Replace the base URL and bring existing SDKs, agents, and workflows with you.',
    boxColor: '#ecf3ee',
  },
];

const WHY_FEATURES = [
  {
    title: 'Production-grade reliability',
    description:
      'Multi-provider redundancy, regional routing, and automatic failover keep critical AI flows online when a single upstream becomes unstable.',
  },
  {
    title: 'Transparent cost control',
    description:
      'Unified billing, usage breakdowns, and cache hit visibility help teams understand exactly where every budget dollar is going.',
  },
  {
    title: 'One control surface',
    description:
      'Models, keys, channels, billing, monitoring, and team permissions all live in one operating layer instead of fragmented tooling.',
  },
  {
    title: 'Fastest migration path',
    description:
      'OpenAI-compatible protocols let you connect existing applications and agent stacks with minimal code changes and less rollout risk.',
  },
  {
    title: 'Flexible ecosystem bridging',
    description:
      'Connect many upstream model vendors and internal tools under one access strategy, without turning integration governance into a manual task.',
  },
  {
    title: 'Built for multi-team workflows',
    description:
      'Operations, engineering, finance, and support can share the same system view instead of reconciling status from separate dashboards.',
  },
];

const GLANCE_CARDS = [
  { label: 'Total requests', value: '143.2K', delta: '+18.7%', note: 'Across all providers and models, peaking at 8.2K requests per hour.' },
  { label: 'Total cost', value: '$1,247', delta: '-12.3%', note: 'Semantic cache and routing efficiency brought spend down from $1,422.' },
  { label: 'Cache hit rate', value: '90%', delta: '+4.2%', note: '127.4K hits saved an estimated $1,180 this cycle.' },
  { label: 'Availability', value: '99.97%', delta: '30 days', note: 'Automatic failover was triggered 3 times with no customer-visible outage.' },
];

const SIDEBAR_GROUPS = [
  { title: 'Overview', items: ['Dashboard', 'Usage', 'Routing'] },
  { title: 'Operations', items: ['Channels', 'Keys', 'Billing'] },
  { title: 'Insights', items: ['Latency', 'Caching', 'Cost by model'] },
];

const LATENCY_ROWS = ['<50ms', '<100ms', '<150ms', '<200ms', '<250ms', '<300ms'];
const LATENCY_COLS = 24;
const COST_TABS = ['7 days', '30 days', '90 days'];
const COST_DAYS = ['Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun'];
const COST_MODELS = [
  { name: 'Claude Opus 4.7', color: '#4ba97e' },
  { name: 'Claude Opus 4.6', color: '#2e6b52' },
  { name: 'GPT 5.5', color: '#6fae93' },
  { name: 'Gemini 3.1 Pro', color: '#c79a3e' },
  { name: 'Gemini 3.5 Flash', color: '#a6745a' },
];
const COST_SERIES = [
  [28, 22, 16, 10, 6],
  [34, 26, 18, 12, 8],
  [24, 20, 15, 9, 6],
  [40, 30, 22, 14, 9],
  [32, 25, 18, 11, 7],
  [20, 16, 12, 8, 5],
  [30, 24, 17, 10, 7],
];

const FOOTER_COLUMNS = (docsUrl) => [
  {
    title: 'Product',
    links: [
      { label: 'Model directory', to: '/pricing' },
      { label: 'Console', to: '/console' },
      { label: 'About', to: '/about' },
    ],
  },
  {
    title: 'Developers',
    links: [
      { label: 'Get started', to: '/register' },
      { label: 'API docs', href: docsUrl, external: true },
      { label: 'Deployment guide', href: `${docsUrl.replace(/\/$/, '')}/installation/`, external: true },
    ],
  },
  {
    title: 'Resources',
    links: [
      { label: 'GitHub', href: 'https://github.com/QuantumNous/new-api', external: true },
      { label: 'Documentation', href: docsUrl, external: true },
      { label: 'Privacy policy', to: '/privacy-policy' },
      { label: 'User agreement', to: '/user-agreement' },
    ],
  },
];

const CTA_SNIPPET = `from openai import OpenAI

client = OpenAI(
    base_url="https://your-newapi.example/v1",
    api_key="<your-key>",
)

response = client.chat.completions.create(
    model="gpt-5.5",
    messages=[
        {"role": "system", "content": "You are a helpful assistant."},
        {"role": "user", "content": "Introduce the new-api gateway in one sentence."},
    ],
    stream=True,
)

for chunk in response:
    print(chunk.choices[0].delta.content or "", end="")`;

function cn(...values) {
  return values.filter(Boolean).join(' ');
}

const TOKEN_COLORS = {
  keyword: '#ff8fb0',
  string: '#c3e88d',
  fn: '#82c7ff',
  number: '#f7a072',
  comment: '#7fa594',
  punct: '#a7d8c1',
  text: '#dcefe5',
};

const PY_KEYWORDS = new Set([
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
]);

function tokenizePython(code) {
  const tokens = [];
  const re =
    /(#[^\n]*)|("(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*')|(\d+(?:\.\d+)?)|([A-Za-z_]\w*)|(\s+)|([^\sA-Za-z_]+)/g;
  let match;

  while ((match = re.exec(code))) {
    if (match[1]) tokens.push({ text: match[1], color: TOKEN_COLORS.comment });
    else if (match[2]) tokens.push({ text: match[2], color: TOKEN_COLORS.string });
    else if (match[3]) tokens.push({ text: match[3], color: TOKEN_COLORS.number });
    else if (match[4]) {
      const next = code[re.lastIndex];
      const color = PY_KEYWORDS.has(match[4])
        ? TOKEN_COLORS.keyword
        : next === '('
          ? TOKEN_COLORS.fn
          : TOKEN_COLORS.text;
      tokens.push({ text: match[4], color });
    } else if (match[5]) {
      tokens.push({ text: match[5], color: TOKEN_COLORS.text });
    } else if (match[6]) {
      tokens.push({ text: match[6], color: TOKEN_COLORS.punct });
    }
  }

  return tokens;
}

function useReducedMotion() {
  const [reduced, setReduced] = useState(false);
  useEffect(() => {
    const media = window.matchMedia('(prefers-reduced-motion: reduce)');
    const update = () => setReduced(media.matches);
    update();
    media.addEventListener('change', update);
    return () => media.removeEventListener('change', update);
  }, []);
  return reduced;
}

function useReveal(threshold = 0.12) {
  const ref = useRef(null);
  const reducedMotion = useReducedMotion();
  const [visible, setVisible] = useState(reducedMotion);

  useEffect(() => {
    if (reducedMotion) {
      setVisible(true);
      return;
    }
    const node = ref.current;
    if (!node) return;
    const observer = new IntersectionObserver(
      ([entry]) => {
        if (entry.isIntersecting) {
          setVisible(true);
          observer.disconnect();
        }
      },
      { threshold },
    );
    observer.observe(node);
    return () => observer.disconnect();
  }, [reducedMotion, threshold]);

  return { ref, visible };
}

function Reveal({ children, className, fast = true }) {
  const { ref, visible } = useReveal();
  return (
    <div
      ref={ref}
      className={cn(
        'fade-in-up-element',
        fast && 'fade-in-up-fast',
        visible && 'animate-fade-in-up',
        className,
      )}
    >
      {children}
    </div>
  );
}

function ScrollProgress() {
  const [width, setWidth] = useState(0);
  useEffect(() => {
    const handleScroll = () => {
      const root = document.documentElement;
      const max = root.scrollHeight - root.clientHeight;
      setWidth(max > 0 ? (window.scrollY / max) * 100 : 0);
    };
    handleScroll();
    window.addEventListener('scroll', handleScroll, { passive: true });
    return () => window.removeEventListener('scroll', handleScroll);
  }, []);
  return (
    <div className='pointer-events-none fixed inset-x-0 top-0 z-[60] h-[3px]'>
      <div
        className='h-full bg-gradient-to-r from-[#102e24] via-[#4ba97e] to-[#2e6b52] transition-[width] duration-150 ease-out'
        style={{ width: `${width}%` }}
      />
    </div>
  );
}

function WavyText({ text }) {
  const chars = Array.from(text);
  return (
    <span className='wavy-text'>
      <span aria-hidden className='span-mother'>
        {chars.map((char, index) => (
          <span key={`top-${index}`} style={{ '--i': index }}>
            {char === ' ' ? '\u00A0' : char}
          </span>
        ))}
      </span>
      <span aria-hidden className='span-mother2'>
        {chars.map((char, index) => (
          <span key={`bottom-${index}`} style={{ '--i': index }}>
            {char === ' ' ? '\u00A0' : char}
          </span>
        ))}
      </span>
      <span className='sr-only'>{text}</span>
    </span>
  );
}

function NavTarget({ link, className, children, onClick }) {
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
    );
  }
  return (
    <Link to={link.to || '/'} className={className} onClick={onClick}>
      {children}
    </Link>
  );
}

function MobileMenu({ links, open, onClose, isAuthenticated, loginTarget, primaryTarget }) {
  return (
    <div className={cn('fixed inset-0 z-[100] flex flex-col bg-[#eaf1ec] transition-transform duration-300 ease-in-out lg:hidden', open ? 'translate-x-0' : 'translate-x-full')} aria-hidden={!open}>
      <div className='flex items-center justify-between px-[16px] py-4'>
        <span className='flex items-center gap-[10px]'>
          <img src='/n123-logo.svg' alt='N123' className='h-[26px] w-auto' />
          <span className='text-[18px] font-semibold text-[#14201a]'>N123</span>
        </span>
        <button type='button' aria-label='Close menu' onClick={onClose} className='relative h-[24px] w-[24px]'>
          <span className='absolute left-0 top-1/2 block h-[1.5px] w-full rotate-45 bg-[#14201a]' />
          <span className='absolute left-0 top-1/2 block h-[1.5px] w-full -rotate-45 bg-[#14201a]' />
        </button>
      </div>

      <nav className='mt-6 flex flex-col px-[20px]'>
        {links.map((link) => (
          <NavTarget
            key={link.label}
            link={link}
            onClick={onClose}
            className='flex items-center justify-between border-b border-black/10 py-[16px] text-[20px] font-medium text-black'
          >
            <>
              {link.label}
              {link.hasDropdown && <span className='opacity-50'>⌄</span>}
            </>
          </NavTarget>
        ))}
      </nav>

      <div className='mt-auto flex flex-col gap-[12px] p-[20px]'>
        <Link to={loginTarget} onClick={onClose} className='inline-flex h-[48px] items-center justify-center rounded-full border border-[#4ba97e]/20 bg-white text-[15px] font-medium text-[#14201a]'>
          {isAuthenticated ? 'Go to console' : 'Sign in'}
        </Link>
        <Link to={primaryTarget} onClick={onClose} className='inline-flex h-[48px] items-center justify-center rounded-full bg-[#4ba97e] text-[15px] font-semibold text-white'>
          Free Start
        </Link>
      </div>
    </div>
  );
}

function LandingNavbar({ docsUrl, isAuthenticated }) {
  const [menuOpen, setMenuOpen] = useState(false);
  const [scrolled, setScrolled] = useState(false);
  const loginTarget = isAuthenticated ? '/console' : '/login';
  const primaryTarget = isAuthenticated ? '/console' : '/register';
  const links = useMemo(
    () =>
      TOP_LINKS.map((link) => {
        if (link.label === 'Docs') return { ...link, href: docsUrl, external: true };
        return link;
      }),
    [docsUrl],
  );

  useEffect(() => {
    const onScroll = () => setScrolled(window.scrollY > 30);
    onScroll();
    window.addEventListener('scroll', onScroll, { passive: true });
    return () => window.removeEventListener('scroll', onScroll);
  }, []);

  useEffect(() => {
    document.body.style.overflow = menuOpen ? 'hidden' : '';
    return () => { document.body.style.overflow = ''; };
  }, [menuOpen]);

  return (
    <>
      <header className={cn('fixed left-0 right-0 top-0 z-50 px-[16px] py-4 transition-colors duration-300 md:px-[26px] md:py-5', scrolled ? 'border-b border-[#e6e9e3]/70 bg-[#fbfbf9]/80 backdrop-blur-md' : 'bg-transparent')}>
        <div className='mx-auto flex max-w-[1420px] items-center justify-between'>
          <div className='flex items-center gap-[36px]'>
            <a href='#top' aria-label='N123' className='flex items-center gap-[8px]'>
              <img src='/n123-logo.svg' alt='N123' className='h-[26px] w-auto' />
              <span className='text-[19px] font-semibold tracking-tight text-[#14201a]'>N123</span>
            </a>
            <nav className='hidden items-center gap-[22px] lg:flex'>
              {links.map((link) => (
                <NavTarget key={link.label} link={link} className='group flex items-center gap-[4px] whitespace-nowrap text-[15px] font-medium text-[#14201a]/85 transition-colors hover:text-[#14201a]'>
                  <>
                    <span className='decoration-[#4ba97e] underline-offset-[5px] group-hover:underline'>{link.label}</span>
                    {link.hasDropdown && <span className='opacity-60'>⌄</span>}
                  </>
                </NavTarget>
              ))}
            </nav>
          </div>

          <div className='flex items-center gap-[12px] md:gap-[16px]'>
            <Link to={loginTarget} className='group hidden h-[34px] items-center rounded-[10px] border border-[#4ba97e]/15 bg-white px-[16px] text-[14px] font-medium text-[#14201a] transition-colors hover:border-[#4ba97e]/35 sm:inline-flex'>
              <span className='decoration-[#4ba97e] underline-offset-[5px] group-hover:underline'>
                {isAuthenticated ? 'Dashboard' : 'Sign in'}
              </span>
            </Link>
            <Link to={primaryTarget} className='inline-flex h-[34px] items-center rounded-[10px] bg-[#4ba97e] px-[16px] text-[14px] font-semibold text-white shadow-[0_4px_14px_rgba(75,169,126,0.3)] transition-colors hover:bg-[#143c2f]'>
              <WavyText text={isAuthenticated ? 'Open Console' : 'Free Start'} />
            </Link>
            <button type='button' aria-label='Menu' onClick={() => setMenuOpen(true)} className='flex h-[24px] w-[24px] flex-col justify-center gap-[5px] lg:hidden'>
              <span className='block h-[1.5px] w-full bg-[#14201a]' />
              <span className='block h-[1.5px] w-full bg-[#14201a]' />
            </button>
          </div>
        </div>
      </header>

      <MobileMenu links={links} open={menuOpen} onClose={() => setMenuOpen(false)} isAuthenticated={isAuthenticated} loginTarget={loginTarget} primaryTarget={primaryTarget} />
    </>
  );
}

function ProviderMarquee() {
  const half = [...PROVIDERS, ...PROVIDERS, ...PROVIDERS];
  const track = [...half, ...half];
  return (
    <section className='border-y border-[#e6e9e3] bg-[#fbfbf9] py-7'>
      <p className='mb-5 text-center text-[13px] tracking-wide text-[#5b6b62]'>Unified access to mainstream AI providers</p>
      <div className='marquee-pause fade-x mx-auto max-w-7xl overflow-hidden px-4'>
        <div className='flex w-max animate-marquee items-center gap-12'>
          {track.map((provider, index) => (
            <div key={`${provider.name}-${index}`} className='flex shrink-0 items-center gap-2 text-[#5b6b62]'>
              <span className='flex size-6 items-center justify-center rounded-full bg-[#eaf1ec] text-[11px] font-semibold uppercase text-[#4ba97e]'>
                {provider.name.slice(0, 2)}
              </span>
              <span className='text-base font-medium text-[#14201a]/80'>{provider.name}</span>
            </div>
          ))}
        </div>
      </div>
    </section>
  );
}

function HeroDashboard() {
  return (
    <div className='overflow-hidden rounded-[28px] border border-white/60 bg-[linear-gradient(180deg,#17392e_0%,#0f2e24_100%)] p-[1px] shadow-[0_24px_70px_rgba(16,46,36,0.22)]'>
      <div className='overflow-hidden rounded-[27px] border border-white/6 bg-[#102e24]'>
        <div className='grid min-h-[560px] grid-cols-[220px_1fr]'>
          <aside className='border-r border-white/8 bg-white/[0.03] px-5 py-5'>
            <div className='mb-5 flex items-center gap-3 rounded-[14px] border border-white/8 bg-white/[0.04] px-3 py-3'>
              <span className='flex size-9 items-center justify-center rounded-full bg-[#4ba97e]/20 text-[#6fae93]'>≋</span>
              <div>
                <div className='text-[13px] font-semibold text-white'>Gateway</div>
                <div className='text-[11px] text-white/50'>new-api orchestration</div>
              </div>
            </div>
            <div className='space-y-5'>
              {SIDEBAR_GROUPS.map((group) => (
                <div key={group.title}>
                  <div className='mb-2 text-[11px] uppercase tracking-[0.18em] text-white/35'>{group.title}</div>
                  <div className='space-y-2'>
                    {group.items.map((item, index) => (
                      <div key={item} className={cn('rounded-[12px] px-3 py-2 text-[13px] transition-colors', index === 0 && group.title === 'Overview' ? 'bg-white/[0.08] text-white' : 'text-white/55')}>
                        {item}
                      </div>
                    ))}
                  </div>
                </div>
              ))}
            </div>
          </aside>
          <div className='mock-scroll overflow-auto px-5 py-5'>
            <div className='grid gap-4 xl:grid-cols-[1.4fr_0.8fr]'>
              <div className='space-y-4'>
                <div className='grid gap-4 sm:grid-cols-2 xl:grid-cols-4'>
                  {GLANCE_CARDS.map((card, index) => (
                    <div key={card.label} className={cn('rounded-[18px] border border-white/8 bg-white/[0.04] p-4', index === 0 && 'xl:col-span-2')}>
                      <div className='text-[12px] text-white/50'>{card.label}</div>
                      <div className='mt-3 flex items-end justify-between gap-3'>
                        <div className='text-[26px] font-semibold text-white'>{card.value}</div>
                        <div className={cn('rounded-full px-2 py-1 text-[11px] font-medium', card.delta.startsWith('-') ? 'bg-[#6fae93]/12 text-[#9fd0b8]' : 'bg-[#4ba97e]/16 text-[#9ae0bf]')}>
                          {card.delta}
                        </div>
                      </div>
                      <p className='mt-3 text-[12px] leading-5 text-white/48'>{card.note}</p>
                    </div>
                  ))}
                </div>

                <div className='grid gap-4 xl:grid-cols-[1.05fr_0.95fr]'>
                  <div className='rounded-[22px] border border-white/8 bg-white/[0.04] p-4'>
                    <div className='mb-3 flex items-center justify-between'>
                      <div>
                        <div className='text-[15px] font-semibold text-white'>Latency heatmap</div>
                        <div className='text-[12px] text-white/50'>95% of requests complete in under 50ms</div>
                      </div>
                      <div className='rounded-full border border-[#4ba97e]/30 bg-[#4ba97e]/12 px-3 py-1 text-[11px] font-medium text-[#9ae0bf]'>Real time</div>
                    </div>
                    <div className='grid gap-2' style={{ gridTemplateColumns: `70px repeat(${LATENCY_COLS}, minmax(0, 1fr))` }}>
                      {LATENCY_ROWS.map((row, rowIndex) => (
                        <React.Fragment key={row}>
                          <div className='pr-3 text-[11px] text-white/40'>{row}</div>
                          {Array.from({ length: LATENCY_COLS }).map((_, colIndex) => {
                            const intensity = ((rowIndex + 2) * (colIndex + 5)) % 10;
                            const alpha = 0.12 + intensity * 0.055;
                            return (
                              <div
                                key={`${row}-${colIndex}`}
                                className='h-5 rounded-[6px]'
                                style={{ backgroundColor: `rgba(75,169,126,${alpha.toFixed(3)})` }}
                              />
                            );
                          })}
                        </React.Fragment>
                      ))}
                    </div>
                  </div>

                  <div className='space-y-4'>
                    <div className='rounded-[22px] border border-white/8 bg-white/[0.04] p-4'>
                      <div className='flex items-start justify-between gap-4'>
                        <div>
                          <div className='text-[15px] font-semibold text-white'>Cache performance</div>
                          <div className='text-[12px] text-white/50'>Hit rate and savings across providers</div>
                        </div>
                        <div className='text-right'>
                          <div className='text-[28px] font-semibold text-white'>90%</div>
                          <div className='text-[11px] text-[#9ae0bf]'>+4.2%</div>
                        </div>
                      </div>
                      <div className='mt-5'>
                        <div className='h-2 overflow-hidden rounded-full bg-white/8'>
                          <div className='h-full rounded-full bg-gradient-to-r from-[#6fae93] to-[#4ba97e]' style={{ width: '90%' }} />
                        </div>
                        <div className='mt-4 grid grid-cols-3 gap-3 text-center'>
                          {[
                            ['127.4K', 'Hits'],
                            ['14.2K', 'Misses'],
                            ['$1,180', 'Saved'],
                          ].map(([value, label]) => (
                            <div key={label} className='rounded-[14px] bg-white/[0.03] px-3 py-3'>
                              <div className='text-[15px] font-semibold text-white'>{value}</div>
                              <div className='mt-1 text-[11px] text-white/45'>{label}</div>
                            </div>
                          ))}
                        </div>
                      </div>
                    </div>
                    <div className='rounded-[22px] border border-white/8 bg-white/[0.04] p-4'>
                      <div className='mb-4 flex items-center justify-between'>
                        <div className='text-[15px] font-semibold text-white'>Cost by model</div>
                        <div className='flex items-center gap-2 rounded-full border border-white/8 bg-white/[0.03] p-1'>
                          {COST_TABS.map((tab, index) => (
                            <div key={tab} className={cn('rounded-full px-3 py-1 text-[11px]', index === 1 ? 'bg-white text-[#102e24]' : 'text-white/45')}>
                              {tab}
                            </div>
                          ))}
                        </div>
                      </div>
                      <div className='space-y-3'>
                        {COST_MODELS.map((model, index) => (
                          <div key={model.name}>
                            <div className='mb-2 flex items-center justify-between text-[12px]'>
                              <span className='text-white/68'>{model.name}</span>
                              <span className='text-white/45'>{['$426', '$318', '$244', '$159', '$100'][index]}</span>
                            </div>
                            <div className='h-2 overflow-hidden rounded-full bg-white/8'>
                              <div className='h-full rounded-full' style={{ width: `${95 - index * 8}%`, backgroundColor: model.color }} />
                            </div>
                          </div>
                        ))}
                      </div>
                    </div>
                  </div>
                </div>
              </div>

              <div className='rounded-[22px] border border-white/8 bg-white/[0.04] p-4'>
                <div className='mb-4 flex items-center justify-between'>
                  <div>
                    <div className='text-[15px] font-semibold text-white'>Daily cost by model</div>
                    <div className='text-[12px] text-white/50'>7-day spend trend and traffic composition</div>
                  </div>
                  <div className='rounded-full border border-white/8 bg-white/[0.03] px-3 py-1 text-[11px] text-white/50'>Updated 2m ago</div>
                </div>
                <div className='rounded-[18px] border border-white/6 bg-[#0c261e] p-4'>
                  <div className='mb-6 grid grid-cols-7 gap-3 text-center text-[11px] text-white/45'>
                    {COST_DAYS.map((day) => (
                      <div key={day}>{day}</div>
                    ))}
                  </div>
                  <div className='grid grid-cols-7 items-end gap-3'>
                    {COST_SERIES.map((group, index) => {
                      const total = group.reduce((sum, item) => sum + item, 0);
                      return (
                        <div key={COST_DAYS[index]} className='flex h-[220px] flex-col justify-end gap-[6px]'>
                          {group.slice().reverse().map((value, stackIndex) => {
                            const model = COST_MODELS[group.length - stackIndex - 1];
                            return (
                              <div key={`${COST_DAYS[index]}-${model.name}`} className='rounded-[8px]' style={{ height: `${Math.max(14, value * 3)}px`, backgroundColor: model.color, opacity: 0.92 - stackIndex * 0.08 }} />
                            );
                          })}
                          <div className='pt-1 text-center text-[11px] text-white/30'>${total}</div>
                        </div>
                      );
                    })}
                  </div>
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}

function HeroSection({ docsUrl, isAuthenticated }) {
  const primaryTarget = isAuthenticated ? '/console' : '/register';
  const secondaryTarget = isAuthenticated ? '/console' : '/login';
  return (
    <section className='relative flex flex-1 items-center overflow-hidden'>
      <div
        aria-hidden
        className='pointer-events-none absolute left-[-8%] top-[8%] h-[420px] w-[420px] rounded-full opacity-60 blur-[90px]'
        style={{ background: 'radial-gradient(circle, rgba(111,174,147,0.32), transparent 72%)', animation: 'aura-float 14s ease-in-out infinite' }}
      />
      <div
        aria-hidden
        className='pointer-events-none absolute right-[-10%] top-[-6%] h-[460px] w-[460px] rounded-full opacity-50 blur-[110px]'
        style={{ background: 'radial-gradient(circle, rgba(75,169,126,0.28), transparent 72%)', animation: 'aura-float-alt 16s ease-in-out infinite' }}
      />
      <div className='relative z-10 mx-auto w-full max-w-[1360px] px-[24px] pb-[64px] pt-[120px]'>
        <div className='grid items-center gap-12 lg:grid-cols-[0.9fr_1.1fr] lg:gap-14'>
          <div className='flex flex-col items-center text-center lg:items-start lg:text-left'>
            <a href='#models' className='hero-fade-in-up group mb-[24px] inline-flex items-center gap-[6px] rounded-full border border-[#4ba97e]/15 bg-[#eaf1ec] px-[16px] py-[8px] text-[12px] leading-normal text-[#4ba97e] transition-all duration-300 hover:border-[#4ba97e]/30 hover:shadow-sm md:text-[13px]'>
              <span className='font-semibold text-[#102e24]'>200+ models</span>
              <span>connected</span>
              <span>•</span>
              <span>chat / image / video</span>
              <span className='transition-transform group-hover:translate-x-[2px]'>→</span>
            </a>

            <h1 className='hero-fade-in-up text-[38px] font-semibold leading-[1.08] tracking-tight text-[#14201a] md:text-[54px] lg:text-[58px]'>
              One gateway for AI agents
              <br />
              <span className='text-[#4ba97e]'>Unified model access layer</span>
            </h1>

            <p className='hero-fade-in-up mt-[20px] max-w-[520px] text-[16px] leading-[1.7] text-[#5b6b62] md:text-[18px]'>
              Connect mainstream large models and multimodal capabilities through a single OpenAI-compatible interface, with smart routing, transparent pricing, and a calmer operating surface for teams.
            </p>

            <div className='hero-fade-in-up mt-[30px] flex flex-col items-center gap-[12px] sm:flex-row'>
              <Link to={primaryTarget} className='inline-flex h-[40px] items-center justify-center rounded-[10px] bg-[#4ba97e] px-[28px] text-[15px] font-semibold text-[#fbfbf9] shadow-[0_8px_24px_rgba(75,169,126,0.28)] transition-colors hover:bg-[#143c2f]'>
                {isAuthenticated ? 'Open console' : 'Free Start'}
              </Link>
              <a href={isAuthenticated ? secondaryTarget : docsUrl} target={isAuthenticated ? undefined : '_blank'} rel={isAuthenticated ? undefined : 'noreferrer'} className='inline-flex h-[40px] items-center justify-center rounded-[10px] border border-[#4ba97e]/20 bg-white px-[28px] text-[15px] font-medium text-[#14201a] transition-colors hover:border-[#4ba97e]/40'>
                {isAuthenticated ? 'Go to dashboard' : 'Read docs'}
              </a>
            </div>

            <dl className='hero-fade-in-up mt-[36px] grid w-full max-w-[520px] grid-cols-4 gap-x-4 gap-y-2'>
              {HERO_STATS.map((stat) => (
                <div key={stat.label} className='text-center lg:text-left'>
                  <dd className='text-[22px] font-semibold text-[#4ba97e] md:text-[24px]'>{stat.value}</dd>
                  <dt className='text-[12px] text-[#5b6b62]'>{stat.label}</dt>
                </div>
              ))}
            </dl>
          </div>

          <div className='hero-fade-in-up w-full lg:-mr-[220px] xl:-mr-[380px]'>
            <HeroDashboard />
          </div>
        </div>
      </div>
    </section>
  );
}

function ProviderSection() {
  const half = [...PROVIDERS, ...PROVIDERS, ...PROVIDERS];
  const track = [...half, ...half];
  return (
    <section className='border-y border-[#e6e9e3] bg-[#fbfbf9] py-7'>
      <p className='mb-5 text-center text-[13px] tracking-wide text-[#5b6b62]'>Unified access to mainstream AI providers</p>
      <div className='marquee-pause fade-x mx-auto max-w-7xl overflow-hidden px-4'>
        <div className='flex w-max animate-marquee items-center gap-12'>
          {track.map((provider, index) => (
            <div key={`${provider.name}-${index}`} className='flex shrink-0 items-center gap-2 text-[#5b6b62]'>
              <span className='flex size-6 items-center justify-center rounded-full bg-[#eaf1ec] text-[11px] font-semibold uppercase text-[#4ba97e]'>
                {provider.name.slice(0, 2)}
              </span>
              <span className='text-base font-medium text-[#14201a]/80'>{provider.name}</span>
            </div>
          ))}
        </div>
      </div>
    </section>
  );
}

function GlanceSection() {
  return (
    <section id='dashboard' className='bg-[#fbfbf9] py-16 sm:py-20 lg:py-24'>
      <div className='mx-auto max-w-7xl px-6 lg:px-8'>
        <div className='mb-8 text-center'>
          <h2 className='text-3xl font-bold tracking-tight text-[#14201a] sm:text-4xl'>
            Your AI usage, visible at a glance
          </h2>
          <p className='mx-auto mt-3 max-w-xl text-[#5b6b62]'>
            Latency, spend, and cache performance come together in one operating surface so teams can understand their agent infrastructure without stitching together separate tools.
          </p>
        </div>
        <Reveal>
          <HeroDashboard />
        </Reveal>
      </div>
    </section>
  );
}

function FeaturesSection() {
  return (
    <section className='relative w-full overflow-hidden'>
      <div className='relative z-10 w-full bg-[#eaf1ec] py-[80px] md:py-[110px]'>
        <div className='features-mesh-bg pointer-events-none absolute inset-0' />
        <Reveal className='relative z-10 mx-auto w-full max-w-[1400px] px-[20px] md:px-[24px]'>
          <div className='text-center md:text-start'>
            <p className='mb-3 text-[13px] tracking-wide text-[#5b6b62]'>Core capabilities</p>
            <h2 className='text-[28px] font-semibold leading-[1.1] text-[#14201a] md:text-[36px] lg:text-[44px]'>
              Make large-model access feel operationally simple
            </h2>
          </div>

          <div className='mb-[64px] mt-6 hidden h-px w-full bg-[#4ba97e]/15 md:block min-[1600px]:mb-[100px]' />

          <div className='grid grid-cols-1 gap-10 md:flex md:items-stretch md:gap-0'>
            {FEATURE_CARDS.map((card, index) => (
              <div key={card.title} className={cn('flex-1 md:px-6', index > 0 && 'md:border-l md:border-[#4ba97e]/15')}>
                <div className='transition-transform duration-300 ease-[cubic-bezier(0.22,1,0.36,1)] hover:-translate-y-[6px]'>
                  <div className='flex h-[174px] items-center justify-center rounded-[18px] shadow-[0_10px_30px_-12px_rgba(75,169,126,0.35)]' style={{ backgroundColor: card.boxColor }}>
                    <span className='text-[#4ba97e]'>⬢</span>
                  </div>
                  <h3 className='mt-[20px] text-[20px] font-semibold leading-[30px] text-[#14201a]'>{card.title}</h3>
                  <p className='mt-[8px] text-[17px] leading-[27px] text-[#5b6b62]'>{card.description}</p>
                </div>
              </div>
            ))}
          </div>
        </Reveal>
      </div>
    </section>
  );
}

function MapCtaSection() {
  const arcs = [
    'M 67.79 57.33 Q 102.51 7.33 137.24 124.33',
    'M 67.79 57.33 Q 180.68 7.33 293.57 235.11',
    'M 293.57 235.11 Q 336.63 63.95 379.69 113.95',
    'M 399.72 85.54 Q 485.65 35.54 571.58 136.41',
    'M 571.58 136.41 Q 632.36 54.15 693.14 104.15',
    'M 571.58 136.41 Q 526.7 86.41 481.83 202.87',
  ];
  const nodes = [[67.79, 57.33],[137.24,124.33],[293.57,235.11],[379.69,113.95],[399.72,85.54],[571.58,136.41],[693.14,104.15],[481.83,202.87]];
  return (
    <section className='relative w-full bg-[#fbfbf9] py-16 sm:py-20 lg:py-24'>
      <div className='mx-auto max-w-3xl px-4 text-center'>
        <p className='text-[13px] tracking-wide text-[#5b6b62]'>Global infrastructure</p>
        <h2 className='mt-2 text-3xl font-bold tracking-tight text-[#14201a] sm:text-4xl'>
          Route nearby, stay low-latency
        </h2>
        <p className='mx-auto mt-3 max-w-xl text-[#5b6b62]'>
          Requests are routed toward the most suitable region and upstream path, keeping mainstream models reachable with predictable latency across regions.
        </p>
      </div>

      <div className='relative mx-auto mt-10 w-full max-w-[1920px] px-[16px]'>
        <div className='relative aspect-[3/1] w-full overflow-hidden'>
          <img src='/images/world-map.svg' alt='World map' className='pointer-events-none absolute inset-0 h-full w-full select-none object-cover object-top' />
          <svg viewBox='0 0 800 400' preserveAspectRatio='xMidYMin slice' className='pointer-events-none absolute inset-0 h-full w-full select-none'>
            <defs>
              <linearGradient id='arc-gradient' x1='0%' y1='0%' x2='100%' y2='0%'>
                <stop offset='0%' stopColor='#4ba97e' stopOpacity='0' />
                <stop offset='18%' stopColor='#4ba97e' stopOpacity='1' />
                <stop offset='82%' stopColor='#2e6b52' stopOpacity='1' />
                <stop offset='100%' stopColor='#2e6b52' stopOpacity='0' />
              </linearGradient>
            </defs>
            {arcs.map((path, index) => (
              <path key={`b-${index}`} d={path} fill='none' stroke='#4ba97e' strokeOpacity='0.25' strokeWidth='0.8' strokeLinecap='round' />
            ))}
            {arcs.map((path, index) => (
              <path key={`f-${index}`} d={path} fill='none' stroke='url(#arc-gradient)' strokeWidth='1.4' strokeLinecap='round' className='arc-flow' style={{ animationDelay: `${index * 0.15}s` }} />
            ))}
            {nodes.map(([cx, cy], index) => (
              <circle key={index} cx={cx} cy={cy} r='2.6' fill='#4ba97e' />
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
  );
}

function WhyChooseSection() {
  return (
    <section className='bg-[#fbfbf9] py-16 sm:py-20 lg:py-24'>
      <div className='mx-auto max-w-7xl px-4 sm:px-6 lg:px-8'>
        <div className='text-center'>
          <h2 className='text-3xl font-bold tracking-tight text-[#14201a] sm:text-4xl'>
            Why teams choose this gateway layer
          </h2>
          <p className='mx-auto mt-4 max-w-[640px] text-[#5b6b62]'>
            The homepage mirrors the reference experience, but keeps protected project identifiers intact while fitting real new-api routes and controls.
          </p>
        </div>
        <div className='mt-14 grid gap-x-8 gap-y-10 sm:grid-cols-2 lg:grid-cols-3'>
          {WHY_FEATURES.map((feature, index) => (
            <Reveal key={feature.title}>
              <div className='flex gap-4'>
                <span className='flex size-10 shrink-0 items-center justify-center rounded-lg bg-[#eaf1ec] text-[#4ba97e]'>
                  {index + 1}
                </span>
                <div>
                  <h3 className='font-semibold text-[#14201a]'>{feature.title}</h3>
                  <p className='mt-1 text-sm leading-relaxed text-[#5b6b62]'>{feature.description}</p>
                </div>
              </div>
            </Reveal>
          ))}
        </div>
      </div>
    </section>
  );
}

function TypewriterCode({ code, speed = 42 }) {
  const [count, setCount] = useState(0);
  const reducedMotion = useReducedMotion();
  const tokens = useMemo(() => tokenizePython(code), [code]);

  useEffect(() => {
    if (reducedMotion) {
      setCount(code.length);
      return;
    }
    let index = 0;
    let timer;
    const tick = () => {
      index += 1;
      setCount(index);
      if (index < code.length) {
        timer = setTimeout(tick, code[index] === '\n' ? speed * 4 : speed);
      } else {
        timer = setTimeout(() => {
          index = 0;
          setCount(0);
          timer = setTimeout(tick, speed);
        }, 2600);
      }
    };
    timer = setTimeout(tick, 500);
    return () => clearTimeout(timer);
  }, [code, reducedMotion, speed]);

  const rendered = [];
  let remaining = count;
  for (let index = 0; index < tokens.length && remaining > 0; index += 1) {
    const slice = tokens[index].text.slice(0, remaining);
    rendered.push(<span key={index} style={{ color: tokens[index].color }}>{slice}</span>);
    remaining -= slice.length;
  }

  return (
    <pre className='pointer-events-none absolute inset-0 m-0 select-none overflow-hidden whitespace-pre p-6 text-left font-mono text-[12px] leading-relaxed opacity-50 sm:p-10 sm:text-[13px]'>
      {rendered}
      <span className='caret-blink text-[#a7d8c1]'>|</span>
    </pre>
  );
}

function CtaSection({ docsUrl, isAuthenticated }) {
  const primaryTarget = isAuthenticated ? '/console' : '/register';
  const secondaryTarget = isAuthenticated ? '/console' : docsUrl;
  return (
    <section className='bg-[#fbfbf9] px-[16px] pb-16 sm:pb-20 lg:pb-24'>
      <div className='relative mx-auto max-w-6xl overflow-hidden rounded-[24px] bg-[#102e24] px-6 py-16 text-center sm:py-20'>
        <TypewriterCode code={CTA_SNIPPET} />
        <div aria-hidden className='absolute inset-0 bg-[radial-gradient(ellipse_60%_60%_at_50%_50%,rgba(16,46,36,0.82),rgba(16,46,36,0.42))]' />
        <div aria-hidden className='pointer-events-none absolute left-1/2 top-[-33%] h-[420px] w-[420px] -translate-x-1/2 rounded-full opacity-50 blur-[90px]' style={{ background: 'radial-gradient(circle, rgba(111,174,147,0.45), transparent 70%)' }} />
        <div className='relative z-10'>
          <h2 className='mx-auto max-w-[640px] text-3xl font-bold tracking-tight text-white drop-shadow-[0_2px_12px_rgba(0,0,0,0.35)] sm:text-4xl'>
            Launch a compatible AI gateway without rebuilding your stack
          </h2>
          <p className='mt-4 text-white/80 drop-shadow-[0_1px_8px_rgba(0,0,0,0.3)]'>
            Preserve your existing OpenAI-style integration and shift routing, billing, and operations into a unified control layer.
          </p>
          <div className='mt-9 flex flex-col items-center justify-center gap-3 sm:flex-row'>
            <Link to={primaryTarget} className='inline-flex h-[40px] items-center justify-center rounded-[10px] bg-white px-7 text-[15px] font-semibold text-[#102e24] transition-colors hover:bg-[#eef2ee]'>
              {isAuthenticated ? 'Open console' : 'Create account'}
            </Link>
            {isAuthenticated ? (
              <Link to={secondaryTarget} className='inline-flex h-[40px] items-center justify-center rounded-[10px] border border-white/40 px-7 text-[15px] font-medium text-white backdrop-blur-sm transition-colors hover:border-white/70'>
                View dashboard
              </Link>
            ) : (
              <a href={secondaryTarget} target='_blank' rel='noreferrer' className='inline-flex h-[40px] items-center justify-center rounded-[10px] border border-white/40 px-7 text-[15px] font-medium text-white backdrop-blur-sm transition-colors hover:border-white/70'>
                Read docs
              </a>
            )}
          </div>
        </div>
      </div>
    </section>
  );
}

function LandingFooter({ docsUrl, siteName }) {
  const currentYear = new Date().getFullYear();
  const columns = useMemo(() => FOOTER_COLUMNS(docsUrl), [docsUrl]);
  return (
    <footer className='mx-[10px] mb-[10px] rounded-[16px] bg-[#102e24] text-[#eef2ee]'>
      <div className='mx-auto max-w-[1420px] px-[24px] pb-[30px] pt-[60px] md:px-[50px] md:pt-[80px]'>
        <div className='grid grid-cols-2 gap-x-[24px] gap-y-[40px] md:grid-cols-5'>
          <div className='col-span-2'>
            <div className='flex items-center gap-[10px]'>
              <img src='/n123-logo.svg' alt='N123' className='h-[26px] w-auto' />
              <span className='text-[19px] font-semibold'>N123</span>
            </div>
            <p className='mt-[16px] max-w-[280px] text-[14px] leading-[1.7] text-[#eef2ee]/65'>
              Unified model access for AI agents. Keep protected project identifiers intact while presenting a single endpoint, transparent operations, and production-ready routing.
            </p>
          </div>
          {columns.map((col) => (
            <div key={col.title}>
              <p className='mb-[22px] text-[12px] font-medium text-[#eef2ee]/55'>{col.title}</p>
              <ul className='flex flex-col gap-[16px]'>
                {col.links.map((link) => (
                  <li key={link.label}>
                    <NavTarget link={link} className='text-[15px] font-medium text-[#eef2ee]/85 transition-colors hover:text-white'>
                      {link.label}
                    </NavTarget>
                  </li>
                ))}
              </ul>
            </div>
          ))}
        </div>
        <div className='mt-[50px] h-px w-full bg-[#eef2ee]/10 md:mt-[70px]' />
        <div className='mt-[24px] flex flex-col items-center justify-between gap-[16px] md:flex-row'>
          <p className='text-[13px] text-[#eef2ee]/50'>Copyright © {currentYear} {siteName}.</p>
          <div className='flex items-center gap-[12px]'>
            {[
              { label: 'GitHub', href: 'https://github.com/QuantumNous/new-api', icon: <IconGithubLogo /> },
              { label: 'Email', href: 'mailto:support@quantumnous.com', icon: <span>@</span> },
            ].map((social) => (
              <a key={social.label} href={social.href} target={social.href.startsWith('mailto:') ? undefined : '_blank'} rel={social.href.startsWith('mailto:') ? undefined : 'noreferrer'} aria-label={social.label} className='flex h-[34px] w-[34px] items-center justify-center rounded-full bg-[#eef2ee]/10 text-[#eef2ee]/85 transition-colors hover:bg-[#eef2ee]/20 hover:text-white'>
                {social.icon}
              </a>
            ))}
          </div>
        </div>
      </div>
    </footer>
  );
}

function LandingPage({ docsUrl, isAuthenticated, siteName }) {
  return (
    <>
      <ScrollProgress />
      <LandingNavbar docsUrl={docsUrl} isAuthenticated={isAuthenticated} />
      <main id='top'>
        <div className='flex min-h-svh flex-col'>
          <HeroSection docsUrl={docsUrl} isAuthenticated={isAuthenticated} />
          <ProviderSection />
        </div>
        <GlanceSection />
        <FeaturesSection />
        <MapCtaSection />
        <WhyChooseSection />
        <CtaSection docsUrl={docsUrl} isAuthenticated={isAuthenticated} />
      </main>
      <LandingFooter docsUrl={docsUrl} siteName={siteName} />
    </>
  );
}

const Home = () => {
  const { t, i18n } = useTranslation();
  const [statusState] = useContext(StatusContext);
  const actualTheme = useActualTheme();
  const [homePageContentLoaded, setHomePageContentLoaded] = useState(false);
  const [homePageContent, setHomePageContent] = useState('');
  const [noticeVisible, setNoticeVisible] = useState(false);
  const isMobile = useIsMobile();
  const location = useLocation();
  const isDemoSiteMode = statusState?.status?.demo_site_enabled || false;
  const docsLink = statusState?.status?.docs_link || 'https://docs.newapi.pro';
  const serverAddress = statusState?.status?.server_address || `${window.location.origin}`;
  const endpointItems = API_ENDPOINTS.map((e) => ({ value: e }));
  const [endpointIndex, setEndpointIndex] = useState(0);
  const isChinese = i18n.language.startsWith('zh');

  const displayHomePageContent = async () => {
    setHomePageContent(localStorage.getItem('home_page_content') || '');
    const res = await API.get('/api/home_page_content');
    const { success, message, data } = res.data;
    if (success) {
      let content = data;
      if (!data.startsWith('https://')) {
        content = marked.parse(data);
      }
      setHomePageContent(content);
      localStorage.setItem('home_page_content', content);
      if (data.startsWith('https://')) {
        const iframe = document.querySelector('iframe');
        if (iframe) {
          iframe.onload = () => {
            iframe.contentWindow.postMessage({ themeMode: actualTheme }, '*');
            iframe.contentWindow.postMessage({ lang: i18n.language }, '*');
          };
        }
      }
    } else {
      showError(message);
      setHomePageContent('Loading home page content failed...');
    }
    setHomePageContentLoaded(true);
  };

  const handleCopyBaseURL = async () => {
    const ok = await copy(serverAddress);
    if (ok) {
      showSuccess(t('Copied to clipboard'));
    }
  };

  useEffect(() => {
    const checkNoticeAndShow = async () => {
      const lastCloseDate = localStorage.getItem('notice_close_date');
      const today = new Date().toDateString();
      if (lastCloseDate !== today) {
        try {
          const res = await API.get('/api/notice');
          const { success, data } = res.data;
          if (success && data && data.trim() !== '') {
            setNoticeVisible(true);
          }
        } catch (error) {
          console.error('Failed to load notice:', error);
        }
      }
    };
    checkNoticeAndShow();
  }, []);

  useEffect(() => {
    displayHomePageContent().then();
  }, []);

  useEffect(() => {
    const timer = setInterval(() => {
      setEndpointIndex((prev) => (prev + 1) % endpointItems.length);
    }, 3000);
    return () => clearInterval(timer);
  }, [endpointItems.length]);

  useEffect(() => {
    document.body.classList.add('landing-home-page');
    return () => document.body.classList.remove('landing-home-page');
  }, []);

  if (!homePageContentLoaded) {
    return <div className='min-h-screen bg-[#fbfbf9]' />;
  }

  if (homePageContent) {
    return (
      <div className='overflow-x-hidden w-full'>
        {homePageContent.startsWith('https://') ? (
          <iframe src={homePageContent} className='w-full h-screen border-none' title='Custom Home Page' />
        ) : (
          <div className='mt-[60px]' dangerouslySetInnerHTML={{ __html: homePageContent }} />
        )}
      </div>
    );
  }

  const isAuthenticated = !!localStorage.getItem('user');
  const siteName = getSystemName() || 'New API';

  return (
    <div className={cn('w-full overflow-x-hidden', isChinese ? 'font-[system-ui]' : '')}>
      <NoticeModal visible={noticeVisible} onClose={() => setNoticeVisible(false)} isMobile={isMobile} />
      <LandingPage docsUrl={docsLink} isAuthenticated={isAuthenticated} siteName={siteName} />
    </div>
  );
};

export default Home;

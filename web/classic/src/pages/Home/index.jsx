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

import React, { useContext, useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { marked } from 'marked';
import { ArrowDown, ArrowRight, ArrowUpRight, Boxes, ChevronRight, Code2, Zap } from 'lucide-react';
import { API } from '../../helpers';
import { StatusContext } from '../../context/Status';
import { useActualTheme } from '../../context/Theme';
import NoticeModal from '../../components/layout/NoticeModal';
import FigmaHomeHeader from '../../components/layout/FigmaHomeHeader';
import {
  BRAND_NAME,
  HeroArt,
  HOME_CONSOLE_PATH,
  HOME_DOCS_URL,
} from '../../components/layout/figmaHomeShared';
import LogoLoading from '../../components/common/ui/LogoLoading';
import { useMinimumLoadingTime } from '../../hooks/common/useMinimumLoadingTime';

const normalizeNoticeContent = (content) => {
  if (content === undefined || content === null) return '';
  const text = String(content);
  const compactText = text.trim().replace(/\s+/g, '');
  const emptyQuotedValues = new Set(['""', "''", '“”', '‘’']);
  return emptyQuotedValues.has(compactText) ? '' : text;
};

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
];

const scrollToCapabilities = () => {
  document
    .getElementById('jl-capabilities')
    ?.scrollIntoView({ behavior: 'smooth', block: 'start' });
};

const Home = () => {
  const { t, i18n } = useTranslation();
  const [statusState] = useContext(StatusContext);
  const actualTheme = useActualTheme();
  const [homePageContentLoaded, setHomePageContentLoaded] = useState(false);
  const [homePageContent, setHomePageContent] = useState('');
  const [noticeVisible, setNoticeVisible] = useState(false);

  const customHomeContent = statusState?.status?.home_page_content || '';

  const promoEnabled = statusState?.status?.home_promo_enabled !== false;
  const promoTextZh = statusState?.status?.home_promo_text_zh;
  const promoTextEn = statusState?.status?.home_promo_text_en;
  const isEnglish = (i18n.language || '').toLowerCase().startsWith('en');
  const fallbackText = t('限时：1:1 充值赠送，最高可获 {{$100}} 免费额度！');
  const promoTextRaw = isEnglish
    ? promoTextEn || promoTextZh || fallbackText
    : promoTextZh || promoTextEn || fallbackText;
  const promoLink = statusState?.status?.home_promo_link || HOME_CONSOLE_PATH;
  const promoIsExternal = /^https?:\/\//i.test(promoLink);

  const promoSegments = React.useMemo(() => {
    if (!promoTextRaw) return [];
    const segments = [];
    const regex = /\{\{([\s\S]+?)\}\}/g;
    let lastIndex = 0;
    let match;
    while ((match = regex.exec(promoTextRaw)) !== null) {
      if (match.index > lastIndex) {
        segments.push({
          highlight: false,
          text: promoTextRaw.slice(lastIndex, match.index),
        });
      }
      segments.push({ highlight: true, text: match[1] });
      lastIndex = regex.lastIndex;
    }
    if (lastIndex < promoTextRaw.length) {
      segments.push({ highlight: false, text: promoTextRaw.slice(lastIndex) });
    }
    return segments;
  }, [promoTextRaw]);

  const showPromo = promoEnabled && promoTextRaw && promoTextRaw.trim() !== '';

  const showHomeLoading = useMinimumLoadingTime(!homePageContentLoaded, 800);

  useEffect(() => {
    if (customHomeContent) {
      const parsedContent = customHomeContent.startsWith('https://')
        ? customHomeContent
        : marked.parse(customHomeContent);
      setHomePageContent(parsedContent);
      localStorage.setItem('home_page_content', parsedContent);
    } else {
      setHomePageContent('');
      localStorage.setItem('home_page_content', '');
    }
    setHomePageContentLoaded(true);
  }, [customHomeContent]);

  useEffect(() => {
    const checkNoticeAndShow = async () => {
      const lastCloseDate = localStorage.getItem('notice_close_date');
      const today = new Date().toDateString();
      if (lastCloseDate === today) return;

      try {
        const res = await API.get('/api/notice');
        const { success, data } = res.data;
        const noticeContent = normalizeNoticeContent(data);
        if (success && noticeContent.trim() !== '') {
          setNoticeVisible(true);
        }
      } catch (error) {
        console.error('failed to load notice:', error);
      }
    };

    checkNoticeAndShow();
  }, []);

  useEffect(() => {
    if (!homePageContent.startsWith('https://')) return;
    const iframe = document.querySelector('.figma-home-custom-frame');
    if (!iframe) return;

    iframe.onload = () => {
      iframe.contentWindow.postMessage({ themeMode: actualTheme }, '*');
      iframe.contentWindow.postMessage({ lang: i18n.language }, '*');
    };
  }, [actualTheme, homePageContent, i18n.language]);

  // Scroll-reveal entrance for landing sections.
  useEffect(() => {
    if (showHomeLoading || homePageContent !== '') return undefined;
    const reveals = Array.from(document.querySelectorAll('.figma-home .jl-reveal'));
    if (!reveals.length) return undefined;

    const prefersReduced = window.matchMedia?.(
      '(prefers-reduced-motion: reduce)',
    ).matches;
    if (prefersReduced || typeof IntersectionObserver === 'undefined') {
      reveals.forEach((el) => el.classList.add('is-visible'));
      return undefined;
    }

    const observer = new IntersectionObserver(
      (entries) => {
        entries.forEach((entry) => {
          if (entry.isIntersecting) {
            entry.target.classList.add('is-visible');
            observer.unobserve(entry.target);
          }
        });
      },
      { threshold: 0.14, rootMargin: '0px 0px -8% 0px' },
    );
    reveals.forEach((el) => observer.observe(el));
    return () => observer.disconnect();
  }, [showHomeLoading, homePageContent]);

  // Pointer-driven 3D tilt on the hero artwork (desktop, motion-allowed only).
  useEffect(() => {
    if (showHomeLoading || homePageContent !== '') return undefined;
    const hero = document.querySelector('.jl-hero');
    const tilt = document.querySelector('.jl-hero-tilt');
    if (!hero || !tilt) return undefined;

    const finePointer = window.matchMedia?.('(pointer: fine)').matches;
    const prefersReduced = window.matchMedia?.(
      '(prefers-reduced-motion: reduce)',
    ).matches;
    if (!finePointer || prefersReduced) return undefined;

    let frame = 0;
    const onMove = (event) => {
      const rect = hero.getBoundingClientRect();
      const px = (event.clientX - rect.left) / rect.width - 0.5;
      const py = (event.clientY - rect.top) / rect.height - 0.5;
      if (frame) cancelAnimationFrame(frame);
      frame = requestAnimationFrame(() => {
        tilt.style.setProperty('--jl-ry', `${(px * 16).toFixed(2)}deg`);
        tilt.style.setProperty('--jl-rx', `${(-py * 13).toFixed(2)}deg`);
      });
    };
    const onLeave = () => {
      if (frame) cancelAnimationFrame(frame);
      tilt.style.setProperty('--jl-ry', '0deg');
      tilt.style.setProperty('--jl-rx', '0deg');
    };

    hero.addEventListener('pointermove', onMove);
    hero.addEventListener('pointerleave', onLeave);
    return () => {
      hero.removeEventListener('pointermove', onMove);
      hero.removeEventListener('pointerleave', onLeave);
      if (frame) cancelAnimationFrame(frame);
    };
  }, [showHomeLoading, homePageContent]);

  // PPT-style full-screen flip between screen 1 (hero) and screen 2 (capabilities).
  // Everything from screen 2 onward scrolls normally. Desktop wheel, motion-allowed only.
  useEffect(() => {
    if (showHomeLoading || homePageContent !== '') return undefined;
    const finePointer = window.matchMedia?.('(pointer: fine)').matches;
    const prefersReduced = window.matchMedia?.(
      '(prefers-reduced-motion: reduce)',
    ).matches;
    if (!finePointer || prefersReduced) return undefined;

    let animating = false;
    let timer = 0;
    const animateTo = (top) => {
      animating = true;
      window.scrollTo({ top, behavior: 'smooth' });
      window.clearTimeout(timer);
      timer = window.setTimeout(() => {
        // Land exactly on target so a short/undershot smooth scroll can't
        // leave us mid-way (which would re-trigger the flip and trap scroll 2).
        window.scrollTo({ top });
        animating = false;
      }, 820);
    };

    const onWheel = (event) => {
      const caps = document.getElementById('jl-capabilities');
      if (!caps) return;
      if (animating) {
        event.preventDefault();
        return;
      }
      const y = window.scrollY;
      // Absolute Y where screen 2 starts (stable ≈ hero height).
      const capsAbsTop = Math.round(y + caps.getBoundingClientRect().top);
      const TOL = 6;
      if (event.deltaY > 0 && y < capsAbsTop - TOL) {
        // Screen 1, scrolling down -> flip to screen 2.
        event.preventDefault();
        animateTo(capsAbsTop);
      } else if (event.deltaY < 0 && y > TOL && y < capsAbsTop + TOL) {
        // Near the screen 1/2 boundary, scrolling up -> flip back to screen 1.
        event.preventDefault();
        animateTo(0);
      }
      // Otherwise (screen 2 content and below): let the browser scroll normally.
    };

    window.addEventListener('wheel', onWheel, { passive: false });
    return () => {
      window.removeEventListener('wheel', onWheel);
      window.clearTimeout(timer);
    };
  }, [showHomeLoading, homePageContent]);

  if (showHomeLoading) {
    return <LogoLoading className='home-logo-loading' />;
  }

  if (homePageContent !== '') {
    return (
      <>
        <NoticeModal
          visible={noticeVisible}
          onClose={() => setNoticeVisible(false)}
        />
        {homePageContent.startsWith('https://') ? (
          <iframe
            src={homePageContent}
            title='Home Page Content'
            className='figma-home-custom-frame'
          />
        ) : (
          <div
            className='figma-home-custom-content markdown-body'
            dangerouslySetInnerHTML={{ __html: homePageContent }}
          />
        )}
      </>
    );
  }

  return (
    <>
      <NoticeModal
        visible={noticeVisible}
        onClose={() => setNoticeVisible(false)}
      />
      <main className='figma-home' style={{ '--jl-logo': 'url(/logo.png)' }}>
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
                      ),
                    )}
                  </span>
                  <ChevronRight size={16} />
                </a>
              ) : (
                <Link to={promoLink} className='jl-promo'>
                  <span>
                    {promoSegments.map((seg, idx) =>
                      seg.highlight ? (
                        <span key={idx} className='jl-promo-highlight'>
                          {seg.text}
                        </span>
                      ) : (
                        <React.Fragment key={idx}>{seg.text}</React.Fragment>
                      ),
                    )}
                  </span>
                  <ChevronRight size={16} />
                </Link>
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
              <Link to={HOME_CONSOLE_PATH} className='jl-btn jl-btn-dark'>
                {t('立即开始')}
                <ArrowRight size={17} className='jl-arrow' />
              </Link>
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
              const Icon = card.icon;
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
              );
            })}
          </div>
        </section>

        <section id='jl-cta' className='jl-cta-wrap'>
          <div className='jl-cta jl-reveal'>
            <div className='jl-cta-globe' aria-hidden='true' />
            <h2 className='jl-cta-title'>{t('开始连接全球 AI')}</h2>
            <Link to={HOME_CONSOLE_PATH} className='jl-btn jl-btn-dark'>
              {t('立即开始')}
              <ArrowRight size={17} className='jl-arrow' />
            </Link>
          </div>
        </section>
      </main>
    </>
  );
};

export default Home;

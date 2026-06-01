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

import React, { useContext, useEffect, useRef, useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { marked } from 'marked';
import { ArrowRight, ChevronRight } from 'lucide-react';
import { API } from '../../helpers';
import { StatusContext } from '../../context/Status';
import { useActualTheme } from '../../context/Theme';
import NoticeModal from '../../components/layout/NoticeModal';
import FigmaHomeHeader, {
  LogoMark,
} from '../../components/layout/FigmaHomeHeader';
import LogoLoading from '../../components/common/ui/LogoLoading';
import { useMinimumLoadingTime } from '../../hooks/common/useMinimumLoadingTime';
import { setEmbeddedInitialPrompt } from '../../app/utils/embedded';
import featureGlobalAccess from '../../assets/home/Global Model Access.png';
import featureStableFast from '../../assets/home/Stable & Fast.png';
import featureSecureReliable from '../../assets/home/Secure & Reliable.png';
import heroGlobeImage from '../../assets/home/image 6.png';
import heroOrbitImage from '../../assets/home/Ellipse 6.png';
import homeMapBg from '../../assets/home/home_mapbg.png';
import homeOrbitDot from '../../assets/home/Frame 37.png';
import homeSearchIcon from '../../assets/home/home_icon_search.png';
import statModelsIcon from '../../assets/home/home_page01_icon_01.png';
import statRegionsIcon from '../../assets/home/home_page01_icon_02.png';
import statUptimeIcon from '../../assets/home/home_page01_icon_03.png';
import statDevelopersIcon from '../../assets/home/home_page01_icon_04.png';
import routeLineOne from '../../assets/home/Vector 1.png';
import routeLineTwo from '../../assets/home/Vector 2.png';
import routeLineFour from '../../assets/home/Vector 4.png';
import routeLineFive from '../../assets/home/Vector 5.png';
import routeLineSix from '../../assets/home/Vector 6.png';
import routeLineSeven from '../../assets/home/Vector 7.png';

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
];

const normalizeNoticeContent = (content) => {
  if (content === undefined || content === null) return '';
  const text = String(content);
  const compactText = text.trim().replace(/\s+/g, '');
  const emptyQuotedValues = new Set(['""', "''", '“”', '‘’']);
  return emptyQuotedValues.has(compactText) ? '' : text;
};

const mapDots = [
  'figma-home-map-dot-1',
  'figma-home-map-dot-2',
  'figma-home-map-dot-3',
  'figma-home-map-dot-4',
  'figma-home-map-dot-5',
  'figma-home-map-dot-6',
  'figma-home-map-dot-7',
  'figma-home-map-dot-8',
];

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
];

const stats = [
  { value: '200+', label: '可接入模型', icon: statModelsIcon },
  { value: '50+', label: '覆盖国家 / 地区', icon: statRegionsIcon },
  { value: '99.9%', label: '可用性保障', icon: statUptimeIcon },
  { value: '100K+', label: '开发者信赖', icon: statDevelopersIcon },
];

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
);

const GlobalMap = () => (
  <div className='figma-home-map' aria-hidden='true'>
    <img src={homeMapBg} alt='' />
  </div>
);

const TypewriterTitle = ({ text }) => {
  const characters = React.useMemo(() => Array.from(text), [text]);
  const [visibleLength, setVisibleLength] = useState(0);
  const [isDeleting, setIsDeleting] = useState(false);

  useEffect(() => {
    setVisibleLength(0);
    setIsDeleting(false);
  }, [text]);

  useEffect(() => {
    if (!characters.length) return undefined;

    const prefersReducedMotion =
      typeof window !== 'undefined' &&
      window.matchMedia?.('(prefers-reduced-motion: reduce)').matches;

    if (prefersReducedMotion) {
      setVisibleLength(characters.length);
      return undefined;
    }

    let delay = isDeleting ? 45 : 90;

    if (!isDeleting && visibleLength === characters.length) {
      delay = 1400;
    } else if (isDeleting && visibleLength === 0) {
      delay = 500;
    }

    const timer = window.setTimeout(() => {
      if (!isDeleting && visibleLength === characters.length) {
        setIsDeleting(true);
        return;
      }

      if (isDeleting && visibleLength === 0) {
        setIsDeleting(false);
        return;
      }

      setVisibleLength((currentLength) =>
        isDeleting
          ? Math.max(currentLength - 1, 0)
          : Math.min(currentLength + 1, characters.length),
      );
    }, delay);

    return () => window.clearTimeout(timer);
  }, [characters, isDeleting, visibleLength]);

  return (
    <span className='figma-home-typewriter' aria-hidden='true'>
      <span className='figma-home-typewriter-text'>
        {characters.slice(0, visibleLength).join('')}
      </span>
    </span>
  );
};

const Home = () => {
  const { t, i18n } = useTranslation();
  const navigate = useNavigate();
  const [statusState] = useContext(StatusContext);
  const actualTheme = useActualTheme();
  const [homePageContentLoaded, setHomePageContentLoaded] = useState(false);
  const [homePageContent, setHomePageContent] = useState('');
  const [noticeVisible, setNoticeVisible] = useState(false);
  const [heroPrompt, setHeroPrompt] = useState('');
  const [isRoutingActive, setIsRoutingActive] = useState(false);
  const heroSectionRef = useRef(null);
  const featureSectionRef = useRef(null);
  const routingSectionRef = useRef(null);
  const heroSearchRef = useRef(null);
  const heroSearchInputRef = useRef(null);

  const getStartedPath = '/console';
  const customHomeContent = statusState?.status?.home_page_content || '';
  const heroTitle = t('一个 API 接入所有 LLM');

  const promoEnabled = statusState?.status?.home_promo_enabled !== false;
  const promoTextZh = statusState?.status?.home_promo_text_zh;
  const promoTextEn = statusState?.status?.home_promo_text_en;
  const isEnglish = (i18n.language || '').toLowerCase().startsWith('en');
  const fallbackText =
    '限时：1:1 充值赠送，最高可获 {{$100}} 免费额度！';
  const promoTextRaw = isEnglish
    ? promoTextEn || promoTextZh || fallbackText
    : promoTextZh || promoTextEn || fallbackText;
  const promoLink = statusState?.status?.home_promo_link || getStartedPath;
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
      segments.push({
        highlight: false,
        text: promoTextRaw.slice(lastIndex),
      });
    }
    return segments;
  }, [promoTextRaw]);

  const showPromo =
    promoEnabled && promoTextRaw && promoTextRaw.trim() !== '';

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

  useEffect(() => {
    if (showHomeLoading || !homePageContentLoaded || homePageContent !== '') {
      return undefined;
    }

    const handleOutsidePointerDown = (event) => {
      const searchContainer = heroSearchRef.current;
      const searchInput = heroSearchInputRef.current;

      if (
        !searchContainer ||
        !searchInput ||
        document.activeElement !== searchInput ||
        searchContainer.contains(event.target)
      ) {
        return;
      }

      searchInput.blur();
    };

    document.addEventListener('pointerdown', handleOutsidePointerDown, true);

    return () => {
      document.removeEventListener(
        'pointerdown',
        handleOutsidePointerDown,
        true,
      );
    };
  }, [homePageContent, homePageContentLoaded, showHomeLoading]);

  useEffect(() => {
    if (showHomeLoading || !homePageContentLoaded || homePageContent !== '') {
      return undefined;
    }

    const routingSection = routingSectionRef.current;
    if (!routingSection) return undefined;

    let triggered = false;
    const activate = () => {
      if (triggered) return;
      triggered = true;
      setIsRoutingActive(true);
    };

    // Decide whether the section's top has scrolled into the visible area.
    const isInView = () => {
      const rect = routingSection.getBoundingClientRect();
      const viewportH =
        window.innerHeight || document.documentElement.clientHeight;
      // Trigger as soon as the top edge of the section is within the viewport
      // (with a small buffer so it kicks in slightly before fully visible).
      return rect.top < viewportH - 80 && rect.bottom > 0;
    };

    // 1) IntersectionObserver against the viewport — primary path
    const observer = new IntersectionObserver(
      ([entry]) => {
        if (entry.isIntersecting) {
          activate();
          observer.disconnect();
        }
      },
      {
        root: null, // viewport, not .app-layout-scroll (that container may not scroll)
        threshold: 0.05,
        rootMargin: '0px 0px -10% 0px',
      },
    );
    observer.observe(routingSection);

    // 2) Scroll/resize listener — works even when the page scrolls inside a
    //    nested container that the IntersectionObserver root can't see.
    const scrollContainer = document.querySelector('.app-layout-scroll');
    const scrollTargets = [window, scrollContainer].filter(Boolean);

    const handleScroll = () => {
      if (isInView()) {
        activate();
        scrollTargets.forEach((t) =>
          t.removeEventListener('scroll', handleScroll),
        );
        window.removeEventListener('resize', handleScroll);
        observer.disconnect();
      }
    };

    scrollTargets.forEach((t) =>
      t.addEventListener('scroll', handleScroll, { passive: true }),
    );
    window.addEventListener('resize', handleScroll);

    // Initial check in case the section is already on screen at mount.
    handleScroll();

    return () => {
      observer.disconnect();
      scrollTargets.forEach((t) =>
        t.removeEventListener('scroll', handleScroll),
      );
      window.removeEventListener('resize', handleScroll);
    };
  }, [homePageContent, homePageContentLoaded, showHomeLoading]);

  const handleHeroSearchSubmit = (event) => {
    event?.preventDefault();
    const prompt = heroPrompt.trim();
    const chatPath = '/console/chat?tool=chat';

    if (!prompt) {
      navigate(chatPath);
      return;
    }

    setEmbeddedInitialPrompt(prompt);
    navigate(chatPath);
  };

  const handleHeroSearchKeyDown = (event) => {
    if (event.key !== 'Enter') return;
    handleHeroSearchSubmit(event);
  };

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
      <main className='figma-home'>
        <FigmaHomeHeader />

        <section ref={heroSectionRef} className='figma-home-hero'>
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
                      <span
                        key={idx}
                        className='figma-home-promo-highlight'
                      >
                        {seg.text}
                      </span>
                    ) : (
                      <React.Fragment key={idx}>{seg.text}</React.Fragment>
                    ),
                  )}
                </span>
                <ChevronRight size={24} />
              </a>
            ) : (
              <Link to={promoLink} className='figma-home-promo'>
                <span className='figma-home-promo-text'>
                  {promoSegments.map((seg, idx) =>
                    seg.highlight ? (
                      <span
                        key={idx}
                        className='figma-home-promo-highlight'
                      >
                        {seg.text}
                      </span>
                    ) : (
                      <React.Fragment key={idx}>{seg.text}</React.Fragment>
                    ),
                  )}
                </span>
                <ChevronRight size={24} />
              </Link>
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
            <img
              className='figma-home-search-icon'
              src={homeSearchIcon}
              alt=''
            />
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

        <section ref={featureSectionRef} className='figma-home-feature-section'>
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
              <span
                key={dotClass}
                className={`figma-home-map-dot ${dotClass}`}
              />
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

          <Link to={getStartedPath} className='figma-home-routing-cta'>
            {t('立即开始')}
            <ArrowRight size={18} />
          </Link>
        </section>
      </main>
    </>
  );
};

export default Home;

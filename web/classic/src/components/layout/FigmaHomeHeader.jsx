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

import React, { useCallback, useMemo, useState } from 'react';
import { Link } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { ChevronDown, Menu, X } from 'lucide-react';
import brandMark from '../../assets/home/Vector_b.png';
import {
  LANGUAGE_PREFERENCE_KEY,
  languageOptions,
  normalizeLanguage,
} from '../../i18n/language';

export const figmaHomeNavItems = [
  {
    label: 'LLM服务',
    to: '/console/chat?tool=chat',
    dropdown: true,
    children: [
      { label: '聊天', to: '/console/chat?tool=chat' },
      { label: '绘图', to: '/chat/image' },
      { label: '视频', to: '/console/chat?tool=video', hidden: true },
    ],
  },
  { label: '控制台', to: '/console' },
  { label: '模型广场', to: '/pricing' },
];

const getVisibleChildren = (children = []) =>
  children.filter((child) => !child.hidden);

const LogoMark = ({ className = '' }) => (
  <span className={`figma-home-logo ${className}`} aria-hidden='true'>
    <img src={brandMark} alt='' />
  </span>
);

const FigmaHomeHeader = () => {
  const { t, i18n } = useTranslation();
  const [mobileMenuOpen, setMobileMenuOpen] = useState(false);
  const [mobileExpandedMenu, setMobileExpandedMenu] = useState(null);

  const currentLanguage =
    languageOptions.find(
      (item) => normalizeLanguage(item.key) === normalizeLanguage(i18n.language),
    ) || languageOptions[0];
  const getStartedPath = '/console';

  const handleLanguageSelect = useCallback(
    (languageKey) => {
      const nextLanguage = normalizeLanguage(languageKey);
      setMobileMenuOpen(false);
      setMobileExpandedMenu(null);
      i18n.changeLanguage(nextLanguage);
      localStorage.setItem(LANGUAGE_PREFERENCE_KEY, nextLanguage);
    },
    [i18n],
  );

  const mobileMenuItems = useMemo(
    () => [
      ...figmaHomeNavItems,
      {
        label: '语言',
        value: currentLanguage.shortLabel,
        children: languageOptions.map((item) => ({
          ...item,
          active:
            normalizeLanguage(item.key) === normalizeLanguage(i18n.language),
        })),
      },
    ],
    [i18n.language],
  );

  const closeMobileMenu = () => {
    setMobileMenuOpen(false);
    setMobileExpandedMenu(null);
  };

  return (
    <>
      <header className='figma-home-header'>
        <Link to='/' className='figma-home-brand' aria-label={t('首页')}>
          <LogoMark />
        </Link>

        <Link to='/console' className='figma-home-mobile-console'>
          {t('控制台')}
        </Link>

        <nav className='figma-home-nav' aria-label={t('主导航')}>
          {figmaHomeNavItems.map((item) => {
            const visibleChildren = getVisibleChildren(item.children);
            const hasDesktopDropdown = item.dropdown || visibleChildren.length;

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
                        <Link key={child.label} to={child.to}>
                          {t(child.label)}
                        </Link>
                      ))}
                    </div>
                  </>
                ) : (
                  <Link to={item.to}>{t(item.label)}</Link>
                )}
              </div>
            );
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
          <Link to={getStartedPath} className='figma-home-header-cta'>
            {t('开始使用')}
          </Link>
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
        className={`figma-home-mobile-panel${mobileMenuOpen ? ' is-open' : ''}`}
      >
        <div className='figma-home-mobile-panel-top'>
          <img
            className='figma-home-mobile-logo-image'
            src={brandMark}
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
            const isExpanded = mobileExpandedMenu === item.label;
            const visibleChildren = getVisibleChildren(item.children);
            if (item.to && !visibleChildren.length) {
              return (
                <div key={item.label} className='figma-home-mobile-menu-item'>
                  <Link
                    to={item.to}
                    className='figma-home-mobile-link'
                    onClick={closeMobileMenu}
                  >
                    <span>{t(item.label)}</span>
                  </Link>
                </div>
              );
            }

            return (
              <div
                key={item.label}
                className={`figma-home-mobile-menu-item${
                  isExpanded ? ' is-expanded' : ''
                }`}
              >
                <button
                  type='button'
                  className='figma-home-mobile-link'
                  aria-expanded={isExpanded}
                  onClick={() =>
                    setMobileExpandedMenu((current) =>
                      current === item.label ? null : item.label,
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
                        <Link
                          key={child.label}
                          to={child.to}
                          onClick={closeMobileMenu}
                        >
                          {t(child.label)}
                        </Link>
                      ) : (
                        <button
                          key={child.key}
                          type='button'
                          className={child.active ? 'is-active' : ''}
                          onClick={() => handleLanguageSelect(child.key)}
                        >
                          {child.fullLabel}
                        </button>
                      ),
                    )}
                  </div>
                ) : null}
              </div>
            );
          })}
        </div>
      </div>
    </>
  );
};

export { LogoMark };
export default FigmaHomeHeader;

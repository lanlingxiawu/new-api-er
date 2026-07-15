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

import React, { useCallback, useContext, useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { Dropdown } from '@douyinfe/semi-ui';
import { ArrowRight, ChevronDown, Languages, Menu, X } from 'lucide-react';
import {
  HOME_CONSOLE_PATH,
  HOME_DOCS_URL,
  HOME_ABOUT_URL,
  BRAND_LOGO_SRC,
  BrandMark,
} from './figmaHomeShared';
import { API } from '../../helpers';
import { UserContext } from '../../context/User';
import { languageOptions, normalizeLanguage } from '../../i18n/language';

const scrollToCapabilities = (event) => {
  const target = document.getElementById('jl-capabilities');
  if (!target) return;
  event.preventDefault();
  target.scrollIntoView({ behavior: 'smooth', block: 'start' });
};

// Kept for backward-compatible imports elsewhere.
const LogoMark = ({ className = '' }) => (
  <img
    className={`jl-brand-logo ${className}`}
    src={BRAND_LOGO_SRC}
    alt=''
    aria-hidden='true'
  />
);

// Shared interface-language state for the landing header/drawer. Mirrors the
// in-app language switch: change immediately (i18next caches to localStorage)
// and best-effort persist to the signed-in user's profile.
function useHomeLanguage() {
  const { i18n } = useTranslation();
  const [userState] = useContext(UserContext);
  const currentLanguage = normalizeLanguage(i18n.language);

  const changeLanguage = useCallback(
    async (code) => {
      if (code === currentLanguage) return;
      const previous = normalizeLanguage(i18n.language);
      i18n.changeLanguage(code);
      localStorage.setItem('i18nextLng', code);
      if (userState?.user?.id) {
        try {
          await API.put('/api/user/self', { language: code });
        } catch (error) {
          // Roll back on failure so UI and persisted preference stay in sync.
          i18n.changeLanguage(previous);
          localStorage.setItem('i18nextLng', previous);
        }
      }
    },
    [i18n, currentLanguage, userState],
  );

  return { currentLanguage, changeLanguage };
}

// Desktop header language menu (replaces the old GitHub link).
function HomeLangSwitcher({ currentLanguage, changeLanguage }) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const currentLabel =
    languageOptions.find((lang) => lang.key === currentLanguage)?.fullLabel ??
    'Language';

  // Auto-close on scroll (outside-click and selection already close it).
  useEffect(() => {
    if (!open) return undefined;
    const close = () => setOpen(false);
    window.addEventListener('scroll', close, { passive: true });
    return () => window.removeEventListener('scroll', close);
  }, [open]);

  return (
    <Dropdown
      trigger='click'
      position='bottomRight'
      visible={open}
      onVisibleChange={setOpen}
      render={
        <Dropdown.Menu>
          {languageOptions.map((lang) => (
            <Dropdown.Item
              key={lang.key}
              active={currentLanguage === lang.key}
              onClick={() => {
                changeLanguage(lang.key);
                setOpen(false);
              }}
            >
              {lang.fullLabel}
            </Dropdown.Item>
          ))}
        </Dropdown.Menu>
      }
    >
      <button
        type='button'
        className='jl-lang'
        aria-expanded={open ? 'true' : 'false'}
        aria-label={t('切换语言')}
      >
        <Languages size={16} />
        <span>{currentLabel}</span>
        <ChevronDown size={14} className='jl-lang-caret' />
      </button>
    </Dropdown>
  );
}

const FigmaHomeHeader = () => {
  const { t } = useTranslation();
  const { currentLanguage, changeLanguage } = useHomeLanguage();
  const [scrolled, setScrolled] = useState(false);
  const [drawerOpen, setDrawerOpen] = useState(false);

  useEffect(() => {
    const onScroll = () => setScrolled(window.scrollY > 8);
    onScroll();
    window.addEventListener('scroll', onScroll, { passive: true });
    return () => window.removeEventListener('scroll', onScroll);
  }, []);

  useEffect(() => {
    document.body.style.overflow = drawerOpen ? 'hidden' : '';
    return () => {
      document.body.style.overflow = '';
    };
  }, [drawerOpen]);

  const closeDrawer = () => setDrawerOpen(false);

  const navLinks = (
    <>
      <Link to='/' className='is-active'>
        {t('首页')}
      </Link>
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
  );

  return (
    <>
      <header className={`jl-header${scrolled ? ' is-scrolled' : ''}`}>
        <Link to='/' className='jl-brand' aria-label={t('首页')}>
          <BrandMark />
        </Link>

        <nav className='jl-nav' aria-label={t('主导航')}>
          {navLinks}
        </nav>

        <div className='jl-header-actions'>
          <HomeLangSwitcher
            currentLanguage={currentLanguage}
            changeLanguage={changeLanguage}
          />
          <Link to={HOME_CONSOLE_PATH} className='jl-btn jl-btn-dark'>
            {t('控制台')}
            <ArrowRight size={16} className='jl-arrow' />
          </Link>
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
        <div
          className='jl-drawer-lang'
          role='group'
          aria-label={t('切换语言')}
        >
          {languageOptions.map((lang) => (
            <button
              key={lang.key}
              type='button'
              className={`jl-drawer-lang-btn${
                currentLanguage === lang.key ? ' is-active' : ''
              }`}
              onClick={() => {
                changeLanguage(lang.key);
                closeDrawer();
              }}
            >
              {lang.fullLabel}
            </button>
          ))}
        </div>
        <div className='jl-drawer-cta'>
          <Link
            to={HOME_CONSOLE_PATH}
            className='jl-btn jl-btn-dark'
            onClick={closeDrawer}
          >
            {t('控制台')}
            <ArrowRight size={16} className='jl-arrow' />
          </Link>
        </div>
      </div>
    </>
  );
};

export { LogoMark };
export default FigmaHomeHeader;

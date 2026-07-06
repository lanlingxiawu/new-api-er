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

import React, { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { ArrowRight, Menu, X } from 'lucide-react';
import {
  HOME_CONSOLE_PATH,
  HOME_DOCS_URL,
  HOME_ABOUT_URL,
  HOME_GITHUB_URL,
  BrandMark,
  GithubIcon,
} from './figmaHomeShared';

const scrollToCapabilities = (event) => {
  const target = document.getElementById('jl-capabilities');
  if (!target) return;
  event.preventDefault();
  target.scrollIntoView({ behavior: 'smooth', block: 'start' });
};

// Kept for backward-compatible imports elsewhere.
const LogoMark = ({ className = '' }) => (
  <span className={`jl-brand-badge ${className}`} aria-hidden='true'>
    <span className='jl-glyph' />
  </span>
);

const FigmaHomeHeader = () => {
  const { t } = useTranslation();
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
          <a
            className='jl-github'
            href={HOME_GITHUB_URL}
            target='_blank'
            rel='noopener noreferrer'
          >
            <GithubIcon size={17} />
            GitHub
          </a>
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
          <a
            href={HOME_GITHUB_URL}
            target='_blank'
            rel='noopener noreferrer'
          >
            GitHub
          </a>
        </nav>
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

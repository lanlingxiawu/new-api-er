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
import { Link } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { scrollDocumentToTop } from '../../helpers/scroll';
import { BrandMark, HOME_FOOTER_GROUPS } from './figmaHomeShared';

export const FigmaFooterLogo = () => (
  <span className='jl-brand'>
    <BrandMark />
  </span>
);

// Normalize both the backend footer_html shape ({titleKey, links:[{labelKey,url,target}]})
// and the built-in HOME_FOOTER_GROUPS shape ({title, links:[{label,href,internal,external}]}).
const normalizeGroup = (group, index) => ({
  id: group.id || `group-${index}`,
  title: group.title || group.titleKey || '',
  links: (group.links || []).map((link, linkIndex) => {
    const isPlain = !!link.plain;
    const url = link.href || link.url || '#';
    const isAnchor = !isPlain && url.startsWith('#');
    const isInternal =
      !isPlain &&
      !isAnchor &&
      (link.internal || (link.target === '_self' && url.startsWith('/')));
    return {
      id: link.id || `link-${linkIndex}`,
      label: link.label || link.labelKey || '',
      url,
      isAnchor,
      isInternal,
      isPlain,
    };
  }),
});

const scrollToAnchor = (event, url) => {
  const target = document.getElementById(url.slice(1));
  if (!target) return;
  event.preventDefault();
  target.scrollIntoView({ behavior: 'smooth', block: 'start' });
};

const FigmaFooter = ({
  footerConfig,
  contactGroup = null,
  copyrightText = '© 2026 巨量词元. All rights reserved.',
}) => {
  const { t } = useTranslation();
  const sourceGroups =
    Array.isArray(footerConfig?.groups) && footerConfig.groups.length
      ? footerConfig.groups
      : HOME_FOOTER_GROUPS;
  const allGroups = contactGroup
    ? [...sourceGroups, contactGroup]
    : sourceGroups;
  const groups = allGroups.map(normalizeGroup);

  return (
    <footer className='jl-footer' style={{ '--jl-logo': 'url(/logo.png)' }}>
      <div className='jl-footer-inner'>
        <div className='jl-footer-top'>
          <div className='jl-footer-brand'>
            <FigmaFooterLogo />
            <p className='jl-footer-tagline'>
              {t('连接全球 AI，让智能无处不在。')}
            </p>
            <div className='jl-footer-copy'>{copyrightText}</div>
          </div>

          {groups.map((group) => (
            <div key={group.id} className='jl-footer-col'>
              <h4>{t(group.title)}</h4>
              {group.links.map((link) => {
                const label = t(link.label);
                if (link.isPlain) {
                  return (
                    <span key={link.id} className='jl-footer-plain'>
                      {label}
                    </span>
                  );
                }
                if (link.isAnchor) {
                  return (
                    <a
                      key={link.id}
                      href={link.url}
                      onClick={(event) => scrollToAnchor(event, link.url)}
                    >
                      {label}
                    </a>
                  );
                }
                if (link.isInternal) {
                  return (
                    <Link
                      key={link.id}
                      to={link.url}
                      onClick={() =>
                        window.requestAnimationFrame(() =>
                          scrollDocumentToTop('auto'),
                        )
                      }
                    >
                      {label}
                    </Link>
                  );
                }
                return (
                  <a
                    key={link.id}
                    href={link.url}
                    target='_blank'
                    rel='noopener noreferrer'
                  >
                    {label}
                  </a>
                );
              })}
            </div>
          ))}
        </div>
      </div>
    </footer>
  );
};

export default FigmaFooter;

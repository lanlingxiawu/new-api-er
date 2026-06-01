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
import brandMark from '../../assets/home/Vector_b.png';

export const FigmaFooterLogo = ({ className = '' }) => (
  <span className={`figma-home-logo ${className}`} aria-hidden='true'>
    <img src={brandMark} alt='' />
  </span>
);

const FigmaFooter = ({
  footerConfig,
  copyrightText = 'Copyright ©2026 Nexaxis. All rights reserved.',
  bottomExtra = null,
}) => {
  const { t } = useTranslation();
  const groups = Array.isArray(footerConfig?.groups) ? footerConfig.groups : [];

  return (
    <footer className='figma-home-footer'>
      <div className='figma-home-footer-inner'>
        <FigmaFooterLogo className='figma-home-footer-logo' />

        <div className='figma-home-footer-columns'>
          {groups.map((group) => (
            <div key={group.id} className='figma-home-footer-column'>
              <h3>{t(group.titleKey)}</h3>
              {group.links.map((link) => {
                const label = t(link.labelKey);
                const content = label;
                const isInternal =
                  link.target === '_self' && link.url.startsWith('/');

                if (isInternal) {
                  const shouldResetScroll =
                    link.url === '/' ||
                    link.url === '/articles' ||
                    link.url.startsWith('/articles/');
                  return (
                    <Link
                      key={link.id}
                      to={link.url}
                      onClick={() => {
                        if (shouldResetScroll) {
                          window.requestAnimationFrame(() =>
                            scrollDocumentToTop('auto'),
                          );
                        }
                      }}
                    >
                      {content}
                    </Link>
                  );
                }

                return (
                  <a
                    key={link.id}
                    href={link.url}
                    target={link.target || '_blank'}
                    rel={
                      link.target === '_blank'
                        ? 'noopener noreferrer'
                        : undefined
                    }
                  >
                    {content}
                  </a>
                );
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
  );
};

export default FigmaFooter;
